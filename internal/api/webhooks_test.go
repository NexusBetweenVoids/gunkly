package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
	"github.com/NexusBetweenVoids/gunkly/internal/webhook"
)

type whHit struct {
	header http.Header
	body   []byte
}

// whTarget is a loopback receiver. It is only reachable with the egress guard
// off; with it on, hits must stay at zero.
type whTarget struct {
	srv  *httptest.Server
	mu   sync.Mutex
	hits []whHit
}

func newWHTarget(t *testing.T) *whTarget {
	t.Helper()
	w := &whTarget{}
	w.srv = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.mu.Lock()
		w.hits = append(w.hits, whHit{r.Header.Clone(), b})
		w.mu.Unlock()
		rw.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(w.srv.Close)
	return w
}

func (w *whTarget) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.hits)
}

func setGuard(t *testing.T, e *env, on bool) {
	t.Helper()
	if err := e.st.UpdateInstanceSettings(context.Background(), store.SettingsUpdate{EgressGuard: &on}); err != nil {
		t.Fatal(err)
	}
}

func TestWebhooksOffMeansNotFound(t *testing.T) {
	f := newHH(t)
	f.e.user("root", true)
	root := f.e.signedInStepUp("root")
	st, v := f.a.do("GET", "/api/v1/instance", nil)
	if st != 200 || field(v, "webhooksEnabled") != true {
		t.Fatalf("instance while on: %d %v", st, v)
	}
	st, v = root.do("PATCH", "/api/v1/admin/settings", map[string]any{"webhooksEnabled": false})
	if st != 200 || field(v, "webhooksEnabled") != false {
		t.Fatalf("switch off: %d %v", st, v)
	}
	f.e.stepUp(f.a, "a")
	n := 0
	for _, r := range f.e.srv.routes {
		if !r.policy.webhooks {
			continue
		}
		n++
		method, p := f.path(r.pattern, f.hA)
		for name, c := range map[string]*client{"owner": f.a, "member": f.m, "outsider": f.b} {
			if st, v := c.do(method, p, validBody(r.pattern)); st != 404 {
				t.Errorf("%s as %s while off: %d %v", r.pattern, name, st, v)
			}
		}
	}
	if n != 8 {
		t.Fatalf("%d webhook routes", n)
	}
	if _, v = f.a.do("GET", "/api/v1/instance", nil); field(v, "webhooksEnabled") != nil {
		t.Fatalf("instance advertises webhooks while off: %v", v)
	}
	// And back on.
	st, _ = root.do("PATCH", "/api/v1/admin/settings", map[string]any{"webhooksEnabled": true})
	expect(t, st, nil, 200, "")
	st, v = f.a.do("GET", "/api/v1/households/"+f.hA+"/webhooks", nil)
	expect(t, st, v, 200, "")
}

func TestWebhookCRUDSecretAndAudit(t *testing.T) {
	f := newHH(t)
	base := "/api/v1/households/" + f.hA + "/webhooks"
	body := map[string]any{"url": "https://hooks.example.com/in/very-secret-token", "description": "Home Assistant",
		"events": []string{"chore.completed", "chore.overdue"}}

	// Creating needs a recent step-up.
	st, v := f.a.do("POST", base, body)
	expect(t, st, v, 403, "step_up_required")
	f.e.stepUp(f.a, "a")
	for name, bad := range map[string]map[string]any{
		"http while guarded": {"url": "http://hooks.example.com/", "events": []string{"chore.completed"}},
		"loopback":           {"url": "https://127.0.0.1/", "events": []string{"chore.completed"}},
		"metadata":           {"url": "https://169.254.169.254/latest/", "events": []string{"chore.completed"}},
		"userinfo":           {"url": "https://u:p@hooks.example.com/", "events": []string{"chore.completed"}},
		"empty url":          {"url": "", "events": []string{"chore.completed"}},
	} {
		st, v = f.a.do("POST", base, bad)
		if st != 400 || code(v) != "invalid_webhook_url" {
			t.Errorf("%s: %d %v", name, st, v)
		}
	}
	for name, bad := range map[string]map[string]any{
		"no events":      {"url": "https://hooks.example.com/", "events": []string{}},
		"unknown event":  {"url": "https://hooks.example.com/", "events": []string{"chore.exploded"}},
		"duplicate":      {"url": "https://hooks.example.com/", "events": []string{"chore.created", "chore.created"}},
		"long text":      {"url": "https://hooks.example.com/", "events": []string{"chore.created"}, "description": strings.Repeat("x", 201)},
		"unknown field":  {"url": "https://hooks.example.com/", "events": []string{"chore.created"}, "secret": "mine"},
		"ping not valid": {"url": "https://hooks.example.com/", "events": []string{"ping"}},
	} {
		if st, v = f.a.do("POST", base, bad); st != 400 {
			t.Errorf("%s: %d %v", name, st, v)
		}
	}

	st, v = f.a.do("POST", base, body)
	expect(t, st, v, 201, "")
	id, secret := field(v, "id").(string), field(v, "secret").(string)
	if !strings.HasPrefix(secret, "whsec_") || field(v, "host") != "hooks.example.com" || field(v, "active") != true {
		t.Fatalf("created %v", v)
	}
	if strings.Contains(toJSON(v), "very-secret-token") {
		t.Fatalf("the URL came back: %v", v)
	}
	// The secret is shown once; the URL never.
	for _, p := range []string{base, base + "/" + id} {
		st, v = f.a.do("GET", p, nil)
		expect(t, st, v, 200, "")
		if s := toJSON(v); strings.Contains(s, secret) || strings.Contains(s, "very-secret-token") || strings.Contains(s, "whsec_") {
			t.Fatalf("%s leaks: %s", p, s)
		}
	}
	// The sealed values are bound to the webhook ID and are not the plaintext.
	urlCT, secretCT, err := f.e.st.WebhookSealed(context.Background(), store.Actor{UserID: userID(t, f.e, "a")}, f.hA, id)
	if err != nil || strings.Contains(urlCT+secretCT, "very-secret") || strings.Contains(secretCT, secret) {
		t.Fatalf("sealed: %v", err)
	}
	if got, err := webhook.OpenSecret(f.e.srv.keyring, id, secretCT); err != nil || got != secret {
		t.Fatalf("OpenSecret = %q, %v", got, err)
	}

	// Patch: description, events, active, URL.
	st, v = f.a.do("PATCH", base+"/"+id, map[string]any{"description": "HA", "events": []string{"chore.created"}, "active": false,
		"url": "https://other.example.org/x"})
	if st != 200 || field(v, "description") != "HA" || field(v, "active") != false || field(v, "host") != "other.example.org" {
		t.Fatalf("patch: %d %v", st, v)
	}
	st, v = f.a.do("PATCH", base+"/"+id, map[string]any{"url": "http://other.example.org/x"})
	expect(t, st, v, 400, "invalid_webhook_url")
	st, v = f.a.do("PATCH", base+"/"+id, map[string]any{"events": []string{}})
	expect(t, st, v, 400, "invalid")

	// Rotation needs step-up and replaces the secret.
	f.e.clock.Add(stepUpWindow + time.Minute)
	st, v = f.a.do("POST", base+"/"+id+"/rotate-secret", nil)
	expect(t, st, v, 403, "step_up_required")
	f.e.stepUp(f.a, "a")
	st, v = f.a.do("POST", base+"/"+id+"/rotate-secret", nil)
	expect(t, st, v, 200, "")
	if s2 := field(v, "secret").(string); s2 == secret || !strings.HasPrefix(s2, "whsec_") {
		t.Fatalf("rotated secret %q", s2)
	}

	// Limit.
	if err := f.e.st.UpdateInstanceSettings(context.Background(), store.SettingsUpdate{Limits: map[string]int{string(limits.WebhooksPerHousehold): 2}}); err != nil {
		t.Fatal(err)
	}
	st, v = f.a.do("POST", base, body)
	expect(t, st, v, 201, "")
	st, v = f.a.do("POST", base, body)
	expect(t, st, v, 409, "limit_reached")

	// Another household cannot see, change or delete it.
	for _, m := range []string{"GET", "PATCH", "DELETE"} {
		if st, v = f.b.do(m, "/api/v1/households/"+f.hB+"/webhooks/"+id, map[string]any{"active": true}); st != 404 {
			t.Errorf("%s through another household: %d %v", m, st, v)
		}
	}
	st, _ = f.a.do("DELETE", base+"/"+id, nil)
	expect(t, st, nil, 204, "")
	st, v = f.a.do("GET", base+"/"+id, nil)
	expect(t, st, v, 404, "not_found")

	evs, err := f.e.st.ListAuditForUser(context.Background(), userID(t, f.e, "a"), 50)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, ev := range evs {
		seen[ev.Event] = true
		if strings.Contains(toJSON(ev.Meta), "secret") {
			t.Errorf("audit meta %v", ev.Meta)
		}
	}
	for _, want := range []string{store.AuditWebhookCreate, store.AuditWebhookUpdate, store.AuditWebhookRotate, store.AuditWebhookDelete} {
		if !seen[want] {
			t.Errorf("no %s audit event", want)
		}
	}
}

func TestWebhookTestSendSignedAndLogged(t *testing.T) {
	f := newHH(t)
	f.e.stepUp(f.a, "a")
	target := newWHTarget(t)
	setGuard(t, f.e, false) // self-hosted LAN case: http to a private address is allowed
	base := "/api/v1/households/" + f.hA + "/webhooks"
	st, v := f.a.do("POST", base, map[string]any{"url": target.srv.URL + "/hook", "events": []string{"chore.completed"}})
	expect(t, st, v, 201, "")
	id, secret := field(v, "id").(string), field(v, "secret").(string)

	st, v = f.a.do("POST", base+"/"+id+"/test", nil)
	if st != 200 || field(v, "status") != "sent" || field(v, "type") != "ping" || field(v, "httpStatus") != 204.0 {
		t.Fatalf("test: %d %v", st, v)
	}
	if target.count() != 1 {
		t.Fatalf("%d hits", target.count())
	}
	h := target.hits[0]
	if h.header.Get("Gunkly-Event") != "ping" || h.header.Get("Gunkly-Delivery") != field(v, "eventId") ||
		!strings.HasPrefix(h.header.Get("User-Agent"), "Gunkly-Webhook/") {
		t.Fatalf("headers %v", h.header)
	}
	if err := webhook.Verify(secret, h.header.Get("Gunkly-Signature"), h.body, f.e.clock.Now()); err != nil {
		t.Fatalf("signature: %v", err)
	}
	for _, want := range []string{`"type":"ping"`, `"version":1`, `"householdId":"` + f.hA + `"`} {
		if !strings.Contains(string(h.body), want) {
			t.Errorf("body %s lacks %s", h.body, want)
		}
	}

	// The rotated secret signs the next one; the old one no longer verifies.
	st, v = f.a.do("POST", base+"/"+id+"/rotate-secret", nil)
	expect(t, st, v, 200, "")
	if st, _ = f.a.do("POST", base+"/"+id+"/test", nil); st != 200 {
		t.Fatal(st)
	}
	h = target.hits[1]
	if err := webhook.Verify(secret, h.header.Get("Gunkly-Signature"), h.body, f.e.clock.Now()); err == nil {
		t.Fatal("the old secret still verifies")
	}
	if err := webhook.Verify(field(v, "secret").(string), h.header.Get("Gunkly-Signature"), h.body, f.e.clock.Now()); err != nil {
		t.Fatalf("new secret: %v", err)
	}

	st, v = f.a.do("GET", base+"/"+id+"/deliveries", nil)
	items, _ := field(v, "items").([]any)
	if st != 200 || len(items) != 2 || field(items[0], "status") != "sent" || field(items[0], "type") != "ping" {
		t.Fatalf("deliveries: %d %v", st, v)
	}
	// A member of the household cannot read the log (the walker covers the rest).
	if st, _ = f.m.do("GET", base+"/"+id+"/deliveries", nil); st != 403 {
		t.Fatalf("member read the log: %d", st)
	}
}

// With the guard on, a stored loopback target (saved while the guard was off)
// is refused at send time: the test reports a failure and zero requests reach
// the receiver.
func TestWebhookTestSendBlockedByGuard(t *testing.T) {
	f := newHH(t)
	f.e.stepUp(f.a, "a")
	target := newWHTarget(t)
	base := "/api/v1/households/" + f.hA + "/webhooks"
	st, v := f.a.do("POST", base, map[string]any{"url": target.srv.URL, "events": []string{"chore.completed"}})
	expect(t, st, v, 400, "invalid_webhook_url") // guard on: not even saved

	setGuard(t, f.e, false)
	st, v = f.a.do("POST", base, map[string]any{"url": target.srv.URL, "events": []string{"chore.completed"}})
	expect(t, st, v, 201, "")
	id := field(v, "id").(string)
	setGuard(t, f.e, true)
	st, v = f.a.do("POST", base+"/"+id+"/test", nil)
	if st != 200 || field(v, "status") != "failed" || field(v, "error") == "" {
		t.Fatalf("test: %d %v", st, v)
	}
	if strings.Contains(toJSON(v), target.srv.URL) || strings.Contains(toJSON(v), "127.0.0.1") {
		t.Fatalf("error quotes the target: %v", v)
	}
	if n := target.count(); n != 0 {
		t.Fatalf("%d requests reached the loopback server", n)
	}
	// Hosted mode forces the guard on whatever the setting says.
	setGuard(t, f.e, false)
	if f.e.srv.hosted = true; !f.e.srv.EgressGuardEnabled(context.Background()) {
		t.Fatal("hosted mode did not force the guard on")
	}
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func userID(t *testing.T, e *env, name string) string {
	t.Helper()
	u, err := e.st.UserByLogin(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}
