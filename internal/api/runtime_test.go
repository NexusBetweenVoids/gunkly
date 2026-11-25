package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NexusBetweenVoids/gunkly/internal/metrics"
)

func adminEnv(t *testing.T, mutate ...func(*Options)) (*env, *client) {
	t.Helper()
	e := newEnv(t, mutate...)
	e.user("root", true)
	return e, e.signedInStepUp("root")
}

func patchConfig(t *testing.T, c *client, values map[string]any, want int, code string) any {
	t.Helper()
	st, v := c.do("PATCH", "/api/v1/admin/config", map[string]any{"values": values})
	expect(t, st, v, want, code)
	return v
}

func TestEmailConfiguredInAdmin(t *testing.T) {
	e, root := adminEnv(t)
	if _, inst := root.do("GET", "/api/v1/instance", nil); field(inst, "emailEnabled") != false {
		t.Fatalf("before: %v", inst)
	}
	// Turned on but incomplete: stays off, and the admin is told why.
	patchConfig(t, root, map[string]any{"email.enabled": "true"}, 200, "")
	_, svc := root.do("GET", "/api/v1/admin/services", nil)
	if field(svc, "emailOn") != false || str(svc, "emailProblem") != "incomplete" {
		t.Fatalf("incomplete: %v", svc)
	}
	for _, bad := range []map[string]any{{"email.port": "0"}, {"email.tls": "ssl"}, {"email.from": "not an address"}, {"email.host": "smtp://x"}} {
		patchConfig(t, root, bad, 400, "invalid")
	}
	patchConfig(t, root, map[string]any{"email.host": "127.0.0.1", "email.port": "1", "email.from": "Gunkly <gunkly@example.com>", "email.password": "pw 1"}, 200, "")
	if _, inst := root.do("GET", "/api/v1/instance", nil); field(inst, "emailEnabled") != true {
		t.Fatalf("after: %v", inst)
	}
	// The test send fails (nothing listens there) and says so.
	st, v := root.do("POST", "/api/v1/admin/email/test", map[string]string{"to": "root@example.com"})
	if st != 200 || field(v, "ok") != false || str(v, "error") != "send_failed" {
		t.Fatalf("test email: %d %v", st, v)
	}
	st, v = root.do("POST", "/api/v1/admin/email/test", map[string]string{"to": "nope"})
	expect(t, st, v, 400, "invalid")
	// Off again.
	patchConfig(t, root, map[string]any{"email.enabled": "false"}, 200, "")
	st, v = root.do("POST", "/api/v1/admin/email/test", map[string]string{"to": "root@example.com"})
	expect(t, st, v, 409, "email_disabled")
	if e.srv.Mailer().(dynMailer).Enabled() {
		t.Fatal("scheduler mailer still on")
	}
}

func TestEmailGroupLockedByEnv(t *testing.T) {
	// Placeholder SMTP values without GUNKLY_EMAIL_ENABLED=true (the old .env.example) lock nothing,
	// and the sign-up difficulty's default value does not lock its field either.
	_, free := adminEnv(t, func(o *Options) {
		o.Env = map[string]string{"GUNKLY_SMTP_HOST": "smtp.example.com", "GUNKLY_EMAIL_ENABLED": "false", "GUNKLY_SIGNUP_POW_MAX": "250000"}
	})
	patchConfig(t, free, map[string]any{"email.host": "mail.lan", "signup.pow_max": "300000"}, 200, "")

	_, root := adminEnv(t, func(o *Options) {
		o.Env = map[string]string{"GUNKLY_EMAIL_ENABLED": "true", "GUNKLY_SMTP_HOST": "smtp.example.com"}
	})
	_, v := root.do("GET", "/api/v1/admin/config", nil)
	for _, k := range []string{"email.enabled", "email.host", "email.password", "email.from"} {
		if field(field(field(v, "fields"), k), "locked") != true {
			t.Errorf("%s not locked: %v", k, field(field(v, "fields"), k))
		}
	}
	if str(field(field(v, "fields"), "email.host"), "value") != "smtp.example.com" {
		t.Errorf("locked host shows %v", field(field(v, "fields"), "email.host"))
	}
	patchConfig(t, root, map[string]any{"email.from": "a@example.com"}, 409, "set_by_environment")
	// Other groups stay editable.
	patchConfig(t, root, map[string]any{"ai.model": "m"}, 200, "")
}

func TestMetricsTokenConfiguredInAdmin(t *testing.T) {
	e, root := adminEnv(t, func(o *Options) { o.Metrics = metrics.New("test") })
	st, _, _ := e.bearerDo("GET", "/metrics", "a-metrics-token-of-length", nil)
	if st != 404 {
		t.Fatalf("no token: %d", st)
	}
	patchConfig(t, root, map[string]any{"metrics.token": "short"}, 400, "invalid")
	patchConfig(t, root, map[string]any{"metrics.token": "a-metrics-token-of-length"}, 200, "")
	if st, _, _ := e.bearerDo("GET", "/metrics", "wrong-token-wrong-token", nil); st != 401 {
		t.Fatalf("wrong token: %d", st)
	}
	if st, _, _ := e.bearerDo("GET", "/metrics", "a-metrics-token-of-length", nil); st != 200 {
		t.Fatalf("right token: %d", st)
	}
}

func TestSSOConfiguredInAdmin(t *testing.T) {
	_, root := adminEnv(t, func(o *Options) { o.BaseURL = "https://chores.example.com" })
	if _, inst := root.do("GET", "/api/v1/instance", nil); field(inst, "sso") != nil {
		t.Fatalf("before: %v", inst)
	}
	patchConfig(t, root, map[string]any{"sso.issuer": "http://idp.example.com"}, 400, "invalid") // https only (http for localhost)
	patchConfig(t, root, map[string]any{"sso.issuer": "https://idp.example.com"}, 200, "")
	if _, svc := root.do("GET", "/api/v1/admin/services", nil); field(svc, "ssoOn") != false || str(svc, "ssoProblem") != "incomplete" {
		t.Fatalf("incomplete: %v", svc)
	}
	patchConfig(t, root, map[string]any{"sso.client_id": "gunkly", "sso.client_secret": "s3cret"}, 200, "")
	if _, inst := root.do("GET", "/api/v1/instance", nil); field(inst, "sso") == nil {
		t.Fatalf("after: %v", inst)
	}
	_, svc := root.do("GET", "/api/v1/admin/services", nil)
	if field(svc, "ssoOn") != true || str(svc, "ssoCallback") != "https://chores.example.com/api/v1/auth/oidc/callback" {
		t.Fatalf("services: %v", svc)
	}
	// Clearing the issuer switches it off again.
	patchConfig(t, root, map[string]any{"sso.issuer": nil, "sso.client_id": nil}, 200, "")
	if _, inst := root.do("GET", "/api/v1/instance", nil); field(inst, "sso") != nil {
		t.Fatalf("cleared: %v", inst)
	}
}

func TestSSONeedsHTTPS(t *testing.T) {
	_, root := adminEnv(t, func(o *Options) { o.BaseURL = "http://chores.lan" })
	patchConfig(t, root, map[string]any{"sso.issuer": "https://idp.example.com", "sso.client_id": "g"}, 200, "")
	if _, svc := root.do("GET", "/api/v1/admin/services", nil); field(svc, "ssoOn") != false || str(svc, "ssoProblem") != "needs_https" {
		t.Fatalf("http base: %v", svc)
	}
}

func TestUpdateWebhook(t *testing.T) {
	var gotAuth, gotMethod string
	status := 200
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotMethod = r.Header.Get("Authorization"), r.Method
		w.WriteHeader(status)
	}))
	defer hook.Close()
	e, root := adminEnv(t, func(o *Options) { o.AIClient = hook.Client() })
	st, v := root.do("POST", "/api/v1/admin/updates/apply", nil)
	expect(t, st, v, 409, "update_webhook_missing")
	if _, u := root.do("GET", "/api/v1/admin/updates", nil); field(u, "canUpdate") != false {
		t.Fatalf("canUpdate before: %v", u)
	}
	patchConfig(t, root, map[string]any{"updates.webhook_url": "ftp://x"}, 400, "invalid")
	patchConfig(t, root, map[string]any{"updates.webhook_url": hook.URL + "/v1/update", "updates.webhook_token": "wt-token"}, 200, "")
	if _, u := root.do("GET", "/api/v1/admin/updates", nil); field(u, "canUpdate") != true {
		t.Fatalf("canUpdate after: %v", u)
	}
	st, v = root.do("POST", "/api/v1/admin/updates/apply", nil)
	if st != 200 || str(v, "result") != "accepted" || gotAuth != "Bearer wt-token" || gotMethod != "POST" {
		t.Fatalf("apply: %d %v %q %q", st, v, gotAuth, gotMethod)
	}
	status = 401
	st, v = root.do("POST", "/api/v1/admin/updates/apply", nil)
	if st != 200 || str(v, "result") != "failed" || str(v, "error") != "rejected" {
		t.Fatalf("rejected: %d %v", st, v)
	}
	// A recent step-up is required.
	st, v = e.signedIn("root").do("POST", "/api/v1/admin/updates/apply", nil)
	expect(t, st, v, 403, "step_up_required")
}
