package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/db"
	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/schedule"
)

// Webhook event types (ADR 0017). Stable strings: they are API values and the
// "type" of a payload. EventPing exists only for the test button.
const (
	EventChoreCompleted        = "chore.completed"
	EventChoreCompletionRevert = "chore.completion_reverted"
	EventChoreOverdue          = "chore.overdue"
	EventChoreCreated          = "chore.created"
	EventChoreUpdated          = "chore.updated"
	EventChoreDeleted          = "chore.deleted"
	EventChoreSnoozed          = "chore.snoozed"
	EventChoreSkipped          = "chore.skipped"
	EventPing                  = "ping"
)

// WebhookEvents lists the events a webhook can subscribe to.
var WebhookEvents = []string{EventChoreCompleted, EventChoreCompletionRevert, EventChoreOverdue, EventChoreCreated,
	EventChoreUpdated, EventChoreDeleted, EventChoreSnoozed, EventChoreSkipped}

// Webhook delivery tuning.
const (
	WebhookMaxFailures     = 20   // consecutive failed attempts that switch a webhook off
	WebhookLogSize         = 50   // delivery-log entries kept per webhook
	maxPendingWebhookQueue = 1000 // pending deliveries per webhook; further events are dropped
)

// WebhookPayloadVersion is the "version" field of every payload.
const WebhookPayloadVersion = 1

// WebhookPayload is the stable, versioned JSON body of a delivery. It carries
// no secrets and no email addresses.
type WebhookPayload struct {
	ID          string      `json:"id"`
	Type        string      `json:"type"`
	Version     int         `json:"version"`
	CreatedAt   string      `json:"createdAt"`
	HouseholdID string      `json:"householdId"`
	Data        WebhookData `json:"data"`
}

// WebhookData is the "data" object of a payload.
type WebhookData struct {
	Chore      *WebhookChore      `json:"chore,omitempty"`
	Completion *WebhookCompletion `json:"completion,omitempty"`
	Actor      *WebhookActor      `json:"actor,omitempty"`
}

// WebhookChore is the chore an event is about.
type WebhookChore struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	DueDate   string   `json:"dueDate"`
	Assignees []string `json:"assignees"`
}

// WebhookCompletion is the completion of a chore.completed or
// chore.completion_reverted event.
type WebhookCompletion struct {
	ID          string `json:"id"`
	CompletedAt string `json:"completedAt"`
	Source      string `json:"source"`
	RevertedAt  string `json:"revertedAt,omitempty"`
}

// WebhookActor is the member who caused an event (absent for the scheduler
// and for completions through a household channel's mark-done link).
type WebhookActor struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

// Webhook is a household webhook's metadata. The URL and secret are sealed and
// never returned by the listing methods.
type Webhook struct {
	ID             string
	HouseholdID    string
	Description    string
	URLHint        string // the host
	Events         []string
	Active         bool
	Failures       int       // consecutive failed attempts
	AutoDisabledAt time.Time // zero unless switched off after too many failures
	LastSuccessAt  time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// NewWebhook is a validated, sealed webhook (see webhook.Prepare): the ID is
// generated first because it is the ciphertexts' associated data.
type NewWebhook struct {
	ID               string
	Description      string
	URLCiphertext    string
	URLHint          string
	SecretCiphertext string
	Events           []string
	Max              int // limits.WebhooksPerHousehold, resolved by the caller
}

// WebhookUpdate changes a webhook; nil/empty fields are unchanged.
// URLCiphertext and URLHint are set together.
type WebhookUpdate struct {
	Description      *string
	Events           []string // nil = unchanged
	Active           *bool
	URLCiphertext    string
	URLHint          string
	SecretCiphertext string // a rotated secret
}

const webhookCols = `id, household_id, description, url_hint, events, active, failures, auto_disabled_at, last_success_at, created_at, updated_at`

func scanWebhook(row interface{ Scan(...any) error }) (Webhook, error) {
	var w Webhook
	var events string
	var disabled, success sql.NullInt64
	var created, updated int64
	if err := row.Scan(&w.ID, &w.HouseholdID, &w.Description, &w.URLHint, &events, &w.Active, &w.Failures,
		&disabled, &success, &created, &updated); err != nil {
		return Webhook{}, notFound(err)
	}
	if err := json.Unmarshal([]byte(events), &w.Events); err != nil {
		return Webhook{}, err
	}
	w.AutoDisabledAt, w.LastSuccessAt = fromMS(disabled), fromMS(success)
	w.CreatedAt, w.UpdatedAt = time.UnixMilli(created).UTC(), time.UnixMilli(updated).UTC()
	return w, nil
}

func validEvents(events []string) bool {
	if len(events) == 0 || len(events) > len(WebhookEvents) {
		return false
	}
	for i, e := range events {
		if !slices.Contains(WebhookEvents, e) || slices.Contains(events[:i], e) {
			return false
		}
	}
	return true
}

// ValidWebhookEvents reports whether events is a non-empty set of known,
// distinct webhook event types.
func ValidWebhookEvents(events []string) bool { return validEvents(events) }

// sortedEvents is the stored form: a JSON array in the order of WebhookEvents.
func sortedEvents(events []string) string {
	out := make([]string, 0, len(events))
	for _, e := range WebhookEvents {
		if slices.Contains(events, e) {
			out = append(out, e)
		}
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// CreateWebhook adds a webhook. Owner only. ErrLimit when the household has
// n.Max already.
func (s *Store) CreateWebhook(ctx context.Context, a Actor, householdID string, n NewWebhook) (Webhook, error) {
	if n.ID == "" || n.Max <= 0 || len(n.Description) > 200 || !validEvents(n.Events) {
		return Webhook{}, ErrInvalid
	}
	var w Webhook
	err := s.manage(ctx, a, householdID, RoleOwner, func(t *tx, _ Role) error { // household lock: the limit holds under concurrency
		var count int
		if err := t.queryRow(ctx, "SELECT count(*) FROM webhooks WHERE household_id = ?", householdID).Scan(&count); err != nil {
			return err
		}
		if count >= n.Max {
			return ErrLimit
		}
		now := s.nowMS()
		if _, err := t.exec(ctx, `INSERT INTO webhooks (id, household_id, description, url_ciphertext, url_hint, secret_ciphertext,
			events, active, failures, created_by, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, TRUE, 0, ?, ?, ?)`,
			n.ID, householdID, n.Description, n.URLCiphertext, n.URLHint, n.SecretCiphertext, sortedEvents(n.Events), a.UserID, now, now); err != nil {
			return err
		}
		var err error
		w, err = scanWebhook(t.queryRow(ctx, "SELECT "+webhookCols+" FROM webhooks WHERE id = ? AND household_id = ?", n.ID, householdID))
		return err
	})
	return w, err
}

// ListWebhooks lists the household's webhooks. Owner only.
func (s *Store) ListWebhooks(ctx context.Context, a Actor, householdID string) ([]Webhook, error) {
	var out []Webhook
	err := s.inTx(ctx, false, func(t *tx) error {
		if _, err := t.authorize(ctx, a, householdID, RoleOwner, false); err != nil {
			return err
		}
		rows, err := t.query(ctx, "SELECT "+webhookCols+" FROM webhooks WHERE household_id = ? ORDER BY created_at, id", householdID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			w, err := scanWebhook(rows)
			if err != nil {
				return err
			}
			out = append(out, w)
		}
		return rows.Err()
	})
	return out, err
}

// Webhook returns one webhook of the household. Owner only.
func (s *Store) Webhook(ctx context.Context, a Actor, householdID, id string) (Webhook, error) {
	var w Webhook
	err := s.inTx(ctx, false, func(t *tx) error {
		if _, err := t.authorize(ctx, a, householdID, RoleOwner, false); err != nil {
			return err
		}
		var err error
		w, err = scanWebhook(t.queryRow(ctx, "SELECT "+webhookCols+" FROM webhooks WHERE id = ? AND household_id = ?", id, householdID))
		return err
	})
	return w, err
}

// WebhookSealed returns what the test button needs to send: the sealed URL and
// secret. Owner only.
func (s *Store) WebhookSealed(ctx context.Context, a Actor, householdID, id string) (urlCT, secretCT string, err error) {
	err = s.inTx(ctx, false, func(t *tx) error {
		if _, err := t.authorize(ctx, a, householdID, RoleOwner, false); err != nil {
			return err
		}
		return notFound(t.queryRow(ctx, "SELECT url_ciphertext, secret_ciphertext FROM webhooks WHERE id = ? AND household_id = ?",
			id, householdID).Scan(&urlCT, &secretCT))
	})
	return urlCT, secretCT, err
}

// UpdateWebhook applies u. Owner only. Turning a webhook on clears its failure
// count and any automatic switch-off.
func (s *Store) UpdateWebhook(ctx context.Context, a Actor, householdID, id string, u WebhookUpdate) (Webhook, error) {
	if (u.Description != nil && len(*u.Description) > 200) || (u.Events != nil && !validEvents(u.Events)) ||
		(u.URLCiphertext == "") != (u.URLHint == "") {
		return Webhook{}, ErrInvalid
	}
	var w Webhook
	err := s.write(ctx, a, householdID, RoleOwner, func(t *tx, _ Role) error {
		now := s.nowMS()
		set := func(col string, v any) error {
			return t.execOne(ctx, "UPDATE webhooks SET "+col+" = ?, updated_at = ? WHERE id = ? AND household_id = ?", v, now, id, householdID)
		}
		var n int
		if err := t.queryRow(ctx, "SELECT count(*) FROM webhooks WHERE id = ? AND household_id = ?", id, householdID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		var err error
		if u.Description != nil {
			err = set("description", *u.Description)
		}
		if err == nil && u.Events != nil {
			err = set("events", sortedEvents(u.Events))
		}
		if err == nil && u.URLCiphertext != "" {
			if err = set("url_ciphertext", u.URLCiphertext); err == nil {
				err = set("url_hint", u.URLHint)
			}
		}
		if err == nil && u.SecretCiphertext != "" {
			err = set("secret_ciphertext", u.SecretCiphertext)
		}
		if err == nil && u.Active != nil {
			if *u.Active {
				err = t.execOne(ctx, `UPDATE webhooks SET active = TRUE, failures = 0, auto_disabled_at = NULL, updated_at = ?
					WHERE id = ? AND household_id = ?`, now, id, householdID)
			} else {
				err = set("active", false)
			}
		}
		if err != nil {
			return err
		}
		w, err = scanWebhook(t.queryRow(ctx, "SELECT "+webhookCols+" FROM webhooks WHERE id = ? AND household_id = ?", id, householdID))
		return err
	})
	return w, err
}

// DeleteWebhook removes a webhook with its delivery log. Owner only.
func (s *Store) DeleteWebhook(ctx context.Context, a Actor, householdID, id string) error {
	return s.write(ctx, a, householdID, RoleOwner, func(t *tx, _ Role) error {
		return t.execOne(ctx, "DELETE FROM webhooks WHERE id = ? AND household_id = ?", id, householdID)
	})
}

// WebhookDelivery is a delivery-log entry.
type WebhookDelivery struct {
	ID            string
	EventID       string
	EventType     string
	Status        string // pending, sent or failed
	Attempts      int
	HTTPStatus    int // of the last attempt; 0 = no response
	DurationMS    int
	Error         string // redacted
	CreatedAt     time.Time
	FinishedAt    time.Time
	NextAttemptAt time.Time
}

// WebhookDeliveries returns the webhook's delivery log: the newest
// WebhookLogSize entries, newest first. Owner only.
func (s *Store) WebhookDeliveries(ctx context.Context, a Actor, householdID, id string) ([]WebhookDelivery, error) {
	var out []WebhookDelivery
	err := s.inTx(ctx, false, func(t *tx) error {
		if _, err := t.authorize(ctx, a, householdID, RoleOwner, false); err != nil {
			return err
		}
		var n int
		if err := t.queryRow(ctx, "SELECT count(*) FROM webhooks WHERE id = ? AND household_id = ?", id, householdID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		rows, err := t.query(ctx, `SELECT id, event_id, event_type, status, attempts, http_status, duration_ms, last_error,
			created_at, finished_at, next_attempt_at FROM webhook_deliveries WHERE webhook_id = ?
			ORDER BY created_at DESC, id DESC LIMIT ?`, id, WebhookLogSize)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var d WebhookDelivery
			var created int64
			var fin, next sql.NullInt64
			if err := rows.Scan(&d.ID, &d.EventID, &d.EventType, &d.Status, &d.Attempts, &d.HTTPStatus, &d.DurationMS, &d.Error,
				&created, &fin, &next); err != nil {
				return err
			}
			d.CreatedAt, d.FinishedAt, d.NextAttemptAt = time.UnixMilli(created).UTC(), fromMS(fin), fromMS(next)
			out = append(out, d)
		}
		return rows.Err()
	})
	return out, err
}

// RecordWebhookTest logs the outcome of a test (ping) send, which is not
// retried and does not count towards the automatic switch-off. Owner only.
func (s *Store) RecordWebhookTest(ctx context.Context, a Actor, householdID, id, eventID string, r WebhookResult) (WebhookDelivery, error) {
	var d WebhookDelivery
	err := s.write(ctx, a, householdID, RoleOwner, func(t *tx, _ Role) error {
		var n int
		if err := t.queryRow(ctx, "SELECT count(*) FROM webhooks WHERE id = ? AND household_id = ?", id, householdID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		now, evID := s.nowMS(), eventID
		status := DeliveryFailed
		if r.OK {
			status = DeliverySent
			r.Error = ""
		}
		at := time.UnixMilli(now).UTC()
		d = WebhookDelivery{ID: ids.New(), EventID: evID, EventType: EventPing, Status: status, Attempts: 1, HTTPStatus: r.HTTPStatus,
			DurationMS: r.DurationMS, Error: r.Error, CreatedAt: at, FinishedAt: at}
		if _, err := t.exec(ctx, `INSERT INTO webhook_deliveries (id, delivery_key, webhook_id, household_id, event_id, event_type, payload, status,
			attempts, next_attempt_at, http_status, duration_ms, last_error, created_at, finished_at)
			VALUES (?, ?, ?, ?, ?, ?, '', ?, 1, NULL, ?, ?, ?, ?, ?)`,
			d.ID, id+":"+evID, id, householdID, evID, EventPing, status, r.HTTPStatus, r.DurationMS, r.Error, now, now); err != nil {
			return err
		}
		return t.pruneWebhookLog(ctx, id)
	})
	return d, err
}

func (t *tx) pruneWebhookLog(ctx context.Context, webhookID string) error {
	_, err := t.exec(ctx, `DELETE FROM webhook_deliveries WHERE id IN (
		SELECT id FROM webhook_deliveries WHERE webhook_id = ? AND status <> 'pending'
		ORDER BY created_at DESC, id DESC LIMIT 1000000000 OFFSET ?)`, webhookID, WebhookLogSize)
	return err
}

// --- Events (outbox) ---

func (t *tx) webhooksEnabled(ctx context.Context) (bool, error) {
	var v string
	err := t.queryRow(ctx, "SELECT value FROM settings WHERE key = ?", settingWebhooksEnabled).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return v == "true", err
}

// subscribers returns the IDs of the household's active webhooks subscribed to
// event; none while webhooks are off or nobody listens.
func (t *tx) subscribers(ctx context.Context, householdID, event string) ([]string, error) {
	on, err := t.webhooksEnabled(ctx)
	if err != nil || !on {
		return nil, err
	}
	rows, err := t.query(ctx, "SELECT id, events FROM webhooks WHERE household_id = ? AND active = TRUE ORDER BY id", householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var evs []string
		if json.Unmarshal([]byte(raw), &evs) == nil && slices.Contains(evs, event) {
			out = append(out, id)
		}
	}
	return out, rows.Err()
}

// webhookEvent is what an event carries.
type webhookEvent struct {
	eventID    string // "" = a fresh ID
	choreID    string
	chore      *Chore // already loaded (a chore about to be deleted); else loaded by emit
	completion *Completion
	actorID    string
}

// emitWebhook queues a delivery of event for every subscribed webhook of the
// household, in the caller's transaction: the event and its deliveries commit
// or roll back together. While webhooks are off it costs one settings read.
func (t *tx) emitWebhook(ctx context.Context, householdID, event string, ev webhookEvent, now int64) error {
	targets, err := t.subscribers(ctx, householdID, event)
	if err != nil || len(targets) == 0 {
		return err
	}
	c := ev.chore
	if c == nil {
		loaded, err := t.chore(ctx, householdID, ev.choreID)
		if err != nil {
			return err
		}
		c = &loaded
	}
	id := ev.eventID
	if id == "" {
		id = ids.New()
	}
	p := WebhookPayload{ID: id, Type: event, Version: WebhookPayloadVersion, CreatedAt: time.UnixMilli(now).UTC().Format(time.RFC3339),
		HouseholdID: householdID, Data: WebhookData{Chore: &WebhookChore{ID: c.ID, Name: c.Name, DueDate: c.DueDate, Assignees: append([]string{}, c.Assignees...)}}}
	if comp := ev.completion; comp != nil {
		p.Data.Completion = &WebhookCompletion{ID: comp.ID, CompletedAt: comp.CompletedAt.UTC().Format(time.RFC3339), Source: comp.Source}
		if !comp.RevertedAt.IsZero() {
			p.Data.Completion.RevertedAt = comp.RevertedAt.UTC().Format(time.RFC3339)
		}
	}
	if ev.actorID != "" {
		var name string
		if err := t.queryRow(ctx, "SELECT display_name FROM users WHERE id = ?", ev.actorID).Scan(&name); err != nil {
			return notFound(err)
		}
		p.Data.Actor = &WebhookActor{ID: ev.actorID, DisplayName: name}
	}
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	for _, wid := range targets {
		var pending, exists int
		if err := t.queryRow(ctx, "SELECT count(*) FROM webhook_deliveries WHERE webhook_id = ? AND status = 'pending'", wid).Scan(&pending); err != nil {
			return err
		}
		if pending >= maxPendingWebhookQueue {
			continue // a dead endpoint cannot grow the queue without bound
		}
		if err := t.queryRow(ctx, "SELECT count(*) FROM webhook_deliveries WHERE delivery_key = ?", wid+":"+id).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			continue // this event is already queued for it
		}
		if _, err := t.exec(ctx, `INSERT INTO webhook_deliveries (id, delivery_key, webhook_id, household_id, event_id, event_type, payload, status,
			attempts, next_attempt_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', 0, ?, ?)`,
			ids.New(), wid+":"+id, wid, householdID, id, event, string(body), now, now); err != nil {
			return err
		}
	}
	return nil
}

// EmitOverdueWebhooks queues chore.overdue events for the chores whose due
// date ended (local midnight after it, household zone) in [from, to). The
// event ID derives from the chore and its due date, so a repeated window, a
// restart or a second instance queues nothing twice. System use: the
// scheduler.
func (s *Store) EmitOverdueWebhooks(ctx context.Context, from, to time.Time) error {
	return s.inTx(ctx, true, func(t *tx) error {
		on, err := t.webhooksEnabled(ctx)
		if err != nil || !on {
			return err
		}
		type cand struct{ id, hh, due, tz string }
		var cands []cand
		err = func() error {
			rows, err := t.query(ctx, `SELECT c.id, c.household_id, c.due_date, h.timezone FROM chores c JOIN households h ON h.id = c.household_id
				WHERE c.archived_at IS NULL AND c.paused_indefinitely = FALSE AND c.next_due IS NOT NULL
				AND c.next_due >= ? AND c.next_due < ? AND (c.paused_until IS NULL OR c.paused_until <= ?)
				AND EXISTS (SELECT 1 FROM webhooks w WHERE w.household_id = c.household_id AND w.active = TRUE)
				ORDER BY c.id LIMIT 5000`, from.Add(-27*time.Hour).UnixMilli(), to.UnixMilli(), to.UnixMilli())
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var c cand
				if err := rows.Scan(&c.id, &c.hh, &c.due, &c.tz); err != nil {
					return err
				}
				cands = append(cands, c)
			}
			return rows.Err()
		}()
		if err != nil {
			return err
		}
		for _, c := range cands {
			d, perr := schedule.ParseDate(c.due)
			loc, lerr := time.LoadLocation(c.tz)
			if perr != nil || lerr != nil {
				continue
			}
			end := OccurrenceInstant(d.AddDays(1), loc)
			if end.Before(from) || !end.Before(to) {
				continue
			}
			id := ids.Hash("overdue|" + c.id + "|" + c.due)[:32]
			if err := t.emitWebhook(ctx, c.hh, EventChoreOverdue, webhookEvent{eventID: id, choreID: c.id}, to.UnixMilli()); err != nil {
				return err
			}
		}
		return nil
	})
}

// --- Sending (system use: the scheduler) ---

// ClaimedWebhookDelivery is a due delivery with what is needed to send it.
type ClaimedWebhookDelivery struct {
	ID               string
	WebhookID        string
	HouseholdID      string
	EventID          string
	EventType        string
	Payload          string
	Attempts         int
	URLCiphertext    string
	SecretCiphertext string
}

// ClaimWebhookDeliveries leases up to limit pending deliveries due at now: the
// next attempt moves to now+lease, so a crashed sender's claims are retried
// after the lease. Deliveries of a webhook that is off are dropped.
func (s *Store) ClaimWebhookDeliveries(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]ClaimedWebhookDelivery, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var out []ClaimedWebhookDelivery
	err := s.inTx(ctx, true, func(t *tx) error {
		out = nil
		q := `SELECT d.id, d.webhook_id, d.household_id, d.event_id, d.event_type, d.payload, d.attempts, w.url_ciphertext, w.secret_ciphertext, w.active
			FROM webhook_deliveries d JOIN webhooks w ON w.id = d.webhook_id
			WHERE d.status = 'pending' AND d.next_attempt_at <= ? ORDER BY d.next_attempt_at, d.id LIMIT ?`
		if t.d == db.Postgres {
			q += " FOR UPDATE OF d SKIP LOCKED"
		}
		type row struct {
			c      ClaimedWebhookDelivery
			active bool
		}
		var rs []row
		err := func() error {
			rows, err := t.query(ctx, q, now.UnixMilli(), limit)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var r row
				c := &r.c
				if err := rows.Scan(&c.ID, &c.WebhookID, &c.HouseholdID, &c.EventID, &c.EventType, &c.Payload, &c.Attempts,
					&c.URLCiphertext, &c.SecretCiphertext, &r.active); err != nil {
					return err
				}
				rs = append(rs, r)
			}
			return rows.Err()
		}()
		if err != nil {
			return err
		}
		for _, r := range rs {
			if !r.active {
				if _, err := t.exec(ctx, "DELETE FROM webhook_deliveries WHERE id = ? AND status = 'pending'", r.c.ID); err != nil {
					return err
				}
				continue
			}
			if _, err := t.exec(ctx, "UPDATE webhook_deliveries SET next_attempt_at = ? WHERE id = ?", now.Add(lease).UnixMilli(), r.c.ID); err != nil {
				return err
			}
			out = append(out, r.c)
		}
		return nil
	})
	return out, err
}

// DropPendingWebhookDeliveries deletes every pending delivery: webhooks were
// switched off instance-wide, so nothing is sent and stale events are not
// replayed if they come back on.
func (s *Store) DropPendingWebhookDeliveries(ctx context.Context) error {
	return s.inTx(ctx, true, func(t *tx) error {
		_, err := t.exec(ctx, "DELETE FROM webhook_deliveries WHERE status = 'pending'")
		return err
	})
}

// WebhookResult is the outcome of one send attempt.
type WebhookResult struct {
	OK         bool
	HTTPStatus int
	DurationMS int
	Error      string    // redacted
	RetryAt    time.Time // failed attempt: when to try again; zero = give up
}

// FinishWebhookDelivery records an attempt. On success the failure count
// resets; on failure it grows, and at WebhookMaxFailures the webhook is
// switched off (its other pending deliveries are failed) and an audit event is
// written. It reports whether this call switched the webhook off.
func (s *Store) FinishWebhookDelivery(ctx context.Context, id string, r WebhookResult) (disabled bool, err error) {
	err = s.inTx(ctx, true, func(t *tx) error {
		disabled = false
		now := s.nowMS()
		var webhookID, hh string
		if err := t.queryRow(ctx, "SELECT webhook_id, household_id FROM webhook_deliveries WHERE id = ? AND status = 'pending'", id).Scan(&webhookID, &hh); err != nil {
			return notFound(err)
		}
		if r.OK {
			if err := t.execOne(ctx, `UPDATE webhook_deliveries SET status = 'sent', attempts = attempts + 1, next_attempt_at = NULL,
				http_status = ?, duration_ms = ?, last_error = '', finished_at = ? WHERE id = ?`, r.HTTPStatus, r.DurationMS, now, id); err != nil {
				return err
			}
			if _, err := t.exec(ctx, "UPDATE webhooks SET failures = 0, last_success_at = ? WHERE id = ?", now, webhookID); err != nil {
				return err
			}
			return t.pruneWebhookLog(ctx, webhookID)
		}
		status, next, fin := DeliveryPending, ms(r.RetryAt), sql.NullInt64{}
		if r.RetryAt.IsZero() {
			status, fin = DeliveryFailed, sql.NullInt64{Int64: now, Valid: true}
		}
		if err := t.execOne(ctx, `UPDATE webhook_deliveries SET status = ?, attempts = attempts + 1, next_attempt_at = ?,
			http_status = ?, duration_ms = ?, last_error = ?, finished_at = ? WHERE id = ?`,
			status, next, r.HTTPStatus, r.DurationMS, r.Error, fin, id); err != nil {
			return err
		}
		if _, err := t.exec(ctx, "UPDATE webhooks SET failures = failures + 1 WHERE id = ?", webhookID); err != nil {
			return err
		}
		res, err := t.exec(ctx, `UPDATE webhooks SET active = FALSE, auto_disabled_at = ?, updated_at = ?
			WHERE id = ? AND active = TRUE AND failures >= ?`, now, now, webhookID, WebhookMaxFailures)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			disabled = true
			if _, err := t.exec(ctx, `UPDATE webhook_deliveries SET status = 'failed', next_attempt_at = NULL, last_error = 'webhook disabled', finished_at = ?
				WHERE webhook_id = ? AND status = 'pending'`, now, webhookID); err != nil {
				return err
			}
			if err := t.insertAudit(ctx, AuditEvent{HouseholdID: hh, Event: AuditWebhookAutoOff, Meta: map[string]string{"webhook": webhookID}}, now); err != nil {
				return err
			}
		}
		return t.pruneWebhookLog(ctx, webhookID)
	})
	return disabled, err
}

// --- Secrets (system use: gunkly rotate-key) ---

// WebhookURLSecrets pages through every webhook's sealed URL by ID.
func (s *Store) WebhookURLSecrets(ctx context.Context, afterID string, limit int) ([]ChannelSecret, error) {
	return s.secretsPage(ctx, "webhooks", "url_ciphertext", afterID, limit)
}

// WebhookSigningSecrets pages through every webhook's sealed signing secret by ID.
func (s *Store) WebhookSigningSecrets(ctx context.Context, afterID string, limit int) ([]ChannelSecret, error) {
	return s.secretsPage(ctx, "webhooks", "secret_ciphertext", afterID, limit)
}

// ReplaceWebhookURLCiphertext swaps a sealed URL if it still equals old.
func (s *Store) ReplaceWebhookURLCiphertext(ctx context.Context, id, old, replacement string) error {
	return s.replaceSecret(ctx, "webhooks", "url_ciphertext", id, old, replacement)
}

// ReplaceWebhookSecretCiphertext swaps a sealed signing secret if it still equals old.
func (s *Store) ReplaceWebhookSecretCiphertext(ctx context.Context, id, old, replacement string) error {
	return s.replaceSecret(ctx, "webhooks", "secret_ciphertext", id, old, replacement)
}
