package api

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
	"github.com/NexusBetweenVoids/gunkly/internal/webhook"
)

const whURL1, whURL2 = "https://hooks.example.com/in/token-one", "https://ha.example.org/api/webhook/abc"

// webhookExportFixture: household A with two webhooks (the second switched
// off), returning their one-time signing secrets.
func webhookExportFixture(t *testing.T) (*hhFixture, []string) {
	t.Helper()
	f := newHH(t)
	base := f.base() + "/webhooks"
	f.e.stepUp(f.a, "a")
	var secrets []string
	for i, b := range []map[string]any{
		{"url": whURL1, "description": "Home Assistant", "events": []string{"chore.completed", "chore.overdue"}},
		{"url": whURL2, "description": "Spare", "events": []string{"chore.created"}},
	} {
		f.e.clock.Add(time.Second) // a stable created_at order
		st, v := f.a.do("POST", base, b)
		expect(t, st, v, 201, "")
		secrets = append(secrets, str(v, "secret"))
		if i == 1 {
			st, v = f.a.do("PATCH", base+"/"+str(v, "id"), map[string]any{"active": false})
			expect(t, st, v, 200, "")
		}
	}
	return f, secrets
}

func exportRaw(t *testing.T, c *client, path string) (string, any) {
	t.Helper()
	resp := c.raw("GET", path, nil, "")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("export: %d", resp.StatusCode)
	}
	var raw bytes.Buffer
	_, _ = raw.ReadFrom(resp.Body)
	var doc any
	if err := json.Unmarshal(raw.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	return raw.String(), doc
}

func withWebhooks(doc any, hooks []map[string]any) map[string]any {
	m := map[string]any{}
	for k, v := range doc.(map[string]any) {
		m[k] = v
	}
	m["webhooks"] = hooks
	return m
}

func TestExportWebhooksOwnerOnlyAndNeverTheSecret(t *testing.T) {
	f, secrets := webhookExportFixture(t)
	raw, doc := exportRaw(t, f.a, f.base()+"/export")
	hooks := list(t, field(doc, "webhooks"))
	if len(hooks) != 2 || str(hooks[0], "name") != "Home Assistant" || str(hooks[0], "url") != whURL1 || field(hooks[0], "enabled") != true ||
		len(list(t, field(hooks[0], "events"))) != 2 || str(hooks[1], "url") != whURL2 || field(hooks[1], "enabled") != false {
		t.Fatalf("webhooks: %v", hooks)
	}
	for _, h := range hooks {
		for k := range h.(map[string]any) {
			if k != "name" && k != "events" && k != "url" && k != "enabled" {
				t.Fatalf("unexpected webhook field %q", k)
			}
		}
	}
	for _, s := range append(secrets, "whsec_", "ciphertext", "secret") {
		if strings.Contains(raw, s) {
			t.Fatalf("export leaks %q:\n%s", s, raw)
		}
	}
	// A member's export has no webhooks (and so no URLs) at all.
	mraw, mdoc := exportRaw(t, f.m, f.base()+"/export")
	if field(mdoc, "webhooks") != nil || strings.Contains(mraw, "hooks.example.com") || strings.Contains(mraw, "webhooks") {
		t.Fatalf("member export carries webhooks:\n%s", mraw)
	}
	// Another household's webhooks never appear.
	braw, _ := exportRaw(t, f.b, "/api/v1/households/"+f.hB+"/export")
	if strings.Contains(braw, "hooks.example.com") {
		t.Fatal("B's export has A's webhook")
	}
}

func TestImportWebhooksRoundTripDisabledWithFreshSecrets(t *testing.T) {
	f, secrets := webhookExportFixture(t)
	_, doc := exportRaw(t, f.a, f.base()+"/export")

	// b imports the file as a new household and owns it.
	st, v := f.b.do("POST", "/api/v1/households/import", doc)
	expect(t, st, v, 201, "")
	if field(v, "webhooksCreated") != float64(2) || len(list(t, field(v, "webhooksSkipped"))) != 0 {
		t.Fatalf("summary %v", v)
	}
	hh := str(field(v, "household"), "id")
	st, v = f.b.do("GET", "/api/v1/households/"+hh+"/webhooks", nil)
	expect(t, st, v, 200, "")
	ws := list(t, v)
	if len(ws) != 2 {
		t.Fatalf("webhooks %v", v)
	}
	if str(ws[0], "description") != "Home Assistant" { // created in the same millisecond: order by id
		ws[0], ws[1] = ws[1], ws[0]
	}
	for i, w := range ws {
		// Every webhook arrives disabled, even the one that was enabled.
		if field(w, "active") != false || field(w, "autoDisabled") != false || str(w, "id") == "" {
			t.Fatalf("webhook %d not disabled: %v", i, w)
		}
	}
	if str(ws[0], "description") != "Home Assistant" || str(ws[0], "host") != "hooks.example.com" || len(list(t, field(ws[0], "events"))) != 2 {
		t.Fatalf("webhook 0: %v", ws[0])
	}
	// The secret is new and the URL survived (sealed, as normal).
	bu, _ := f.e.st.UserByLogin(context.Background(), "b")
	urlCT, secretCT, err := f.e.st.WebhookSealed(context.Background(), store.Actor{UserID: bu.ID}, hh, str(ws[0], "id"))
	if err != nil {
		t.Fatal(err)
	}
	id := str(ws[0], "id")
	if u, err := webhook.OpenURL(f.e.srv.keyring, id, urlCT); err != nil || u != whURL1 {
		t.Fatalf("url %q %v", u, err)
	}
	if sec, err := webhook.OpenSecret(f.e.srv.keyring, id, secretCT); err != nil || !strings.HasPrefix(sec, "whsec_") || sec == secrets[0] || sec == secrets[1] {
		t.Fatalf("secret %q %v", sec, err)
	}
	// The owner reviews: rotating shows the new secret once, then enables.
	f.e.stepUp(f.b, "b")
	st, v = f.b.do("POST", "/api/v1/households/"+hh+"/webhooks/"+id+"/rotate-secret", nil)
	expect(t, st, v, 200, "")
	if !strings.HasPrefix(str(v, "secret"), "whsec_") {
		t.Fatalf("rotate %v", v)
	}
	st, v = f.b.do("PATCH", "/api/v1/households/"+hh+"/webhooks/"+id, map[string]any{"active": true})
	expect(t, st, v, 200, "")
	if field(v, "active") != true {
		t.Fatalf("enable %v", v)
	}
	// A's own webhooks are untouched.
	if _, v = f.a.do("GET", f.base()+"/webhooks", nil); len(list(t, v)) != 2 {
		t.Fatalf("A: %v", v)
	}

	// Merging into A (owner) adds two more, disabled.
	st, v = f.a.do("POST", f.base()+"/import", doc)
	expect(t, st, v, 200, "")
	if field(v, "webhooksCreated") != float64(2) {
		t.Fatalf("merge summary %v", v)
	}
	_, v = f.a.do("GET", f.base()+"/webhooks", nil)
	active := 0
	for _, w := range list(t, v) {
		if field(w, "active") == true {
			active++
		}
	}
	if len(list(t, v)) != 4 || active != 1 {
		t.Fatalf("A after merge: %d webhooks, %d active", len(list(t, v)), active)
	}
}

func TestImportWebhooksSkipsAreReportedNotFatal(t *testing.T) {
	f, _ := webhookExportFixture(t)
	_, doc := exportRaw(t, f.a, f.base()+"/export")
	file := withWebhooks(doc, []map[string]any{
		{"name": "ok", "url": whURL1, "events": []string{"chore.completed"}, "enabled": true},
		{"name": "plain http", "url": "http://hooks.example.com/", "events": []string{"chore.completed"}, "enabled": true},
		{"name": "loopback", "url": "https://127.0.0.1/x", "events": []string{"chore.completed"}, "enabled": true},
		{"name": "metadata", "url": "https://169.254.169.254/", "events": []string{"chore.completed"}, "enabled": true},
		{"name": "userinfo", "url": "https://u:p@hooks.example.com/", "events": []string{"chore.completed"}, "enabled": true},
		{"name": "bad event", "url": whURL2, "events": []string{"chore.exploded"}, "enabled": true},
		{"name": "no events", "url": whURL2, "events": []string{}, "enabled": true},
	})
	st, v := f.b.do("POST", "/api/v1/households/import", file)
	expect(t, st, v, 201, "")
	if field(v, "webhooksCreated") != float64(1) {
		t.Fatalf("summary %v", v)
	}
	skipped := list(t, field(v, "webhooksSkipped"))
	if len(skipped) != 6 {
		t.Fatalf("skipped %v", skipped)
	}
	for _, s := range skipped {
		if str(s, "reason") != "invalid" || str(s, "name") == "ok" {
			t.Fatalf("skip %v", s)
		}
	}
	// With the guard off, loopback and plain http are acceptable targets.
	setGuard(t, f.e, false)
	st, v = f.b.do("POST", "/api/v1/households/import", file)
	expect(t, st, v, 201, "")
	if field(v, "webhooksCreated") != float64(4) {
		t.Fatalf("guard off: %v", v)
	}
}

func TestImportWebhooksInstanceSwitchOwnerAndLimit(t *testing.T) {
	f, _ := webhookExportFixture(t)
	_, doc := exportRaw(t, f.a, f.base()+"/export")

	// Members merging cannot create webhooks.
	st, v := f.m.do("POST", f.base()+"/import", doc)
	expect(t, st, v, 200, "")
	sk := list(t, field(v, "webhooksSkipped"))
	if field(v, "webhooksCreated") != float64(0) || len(sk) != 2 || str(sk[0], "reason") != "owner_only" {
		t.Fatalf("member merge %v", v)
	}
	if _, v = f.a.do("GET", f.base()+"/webhooks", nil); len(list(t, v)) != 2 {
		t.Fatalf("member import created webhooks: %v", v)
	}

	// The per-household limit: the rest is skipped and reported.
	three := withWebhooks(doc, []map[string]any{
		{"name": "1", "url": whURL1, "events": []string{"chore.created"}, "enabled": true},
		{"name": "2", "url": whURL1 + "2", "events": []string{"chore.created"}, "enabled": true},
		{"name": "3", "url": whURL1 + "3", "events": []string{"chore.created"}, "enabled": true},
	})
	if err := f.e.st.UpdateInstanceSettings(context.Background(), store.SettingsUpdate{Limits: map[string]int{string(limits.WebhooksPerHousehold): 2}}); err != nil {
		t.Fatal(err)
	}
	st, v = f.b.do("POST", "/api/v1/households/import", three)
	expect(t, st, v, 201, "")
	sk = list(t, field(v, "webhooksSkipped"))
	if field(v, "webhooksCreated") != float64(2) || len(sk) != 1 || str(sk[0], "name") != "3" || str(sk[0], "reason") != "limit" {
		t.Fatalf("limit %v", v)
	}

	// Webhooks switched off on the instance: skipped and reported, the rest imports.
	f.e.user("root", true)
	root := f.e.signedInStepUp("root")
	st, v = root.do("PATCH", "/api/v1/admin/settings", map[string]any{"webhooksEnabled": false})
	expect(t, st, v, 200, "")
	st, v = f.b.do("POST", "/api/v1/households/import", doc)
	expect(t, st, v, 201, "")
	sk = list(t, field(v, "webhooksSkipped"))
	if field(v, "webhooksCreated") != float64(0) || len(sk) != 2 || str(sk[0], "reason") != "webhooks_disabled" || field(v, "areasCreated") == nil {
		t.Fatalf("switch off %v", v)
	}
	hh := str(field(v, "household"), "id")
	st, v = root.do("PATCH", "/api/v1/admin/settings", map[string]any{"webhooksEnabled": true})
	expect(t, st, v, 200, "")
	if _, v = f.b.do("GET", "/api/v1/households/"+hh+"/webhooks", nil); len(list(t, v)) != 0 {
		t.Fatalf("webhooks appeared: %v", v)
	}
}

func TestImportWebhooksFileValidation(t *testing.T) {
	f, _ := webhookExportFixture(t)
	_, doc := exportRaw(t, f.a, f.base()+"/export")
	// A webhook entry may not smuggle a secret field: the schema is strict.
	file := withWebhooks(doc, []map[string]any{{"name": "x", "url": whURL1, "events": []string{"chore.created"}, "enabled": true, "secret": "whsec_x"}})
	st, v := f.b.do("POST", "/api/v1/households/import", file)
	expect(t, st, v, 400, "import_invalid")
	// Too many entries.
	many := make([]map[string]any, 101)
	for i := range many {
		many[i] = map[string]any{"name": "n", "url": whURL1, "events": []string{"chore.created"}, "enabled": true}
	}
	st, v = f.b.do("POST", "/api/v1/households/import", withWebhooks(doc, many))
	expect(t, st, v, 400, "import_invalid")
}
