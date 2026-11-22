package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

const (
	oauthBase     = "http://localhost" // a loopback http base URL may enable OAuth (development)
	oauthRedirect = "https://app.example/callback"
)

var noRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

type oauthFix struct {
	*tokenEnv
}

// newOAuthEnv is the household fixture with OAuth enabled through the setting.
func newOAuthEnv(t *testing.T) *oauthFix {
	f := newHH(t, func(o *Options) { o.BaseURL = oauthBase })
	te := &tokenEnv{hhFixture: f}
	te.aStep = f.e.signedInStepUp("a")
	setOAuth(t, f.e, true)
	return &oauthFix{te}
}

func setOAuth(t *testing.T, e *env, on bool) {
	t.Helper()
	if err := e.st.UpdateInstanceSettings(context.Background(), store.SettingsUpdate{OAuthEnabled: &on}); err != nil {
		t.Fatal(err)
	}
	e.srv.forgetMCP()
}

// raw is a request without cookies (an OAuth client is not a browser).
func (e *env) oauthRaw(method, path string, form url.Values, jsonBody any, hdr ...string) (int, http.Header, []byte) {
	e.t.Helper()
	var body io.Reader
	ct := ""
	switch {
	case jsonBody != nil:
		b, _ := json.Marshal(jsonBody)
		body, ct = strings.NewReader(string(b)), "application/json"
	case form != nil:
		body, ct = strings.NewReader(form.Encode()), "application/x-www-form-urlencoded"
	}
	req, _ := http.NewRequest(method, e.ts.URL+path, body)
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := (&http.Client{CheckRedirect: noRedirect}).Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, b
}

func asMap(b []byte) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

// browser is the signed-in user's client without redirect following.
func (c *client) browser(method, path string, form url.Values, hdr ...string) (int, http.Header, string) {
	c.e.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, c.e.ts.URL+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := (&http.Client{Jar: c.c.Jar, CheckRedirect: noRedirect}).Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(b)
}

func (c *client) browseGet(path string) (int, http.Header, string) {
	c.e.t.Helper()
	return c.browser("GET", path, nil)
}

func pkce() (verifier, challenge string) {
	verifier = ids.Token()
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func (f *oauthFix) register(t *testing.T, name string, uris ...string) string {
	t.Helper()
	if len(uris) == 0 {
		uris = []string{oauthRedirect}
	}
	st, _, b := f.e.oauthRaw("POST", "/oauth/register", nil, map[string]any{"client_name": name, "redirect_uris": uris})
	if st != 201 {
		t.Fatalf("register: %d %s", st, b)
	}
	return asMap(b)["client_id"].(string)
}

func authzQuery(clientID, challenge string, extra ...string) url.Values {
	q := url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {oauthRedirect},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "state": {"st-1"}}
	for i := 0; i+1 < len(extra); i += 2 {
		q.Set(extra[i], extra[i+1])
	}
	return q
}

// consent posts the user's decision the way the page's form does and returns
// the redirect it answers with.
func (f *oauthFix) consent(t *testing.T, u *client, q url.Values, decision string, hhs []string, extra ...string) (int, *url.URL, string) {
	t.Helper()
	f.e.stepUp(u, f.nameOf(u)) // the consent page asks for a password unless the session re-verified: tested separately
	return f.consentRaw(t, u, q, decision, hhs, extra...)
}

// nameOf is the username behind one of the fixture's clients.
func (f *oauthFix) nameOf(u *client) string {
	for n, c := range map[string]*client{"a": f.a, "m": f.m, "g": f.g, "b": f.b, "x": f.x} {
		if c == u {
			return n
		}
	}
	f.e.t.Fatal("unknown client")
	return ""
}

// consentRaw posts the decision as is (no step-up first).
func (f *oauthFix) consentRaw(t *testing.T, u *client, q url.Values, decision string, hhs []string, extra ...string) (int, *url.URL, string) {
	t.Helper()
	form := url.Values{"csrf": {u.cookie(csrfCookie)}, "decision": {decision}, "hh": hhs}
	for k, v := range q {
		form[k] = v
	}
	for i := 0; i+1 < len(extra); i += 2 {
		form.Set(extra[i], extra[i+1])
	}
	st, h, body := u.browser("POST", "/oauth/authorize", form)
	if st != http.StatusSeeOther {
		return st, nil, body
	}
	loc, err := url.Parse(h.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return st, loc, body
}

// grant runs register-less consent for an existing client and returns the code.
func (f *oauthFix) code(t *testing.T, u *client, clientID, challenge string, hhs []string, extra ...string) string {
	t.Helper()
	q := authzQuery(clientID, challenge)
	st, loc, body := f.consent(t, u, q, "allow", hhs, extra...)
	if st != http.StatusSeeOther || loc.Query().Get("code") == "" {
		t.Fatalf("consent: %d %v %s", st, loc, body)
	}
	if loc.Query().Get("state") != "st-1" || loc.Query().Get("iss") != oauthBase {
		t.Fatalf("redirect params %v", loc.Query())
	}
	return loc.Query().Get("code")
}

func (f *oauthFix) token(form url.Values) (int, map[string]any, http.Header) {
	st, h, b := f.e.oauthRaw("POST", "/oauth/token", form, nil)
	return st, asMap(b), h
}

type tokens struct{ access, refresh string }

// exchange redeems a code and returns the token pair.
func (f *oauthFix) exchange(t *testing.T, clientID, code, verifier string) tokens {
	t.Helper()
	st, m, h := f.token(url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code},
		"redirect_uri": {oauthRedirect}, "code_verifier": {verifier}})
	if st != 200 {
		t.Fatalf("token: %d %v", st, m)
	}
	if h.Get("Cache-Control") != "no-store" || m["token_type"] != "Bearer" || m["expires_in"].(float64) != 3600 {
		t.Fatalf("token response %v %v", m, h)
	}
	a, r := m["access_token"].(string), m["refresh_token"].(string)
	if !strings.HasPrefix(a, "gko_") || len(a) != 47 || !strings.HasPrefix(r, "gkr_") || len(r) != 47 {
		t.Fatalf("token shapes %q %q", a, r)
	}
	return tokens{a, r}
}

// connectUser runs the whole flow for user u and returns the tokens.
func (f *oauthFix) connectUser(t *testing.T, u *client, name string, hhs []string, extra ...string) (string, tokens) {
	t.Helper()
	cid := f.register(t, name)
	ver, ch := pkce()
	return cid, f.exchange(t, cid, f.code(t, u, cid, ch, hhs, extra...), ver)
}

func TestOAuthDisabledIs404(t *testing.T) {
	f := newHH(t, func(o *Options) { o.BaseURL = oauthBase })
	e := f.e
	for _, r := range []struct{ method, path string }{
		{"GET", "/.well-known/oauth-protected-resource"}, {"GET", "/.well-known/oauth-protected-resource/mcp"},
		{"GET", "/.well-known/oauth-authorization-server"}, {"POST", "/oauth/register"}, {"GET", "/oauth/authorize"},
		{"POST", "/oauth/authorize"}, {"POST", "/oauth/token"}, {"POST", "/oauth/revoke"},
	} {
		if st, _, _ := e.oauthRaw(r.method, r.path, nil, nil); st != 404 {
			t.Errorf("%s %s: %d, want 404", r.method, r.path, st)
		}
	}
	// /mcp keeps today's plain challenge; nothing about OAuth is advertised.
	st, h, _ := e.rawMCP("POST", "", `{}`)
	if st != 401 || h.Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("/mcp: %d %q", st, h.Get("WWW-Authenticate"))
	}
	if st, _, _ := e.rawMCP("POST", "gko_"+strings.Repeat("a", 43), `{}`); st != 401 {
		t.Fatalf("access token while off: %d", st)
	}
	if _, v := f.a.do("GET", "/api/v1/instance", nil); field(v, "oauthEnabled") != nil {
		t.Fatalf("instance advertises OAuth: %v", v)
	}
	// Even with the setting on, a base URL that is not https cannot serve it.
	f2 := newHH(t, func(o *Options) { o.BaseURL = "http://gunkly.example" })
	setOAuth(t, f2.e, true)
	if st, _, _ := f2.e.oauthRaw("GET", "/.well-known/oauth-authorization-server", nil, nil); st != 404 {
		t.Fatalf("http base URL: %d", st)
	}
}

func TestOAuthEnableNeedsHTTPS(t *testing.T) {
	for base, want := range map[string]int{"": 400, "http://gunkly.example": 400, "https://gunkly.example": 200, "http://localhost:8080": 200, "http://127.0.0.1": 200} {
		e := newEnv(t, func(o *Options) { o.BaseURL = base })
		e.user("admin", true)
		c := e.signedInStepUp("admin")
		st, v := c.do("PATCH", "/api/v1/admin/settings", map[string]any{"oauthEnabled": true})
		if st != want || want == 400 && code(v) != "oauth_requires_https" {
			t.Errorf("base %q: %d %v", base, st, v)
		}
		if want == 200 && field(v, "oauthEnabled") != true {
			t.Errorf("base %q: %v", base, v)
		}
		if _, v := c.do("GET", "/api/v1/admin/settings", nil); field(v, "oauthEnabled") != (want == 200) {
			t.Errorf("base %q: settings %v", base, v)
		}
	}
	// Off by default, also on a fresh instance.
	e := newEnv(t, func(o *Options) { o.BaseURL = "https://gunkly.example" })
	e.user("admin", true)
	if _, v := e.signedIn("admin").do("GET", "/api/v1/admin/settings", nil); field(v, "oauthEnabled") != false || field(v, "oauthPossible") != true {
		t.Fatalf("defaults %v", v)
	}
}

func TestOAuthMetadata(t *testing.T) {
	f := newOAuthEnv(t)
	for _, p := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		st, _, b := f.e.oauthRaw("GET", p, nil, nil)
		m := asMap(b)
		if st != 200 || m["resource"] != oauthBase+"/mcp" || m["authorization_servers"].([]any)[0] != oauthBase ||
			len(m["scopes_supported"].([]any)) != 3 || m["bearer_methods_supported"].([]any)[0] != "header" {
			t.Errorf("%s: %d %s", p, st, b)
		}
	}
	st, _, b := f.e.oauthRaw("GET", "/.well-known/oauth-authorization-server", nil, nil)
	m := asMap(b)
	if st != 200 || m["issuer"] != oauthBase || m["authorization_endpoint"] != oauthBase+"/oauth/authorize" ||
		m["token_endpoint"] != oauthBase+"/oauth/token" || m["registration_endpoint"] != oauthBase+"/oauth/register" ||
		m["revocation_endpoint"] != oauthBase+"/oauth/revoke" {
		t.Fatalf("AS metadata: %d %s", st, b)
	}
	for k, want := range map[string]string{"code_challenge_methods_supported": "S256", "token_endpoint_auth_methods_supported": "none",
		"response_types_supported": "code"} {
		if l := m[k].([]any); len(l) != 1 || l[0] != want {
			t.Errorf("%s = %v", k, l)
		}
	}
	if g := m["grant_types_supported"].([]any); len(g) != 2 || g[0] != "authorization_code" || g[1] != "refresh_token" {
		t.Errorf("grant types %v", g)
	}
	// The /mcp challenge now points at the metadata.
	_, h, _ := f.e.rawMCP("POST", "", `{}`)
	if h.Get("WWW-Authenticate") != `Bearer resource_metadata="`+oauthBase+`/.well-known/oauth-protected-resource"` {
		t.Errorf("challenge %q", h.Get("WWW-Authenticate"))
	}
}

func TestOAuthRegistrationValidation(t *testing.T) {
	f := newOAuthEnv(t)
	long := strings.Repeat("n", 101)
	for _, c := range []struct {
		name string
		body any
		want int
	}{
		{"ok https", map[string]any{"client_name": "A", "redirect_uris": []string{"https://a.example/cb"}}, 201},
		{"ok loopback any port", map[string]any{"client_name": "A", "redirect_uris": []string{"http://127.0.0.1:5173/cb", "http://localhost/cb", "http://[::1]:9/x"}}, 201},
		{"ok no name", map[string]any{"redirect_uris": []string{"https://a.example/cb"}}, 201},
		{"ok asks for a secret, gets none", map[string]any{"client_name": "A", "redirect_uris": []string{"https://a.example/cb"}, "token_endpoint_auth_method": "client_secret_post"}, 201},
		{"no uris", map[string]any{"client_name": "A"}, 400},
		{"http non-loopback", map[string]any{"redirect_uris": []string{"http://a.example/cb"}}, 400},
		{"fragment", map[string]any{"redirect_uris": []string{"https://a.example/cb#x"}}, 400},
		{"wildcard", map[string]any{"redirect_uris": []string{"https://*.example/cb"}}, 400},
		{"custom scheme", map[string]any{"redirect_uris": []string{"myapp://cb"}}, 400},
		{"javascript", map[string]any{"redirect_uris": []string{"javascript:alert(1)"}}, 400},
		{"userinfo", map[string]any{"redirect_uris": []string{"https://u:p@a.example/cb"}}, 400},
		{"relative", map[string]any{"redirect_uris": []string{"/cb"}}, 400},
		{"too many", map[string]any{"redirect_uris": []string{"https://1.example/", "https://2.example/", "https://3.example/", "https://4.example/", "https://5.example/", "https://6.example/"}}, 400},
		{"name too long", map[string]any{"client_name": long, "redirect_uris": []string{"https://a.example/cb"}}, 400},
		{"control char in name", map[string]any{"client_name": "a\nb", "redirect_uris": []string{"https://a.example/cb"}}, 400},
		{"no authorization_code grant", map[string]any{"redirect_uris": []string{"https://a.example/cb"}, "grant_types": []string{"implicit"}}, 400},
		{"not json", nil, 400},
	} {
		var st int
		var b []byte
		if c.body == nil {
			st, _, b = f.e.oauthRaw("POST", "/oauth/register", nil, nil)
		} else {
			st, _, b = f.e.oauthRaw("POST", "/oauth/register", nil, c.body)
		}
		if st != c.want {
			t.Errorf("%s: %d %s", c.name, st, b)
		}
		if c.want == 201 {
			m := asMap(b)
			if m["token_endpoint_auth_method"] != "none" || m["client_secret"] != nil || m["client_id"] == "" {
				t.Errorf("%s: %s", c.name, b)
			}
		}
	}
	// Public clients only, and a JSON body is required.
	if st, _, _ := f.e.oauthRaw("POST", "/oauth/register", url.Values{"client_name": {"x"}}, nil); st != 400 {
		t.Errorf("form body: %d", st)
	}
}

func TestOAuthRegistrationLimits(t *testing.T) {
	f := newOAuthEnv(t)
	// The instance cap.
	if err := f.e.st.UpdateInstanceSettings(context.Background(), store.SettingsUpdate{Limits: map[string]int{string(limits.OAuthClients): 2}}); err != nil {
		t.Fatal(err)
	}
	one := f.register(t, "one")
	f.e.clock.Add(time.Second)
	f.register(t, "two")
	// At the cap the oldest unused client makes room.
	f.e.clock.Add(time.Minute)
	three := f.register(t, "three")
	ctx := context.Background()
	if _, err := f.e.st.OAuthClientByID(ctx, one); err == nil {
		t.Fatal("the oldest unused client survived the cap")
	}
	if _, err := f.e.st.OAuthClientByID(ctx, three); err != nil {
		t.Fatal("the new client was not stored")
	}
	// Fails only when every client is in use.
	_, ch := pkce()
	f.code(t, f.a, three, ch, []string{f.hA})
	four := f.register(t, "four") // evicts "two", the only unused one left
	f.code(t, f.a, four, ch, []string{f.hA})
	if st, _, _ := f.e.oauthRaw("POST", "/oauth/register", nil, map[string]any{"redirect_uris": []string{oauthRedirect}}); st != 503 {
		t.Fatalf("all clients in use, over the cap: %d", st)
	}
	// The per-IP rate limit.
	e := f.e
	e.srv.rl = newRateLimiter(5, 10000, e.clock.Now)
	e.srv.rl.perMinute, e.srv.rl.burstOf = bucketRates, bucketBursts
	var last int
	for i := 0; i < limits.OAuthRegisterPerMinute+2; i++ {
		last, _, _ = e.oauthRaw("POST", "/oauth/register", nil, map[string]any{"redirect_uris": []string{"bad"}})
	}
	if last != 429 {
		t.Fatalf("registration not rate limited: %d", last)
	}
}

func TestOAuthFullFlow(t *testing.T) {
	f := newOAuthEnv(t)
	e := f.e
	chore := mkIn(t, f.a, f.hA, intervalBody("Bins", "2026-11-21"))
	cid := f.register(t, `<script>alert(1)</script> "Evil" & Co`)
	ver, ch := pkce()

	// Not signed in: the consent page sends the browser to sign in and back.
	anon := e.client()
	q := authzQuery(cid, ch, "scope", "chores:read chores:complete chores:write", "resource", oauthBase+"/mcp")
	st, h, _ := anon.browseGet("/oauth/authorize?" + q.Encode())
	if st != 303 || !strings.HasPrefix(h.Get("Location"), "/signin?next=%2Foauth%2Fauthorize%3F") {
		t.Fatalf("anon: %d %q", st, h.Get("Location"))
	}
	next, _ := url.QueryUnescape(strings.TrimPrefix(h.Get("Location"), "/signin?next="))
	if next != "/oauth/authorize?"+r2(q) {
		t.Fatalf("next %q", next)
	}

	// Signed in: the consent page shows the escaped name, the unverified label, the households and actions.
	st, h, body := f.a.browseGet("/oauth/authorize?" + q.Encode())
	if st != 200 || h.Get("Cache-Control") != "no-store" || !strings.Contains(h.Get("Content-Security-Policy"), "form-action 'self' https://app.example;") {
		t.Fatalf("consent: %d %v", st, h)
	}
	for _, want := range []string{"&lt;script&gt;alert(1)&lt;/script&gt;", "Unverified app.", "app.example", `name="hh" value="` + f.hA + `"`,
		`name="complete"`, `name="write"`, `name="decision" value="deny"`, `name="csrf"`, "(owner)"} {
		if !strings.Contains(body, want) {
			t.Errorf("consent page lacks %q", want)
		}
	}
	if strings.Contains(body, "<script") || strings.Contains(body, "<form method=\"post\" action=\"/oauth/authorize\"><script") {
		t.Error("client name not escaped")
	}
	if regexp.MustCompile(`name="write" value="1" checked`).MatchString(body) == false {
		t.Error("an explicitly requested scope should be preselected")
	}

	// Allow: a single-use code goes back to the client with state and issuer.
	code := f.code(t, f.a, cid, ch, []string{f.hA}, "scope", "chores:read chores:complete", "complete", "1")
	tk := f.exchange(t, cid, code, ver)

	// The access token works on /mcp with exactly the granted scopes and households.
	cs := f.connect(t, tk.access)
	if got, want := toolNames(t, cs), []string{"complete_chore", "get_chore", "list_chores", "list_households", "skip_chore", "snooze_chore", "undo_completion", "upcoming"}; !slices.Equal(got, want) {
		t.Fatalf("tools %v", got)
	}
	hs := mustOK(t, cs, "list_households", nil)["households"].([]any)
	if len(hs) != 1 || str(hs[0], "id") != f.hA {
		t.Fatalf("households %v", hs)
	}
	mustFail(t, cs, "create_chore", map[string]any{"name": "x"}, "permission")
	mustOK(t, cs, "complete_chore", map[string]any{"chore": chore})
	// Completion attribution: source oauth, token id = the grant id, named in the history.
	comps, _, _ := e.st.CompletionsPage(context.Background(), store.Actor{UserID: f.userID("a")}, f.hA, chore, 5, "")
	grants, _ := e.st.ListOAuthGrants(context.Background(), f.userID("a"))
	if len(comps) != 1 || comps[0].Source != store.SourceOAuth || len(grants) != 1 || comps[0].TokenID != grants[0].ID || comps[0].Via != grants[0].ClientName {
		t.Fatalf("attribution %+v %+v", comps, grants)
	}
	// The export carries the new source.
	resp := f.aStep.raw("GET", "/api/v1/households/"+f.hA+"/export", nil, "")
	exp, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(exp), `"source": "oauth"`) && !strings.Contains(string(exp), `"source":"oauth"`) {
		t.Fatalf("export lacks the oauth source: %d %s", resp.StatusCode, exp)
	}
	st, v := f.a.do("GET", "/api/v1/households/"+f.hA+"/chores/"+chore+"/completions", nil)
	if items := field(v, "items").([]any); st != 200 || len(items) != 1 || field(items[0], "source") != "oauth" || field(items[0], "via") != grants[0].ClientName {
		t.Fatalf("history %d %v", st, v)
	}

	// The same token is refused by the REST API (audience: /mcp only).
	for _, p := range []string{"/api/v1/token", "/api/v1/households", "/api/v1/households/" + f.hA + "/chores"} {
		if st, h, _ := e.bearerDo("GET", p, tk.access, nil); st != 401 || h.Get("WWW-Authenticate") == "" {
			t.Errorf("REST %s with an access token: %d", p, st)
		}
	}
	// ...and refresh tokens are no access tokens.
	if st, _, _ := e.rawMCP("POST", tk.refresh, `{}`); st != 401 {
		t.Errorf("refresh token on /mcp: %d", st)
	}

	// Refresh rotates: the new pair works, the old refresh token revokes the grant.
	st, m, _ := f.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {cid}, "refresh_token": {tk.refresh}})
	if st != 200 || m["refresh_token"] == tk.refresh || m["access_token"] == tk.access || m["scope"] != "chores:read chores:complete" {
		t.Fatalf("refresh: %d %v", st, m)
	}
	tk2 := tokens{m["access_token"].(string), m["refresh_token"].(string)}
	if st, _, _ := e.rawMCP("POST", tk2.access, `{"jsonrpc":"2.0","id":1,"method":"ping"}`); st != 200 {
		t.Fatalf("new access token: %d", st)
	}
	if st, m, _ := f.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {cid}, "refresh_token": {tk.refresh}}); st != 400 || m["error"] != "invalid_grant" {
		t.Fatalf("refresh reuse: %d %v", st, m)
	}
	if st, _, _ := e.rawMCP("POST", tk2.access, `{"jsonrpc":"2.0","id":1,"method":"ping"}`); st != 401 {
		t.Fatalf("access token after reuse: %d", st)
	}
	if st, m, _ := f.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {cid}, "refresh_token": {tk2.refresh}}); st != 400 || m["error"] != "invalid_grant" {
		t.Fatalf("refresh after reuse: %d %v", st, m)
	}
	audits, _ := e.st.ListAuditForUser(context.Background(), f.userID("a"), 50)
	var evs []string
	for _, a := range audits {
		evs = append(evs, a.Event)
	}
	for _, want := range []string{store.AuditOAuthGrant, store.AuditOAuthTokenReuse} {
		if !slices.Contains(evs, want) {
			t.Errorf("audit lacks %s: %v", want, evs)
		}
	}

	// Revocation endpoint (RFC 7009): a refresh or an access token ends the grant; always 200.
	cid2, t3 := f.connectUser(t, f.a, "second", []string{f.hA})
	if st, _, _ := e.rawMCP("POST", t3.access, `{"jsonrpc":"2.0","id":1,"method":"ping"}`); st != 200 {
		t.Fatalf("t3: %d", st)
	}
	for _, c := range []struct{ tok, client string }{{"gk_nonsense", cid2}, {t3.refresh, cid}, {t3.refresh, "nope"}} { // wrong client or unknown: no effect, still 200
		if st, _, _ := e.oauthRaw("POST", "/oauth/revoke", url.Values{"token": {c.tok}, "client_id": {c.client}}, nil); st != 200 {
			t.Errorf("revoke %v: %d", c, st)
		}
	}
	if st, _, _ := e.rawMCP("POST", t3.access, `{"jsonrpc":"2.0","id":1,"method":"ping"}`); st != 200 {
		t.Fatal("revocation by another client took effect")
	}
	if st, _, _ := e.oauthRaw("POST", "/oauth/revoke", url.Values{"token": {t3.access}, "client_id": {cid2}}, nil); st != 200 {
		t.Fatal("revoke")
	}
	if st, _, _ := e.rawMCP("POST", t3.access, `{}`); st != 401 {
		t.Fatalf("access token after revocation: %d", st)
	}
}

// r2 re-encodes like the handler does (RequestURI keeps the order of the original query).
func r2(q url.Values) string { return q.Encode() }

func TestOAuthAuthorizeValidation(t *testing.T) {
	f := newOAuthEnv(t)
	cid := f.register(t, "App")
	_, ch := pkce()
	get := func(q url.Values) (int, string) {
		st, h, _ := f.a.browseGet("/oauth/authorize?" + q.Encode())
		return st, h.Get("Location")
	}
	// A bad client or redirect URI is never answered with a redirect.
	for name, q := range map[string]url.Values{
		"unknown client":    authzQuery("nope", ch),
		"redirect mismatch": authzQuery(cid, ch, "redirect_uri", "https://evil.example/callback"),
		"redirect prefix":   authzQuery(cid, ch, "redirect_uri", oauthRedirect+"/../x"),
		"redirect query":    authzQuery(cid, ch, "redirect_uri", oauthRedirect+"?a=b"),
		"no redirect":       authzQuery(cid, ch, "redirect_uri", ""),
	} {
		if st, loc := get(q); st != 400 || loc != "" {
			t.Errorf("%s: %d %q", name, st, loc)
		}
	}
	// Other faults go back to the client as an error.
	for name, c := range map[string]struct {
		q    url.Values
		want string
	}{
		"no challenge":      {authzQuery(cid, "", "code_challenge", ""), "invalid_request"},
		"plain":             {authzQuery(cid, ch, "code_challenge_method", "plain"), "invalid_request"},
		"no method":         {authzQuery(cid, ch, "code_challenge_method", ""), "invalid_request"},
		"short challenge":   {authzQuery(cid, "abc"), "invalid_request"},
		"token response":    {authzQuery(cid, ch, "response_type", "token"), "unsupported_response_type"},
		"resource mismatch": {authzQuery(cid, ch, "resource", "https://other.example/mcp"), "invalid_target"},
		"unknown scope":     {authzQuery(cid, ch, "scope", "chores:read admin"), "invalid_scope"},
		"long state":        {authzQuery(cid, ch, "state", strings.Repeat("s", 2000)), "invalid_request"},
	} {
		st, loc := get(c.q)
		u, _ := url.Parse(loc)
		if st != 303 || u == nil || u.Host != "app.example" || u.Query().Get("error") != c.want || u.Query().Get("code") != "" {
			t.Errorf("%s: %d %q", name, st, loc)
		}
	}
	// A matching resource and subset of scopes is fine.
	if st, _, _ := f.a.browseGet("/oauth/authorize?" + authzQuery(cid, ch, "resource", oauthBase+"/mcp", "scope", "chores:read").Encode()); st != 200 {
		t.Errorf("valid request: %d", st)
	}
	// The posted decision is validated again (hidden fields are client controlled).
	for name, c := range map[string][]string{"redirect": {"redirect_uri", "https://evil.example/callback"}, "client": {"client_id", "nope"}, "method": {"code_challenge_method", "plain"}} {
		st, _, _ := f.consent(t, f.a, authzQuery(cid, ch), "allow", []string{f.hA}, c...)
		if st != 400 {
			t.Errorf("post with bad %s: %d", name, st)
		}
	}
	// Nothing chosen: the page comes back with a message, no code is issued.
	if st, loc, body := f.consent(t, f.a, authzQuery(cid, ch), "allow", nil); st != 200 || loc != nil || !strings.Contains(body, "Choose at least one household.") {
		t.Errorf("no household: %d %v", st, loc)
	}
	// Households the user is not in cannot be granted.
	if st, _, _ := f.consent(t, f.a, authzQuery(cid, ch), "allow", []string{f.hB}); st != 400 {
		t.Errorf("foreign household: %d", st)
	}
}

func TestOAuthDeny(t *testing.T) {
	f := newOAuthEnv(t)
	cid := f.register(t, "App")
	_, ch := pkce()
	st, loc, _ := f.consent(t, f.a, authzQuery(cid, ch), "deny", []string{f.hA})
	if st != 303 || loc.Query().Get("error") != "access_denied" || loc.Query().Get("code") != "" || loc.Query().Get("state") != "st-1" {
		t.Fatalf("deny: %d %v", st, loc)
	}
	if gs, _ := f.e.st.ListOAuthGrants(context.Background(), f.userID("a")); len(gs) != 0 {
		t.Fatal("a grant exists after deny")
	}
}

func TestOAuthConsentCSRF(t *testing.T) {
	f := newOAuthEnv(t)
	f.e.stepUp(f.a, "a")
	cid := f.register(t, "App")
	_, ch := pkce()
	post := func(mod func(url.Values), hdr ...string) int {
		form := authzQuery(cid, ch)
		form.Set("csrf", f.a.cookie(csrfCookie))
		form.Set("decision", "allow")
		form["hh"] = []string{f.hA}
		mod(form)
		st, _, _ := f.a.browser("POST", "/oauth/authorize", form, hdr...)
		return st
	}
	if st := post(func(v url.Values) {}); st != 303 {
		t.Fatalf("valid post: %d", st)
	}
	for name, c := range map[string]struct {
		mod func(url.Values)
		hdr []string
	}{
		"no token":       {func(v url.Values) { v.Del("csrf") }, nil},
		"wrong token":    {func(v url.Values) { v.Set("csrf", strings.Repeat("x", 43)) }, nil},
		"cross site":     {func(v url.Values) {}, []string{"Sec-Fetch-Site", "cross-site"}},
		"foreign origin": {func(v url.Values) {}, []string{"Origin", "https://evil.example"}},
	} {
		if st := post(c.mod, c.hdr...); st != 403 {
			t.Errorf("%s: %d", name, st)
		}
	}
	// A real browser's same-origin post is accepted.
	if st := post(func(v url.Values) {}, "Sec-Fetch-Site", "same-origin", "Origin", oauthBase); st != 303 {
		t.Errorf("same-origin post: %d", st)
	}
	// Without a session the post answers an error page, not a code.
	if st, _, _ := f.e.client().browser("POST", "/oauth/authorize", url.Values{"csrf": {"x"}}); st != 403 {
		t.Errorf("anonymous post: %d", st)
	}
	anon := f.e.client()
	form := authzQuery(cid, ch)
	form.Set("csrf", anon.cookie(csrfCookie))
	form.Set("decision", "allow")
	form["hh"] = []string{f.hA}
	if st, _, _ := anon.browser("POST", "/oauth/authorize", form); st != 401 {
		t.Errorf("post without a session: %d", st)
	}
}

func TestOAuthTokenEndpointFailures(t *testing.T) {
	f := newOAuthEnv(t)
	cid := f.register(t, "App")
	other := f.register(t, "Other")
	redeem := func(mod func(url.Values), hhs ...string) (int, map[string]any) {
		ver, ch := pkce()
		code := f.code(t, f.a, cid, ch, []string{f.hA})
		form := url.Values{"grant_type": {"authorization_code"}, "client_id": {cid}, "code": {code}, "redirect_uri": {oauthRedirect}, "code_verifier": {ver}}
		mod(form)
		st, m, _ := f.token(form)
		return st, m
	}
	for name, c := range map[string]struct {
		mod  func(url.Values)
		want string
	}{
		"missing verifier":                      {func(v url.Values) { v.Del("code_verifier") }, "invalid_grant"},
		"wrong verifier":                        {func(v url.Values) { v.Set("code_verifier", strings.Repeat("a", 50)) }, "invalid_grant"},
		"short verifier":                        {func(v url.Values) { v.Set("code_verifier", "short") }, "invalid_grant"},
		"plain verifier (the challenge itself)": {func(v url.Values) {}, ""}, // replaced below
		"other client":                          {func(v url.Values) { v.Set("client_id", other) }, "invalid_grant"},
		"other redirect":                        {func(v url.Values) { v.Set("redirect_uri", "https://app.example/other") }, "invalid_grant"},
		"no redirect":                           {func(v url.Values) { v.Del("redirect_uri") }, "invalid_grant"},
		"resource":                              {func(v url.Values) { v.Set("resource", "https://other.example/mcp") }, "invalid_target"},
		"unknown code":                          {func(v url.Values) { v.Set("code", "nope") }, "invalid_grant"},
		"unknown client":                        {func(v url.Values) { v.Set("client_id", "nope") }, "invalid_client"},
		"no client":                             {func(v url.Values) { v.Del("client_id") }, "invalid_request"},
		"bad grant type":                        {func(v url.Values) { v.Set("grant_type", "password") }, "unsupported_grant_type"},
	} {
		if c.want == "" {
			continue
		}
		if st, m := redeem(c.mod); st < 400 || m["error"] != c.want {
			t.Errorf("%s: %d %v", name, st, m)
		}
	}
	// plain: the verifier equal to the challenge does not pass S256.
	_, ch := pkce()
	code := f.code(t, f.a, cid, ch, []string{f.hA})
	if st, m, _ := f.token(url.Values{"grant_type": {"authorization_code"}, "client_id": {cid}, "code": {code}, "redirect_uri": {oauthRedirect}, "code_verifier": {ch}}); st != 400 || m["error"] != "invalid_grant" {
		t.Errorf("plain verifier: %d %v", st, m)
	}
	// Reuse of a code: the second redemption fails and revokes the first one's tokens.
	ver, ch := pkce()
	code = f.code(t, f.a, cid, ch, []string{f.hA})
	tk := f.exchange(t, cid, code, ver)
	if st, m, _ := f.token(url.Values{"grant_type": {"authorization_code"}, "client_id": {cid}, "code": {code}, "redirect_uri": {oauthRedirect}, "code_verifier": {ver}}); st != 400 || m["error"] != "invalid_grant" {
		t.Fatalf("code reuse: %d %v", st, m)
	}
	if st, _, _ := f.e.rawMCP("POST", tk.access, `{}`); st != 401 {
		t.Fatalf("access token after code reuse: %d", st)
	}
	// A code is good for 60 seconds.
	ver, ch = pkce()
	code = f.code(t, f.a, cid, ch, []string{f.hA})
	f.e.clock.Add(61 * time.Second)
	if st, m, _ := f.token(url.Values{"grant_type": {"authorization_code"}, "client_id": {cid}, "code": {code}, "redirect_uri": {oauthRedirect}, "code_verifier": {ver}}); st != 400 || m["error"] != "invalid_grant" {
		t.Fatalf("expired code: %d %v", st, m)
	}
	// GET is not a token endpoint; the form must be form encoded.
	if st, _, b := f.e.oauthRaw("GET", "/oauth/token", nil, nil); st == 200 && strings.Contains(string(b), "access_token") {
		t.Errorf("GET token: %d", st)
	}
}

func TestOAuthTokenLifetimes(t *testing.T) {
	f := newOAuthEnv(t)
	cid, tk := f.connectUser(t, f.a, "App", []string{f.hA})
	ping := `{"jsonrpc":"2.0","id":1,"method":"ping"}`
	f.e.clock.Add(59 * time.Minute)
	if st, _, _ := f.e.rawMCP("POST", tk.access, ping); st != 200 {
		t.Fatalf("access token at 59 minutes: %d", st)
	}
	f.e.clock.Add(2 * time.Minute)
	if st, _, _ := f.e.rawMCP("POST", tk.access, ping); st != 401 {
		t.Fatalf("access token at 61 minutes: %d", st)
	}
	// A refresh token works for 30 idle days, and every rotation restarts that.
	cur := tk.refresh
	for i := 0; i < 3; i++ {
		f.e.clock.Add(29 * 24 * time.Hour)
		st, m, _ := f.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {cid}, "refresh_token": {cur}})
		if i < 2 && st != 200 {
			t.Fatalf("rotation %d: %d %v", i, st, m)
		}
		if i == 2 { // 87 days since consent: still inside the 90-day grant
			if st != 200 {
				t.Fatalf("rotation 3: %d %v", st, m)
			}
		}
		cur = m["refresh_token"].(string)
	}
	// Past the absolute 90 days the grant is over, however fresh the token.
	f.e.clock.Add(5 * 24 * time.Hour)
	if st, m, _ := f.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {cid}, "refresh_token": {cur}}); st != 400 || m["error"] != "invalid_grant" {
		t.Fatalf("after 90 days: %d %v", st, m)
	}
	// Idle: a refresh token unused for 30 days is dead.
	f.a = f.e.signedIn("a") // the old session idled out with the clock
	cid, tk = f.connectUser(t, f.a, "Idle", []string{f.hA})
	f.e.clock.Add(31 * 24 * time.Hour)
	if st, m, _ := f.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {cid}, "refresh_token": {tk.refresh}}); st != 400 || m["error"] != "invalid_grant" {
		t.Fatalf("idle refresh: %d %v", st, m)
	}
}

func TestOAuthScopesAndHouseholdBinding(t *testing.T) {
	f := newOAuthEnv(t)
	chore := mkIn(t, f.a, f.hA, intervalBody("Bins", "2026-11-21"))
	mkIn(t, f.b, f.hB, intervalBody("Laundry", "2026-11-21"))

	// Read only: only what is ticked (read is always granted).
	_, tkRead := f.connectUser(t, f.a, "Reader", []string{f.hA}, "complete", "", "write", "")
	cs := f.connect(t, tkRead.access)
	if got := toolNames(t, cs); !slices.Equal(got, []string{"get_chore", "list_chores", "list_households", "upcoming"}) {
		t.Fatalf("read-only tools %v", got)
	}
	mustFail(t, cs, "complete_chore", map[string]any{"chore": chore}, "permission")
	// The app asked for read only: it cannot be granted more by a crafted post.
	cid := f.register(t, "ReadOnlyApp")
	ver, ch := pkce()
	q := authzQuery(cid, ch, "scope", "chores:read")
	st, loc, _ := f.consent(t, f.a, q, "allow", []string{f.hA}, "complete", "1", "write", "1")
	if st != 303 {
		t.Fatal(st)
	}
	tk := f.exchange(t, cid, loc.Query().Get("code"), ver)
	if got := toolNames(t, f.connect(t, tk.access)); !slices.Equal(got, []string{"get_chore", "list_chores", "list_households", "upcoming"}) {
		t.Fatalf("scope escalation: %v", got)
	}

	// Another user's grant never reaches this household: b's token sees B only.
	_, tkB := f.connectUser(t, f.b, "B app", []string{f.hB}, "complete", "1")
	cb := f.connect(t, tkB.access)
	hs := mustOK(t, cb, "list_households", nil)["households"].([]any)
	if len(hs) != 1 || str(hs[0], "id") != f.hB {
		t.Fatalf("b households %v", hs)
	}
	mustFail(t, cb, "get_chore", map[string]any{"household": f.hA, "chore": chore}, "no household")
	mustFail(t, cb, "complete_chore", map[string]any{"household": f.hA, "chore": chore}, "no household")

	// A member's grant follows the live role: demoted to guest, it can no longer complete.
	_, tkM := f.connectUser(t, f.m, "M app", []string{f.hA}, "complete", "1")
	cm := f.connect(t, tkM.access)
	if err := f.e.st.ChangeRole(context.Background(), store.Actor{UserID: f.userID("a")}, f.hA, f.mID, store.RoleGuest); err != nil {
		t.Fatal(err)
	}
	mustFail(t, cm, "complete_chore", map[string]any{"chore": chore}, "guest")
	// Leaving the household drops the binding (and rejoining does not revive it).
	if err := f.e.st.RemoveMember(context.Background(), store.Actor{UserID: f.mID}, f.hA, f.mID); err != nil {
		t.Fatal(err)
	}
	mustFail(t, f.connect(t, tkM.access), "list_chores", nil, "no household")
	if gs, _ := f.e.st.ListOAuthGrants(context.Background(), f.mID); len(gs) != 1 || len(gs[0].Households) != 0 {
		t.Fatalf("grants after leaving: %+v", gs)
	}

	// A guest can grant (read), and what it gets is read only whatever it ticks.
	_, tkG := f.connectUser(t, f.g, "G app", []string{f.hA}, "complete", "1")
	mustFail(t, f.connect(t, tkG.access), "complete_chore", map[string]any{"chore": chore}, "guest")
}

func TestOAuthConnectedAppsAndDisconnect(t *testing.T) {
	f := newOAuthEnv(t)
	cid := f.register(t, "ChatGPT")
	_, ch := pkce()
	f.code(t, f.a, cid, ch, []string{f.hA}) // consent given, code never redeemed: not an app yet
	if _, v := f.a.do("GET", "/api/v1/account/oauth-grants", nil); len(list(t, v)) != 0 {
		t.Fatalf("unredeemed grant listed: %v", v)
	}
	_, tk := f.connectUser(t, f.a, "ChatGPT", []string{f.hA}, "complete", "1")
	st, v := f.a.do("GET", "/api/v1/account/oauth-grants", nil)
	l := list(t, v)
	if st != 200 || len(l) != 1 || str(l[0], "name") != "ChatGPT" || len(l[0].(map[string]any)["households"].([]any)) != 1 ||
		len(l[0].(map[string]any)["scopes"].([]any)) != 2 || str(l[0], "createdAt") == "" {
		t.Fatalf("list: %d %v", st, v)
	}
	if strings.Contains(strings.ToLower(jsonOf(v)), "gko_") || strings.Contains(jsonOf(v), "hash") {
		t.Fatal("secrets in the list")
	}
	// Another user cannot see or disconnect it.
	if _, v := f.b.do("GET", "/api/v1/account/oauth-grants", nil); len(list(t, v)) != 0 {
		t.Fatalf("b sees a's grants: %v", v)
	}
	id := str(l[0], "id")
	if st, v := f.b.do("DELETE", "/api/v1/account/oauth-grants/"+id, nil); st != 404 {
		t.Fatalf("b disconnects a's grant: %d %v", st, v)
	}
	// A bearer token cannot manage grants.
	if st, _, _ := f.e.bearerDo("GET", "/api/v1/account/oauth-grants", tk.access, nil); st != 401 {
		t.Fatalf("grants via access token: %d", st)
	}
	if st, v := f.a.do("DELETE", "/api/v1/account/oauth-grants/"+id, nil); st != 204 {
		t.Fatalf("disconnect: %d %v", st, v)
	}
	if st, _, _ := f.e.rawMCP("POST", tk.access, `{}`); st != 401 {
		t.Fatalf("access token after disconnect: %d", st)
	}
	st, _, _ = f.e.oauthRaw("POST", "/oauth/token", url.Values{"grant_type": {"refresh_token"}, "client_id": {cid}, "refresh_token": {tk.refresh}}, nil)
	if st != 400 {
		t.Fatalf("refresh after disconnect: %d", st)
	}
	if st, _ := f.a.do("DELETE", "/api/v1/account/oauth-grants/"+id, nil); st != 404 {
		t.Fatalf("second disconnect: %d", st)
	}
	// Grants stay dormant while OAuth is off.
	_, tk = f.connectUser(t, f.a, "Again", []string{f.hA})
	setOAuth(t, f.e, false)
	if st, _, _ := f.e.rawMCP("POST", tk.access, `{}`); st != 401 {
		t.Fatalf("access token while off: %d", st)
	}
	setOAuth(t, f.e, true)
	if st, _, _ := f.e.rawMCP("POST", tk.access, `{"jsonrpc":"2.0","id":1,"method":"ping"}`); st != 200 {
		t.Fatalf("access token after re-enabling: %d", st)
	}
}

func jsonOf(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestOAuthPasswordChangeRevokesGrants(t *testing.T) {
	f := newOAuthEnv(t)
	_, tk := f.connectUser(t, f.a, "App", []string{f.hA})
	if st, _, _ := f.e.rawMCP("POST", tk.access, `{"jsonrpc":"2.0","id":1,"method":"ping"}`); st != 200 {
		t.Fatal(st)
	}
	if err := f.e.st.SetPassword(context.Background(), f.userID("a"), "fake:new", ""); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := f.e.rawMCP("POST", tk.access, `{}`); st != 401 {
		t.Fatalf("access token after a password change: %d", st)
	}
	if gs, _ := f.e.st.ListOAuthGrants(context.Background(), f.userID("a")); len(gs) != 0 {
		t.Fatalf("grants after a password change: %v", gs)
	}
}

func TestOAuthPerUserGrantLimit(t *testing.T) {
	f := newOAuthEnv(t)
	if err := f.e.st.UpdateInstanceSettings(context.Background(), store.SettingsUpdate{Limits: map[string]int{string(limits.OAuthGrantsPerUser): 2}}); err != nil {
		t.Fatal(err)
	}
	f.connectUser(t, f.a, "1", []string{f.hA})
	f.connectUser(t, f.a, "2", []string{f.hA})
	cid := f.register(t, "3")
	_, ch := pkce()
	if st, _, body := f.consent(t, f.a, authzQuery(cid, ch), "allow", []string{f.hA}); st != 409 || !strings.Contains(body, "Too many connected apps.") {
		t.Fatalf("over the limit: %d", st)
	}
}

func TestOAuthPurgesUnusedClients(t *testing.T) {
	f := newOAuthEnv(t)
	ctx := context.Background()
	unused := f.register(t, "unused")
	visited := f.register(t, "visited")
	granted := f.register(t, "granted")
	ver, ch := pkce()
	f.exchange(t, granted, f.code(t, f.a, granted, ch, []string{f.hA}), ver)
	// Only looking at the consent page does not keep a client.
	if st, _, _ := f.a.browseGet("/oauth/authorize?" + authzQuery(visited, ch).Encode()); st != 200 {
		t.Fatal(st)
	}
	// An unredeemed consent: its grant is purged after an hour, the client stays (approved).
	pending := f.register(t, "pending")
	f.code(t, f.a, pending, ch, []string{f.hA})
	has := func(id string) bool { _, err := f.e.st.OAuthClientByID(ctx, id); return err == nil }
	purge := func() {
		t.Helper()
		if _, err := f.e.st.PurgeExpired(ctx, store.RetentionPolicy{}, f.e.clock.Now()); err != nil {
			t.Fatal(err)
		}
	}

	f.e.clock.Add(59 * time.Minute)
	purge()
	if !has(unused) || !has(visited) {
		t.Fatal("an unused client was purged before an hour")
	}
	f.e.clock.Add(2 * time.Minute)
	purge()
	if has(unused) || has(visited) {
		t.Fatal("a client never approved within an hour survived")
	}
	if !has(granted) || !has(pending) {
		t.Fatal("an approved client was purged")
	}
	// Idle for 90 days without any grant: purged too; one with a (revoked, then purged) grant too.
	f.e.clock.Add(91 * 24 * time.Hour)
	purge()
	if has(pending) {
		t.Fatal("an idle client survived 90 days")
	}
	if has(granted) { // the grant expired after 90 days and was purged with its tokens
		t.Fatal("a client with only an expired grant survived")
	}
	if gs, _ := f.e.st.ListOAuthGrants(ctx, f.userID("a")); len(gs) != 0 {
		t.Fatal("expired grants listed")
	}
}
