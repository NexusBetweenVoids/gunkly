package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/db"
	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/schedule"
)

// Delivery statuses.
const (
	DeliveryPending = "pending"
	DeliverySent    = "sent"
	DeliveryFailed  = "failed"
)

// QuotaExceeded is the delivery-log error of a reminder not sent because a
// notification quota was reached.
const QuotaExceeded = "quota exceeded"

// --- Scheduler input (system use, unscoped) ---

// ReminderChore is an active chore with what the scheduler needs to compute
// its reminders.
type ReminderChore struct {
	ID          string
	HouseholdID string
	Name        string
	DueDate     string
	NextDue     time.Time
	Timezone    string
	Quiet       schedule.QuietHours // Start == End: none
	Preset      string              // effective preset: the chore's, else the household default
	Custom      []schedule.Reminder // chore_reminders rows (used when Preset is "custom")
}

// ReminderChores pages (by ID) through active chores — not archived, not
// paused at now, with a due instant before horizon — for the scheduler.
func (s *Store) ReminderChores(ctx context.Context, now, horizon time.Time, afterID string, limit int) ([]ReminderChore, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	var out []ReminderChore
	err := s.inTx(ctx, false, func(t *tx) error {
		idx := map[string]int{}
		err := func() error {
			rows, err := t.query(ctx, `SELECT c.id, c.household_id, c.name, c.due_date, c.next_due, h.timezone,
				h.quiet_start_min, h.quiet_end_min, CASE WHEN c.reminder_preset = '' THEN h.default_reminder_preset ELSE c.reminder_preset END
				FROM chores c JOIN households h ON h.id = c.household_id
				WHERE c.id > ? AND c.archived_at IS NULL AND c.paused_indefinitely = FALSE AND c.next_due IS NOT NULL
				AND c.next_due < ? AND (c.paused_until IS NULL OR c.paused_until <= ?)
				ORDER BY c.id LIMIT ?`, afterID, horizon.UnixMilli(), now.UnixMilli(), limit)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var r ReminderChore
				var next int64
				var qs, qe sql.NullInt64
				if err := rows.Scan(&r.ID, &r.HouseholdID, &r.Name, &r.DueDate, &next, &r.Timezone, &qs, &qe, &r.Preset); err != nil {
					return err
				}
				r.NextDue = time.UnixMilli(next).UTC()
				if qs.Valid && qe.Valid {
					r.Quiet = schedule.QuietHours{Start: int(qs.Int64), End: int(qe.Int64)}
				}
				idx[r.ID] = len(out)
				out = append(out, r)
			}
			return rows.Err()
		}()
		if err != nil || len(out) == 0 {
			return err
		}
		rows, err := t.query(ctx, `SELECT chore_id, kind, days_before, time_of_day FROM chore_reminders
			WHERE chore_id >= ? AND chore_id <= ? ORDER BY chore_id, id`, out[0].ID, out[len(out)-1].ID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var cid, kind string
			var days, minute int
			if err := rows.Scan(&cid, &kind, &days, &minute); err != nil {
				return err
			}
			i, ok := idx[cid]
			if !ok {
				continue
			}
			r := schedule.Reminder{Days: days, Minute: minute}
			switch kind {
			case "before":
				r.Kind = schedule.Before
			case "on":
				r.Kind = schedule.OnDue
			case "overdue":
				r.Kind, r.Days = schedule.Overdue, max(1, days)
			}
			out[i].Custom = append(out[i].Custom, r)
		}
		return rows.Err()
	})
	return out, err
}

// ReminderTarget is one place a chore's reminder goes: a recipient's personal
// channel, Web Push subscription or email (UserID set; exactly one of
// ChannelID, PushID and Email) or a household channel (UserID empty).
type ReminderTarget struct {
	UserID    string
	ChannelID string
	PushID    string
	Email     bool // the user's own verified address
	// Quiet is the recipient's personal quiet hours (household time zone);
	// Start == End: none, and always none for household channels.
	Quiet schedule.QuietHours
}

// ReminderTargets resolves where a chore's reminders go: the personal
// channels, Web Push subscriptions and (opted in, verified) email address of its non-guest assignees (or, with no
// assignees, of every non-guest member) who have not muted it, plus the
// household's channels. A chore that takes turns reminds only the person whose
// turn it is (household channels are unchanged). Guests never receive reminders.
func (s *Store) ReminderTargets(ctx context.Context, householdID, choreID string) ([]ReminderTarget, error) {
	var out []ReminderTarget
	err := s.inTx(ctx, false, func(t *tx) error {
		var turn string
		if err := t.queryRow(ctx, "SELECT COALESCE(turn_user_id, '') FROM chores WHERE id = ? AND household_id = ? AND rotation = TRUE",
			choreID, householdID).Scan(&turn); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		const eligible = `m.household_id = ? AND m.role <> 'guest'
			AND NOT EXISTS (SELECT 1 FROM chore_mutes cm WHERE cm.user_id = m.user_id AND cm.chore_id = ?)
			AND (NOT EXISTS (SELECT 1 FROM chore_assignees ca WHERE ca.chore_id = ?)
				OR EXISTS (SELECT 1 FROM chore_assignees ca WHERE ca.chore_id = ? AND ca.user_id = m.user_id))
			AND (? = '' OR m.user_id = ?)`
		rows, err := t.query(ctx, `SELECT m.user_id, nc.id, '', 0, u.quiet_start_min, u.quiet_end_min FROM memberships m
			JOIN users u ON u.id = m.user_id
			JOIN notification_channels nc ON nc.scope = 'user' AND nc.user_id = m.user_id
			WHERE `+eligible+`
			UNION ALL
			SELECT m.user_id, '', ps.id, 0, u.quiet_start_min, u.quiet_end_min FROM memberships m
			JOIN users u ON u.id = m.user_id
			JOIN push_subscriptions ps ON ps.user_id = m.user_id
			WHERE `+eligible+`
			UNION ALL
			SELECT m.user_id, '', '', 1, u.quiet_start_min, u.quiet_end_min FROM memberships m
			JOIN users u ON u.id = m.user_id AND u.email_reminders = TRUE AND u.email_verified = TRUE AND u.email IS NOT NULL
			WHERE `+eligible+`
			UNION ALL
			SELECT '', id, '', 0, CAST(NULL AS INTEGER), CAST(NULL AS INTEGER) FROM notification_channels WHERE scope = 'household' AND household_id = ?
			ORDER BY 1, 2, 3, 4`, householdID, choreID, choreID, choreID, turn, turn, householdID, choreID, choreID, choreID, turn, turn,
			householdID, choreID, choreID, choreID, turn, turn, householdID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r ReminderTarget
			var email int
			var qs, qe sql.NullInt64
			if err := rows.Scan(&r.UserID, &r.ChannelID, &r.PushID, &email, &qs, &qe); err != nil {
				return err
			}
			r.Email = email == 1
			if qs.Valid && qe.Valid {
				r.Quiet = schedule.QuietHours{Start: int(qs.Int64), End: int(qe.Int64)}
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

// --- Deliveries ---

// NewDelivery is a reminder to send to one target.
type NewDelivery struct {
	Key           string // idempotency key: a repeated key is a no-op
	HouseholdID   string
	ChoreID       string
	UserID        string // "" for a household channel
	ChannelID     string // "" for a push delivery
	PushID        string // push subscription; "" for a channel delivery
	ViaEmail      bool   // to the user's own verified address
	Kind          schedule.ReminderKind
	DueDate       string    // occurrence the reminder is for
	FireAt        time.Time // when it fired
	NextAttemptAt time.Time // when to send (after quiet-hours deferral)
	// Coalesce: on a repeated Key of a still-pending delivery, keep the more
	// urgent Kind (quiet-hours coalescing) instead of ignoring it.
	Coalesce bool
}

// Quota caps notifications (limits.NotificationsHouseholdHour/Day and
// NotificationsUserDay, resolved by the caller). Zero fields are unlimited.
type Quota struct {
	HouseholdHour int
	HouseholdDay  int
	UserDay       int
}

// EnqueueDeliveries inserts deliveries, skipping keys that exist. A delivery
// over quota is recorded as failed with QuotaExceeded (visible in the log,
// never retried). Returns how many new pending deliveries were queued.
func (s *Store) EnqueueDeliveries(ctx context.Context, ds []NewDelivery, q Quota, now time.Time) (int, error) {
	n := 0
	err := s.inTx(ctx, true, func(t *tx) error {
		n = 0
		for _, d := range ds {
			var status string
			var kind int
			err := t.queryRow(ctx, "SELECT status, kind FROM deliveries WHERE delivery_key = ?", d.Key).Scan(&status, &kind)
			if err == nil {
				if d.Coalesce && status == DeliveryPending && int(d.Kind) > kind {
					if _, err := t.exec(ctx, "UPDATE deliveries SET kind = ? WHERE delivery_key = ? AND status = 'pending'", int(d.Kind), d.Key); err != nil {
						return err
					}
				}
				continue
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			over, err := t.overQuota(ctx, d, q, now)
			if err != nil {
				return err
			}
			status, lastErr, next := DeliveryPending, "", ms(d.NextAttemptAt)
			if over {
				status, lastErr, next = DeliveryFailed, QuotaExceeded, sql.NullInt64{}
			} else {
				n++
			}
			if _, err := t.exec(ctx, `INSERT INTO deliveries (id, delivery_key, household_id, chore_id, user_id, channel_id, push_subscription_id, status,
				attempts, next_attempt_at, last_error, created_at, kind, due_date, fire_at, via_email)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?)`,
				ids.New(), d.Key, d.HouseholdID, nullStr(d.ChoreID), nullStr(d.UserID), nullStr(d.ChannelID), nullStr(d.PushID), status,
				next, lastErr, now.UnixMilli(), int(d.Kind), d.DueDate, ms(d.FireAt), d.ViaEmail); err != nil {
				return err
			}
		}
		return nil
	})
	return n, err
}

func (t *tx) overQuota(ctx context.Context, d NewDelivery, q Quota, now time.Time) (bool, error) {
	count := func(col, id string, since time.Duration) (int, error) {
		var n int
		err := t.queryRow(ctx, "SELECT count(*) FROM deliveries WHERE "+col+" = ? AND created_at >= ? AND status <> 'failed'",
			id, now.Add(-since).UnixMilli()).Scan(&n)
		return n, err
	}
	checks := []struct {
		col, id string
		since   time.Duration
		max     int
	}{
		{"household_id", d.HouseholdID, time.Hour, q.HouseholdHour},
		{"household_id", d.HouseholdID, 24 * time.Hour, q.HouseholdDay},
		{"user_id", d.UserID, 24 * time.Hour, q.UserDay},
	}
	for _, c := range checks {
		if c.max <= 0 || c.id == "" {
			continue
		}
		n, err := count(c.col, c.id, c.since)
		if err != nil {
			return false, err
		}
		if n >= c.max {
			return true, nil
		}
	}
	return false, nil
}

// ClaimedDelivery is a due delivery with everything needed to re-check and
// send it. Fields from joined rows are empty when the row is gone.
type ClaimedDelivery struct {
	ID          string
	HouseholdID string
	ChoreID     string
	UserID      string
	ChannelID   string
	Kind        schedule.ReminderKind
	DueDate     string
	Attempts    int

	Service    string // "" = channel deleted (and no push subscription)
	Ciphertext string

	PushID         string // push subscription; "" for a channel delivery
	PushEndpoint   string // "" = subscription deleted
	PushCiphertext string // sealed keys

	ViaEmail bool   // the user's own address
	EmailTo  string // "" = the user no longer has email reminders on a verified address

	ChoreName    string
	ChoreDueDate string
	ChoreNextDue time.Time
	ChoreActive  bool // exists, not archived, not paused

	Locale      string // recipient's; "" for household channels
	RecipientOK bool   // household channel, or recipient still a non-guest member who has not muted the chore
	HouseholdTZ string
}

// ClaimDeliveries leases up to limit pending deliveries due at now: their
// next attempt moves to now+lease, so a crashed sender's claims are retried
// after the lease (with the same delivery row, never a duplicate).
func (s *Store) ClaimDeliveries(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]ClaimedDelivery, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var out []ClaimedDelivery
	err := s.inTx(ctx, true, func(t *tx) error {
		q := "SELECT id FROM deliveries WHERE status = 'pending' AND next_attempt_at <= ? ORDER BY next_attempt_at, id LIMIT ?"
		if t.d == db.Postgres {
			q += " FOR UPDATE SKIP LOCKED"
		}
		var idList []string
		err := func() error {
			rows, err := t.query(ctx, q, now.UnixMilli(), limit)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					return err
				}
				idList = append(idList, id)
			}
			return rows.Err()
		}()
		if err != nil {
			return err
		}
		for _, id := range idList {
			if _, err := t.exec(ctx, "UPDATE deliveries SET next_attempt_at = ? WHERE id = ?", now.Add(lease).UnixMilli(), id); err != nil {
				return err
			}
			c, err := t.claimed(ctx, id, now)
			if err != nil {
				return err
			}
			out = append(out, c)
		}
		return nil
	})
	return out, err
}

func (t *tx) claimed(ctx context.Context, id string, now time.Time) (ClaimedDelivery, error) {
	var c ClaimedDelivery
	var kind int
	var next, archived, pausedUntil sql.NullInt64
	var pausedIndef sql.NullBool
	var choreDue, choreName, role, tz sql.NullString
	var viaEmail, emailOn, emailVerified sql.NullBool
	var email string
	var muted int
	err := t.queryRow(ctx, `SELECT d.id, d.household_id, COALESCE(d.chore_id, ''), COALESCE(d.user_id, ''), COALESCE(d.channel_id, ''),
		d.kind, d.due_date, d.attempts, COALESCE(nc.service, ''), COALESCE(nc.url_ciphertext, ''),
		COALESCE(d.push_subscription_id, ''), COALESCE(ps.endpoint, ''), COALESCE(ps.keys_ciphertext, ''),
		c.name, c.due_date, c.next_due, c.archived_at, c.paused_indefinitely, c.paused_until,
		COALESCE(u.locale, ''), m.role, h.timezone, d.via_email, u.email_reminders, u.email_verified, COALESCE(u.email, ''),
		(SELECT count(*) FROM chore_mutes cm WHERE cm.user_id = d.user_id AND cm.chore_id = d.chore_id)
		FROM deliveries d
		LEFT JOIN notification_channels nc ON nc.id = d.channel_id
		LEFT JOIN push_subscriptions ps ON ps.id = d.push_subscription_id
		LEFT JOIN chores c ON c.id = d.chore_id AND c.household_id = d.household_id
		LEFT JOIN users u ON u.id = d.user_id
		LEFT JOIN memberships m ON m.household_id = d.household_id AND m.user_id = d.user_id
		LEFT JOIN households h ON h.id = d.household_id
		WHERE d.id = ?`, id).Scan(&c.ID, &c.HouseholdID, &c.ChoreID, &c.UserID, &c.ChannelID, &kind, &c.DueDate, &c.Attempts,
		&c.Service, &c.Ciphertext, &c.PushID, &c.PushEndpoint, &c.PushCiphertext, &choreName, &choreDue, &next, &archived, &pausedIndef, &pausedUntil,
		&c.Locale, &role, &tz, &viaEmail, &emailOn, &emailVerified, &email, &muted)
	if err != nil {
		return ClaimedDelivery{}, err
	}
	c.Kind = schedule.ReminderKind(kind)
	c.ChoreName, c.ChoreDueDate, c.ChoreNextDue = choreName.String, choreDue.String, fromMS(next)
	c.ChoreActive = choreName.Valid && !archived.Valid && !pausedIndef.Bool && next.Valid &&
		(!pausedUntil.Valid || pausedUntil.Int64 <= now.UnixMilli())
	c.RecipientOK = c.UserID == "" || (Role(role.String).AtLeast(RoleMember) && muted == 0)
	c.HouseholdTZ = tz.String
	c.ViaEmail = viaEmail.Bool
	if c.ViaEmail && emailOn.Bool && emailVerified.Bool {
		c.EmailTo = email
	}
	return c, nil
}

// MarkDeliverySent records a successful send.
func (s *Store) MarkDeliverySent(ctx context.Context, id string, now time.Time) error {
	return s.inTx(ctx, true, func(t *tx) error {
		return t.execOne(ctx, `UPDATE deliveries SET status = 'sent', attempts = attempts + 1, sent_at = ?, next_attempt_at = NULL, last_error = ''
			WHERE id = ? AND status = 'pending'`, now.UnixMilli(), id)
	})
}

// MarkDeliveryFailed records a failed attempt. With a non-zero retryAt the
// delivery stays pending until then; otherwise it is failed for good.
// lastErr must already be scrubbed of secrets (notify.SafeError).
func (s *Store) MarkDeliveryFailed(ctx context.Context, id, lastErr string, retryAt time.Time) error {
	status := DeliveryPending
	if retryAt.IsZero() {
		status = DeliveryFailed
	}
	return s.inTx(ctx, true, func(t *tx) error {
		return t.execOne(ctx, `UPDATE deliveries SET status = ?, attempts = attempts + 1, next_attempt_at = ?, last_error = ?
			WHERE id = ? AND status = 'pending'`, status, ms(retryAt), lastErr, id)
	})
}

// DropDelivery deletes a pending delivery that must no longer be sent (its
// occurrence was completed, snoozed or skipped, the chore was archived or
// paused, the recipient muted it or left, or the channel is gone).
func (s *Store) DropDelivery(ctx context.Context, id string) error {
	return s.inTx(ctx, true, func(t *tx) error {
		_, err := t.exec(ctx, "DELETE FROM deliveries WHERE id = ? AND status = 'pending'", id)
		return err
	})
}

// --- Delivery log ---

// Delivery is a delivery-log entry.
type Delivery struct {
	ID            string
	ChoreID       string
	ChoreName     string
	UserID        string
	ChannelID     string
	ChannelLabel  string
	Service       string
	Kind          schedule.ReminderKind
	Status        string
	Attempts      int
	LastError     string
	CreatedAt     time.Time
	SentAt        time.Time
	NextAttemptAt time.Time
}

const deliveryLogQuery = `SELECT d.id, COALESCE(d.chore_id, ''), COALESCE(c.name, ''), COALESCE(d.user_id, ''), COALESCE(d.channel_id, ''),
	COALESCE(nc.label, ''), CASE WHEN ps.id IS NOT NULL THEN 'webpush' WHEN d.via_email THEN 'email' ELSE COALESCE(nc.service, '') END, d.kind, d.status, d.attempts, d.last_error, d.created_at, d.sent_at, d.next_attempt_at
	FROM deliveries d
	LEFT JOIN chores c ON c.id = d.chore_id
	LEFT JOIN notification_channels nc ON nc.id = d.channel_id
	LEFT JOIN push_subscriptions ps ON ps.id = d.push_subscription_id
	WHERE `

// DeliveryPage selects a page of a delivery log, newest first. The zero value
// is the first page of 100. A cursor (BeforeAt, BeforeID) is the CreatedAt and
// ID of the last entry of the previous page.
type DeliveryPage struct {
	Limit    int // 1..500; otherwise 100
	BeforeAt time.Time
	BeforeID string
}

func (t *tx) deliveryLog(ctx context.Context, where string, p DeliveryPage, args ...any) ([]Delivery, error) {
	limit := p.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if !p.BeforeAt.IsZero() {
		where += " AND (d.created_at < ? OR (d.created_at = ? AND d.id < ?))"
		args = append(args, p.BeforeAt.UnixMilli(), p.BeforeAt.UnixMilli(), p.BeforeID)
	}
	rows, err := t.query(ctx, deliveryLogQuery+where+" ORDER BY d.created_at DESC, d.id DESC LIMIT ?", append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Delivery
	for rows.Next() {
		var d Delivery
		var kind int
		var created int64
		var sent, next sql.NullInt64
		if err := rows.Scan(&d.ID, &d.ChoreID, &d.ChoreName, &d.UserID, &d.ChannelID, &d.ChannelLabel, &d.Service, &kind,
			&d.Status, &d.Attempts, &d.LastError, &created, &sent, &next); err != nil {
			return nil, err
		}
		d.Kind, d.CreatedAt, d.SentAt, d.NextAttemptAt = schedule.ReminderKind(kind), time.UnixMilli(created).UTC(), fromMS(sent), fromMS(next)
		out = append(out, d)
	}
	return out, rows.Err()
}

// ListHouseholdDeliveries is the household's delivery log: deliveries to its
// household channels plus the actor's own. Member or owner.
func (s *Store) ListHouseholdDeliveries(ctx context.Context, a Actor, householdID string, limit int) ([]Delivery, error) {
	return s.ListHouseholdDeliveriesPage(ctx, a, householdID, DeliveryPage{Limit: limit})
}

// ListHouseholdDeliveriesPage is ListHouseholdDeliveries with a cursor.
func (s *Store) ListHouseholdDeliveriesPage(ctx context.Context, a Actor, householdID string, p DeliveryPage) ([]Delivery, error) {
	var out []Delivery
	err := s.inTx(ctx, false, func(t *tx) error {
		if _, err := t.authorize(ctx, a, householdID, RoleMember, false); err != nil {
			return err
		}
		var err error
		out, err = t.deliveryLog(ctx, "d.household_id = ? AND (d.user_id IS NULL OR d.user_id = ?)", p, householdID, a.UserID)
		return err
	})
	return out, err
}

// ListUserDeliveries is the actor's personal delivery log across households.
func (s *Store) ListUserDeliveries(ctx context.Context, a Actor, limit int) ([]Delivery, error) {
	return s.ListUserDeliveriesPage(ctx, a, DeliveryPage{Limit: limit})
}

// ListUserDeliveriesPage is ListUserDeliveries with a cursor.
func (s *Store) ListUserDeliveriesPage(ctx context.Context, a Actor, p DeliveryPage) ([]Delivery, error) {
	var out []Delivery
	err := s.inTx(ctx, false, func(t *tx) error {
		var err error
		out, err = t.deliveryLog(ctx, "d.user_id = ?", p, a.UserID)
		return err
	})
	return out, err
}

// RecordTestDelivery logs the outcome of a test message (no chore, kind 0).
// householdID is "" for a personal channel, whose entry is private to the
// actor; for a household channel the entry is visible to its members. lastErr
// must already be redacted (notify.SafeError). Callers have verified channel
// access; a channel that is not in that scope is ErrNotFound.
func (s *Store) RecordTestDelivery(ctx context.Context, a Actor, householdID, channelID string, sent bool, lastErr string) error {
	return s.inTx(ctx, true, func(t *tx) error {
		var n int
		q, args := "SELECT count(*) FROM notification_channels WHERE id = ? AND scope = 'user' AND user_id = ?", []any{channelID, a.UserID}
		if householdID != "" {
			q, args = "SELECT count(*) FROM notification_channels WHERE id = ? AND scope = 'household' AND household_id = ?", []any{channelID, householdID}
		}
		if err := t.queryRow(ctx, q, args...).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			return ErrNotFound
		}
		now, status := s.nowMS(), DeliveryFailed
		var sentAt sql.NullInt64
		if sent {
			status, sentAt = DeliverySent, sql.NullInt64{Int64: now, Valid: true}
			lastErr = ""
		}
		id := ids.New()
		var user any
		if householdID == "" {
			user = a.UserID
		}
		_, err := t.exec(ctx, `INSERT INTO deliveries (id, delivery_key, household_id, user_id, channel_id, status, attempts, last_error, created_at, sent_at)
			VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?, ?)`, id, "test:"+id, nullStr(householdID), user, channelID, status, lastErr, now, sentAt)
		return err
	})
}

// --- Scheduler state ---

const lastTickKey = "scheduler.last_tick"

// LastSchedulerTick returns the end of the last processed scheduler window,
// or the zero time if the scheduler never ran.
func (s *Store) LastSchedulerTick(ctx context.Context) (time.Time, error) {
	var v string
	err := s.db.QueryRowContext(ctx, s.d.Rebind("SELECT value FROM settings WHERE key = ?"), lastTickKey).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return time.Time{}, nil // corrupt: start fresh rather than replay
	}
	return time.UnixMilli(n).UTC(), nil
}

// SetLastSchedulerTick stores the end of the processed scheduler window.
func (s *Store) SetLastSchedulerTick(ctx context.Context, at time.Time) error {
	return s.inTx(ctx, true, func(t *tx) error {
		_, err := t.exec(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			lastTickKey, strconv.FormatInt(at.UnixMilli(), 10), s.nowMS())
		return err
	})
}

// EmailDeliveriesSince counts email reminders sent since t instance-wide, for
// the global hourly email cap (limits.EmailsGlobalHour).
func (s *Store) EmailDeliveriesSince(ctx context.Context, t time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, s.d.Rebind("SELECT count(*) FROM deliveries WHERE via_email = TRUE AND status = 'sent' AND sent_at >= ?"),
		t.UnixMilli()).Scan(&n)
	return n, err
}

// SetEmailReminders turns reminders by email on or off for the user. Turning
// them on needs a verified address (ErrInvalid otherwise).
func (s *Store) SetEmailReminders(ctx context.Context, userID string, on bool) (User, error) {
	var u User
	err := s.inTx(ctx, true, func(t *tx) error {
		q := "UPDATE users SET email_reminders = ? WHERE id = ?"
		if on {
			q += " AND email_verified = TRUE AND email IS NOT NULL"
		}
		res, err := t.exec(ctx, q, on, userID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrInvalid
		}
		u, err = scanUser(t.queryRow(ctx, "SELECT "+userCols+" FROM users WHERE id = ?", userID))
		return err
	})
	return u, err
}
