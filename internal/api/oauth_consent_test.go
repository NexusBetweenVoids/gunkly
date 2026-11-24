package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/sso"
)

// The consent page's passkey and single sign-on confirmations (ADR 0016, 0018).

const consentOrigin = "http://localhost" // the oauthFix base URL

// passkeyUser returns the fixture's user a with one registered passkey and a
// fresh passwordless session that has not stepped up.
func (f *oauthFix) passkeyUser(t *testing.T) (*softKey, *client) {
	t.Helper()
	key := newSoftKey(t, consentOrigin, "localhost")
	st, opts := f.aStep.do("POST", "/api/v1/account/passkeys/options", nil)
	expect(t, st, opts, 200, "")
	st, v := f.aStep.do("POST", "/api/v1/account/passkeys", map[string]any{
		"challenge": field(opts, "challenge"), "credential": key.createCredential(opts), "name": "Key"})
	expect(t, st, v, 201, "")
	return key, f.passkeySession(t, key)
}

// passkeySession signs in with the passkey: a session without a step-up.
func (f *oauthFix) passkeySession(t *testing.T, key *softKey) *client {
	t.Helper()
	c := f.e.client()
	st, opts := c.do("POST", "/api/v1/session/passkey/login/options", nil)
	expect(t, st, opts, 200, "")
	st, v := c.do("POST", "/api/v1/session/passkey/login", map[string]any{"challenge": field(opts, "challenge"), "credential": key.assertion(opts)})
	expect(t, st, v, 200, "")
	return c
}

// consentOptions is what the page's script asks for first.
func (f *oauthFix) consentOptions(t *testing.T, u *client) (int, any) {
	t.Helper()
	st, _, body := u.browser("POST", "/oauth/authorize/passkey", url.Values{"csrf": {u.cookie(csrfCookie)}})
	var v any
	_ = json.Unmarshal([]byte(body), &v)
	return st, v
}

func answerJSON(opts any, credential any) string {
	b, _ := json.Marshal(map[string]any{"challenge": field(opts, "challenge"), "credential": credential})
	return string(b)
}

func TestOAuthConsentPasskey(t *testing.T) {
	f := newOAuthEnv(t)
	key, a := f.passkeyUser(t)
	cid := f.register(t, "App")
	_, ch := pkce()

	// The page: the plain form plus one same-origin script, strict CSP.
	st, h, body := a.browseGet("/oauth/authorize?" + authzQuery(cid, ch).Encode())
	if st != 200 || strings.Count(body, "<script") != 1 || !strings.Contains(body, `<script src="/oauth-consent.js" defer></script>`) ||
		!strings.Contains(body, `id="oauth-passkey"`) || !strings.Contains(body, `name="password"`) {
		t.Fatalf("page: %d %s", st, body)
	}
	if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self';") || strings.Contains(csp, "unsafe") {
		t.Fatalf("csp: %s", csp)
	}
	if st, h, js := a.browseGet("/oauth-consent.js"); st != 200 || !strings.HasPrefix(h.Get("Content-Type"), "text/javascript") || !strings.Contains(js, "navigator.credentials") {
		t.Fatalf("script: %d %s", st, h.Get("Content-Type"))
	}
	// An account without a passkey gets no script at all.
	if b := f.page(t, f.m, cid, ch); strings.Contains(b, "<script") || strings.Contains(b, "oauth-passkey") {
		t.Fatal("script offered without a passkey")
	}
	// No session or no CSRF: no options.
	if st, _, _ := f.e.client().browser("POST", "/oauth/authorize/passkey", url.Values{"csrf": {"x"}}); st == 200 {
		t.Fatal("options without a session")
	}
	if st, _, _ := a.browser("POST", "/oauth/authorize/passkey", url.Values{}); st != 403 {
		t.Fatalf("options without csrf: %d", st)
	}

	refused := func(name string, st int, loc *url.URL, body string) {
		t.Helper()
		if st != 200 || loc != nil || !strings.Contains(body, "could not be verified") {
			t.Errorf("%s: %d %v %.200s", name, st, loc, body)
		}
	}
	post := func(c *client, passkey string, extra ...string) (int, *url.URL, string) {
		return f.consentRaw(t, c, authzQuery(cid, ch), "allow", []string{f.hA}, append([]string{"passkey", passkey}, extra...)...)
	}

	// A forged answer, a made-up flag and an unknown challenge are refused.
	st, loc, b := post(a, `{"challenge":"nope","credential":{}}`)
	refused("forged", st, loc, b)
	st, loc, b = post(a, "not json")
	refused("garbage", st, loc, b)
	st, loc, b = post(a, "", "confirmed", "1", "stepup", "true", "passkey_ok", "1")
	if st != 200 || loc != nil || !strings.Contains(b, `name="password"`) {
		t.Errorf("a flag instead of an assertion: %d %v", st, loc)
	}

	// A challenge issued for another purpose (the generic step-up) does not count here.
	_, opts := a.do("POST", "/api/v1/session/step-up/passkey/options", nil)
	st, loc, b = post(a, answerJSON(opts, key.assertion(opts)))
	refused("other purpose", st, loc, b)

	// A challenge issued to another session of the same account does not count.
	other := f.passkeySession(t, key)
	f.page(t, other, cid, ch)
	st2, optsOther := f.consentOptions(t, other)
	expect(t, st2, optsOther, 200, "")
	st, loc, b = post(a, answerJSON(optsOther, key.assertion(optsOther)))
	refused("other session", st, loc, b)

	// An assertion without user verification is refused.
	f.page(t, a, cid, ch)
	st2, opts = f.consentOptions(t, a)
	expect(t, st2, opts, 200, "")
	key.flags = flagUP
	assertion := key.assertion(opts)
	key.flags = flagUP | flagUV
	st, loc, b = post(a, answerJSON(opts, assertion))
	refused("no user verification", st, loc, b)

	// A signature by another key is refused.
	f.e.clock.Add(time.Minute) // past the account backoff of the failures above
	st2, opts = f.consentOptions(t, a)
	expect(t, st2, opts, 200, "")
	stranger := newSoftKey(t, consentOrigin, "localhost")
	st, loc, b = post(a, answerJSON(opts, stranger.assertion(opts)))
	refused("other key", st, loc, b)

	// The real thing: options from this session, a user-verified assertion, posted with the form.
	f.e.clock.Add(time.Minute)
	st2, opts = f.consentOptions(t, a)
	expect(t, st2, opts, 200, "")
	if field(field(opts, "options"), "publicKey").(map[string]any)["userVerification"] != "required" {
		t.Fatalf("user verification not required: %v", opts)
	}
	good := answerJSON(opts, key.assertion(opts))
	st, loc, b = post(a, good)
	if st != http.StatusSeeOther || loc.Query().Get("code") == "" {
		t.Fatalf("passkey consent: %d %v %.200s", st, loc, b)
	}
	// The challenge was single use: the same answer again does nothing (the session is
	// stepped up now, so the post would pass on that alone; use a new session).
	again := f.passkeySession(t, key)
	f.page(t, again, cid, ch)
	if st, loc, _ = f.consentRaw(t, again, authzQuery(cid, ch), "allow", []string{f.hA}, "passkey", good); st != 200 || loc != nil {
		t.Fatalf("replayed answer: %d %v", st, loc)
	}
	// After the passkey step-up the page offers no confirmation fields.
	if b := f.page(t, a, cid, ch); strings.Contains(b, "oauth-passkey") || strings.Contains(b, `name="password"`) {
		t.Fatal("confirmation asked again within the step-up window")
	}
}

// ssoConsentEnv is the OAuth fixture with single sign-on pointed at a fake provider.
func newSSOConsentEnv(t *testing.T) (*oauthFix, *ssoEnv) {
	t.Helper()
	idp := newFakeIdP(t)
	cfg := &sso.Config{Issuer: idp.srv.URL, ClientID: idpClient, ClientSecret: idpSecret, Name: "Authelia", Scopes: []string{"openid", "profile", "email"}}
	h := newHH(t, func(o *Options) { o.SSO = cfg; o.BaseURL = "http://localhost:4865" })
	setOAuth(t, h.e, true)
	idp.now = h.e.clock.Now
	return &oauthFix{&tokenEnv{hhFixture: h}}, &ssoEnv{env: h.e, idp: idp}
}

func TestOAuthConsentSSO(t *testing.T) {
	f, se := newSSOConsentEnv(t)
	se.createOn(true)
	u, _ := se.signIn(claims("sub-1"))
	st, v := u.do("POST", "/api/v1/households", map[string]string{"name": "Home", "timezone": "Europe/Amsterdam"})
	expect(t, st, v, 201, "")
	hh := field(v, "id").(string)
	cid := f.register(t, "App")
	_, ch := pkce()
	q := authzQuery(cid, ch, "scope", "chores:read chores:complete")

	// The account has no password: the page offers the provider, no password field.
	st, h, body := u.browseGet("/oauth/authorize?" + q.Encode())
	if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "form-action 'self' https://app.example "+se.idp.srv.URL) {
		t.Fatalf("form-action must allow the provider the post is redirected to: %s", csp)
	}
	if st != 200 || !strings.Contains(body, "Confirm with Authelia") || strings.Contains(body, `name="password"`) || strings.Contains(body, "<script") {
		t.Fatalf("page: %d %s", st, body)
	}
	// Without a step-up nothing is granted, whatever the form says.
	st, loc, b := f.consentRaw(t, u, q, "allow", []string{hh}, "password", "anything", "confirmed", "1")
	if st != 200 || loc != nil || !strings.Contains(b, "did not match") {
		t.Fatalf("allow without step-up: %d %v", st, loc)
	}

	// Confirm: the browser leaves for the provider, asked to authenticate anew.
	begin := func(q url.Values, extra ...string) string {
		t.Helper()
		st, loc, body := f.consentRaw(t, u, q, "sso", []string{hh}, extra...)
		if st != http.StatusSeeOther || !strings.HasPrefix(loc.String(), se.idp.srv.URL+"/auth") {
			t.Fatalf("sso: %d %v %.200s", st, loc, body)
		}
		return loc.String()
	}
	auth := begin(q)
	cb, aq := se.idp.authorize(auth, claims("sub-1"))
	if aq.Get("prompt") != "login" || aq.Get("max_age") != "0" {
		t.Fatalf("not a re-authentication: %v", aq)
	}
	st, back, _ := u.follow("GET", cb, nil)
	if st != 303 || !strings.HasPrefix(back, "/oauth/authorize?") {
		t.Fatalf("callback: %d %q", st, back)
	}
	bu, _ := url.Parse(back)
	bq := bu.Query()
	if bq.Get("sso") != "stepup" || bq.Get("client_id") != cid || bq.Get("redirect_uri") != oauthRedirect || bq.Get("code_challenge") != ch ||
		bq.Get("state") != "st-1" || bq.Get("scope") != "chores:read chores:complete" {
		t.Fatalf("pending request not preserved: %v", bq)
	}
	// Back on the same consent request, stepped up: no confirmation fields; allow issues the code.
	if st, _, body := u.browseGet(back); st != 200 || !strings.Contains(body, "Confirmed.") || strings.Contains(body, "Confirm with Authelia") {
		t.Fatalf("returned page: %d %s", st, body)
	}
	st, loc, b = f.consentRaw(t, u, q, "allow", []string{hh})
	if st != http.StatusSeeOther || loc.Query().Get("code") == "" || loc.Query().Get("state") != "st-1" {
		t.Fatalf("allow after sso: %d %v %s", st, loc, b)
	}
	// A forged "sso=stepup" in the URL grants nothing and says nothing.
	u2, _ := se.signIn(claims("sub-1"))
	if st, _, body := u2.browseGet("/oauth/authorize?" + q.Encode() + "&sso=stepup"); st != 200 || strings.Contains(body, "Confirmed.") {
		t.Fatalf("forged return: %d", st)
	}
	if st, loc, _ := f.consentRaw(t, u2, q, "allow", []string{hh}); st != 200 || loc != nil {
		t.Fatalf("forged return granted: %d %v", st, loc)
	}

	// Another provider subject does not confirm this account.
	u3, _ := se.signIn(claims("sub-1"))
	_, loc, _ = f.consentRaw(t, u3, q, "sso", []string{hh})
	cb, _ = se.idp.authorize(loc.String(), claims("sub-2"))
	st, back, _ = u3.follow("GET", cb, nil)
	if st != 303 || !strings.HasPrefix(back, "/oauth/authorize?") || !strings.Contains(back, "sso=failed") {
		t.Fatalf("other subject: %d %q", st, back)
	}
	if st, _, body := u3.browseGet(back); st != 200 || !strings.Contains(body, "did not work") {
		t.Fatalf("failure page: %d", st)
	}
	if st, loc, _ := f.consentRaw(t, u3, q, "allow", []string{hh}); st != 200 || loc != nil {
		t.Fatalf("other subject granted: %d %v", st, loc)
	}
	// A provider that did not ask again (stale auth_time) does not confirm either.
	u4, _ := se.signIn(claims("sub-1"))
	_, loc, _ = f.consentRaw(t, u4, q, "sso", []string{hh})
	cb, _ = se.idp.authorize(loc.String(), claims("sub-1", map[string]any{"auth_time": se.clock.Now().Add(-time.Hour).Unix()}))
	if st, back, _ = u4.follow("GET", cb, nil); st != 303 || !strings.Contains(back, "sso=failed") {
		t.Fatalf("stale auth_time: %d %q", st, back)
	}
	// A callback in another browser (no flow cookie) does not confirm.
	u5, _ := se.signIn(claims("sub-1"))
	_, loc, _ = f.consentRaw(t, u5, q, "sso", []string{hh})
	cb, _ = se.idp.authorize(loc.String(), claims("sub-1"))
	if st, back, _ = u2.follow("GET", cb, nil); st != 303 || back != "/signin?sso=failed" {
		t.Fatalf("callback elsewhere: %d %q", st, back)
	}
	if st, loc, _ := f.consentRaw(t, u2, q, "allow", []string{hh}); st != 200 || loc != nil {
		t.Fatalf("callback elsewhere granted: %d %v", st, loc)
	}

	// Tampered request: the post re-validates the client and redirect URI before
	// anything is sent to the provider, and the way back validates them again.
	for name, bad := range map[string]url.Values{
		"redirect": authzQuery(cid, ch, "redirect_uri", "https://evil.example/cb"),
		"client":   authzQuery("no-such-client", ch),
		"pkce":     authzQuery(cid, "short"),
	} {
		st, loc, body := f.consentRaw(t, u, bad, "sso", []string{hh})
		if loc != nil && strings.HasPrefix(loc.String(), se.idp.srv.URL) {
			t.Errorf("%s: sent to the provider", name)
		}
		if name != "pkce" && (st != 400 || loc != nil) {
			t.Errorf("%s: %d %.100s", name, st, body)
		}
	}
	bu.RawQuery = func() string { q := bu.Query(); q.Set("redirect_uri", "https://evil.example/cb"); return q.Encode() }()
	if st, h, _ := u.browseGet(bu.String()); st != 400 || h.Get("Location") != "" {
		t.Fatalf("tampered return URL: %d %q", st, h.Get("Location"))
	}
	bu.RawQuery = func() string {
		q := bu.Query()
		q.Set("redirect_uri", oauthRedirect)
		q.Set("client_id", "x")
		return q.Encode()
	}()
	if st, _, _ := u.browseGet(bu.String()); st != 400 {
		t.Fatalf("tampered client: %d", st)
	}
	// What the sealed cookie may carry: only a local consent path.
	for _, n := range []string{"https://evil.example/oauth/authorize?x=1", "//evil.example/oauth/authorize?x=1",
		"/oauth/authorize?x=" + strings.Repeat("a", ssoMaxConsentNext), "/oauth/authorize?x=1\n"} {
		if got := flowNext(n); got != "/" {
			t.Errorf("flowNext(%.40q) = %q", n, got)
		}
	}
	if got := flowNext("/oauth/authorize?x=1"); got != "/oauth/authorize?x=1" {
		t.Errorf("flowNext = %q", got)
	}
}

func TestOAuthConsentSSOWithPasswordKeepsPassword(t *testing.T) {
	f, se := newSSOConsentEnv(t)
	cid := f.register(t, "App")
	_, ch := pkce()
	if b := f.page(t, f.a, cid, ch); strings.Contains(b, "Confirm with") || !strings.Contains(b, `name="password"`) {
		t.Fatal("unlinked account must see the password form only")
	}
	_ = se.link(f.e.signedInStepUp("a"), "sub-a")
	f.e.clock.Add(time.Second)
	a := se.signedIn("a")
	b := f.page(t, a, cid, ch)
	if !strings.Contains(b, `name="password"`) || !strings.Contains(b, "Confirm with Authelia") {
		t.Fatalf("a linked account with a password keeps the password: %s", b)
	}
	// The password path works as before.
	f.e.stepUp(a, "a")
	if st, loc, body := f.consentRaw(t, a, authzQuery(cid, ch), "allow", []string{f.hA}); st != http.StatusSeeOther || loc.Query().Get("code") == "" {
		t.Fatalf("password consent: %d %.200s", st, body)
	}
}
