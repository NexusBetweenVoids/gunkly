package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// abuseEnv is a mail environment with an admin (root, confirmed email) and a member (ann) with a household.
type abuseEnv struct {
	*mailEnv
	ann  *client
	hh   string
	root *client
	annU store.User
}

func newAbuseEnv(t *testing.T) *abuseEnv {
	t.Helper()
	e := &abuseEnv{mailEnv: newMailEnv(t)}
	e.user("root", true)
	e.verified("root")
	e.annU = e.user("ann", false)
	e.ann = e.signedIn("ann")
	st, v := e.ann.do("POST", "/api/v1/households", map[string]string{"name": "Home", "timezone": "UTC"})
	if st != 201 {
		t.Fatalf("household: %d %v", st, v)
	}
	e.hh = field(v, "id").(string)
	e.root = e.signedIn("root")
	e.mails()
	return e
}

// events lists the event names of the admin health page, newest first.
func (e *abuseEnv) events() []string {
	e.t.Helper()
	_, v := e.root.do("GET", "/api/v1/admin/health", nil)
	var out []string
	evs, _ := v.(map[string]any)["securityEvents"].([]any)
	for _, ev := range evs {
		out = append(out, str(ev, "event"))
	}
	return out
}

func (e *abuseEnv) wantNoAlert() {
	e.t.Helper()
	if ms := e.mails(); len(ms) != 0 {
		e.t.Fatalf("unexpected mail: %+v", ms)
	}
	if evs := e.events(); len(evs) != 0 {
		e.t.Fatalf("unexpected events: %v", evs)
	}
}

func TestNotificationLimitAlert(t *testing.T) {
	e := newAbuseEnv(t)
	hit := store.QuotaHit{HouseholdID: e.hh, Hours: 1, Max: 100}
	ctx := context.Background()

	e.srv.NotificationLimitReached(ctx, hit)
	m := e.oneMail("root@example.com")
	if !strings.Contains(m.Subject, "notification limit") || !strings.Contains(m.Body, e.hh) || !strings.Contains(m.Body, "100") {
		t.Fatalf("mail: %+v", m)
	}
	if evs := e.events(); len(evs) != 1 || evs[0] != store.AuditNotifyLimit {
		t.Fatalf("events: %v", evs)
	}
	// Again within the hour and again later the same day: no second email.
	e.srv.NotificationLimitReached(ctx, hit)
	e.clock.Add(2 * time.Hour)
	e.srv.NotificationLimitReached(ctx, store.QuotaHit{HouseholdID: e.hh, Hours: 24, Max: 250})
	if ms := e.mails(); len(ms) != 0 {
		t.Fatalf("second alert mailed: %+v", ms)
	}
	// The next day it mails again.
	e.clock.Add(24 * time.Hour)
	e.srv.NotificationLimitReached(ctx, hit)
	e.oneMail("root@example.com")
}

func TestChannelsBurstAlert(t *testing.T) {
	e := newAbuseEnv(t)
	base := "/api/v1/households/" + e.hh + "/channels"
	add := func() {
		t.Helper()
		st, v := e.ann.do("POST", base, map[string]string{"url": discordURL})
		expect(t, st, v, 201, "")
	}
	for range channelsBurstThreshold - 1 {
		add()
	}
	e.wantNoAlert()
	add()
	m := e.oneMail("root@example.com")
	if !strings.Contains(m.Body, "ann") || !strings.Contains(m.Body, e.hh) {
		t.Fatalf("mail: %+v", m)
	}
	if evs := e.events(); len(evs) != 1 || evs[0] != store.AuditChannelsBurst {
		t.Fatalf("events: %v", evs)
	}
	add() // more in the same period: recorded, not mailed
	if ms := e.mails(); len(ms) != 0 {
		t.Fatalf("second alert mailed: %+v", ms)
	}
}

func TestChannelsSpreadOutNoAlert(t *testing.T) {
	e := newAbuseEnv(t)
	base := "/api/v1/households/" + e.hh + "/channels"
	for range channelsBurstThreshold + 2 {
		st, v := e.ann.do("POST", base, map[string]string{"url": discordURL})
		expect(t, st, v, 201, "")
		e.clock.Add(20 * time.Minute)
	}
	e.wantNoAlert()
}

func TestTokenBurstAlert(t *testing.T) {
	e := newAbuseEnv(t)
	ann := e.signedInStepUp("ann")
	for range tokenBurstThreshold - 1 {
		mkToken(t, ann, []string{scRead}, e.hh)
	}
	e.wantNoAlert()
	mkToken(t, ann, []string{scRead}, e.hh)
	m := e.oneMail("root@example.com")
	if !strings.Contains(m.Body, "ann") || !strings.Contains(m.Body, "5") {
		t.Fatalf("mail: %+v", m)
	}
	if evs := e.events(); len(evs) != 1 || evs[0] != store.AuditTokenBurst {
		t.Fatalf("events: %v", evs)
	}
	mkToken(t, ann, []string{scRead}, e.hh)
	if ms := e.mails(); len(ms) != 0 {
		t.Fatalf("second alert mailed: %+v", ms)
	}
}

// OAuth grants count too (they are audited by the store when the code is exchanged).
func TestOAuthGrantBurstAlert(t *testing.T) {
	e := newAbuseEnv(t)
	ann := e.annU
	for range tokenBurstThreshold - 1 {
		_ = e.st.Audit(context.Background(), store.AuditEvent{ActorUserID: ann.ID, Event: store.AuditOAuthGrant})
	}
	e.srv.tokenBurstCheck(context.Background(), &call{}, ann)
	e.wantNoAlert()
	_ = e.st.Audit(context.Background(), store.AuditEvent{ActorUserID: ann.ID, Event: store.AuditOAuthGrant})
	e.srv.tokenBurstCheck(context.Background(), &call{}, ann)
	e.oneMail("root@example.com")
	e.srv.tokenBurstCheck(context.Background(), &call{}, ann)
	if ms := e.mails(); len(ms) != 0 {
		t.Fatalf("second alert mailed: %+v", ms)
	}
	if evs := e.events(); len(evs) != 2 || evs[0] != store.AuditTokenBurst {
		t.Fatalf("events: %v", evs)
	}
}

func TestTokenRateLimitedAlert(t *testing.T) {
	e := newAbuseEnv(t)
	secret, id := mkToken(t, e.signedInStepUp("ann"), []string{scRead}, e.hh)
	e.mails()
	e.srv.rl.perMinute = map[string]int{rateToken: 1}
	e.srv.rl.burstOf = map[string]int{rateToken: 1}
	call := func() int {
		st, _, _ := e.bearerDo("GET", "/api/v1/households", secret, nil)
		return st
	}
	if call() != 200 {
		t.Fatal("first request refused")
	}
	for range tokenRateLimitThreshold - 1 {
		if call() != 429 {
			t.Fatal("not rate limited")
		}
	}
	e.wantNoAlert()
	if call() != 429 {
		t.Fatal("not rate limited")
	}
	m := e.oneMail("root@example.com")
	if !strings.Contains(m.Body, id) || !strings.Contains(m.Body, "ann") || strings.Contains(m.Body, secret) {
		t.Fatalf("mail: %+v", m)
	}
	if evs := e.events(); len(evs) != 1 || evs[0] != store.AuditTokenRateLimited {
		t.Fatalf("events: %v", evs)
	}
	// More than a day... within the hour the counter has moved on: no second alert.
	for range tokenRateLimitThreshold {
		call()
	}
	if ms := e.mails(); len(ms) != 0 {
		t.Fatalf("second alert mailed: %+v", ms)
	}
}

func TestHitCounterBounded(t *testing.T) {
	c := newWindowCounter(time.Now, 2)
	if c.hit("a") != 1 || c.hit("a") != 2 || c.hit("b") != 1 || c.hit("c") != 0 {
		t.Fatal("counter")
	}
}
