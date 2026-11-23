package scheduler

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/store"
	"github.com/NexusBetweenVoids/gunkly/internal/webhook"
)

type hit struct {
	header http.Header
	body   []byte
}

// whEnv is a fixture with webhooks on, one webhook aimed at a loopback test
// server (so the sender's guard is off) and a worker with the real sender.
type whEnv struct {
	*fixture
	srv    *httptest.Server
	mu     sync.Mutex
	hits   []hit
	status int // what the server answers
	hook   store.Webhook
	secret string
	w      *Worker
}

func newWHEnv(t *testing.T, events ...string) *whEnv {
	t.Helper()
	e := &whEnv{fixture: newFixture(t), status: 200}
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		e.mu.Lock()
		e.hits = append(e.hits, hit{r.Header.Clone(), b})
		st := e.status
		e.mu.Unlock()
		w.WriteHeader(st)
	}))
	t.Cleanup(e.srv.Close)
	on := true
	if err := e.st.UpdateInstanceSettings(e.ctx, store.SettingsUpdate{WebhooksEnabled: &on}); err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		events = []string{store.EventChoreCreated}
	}
	p, err := webhook.Prepare(e.ring, e.srv.URL+"/hook", "test", events, false) // guard off: http to loopback
	if err != nil {
		t.Fatal(err)
	}
	p.Max = 10
	e.secret = p.Secret
	if e.hook, err = e.st.CreateWebhook(e.ctx, e.owner, e.hid, p.NewWebhook); err != nil {
		t.Fatal(err)
	}
	e.w = New(Config{Store: e.st, DB: e.db, Sender: e.sender, Keyring: e.ring, Now: e.clock,
		Webhooks: webhook.NewSender(func(context.Context) bool { return false }, "test", e.clock),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)), Tick: 20 * time.Millisecond})
	return e
}

func (e *whEnv) setStatus(s int) {
	e.mu.Lock()
	e.status = s
	e.mu.Unlock()
}

func (e *whEnv) take() []hit {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := e.hits
	e.hits = nil
	return out
}

func (e *whEnv) log() []store.WebhookDelivery {
	e.t.Helper()
	l, err := e.st.WebhookDeliveries(e.ctx, e.owner, e.hid, e.hook.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return l
}

func TestWebhookDeliveredSignedOnce(t *testing.T) {
	e := newWHEnv(t)
	now := time.Now().Add(time.Minute)
	e.set(now)
	e.chore("Dishes", "2026-11-21", "none")
	e.w.sendWebhooks(e.ctx)
	e.w.sendWebhooks(e.ctx) // nothing more to send
	hits := e.take()
	if len(hits) != 1 {
		t.Fatalf("%d requests", len(hits))
	}
	h := hits[0]
	var p store.WebhookPayload
	if err := json.Unmarshal(h.body, &p); err != nil {
		t.Fatal(err)
	}
	if p.Type != store.EventChoreCreated || p.Version != 1 || p.Data.Chore == nil || p.Data.Chore.Name != "Dishes" || p.Data.Actor == nil {
		t.Fatalf("payload %s", h.body)
	}
	if h.header.Get("Gunkly-Event") != store.EventChoreCreated || h.header.Get("Gunkly-Delivery") == "" || h.header.Get("User-Agent") != "Gunkly-Webhook/test" ||
		h.header.Get("Content-Type") != "application/json" {
		t.Fatalf("headers %v", h.header)
	}
	if err := webhook.Verify(e.secret, h.header.Get("Gunkly-Signature"), h.body, now); err != nil {
		t.Fatalf("signature: %v", err)
	}
	l := e.log()
	if len(l) != 1 || l[0].Status != store.DeliverySent || l[0].HTTPStatus != 200 || l[0].Attempts != 1 || l[0].EventID != p.ID {
		t.Fatalf("log %+v", l)
	}
	if got, _ := e.st.Webhook(e.ctx, e.owner, e.hid, e.hook.ID); got.LastSuccessAt.IsZero() {
		t.Fatal("no last success")
	}
}

func TestWebhookRetriesWithBackoffThenGivesUp(t *testing.T) {
	e := newWHEnv(t)
	e.setStatus(503)
	at := time.Now().Add(time.Minute)
	e.set(at)
	e.chore("Dishes", "2026-11-21", "none")
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		e.set(at)
		e.w.sendWebhooks(e.ctx)
		if n := len(e.take()); n != 1 {
			t.Fatalf("attempt %d: %d requests", attempt, n)
		}
		l := e.log()
		if l[0].Attempts != attempt || l[0].HTTPStatus != 503 || l[0].Error != "HTTP 503" {
			t.Fatalf("attempt %d: log %+v", attempt, l[0])
		}
		if attempt == maxAttempts {
			if l[0].Status != store.DeliveryFailed {
				t.Fatalf("not given up: %+v", l[0])
			}
			break
		}
		if l[0].Status != store.DeliveryPending {
			t.Fatalf("attempt %d: %+v", attempt, l[0])
		}
		// Not retried before the backoff has passed, retried right after it.
		wait := Backoff(attempt)
		e.set(at.Add(wait - time.Second))
		e.w.sendWebhooks(e.ctx)
		if n := len(e.take()); n != 0 {
			t.Fatalf("attempt %d retried %v early", attempt, time.Second)
		}
		at = at.Add(wait)
	}
	// Retries of one event carry the same delivery ID and body.
	e.set(at.Add(24 * time.Hour))
	e.w.sendWebhooks(e.ctx)
	if n := len(e.take()); n != 0 {
		t.Fatalf("%d requests after giving up", n)
	}
	// It then recovers on the next event: the failure count resets on success.
	e.setStatus(200)
	e.set(at.Add(25 * time.Hour))
	e.chore("Bins", "2026-11-22", "none")
	e.w.sendWebhooks(e.ctx)
	if n := len(e.take()); n != 1 {
		t.Fatalf("%d requests after recovery", n)
	}
	if got, _ := e.st.Webhook(e.ctx, e.owner, e.hid, e.hook.ID); got.Failures != 0 || !got.Active {
		t.Fatalf("after recovery %+v", got)
	}
}

func TestWebhookRetryKeepsDeliveryIDAndBody(t *testing.T) {
	e := newWHEnv(t)
	e.setStatus(500)
	at := time.Now().Add(time.Minute)
	e.set(at)
	e.chore("Dishes", "2026-11-21", "none")
	e.w.sendWebhooks(e.ctx)
	e.setStatus(200)
	e.set(at.Add(Backoff(1)))
	e.w.sendWebhooks(e.ctx)
	hits := e.take()
	if len(hits) != 2 || hits[0].header.Get("Gunkly-Delivery") != hits[1].header.Get("Gunkly-Delivery") || string(hits[0].body) != string(hits[1].body) {
		t.Fatalf("hits %+v", hits)
	}
	if l := e.log(); len(l) != 1 || l[0].Status != store.DeliverySent || l[0].Attempts != 2 {
		t.Fatalf("log %+v", l)
	}
}

func TestWebhookAutoDisablesAfterConsecutiveFailures(t *testing.T) {
	e := newWHEnv(t)
	e.setStatus(404) // a permanent failure: one attempt per event
	at := time.Now().Add(time.Minute)
	for i := range store.WebhookMaxFailures {
		e.set(at)
		e.chore("c", "2026-11-21", "none")
		e.w.sendWebhooks(e.ctx)
		if n := len(e.take()); n != 1 {
			t.Fatalf("event %d: %d requests", i, n)
		}
	}
	got, err := e.st.Webhook(e.ctx, e.owner, e.hid, e.hook.ID)
	if err != nil || got.Active || got.AutoDisabledAt.IsZero() {
		t.Fatalf("%+v %v", got, err)
	}
	e.chore("after", "2026-11-21", "none")
	e.w.sendWebhooks(e.ctx)
	if n := len(e.take()); n != 0 {
		t.Fatalf("%d requests to a disabled webhook", n)
	}
	var audits int
	if err := e.db.QueryRowContext(e.ctx, e.db.Dialect.Rebind("SELECT count(*) FROM audit_log WHERE event = ?"), store.AuditWebhookAutoOff).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audit events: %d, %v", audits, err)
	}
}

func TestWebhooksOffSendsNothingAndDropsTheQueue(t *testing.T) {
	e := newWHEnv(t)
	at := time.Now().Add(time.Minute)
	e.set(at)
	e.chore("Dishes", "2026-11-21", "none") // queued while on
	off := false
	if err := e.st.UpdateInstanceSettings(e.ctx, store.SettingsUpdate{WebhooksEnabled: &off}); err != nil {
		t.Fatal(err)
	}
	e.w.sendWebhooks(e.ctx)
	e.chore("Bins", "2026-11-22", "none") // not even queued
	e.w.sendWebhooks(e.ctx)
	if n := len(e.take()); n != 0 {
		t.Fatalf("%d requests while off", n)
	}
	on := true
	if err := e.st.UpdateInstanceSettings(e.ctx, store.SettingsUpdate{WebhooksEnabled: &on}); err != nil {
		t.Fatal(err)
	}
	e.w.sendWebhooks(e.ctx)
	if n := len(e.take()); n != 0 {
		t.Fatalf("stale events replayed after switching on: %d", n)
	}
}

func TestWebhookOverdueFromTick(t *testing.T) {
	e := newWHEnv(t, store.EventChoreOverdue)
	e.chore("Dishes", "2026-11-21", "none")
	w := e.w
	e.tickAt(w, "2026-11-21 23:59:30") // starts the clock; the due date has not ended
	e.tickAt(w, "2026-11-22 00:00:30")
	e.tickAt(w, "2026-11-22 00:01:30")
	// The worker's clock is in the past relative to the store's rows' created_at;
	// claim at a later instant.
	e.set(time.Now().Add(time.Hour))
	w.sendWebhooks(e.ctx)
	hits := e.take()
	if len(hits) != 1 {
		t.Fatalf("%d overdue requests", len(hits))
	}
	var p store.WebhookPayload
	if err := json.Unmarshal(hits[0].body, &p); err != nil || p.Type != store.EventChoreOverdue || p.Data.Chore.DueDate != "2026-11-21" || p.Data.Actor != nil {
		t.Fatalf("payload %s, %v", hits[0].body, err)
	}
}
