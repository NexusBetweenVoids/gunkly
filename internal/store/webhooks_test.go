package store_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/db"
	"github.com/NexusBetweenVoids/gunkly/internal/db/dbtest"
	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/schedule"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

type whFixture struct {
	t     *testing.T
	ctx   context.Context
	d     *db.DB
	s     *store.Store
	owner store.Actor
	hh    string
}

func newWH(t *testing.T) *whFixture {
	t.Helper()
	d := dbtest.Open(t)
	f := &whFixture{t: t, ctx: context.Background(), d: d, s: store.New(d)}
	f.owner = mkUser(t, f.s, "owner")
	f.hh = mkHousehold(t, f.s, f.owner, "Home")
	return f
}

func (f *whFixture) enable(on bool) {
	f.t.Helper()
	noErr(f.t, f.s.UpdateInstanceSettings(f.ctx, store.SettingsUpdate{WebhooksEnabled: &on}))
}

func (f *whFixture) add(events ...string) store.Webhook {
	f.t.Helper()
	if len(events) == 0 {
		events = store.WebhookEvents
	}
	w, err := f.s.CreateWebhook(f.ctx, f.owner, f.hh, store.NewWebhook{ID: ids.New(), URLCiphertext: "u", URLHint: "example.com",
		SecretCiphertext: "s", Events: events, Max: 10})
	noErr(f.t, err)
	return w
}

func (f *whFixture) count(where string, args ...any) int {
	f.t.Helper()
	var n int
	noErr(f.t, f.d.QueryRowContext(f.ctx, f.d.Dialect.Rebind("SELECT count(*) FROM "+where), args...).Scan(&n))
	return n
}

// claim leases everything due and returns the payloads by event type.
func (f *whFixture) claim(now time.Time) []store.ClaimedWebhookDelivery {
	f.t.Helper()
	ds, err := f.s.ClaimWebhookDeliveries(f.ctx, now, time.Minute, 100)
	noErr(f.t, err)
	return ds
}

func TestWebhookCRUDAndRoles(t *testing.T) {
	f := newWH(t)
	member := mkUser(t, f.s, "member")
	noErr(t, f.s.AddMember(f.ctx, f.owner, f.hh, member.UserID, store.RoleMember))
	outsider := mkUser(t, f.s, "outsider")
	w := f.add(store.EventChoreCompleted)
	if !w.Active || w.URLHint != "example.com" || len(w.Events) != 1 {
		t.Fatalf("created %+v", w)
	}
	// Members and outsiders can do nothing; an outsider cannot tell the household exists.
	_, err := f.s.ListWebhooks(f.ctx, member, f.hh)
	is(t, err, store.ErrForbidden)
	_, err = f.s.ListWebhooks(f.ctx, outsider, f.hh)
	is(t, err, store.ErrNotFound)
	_, err = f.s.Webhook(f.ctx, member, f.hh, w.ID)
	is(t, err, store.ErrForbidden)
	is(t, f.s.DeleteWebhook(f.ctx, member, f.hh, w.ID), store.ErrForbidden)
	_, err = f.s.CreateWebhook(f.ctx, member, f.hh, store.NewWebhook{ID: ids.New(), URLCiphertext: "u", URLHint: "h", SecretCiphertext: "s", Events: store.WebhookEvents, Max: 5})
	is(t, err, store.ErrForbidden)
	// Another household's webhook is not reachable through this one.
	other := mkHousehold(t, f.s, f.owner, "Other")
	_, err = f.s.Webhook(f.ctx, f.owner, other, w.ID)
	is(t, err, store.ErrNotFound)
	_, err = f.s.UpdateWebhook(f.ctx, f.owner, other, w.ID, store.WebhookUpdate{Active: new(bool)})
	is(t, err, store.ErrNotFound)

	// Update, validation.
	desc := "dishwasher"
	w2, err := f.s.UpdateWebhook(f.ctx, f.owner, f.hh, w.ID, store.WebhookUpdate{Description: &desc, Events: []string{store.EventChoreSkipped, store.EventChoreCompleted}})
	noErr(t, err)
	if w2.Description != desc || strings.Join(w2.Events, ",") != "chore.completed,chore.skipped" {
		t.Fatalf("updated %+v", w2)
	}
	_, err = f.s.UpdateWebhook(f.ctx, f.owner, f.hh, w.ID, store.WebhookUpdate{Events: []string{"chore.exploded"}})
	is(t, err, store.ErrInvalid)
	_, err = f.s.UpdateWebhook(f.ctx, f.owner, f.hh, w.ID, store.WebhookUpdate{Events: []string{}})
	is(t, err, store.ErrInvalid)

	// Limit.
	for i := 1; i < 10; i++ {
		f.add()
	}
	_, err = f.s.CreateWebhook(f.ctx, f.owner, f.hh, store.NewWebhook{ID: ids.New(), URLCiphertext: "u", URLHint: "h", SecretCiphertext: "s", Events: store.WebhookEvents, Max: 10})
	is(t, err, store.ErrLimit)

	noErr(t, f.s.DeleteWebhook(f.ctx, f.owner, f.hh, w.ID))
	is(t, f.s.DeleteWebhook(f.ctx, f.owner, f.hh, w.ID), store.ErrNotFound)
}

func TestWebhookEventsAndPayloads(t *testing.T) {
	f := newWH(t)
	f.enable(true)
	f.add() // all events
	member := mkUser(t, f.s, "member")
	noErr(t, f.s.AddMember(f.ctx, f.owner, f.hh, member.UserID, store.RoleMember))

	in := choreIn("Dishes")
	in.Assignees = []string{member.UserID}
	c := must(f.s.CreateChore(f.ctx, member, f.hh, in))
	in.Name = "Dishes!"
	must(f.s.UpdateChore(f.ctx, member, f.hh, c.ID, in))
	must(f.s.SnoozeChore(f.ctx, member, f.hh, c.ID, must(schedule.ParseDate("2036-01-01"))))
	must(f.s.SkipChore(f.ctx, member, f.hh, c.ID))
	comp, _, err := f.s.CompleteChoreAuto(f.ctx, member, f.hh, c.ID, "secret note", store.SourceApp, "")
	noErr(t, err)
	must(f.s.RevertChoreCompletion(f.ctx, member, f.hh, c.ID, comp.ID))
	noErr(t, f.s.DeleteChore(f.ctx, member, f.hh, c.ID))

	got := map[string]store.WebhookPayload{}
	for _, d := range f.claim(time.Now().Add(time.Hour)) {
		var p store.WebhookPayload
		noErr(t, json.Unmarshal([]byte(d.Payload), &p))
		if p.Type != d.EventType || p.ID != d.EventID || p.Version != 1 || p.HouseholdID != f.hh || p.CreatedAt == "" {
			t.Errorf("envelope %+v vs %+v", p, d)
		}
		if strings.Contains(d.Payload, "secret note") || strings.Contains(d.Payload, "@example.com") {
			t.Errorf("payload leaks a note or an address: %s", d.Payload)
		}
		got[p.Type] = p
	}
	for _, e := range []string{store.EventChoreCreated, store.EventChoreUpdated, store.EventChoreSnoozed, store.EventChoreSkipped,
		store.EventChoreCompleted, store.EventChoreCompletionRevert, store.EventChoreDeleted} {
		p, ok := got[e]
		if !ok {
			t.Errorf("no %s event", e)
			continue
		}
		if p.Data.Chore == nil || p.Data.Chore.ID != c.ID || len(p.Data.Chore.Assignees) != 1 || p.Data.Chore.Assignees[0] != member.UserID {
			t.Errorf("%s chore = %+v", e, p.Data.Chore)
		}
		if p.Data.Actor == nil || p.Data.Actor.ID != member.UserID || p.Data.Actor.DisplayName != "member" {
			t.Errorf("%s actor = %+v", e, p.Data.Actor)
		}
	}
	if got[store.EventChoreUpdated].Data.Chore.Name != "Dishes!" || got[store.EventChoreDeleted].Data.Chore.Name != "Dishes!" {
		t.Errorf("names: %+v %+v", got[store.EventChoreUpdated].Data.Chore, got[store.EventChoreDeleted].Data.Chore)
	}
	if cp := got[store.EventChoreCompleted].Data.Completion; cp == nil || cp.ID != comp.ID || cp.Source != store.SourceApp || cp.RevertedAt != "" {
		t.Errorf("completed = %+v", cp)
	}
	if cp := got[store.EventChoreCompletionRevert].Data.Completion; cp == nil || cp.ID != comp.ID || cp.RevertedAt == "" {
		t.Errorf("reverted = %+v", cp)
	}
	if got[store.EventChoreCreated].Data.Completion != nil {
		t.Error("created event carries a completion")
	}
}

func TestWebhookSubscriptionAndSwitch(t *testing.T) {
	f := newWH(t)
	a := f.add(store.EventChoreCompleted)
	f.add(store.EventChoreCreated)

	// Off (the default): nothing is queued.
	c := must(f.s.CreateChore(f.ctx, f.owner, f.hh, choreIn("Off")))
	if n := f.count("webhook_deliveries"); n != 0 {
		t.Fatalf("%d deliveries while webhooks are off", n)
	}
	f.enable(true)
	must(f.s.CreateChore(f.ctx, f.owner, f.hh, choreIn("On")))
	if n := f.count("webhook_deliveries"); n != 1 { // only the chore.created subscriber
		t.Fatalf("%d deliveries, want 1", n)
	}
	_, _, err := f.s.CompleteChoreAuto(f.ctx, f.owner, f.hh, c.ID, "", store.SourceApp, "")
	noErr(t, err)
	if n := f.count("webhook_deliveries WHERE webhook_id = ?", a.ID); n != 1 {
		t.Fatalf("%d deliveries for the completed subscriber", n)
	}
	// An inactive webhook gets nothing.
	off := false
	_, err = f.s.UpdateWebhook(f.ctx, f.owner, f.hh, a.ID, store.WebhookUpdate{Active: &off})
	noErr(t, err)
	_, _, err = f.s.CompleteChoreAuto(f.ctx, f.owner, f.hh, c.ID, "", store.SourceApp, "")
	noErr(t, err)
	if n := f.count("webhook_deliveries WHERE webhook_id = ?", a.ID); n != 1 {
		t.Fatalf("%d deliveries after switching off", n)
	}
	// Deleting the household's webhook removes its log; deleting the chore keeps no stale rows elsewhere.
	noErr(t, f.s.DeleteWebhook(f.ctx, f.owner, f.hh, a.ID))
	if n := f.count("webhook_deliveries WHERE webhook_id = ?", a.ID); n != 0 {
		t.Fatalf("%d deliveries left after delete", n)
	}
	// Switching the instance setting off drops the queue.
	f.enable(false)
	noErr(t, f.s.DropPendingWebhookDeliveries(f.ctx))
	if n := f.count("webhook_deliveries"); n != 0 {
		t.Fatalf("%d deliveries after drop", n)
	}
}

func TestWebhookOverdueIsIdempotent(t *testing.T) {
	f := newWH(t)
	f.enable(true)
	f.add(store.EventChoreOverdue)
	ams, _ := time.LoadLocation("Europe/Amsterdam")
	in := choreIn("Bins")
	in.NextDue = store.OccurrenceInstant(must(schedule.ParseDate("2026-11-21")), ams)
	c := must(f.s.CreateChore(f.ctx, f.owner, f.hh, in))
	if n := f.count("webhook_deliveries"); n != 0 { // chore.created is not subscribed
		t.Fatalf("%d", n)
	}
	end := time.Date(2026, 11, 22, 0, 0, 0, 0, ams) // the due date ends at local midnight
	emit := func(from, to time.Time) { noErr(t, f.s.EmitOverdueWebhooks(f.ctx, from, to)) }

	emit(end.Add(-time.Hour), end.Add(-time.Second)) // still the due date
	if n := f.count("webhook_deliveries"); n != 0 {
		t.Fatalf("emitted early: %d", n)
	}
	emit(end.Add(-time.Minute), end.Add(time.Minute))
	emit(end.Add(-time.Minute), end.Add(time.Minute)) // the same window again
	emit(end.Add(-time.Hour), end.Add(2*time.Hour))   // an overlapping one
	if n := f.count("webhook_deliveries"); n != 1 {
		t.Fatalf("%d overdue deliveries, want 1", n)
	}
	ds := f.claim(time.Now().Add(time.Hour))
	var p store.WebhookPayload
	noErr(t, json.Unmarshal([]byte(ds[0].Payload), &p))
	if p.Type != store.EventChoreOverdue || p.Data.Chore.ID != c.ID || p.Data.Actor != nil {
		t.Fatalf("payload %+v", p)
	}
}

func TestWebhookFinishRetryAndAutoDisable(t *testing.T) {
	f := newWH(t)
	f.enable(true)
	w := f.add(store.EventChoreCreated)
	now := time.Now()
	for i := range store.WebhookMaxFailures {
		must(f.s.CreateChore(f.ctx, f.owner, f.hh, choreIn("c")))
		ds := f.claim(now.Add(time.Hour))
		if len(ds) != 1 {
			t.Fatalf("round %d: claimed %d", i, len(ds))
		}
		off, err := f.s.FinishWebhookDelivery(f.ctx, ds[0].ID, store.WebhookResult{HTTPStatus: 500, DurationMS: 7, Error: "HTTP 500"})
		noErr(t, err)
		if off != (i == store.WebhookMaxFailures-1) {
			t.Fatalf("round %d: disabled = %v", i, off)
		}
	}
	got := must(f.s.Webhook(f.ctx, f.owner, f.hh, w.ID))
	if got.Active || got.AutoDisabledAt.IsZero() || got.Failures != store.WebhookMaxFailures {
		t.Fatalf("after failures: %+v", got)
	}
	if n := f.count("audit_log WHERE event = ? AND household_id = ?", store.AuditWebhookAutoOff, f.hh); n != 1 {
		t.Fatalf("%d auto-disable audit events", n)
	}
	// A disabled webhook gets no more events; turning it on starts afresh.
	must(f.s.CreateChore(f.ctx, f.owner, f.hh, choreIn("later")))
	if n := f.count("webhook_deliveries WHERE status = 'pending'"); n != 0 {
		t.Fatalf("%d pending after switch-off", n)
	}
	on := true
	got = must(f.s.UpdateWebhook(f.ctx, f.owner, f.hh, w.ID, store.WebhookUpdate{Active: &on}))
	if !got.Active || got.Failures != 0 || !got.AutoDisabledAt.IsZero() {
		t.Fatalf("after re-enable: %+v", got)
	}
	// A success resets the count; a retry keeps the delivery pending.
	must(f.s.CreateChore(f.ctx, f.owner, f.hh, choreIn("again")))
	ds := f.claim(now.Add(time.Hour))
	_, err := f.s.FinishWebhookDelivery(f.ctx, ds[0].ID, store.WebhookResult{HTTPStatus: 503, Error: "HTTP 503", RetryAt: now.Add(2 * time.Hour)})
	noErr(t, err)
	if len(f.claim(now.Add(time.Hour))) != 0 || len(f.claim(now.Add(3*time.Hour))) != 1 {
		t.Fatal("the retry was not scheduled")
	}
	_, err = f.s.FinishWebhookDelivery(f.ctx, ds[0].ID, store.WebhookResult{OK: true, HTTPStatus: 204})
	noErr(t, err)
	got = must(f.s.Webhook(f.ctx, f.owner, f.hh, w.ID))
	if got.Failures != 0 || got.LastSuccessAt.IsZero() {
		t.Fatalf("after success: %+v", got)
	}
	log := must(f.s.WebhookDeliveries(f.ctx, f.owner, f.hh, w.ID))
	if len(log) == 0 || log[0].Status != "sent" || log[0].Attempts != 2 || log[0].HTTPStatus != 204 {
		t.Fatalf("log[0] = %+v", log)
	}
}

func TestWebhookLogIsPruned(t *testing.T) {
	f := newWH(t)
	w := f.add()
	for range store.WebhookLogSize + 10 {
		_, err := f.s.RecordWebhookTest(f.ctx, f.owner, f.hh, w.ID, ids.New(), store.WebhookResult{OK: true, HTTPStatus: 200})
		noErr(t, err)
	}
	if log := must(f.s.WebhookDeliveries(f.ctx, f.owner, f.hh, w.ID)); len(log) != store.WebhookLogSize {
		t.Fatalf("%d entries kept", len(log))
	}
	if n := f.count("webhook_deliveries"); n != store.WebhookLogSize {
		t.Fatalf("%d rows kept", n)
	}
	_, err := f.s.WebhookDeliveries(f.ctx, f.owner, f.hh, "0190f0e0-0000-7000-8000-000000000000")
	is(t, err, store.ErrNotFound)
}
