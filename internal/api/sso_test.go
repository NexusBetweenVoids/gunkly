package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/coreos/go-oidc/v3/oidc/oidctest"

	"github.com/NexusBetweenVoids/gunkly/internal/sso"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

const (
	idpClient = "gunkly-test"
	idpSecret = "s3cret"
)

// fakeIdP is an OpenID Connect provider: discovery and JWKS (go-oidc's test
// server, with an ES256 key like Kanidm's), an authorization endpoint the test
// drives by hand and a token endpoint that checks PKCE and the client secret.
type fakeIdP struct {
	t      *testing.T
	srv    *httptest.Server
	key    *ecdsa.PrivateKey
	mu     sync.Mutex
	codes  map[string]idpGrant
	tokens int
	// What the next token response contains, per test.
	tweak func(claims map[string]any) // edits the ID token claims
	sign  *ecdsa.PrivateKey           // signs the token (default: the published key)
	// now is the clock for auth_time (the server's test clock).
	now func() time.Time
}

type idpGrant struct {
	challenge, nonce, redirect string
	claims                     map[string]any
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIdP{t: t, key: key, codes: map[string]idpGrant{}}
	disc := &oidctest.Server{
		PublicKeys: []oidctest.PublicKey{{PublicKey: key.Public(), KeyID: "k1", Algorithm: oidc.ES256}},
		Algorithms: []string{oidc.ES256},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", f.token)
	mux.Handle("/", disc)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	disc.SetIssuer(f.srv.URL)
	return f
}

// token is the token endpoint: PKCE S256 and the client secret are required.
func (f *fakeIdP) token(w http.ResponseWriter, r *http.Request) {
	fail := func(msg string) { http.Error(w, `{"error":"`+msg+`"}`, http.StatusBadRequest) }
	if r.ParseForm() != nil || r.PostForm.Get("grant_type") != "authorization_code" {
		fail("unsupported_grant_type")
		return
	}
	id, secret, basic := r.BasicAuth()
	if !basic {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if id != idpClient || secret != idpSecret {
		http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	g, ok := f.codes[r.PostForm.Get("code")]
	delete(f.codes, r.PostForm.Get("code")) // single use
	f.tokens++
	f.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if !ok || g.redirect != r.PostForm.Get("redirect_uri") || base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
		fail("invalid_grant")
		return
	}
	claims := map[string]any{"iss": f.srv.URL, "aud": idpClient, "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nonce": g.nonce}
	for k, v := range g.claims {
		claims[k] = v
	}
	if f.tweak != nil {
		f.tweak(claims)
	}
	b, _ := json.Marshal(claims)
	signer := f.key
	if f.sign != nil {
		signer = f.sign
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer",
		"id_token": oidctest.SignIDToken(signer, "k1", oidc.ES256, string(b))})
}

// ssoEnv is an env with single sign-on pointed at the fake provider.
type ssoEnv struct {
	*env
	idp *fakeIdP
}

func newSSOEnv(t *testing.T, mutate ...func(*Options)) *ssoEnv {
	t.Helper()
	idp := newFakeIdP(t)
	cfg := &sso.Config{Issuer: idp.srv.URL, ClientID: idpClient, ClientSecret: idpSecret, Name: "Authelia", Scopes: []string{"openid", "profile", "email"}}
	e := newEnv(t, append([]func(*Options){func(o *Options) { o.SSO = cfg; o.BaseURL = "http://localhost:4865" }}, mutate...)...)
	idp.now = e.clock.Now
	return &ssoEnv{env: e, idp: idp}
}

// follow sends a request with the client's cookies and does not follow redirects.
func (c *client) follow(method, path string, body any) (int, string, http.Header) {
	c.e.t.Helper()
	nc := &http.Client{Jar: c.c.Jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var rb *strings.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rb = strings.NewReader(string(b))
	} else {
		rb = strings.NewReader("")
	}
	req, _ := http.NewRequest(method, c.e.ts.URL+path, rb)
	if method != http.MethodGet {
		req.Header.Set("X-CSRF-Token", c.cookie(csrfCookie))
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := nc.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode, resp.Header.Get("Location"), resp.Header
}

// authorize plays the browser at the provider: it reads the authorization URL
// the app sent it to, "logs the user in" as claims and returns the callback
// path with the code and state.
func (f *fakeIdP) authorize(authURL string, claims map[string]any) (callback string, q url.Values) {
	f.t.Helper()
	u, err := url.Parse(authURL)
	if err != nil || !strings.HasPrefix(authURL, f.srv.URL+"/auth") {
		f.t.Fatalf("not an authorization URL: %q", authURL)
	}
	q = u.Query()
	if q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" ||
		q.Get("state") == "" || q.Get("nonce") == "" || q.Get("client_id") != idpClient ||
		q.Get("redirect_uri") != "http://localhost:4865/api/v1/auth/oidc/callback" ||
		!strings.Contains(" "+q.Get("scope")+" ", " openid ") {
		f.t.Fatalf("authorization request: %v", q)
	}
	// Like a real provider: a request with max_age gets auth_time (the user
	// just authenticated) unless the test sets its own.
	if q.Get("max_age") != "" && f.now != nil {
		if _, ok := claims["auth_time"]; !ok {
			c := map[string]any{"auth_time": f.now().Unix()}
			for k, v := range claims {
				c[k] = v
			}
			claims = c
		}
	}
	code := "code-" + strconv.Itoa(len(f.codes)) + "-" + q.Get("state")[:6]
	f.mu.Lock()
	f.codes[code] = idpGrant{challenge: q.Get("code_challenge"), nonce: q.Get("nonce"), redirect: q.Get("redirect_uri"), claims: claims}
	f.mu.Unlock()
	cb := url.Values{"code": {code}, "state": {q.Get("state")}, "iss": {f.srv.URL}}
	return "/api/v1/auth/oidc/callback?" + cb.Encode(), q
}

// signIn runs a whole SSO sign-in as a fresh browser: start, provider, callback.
// It returns the client and where the callback sent it.
func (e *ssoEnv) signIn(claims map[string]any) (*client, string) {
	e.t.Helper()
	c := e.client()
	st, loc, _ := c.follow("GET", "/api/v1/auth/oidc/start", nil)
	if st != http.StatusSeeOther {
		e.t.Fatalf("start: %d %s", st, loc)
	}
	cb, _ := e.idp.authorize(loc, claims)
	st, loc, _ = c.follow("GET", cb, nil)
	if st != http.StatusSeeOther {
		e.t.Fatalf("callback: %d", st)
	}
	return c, loc
}

func claims(sub string, more ...map[string]any) map[string]any {
	m := map[string]any{"sub": sub, "preferred_username": sub, "name": "Name " + sub}
	for _, x := range more {
		for k, v := range x {
			m[k] = v
		}
	}
	return m
}

func signedInAs(t *testing.T, c *client) string {
	t.Helper()
	st, me := c.do("GET", "/api/v1/me", nil)
	if st != 200 {
		return ""
	}
	return field(me, "username").(string)
}

func (e *ssoEnv) createOn(on bool) {
	e.t.Helper()
	if err := e.st.UpdateInstanceSettings(context.Background(), store.SettingsUpdate{SSOCreateAccounts: &on}); err != nil {
		e.t.Fatal(err)
	}
}

// link links the (step-up) client's account to the provider user sub.
func (e *ssoEnv) link(c *client, sub string) string {
	e.t.Helper()
	st, v := c.do("POST", "/api/v1/account/sso/link", nil)
	if st != 200 {
		e.t.Fatalf("link: %d %v", st, v)
	}
	cb, _ := e.idp.authorize(field(v, "url").(string), claims(sub))
	st, loc, _ := c.follow("GET", cb, nil)
	if st != http.StatusSeeOther {
		e.t.Fatalf("link callback: %d", st)
	}
	return loc
}

func TestSSOLinkThenSignInAndNoSecondFactor(t *testing.T) {
	e := newSSOEnv(t)
	e.user("ann", false)
	c := e.signedInStepUp("ann")
	other := e.signedIn("ann")
	if loc := e.link(c, "sub-ann"); loc != "/account/security?sso=linked" {
		t.Fatalf("link landed on %q", loc)
	}
	if st, _ := other.do("GET", "/api/v1/me", nil); st != 401 {
		t.Fatalf("linking must sign out other sessions: %d", st)
	}
	st, v := c.do("GET", "/api/v1/account/sso", nil)
	expect(t, st, v, 200, "")
	if field(v, "linked") != true || field(v, "name") != "Authelia" || field(v, "canUnlink") != true {
		t.Fatalf("status: %v", v)
	}
	// The provider handles MFA: a TOTP account is not asked for a code.
	e.stepUp(c, "ann")
	_, sv := c.do("POST", "/api/v1/account/totp/setup", nil)
	secret := field(sv, "secret").(string)
	if st, v := c.do("POST", "/api/v1/account/totp/confirm", map[string]string{"code": totpCode(secret, e.clock.Now())}); st != 200 {
		t.Fatalf("totp: %d %v", st, v)
	}
	sc, loc := e.signIn(claims("sub-ann"))
	if loc != "/" {
		t.Fatalf("sign-in landed on %q", loc)
	}
	if signedInAs(t, sc) != "ann" {
		t.Fatal("SSO sign-in did not sign ann in")
	}
	// The identity key is (issuer, sub): the same name under another sub is a stranger.
	if _, loc := e.signIn(claims("someone-else", map[string]any{"preferred_username": "ann"})); loc != "/signin?sso=no_account" {
		t.Fatalf("stranger landed on %q", loc)
	}
}

func TestSSOUnknownSubject(t *testing.T) {
	e := newSSOEnv(t)
	e.user("ann", false) // ann@example.com
	// Creation off (the default): a friendly "no account" result, nothing created.
	c, loc := e.signIn(claims("new-1"))
	if loc != "/signin?sso=no_account" || signedInAs(t, c) != "" {
		t.Fatalf("creation off: %q", loc)
	}
	if _, err := e.st.UserByLogin(context.Background(), "new-1"); err == nil {
		t.Fatal("an account was created while creation is off")
	}

	e.createOn(true)
	c, loc = e.signIn(claims("new-1", map[string]any{"preferred_username": "Jörg Müller!", "name": "Jörg"}))
	if loc != "/" {
		t.Fatalf("creation on: %q", loc)
	}
	if got := signedInAs(t, c); got != "J-rg-M-ller" {
		t.Fatalf("username %q", got)
	}
	u, _ := e.st.UserByLogin(context.Background(), "J-rg-M-ller")
	if u.DisplayName != "Jörg" || u.HasPassword() || u.Email != "" {
		t.Fatalf("account: %+v", u)
	}
	// The same subject signs into the same account; no second account appears.
	if c2, _ := e.signIn(claims("new-1", map[string]any{"preferred_username": "other"})); signedInAs(t, c2) != "J-rg-M-ller" {
		t.Fatal("second sign-in did not reach the same account")
	}
	// A taken name gets a suffix.
	c3, _ := e.signIn(claims("new-2", map[string]any{"preferred_username": "J-rg-M-ller"}))
	if got := signedInAs(t, c3); got != "J-rg-M-ller-2" {
		t.Fatalf("de-duplicated username %q", got)
	}
	// A password never works for such an account.
	for _, pw := range []string{"", "!sso", "password"} {
		if st, _ := e.client().do("POST", "/api/v1/session", map[string]string{"login": "J-rg-M-ller", "password": pw}); st == 200 {
			t.Fatalf("password %q signed in an SSO-only account", pw)
		}
	}
}

func TestSSONeverLinksByEmail(t *testing.T) {
	e := newSSOEnv(t)
	e.createOn(true)
	e.user("ann", false) // ann@example.com
	c, _ := e.signIn(claims("attacker", map[string]any{"preferred_username": "ann", "email": "ann@example.com", "email_verified": true}))
	if got := signedInAs(t, c); got == "ann" || got == "" {
		t.Fatalf("signed in as %q: the provider's email must not reach ann's account", got)
	}
	ann, _ := e.st.UserByLogin(context.Background(), "ann")
	if ident, err := e.st.SSOIdentityForUser(context.Background(), ann.ID, e.idp.srv.URL); err == nil {
		t.Fatalf("ann was linked: %+v", ident)
	}
}

func TestSSOEmailOnlyWhenVerifiedAndEmailOn(t *testing.T) {
	// Email off: never stored.
	e := newSSOEnv(t)
	e.createOn(true)
	e.signIn(claims("aa", map[string]any{"email": "aa@example.org", "email_verified": true}))
	if u, err := e.st.UserByLogin(context.Background(), "aa"); err != nil || u.Email != "" {
		t.Fatalf("email stored with email off: %q", u.Email)
	}
	// Email on: stored when verified (also as the string "true"), not when unverified.
	m := newMailEnv(t, func(o *Options) {
		o.SSO = &sso.Config{Issuer: e.idp.srv.URL, ClientID: idpClient, ClientSecret: idpSecret, Scopes: []string{"openid"}}
		o.BaseURL = "http://localhost:4865"
	})
	m2 := &ssoEnv{env: m.env, idp: e.idp}
	m2.createOn(true)
	m2.signIn(claims("vv", map[string]any{"email": "vv@example.org", "email_verified": "true"}))
	m2.signIn(claims("uu", map[string]any{"email": "uu@example.org", "email_verified": false}))
	v, err1 := m2.st.UserByLogin(context.Background(), "vv")
	u, err2 := m2.st.UserByLogin(context.Background(), "uu")
	if err1 != nil || err2 != nil || v.Email != "vv@example.org" || !v.EmailVerified || u.Email != "" {
		t.Fatalf("verified %+v, unverified %+v", v, u)
	}
}

func TestSSORejectsBadFlows(t *testing.T) {
	e := newSSOEnv(t)
	e.createOn(true)
	start := func() (*client, string) {
		c := e.client()
		_, loc, _ := c.follow("GET", "/api/v1/auth/oidc/start", nil)
		return c, loc
	}
	callbackFor := func(c *client, loc string, claims map[string]any, edit func(url.Values)) (int, string) {
		cb, _ := e.idp.authorize(loc, claims)
		u, _ := url.Parse(cb)
		q := u.Query()
		if edit != nil {
			edit(q)
		}
		st, to, _ := c.follow("GET", u.Path+"?"+q.Encode(), nil)
		return st, to
	}
	denied := func(name string, c *client, st int, to string) {
		t.Helper()
		if st != http.StatusSeeOther || to != "/signin?sso=failed" || signedInAs(t, c) != "" {
			t.Fatalf("%s: %d %q (signed in as %q)", name, st, to, signedInAs(t, c))
		}
	}

	c, loc := start()
	st, to := callbackFor(c, loc, claims("x"), func(q url.Values) { q.Set("state", "forged") })
	denied("bad state", c, st, to)

	c, loc = start()
	st, to = callbackFor(c, loc, claims("x"), func(q url.Values) { q.Set("iss", "https://evil.example") })
	denied("mix-up (iss)", c, st, to)

	// No cookie: a callback somebody else started (login CSRF).
	c, loc = start()
	cb, _ := e.idp.authorize(loc, claims("x"))
	st2, to2, _ := e.client().follow("GET", cb, nil)
	denied("no cookie", c, st2, to2)

	// The flow is single use.
	c, loc = start()
	cb, _ = e.idp.authorize(loc, claims("x"))
	if st, to, _ := c.follow("GET", cb, nil); to != "/" || st != 303 {
		t.Fatalf("first callback: %d %q", st, to)
	}
	c2 := e.client()
	st3, to3, _ := c2.follow("GET", cb, nil)
	denied("replay", c2, st3, to3)

	for name, tweak := range map[string]func(map[string]any){
		"wrong nonce":    func(m map[string]any) { m["nonce"] = "not-mine" },
		"no nonce":       func(m map[string]any) { delete(m, "nonce") },
		"wrong audience": func(m map[string]any) { m["aud"] = "someone-else" },
		"expired":        func(m map[string]any) { m["exp"] = e.clock.Now().Add(-time.Hour).Unix() },
		"wrong issuer":   func(m map[string]any) { m["iss"] = "https://evil.example" },
		"no subject":     func(m map[string]any) { delete(m, "sub") },
		"two audiences, bad azp": func(m map[string]any) {
			m["aud"] = []string{idpClient, "other"}
			m["azp"] = "other"
		},
	} {
		e.idp.tweak = tweak
		c, loc := start()
		st, to := callbackFor(c, loc, claims("x"), nil)
		denied(name, c, st, to)
	}
	e.idp.tweak = nil

	// Signed by a key the provider does not publish.
	other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	e.idp.sign = other
	c, loc = start()
	st, to = callbackFor(c, loc, claims("x"), nil)
	denied("unknown signing key", c, st, to)
	e.idp.sign = nil

	// The provider refused (the user said no).
	c, loc = start()
	_, q := e.idp.authorize(loc, claims("x"))
	st4, to4, _ := c.follow("GET", "/api/v1/auth/oidc/callback?error=access_denied&state="+q.Get("state"), nil)
	if st4 != 303 || to4 != "/signin?sso=denied" {
		t.Fatalf("denied: %d %q", st4, to4)
	}
	// Nothing was ever created by the failures above.
	if _, err := e.st.UserByLogin(context.Background(), "x"); err == nil {
		t.Fatal("a failed sign-in created an account")
	}
}

func TestSSOUnlinkNeedsAnotherWayIn(t *testing.T) {
	e := newSSOEnv(t)
	e.user("ann", false)
	c := e.signedInStepUp("ann")
	e.link(c, "sub-ann")
	// A password account may unlink (step-up; other sessions out).
	other, _ := e.signIn(claims("sub-ann"))
	if st, v := c.do("DELETE", "/api/v1/account/sso", nil); st != 204 {
		t.Fatalf("unlink: %d %v", st, v)
	}
	if st, _ := other.do("GET", "/api/v1/me", nil); st != 401 {
		t.Fatalf("unlinking must sign out other sessions: %d", st)
	}
	if _, loc := e.signIn(claims("sub-ann")); loc != "/signin?sso=no_account" {
		t.Fatalf("unlinked identity still signs in: %q", loc)
	}
	if st, v := c.do("DELETE", "/api/v1/account/sso", nil); st != 404 {
		t.Fatalf("second unlink: %d %v", st, v)
	}

	// An account created by SSO has no password: it cannot unlink until it has one.
	e.createOn(true)
	nc, _ := e.signIn(claims("sub-new"))
	if st, v := nc.do("GET", "/api/v1/me", nil); st != 200 || field(v, "hasPassword") != false || field(v, "ssoLinked") != true {
		t.Fatalf("me: %d %v", st, v)
	}
	// Step-up for it goes through the provider, which is asked to authenticate again.
	st, v := nc.do("POST", "/api/v1/session/step-up/sso", map[string]string{"next": "/account/security"})
	expect(t, st, v, 200, "")
	authURL := field(v, "url").(string)
	u, _ := url.Parse(authURL)
	if u.Query().Get("prompt") != "login" || u.Query().Get("max_age") != "0" {
		t.Fatalf("step-up must force a new login: %s", authURL)
	}
	if st, v := nc.do("POST", "/api/v1/account/password", map[string]string{"newPassword": "a new long password"}); st != 403 {
		t.Fatalf("password change before step-up: %d %v", st, v)
	}
	cb, _ := e.idp.authorize(authURL, claims("sub-new"))
	if st, loc, _ := nc.follow("GET", cb, nil); st != 303 || loc != "/account/security?sso=stepup" {
		t.Fatalf("step-up callback: %d %q", st, loc)
	}
	if st, v := nc.do("DELETE", "/api/v1/account/sso", nil); st != 409 || code(v) != "sso_last_login" {
		t.Fatalf("unlink without password: %d %v", st, v)
	}
	if st, v := nc.do("POST", "/api/v1/account/password", map[string]string{"newPassword": "a new long password"}); st != 204 {
		t.Fatalf("set a password: %d %v", st, v)
	}
}

func TestSSOStepUpOnlyForTheLinkedIdentity(t *testing.T) {
	e := newSSOEnv(t)
	e.createOn(true)
	nc, _ := e.signIn(claims("sub-1"))
	_, v := nc.do("POST", "/api/v1/session/step-up/sso", map[string]string{})
	// Authenticating as somebody else at the provider does not confirm this account.
	cb, _ := e.idp.authorize(field(v, "url").(string), claims("sub-2"))
	if st, loc, _ := nc.follow("GET", cb, nil); st != 303 || loc != "/account/security?sso=failed" {
		t.Fatalf("wrong identity: %d %q", st, loc)
	}
	if st, _ := nc.do("POST", "/api/v1/account/tokens", map[string]any{}); st != 403 {
		t.Fatalf("step-up granted to the wrong identity: %d", st)
	}
	// An old authentication (the provider ignoring prompt=login) is not a step-up.
	_, v = nc.do("POST", "/api/v1/session/step-up/sso", map[string]string{})
	cb, _ = e.idp.authorize(field(v, "url").(string), claims("sub-1", map[string]any{"auth_time": e.clock.Now().Add(-time.Hour).Unix()}))
	if st, loc, _ := nc.follow("GET", cb, nil); st != 303 || loc != "/account/security?sso=failed" {
		t.Fatalf("stale auth_time: %d %q", st, loc)
	}
	// A user without a linked identity has no SSO step-up.
	e.user("bob", false)
	b := e.signedIn("bob")
	if st, v := b.do("POST", "/api/v1/session/step-up/sso", map[string]string{}); st != 404 {
		t.Fatalf("unlinked step-up: %d %v", st, v)
	}
}

func TestSSOLinkConflictAndSession(t *testing.T) {
	e := newSSOEnv(t)
	e.user("ann", false)
	e.user("bob", false)
	a, b := e.signedInStepUp("ann"), e.signedInStepUp("bob")
	e.link(a, "shared")
	if loc := e.link(b, "shared"); loc != "/account/security?sso=already_linked" {
		t.Fatalf("one identity, two accounts: %q", loc)
	}
	if st, v := a.do("POST", "/api/v1/account/sso/link", nil); st != 409 || code(v) != "sso_already_linked" {
		t.Fatalf("link twice: %d %v", st, v)
	}
	// Linking needs the step-up and the same session the flow started in.
	e.user("cy", false)
	c := e.signedInStepUp("cy")
	e.clock.Add(14 * time.Minute) // the step-up is almost out of its window
	_, v := c.do("POST", "/api/v1/account/sso/link", nil)
	cb, _ := e.idp.authorize(field(v, "url").(string), claims("sub-cy"))
	e.clock.Add(2 * time.Minute) // and runs out while the user is at the provider
	if st, loc, _ := c.follow("GET", cb, nil); st != 303 || loc != "/account/security?sso=failed" {
		t.Fatalf("link after the step-up expired: %d %q", st, loc)
	}
	if st, _ := c.do("GET", "/api/v1/account/sso", nil); st != 200 {
		t.Fatal("session lost")
	}
}

func TestSSOOffInHostedModeAndWhenNotConfigured(t *testing.T) {
	paths := []struct{ method, path string }{
		{"GET", "/api/v1/auth/oidc/start"}, {"GET", "/api/v1/auth/oidc/callback"}, {"GET", "/api/v1/account/sso"},
		{"POST", "/api/v1/account/sso/link"}, {"DELETE", "/api/v1/account/sso"}, {"POST", "/api/v1/session/step-up/sso"},
	}
	check := func(name string, e *env) {
		e.user("ann", false)
		c := e.signedInStepUp("ann")
		for _, p := range paths {
			if st, _, _ := c.follow(p.method, p.path, nil); st != 404 {
				t.Errorf("%s: %s %s answered %d", name, p.method, p.path, st)
			}
		}
		_, inst := e.client().do("GET", "/api/v1/instance", nil)
		if _, has := inst.(map[string]any)["sso"]; has {
			t.Errorf("%s: the instance advertises single sign-on", name)
		}
	}
	idp := newFakeIdP(t)
	cfg := &sso.Config{Issuer: idp.srv.URL, ClientID: idpClient, Scopes: []string{"openid"}}
	check("hosted", newEnv(t, func(o *Options) { o.SSO = cfg; o.HostedMode = true; o.BaseURL = "https://gunkly.example.com" }))
	check("not configured", newEnv(t))
	// Configured: advertised, with the button label.
	e := newSSOEnv(t)
	if _, inst := e.client().do("GET", "/api/v1/instance", nil); field(field(inst, "sso"), "name") != "Authelia" {
		t.Fatalf("instance: %v", inst)
	}
}

func TestSSOProviderDownAndRestricted(t *testing.T) {
	e := newSSOEnv(t)
	e.idp.srv.Close()
	c := e.client()
	if st, loc, _ := c.follow("GET", "/api/v1/auth/oidc/start", nil); st != 303 || loc != "/signin?sso=unavailable" {
		t.Fatalf("down: %d %q", st, loc)
	}
}

// The provider's own host is trusted (it is on the LAN); any other host its
// discovery document names still goes through the private-address guard.
func TestSSOGuardsOtherHostsInDiscovery(t *testing.T) {
	idp := newFakeIdP(t)
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the guard let a request through to another loopback address")
	}))
	defer elsewhere.Close()
	// Rewrite the discovery document so the token endpoint is on another loopback port.
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		resp, err := http.Get(idp.srv.URL + r.URL.Path)
		if err != nil {
			t.Error(err)
			return
		}
		defer resp.Body.Close()
		var d map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&d)
		d["issuer"] = "http://" + r.Host
		d["token_endpoint"] = elsewhere.URL + "/token"
		_ = json.NewEncoder(w).Encode(d)
	}))
	defer front.Close()
	cfg := &sso.Config{Issuer: front.URL, ClientID: idpClient, ClientSecret: idpSecret, Scopes: []string{"openid"}}
	e := newEnv(t, func(o *Options) { o.SSO = cfg; o.BaseURL = "http://localhost:4865" })
	c := e.client()
	st, loc, _ := c.follow("GET", "/api/v1/auth/oidc/start", nil)
	if st != 303 || !strings.HasPrefix(loc, idp.srv.URL+"/auth") {
		t.Fatalf("start: %d %q", st, loc)
	}
	u, _ := url.Parse(loc)
	cb := url.Values{"code": {"x"}, "state": {u.Query().Get("state")}}
	if st, to, _ := c.follow("GET", "/api/v1/auth/oidc/callback?"+cb.Encode(), nil); st != 303 || to != "/signin?sso=failed" {
		t.Fatalf("callback: %d %q", st, to)
	}
}

func TestSSOUsernameDerivation(t *testing.T) {
	for in, want := range map[string]string{
		"alice": "alice", "Alice.Smith": "Alice.Smith", "a b": "a-b", "  ": "", "x": "", "---a1": "a1", "ü": "",
		"first_last-1": "first_last-1", "trailing...": "trailing", strings.Repeat("a", 40): strings.Repeat("a", 28),
	} {
		if got := ssoUsername(in); got != want {
			t.Errorf("ssoUsername(%q) = %q, want %q", in, got, want)
		}
		if got := ssoUsername(in); got != "" && !usernameRE.MatchString(got) {
			t.Errorf("ssoUsername(%q) = %q is not a valid username", in, got)
		}
	}
}

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{
		"": "/", "/": "/", "/account": "/account", "//evil.example": "/", "/\\evil.example": "/", "https://evil.example": "/",
		"evil": "/", "/ok?x=1": "/ok?x=1", "/a\r\nSet-Cookie: x": "/",
		"/\t/evil.example": "/", "/\v/evil.example": "/", "/\t\\evil.example": "/", "/a\x7fb": "/", "/%09/evil.example": "/%09/evil.example",
	} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

// The button name: the admin's setting, else GUNKLY_OIDC_NAME, else "" (the
// app then shows the generic "SSO").
func TestSSOButtonName(t *testing.T) {
	e := newSSOEnv(t)
	name := func() any {
		_, v := e.client().do("GET", "/api/v1/instance", nil)
		return field(field(v, "sso"), "name")
	}
	set := func(n string) error {
		return e.st.UpdateInstanceSettings(context.Background(), store.SettingsUpdate{SSOName: &n})
	}
	if got := name(); got != "Authelia" {
		t.Fatalf("env default: %v", got)
	}
	if err := set("  Our Keycloak  "); err != nil {
		t.Fatal(err)
	}
	if got := name(); got != "Our Keycloak" {
		t.Fatalf("admin setting (trimmed): %v", got)
	}
	for _, bad := range []string{strings.Repeat("x", 51), "a\nb", "a\x00b"} {
		if err := set(bad); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("%q: %v, want ErrInvalid", bad, err)
		}
	}
	if err := set(""); err != nil {
		t.Fatal(err)
	}
	if got := name(); got != "Authelia" {
		t.Fatalf("cleared: back to the env default, got %v", got)
	}
	g := newSSOEnv(t, func(o *Options) { o.SSO.Name = "" })
	if _, v := g.client().do("GET", "/api/v1/instance", nil); field(field(v, "sso"), "name") != "" {
		t.Fatalf("no name anywhere: %v", v)
	}
}

// auditReasons lists the reasons of the caller's login_failure audit entries.
func auditReasons(t *testing.T, c *client) []string {
	t.Helper()
	st, v := c.do("GET", "/api/v1/account/audit", nil)
	if st != 200 {
		t.Fatalf("audit: %d %v", st, v)
	}
	var out []string
	for _, ev := range v.([]any) {
		if field(ev, "event") == store.AuditLoginFailure {
			out = append(out, fmt.Sprint(field(field(ev, "meta"), "via"), "/", field(field(ev, "meta"), "reason")))
		}
	}
	return out
}

// An SSO step-up needs auth_time (the request said max_age=0); failed ones are audited.
func TestSSOStepUpNeedsAuthTimeAndIsAudited(t *testing.T) {
	e := newSSOEnv(t)
	e.createOn(true)
	nc, _ := e.signIn(claims("sub-1"))
	step := func(c map[string]any) string {
		_, v := nc.do("POST", "/api/v1/session/step-up/sso", map[string]string{})
		cb, _ := e.idp.authorize(field(v, "url").(string), c)
		_, loc, _ := nc.follow("GET", cb, nil)
		return loc
	}
	e.idp.now = nil // this provider leaves auth_time out
	if loc := step(claims("sub-1")); loc != "/account/security?sso=failed" {
		t.Fatalf("no auth_time: %q", loc)
	}
	if loc := step(claims("sub-2", map[string]any{"auth_time": e.clock.Now().Unix()})); loc != "/account/security?sso=failed" {
		t.Fatalf("other subject: %q", loc)
	}
	got := strings.Join(auditReasons(t, nc), ",")
	if !strings.Contains(got, "sso_stepup/auth_time_missing") || !strings.Contains(got, "sso_stepup/subject") {
		t.Fatalf("failed step-ups not audited: %q", got)
	}
	if loc := step(claims("sub-1", map[string]any{"auth_time": e.clock.Now().Unix()})); !strings.HasSuffix(loc, "?sso=stepup") {
		t.Fatalf("fresh auth_time: %q", loc)
	}
}

// The admin settings describe the single sign-on setup (never the secret), so
// an admin can see what GUNKLY_OIDC_* configured and what to register.
func TestSSOSetupShownToAdmins(t *testing.T) {
	e := newSSOEnv(t)
	e.user("root", true)
	st, v := e.signedInStepUp("root").do("GET", "/api/v1/admin/settings", nil)
	if st != 200 || field(v, "ssoConfigured") != true || field(v, "ssoAvailable") != true || field(v, "ssoIssuer") != e.idp.srv.URL ||
		field(v, "ssoClientId") != idpClient || field(v, "ssoCallback") != "http://localhost:4865/api/v1/auth/oidc/callback" ||
		len(field(v, "ssoScopes").([]any)) != 3 {
		t.Fatalf("configured: %d %v", st, v)
	}
	if b, _ := json.Marshal(v); strings.Contains(string(b), idpSecret) {
		t.Fatal("the client secret reached the admin page")
	}
	off := newEnv(t, func(o *Options) { o.BaseURL = "https://gunkly.example.com" })
	off.user("root", true)
	_, v = off.signedInStepUp("root").do("GET", "/api/v1/admin/settings", nil)
	if field(v, "ssoConfigured") != false || field(v, "ssoAvailable") != true || field(v, "ssoIssuer") != nil ||
		field(v, "ssoCallback") != "https://gunkly.example.com/api/v1/auth/oidc/callback" {
		t.Fatalf("not configured: %v", v)
	}
}
