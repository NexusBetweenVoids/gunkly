// Package scheduler is the in-process reminder worker: each tick it turns due
// reminders into deliveries (idempotent delivery keys), sends due deliveries
// with retries and backoff, and runs the daily retention and inactive-account jobs.
//
// Only one instance sends. On Postgres the leader holds a session-level
// advisory lock on a dedicated connection; other instances stand by and take
// over when that connection goes away. SQLite is single-instance, no lock.
package scheduler

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/auth"
	"github.com/NexusBetweenVoids/gunkly/internal/db"
	"github.com/NexusBetweenVoids/gunkly/internal/i18n"
	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/mail"
	"github.com/NexusBetweenVoids/gunkly/internal/notify"
	"github.com/NexusBetweenVoids/gunkly/internal/schedule"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// Reminder presets. A chore's reminder_preset (or the household default when
// empty) names one; PresetCustom uses the chore's own chore_reminders rows.
// Unknown names and "" mean no reminders.
const PresetCustom = "custom"

// Presets are the built-in reminder presets (local times, household zone).
var Presets = map[string][]schedule.Reminder{
	"none":   nil,
	"gentle": {{Kind: schedule.OnDue, Minute: 9 * 60}},
	"nag": {
		{Kind: schedule.Before, Days: 1, Minute: 18 * 60},
		{Kind: schedule.OnDue, Minute: 9 * 60},
		{Kind: schedule.Overdue, Days: 1, Minute: 9 * 60},
	},
}

// Defaults.
const (
	DefaultTick      = 30 * time.Second
	DefaultLockKey   = 0x67756e6b // "gunk"; the migration lock uses another key
	maxAttempts      = 6          // then failed for good
	claimLease       = 2 * time.Minute
	claimBatch       = 50
	maxClaimRounds   = 20 // per tick
	sendTimeout      = 30 * time.Second
	catchUpAfter     = 5 * time.Minute // a gap this long is downtime: one catch-up per chore
	reminderHorizon  = 366 * 24 * time.Hour
	retentionEvery   = 24 * time.Hour
	vacuumPages      = 1000
	vacuumMaxBatches = 20
)

// Config wires a Worker.
type Config struct {
	Store   *store.Store
	DB      *db.DB // the leadership lock (Postgres)
	Sender  notify.Sender
	Keyring *auth.Keyring
	Limits  *limits.Service // nil = limits.Defaults
	BaseURL string          // for mark-done links; "" = messages carry no link
	// EnabledKinds returns the admin's service allowlist; nil = notify.DefaultKinds.
	// A delivery to a kind disabled after the channel was saved fails.
	EnabledKinds func(context.Context) (map[string]bool, error)
	// Pusher delivers Web Push reminders; nil fails them ("push not configured").
	Pusher notify.Pusher
	// Mailer sends inactivity warnings by email; nil = no email.
	Mailer Mailer
	// Hosted enables the purge of unverified, household-less accounts (only
	// meaningful where sign-up requires an email address).
	Hosted bool
	// OnDelivery, if set, is told each finished send attempt: the channel kind
	// ("webpush" for push) and whether it succeeded (metrics).
	OnDelivery  func(kind string, ok bool)
	Logger      *slog.Logger     // nil = slog.Default()
	Now         func() time.Time // nil = time.Now
	Tick        time.Duration    // 0 = DefaultTick
	LockKey     int64            // 0 = DefaultLockKey
	Concurrency int              // parallel sends; 0 = 4
}

// Worker is the scheduler. Create with New, start with Run.
type Worker struct {
	cfg           Config
	log           *slog.Logger
	leading       atomic.Bool
	lastRetention time.Time
	lastInactive  time.Time
}

// New returns a Worker for cfg.
func New(cfg Config) *Worker {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Tick <= 0 {
		cfg.Tick = DefaultTick
	}
	if cfg.LockKey == 0 {
		cfg.LockKey = DefaultLockKey
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 4
	}
	l := cfg.Logger
	if l == nil {
		l = slog.Default()
	}
	return &Worker{cfg: cfg, log: l.With("component", "scheduler")}
}

// Run leads (or stands by) until ctx is cancelled. It returns once the
// current tick, including in-flight sends, has finished and the lock is
// released. Sends are not interrupted by ctx; each is bounded by its own
// timeout, so the caller's shutdown deadline should exceed sendTimeout.
func (w *Worker) Run(ctx context.Context) error {
	if w.cfg.Store == nil || w.cfg.DB == nil || w.cfg.Sender == nil || w.cfg.Keyring == nil {
		return errors.New("scheduler: incomplete config")
	}
	for ctx.Err() == nil {
		var conn *sql.Conn
		if w.cfg.DB.Dialect == db.Postgres {
			var err error
			if conn, err = w.acquire(ctx); err != nil {
				return nil //nolint:nilerr // only ctx cancellation ends acquire
			}
			w.log.Info("scheduler leading")
		}
		w.leading.Store(true)
		w.lead(ctx, conn)
		w.leading.Store(false)
		if conn != nil {
			w.release(conn)
			w.log.Info("scheduler stepped down")
			// Give a standby the first chance to take over.
			select {
			case <-ctx.Done():
			case <-time.After(2 * w.cfg.Tick):
			}
		}
	}
	return nil
}

// acquire blocks until this instance holds the advisory lock or ctx ends.
func (w *Worker) acquire(ctx context.Context) (*sql.Conn, error) {
	for {
		conn, err := w.cfg.DB.Conn(ctx)
		if err == nil {
			var ok bool
			err = conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", w.cfg.LockKey).Scan(&ok)
			if err == nil && ok {
				return conn, nil
			}
			_ = conn.Close()
		}
		if err != nil && ctx.Err() == nil {
			w.log.Warn("scheduler lock attempt failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(w.cfg.Tick):
		}
	}
}

// release unlocks and discards the lock connection: returning it to the
// pool could keep the session (and a lock whose unlock failed) alive.
func (w *Worker) release(conn *sql.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", w.cfg.LockKey)
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
}

// lead ticks until ctx ends or the lock connection is lost.
func (w *Worker) lead(ctx context.Context, conn *sql.Conn) {
	t := time.NewTicker(w.cfg.Tick)
	defer t.Stop()
	for {
		if conn != nil {
			if err := conn.PingContext(ctx); err != nil {
				if ctx.Err() == nil {
					w.log.Warn("scheduler lost its lock connection", "err", err)
				}
				return
			}
		}
		if err := w.tick(ctx); err != nil && ctx.Err() == nil {
			w.log.Error("scheduler tick failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// tick processes the window since the last tick, sends what is due and runs
// retention once a day.
func (w *Worker) tick(ctx context.Context) error {
	now := w.cfg.Now().UTC().Truncate(time.Millisecond)
	last, err := w.cfg.Store.LastSchedulerTick(ctx)
	if err != nil {
		return err
	}
	switch {
	case last.IsZero() || last.After(now):
		// First run (or the clock went back): start here, no backlog.
		if err := w.cfg.Store.SetLastSchedulerTick(ctx, now); err != nil {
			return err
		}
	case now.After(last):
		if err := w.enqueue(ctx, last, now, now.Sub(last) >= catchUpAfter); err != nil {
			return err
		}
		if err := w.cfg.Store.SetLastSchedulerTick(ctx, now); err != nil {
			return err
		}
	}
	w.send(ctx)
	if now.Sub(w.lastRetention) >= retentionEvery {
		w.lastRetention = now
		w.retention(ctx, now)
	}
	if now.Sub(w.lastInactive) >= inactiveEvery {
		w.lastInactive = now
		w.inactive(ctx, now)
	}
	return nil
}

// specs resolves a chore's reminder specs.
func specs(c store.ReminderChore) []schedule.Reminder {
	if c.Preset == PresetCustom {
		return c.Custom
	}
	return Presets[c.Preset]
}

// DeliveryKey is the idempotency key of one reminder to one target. Quiet-hours
// deferred reminders are keyed by their release instant without the kind, so
// all of a chore's reminders deferred to the same instant coalesce into one.
func DeliveryKey(choreID, dueDate string, kind schedule.ReminderKind, at time.Time, deferred bool, channelID, userID string) string {
	k := strconv.Itoa(int(kind))
	if deferred {
		k = "quiet"
	}
	h := sha256.Sum256([]byte(choreID + "|" + dueDate + "|" + k + "|" + strconv.FormatInt(at.UnixMilli(), 10) + "|" + channelID + "|" + userID))
	return hex.EncodeToString(h[:])
}

// enqueue turns the reminders firing in [from, to) into deliveries. After
// downtime (catchUp) only the latest missed reminder per chore is kept.
func (w *Worker) enqueue(ctx context.Context, from, to time.Time, catchUp bool) error {
	locs := map[string]*time.Location{}
	quotas := map[string]store.Quota{}
	after := ""
	for {
		page, err := w.cfg.Store.ReminderChores(ctx, to, to.Add(reminderHorizon), after, 500)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			return nil
		}
		for _, c := range page {
			after = c.ID
			if err := w.enqueueChore(ctx, c, from, to, catchUp, locs, quotas); err != nil {
				w.log.Error("scheduler: enqueue chore", "chore", c.ID, "err", err)
			}
		}
	}
}

func (w *Worker) enqueueChore(ctx context.Context, c store.ReminderChore, from, to time.Time, catchUp bool,
	locs map[string]*time.Location, quotas map[string]store.Quota) error {
	sp := specs(c)
	if len(sp) == 0 {
		return nil
	}
	due, err := schedule.ParseDate(c.DueDate)
	if err != nil {
		return err
	}
	loc, ok := locs[c.Timezone]
	if !ok {
		if loc, err = time.LoadLocation(c.Timezone); err != nil {
			return err
		}
		locs[c.Timezone] = loc
	}
	var fires []schedule.Fire
	if catchUp {
		if f, ok := schedule.CatchUp(due, sp, loc, from, to); ok {
			fires = []schedule.Fire{f}
		}
	} else {
		fires = schedule.Reminders(due, sp, loc, from, to)
	}
	if len(fires) == 0 {
		return nil
	}
	targets, err := w.cfg.Store.ReminderTargets(ctx, c.HouseholdID, c.ID)
	if err != nil || len(targets) == 0 {
		return err
	}
	q, ok := quotas[c.HouseholdID]
	if !ok {
		if q, err = w.quota(ctx, c.HouseholdID); err != nil {
			return err
		}
		quotas[c.HouseholdID] = q
	}
	var ds []store.NewDelivery
	for _, f := range fires {
		send := f.At
		if catchUp {
			send = to
		}
		release := c.Quiet.Defer(send, loc)
		deferred := !release.Equal(send)
		keyAt := f.At
		if deferred {
			keyAt = release
		}
		for _, tg := range targets {
			if tg.Email && w.cfg.Mailer == nil {
				continue // this instance sends no email
			}
			target := tg.ChannelID
			switch {
			case tg.PushID != "":
				target = "push:" + tg.PushID // a subscription is a target like a channel
			case tg.Email:
				target = "email" // with the user ID in the key: the user's own address
			}
			ds = append(ds, store.NewDelivery{
				Key:         DeliveryKey(c.ID, c.DueDate, f.Kind, keyAt, deferred, target, tg.UserID),
				HouseholdID: c.HouseholdID, ChoreID: c.ID, UserID: tg.UserID, ChannelID: tg.ChannelID, PushID: tg.PushID, ViaEmail: tg.Email,
				Kind: f.Kind, DueDate: c.DueDate, FireAt: f.At, NextAttemptAt: release, Coalesce: deferred,
			})
		}
	}
	_, err = w.cfg.Store.EnqueueDeliveries(ctx, ds, q, to)
	return err
}

// quota resolves the household's notification limits through internal/limits.
func (w *Worker) quota(ctx context.Context, householdID string) (store.Quota, error) {
	get := func(k limits.Kind) (int, error) {
		if w.cfg.Limits == nil {
			return limits.Defaults[k], nil
		}
		return w.cfg.Limits.Limit(ctx, k, householdID)
	}
	var q store.Quota
	var err error
	if q.HouseholdHour, err = get(limits.NotificationsHouseholdHour); err != nil {
		return q, err
	}
	if q.HouseholdDay, err = get(limits.NotificationsHouseholdDay); err != nil {
		return q, err
	}
	// Per-user limits are instance-wide: resolve without a household plan.
	if w.cfg.Limits == nil {
		q.UserDay = limits.Defaults[limits.NotificationsUserDay]
	} else if q.UserDay, err = w.cfg.Limits.Limit(ctx, limits.NotificationsUserDay, ""); err != nil {
		return q, err
	}
	return q, nil
}

// send claims and sends due deliveries until none are left (bounded per
// tick). It stops starting new sends once ctx is cancelled but lets started
// ones finish.
func (w *Worker) send(ctx context.Context) {
	enabled := notify.DefaultKinds()
	if w.cfg.EnabledKinds != nil {
		e, err := w.cfg.EnabledKinds(ctx)
		if err != nil {
			w.log.Error("scheduler: enabled services", "err", err)
			return
		}
		enabled = e
	}
	sendCtx := context.WithoutCancel(ctx)
	sem := make(chan struct{}, w.cfg.Concurrency)
	var wg sync.WaitGroup
	defer wg.Wait()
	for range maxClaimRounds {
		if ctx.Err() != nil {
			return
		}
		ds, err := w.cfg.Store.ClaimDeliveries(ctx, w.cfg.Now(), claimLease, claimBatch)
		if err != nil {
			if ctx.Err() == nil {
				w.log.Error("scheduler: claim deliveries", "err", err)
			}
			return
		}
		for _, d := range ds {
			sem <- struct{}{}
			wg.Add(1)
			go func() {
				defer func() { <-sem; wg.Done() }()
				w.deliver(sendCtx, d, enabled)
			}()
		}
		if len(ds) < claimBatch {
			return
		}
		wg.Wait() // finish this batch before claiming more
	}
}

// Backoff is the wait before retry number attempt (1-based): 1, 2, 4, ... minutes, at most an hour.
func Backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := time.Minute << min(attempt-1, 6)
	return min(d, time.Hour)
}

func (w *Worker) deliver(ctx context.Context, d store.ClaimedDelivery, enabled map[string]bool) {
	st := w.cfg.Store
	log := w.log.With("delivery", d.ID)
	if d.ChoreID == "" || !d.ChoreActive || d.ChoreDueDate != d.DueDate || !d.RecipientOK ||
		(d.Service == "" && d.PushEndpoint == "" && !d.ViaEmail) || (d.ViaEmail && d.EmailTo == "") {
		if err := st.DropDelivery(ctx, d.ID); err != nil {
			log.Error("scheduler: drop delivery", "err", err)
		}
		return
	}
	fail := func(msg string, retry bool) {
		var at time.Time
		if retry && d.Attempts+1 < maxAttempts {
			at = w.cfg.Now().Add(Backoff(d.Attempts + 1))
		}
		if err := st.MarkDeliveryFailed(ctx, d.ID, msg, at); err != nil {
			log.Error("scheduler: mark failed", "err", err)
		}
	}
	if d.PushEndpoint != "" {
		w.deliverPush(ctx, d, fail)
		return
	}
	if d.ViaEmail {
		w.deliverEmail(ctx, d, fail)
		return
	}
	if !enabled[d.Service] {
		fail("service disabled", false)
		return
	}
	rawURL, err := notify.Open(w.cfg.Keyring, d.ChannelID, d.Ciphertext)
	if err != nil {
		log.Error("scheduler: cannot decrypt channel", "channel", d.ChannelID, "err", err)
		fail("cannot decrypt channel", false)
		return
	}
	due, err := schedule.ParseDate(d.DueDate)
	if err != nil {
		fail("invalid due date", false)
		return
	}
	now := w.cfg.Now()
	var token string
	if w.cfg.BaseURL != "" {
		l := store.MarkDoneLink{ChoreID: d.ChoreID, OccurrenceDue: d.ChoreNextDue, IssuedAt: now, ExpiresAt: now.Add(store.MarkDoneMaxTTL)}
		if d.UserID != "" {
			l.UserID = d.UserID
		} else {
			l.ChannelID = d.ChannelID
		}
		if token, err = st.CreateMarkDone(ctx, l); err != nil {
			log.Warn("scheduler: no mark-done link", "err", err) // send without a link
			token = ""
		}
	}
	msg := notify.Render(notify.Reminder{Locale: d.Locale, ChoreName: d.ChoreName, Kind: d.Kind, Due: due,
		MarkDoneURL: notify.MarkDoneURL(w.cfg.BaseURL, token)})
	sctx, cancel := context.WithTimeout(ctx, sendTimeout)
	err = w.cfg.Sender.Send(sctx, rawURL, msg)
	cancel()
	if err != nil {
		if token != "" {
			_ = st.RevokeMarkDone(ctx, token)
		}
		safe := notify.SafeError(err, rawURL)
		log.Warn("scheduler: send failed", "service", d.Service, "attempt", d.Attempts+1, "err", safe)
		w.observe(d.Service, false)
		fail(safe, !errors.Is(err, notify.ErrNoSender))
		return
	}
	w.observe(d.Service, true)
	if err := st.MarkDeliverySent(ctx, d.ID, w.cfg.Now()); err != nil {
		log.Error("scheduler: mark sent", "err", err)
	}
}

func (w *Worker) observe(kind string, ok bool) {
	if w.cfg.OnDelivery != nil {
		w.cfg.OnDelivery(kind, ok)
	}
}

// emailKind is the delivery-log and metrics kind of an email reminder.
const emailKind = "email"

// deliverEmail sends a reminder to the user's own verified address with the
// same text and mark-done link as a messaging channel. It respects the
// instance-wide hourly email cap (limits.EmailsGlobalHour): over it the
// delivery is retried later like any failed send.
func (w *Worker) deliverEmail(ctx context.Context, d store.ClaimedDelivery, fail func(string, bool)) {
	st := w.cfg.Store
	log := w.log.With("delivery", d.ID)
	if w.cfg.Mailer == nil {
		fail("email not configured", false)
		return
	}
	due, err := schedule.ParseDate(d.DueDate)
	if err != nil {
		fail("invalid due date", false)
		return
	}
	now := w.cfg.Now()
	capPerHour := limits.Defaults[limits.EmailsGlobalHour]
	if w.cfg.Limits != nil {
		if m, err := w.cfg.Limits.Limit(ctx, limits.EmailsGlobalHour, ""); err == nil {
			capPerHour = m
		}
	}
	if capPerHour > 0 {
		n, err := st.EmailDeliveriesSince(ctx, now.Add(-time.Hour))
		if err != nil {
			log.Error("scheduler: count emails", "err", err)
			fail("internal error", true)
			return
		}
		if n >= capPerHour {
			fail("email rate limit", true)
			return
		}
	}
	var token string
	if w.cfg.BaseURL != "" {
		l := store.MarkDoneLink{ChoreID: d.ChoreID, OccurrenceDue: d.ChoreNextDue, IssuedAt: now, ExpiresAt: now.Add(store.MarkDoneMaxTTL), UserID: d.UserID}
		if token, err = st.CreateMarkDone(ctx, l); err != nil {
			log.Warn("scheduler: no mark-done link", "err", err)
			token = ""
		}
	}
	msg := notify.Render(notify.Reminder{Locale: d.Locale, ChoreName: d.ChoreName, Kind: d.Kind, Due: due,
		MarkDoneURL: notify.MarkDoneURL(w.cfg.BaseURL, token)})
	sctx, cancel := context.WithTimeout(ctx, sendTimeout)
	err = w.cfg.Mailer.Send(sctx, mail.Message{To: d.EmailTo, Subject: msg.Title,
		Body: msg.Body + "\n\n" + i18n.T(d.Locale, "notify.email_footer", nil)})
	cancel()
	if err != nil {
		if token != "" {
			_ = st.RevokeMarkDone(ctx, token)
		}
		log.Warn("scheduler: email failed", "attempt", d.Attempts+1, "err", err)
		w.observe(emailKind, false)
		fail("email send failed", true) // the SMTP error may carry the address
		return
	}
	w.observe(emailKind, true)
	if err := st.MarkDeliverySent(ctx, d.ID, w.cfg.Now()); err != nil {
		log.Error("scheduler: mark sent", "err", err)
	}
}

// deliverPush sends a reminder to a browser push subscription. The payload is
// the reminder text and a same-origin path; no mark-done link (opening the app
// is the action). A subscription the push service reports gone is deleted.
func (w *Worker) deliverPush(ctx context.Context, d store.ClaimedDelivery, fail func(string, bool)) {
	st := w.cfg.Store
	log := w.log.With("delivery", d.ID)
	if w.cfg.Pusher == nil {
		fail("push not configured", false)
		return
	}
	due, err := schedule.ParseDate(d.DueDate)
	if err != nil {
		fail("invalid due date", false)
		return
	}
	msg := notify.Render(notify.Reminder{Locale: d.Locale, ChoreName: d.ChoreName, Kind: d.Kind, Due: due})
	sctx, cancel := context.WithTimeout(ctx, sendTimeout)
	err = w.cfg.Pusher.Push(sctx, d.PushID, d.PushEndpoint, d.PushCiphertext,
		notify.PushMessage{Title: msg.Title, Body: msg.Body, URL: "/chores/" + d.ChoreID})
	cancel()
	switch {
	case err == nil:
		w.observe(notify.KindWebPush, true)
		if err := st.MarkDeliverySent(ctx, d.ID, w.cfg.Now()); err != nil {
			log.Error("scheduler: mark sent", "err", err)
		}
		if err := st.MarkPushUsed(ctx, d.PushID, w.cfg.Now()); err != nil {
			log.Warn("scheduler: mark push used", "err", err)
		}
	case errors.Is(err, notify.ErrPushGone):
		w.observe(notify.KindWebPush, false)
		log.Info("scheduler: push subscription gone, removing it")
		fail("subscription expired", false)
		if err := st.DeleteGonePushSubscription(ctx, d.PushID); err != nil {
			log.Error("scheduler: delete push subscription", "err", err)
		}
	default:
		// The error never carries the endpoint (webpush.Post strips it).
		w.observe(notify.KindWebPush, false)
		log.Warn("scheduler: push failed", "attempt", d.Attempts+1, "err", err)
		fail(notify.SafeError(err, d.PushEndpoint), true)
	}
}

func (w *Worker) retention(ctx context.Context, now time.Time) {
	st, err := w.cfg.Store.InstanceSettings(ctx) // read each run so admin changes apply
	if err != nil {
		w.log.Error("scheduler: retention settings", "err", err)
		return
	}
	p := st.Policy()
	if w.cfg.Hosted && w.cfg.Mailer != nil {
		p.UnverifiedAccounts = store.UnverifiedAccountMaxAge
	}
	n, err := w.cfg.Store.PurgeExpired(ctx, p, now)
	if err != nil {
		w.log.Error("scheduler: retention", "err", err)
		return
	}
	if err := w.cfg.Store.ReclaimSpace(ctx, vacuumPages, vacuumMaxBatches); err != nil {
		w.log.Error("scheduler: reclaim space", "err", err)
		return
	}
	w.log.Info("scheduler: retention done", "deleted", n)
}
