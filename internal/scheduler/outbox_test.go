package scheduler

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/mail"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

const (
	secretTo   = "someone@example.com"
	secretBody = "Your sign-in code is 424242"
)

var t0 = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

// queue puts one email into the outbox at the fixture's current time.
func (f *fixture) queue(kind string, ttl time.Duration) string {
	f.t.Helper()
	id := ids.New()
	now := f.clock()
	err := f.st.EnqueueMail(f.ctx, store.OutboxMail{ID: id, Kind: kind, UserID: f.owner.UserID,
		Payload: mail.Seal(f.ring, id, mail.Message{To: secretTo, Subject: "Code", Body: secretBody}), ExpiresAt: now.Add(ttl)}, now)
	if err != nil {
		f.t.Fatal(err)
	}
	return id
}

func (f *fixture) queued() int {
	f.t.Helper()
	var n int
	if err := f.db.QueryRowContext(f.ctx, "SELECT count(*) FROM mail_outbox").Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func (f *fixture) outbox(m *fakeMailer, logs *bytes.Buffer) *Outbox {
	return &Outbox{Store: f.st, Mailer: m, Sealer: f.ring, Now: f.clock, Logger: slog.New(slog.NewTextHandler(logs, nil))}
}

func (f *fixture) gaveUp() []store.AuditEvent {
	f.t.Helper()
	evs, err := f.st.ListAuditForUser(f.ctx, f.owner.UserID, 50)
	if err != nil {
		f.t.Fatal(err)
	}
	var out []store.AuditEvent
	for _, e := range evs {
		if e.Event == store.AuditMailGaveUp {
			out = append(out, e)
		}
	}
	return out
}

func TestOutboxBackoffAndGiveUp(t *testing.T) {
	f := newFixture(t)
	var logs bytes.Buffer
	m := &fakeMailer{fail: errors.New("dial " + secretTo + ": refused")}
	ob := f.outbox(m, &logs)
	f.set(t0)
	f.queue("otp", 24*time.Hour)
	f.queue("verify", 24*time.Hour)

	// Every attempt fails; the waits are 1m, 5m, 30m, 2h, 6h.
	at := t0
	for i, wait := range []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour} {
		if got := ob.Process(f.ctx); got != 0 {
			t.Fatalf("attempt %d: sent %d", i+1, got)
		}
		if f.queued() != 2 {
			t.Fatalf("attempt %d: %d queued", i+1, f.queued())
		}
		f.set(at.Add(wait - time.Second))
		ob.Process(f.ctx)
		if len(m.take()) != 0 || strings.Count(logs.String(), "send failed") != 2*(i+1) {
			t.Fatalf("attempt %d: retried %v early (logs: %d failures)", i+1, wait, strings.Count(logs.String(), "send failed"))
		}
		at = at.Add(wait)
		f.set(at)
	}
	ob.Process(f.ctx) // the sixth and last attempt
	if f.queued() != 0 {
		t.Fatalf("still queued after the last attempt: %d", f.queued())
	}
	gone := f.gaveUp()
	if len(gone) != 2 {
		t.Fatalf("audit events: %+v", gone)
	}
	for _, e := range gone {
		if e.Meta["reason"] != store.MailGaveUpAttempts || e.Meta["attempts"] != "6" || (e.Meta["kind"] != "otp" && e.Meta["kind"] != "verify") {
			t.Fatalf("meta: %v", e.Meta)
		}
	}
	// Neither the audit log nor the log ever holds the address or the text.
	for _, s := range []string{logs.String(), gone[0].Meta["kind"] + gone[0].Meta["reason"]} {
		if strings.Contains(s, secretTo) || strings.Contains(s, "424242") || strings.Contains(s, "refused") {
			t.Fatalf("leak: %s", s)
		}
	}
}

func TestOutboxRecoversAndDeletesBody(t *testing.T) {
	f := newFixture(t)
	var logs bytes.Buffer
	m := &fakeMailer{fail: errors.New("down")}
	f.set(t0)
	id := f.queue("verify", 24*time.Hour)

	// What is stored is sealed: no address, no text.
	var payload string
	if err := f.db.QueryRowContext(f.ctx, f.db.Dialect.Rebind("SELECT payload FROM mail_outbox WHERE id = ?"), id).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payload, secretTo) || strings.Contains(payload, "424242") || strings.Contains(payload, "Code") {
		t.Fatalf("outbox row is not sealed: %s", payload)
	}
	f.outbox(m, &logs).Process(f.ctx) // fails once

	// A restart: a new process with new state picks the message up from the database.
	m.mu.Lock()
	m.fail = nil
	m.mu.Unlock()
	f.set(t0.Add(time.Minute))
	restarted := f.outbox(m, &logs)
	if got := restarted.Process(f.ctx); got != 1 {
		t.Fatalf("sent %d after the restart", got)
	}
	out := m.take()
	if len(out) != 1 || out[0].To != secretTo || out[0].Body != secretBody {
		t.Fatalf("delivered: %+v", out)
	}
	if f.queued() != 0 {
		t.Fatalf("the sent message is still stored")
	}
	f.set(t0.Add(24 * time.Hour))
	if restarted.Process(f.ctx) != 0 || len(m.take()) != 0 {
		t.Fatal("sent twice")
	}
	if len(f.gaveUp()) != 0 {
		t.Fatal("a delivered email was audited as given up")
	}
}

func TestOutboxExpiry(t *testing.T) {
	f := newFixture(t)
	var logs bytes.Buffer
	m := &fakeMailer{fail: errors.New("down")}
	ob := f.outbox(m, &logs)
	f.set(t0)
	f.queue("otp", 10*time.Minute)
	ob.Process(f.ctx) // fails; retry at +1m (before the 10 minute expiry)
	f.set(t0.Add(time.Minute))
	ob.Process(f.ctx) // fails; retry at +6m
	f.set(t0.Add(6 * time.Minute))
	ob.Process(f.ctx) // fails; the next retry (+36m) would be after the expiry: given up now
	if f.queued() != 0 {
		t.Fatal("kept an email that cannot be useful any more")
	}
	if g := f.gaveUp(); len(g) != 1 || g[0].Meta["reason"] != store.MailGaveUpExpired || g[0].Meta["attempts"] != "3" {
		t.Fatalf("audit: %+v", g)
	}

	// Not tried at all once past its expiry (the server was down for a while).
	m.mu.Lock()
	m.fail = nil
	m.mu.Unlock()
	f.queue("otp", 10*time.Minute)
	f.set(t0.Add(time.Hour))
	if ob.Process(f.ctx) != 0 || len(m.take()) != 0 {
		t.Fatal("an expired code was sent")
	}
	if g := f.gaveUp(); len(g) != 2 || f.queued() != 0 {
		t.Fatalf("audit: %+v", g)
	}
}

// The Worker drives the outbox on every tick (and between ticks when leading).
func TestWorkerTickSendsQueuedMail(t *testing.T) {
	f := newFixture(t)
	m := &fakeMailer{}
	w := f.emailWorker(m)
	f.set(t0)
	f.queue("verify", time.Hour)
	if err := w.tick(f.ctx); err != nil {
		t.Fatal(err)
	}
	if out := m.take(); len(out) != 1 || out[0].To != secretTo {
		t.Fatalf("delivered: %+v", out)
	}
}

// An unreadable payload (for example sealed under a key that is gone) is given up, not retried forever.
func TestOutboxUnreadablePayload(t *testing.T) {
	f := newFixture(t)
	var logs bytes.Buffer
	f.set(t0)
	err := f.st.EnqueueMail(f.ctx, store.OutboxMail{ID: ids.New(), Kind: "reset", UserID: f.owner.UserID, Payload: "v9:AAAA", ExpiresAt: t0.Add(time.Hour)}, t0)
	if err != nil {
		t.Fatal(err)
	}
	m := &fakeMailer{}
	if f.outbox(m, &logs).Process(f.ctx) != 0 || f.queued() != 0 {
		t.Fatal("unreadable mail kept")
	}
	if g := f.gaveUp(); len(g) != 1 || g[0].Meta["reason"] != store.MailGaveUpUnreadable {
		t.Fatalf("audit: %+v", g)
	}
}
