package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // RFC 6238 test helper
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/auth"
	"github.com/NexusBetweenVoids/gunkly/internal/db/dbtest"
	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }

func (c *fakeClock) Add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

type env struct {
	t             *testing.T
	srv           *Server
	st            *store.Store
	ts            *httptest.Server
	clock         *fakeClock
	dummyVerifies atomic.Int32
}

var staticFS = fstest.MapFS{
	"index.html":           {Data: []byte("<!doctype html><title>Gunkly</title>")},
	"assets/app-123.js":    {Data: []byte("console.log(1)")},
	"manifest.webmanifest": {Data: []byte("{}")},
	"sw.js":                {Data: []byte("//")},
}

func newEnv(t *testing.T, mutate ...func(*Options)) *env {
	t.Helper()
	clock := &fakeClock{t: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)}
	st := store.New(dbtest.Open(t)).WithClock(clock.Now)
	kr, err := auth.NewKeyring(1, map[uint32]string{1: strings.Repeat("k", auth.MinSecretLen)})
	if err != nil {
		t.Fatal(err)
	}
	o := Options{Store: st, Limits: limits.New(st), Keyring: kr, Static: staticFS, Now: clock.Now,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for _, m := range mutate {
		m(&o)
	}
	e := &env{t: t, st: st, clock: clock}
	e.srv = New(o)
	// Fast fake hashing; the real Argon2id is covered in internal/auth.
	e.srv.hashPassword = func(pw string) string { return "fake:" + pw }
	e.srv.verifyPassword = func(h, pw string) (bool, error) {
		if h == e.srv.dummy() {
			e.dummyVerifies.Add(1)
		}
		return h == "fake:"+pw, nil
	}
	// Generous limits so functional tests are not throttled; TestRateLimit
	// restores the real one.
	e.srv.rl = newRateLimiter(10000, 10000, clock.Now)
	e.ts = httptest.NewServer(e.srv.Handler())
	t.Cleanup(e.ts.Close)
	return e
}

func (e *env) user(name string, isAdmin bool) store.User {
	e.t.Helper()
	u, err := e.st.CreateUser(context.Background(), store.NewUser{
		Username: name, Email: name + "@example.com", DisplayName: name, PasswordHash: "fake:password-" + name, IsAdmin: isAdmin,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

type client struct {
	e *env
	c *http.Client
	h map[string]string // extra headers
}

// client returns an HTTP client with a cookie jar that has fetched a CSRF
// cookie; do() echoes it like the SPA does.
func (e *env) client() *client {
	jar, _ := cookiejar.New(nil)
	c := &client{e: e, c: &http.Client{Jar: jar}, h: map[string]string{}}
	if st, _ := c.do("GET", "/api/v1/session", nil); st != 200 {
		e.t.Fatalf("GET session: %d", st)
	}
	return c
}

func (c *client) cookie(name string) string {
	u, _ := url.Parse(c.e.ts.URL)
	for _, k := range c.c.Jar.Cookies(u) {
		if k.Name == name {
			return k.Value
		}
	}
	return ""
}

func (c *client) raw(method, path string, body any, csrf string) *http.Response {
	c.e.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.e.ts.URL+path, r)
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	for k, v := range c.h {
		req.Header.Set(k, v)
	}
	resp, err := c.c.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	return resp
}

// do sends a request with the CSRF header and returns status and the
// decoded JSON body (nil if none).
func (c *client) do(method, path string, body any) (int, any) {
	c.e.t.Helper()
	resp := c.raw(method, path, body, c.cookie(csrfCookie))
	defer resp.Body.Close()
	var v any
	_ = json.NewDecoder(resp.Body).Decode(&v)
	return resp.StatusCode, v
}

func (c *client) login(name string, extra map[string]string) (int, any) {
	body := map[string]string{"login": name, "password": "password-" + name}
	for k, v := range extra {
		body[k] = v
	}
	return c.do("POST", "/api/v1/session", body)
}

func (e *env) signedIn(name string) *client {
	c := e.client()
	if st, v := c.login(name, nil); st != 200 {
		e.t.Fatalf("login %s: %d %v", name, st, v)
	}
	return c
}

func code(v any) string {
	m, _ := v.(map[string]any)
	er, _ := m["error"].(map[string]any)
	s, _ := er["code"].(string)
	return s
}

func field(v any, k string) any { m, _ := v.(map[string]any); return m[k] }

func expect(t *testing.T, gotStatus int, body any, wantStatus int, wantCode string) {
	t.Helper()
	if gotStatus != wantStatus || (wantCode != "" && code(body) != wantCode) {
		t.Fatalf("got %d %v, want %d %s", gotStatus, body, wantStatus, wantCode)
	}
}

func TestRoutePolicies(t *testing.T) {
	e := newEnv(t)
	n := 0
	for _, r := range e.srv.routes {
		if err := r.policy.validate(r.pattern); err != nil {
			t.Error(err)
		}
		if strings.Contains(r.pattern, "/api/v1/") {
			n++
			if r.policy.auth == authUnset {
				t.Errorf("%s has no policy", r.pattern)
			}
		}
	}
	if n < 30 {
		t.Fatalf("only %d api routes registered", n)
	}
	for _, bad := range []struct {
		pattern string
		p       policy
	}{
		{"GET /api/v1/x", policy{}},
		{"GET /api/v1/households/{hh}/x", session},
		{"GET /api/v1/households/{hh}/x", policy{auth: authHousehold}},
		{"GET /api/v1/x", household(store.RoleGuest)},
		{"GET /api/v1/x", public.withStepUp()},
		{"POST /x/{token}", public.withTokenAuth()},                // token routes must be rate limited
		{"POST /x/{token}", session.withRate("x").withTokenAuth()}, // and public
	} {
		if bad.p.validate(bad.pattern) == nil {
			t.Errorf("%s %+v accepted", bad.pattern, bad.p)
		}
	}
}

// TestNoDatabaseImports backs up depguard: handlers never touch SQL.
func TestNoDatabaseImports(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		af, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range af.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if p == "database/sql" || strings.Contains(p, "pgx") || strings.Contains(p, "modernc.org/sqlite") || strings.HasSuffix(p, "/internal/db") {
				t.Errorf("%s imports %s", f, p)
			}
		}
	}
}

func TestSecurityHeadersAndStatic(t *testing.T) {
	e := newEnv(t)
	cases := []struct {
		path, cache, ctype string
		status             int
	}{
		{"/healthz", "no-store", "application/json", 200},
		{"/api/v1/session", "no-store", "application/json", 200},
		{"/api/v1/nope", "no-store", "application/json", 404},
		{"/", "no-cache", "text/html", 200},
		{"/chores/123", "no-cache", "text/html", 200}, // SPA fallback
		{"/assets/app-123.js", "public, max-age=31536000, immutable", "text/javascript", 200},
		{"/assets/missing.js", "no-store", "application/json", 404},
		{"/manifest.webmanifest", "no-cache", "application/manifest+json", 200},
		{"/sw.js", "no-cache", "text/javascript", 200},
	}
	for _, c := range cases {
		resp, err := http.Get(e.ts.URL + c.path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		h := resp.Header
		if resp.StatusCode != c.status || h.Get("Cache-Control") != c.cache || !strings.HasPrefix(h.Get("Content-Type"), c.ctype) {
			t.Errorf("%s: %d cache=%q type=%q", c.path, resp.StatusCode, h.Get("Cache-Control"), h.Get("Content-Type"))
		}
		if h.Get("Content-Security-Policy") != csp || h.Get("X-Content-Type-Options") != "nosniff" ||
			h.Get("Referrer-Policy") != "no-referrer" || h.Get("X-Frame-Options") != "DENY" ||
			h.Get("Cross-Origin-Opener-Policy") != "same-origin" || h.Get("Permissions-Policy") == "" {
			t.Errorf("%s: missing security headers: %v", c.path, h)
		}
		if h.Get("Strict-Transport-Security") != "" {
			t.Errorf("%s: HSTS over http", c.path)
		}
	}
	hs := newEnv(t, func(o *Options) { o.BaseURL = "https://gunkly.example" })
	rec := httptest.NewRecorder()
	hs.srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Header().Get("Strict-Transport-Security") == "" {
		t.Error("no HSTS with https BASE_URL")
	}
}

func TestHealth(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	st, _ := c.do("GET", "/healthz", nil)
	expect(t, st, nil, 200, "")
	st, _ = c.do("GET", "/readyz", nil)
	expect(t, st, nil, 200, "")
	bad := newEnv(t, func(o *Options) { o.Ready = func(context.Context) error { return errors.New("down") } })
	st, v := bad.client().do("GET", "/readyz", nil)
	expect(t, st, v, 503, "not_ready")
}

func TestClientIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("::1/128")}
	cases := []struct{ peer, xff, want string }{
		{"203.0.113.9:1234", "1.2.3.4", "203.0.113.9"},             // untrusted peer: XFF ignored
		{"10.0.0.2:1234", "", "10.0.0.2"},                          // trusted, no header
		{"10.0.0.2:1234", "1.2.3.4", "1.2.3.4"},                    // one proxy
		{"10.0.0.2:1234", "6.6.6.6, 1.2.3.4, 10.0.0.7", "1.2.3.4"}, // spoofed left part skipped
		{"10.0.0.2:1234", "garbage, 10.0.0.7", "10.0.0.7"},         // malformed stops walk
		{"[::1]:1234", "2001:db8::1", "2001:db8::1"},               // IPv6
		{"[::ffff:203.0.113.9]:1234", "1.2.3.4", "203.0.113.9"},    // mapped peer, untrusted
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.peer
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := clientIP(r, trusted); got.String() != c.want {
			t.Errorf("%s / %q: got %s, want %s", c.peer, c.xff, got, c.want)
		}
	}
}

func TestCSRF(t *testing.T) {
	e := newEnv(t)
	e.user("ann", false)
	c := e.signedIn("ann")
	body := map[string]string{"name": "Home", "timezone": "Europe/Amsterdam"}
	for _, tc := range []struct {
		name, csrf string
		headers    map[string]string
	}{
		{"no header", "", nil},
		{"wrong header", strings.Repeat("x", 43), nil},
		{"cross-site origin", c.cookie(csrfCookie), map[string]string{"Origin": "https://evil.example"}},
		{"cross-site fetch", c.cookie(csrfCookie), map[string]string{"Sec-Fetch-Site": "cross-site"}},
	} {
		c.h = tc.headers
		resp := c.raw("POST", "/api/v1/households", body, tc.csrf)
		resp.Body.Close()
		if resp.StatusCode != 403 {
			t.Errorf("%s: status %d", tc.name, resp.StatusCode)
		}
	}
	c.h = map[string]string{"Origin": e.ts.URL, "Sec-Fetch-Site": "same-origin"}
	st, v := c.do("POST", "/api/v1/households", body)
	expect(t, st, v, 201, "")
	// Login itself is CSRF-protected too.
	anon := e.client()
	resp := anon.raw("POST", "/api/v1/session", map[string]string{"login": "ann", "password": "password-ann"}, "")
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("login without csrf: %d", resp.StatusCode)
	}
}

func TestLoginLogout(t *testing.T) {
	e := newEnv(t)
	e.user("ann", false)
	c := e.client()
	st, v := c.login("ann", map[string]string{"password": "wrong-password"})
	expect(t, st, v, 401, "invalid_credentials")
	before := e.dummyVerifies.Load()
	st, v = c.do("POST", "/api/v1/session", map[string]string{"login": "nobody", "password": "whatever-123"})
	expect(t, st, v, 401, "invalid_credentials")
	if e.dummyVerifies.Load() != before+1 {
		t.Fatal("unknown user did not run the dummy password verification")
	}
	st, v = c.do("POST", "/api/v1/session", map[string]any{"login": "ann", "password": "x", "extra": 1})
	expect(t, st, v, 400, "invalid_json")
	st, v = c.login("ann", nil)
	expect(t, st, v, 200, "")
	if field(v, "csrf") != c.cookie(csrfCookie) || c.cookie(sessionCookie) == "" {
		t.Fatal("cookies not set")
	}
	st, v = c.do("GET", "/api/v1/me", nil)
	if st != 200 || field(v, "username") != "ann" {
		t.Fatalf("me: %d %v", st, v)
	}
	st, v = c.do("PATCH", "/api/v1/me", map[string]any{"displayName": "Ann B", "weekStart": 1, "locale": "nl-NL"})
	if st != 200 || field(v, "displayName") != "Ann B" || field(v, "weekStart") != 1.0 {
		t.Fatalf("patch me: %d %v", st, v)
	}
	st, v = c.do("PATCH", "/api/v1/me", map[string]any{"weekStart": nil})
	if st != 200 || field(v, "weekStart") != nil || field(v, "displayName") != "Ann B" {
		t.Fatalf("clear weekStart: %d %v", st, v)
	}
	// A second device; list, then sign out everywhere else.
	other := e.signedIn("ann")
	st, v = c.do("GET", "/api/v1/sessions", nil)
	if list, _ := v.([]any); st != 200 || len(list) != 2 {
		t.Fatalf("sessions: %d %v", st, v)
	}
	st, v = c.do("DELETE", "/api/v1/sessions", nil)
	if st != 200 || field(v, "revoked") != 1.0 {
		t.Fatalf("revoke others: %d %v", st, v)
	}
	st, v = other.do("GET", "/api/v1/me", nil)
	expect(t, st, v, 401, "unauthenticated")
	st, _ = c.do("DELETE", "/api/v1/session", nil)
	expect(t, st, nil, 204, "")
	st, v = c.do("GET", "/api/v1/me", nil)
	expect(t, st, v, 401, "unauthenticated")
	st, v = c.do("GET", "/api/v1/account/audit", nil)
	expect(t, st, v, 401, "unauthenticated")
	// Audit trail of the second device's session (still valid? no: revoked) -> sign in again.
	c = e.signedIn("ann")
	_, v = c.do("GET", "/api/v1/account/audit", nil)
	events := map[string]bool{}
	for _, ev := range v.([]any) {
		events[field(ev, "event").(string)] = true
	}
	for _, want := range []string{store.AuditLoginFailure, store.AuditLoginSuccess, store.AuditLogout, store.AuditSessionRevoke} {
		if !events[want] {
			t.Errorf("audit lacks %s: %v", want, events)
		}
	}
}

func TestSessionExpiry(t *testing.T) {
	e := newEnv(t)
	e.user("ann", false)
	c := e.signedIn("ann")
	e.clock.Add(29 * 24 * time.Hour)
	st, _ := c.do("GET", "/api/v1/me", nil) // activity slides the idle window
	expect(t, st, nil, 200, "")
	e.clock.Add(30*24*time.Hour + time.Minute)
	st, v := c.do("GET", "/api/v1/me", nil)
	expect(t, st, v, 401, "unauthenticated")

	// Absolute: active every 20 days, still expires after 90.
	c = e.signedIn("ann")
	for i := 0; i < 4; i++ {
		e.clock.Add(20 * 24 * time.Hour)
		st, _ = c.do("GET", "/api/v1/me", nil)
		expect(t, st, nil, 200, "")
	}
	e.clock.Add(11 * 24 * time.Hour)
	st, v = c.do("GET", "/api/v1/me", nil)
	expect(t, st, v, 401, "unauthenticated")
}

func TestSessionLifetimesConfigurable(t *testing.T) {
	e := newEnv(t)
	e.user("ann", false)
	e.user("root", true)
	root := e.signedIn("root")
	st, v := root.do("GET", "/api/v1/admin/settings", nil)
	if st != 200 || field(v, "sessionIdleDays") != 30.0 || field(v, "sessionMaxDays") != 90.0 ||
		field(v, "auditRetentionDays") != 90.0 || field(v, "deliveryRetentionDays") != 30.0 {
		t.Fatalf("defaults: %d %v", st, v)
	}
	for _, bad := range []map[string]any{
		{"sessionIdleDays": 0}, {"sessionIdleDays": 366}, {"sessionMaxDays": 731},
		{"sessionIdleDays": 100}, // exceeds the stored max (90)
		{"sessionMaxDays": 20},   // below the stored idle (30)
		{"auditRetentionDays": 0}, {"deliveryRetentionDays": 3651},
	} {
		st, v = root.do("PATCH", "/api/v1/admin/settings", bad)
		expect(t, st, v, 400, "invalid")
	}
	st, v = root.do("PATCH", "/api/v1/admin/settings", map[string]any{"sessionIdleDays": 2, "sessionMaxDays": 5,
		"auditRetentionDays": 7, "deliveryRetentionDays": 3})
	if st != 200 || field(v, "sessionIdleDays") != 2.0 || field(v, "sessionMaxDays") != 5.0 ||
		field(v, "auditRetentionDays") != 7.0 || field(v, "deliveryRetentionDays") != 3.0 {
		t.Fatalf("patch: %d %v", st, v)
	}

	// Idle: a session unused for more than 2 days is gone.
	c := e.signedIn("ann")
	e.clock.Add(47 * time.Hour)
	st, _ = c.do("GET", "/api/v1/me", nil)
	expect(t, st, nil, 200, "")
	e.clock.Add(49 * time.Hour)
	st, v = c.do("GET", "/api/v1/me", nil)
	expect(t, st, v, 401, "unauthenticated")

	// Absolute: active daily, still expires after 5 days.
	c = e.signedIn("ann")
	for range 4 {
		e.clock.Add(24 * time.Hour)
		st, _ = c.do("GET", "/api/v1/me", nil)
		expect(t, st, nil, 200, "")
	}
	e.clock.Add(25 * time.Hour)
	st, v = c.do("GET", "/api/v1/me", nil)
	expect(t, st, v, 401, "unauthenticated")

	// Raising the limits applies to existing sessions.
	root = e.signedIn("root")
	c = e.signedIn("ann")
	st, v = root.do("PATCH", "/api/v1/admin/settings", map[string]any{"sessionMaxDays": 60, "sessionIdleDays": 40})
	expect(t, st, v, 200, "")
	e.clock.Add(39 * 24 * time.Hour)
	st, v = c.do("GET", "/api/v1/me", nil)
	expect(t, st, v, 200, "")
}

func totpCode(secret string, at time.Time) string {
	key, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(at.Unix()/30)) //nolint:gosec // test
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	h := mac.Sum(nil)
	off := h[len(h)-1] & 0x0f
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(h[off:])&0x7fffffff)%1_000_000)
}

func TestStepUpAndTOTP(t *testing.T) {
	e := newEnv(t)
	e.user("ann", false)
	c := e.signedIn("ann")
	e.clock.Add(16 * time.Minute)
	st, v := c.do("POST", "/api/v1/account/totp/setup", nil)
	expect(t, st, v, 403, "step_up_required")
	st, v = c.do("POST", "/api/v1/session/step-up", map[string]string{"password": "nope-nope-nope"})
	expect(t, st, v, 401, "invalid_credentials")
	st, _ = c.do("POST", "/api/v1/session/step-up", map[string]string{"password": "password-ann"})
	expect(t, st, nil, 204, "")
	st, v = c.do("POST", "/api/v1/account/totp/setup", nil)
	expect(t, st, v, 200, "")
	secret := field(v, "secret").(string)
	if !strings.HasPrefix(field(v, "uri").(string), "otpauth://totp/") {
		t.Fatal(v)
	}
	st, v = c.do("POST", "/api/v1/account/totp/confirm", map[string]string{"code": "000000"})
	expect(t, st, v, 401, "invalid_code")
	confirmCode := totpCode(secret, e.clock.Now())
	st, v = c.do("POST", "/api/v1/account/totp/confirm", map[string]string{"code": confirmCode})
	expect(t, st, v, 200, "")
	recovery := field(v, "recoveryCodes").([]any)
	if len(recovery) != auth.RecoveryCodeCount {
		t.Fatal(v)
	}

	d := e.client()
	st, v = d.login("ann", nil)
	if st != 200 || field(v, "twoFactorRequired") != true || d.cookie(sessionCookie) != "" {
		t.Fatalf("2fa required: %d %v", st, v)
	}
	st, v = d.login("ann", map[string]string{"totp": confirmCode}) // replay of the confirm code
	expect(t, st, v, 401, "invalid_code")
	e.clock.Add(30 * time.Second)
	next := totpCode(secret, e.clock.Now())
	st, v = d.login("ann", map[string]string{"totp": next})
	expect(t, st, v, 200, "")
	st, v = e.client().login("ann", map[string]string{"totp": next}) // replay
	expect(t, st, v, 401, "invalid_code")
	rc := recovery[0].(string)
	st, v = e.client().login("ann", map[string]string{"recoveryCode": rc})
	expect(t, st, v, 200, "")
	st, v = e.client().login("ann", map[string]string{"recoveryCode": rc}) // single use
	expect(t, st, v, 401, "invalid_code")

	// Step-up now needs the second factor too.
	e.clock.Add(16 * time.Minute)
	st, v = c.do("POST", "/api/v1/session/step-up", map[string]string{"password": "password-ann"})
	expect(t, st, v, 401, "two_factor_required")
	st, _ = c.do("POST", "/api/v1/session/step-up", map[string]string{"password": "password-ann", "totp": totpCode(secret, e.clock.Now())})
	expect(t, st, nil, 204, "")
	st, _ = c.do("DELETE", "/api/v1/account/totp", nil)
	expect(t, st, nil, 204, "")
	st, v = e.client().login("ann", nil)
	if st != 200 || field(v, "authenticated") != true {
		t.Fatalf("login after disable: %d %v", st, v)
	}
}

func TestPasswordChange(t *testing.T) {
	e := newEnv(t)
	e.user("ann", false)
	c := e.signedIn("ann")
	other := e.signedIn("ann")
	st, v := c.do("POST", "/api/v1/account/password", map[string]string{"newPassword": "short"})
	expect(t, st, v, 400, "password_too_short")
	st, _ = c.do("POST", "/api/v1/account/password", map[string]string{"newPassword": "a-new-long-password"})
	expect(t, st, nil, 204, "")
	st, _ = c.do("GET", "/api/v1/me", nil)
	expect(t, st, nil, 200, "")
	st, _ = other.do("GET", "/api/v1/me", nil)
	expect(t, st, nil, 401, "")
	st, _ = e.client().do("POST", "/api/v1/session", map[string]string{"login": "ann", "password": "a-new-long-password"})
	expect(t, st, nil, 200, "")
}

func TestSetupOneTime(t *testing.T) {
	e := newEnv(t)
	if err := e.srv.InitSetup(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Recover the logged code by replacing the hash with a known one.
	codes, hashes := auth.GenerateRecoveryCodes(1)
	e.srv.setupHash = hashes[0]
	c := e.client()
	st, v := c.do("GET", "/api/v1/setup", nil)
	if st != 200 || field(v, "needed") != true {
		t.Fatal(v)
	}
	acct := map[string]string{"username": "root", "password": "password-root", "displayName": "Root"}
	acct["code"] = "aaaa-bbbb-cccc"
	st, v = c.do("POST", "/api/v1/setup", acct)
	expect(t, st, v, 403, "invalid_setup_code")
	acct["code"] = codes[0]
	st, v = c.do("POST", "/api/v1/setup", acct)
	if st != 201 || field(field(v, "user"), "isAdmin") != true {
		t.Fatalf("setup: %d %v", st, v)
	}
	acct["username"] = "root2"
	st, v = e.client().do("POST", "/api/v1/setup", acct)
	expect(t, st, v, 404, "not_found")
	_, v = c.do("GET", "/api/v1/setup", nil)
	if field(v, "needed") != false {
		t.Fatal(v)
	}
	// With users present, InitSetup does nothing.
	if err := e.srv.InitSetup(context.Background()); err != nil || e.srv.setupPending() {
		t.Fatal("setup re-armed")
	}
}

func TestRateLimit(t *testing.T) {
	e := newEnv(t)
	e.srv.rl = newRateLimiter(limits.Defaults[limits.LoginAttemptsPerMinute], 100, e.clock.Now)
	c := e.client()
	var last *http.Response
	for i := 0; i < 7; i++ {
		last = c.raw("POST", "/api/v1/session", map[string]string{"login": "x", "password": "password-x"}, c.cookie(csrfCookie))
		last.Body.Close()
	}
	if last.StatusCode != 429 || last.Header.Get("Retry-After") == "" {
		t.Fatalf("status %d retry %q", last.StatusCode, last.Header.Get("Retry-After"))
	}
	e.clock.Add(maxBackoff + time.Minute)
	st, _ := c.do("POST", "/api/v1/session", map[string]string{"login": "x", "password": "password-x"})
	expect(t, st, nil, 401, "") // backoff ends; never a hard lock
}

func TestRateLimiterBounded(t *testing.T) {
	clock := &fakeClock{t: time.Unix(0, 0)}
	l := newRateLimiter(5, 100, clock.Now)
	for i := 0; i < 1000; i++ {
		l.allow(strconv.Itoa(i))
	}
	if len(l.m) > 100 {
		t.Fatalf("%d keys", len(l.m))
	}
	for i := 0; i < 5; i++ {
		if l.allow("k") != 0 {
			t.Fatal("burst denied")
		}
	}
	d1 := l.allow("k")
	clock.Add(d1)
	l.allow("k") // refill one
	d2 := l.allow("k")
	if d1 <= 0 || d2 <= d1 || d2 > maxBackoff {
		t.Fatalf("backoff %v then %v", d1, d2)
	}
}

// household fixture over HTTP: a owns A with member m and guest g; b owns B;
// x belongs nowhere.
type hhFixture struct {
	e          *env
	a, m, g, b *client
	x          *client
	hA, hB     string
	mID, gID   string
	inviteID   string
}

func newHH(t *testing.T, mutate ...func(*Options)) *hhFixture {
	e := newEnv(t, mutate...)
	f := &hhFixture{e: e}
	ctx := context.Background()
	for _, n := range []string{"a", "m", "g", "b", "x"} {
		e.user(n, false)
	}
	f.a, f.m, f.g, f.b, f.x = e.signedIn("a"), e.signedIn("m"), e.signedIn("g"), e.signedIn("b"), e.signedIn("x")
	mk := func(c *client, name string) string {
		st, v := c.do("POST", "/api/v1/households", map[string]string{"name": name, "timezone": "Europe/Amsterdam"})
		expect(t, st, v, 201, "")
		return field(v, "id").(string)
	}
	f.hA, f.hB = mk(f.a, "A"), mk(f.b, "B")
	mu, _ := e.st.UserByLogin(ctx, "m")
	gu, _ := e.st.UserByLogin(ctx, "g")
	f.mID, f.gID = mu.ID, gu.ID
	au, _ := e.st.UserByLogin(ctx, "a")
	if err := e.st.AddMember(ctx, store.Actor{UserID: au.ID}, f.hA, f.mID, store.RoleMember); err != nil {
		t.Fatal(err)
	}
	if err := e.st.AddMember(ctx, store.Actor{UserID: au.ID}, f.hA, f.gID, store.RoleGuest); err != nil {
		t.Fatal(err)
	}
	st, v := f.a.do("POST", "/api/v1/households/"+f.hA+"/invites", map[string]string{"role": "member"})
	expect(t, st, v, 201, "")
	f.inviteID = field(v, "id").(string)
	return f
}

func (f *hhFixture) path(pattern, hh string) (string, string) {
	method, p, _ := strings.Cut(pattern, " ")
	p = strings.NewReplacer("{hh}", hh, "{user}", f.mID, "{invite}", f.inviteID,
		"{area}", "nope", "{chore}", "nope", "{completion}", "nope",
		"{id}", "0190f0e0-0000-7000-8000-000000000000").Replace(p)
	return method, p
}

func validBody(pattern string) any {
	switch {
	case strings.HasSuffix(pattern, "/invites"):
		return map[string]string{"role": "guest"}
	case strings.Contains(pattern, "/members/"):
		return map[string]string{"role": "guest"}
	case strings.HasPrefix(pattern, "PATCH"):
		return map[string]string{"name": "Hacked"}
	}
	return nil
}

// TestHouseholdIsolation walks every household-scoped route in the table:
// outsiders get 404 (also when naming their own household with A's IDs is
// impossible: IDs only appear in the path), guests get 403 on anything above
// guest level, and household A is unchanged afterwards.
func TestHouseholdIsolation(t *testing.T) {
	f := newHH(t)
	n := 0
	for _, r := range f.e.srv.routes {
		if r.policy.auth != authHousehold {
			continue
		}
		n++
		method, p := f.path(r.pattern, f.hA)
		for name, c := range map[string]*client{"b": f.b, "x": f.x} {
			st, v := c.do(method, p, validBody(r.pattern))
			if st != 404 {
				t.Errorf("%s as %s: %d %v", r.pattern, name, st, v)
			}
		}
		// DELETE is guest-level on members (guests may leave), so it needs the
		// owner check; feeds are a guest's own, read-only objects.
		if r.policy.minRole != store.RoleGuest || (strings.HasPrefix(r.pattern, "DELETE") && !strings.Contains(r.pattern, "/feeds/")) {
			st, v := f.g.do(method, p, validBody(r.pattern))
			if st != 403 {
				t.Errorf("%s as guest: %d %v", r.pattern, st, v)
			}
		}
		if r.policy.minRole == store.RoleOwner {
			st, v := f.m.do(method, p, validBody(r.pattern))
			if st != 403 {
				t.Errorf("%s as member: %d %v", r.pattern, st, v)
			}
		}
	}
	if n < 30 {
		t.Fatalf("only %d household routes", n)
	}
	// A is intact.
	st, v := f.a.do("GET", "/api/v1/households/"+f.hA, nil)
	if st != 200 || field(v, "name") != "A" {
		t.Fatalf("household A changed: %v", v)
	}
	_, v = f.a.do("GET", "/api/v1/households/"+f.hA+"/members", nil)
	if l, _ := v.([]any); len(l) != 3 {
		t.Fatalf("members A changed: %v", v)
	}
	_, v = f.a.do("GET", "/api/v1/households/"+f.hA+"/invites", nil)
	if l, _ := v.([]any); len(l) != 1 {
		t.Fatalf("invites A changed: %v", v)
	}
	// b's listing shows only B.
	_, v = f.b.do("GET", "/api/v1/households", nil)
	if l, _ := v.([]any); len(l) != 1 || field(l[0], "id") != f.hB {
		t.Fatalf("b lists %v", v)
	}
	// Guest reads and can leave.
	st, _ = f.g.do("GET", "/api/v1/households/"+f.hA+"/members", nil)
	expect(t, st, nil, 200, "")
	st, _ = f.g.do("DELETE", "/api/v1/households/"+f.hA+"/members/"+f.gID, nil)
	expect(t, st, nil, 204, "")
}

func TestHouseholdOwnerOps(t *testing.T) {
	f := newHH(t)
	base := "/api/v1/households/" + f.hA
	st, v := f.a.do("PATCH", base, map[string]any{"timezone": "Not/AZone"})
	expect(t, st, v, 400, "invalid")
	st, v = f.a.do("PATCH", base, map[string]any{"name": "Home", "timezone": "America/New_York", "quietHours": map[string]int{"start": 1320, "end": 420}})
	if st != 200 || field(v, "timezone") != "America/New_York" || field(v, "quietHours") == nil {
		t.Fatalf("patch: %d %v", st, v)
	}
	st, v = f.a.do("PATCH", base, map[string]any{"quietHours": nil})
	if st != 200 || field(v, "quietHours") != nil || field(v, "name") != "Home" {
		t.Fatalf("clear quiet: %d %v", st, v)
	}
	st, _ = f.a.do("PATCH", base+"/members/"+f.mID, map[string]string{"role": "guest"})
	expect(t, st, nil, 204, "")

	// Invites: accept by an existing user, register a new account, revoke.
	st, v = f.a.do("POST", base+"/invites", map[string]any{"role": "member", "expiresInHours": 48})
	expect(t, st, v, 201, "")
	tok := field(v, "token").(string)
	st, v = f.x.do("POST", "/api/v1/invites/"+tok+"/accept", nil)
	if st != 200 || field(v, "householdId") != f.hA {
		t.Fatalf("accept: %d %v", st, v)
	}
	st, v = f.x.do("POST", "/api/v1/invites/"+tok+"/accept", nil)
	expect(t, st, v, 409, "conflict")
	newbie := f.e.client()
	st, v = newbie.do("POST", "/api/v1/invites/"+tok+"/register", map[string]string{"username": "newbie", "password": "password-newbie", "displayName": "N"})
	expect(t, st, v, 201, "")
	st, _ = newbie.do("GET", base, nil)
	expect(t, st, nil, 200, "")
	st, v = f.e.client().do("POST", "/api/v1/invites/not-a-token/register", map[string]string{"username": "z1", "password": "password-z1", "displayName": "Z"})
	expect(t, st, v, 404, "not_found")
	st, _ = f.a.do("DELETE", base+"/invites/"+f.inviteID, nil)
	expect(t, st, nil, 204, "")
	f.e.clock.Add(49 * time.Hour) // second invite expired
	st, v = f.b.do("POST", "/api/v1/invites/"+tok+"/accept", nil)
	expect(t, st, v, 404, "not_found")

	// Invite registration can be switched off by the admin setting.
	off := false
	if err := f.e.st.UpdateInstanceSettings(context.Background(), store.SettingsUpdate{InviteRegistration: &off}); err != nil {
		t.Fatal(err)
	}
	st, v = f.a.do("POST", base+"/invites", map[string]any{"role": "guest"})
	expect(t, st, v, 201, "")
	st, v = f.e.client().do("POST", "/api/v1/invites/"+field(v, "token").(string)+"/register", map[string]string{"username": "z2", "password": "password-z2", "displayName": "Z"})
	expect(t, st, v, 403, "registration_disabled")
}

func TestInviteLimit(t *testing.T) {
	f := newHH(t)
	lim := limits.Defaults[limits.ActiveInvitesPerHousehold]
	for i := 1; i < lim; i++ { // fixture created one
		st, v := f.a.do("POST", "/api/v1/households/"+f.hA+"/invites", map[string]string{"role": "guest"})
		expect(t, st, v, 201, "")
	}
	st, v := f.a.do("POST", "/api/v1/households/"+f.hA+"/invites", map[string]string{"role": "guest"})
	expect(t, st, v, 409, "limit_reached")
}

func TestAdmin(t *testing.T) {
	e := newEnv(t)
	e.user("root", true)
	e.user("ann", false)
	ann := e.signedIn("ann")
	for _, p := range []string{"/api/v1/admin/users", "/api/v1/admin/settings"} {
		st, v := ann.do("GET", p, nil)
		expect(t, st, v, 403, "forbidden")
	}
	root := e.signedIn("root")
	st, v := root.do("GET", "/api/v1/admin/users", nil)
	if l, _ := v.([]any); st != 200 || len(l) != 2 {
		t.Fatalf("users: %d %v", st, v)
	}
	st, v = root.do("POST", "/api/v1/admin/users", map[string]string{"username": "bob", "password": "password-bob", "displayName": "Bob"})
	expect(t, st, v, 201, "")
	bobID := field(v, "id").(string)
	st, v = root.do("POST", "/api/v1/admin/users/"+bobID+"/reset-password", nil)
	if st != 200 || len(field(v, "password").(string)) < 16 {
		t.Fatalf("reset: %d %v", st, v)
	}
	// Unset shows the effective default, not 0 (the form's min would block saving).
	st, v = root.do("GET", "/api/v1/admin/settings", nil)
	if st != 200 || field(v, "minPasswordLength") != float64(auth.DefaultMinPasswordLen) {
		t.Fatalf("default min password length: %d %v", st, v)
	}
	st, v = root.do("PATCH", "/api/v1/admin/settings", map[string]any{"limits": map[string]int{"nope": 1}})
	expect(t, st, v, 400, "invalid")
	st, v = root.do("PATCH", "/api/v1/admin/settings", map[string]any{"openSignup": true, "minPasswordLength": 12,
		"limits": map[string]int{string(limits.HouseholdsPerUser): 1}})
	if st != 200 || field(v, "openSignup") != true || field(field(v, "limits"), string(limits.HouseholdsPerUser)) != 1.0 {
		t.Fatalf("settings: %d %v", st, v)
	}
	body := map[string]string{"name": "H", "timezone": "UTC"}
	st, _ = ann.do("POST", "/api/v1/households", body)
	expect(t, st, nil, 201, "")
	st, v = ann.do("POST", "/api/v1/households", body)
	expect(t, st, v, 409, "limit_reached")
	e.clock.Add(16 * time.Minute)
	st, v = root.do("PATCH", "/api/v1/admin/settings", map[string]any{"openSignup": false})
	expect(t, st, v, 403, "step_up_required")
}
