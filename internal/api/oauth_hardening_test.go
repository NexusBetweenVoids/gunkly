package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/auth"
	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// post is the consent form's post without the automatic step-up.
func (f *oauthFix) post(t *testing.T, u *client, cid, ch, decision string, extra ...string) (int, string, string) {
	t.Helper()
	st, loc, body := f.consentRaw(t, u, authzQuery(cid, ch), decision, []string{f.hA}, extra...)
	if loc != nil {
		return st, loc.Query().Get("code") + loc.Query().Get("error"), body
	}
	return st, "", body
}

func (f *oauthFix) page(t *testing.T, u *client, cid, ch string) string {
	t.Helper()
	st, _, body := u.browseGet("/oauth/authorize?" + authzQuery(cid, ch).Encode())
	if st != 200 {
		t.Fatalf("consent page: %d %s", st, body)
	}
	return body
}

func TestOAuthConsentAsksForPassword(t *testing.T) {
	f := newOAuthEnv(t)
	ctx := context.Background()
	cid := f.register(t, "App")
	_, ch := pkce()

	body := f.page(t, f.a, cid, ch)
	for _, want := range []string{`<label for="oauth-password">`, `id="oauth-password" type="password" name="password" autocomplete="current-password" required`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, `name="code"`) || strings.Contains(body, "email_code") {
		t.Error("code field or email button for a user with no second factor")
	}
	if strings.Contains(body, "style=") || strings.Contains(body, "<script") || strings.Contains(body, " onclick") {
		t.Error("inline style or script on the consent page")
	}

	// No password, a wrong one: the same generic error, no code, no step-up; both are audited.
	var errs []string
	for _, pw := range []string{"", "wrong-password"} {
		st, res, b := f.post(t, f.a, cid, ch, "allow", "password", pw)
		if st != 200 || res != "" || !strings.Contains(b, "That did not match.") {
			t.Fatalf("password %q: %d %q", pw, st, res)
		}
		errs = append(errs, b[strings.Index(b, `class="err"`):strings.Index(b, `class="err"`)+80])
	}
	if errs[0] != errs[1] {
		t.Errorf("the two failures differ: %q %q", errs[0], errs[1])
	}
	if gs, _ := f.e.st.ListOAuthGrants(ctx, f.userID("a")); len(gs) != 0 {
		t.Fatal("a grant without a password")
	}
	_, v := f.a.do("GET", "/api/v1/account/audit", nil)
	n := 0
	for _, ev := range v.([]any) {
		if field(ev, "event") == store.AuditLoginFailure {
			n++
		}
	}
	if n < 1 {
		t.Error("failed consent step-up not audited")
	}
	// The session was not stepped up by the failures.
	if st, v := f.a.do("POST", "/api/v1/account/totp/setup", nil); st != 403 {
		t.Fatalf("session stepped up by a failure: %d %v", st, v)
	}

	// Deny never needs a password.
	if st, res, _ := f.post(t, f.a, cid, ch, "deny"); st != 303 || res != "access_denied" {
		t.Fatalf("deny: %d %q", st, res)
	}
	// The right password continues and steps the session up.
	if st, res, b := f.post(t, f.a, cid, ch, "allow", "password", "password-a"); st != 303 || res == "" {
		t.Fatalf("allow: %d %q %s", st, res, b)
	}
	if st, v := f.a.do("POST", "/api/v1/account/totp/setup", nil); st != 200 {
		t.Fatalf("session not stepped up: %d %v", st, v)
	}
	// Within the window: no fields, and a post without a password works.
	if body := f.page(t, f.a, cid, ch); strings.Contains(body, `name="password"`) {
		t.Error("password field right after a step-up")
	}
	if st, res, _ := f.post(t, f.a, cid, ch, "allow"); st != 303 || res == "" {
		t.Fatalf("allow within the window: %d %q", st, res)
	}
	// The window ends: asked again.
	f.e.clock.Add(stepUpWindow + time.Minute)
	if body := f.page(t, f.a, cid, ch); !strings.Contains(body, `name="password"`) {
		t.Error("no password field after the window")
	}
	if st, res, _ := f.post(t, f.a, cid, ch, "allow"); st != 200 || res != "" {
		t.Fatalf("allow after the window without a password: %d %q", st, res)
	}
}

func TestOAuthConsentPasswordBackoff(t *testing.T) {
	f := newOAuthEnv(t)
	cid := f.register(t, "App")
	_, ch := pkce()
	var last int
	for i := 0; i < freeFailures+2; i++ {
		last, _, _ = f.post(t, f.m, cid, ch, "allow", "password", "nope")
	}
	if last != 429 {
		t.Fatalf("no backoff: %d", last)
	}
	// Even the right password waits (and the REST step-up shares the counter).
	if st, res, _ := f.post(t, f.m, cid, ch, "allow", "password", "password-m"); st != 429 || res != "" {
		t.Fatalf("right password while backing off: %d", st)
	}
	f.e.clock.Add(2 * time.Minute)
	if st, res, _ := f.post(t, f.m, cid, ch, "allow", "password", "password-m"); st != 303 || res == "" {
		t.Fatalf("after the wait: %d", st)
	}
}

func (f *oauthFix) enableTOTP(t *testing.T, name string) (secret string, recovery []string) {
	t.Helper()
	c := f.e.signedInStepUp(name)
	st, v := c.do("POST", "/api/v1/account/totp/setup", nil)
	expect(t, st, v, 200, "")
	secret = field(v, "secret").(string)
	st, v = c.do("POST", "/api/v1/account/totp/confirm", map[string]string{"code": totpCode(secret, f.e.clock.Now())})
	expect(t, st, v, 200, "")
	for _, x := range field(v, "recoveryCodes").([]any) {
		recovery = append(recovery, x.(string))
	}
	return secret, recovery
}

// loginTOTP signs in a user with an authenticator app on a new device.
func (f *oauthFix) loginTOTP(t *testing.T, name, secret string) *client {
	t.Helper()
	f.e.clock.Add(31 * time.Second) // a fresh time step: codes are single use
	c := f.e.client()
	st, v := c.login(name, map[string]string{"totp": totpCode(secret, f.e.clock.Now())})
	expect(t, st, v, 200, "")
	return c
}

func TestOAuthConsentSecondFactor(t *testing.T) {
	f := newOAuthEnv(t)
	secret, recovery := f.enableTOTP(t, "m")
	m := f.loginTOTP(t, "m", secret)
	cid := f.register(t, "App")
	_, ch := pkce()

	body := f.page(t, m, cid, ch)
	for _, want := range []string{`name="password"`, `<label for="oauth-code">`, `name="code" autocomplete="one-time-code"`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, "email_code") {
		t.Error("email button without email codes")
	}
	for name, kv := range map[string][]string{
		"password only":  {"password", "password-m"},
		"wrong code":     {"password", "password-m", "code", "000000"},
		"wrong password": {"password", "bad-bad-bad", "code", totpCode(secret, f.e.clock.Now())},
	} {
		if st, res, b := f.post(t, m, cid, ch, "allow", kv...); st != 200 || res != "" || !strings.Contains(b, "That did not match.") {
			t.Errorf("%s: %d %q", name, st, res)
		}
	}
	f.e.clock.Add(31 * time.Second)
	good := totpCode(secret, f.e.clock.Now())
	if st, res, _ := f.post(t, m, cid, ch, "allow", "password", "password-m", "code", good); st != 303 || res == "" {
		t.Fatalf("right password and code: %d %q", st, res)
	}

	// A recovery code works in the same field, once.
	m2 := f.loginTOTP(t, "m", secret)
	if st, res, _ := f.post(t, m2, cid, ch, "allow", "password", "password-m", "code", recovery[0]); st != 303 || res == "" {
		t.Fatalf("recovery code: %d %q", st, res)
	}
	f.e.clock.Add(stepUpWindow + time.Minute)
	f.e.clock.Add(2 * time.Minute) // let any backoff lapse
	if st, res, _ := f.post(t, m2, cid, ch, "allow", "password", "password-m", "code", recovery[0]); st != 200 || res != "" {
		t.Fatalf("recovery code reused: %d %q", st, res)
	}
	if len(recovery[0]) == 0 || len(recovery) != auth.RecoveryCodeCount {
		t.Fatal("recovery codes")
	}
}

func TestOAuthConsentEmailCode(t *testing.T) {
	me := newMailEnv(t, func(o *Options) { o.BaseURL = oauthBase })
	me.user("ann", false)
	me.verified("ann")
	me.enableEmailOTP("ann")
	setOAuth(t, me.env, true)
	f := &oauthFix{&tokenEnv{hhFixture: &hhFixture{e: me.env}}}
	c := me.client()
	c.login("ann", nil)
	st, v := c.login("ann", map[string]string{"emailCode": codeIn(t, me.oneMail("ann@example.com"))})
	expect(t, st, v, 200, "")
	st, v = c.do("POST", "/api/v1/households", map[string]string{"name": "H", "timezone": "Europe/Amsterdam"})
	expect(t, st, v, 201, "")
	f.hA = field(v, "id").(string)
	cid := f.register(t, "App")
	_, ch := pkce()
	if err := me.st.UpdateInstanceSettings(context.Background(), store.SettingsUpdate{Limits: map[string]int{string(limits.EmailCodesAccountHour): 3}}); err != nil {
		t.Fatal(err)
	}

	body := f.page(t, c, cid, ch)
	if !strings.Contains(body, `name="decision" value="email_code"`) || !strings.Contains(body, "Email me a code") || strings.Contains(body, `name="code"`) {
		t.Fatalf("first page: email button and no code field expected")
	}
	before := len(me.mails())
	// A wrong password sends nothing.
	if st, _, b := f.post(t, c, cid, ch, "email_code", "password", "nope-nope"); st != 200 || !strings.Contains(b, "That did not match.") || len(me.mails()) != before {
		t.Fatalf("wrong password with the email button: %d, mails %d", st, len(me.mails()))
	}
	st, _, b := f.post(t, c, cid, ch, "email_code", "password", "password-ann")
	if st != 200 || !strings.Contains(b, `name="code" autocomplete="one-time-code"`) || !strings.Contains(b, "We sent a code") {
		t.Fatalf("after the button: %d", st)
	}
	msgs := me.mails()
	if len(msgs) != before+1 {
		t.Fatalf("mails %d, want %d", len(msgs), before+1)
	}
	code := codeIn(t, msgs[len(msgs)-1])
	// A wrong code fails (and keeps the code field); the right one continues.
	if st, res, b := f.post(t, c, cid, ch, "allow", "password", "password-ann", "code", "000000"); st != 200 || res != "" || !strings.Contains(b, `name="code"`) {
		t.Fatalf("wrong emailed code: %d %q", st, res)
	}
	if st, res, _ := f.post(t, c, cid, ch, "allow", "password", "password-ann", "code", code); st != 303 || res == "" {
		t.Fatalf("emailed code: %d %q", st, res)
	}
	// The codes' hourly limit still applies: the button is throttled, not unbounded.
	me.clock.Add(stepUpWindow + time.Minute)
	for i := 0; i < 4; i++ {
		st, _, _ = f.post(t, c, cid, ch, "email_code", "password", "password-ann")
	}
	if st != 429 {
		t.Fatalf("email code sends not limited: %d", st)
	}
}

func TestOAuthConsentPhishingHardening(t *testing.T) {
	f := newOAuthEnv(t)
	ver, ch := pkce()
	cid := f.register(t, "Some app")
	body := f.page(t, f.a, cid, ch)
	if !strings.Contains(body, `sent back to:`) || !strings.Contains(body, `<strong class="host">app.example</strong>`) ||
		!strings.Contains(body, "Unverified app.") || !strings.Contains(body, "First time:") {
		t.Fatalf("first-time page lacks host, label or warning:\n%s", body)
	}
	// The host comes before the unverified notice.
	if strings.Index(body, `class="host"`) > strings.Index(body, "Unverified app.") {
		t.Error("the return host is not the first line")
	}
	f.exchange(t, cid, f.code(t, f.a, cid, ch, []string{f.hA}), ver) // approve once
	same := f.register(t, "Another app")
	if body := f.page(t, f.a, same, ch); strings.Contains(body, "First time:") {
		t.Error("warned about a host this user approved before")
	}
	other := f.register(t, "Third app", "https://elsewhere.example/cb")
	q := authzQuery(other, ch, "redirect_uri", "https://elsewhere.example/cb")
	if _, _, body := f.a.browseGet("/oauth/authorize?" + q.Encode()); !strings.Contains(body, "First time:") || !strings.Contains(body, `class="host">elsewhere.example<`) {
		t.Error("no warning for a new host")
	}
	// Another user has no history with the host.
	if body := f.page(t, f.m, same, ch); !strings.Contains(body, "First time:") {
		t.Error("another user's approvals count")
	}
}

func TestOAuthRejectsGunklyImpersonation(t *testing.T) {
	f := newOAuthEnv(t)
	for _, n := range []string{"Gunkly", "gunkly backup", "My GUNKLY", "ＧＵＮＫＬＹ", "Gúnkly", "𝐆𝐮𝐧𝐤𝐥𝐲", "Ｇunkly", "gunkly.app"} {
		if st, _, _ := f.e.oauthRaw("POST", "/oauth/register", nil, map[string]any{"client_name": n, "redirect_uris": []string{oauthRedirect}}); st != 400 {
			t.Errorf("name %q registered: %d", n, st)
		}
	}
	for _, n := range []string{"Gunk", "Gun kly", "Claude", ""} {
		if st, _, _ := f.e.oauthRaw("POST", "/oauth/register", nil, map[string]any{"client_name": n, "redirect_uris": []string{oauthRedirect}}); st != 201 {
			t.Errorf("name %q refused: %d", n, st)
		}
	}
}

func TestOAuthUnusedClientsPerIP(t *testing.T) {
	f := newOAuthEnv(t)
	if err := f.e.st.UpdateInstanceSettings(context.Background(), store.SettingsUpdate{Limits: map[string]int{string(limits.OAuthUnusedClientsPerIP): 3}}); err != nil {
		t.Fatal(err)
	}
	ids := []string{f.register(t, "1"), f.register(t, "2"), f.register(t, "3")}
	reg := func() int {
		st, _, _ := f.e.oauthRaw("POST", "/oauth/register", nil, map[string]any{"redirect_uris": []string{oauthRedirect}})
		return st
	}
	if st := reg(); st != 503 {
		t.Fatalf("fourth unused client from one address: %d", st)
	}
	// Approving one frees a slot: only unused clients count.
	_, ch := pkce()
	f.code(t, f.a, ids[0], ch, []string{f.hA})
	if st := reg(); st != 201 {
		t.Fatalf("after an approval: %d", st)
	}
}

func TestImpersonates(t *testing.T) {
	for n, want := range map[string]bool{"gunkly": true, "GunKly": true, "ＧＵＮＫＬＹ": true, "gunk": false, "": false, "gun-kly": false} {
		if got := impersonates(n); got != want {
			t.Errorf("impersonates(%q) = %v", n, got)
		}
	}
}
