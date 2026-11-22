package scheduler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/auth"
	"github.com/NexusBetweenVoids/gunkly/internal/db"
	"github.com/NexusBetweenVoids/gunkly/internal/db/dbtest"
	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/notify"
	"github.com/NexusBetweenVoids/gunkly/internal/schedule"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

var ams = mustLoc("Europe/Amsterdam")

func mustLoc(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}

// sent is one message delivered by fakeSender.
type sent struct {
	url string
	msg notify.Message
}

type fakeSender struct {
	mu   sync.Mutex
	out  []sent
	fail error
}

func (f *fakeSender) Send(_ context.Context, rawURL string, m notify.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.out = append(f.out, sent{rawURL, m})
	return nil
}

func (f *fakeSender) take() []sent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.out
	f.out = nil
	return out
}

type fixture struct {
	t      *testing.T
	ctx    context.Context
	db     *db.DB
	st     *store.Store
	ring   *auth.Keyring
	sender *fakeSender
	mu     sync.Mutex
	now    time.Time
	owner  store.Actor
	hid    string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, ctx: context.Background(), sender: &fakeSender{}}
	f.db = dbtest.Open(t)
	f.st = store.New(f.db)
	var err error
	if f.ring, err = auth.NewKeyring(1, map[uint32]string{1: strings.Repeat("k", 32)}); err != nil {
		t.Fatal(err)
	}
	f.owner = f.user("owner")
	h, err := f.st.CreateHousehold(f.ctx, f.owner, store.NewHousehold{Name: "Home", Timezone: "Europe/Amsterdam"})
	if err != nil {
		t.Fatal(err)
	}
	f.hid = h.ID
	return f
}

func (f *fixture) clock() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fixture) set(t time.Time) {
	f.mu.Lock()
	f.now = t
	f.mu.Unlock()
}

func (f *fixture) worker() *Worker {
	return New(Config{Store: f.st, DB: f.db, Sender: f.sender, Keyring: f.ring, BaseURL: "https://gunkly.example",
		Now: f.clock, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Tick: 20 * time.Millisecond})
}

// tickAt runs one tick of w at local wall time in Amsterdam.
func (f *fixture) tickAt(w *Worker, local string) {
	f.t.Helper()
	at, err := time.ParseInLocation("2006-01-02 15:04:05", local, ams)
	if err != nil {
		f.t.Fatal(err)
	}
	f.set(at)
	if err := w.tick(f.ctx); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) user(name string) store.Actor {
	f.t.Helper()
	u, err := f.st.CreateUser(f.ctx, store.NewUser{Username: name, DisplayName: name, PasswordHash: "x"})
	if err != nil {
		f.t.Fatal(err)
	}
	return store.Actor{UserID: u.ID}
}

func (f *fixture) member(name string, role store.Role) store.Actor {
	f.t.Helper()
	a := f.user(name)
	if err := f.st.AddMember(f.ctx, f.owner, f.hid, a.UserID, role); err != nil {
		f.t.Fatal(err)
	}
	return a
}

// channel gives a a personal channel whose URL names it.
func (f *fixture) channel(a store.Actor, name string) {
	f.t.Helper()
	nc, err := notify.Prepare(f.ring, "discord://"+name+"@123456", name, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	nc.Max = 10
	if _, err := f.st.CreateUserChannel(f.ctx, a, nc); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) householdChannel() {
	f.t.Helper()
	nc, err := notify.Prepare(f.ring, "discord://household@123456", "family", nil)
	if err != nil {
		f.t.Fatal(err)
	}
	nc.Max = 10
	if _, err := f.st.CreateHouseholdChannel(f.ctx, f.owner, f.hid, nc); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) chore(name, due, preset string, assignees ...string) store.Chore {
	f.t.Helper()
	d, err := schedule.ParseDate(due)
	if err != nil {
		f.t.Fatal(err)
	}
	c, err := f.st.CreateChore(f.ctx, f.owner, f.hid, store.ChoreInput{
		Name: name, Kind: store.KindRecurring, ScheduleType: store.ScheduleInterval, IntervalN: 1, IntervalUnit: "week",
		Anchor: store.AnchorFromCompletion, FirstDue: due, DueDate: due, NextDue: store.OccurrenceInstant(d, ams),
		ReminderPreset: preset, Assignees: assignees,
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return c
}

// custom adds a chore_reminders row (no store API yet: the editor is the API agent's).
func (f *fixture) custom(choreID, kind string, days, minute int) {
	f.t.Helper()
	if _, err := f.db.ExecContext(f.ctx, f.db.Dialect.Rebind("INSERT INTO chore_reminders (id, chore_id, kind, days_before, time_of_day) VALUES (?, ?, ?, ?, ?)"),
		ids.New(), choreID, kind, days, minute); err != nil {
		f.t.Fatal(err)
	}
}

func recipients(ss []sent) string {
	var names []string
	for _, s := range ss {
		names = append(names, strings.TrimSuffix(strings.TrimPrefix(s.url, "discord://"), "@123456"))
	}
	return strings.Join(names, ",")
}

func TestSendsOnceAcrossRestartsAndWorkers(t *testing.T) {
	f := newFixture(t)
	f.channel(f.owner, "owner")
	f.chore("Dishes", "2026-11-21", "gentle") // on due at 09:00
	w := f.worker()
	f.tickAt(w, "2026-11-21 08:59:30")
	if got := f.sender.take(); len(got) != 0 {
		t.Fatalf("sent early: %v", got)
	}
	f.tickAt(w, "2026-11-21 09:00:10")
	got := f.sender.take()
	if len(got) != 1 || got[0].msg.Body == "" {
		t.Fatalf("sent = %+v", got)
	}
	body := got[0].msg.Body
	if !strings.Contains(body, "Dishes is due today.") || !strings.Contains(body, "https://gunkly.example/done/") {
		t.Fatalf("body = %q", body)
	}
	tok := body[strings.Index(body, "/done/")+len("/done/"):]
	if name, err := f.st.PeekMarkDone(f.ctx, tok); err != nil || name != "Dishes" {
		t.Fatalf("mark-done link: %q, %v", name, err)
	}

	// Simulated crash before the window was recorded, a second worker taking
	// over, and repeated ticks: the same window is processed again.
	before, _ := time.ParseInLocation("2006-01-02 15:04:05", "2026-11-21 08:59:30", ams)
	if err := f.st.SetLastSchedulerTick(f.ctx, before); err != nil {
		t.Fatal(err)
	}
	f.tickAt(f.worker(), "2026-11-21 09:00:20")
	f.tickAt(w, "2026-11-21 09:00:20")
	if got := f.sender.take(); len(got) != 0 {
		t.Fatalf("duplicate send: %v", recipients(got))
	}
	log, err := f.st.ListUserDeliveries(f.ctx, f.owner, 10)
	if err != nil || len(log) != 1 || log[0].Status != store.DeliverySent {
		t.Fatalf("log = %+v, %v", log, err)
	}
}

func TestRecipients(t *testing.T) {
	f := newFixture(t)
	m := f.member("member", store.RoleMember)
	g := f.member("guest", store.RoleGuest)
	f.channel(f.owner, "owner")
	f.channel(m, "member")
	f.channel(g, "guest")
	f.householdChannel()
	anyone := f.chore("Anyone", "2026-11-21", "gentle")
	f.chore("Assigned", "2026-11-22", "gentle", m.UserID)
	w := f.worker()

	f.tickAt(w, "2026-11-21 08:59:00")
	f.tickAt(w, "2026-11-21 09:01:00")
	if got := recipients(f.sender.take()); !sameSet(got, "household,member,owner") {
		t.Fatalf("unassigned chore went to %q", got)
	}
	// Muting removes the member; assigned chores go to assignees only.
	if err := f.st.SetChoreMuted(f.ctx, m, f.hid, anyone.ID, true); err != nil {
		t.Fatal(err)
	}
	f.tickAt(w, "2026-11-22 08:59:00")
	f.tickAt(w, "2026-11-22 09:01:00")
	if got := recipients(f.sender.take()); !sameSet(got, "household,member") {
		t.Fatalf("assigned chore went to %q", got)
	}
}

func sameSet(a, b string) bool {
	as, bs := strings.Split(a, ","), strings.Split(b, ",")
	if len(as) != len(bs) {
		return false
	}
	seen := map[string]int{}
	for _, x := range as {
		seen[x]++
	}
	for _, x := range bs {
		seen[x]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

func TestMuteAfterEnqueueDrops(t *testing.T) {
	f := newFixture(t)
	f.channel(f.owner, "owner")
	c := f.chore("Dishes", "2026-11-21", "gentle")
	quiet := &store.QuietHours{StartMin: 8 * 60, EndMin: 10 * 60}
	if _, err := f.st.UpdateHousehold(f.ctx, f.owner, f.hid, store.HouseholdUpdate{QuietHours: &quiet}); err != nil {
		t.Fatal(err)
	}
	w := f.worker()
	f.tickAt(w, "2026-11-21 08:59:00")
	f.tickAt(w, "2026-11-21 09:01:00") // deferred to 10:00
	if err := f.st.SetChoreMuted(f.ctx, f.owner, f.hid, c.ID, true); err != nil {
		t.Fatal(err)
	}
	f.tickAt(w, "2026-11-21 10:00:30")
	if got := f.sender.take(); len(got) != 0 {
		t.Fatalf("muted chore sent: %v", recipients(got))
	}
}

func TestQuietHoursDeferAndCoalesce(t *testing.T) {
	f := newFixture(t)
	f.channel(f.owner, "owner")
	c := f.chore("Bins", "2026-11-21", PresetCustom)
	f.custom(c.ID, "before", 1, 23*60) // 2026-11-20 23:00, in quiet hours
	f.custom(c.ID, "on", 0, 8*60)      // 2026-11-21 08:00, in quiet hours
	quiet := &store.QuietHours{StartMin: 22 * 60, EndMin: 10 * 60}
	if _, err := f.st.UpdateHousehold(f.ctx, f.owner, f.hid, store.HouseholdUpdate{QuietHours: &quiet}); err != nil {
		t.Fatal(err)
	}
	w := f.worker()
	for _, at := range []string{"2026-11-20 21:00:00", "2026-11-20 23:00:30", "2026-11-21 08:00:30", "2026-11-21 09:59:59"} {
		f.tickAt(w, at)
		if got := f.sender.take(); len(got) != 0 {
			t.Fatalf("sent during quiet hours at %s", at)
		}
	}
	f.tickAt(w, "2026-11-21 10:00:01")
	got := f.sender.take()
	if len(got) != 1 || !strings.Contains(got[0].msg.Body, "Bins is due today.") {
		t.Fatalf("after quiet hours: %+v", got)
	}
	f.tickAt(w, "2026-11-21 10:05:00")
	if len(f.sender.take()) != 0 {
		t.Fatal("second send after coalescing")
	}
}

func TestCatchUpAfterDowntime(t *testing.T) {
	f := newFixture(t)
	f.channel(f.owner, "owner")
	f.chore("Gutters", "2026-11-21", "nag") // before 18:00 day before, on 09:00, overdue daily 09:00
	f.chore("Filter", "2026-11-22", "nag")
	w := f.worker()
	f.tickAt(w, "2026-11-19 12:00:00")
	f.tickAt(w, "2026-11-23 12:00:00") // four days down
	got := f.sender.take()
	if len(got) != 2 {
		t.Fatalf("catch-up sent %d, want one per chore: %+v", len(got), got)
	}
	for _, s := range got {
		if !strings.Contains(s.msg.Body, "overdue") {
			t.Errorf("catch-up is not the latest (overdue) reminder: %q", s.msg.Body)
		}
	}
	f.tickAt(w, "2026-11-23 12:00:30")
	if len(f.sender.take()) != 0 {
		t.Fatal("extra sends after catch-up")
	}
}

func TestDSTReminderTime(t *testing.T) {
	f := newFixture(t)
	f.channel(f.owner, "owner")
	c := f.chore("Clocks", "2026-03-29", PresetCustom)
	f.custom(c.ID, "on", 0, 2*60+30) // 02:30 does not exist on 2026-03-29 in Amsterdam
	w := f.worker()
	f.tickAt(w, "2026-03-29 01:59:00")
	f.set(time.Date(2026, 3, 29, 0, 59, 59, 0, time.UTC)) // 01:59:59 CET
	if err := w.tick(f.ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.sender.take()) != 0 {
		t.Fatal("sent before the shifted time")
	}
	f.set(time.Date(2026, 3, 29, 1, 0, 1, 0, time.UTC)) // 03:00:01 CEST
	if err := w.tick(f.ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.sender.take(); len(got) != 1 {
		t.Fatalf("sent %d at 03:00 CEST", len(got))
	}
}

func TestRetryBackoffAndRedaction(t *testing.T) {
	f := newFixture(t)
	f.channel(f.owner, "secretname")
	f.chore("Dishes", "2026-11-21", "gentle")
	f.sender.fail = errors.New("dial discord://secretname@123456: refused")
	w := f.worker()
	start, _ := time.ParseInLocation("2006-01-02 15:04:05", "2026-11-21 09:00:30", ams)
	f.tickAt(w, "2026-11-21 08:59:00")
	at := start
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		f.set(at)
		if err := w.tick(f.ctx); err != nil {
			t.Fatal(err)
		}
		log, err := f.st.ListUserDeliveries(f.ctx, f.owner, 10)
		if err != nil || len(log) != 1 || log[0].Attempts != attempt {
			t.Fatalf("attempt %d: log = %+v, %v", attempt, log, err)
		}
		d := log[0]
		if strings.Contains(d.LastError, "secretname") {
			t.Fatalf("channel URL in delivery log: %q", d.LastError)
		}
		if attempt < maxAttempts {
			if d.Status != store.DeliveryPending || !d.NextAttemptAt.Equal(at.Add(Backoff(attempt))) {
				t.Fatalf("attempt %d: %+v", attempt, d)
			}
			// Nothing happens before the backoff elapses.
			f.set(at.Add(Backoff(attempt) - time.Second))
			if err := w.tick(f.ctx); err != nil {
				t.Fatal(err)
			}
			at = at.Add(Backoff(attempt))
		} else if d.Status != store.DeliveryFailed {
			t.Fatalf("not failed after %d attempts: %+v", attempt, d)
		}
	}
	if Backoff(1) != time.Minute || Backoff(2) != 2*time.Minute || Backoff(30) != time.Hour {
		t.Fatal("backoff schedule")
	}
}

func TestEarlyCompletionCancelsPending(t *testing.T) {
	f := newFixture(t)
	f.channel(f.owner, "owner")
	c := f.chore("Dishes", "2026-11-21", "gentle")
	quiet := &store.QuietHours{StartMin: 8 * 60, EndMin: 10 * 60}
	if _, err := f.st.UpdateHousehold(f.ctx, f.owner, f.hid, store.HouseholdUpdate{QuietHours: &quiet}); err != nil {
		t.Fatal(err)
	}
	w := f.worker()
	f.tickAt(w, "2026-11-21 08:59:00")
	f.tickAt(w, "2026-11-21 09:01:00") // pending until 10:00
	next := store.OccurrenceInstant(schedule.Date{Year: 2026, Month: 11, Day: 28}, ams)
	if _, err := f.st.CompleteChore(f.ctx, f.owner, f.hid, c.ID, store.CompletionInput{CompletedAt: f.clock(), Source: store.SourceApp,
		DueDate: "2026-11-28", NextDue: next}); err != nil {
		t.Fatal(err)
	}
	f.tickAt(w, "2026-11-21 10:00:30")
	if got := f.sender.take(); len(got) != 0 {
		t.Fatalf("reminder for a completed occurrence sent: %+v", got)
	}
}

func TestQuotaEnforced(t *testing.T) {
	f := newFixture(t)
	f.channel(f.owner, "owner")
	for i := range 3 {
		f.chore("Chore"+string(rune('A'+i)), "2026-11-21", "gentle")
	}
	w := New(Config{Store: f.st, DB: f.db, Sender: f.sender, Keyring: f.ring, Now: f.clock,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Limits: nil})
	// Pre-fill the household's hourly quota.
	var ds []store.NewDelivery
	now, _ := time.ParseInLocation("2006-01-02 15:04:05", "2026-11-21 08:30:00", ams)
	for i := range 100 {
		ds = append(ds, store.NewDelivery{Key: "fill" + string(rune(i+'0')) + ids.New(), HouseholdID: f.hid, Kind: schedule.OnDue,
			DueDate: "2026-11-21", FireAt: now, NextAttemptAt: now.Add(24 * time.Hour)})
	}
	if _, err := f.st.EnqueueDeliveries(f.ctx, ds, store.Quota{}, now); err != nil {
		t.Fatal(err)
	}
	f.tickAt(w, "2026-11-21 08:59:00")
	f.tickAt(w, "2026-11-21 09:01:00")
	if got := f.sender.take(); len(got) != 0 {
		t.Fatalf("sent over quota: %d", len(got))
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	f := newFixture(t)
	f.set(time.Date(2026, 11, 21, 8, 0, 0, 0, time.UTC))
	w := f.worker()
	ctx, cancel := context.WithCancel(f.ctx)
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	deadline := time.Now().Add(10 * time.Second)
	for !w.leading.Load() {
		if time.Now().After(deadline) {
			t.Fatal("never led")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return")
	}
	if err := New(Config{}).Run(f.ctx); err == nil {
		t.Fatal("incomplete config accepted")
	}
}

// A chore saved with a chosen reminder time (explicit rows built from a preset
// with every minute replaced) fires at that local time, also on the day the
// clocks change.
func TestChosenReminderTimeIncludingDST(t *testing.T) {
	f := newFixture(t)
	f.channel(f.owner, "owner")
	for _, due := range []string{"2026-03-29", "2026-03-30"} {
		d, _ := schedule.ParseDate(due)
		_, err := f.st.CreateChore(f.ctx, f.owner, f.hid, store.ChoreInput{
			Name: "Timed " + due, Kind: store.KindRecurring, ScheduleType: store.ScheduleInterval, IntervalN: 1, IntervalUnit: "week",
			Anchor: store.AnchorFromCompletion, FirstDue: due, DueDate: due, NextDue: store.OccurrenceInstant(d, ams),
			ReminderPreset: PresetCustom, Reminders: []schedule.Reminder{{Kind: schedule.OnDue, Minute: 7*60 + 30}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	w := f.worker()
	f.tickAt(w, "2026-03-29 07:29:00")
	f.sender.take()
	f.tickAt(w, "2026-03-29 07:29:59") // 07:29:59 CEST: the day has shifted by an hour
	if n := len(f.sender.take()); n != 0 {
		t.Fatalf("sent %d before 07:30", n)
	}
	f.tickAt(w, "2026-03-29 07:30:01")
	if n := len(f.sender.take()); n != 1 {
		t.Fatalf("sent %d at 07:30 on the DST day", n)
	}
	f.tickAt(w, "2026-03-29 09:00:01") // the preset's own 09:00 does not fire
	if n := len(f.sender.take()); n != 0 {
		t.Fatalf("sent %d at 09:00", n)
	}
	f.tickAt(w, "2026-03-30 07:30:01")
	if n := len(f.sender.take()); n != 1 {
		t.Fatalf("sent %d at 07:30 the day after", n)
	}
}
