package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

const (
	scRead     = store.ScopeRead
	scComplete = store.ScopeComplete
	scWrite    = store.ScopeWrite
)

// bearerDo sends one request authenticated by an API token only (no cookies,
// no CSRF header) and returns the response status, headers and decoded body.
func (e *env) bearerDo(method, path, secret string, body any, hdr ...string) (int, http.Header, any) {
	e.t.Helper()
	var r *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	} else {
		r = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, e.ts.URL+path, r)
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var v any
	_ = json.NewDecoder(resp.Body).Decode(&v)
	return resp.StatusCode, resp.Header, v
}

// mkToken creates a token through the API as the stepped-up client c.
func mkToken(t *testing.T, c *client, scopes []string, hhs ...string) (secret, id string) {
	t.Helper()
	st, v := c.do("POST", "/api/v1/account/tokens", map[string]any{"name": "test", "scopes": scopes, "households": hhs})
	expect(t, st, v, 201, "")
	return str(v, "token"), str(v, "id")
}

func mkIn(t *testing.T, c *client, hh string, body any) string {
	t.Helper()
	st, v := c.do("POST", "/api/v1/households/"+hh+"/chores", body)
	expect(t, st, v, 201, "")
	return str(v, "id")
}

// tokenEnv is the household fixture plus a stepped-up session per user.
type tokenEnv struct {
	*hhFixture
	aStep, mStep, gStep *client
}

func newTokenEnv(t *testing.T) *tokenEnv {
	f := newHH(t)
	te := &tokenEnv{hhFixture: f}
	te.aStep, te.mStep, te.gStep = f.e.signedInStepUp("a"), f.e.signedInStepUp("m"), f.e.signedInStepUp("g")
	return te
}

func TestTokenLifecycle(t *testing.T) {
	f := newTokenEnv(t)
	e := f.e
	in := map[string]any{"name": "Home Assistant", "scopes": []string{scComplete, scRead, scRead}, "households": []string{f.hA}, "expiresInDays": 30}
	// Creating needs a fresh step-up; listing and revoking do not.
	st, v := f.a.do("POST", "/api/v1/account/tokens", in)
	expect(t, st, v, 403, "step_up_required")
	st, v = f.aStep.do("POST", "/api/v1/account/tokens", in)
	expect(t, st, v, 201, "")
	secret, id := str(v, "token"), str(v, "id")
	if len(secret) != 46 || !strings.HasPrefix(secret, "gk_") {
		t.Fatalf("secret %q", secret)
	}
	if sc := field(v, "scopes").([]any); len(sc) != 2 || sc[0] != scRead || sc[1] != scComplete {
		t.Fatalf("scopes %v", sc)
	}
	if str(v, "expiresAt") == "" || field(v, "lastUsedAt") != nil {
		t.Fatalf("created: %v", v)
	}
	// Only the hash is stored.
	toks, err := e.st.ListAPITokens(context.Background(), f.userID("a"))
	if err != nil || len(toks) != 1 || toks[0].ID != id {
		t.Fatalf("stored: %v %v", toks, err)
	}
	// Listing never shows the secret.
	st, v = f.a.do("GET", "/api/v1/account/tokens", nil)
	if l := list(t, v); st != 200 || len(l) != 1 || str(l[0], "token") != "" || str(l[0], "id") != id {
		t.Fatalf("list: %d %v", st, v)
	}
	if hs := field(list(t, v)[0], "households").([]any); len(hs) != 1 || str(hs[0], "name") != "A" || str(hs[0], "role") != "owner" {
		t.Fatalf("households: %v", hs)
	}
	// Other users neither see nor revoke it.
	_, v = f.b.do("GET", "/api/v1/account/tokens", nil)
	if len(list(t, v)) != 0 {
		t.Fatalf("b sees %v", v)
	}
	st, v = f.b.do("DELETE", "/api/v1/account/tokens/"+id, nil)
	expect(t, st, v, 404, "not_found")
	if st, _, _ := e.bearerDo("GET", "/api/v1/token", secret, nil); st != 200 {
		t.Fatalf("token works: %d", st)
	}
	st, v = f.a.do("DELETE", "/api/v1/account/tokens/"+id, nil)
	expect(t, st, v, 204, "")
	st, hdr, v := e.bearerDo("GET", "/api/v1/token", secret, nil)
	expect(t, st, v, 401, "unauthenticated")
	if hdr.Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("WWW-Authenticate %q", hdr.Get("WWW-Authenticate"))
	}
	st, v = f.a.do("DELETE", "/api/v1/account/tokens/"+id, nil)
	expect(t, st, v, 404, "not_found")
	if _, v = f.a.do("GET", "/api/v1/account/tokens", nil); len(list(t, v)) != 0 {
		t.Fatalf("revoked token listed: %v", v)
	}
	// Audit: create and revoke name the token, never the secret.
	evs, _ := e.st.ListAuditForUser(context.Background(), f.userID("a"), 50)
	seen := map[string]bool{}
	for _, ev := range evs {
		if ev.Event == store.AuditTokenCreate || ev.Event == store.AuditTokenRevoke {
			seen[ev.Event] = ev.Meta["token"] == id
		}
		for _, mv := range ev.Meta {
			if strings.Contains(mv, secret) {
				t.Fatalf("secret in audit meta of %s", ev.Event)
			}
		}
	}
	if !seen[store.AuditTokenCreate] || !seen[store.AuditTokenRevoke] {
		t.Fatalf("audit %v", seen)
	}
}

func TestTokenCreateValidation(t *testing.T) {
	f := newTokenEnv(t)
	ok := func() map[string]any {
		return map[string]any{"name": "x", "scopes": []string{scRead}, "households": []string{f.hA}}
	}
	cases := []struct {
		name   string
		mutate func(m map[string]any)
		status int
		code   string
	}{
		{"no name", func(m map[string]any) { m["name"] = "  " }, 400, "invalid"},
		{"no scopes", func(m map[string]any) { m["scopes"] = []string{} }, 400, "invalid"},
		{"unknown scope", func(m map[string]any) { m["scopes"] = []string{"chores:admin"} }, 400, "invalid"},
		{"no households", func(m map[string]any) { m["households"] = []string{} }, 400, "invalid"},
		{"foreign household", func(m map[string]any) { m["households"] = []string{f.hB} }, 404, "not_found"},
		{"one foreign of two", func(m map[string]any) { m["households"] = []string{f.hA, f.hB} }, 404, "not_found"},
		{"expiry 0", func(m map[string]any) { m["expiresInDays"] = 0 }, 400, "invalid"},
		{"expiry too long", func(m map[string]any) { m["expiresInDays"] = 3651 }, 400, "invalid"},
		{"unknown field", func(m map[string]any) { m["admin"] = true }, 400, "invalid_json"},
	}
	for _, c := range cases {
		m := ok()
		c.mutate(m)
		st, v := f.aStep.do("POST", "/api/v1/account/tokens", m)
		if st != c.status || code(v) != c.code {
			t.Errorf("%s: %d %v", c.name, st, v)
		}
	}
	// A guest may hold a token (it can only ever read); null expiry = never.
	m := ok()
	m["expiresInDays"] = nil
	st, v := f.aStep.do("POST", "/api/v1/account/tokens", m)
	expect(t, st, v, 201, "")
	if field(v, "expiresAt") != nil {
		t.Fatalf("expiresAt %v", field(v, "expiresAt"))
	}
	// The per-user limit (api_tokens_per_user).
	if err := f.e.st.UpdateInstanceSettings(context.Background(), store.SettingsUpdate{Limits: map[string]int{"api_tokens_per_user": 2}}); err != nil {
		t.Fatal(err)
	}
	st, v = f.aStep.do("POST", "/api/v1/account/tokens", ok())
	expect(t, st, v, 201, "")
	st, v = f.aStep.do("POST", "/api/v1/account/tokens", ok())
	expect(t, st, v, 409, "limit_reached")
}

func TestTokenDescribesItself(t *testing.T) {
	f := newTokenEnv(t)
	secret, id := mkToken(t, f.aStep, []string{scRead}, f.hA)
	st, _, v := f.e.bearerDo("GET", "/api/v1/token", secret, nil)
	expect(t, st, v, 200, "")
	if str(v, "id") != id || str(v, "name") != "test" || field(v, "expiresAt") != nil ||
		str(field(v, "user"), "displayName") != "a" || str(field(v, "user"), "id") != f.userID("a") {
		t.Fatalf("describe: %v", v)
	}
	hs := field(v, "households").([]any)
	if len(hs) != 1 || str(hs[0], "id") != f.hA || str(hs[0], "name") != "A" || str(hs[0], "role") != "owner" {
		t.Fatalf("households %v", hs)
	}
	if sc := field(v, "scopes").([]any); len(sc) != 1 || sc[0] != scRead {
		t.Fatalf("scopes %v", sc)
	}
	// Bearer only: a session cookie is not enough.
	st, v = f.a.do("GET", "/api/v1/token", nil)
	expect(t, st, v, 401, "unauthenticated")
}

// tokenRoutes is the contract: exactly these routes accept a token, each with
// exactly one scope. Widening it must be a deliberate edit of this table.
var tokenRoutes = map[string]string{
	"GET /api/v1/households":                                                      scRead,
	"GET /api/v1/households/{hh}":                                                 scRead,
	"GET /api/v1/templates":                                                       scRead,
	"GET /api/v1/households/{hh}/areas":                                           scRead,
	"GET /api/v1/households/{hh}/chores":                                          scRead,
	"GET /api/v1/households/{hh}/chores/{chore}":                                  scRead,
	"GET /api/v1/households/{hh}/chores/{chore}/completions":                      scRead,
	"GET /api/v1/households/{hh}/calendar":                                        scRead,
	"GET /api/v1/households/{hh}/chores/{chore}/reminders":                        scRead,
	"GET /api/v1/households/{hh}/reminder-presets":                                scRead,
	"POST /api/v1/households/{hh}/chores/{chore}/complete":                        scComplete,
	"POST /api/v1/households/{hh}/chores/{chore}/snooze":                          scComplete,
	"POST /api/v1/households/{hh}/chores/{chore}/skip":                            scComplete,
	"POST /api/v1/households/{hh}/chores/{chore}/pause":                           scComplete,
	"POST /api/v1/households/{hh}/chores/{chore}/resume":                          scComplete,
	"POST /api/v1/households/{hh}/chores/{chore}/completions/{completion}/revert": scComplete,
	"POST /api/v1/households/{hh}/chores":                                         scWrite,
	"PUT /api/v1/households/{hh}/chores/{chore}":                                  scWrite,
	"DELETE /api/v1/households/{hh}/chores/{chore}":                               scWrite,
	"POST /api/v1/households/{hh}/chores/{chore}/archive":                         scWrite,
	"POST /api/v1/households/{hh}/chores/{chore}/unarchive":                       scWrite,
	"PUT /api/v1/households/{hh}/chores/{chore}/reminders":                        scWrite,
	"PUT /api/v1/households/{hh}/chores/{chore}/mute":                             scWrite,
	"DELETE /api/v1/households/{hh}/chores/{chore}/mute":                          scWrite,
}

// Every route either refuses tokens or requires exactly its declared scope,
// and the set that accepts tokens is the contract above.
func TestEveryRouteRefusesTokensOrRequiresItsScope(t *testing.T) {
	f := newTokenEnv(t)
	all := []string{scRead, scComplete, scWrite}
	secrets := map[string]string{}
	for _, sc := range all {
		secrets[sc], _ = mkToken(t, f.aStep, []string{sc}, f.hA)
	}
	everything, _ := mkToken(t, f.aStep, all, f.hA)
	n := 0
	for _, r := range f.e.srv.routes {
		if !strings.Contains(r.pattern, "/api/v1/") || r.policy.auth < authSession || r.policy.auth > authAdmin {
			continue
		}
		n++
		method, p := f.path(r.pattern, f.hA)
		want, capable := tokenRoutes[r.pattern]
		if (r.policy.token != tokenNone) != capable || capable && r.policy.token != want {
			t.Errorf("%s: policy token %q, contract %q", r.pattern, r.policy.token, want)
		}
		if !capable {
			st, _, v := f.e.bearerDo(method, p, everything, nil)
			if st != 403 || code(v) != "token_not_allowed" {
				t.Errorf("%s: token got %d %v", r.pattern, st, v)
			}
			continue
		}
		for _, sc := range all {
			st, _, v := f.e.bearerDo(method, p, secrets[sc], nil)
			if sc == want && (st == 401 || code(v) == "insufficient_scope" || code(v) == "token_not_allowed") {
				t.Errorf("%s: token with %s refused: %d %v", r.pattern, sc, st, v)
			}
			if sc != want && (st != 403 || code(v) != "insufficient_scope") {
				t.Errorf("%s: token with %s only got %d %v", r.pattern, sc, st, v)
			}
		}
	}
	if n < 80 {
		t.Fatalf("only %d routes walked", n)
	}
	for pat := range tokenRoutes {
		found := false
		for _, r := range f.e.srv.routes {
			found = found || r.pattern == pat
		}
		if !found {
			t.Errorf("contract route %q does not exist", pat)
		}
	}
}

// A token bound to A cannot touch B even when its owner is a member of B.
func TestTokenHouseholdBindingAndIsolation(t *testing.T) {
	f := newTokenEnv(t)
	ctx := context.Background()
	// a joins B as a member, so only the binding keeps the token out.
	if err := f.e.st.AddMember(ctx, store.Actor{UserID: f.userID("b")}, f.hB, f.userID("a"), store.RoleMember); err != nil {
		t.Fatal(err)
	}
	cB := mkIn(t, f.b, f.hB, intervalBody("B chore", "2026-11-21"))
	cA := f.mk(t, f.a, intervalBody("A chore", "2026-11-21"))
	secret, _ := mkToken(t, f.aStep, []string{scRead, scComplete, scWrite}, f.hA)
	do := func(method, path string, body any) (int, any) {
		st, _, v := f.e.bearerDo(method, path, secret, body)
		return st, v
	}
	hB := "/api/v1/households/" + f.hB
	st, v := do("GET", hB, nil)
	expect(t, st, v, 404, "not_found")
	st, v = do("GET", hB+"/chores", nil)
	expect(t, st, v, 404, "not_found")
	st, v = do("POST", hB+"/chores", intervalBody("Sneaky", "2026-11-21"))
	expect(t, st, v, 404, "not_found")
	st, v = do("POST", hB+"/chores/"+cB+"/complete", nil)
	expect(t, st, v, 404, "not_found")
	// The same request by the owner's session works: it is the binding.
	st, v = f.a.do("GET", hB+"/chores", nil)
	expect(t, st, v, 200, "")
	// An unknown household answers the same.
	st, v = do("GET", "/api/v1/households/0190f0e0-0000-7000-8000-000000000000", nil)
	expect(t, st, v, 404, "not_found")
	// The household list shows only the bound one.
	st, v = do("GET", "/api/v1/households", nil)
	if l := list(t, v); st != 200 || len(l) != 1 || str(l[0], "id") != f.hA {
		t.Fatalf("list: %d %v", st, v)
	}
	st, v = do("GET", "/api/v1/households/"+f.hA+"/chores", nil)
	if l := list(t, v); st != 200 || len(l) != 1 {
		t.Fatalf("A chores: %d %v", st, v)
	}
	st, v = do("POST", "/api/v1/households/"+f.hA+"/chores/"+cA+"/complete", nil)
	expect(t, st, v, 201, "")
	// Leaving the household cuts the token off like any outsider.
	if err := f.e.st.AddMember(ctx, store.Actor{UserID: f.userID("a")}, f.hA, f.userID("x"), store.RoleOwner); err != nil {
		t.Fatal(err)
	}
	if err := f.e.st.RemoveMember(ctx, store.Actor{UserID: f.userID("a")}, f.hA, f.userID("a")); err != nil {
		t.Fatal(err)
	}
	st, v = do("GET", "/api/v1/households/"+f.hA+"/chores", nil)
	expect(t, st, v, 404, "not_found")
}

// Scope AND the owner's current role: a guest's token can only read, and a
// demotion applies to existing tokens at once.
func TestTokenEffectivePermissionFollowsOwnerRole(t *testing.T) {
	f := newTokenEnv(t)
	cid := f.mk(t, f.a, intervalBody("Dishes", "2026-11-21"))
	all := []string{scRead, scComplete, scWrite}
	gTok, _ := mkToken(t, f.gStep, all, f.hA)
	chores := "/api/v1/households/" + f.hA + "/chores"
	st, _, v := f.e.bearerDo("GET", chores, gTok, nil)
	expect(t, st, v, 200, "")
	st, _, v = f.e.bearerDo("POST", chores+"/"+cid+"/complete", gTok, nil)
	expect(t, st, v, 403, "forbidden")
	st, _, v = f.e.bearerDo("POST", chores, gTok, intervalBody("New", "2026-11-21"))
	expect(t, st, v, 403, "forbidden")
	// A member's token with read+complete can complete but not write.
	mTok, _ := mkToken(t, f.mStep, []string{scRead, scComplete}, f.hA)
	st, _, v = f.e.bearerDo("POST", chores+"/"+cid+"/complete", mTok, nil)
	expect(t, st, v, 201, "")
	st, _, v = f.e.bearerDo("POST", chores, mTok, intervalBody("New", "2026-11-21"))
	expect(t, st, v, 403, "insufficient_scope")
	// Demoting the owner to guest takes effect on the existing token.
	mWrite, _ := mkToken(t, f.mStep, all, f.hA)
	st, _, v = f.e.bearerDo("POST", chores, mWrite, intervalBody("Made by token", "2026-11-21"))
	expect(t, st, v, 201, "")
	if err := f.e.st.ChangeRole(context.Background(), store.Actor{UserID: f.userID("a")}, f.hA, f.mID, store.RoleGuest); err != nil {
		t.Fatal(err)
	}
	st, _, v = f.e.bearerDo("POST", chores, mWrite, intervalBody("Again", "2026-11-21"))
	expect(t, st, v, 403, "forbidden")
	st, _, v = f.e.bearerDo("GET", chores, mWrite, nil)
	expect(t, st, v, 200, "")
	st, _, v = f.e.bearerDo("GET", "/api/v1/token", mWrite, nil)
	if hs := field(v, "households").([]any); st != 200 || str(hs[0], "role") != "guest" {
		t.Fatalf("role in /token: %v", v)
	}
}

func TestTokenRejections401(t *testing.T) {
	f := newTokenEnv(t)
	e := f.e
	st, id := mkToken(t, f.aStep, []string{scRead}, f.hA)
	short, _ := func() (string, string) {
		s, v := f.aStep.do("POST", "/api/v1/account/tokens", map[string]any{"name": "short", "scopes": []string{scRead}, "households": []string{f.hA}, "expiresInDays": 1})
		expect(t, s, v, 201, "")
		return str(v, "token"), str(v, "id")
	}()
	if err := e.st.RevokeAPIToken(context.Background(), f.userID("a"), id); err != nil {
		t.Fatal(err)
	}
	e.clock.Add(25 * time.Hour) // expires "short"
	cases := map[string]string{
		"missing": "", "unknown": "gk_" + ids.Token(), "wrong prefix": "xx_" + ids.Token(), "malformed": "gk_short",
		"revoked": st, "expired": short, "session-like": f.a.cookie(sessionCookie),
	}
	var first string
	for name, secret := range cases {
		s, hdr, v := e.bearerDo("GET", "/api/v1/token", secret, nil)
		b, _ := json.Marshal(v)
		if s != 401 || hdr.Get("WWW-Authenticate") != "Bearer" || code(v) != "unauthenticated" {
			t.Errorf("%s: %d %v %q", name, s, v, hdr.Get("WWW-Authenticate"))
		}
		if first == "" {
			first = string(b)
		} else if string(b) != first {
			t.Errorf("%s: body differs (oracle): %s", name, b)
		}
		// Also on a normal token route.
		if s, _, _ := e.bearerDo("GET", "/api/v1/households", secret, nil); s != 401 {
			t.Errorf("%s: households %d", name, s)
		}
	}
}

func TestBearerIgnoresCookiesAndNeedsNoCSRF(t *testing.T) {
	f := newTokenEnv(t)
	e := f.e
	cid := f.mk(t, f.a, intervalBody("Dishes", "2026-11-21"))
	// m's token on a client that holds a's session cookie: the token decides.
	mTok, _ := mkToken(t, f.mStep, []string{scRead, scComplete}, f.hA)
	req, _ := http.NewRequest("GET", e.ts.URL+"/api/v1/token", nil)
	req.Header.Set("Authorization", "Bearer "+mTok)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: f.a.cookie(sessionCookie)})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var v any
	_ = json.NewDecoder(resp.Body).Decode(&v)
	resp.Body.Close()
	if resp.StatusCode != 200 || str(field(v, "user"), "displayName") != "m" || len(resp.Header.Values("Set-Cookie")) != 0 {
		t.Fatalf("%d %v cookies %v", resp.StatusCode, v, resp.Header.Values("Set-Cookie"))
	}
	// A bad token is not rescued by a valid cookie, and clears nothing.
	req, _ = http.NewRequest("GET", e.ts.URL+"/api/v1/households", nil)
	req.Header.Set("Authorization", "Bearer gk_"+ids.Token())
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: f.a.cookie(sessionCookie)})
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 || len(resp.Header.Values("Set-Cookie")) != 0 {
		t.Fatalf("bad token with cookie: %d %v", resp.StatusCode, resp.Header.Values("Set-Cookie"))
	}
	// Unsafe method: no CSRF cookie or header, even with a cross-site hint.
	complete := "/api/v1/households/" + f.hA + "/chores/" + cid + "/complete"
	st, _, v := e.bearerDo("POST", complete, mTok, nil, "Sec-Fetch-Site", "cross-site", "Origin", "https://evil.example")
	expect(t, st, v, 201, "")
	// Without a bearer header the same request still needs CSRF.
	c := e.signedIn("m")
	resp = c.raw("POST", complete, nil, "")
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("session without CSRF: %d", resp.StatusCode)
	}
	// A non-bearer Authorization scheme is ignored.
	st, _, v = e.bearerDo("GET", "/api/v1/households", "", nil, "Authorization", "Basic Zm9vOmJhcg==")
	expect(t, st, v, 401, "unauthenticated")
}

func TestTokenCompletionAttribution(t *testing.T) {
	f := newTokenEnv(t)
	cid := f.mk(t, f.a, intervalBody("Dishes", "2026-11-21"))
	secret, tid := mkToken(t, f.mStep, []string{scRead, scComplete}, f.hA)
	chore := "/api/v1/households/" + f.hA + "/chores/" + cid
	st, _, v := f.e.bearerDo("POST", chore+"/complete", secret, map[string]string{"note": "by robot"})
	expect(t, st, v, 201, "")
	c := field(v, "completion")
	if str(c, "source") != "api_token" || str(c, "userId") != f.mID {
		t.Fatalf("completion %v", c)
	}
	undo := str(c, "id")
	st, _, v = f.e.bearerDo("GET", chore+"/completions", secret, nil)
	if items := field(v, "items").([]any); st != 200 || len(items) != 1 || str(items[0], "source") != "api_token" {
		t.Fatalf("history %d %v", st, v)
	}
	comps, _, err := f.e.st.CompletionsPage(context.Background(), store.Actor{UserID: f.mID}, f.hA, cid, 10, "")
	if err != nil || len(comps) != 1 || comps[0].TokenID != tid || comps[0].Source != store.SourceAPIToken {
		t.Fatalf("stored %v %v", comps, err)
	}
	evs, _ := f.e.st.ListAuditForUser(context.Background(), f.mID, 20)
	i := slices.IndexFunc(evs, func(e store.AuditEvent) bool { return e.Event == store.AuditTokenComplete })
	if i < 0 || evs[i].Meta["token"] != tid || evs[i].HouseholdID != f.hA {
		t.Fatalf("audit %+v", evs)
	}
	// Completing in the app stays source app.
	st2, v2 := f.m.do("POST", chore+"/complete", nil)
	expect(t, st2, v2, 201, "")
	if str(field(v2, "completion"), "source") != "app" {
		t.Fatalf("app completion %v", v2)
	}
	// Undo by token works with chores:complete.
	st, _, v = f.e.bearerDo("POST", chore+"/completions/"+undo+"/revert", secret, nil)
	expect(t, st, v, 200, "")
}

func TestTokenRateLimits(t *testing.T) {
	f := newTokenEnv(t)
	secret, _ := mkToken(t, f.aStep, []string{scRead}, f.hA)
	// Real per-bucket rates (the test env swaps in a generous limiter).
	f.e.srv.rl = newRateLimiter(5, 10000, f.e.clock.Now)
	f.e.srv.rl.perMinute, f.e.srv.rl.burstOf = bucketRates, bucketBursts
	var denied int
	for i := 0; i < 70; i++ {
		st, hdr, _ := f.e.bearerDo("GET", "/api/v1/households", secret, nil)
		if st == 429 {
			if denied == 0 && i != 60 {
				t.Fatalf("first 429 at request %d, want 60 (burst)", i)
			}
			if hdr.Get("Retry-After") == "" {
				t.Fatal("no Retry-After")
			}
			denied++
		}
	}
	if denied != 10 {
		t.Fatalf("%d denied", denied)
	}
	f.e.clock.Add(time.Minute) // refills 120 tokens, capped at the burst
	if st, _, _ := f.e.bearerDo("GET", "/api/v1/households", secret, nil); st != 200 {
		t.Fatalf("after refill: %d", st)
	}
	// Guessing tokens from one address is bounded per IP.
	f.e.srv.rl = newRateLimiter(5, 10000, f.e.clock.Now)
	f.e.srv.rl.perMinute, f.e.srv.rl.burstOf = bucketRates, bucketBursts
	last := 0
	for i := 0; i < 110; i++ {
		last, _, _ = f.e.bearerDo("GET", "/api/v1/token", "gk_"+ids.Token(), nil)
	}
	if last != 429 {
		t.Fatalf("guessing not throttled: %d", last)
	}
}

func TestTokenLastUsedIsThrottled(t *testing.T) {
	f := newTokenEnv(t)
	secret, id := mkToken(t, f.aStep, []string{scRead}, f.hA)
	lastUsed := func() time.Time {
		ts, err := f.e.st.ListAPITokens(context.Background(), f.userID("a"))
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range ts {
			if k.ID == id {
				return k.LastUsedAt
			}
		}
		t.Fatal("token missing")
		return time.Time{}
	}
	if !lastUsed().IsZero() {
		t.Fatal("used before use")
	}
	call := func() { f.e.bearerDo("GET", "/api/v1/households", secret, nil) }
	call()
	first := lastUsed()
	if first.IsZero() {
		t.Fatal("not recorded")
	}
	f.e.clock.Add(30 * time.Second)
	call()
	if !lastUsed().Equal(first) {
		t.Fatal("written again within a minute")
	}
	f.e.clock.Add(31 * time.Second)
	call()
	if !lastUsed().After(first) {
		t.Fatal("not refreshed after a minute")
	}
}

// Tokens die with the account, and a token cannot manage tokens.
func TestTokenCannotManageTokens(t *testing.T) {
	f := newTokenEnv(t)
	secret, id := mkToken(t, f.aStep, []string{scRead, scComplete, scWrite}, f.hA)
	for _, r := range [][2]string{{"GET", "/api/v1/account/tokens"}, {"POST", "/api/v1/account/tokens"}, {"DELETE", "/api/v1/account/tokens/" + id}, {"GET", "/api/v1/me"}} {
		st, _, v := f.e.bearerDo(r[0], r[1], secret, nil)
		expect(t, st, v, 403, "token_not_allowed")
	}
}
