package api

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
)

const prefixBase = "http://localhost/sub" // loopback http, so OAuth may be enabled

var prefixFS = fstest.MapFS{
	"index.html": {Data: []byte(`<!doctype html><html><head><link rel="manifest" href="./manifest.webmanifest" />` +
		`<script type="module" src="./assets/app-1.js"></script></head><body></body></html>`)},
	"assets/app-1.js":      {Data: []byte("console.log(1)")},
	"manifest.webmanifest": {Data: []byte(`{"id":"/","start_url":"/","scope":"/","icons":[{"src":"/icons/icon-192.png"}]}`)},
	"sw.js":                {Data: []byte("//")},
}

// newPrefixEnv serves under /sub. Every helper request goes through ts.URL,
// so pointing it at the prefix makes the existing client helpers prefix-aware.
// root is the server's address without the prefix.
func newPrefixEnv(t *testing.T) (e *env, root string) {
	e = newEnv(t, func(o *Options) { o.BaseURL = prefixBase; o.Static = prefixFS })
	root = e.ts.URL
	e.ts.URL += "/sub"
	return e, root
}

type rawResp struct {
	StatusCode int
	Header     http.Header
}

func (r rawResp) Cookies() []*http.Cookie { return (&http.Response{Header: r.Header}).Cookies() }

func rawGet(t *testing.T, url string) (rawResp, string) {
	t.Helper()
	resp, err := (&http.Client{CheckRedirect: noRedirect}).Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return rawResp{resp.StatusCode, resp.Header}, string(b)
}

func TestPrefixRouting(t *testing.T) {
	_, root := newPrefixEnv(t)
	for p, want := range map[string]int{"/sub/healthz": 200, "/sub/readyz": 200, "/sub/api/v1/session": 200, "/sub/assets/app-1.js": 200,
		"/sub/some/deep/link": 200, "/sub/api/v1/nope": 404,
		"/healthz": 404, "/api/v1/session": 404, "/other": 404, "/subway": 404} {
		if resp, _ := rawGet(t, root+p); resp.StatusCode != want {
			t.Errorf("GET %s = %d, want %d", p, resp.StatusCode, want)
		}
	}
	for p, loc := range map[string]string{"/": "/sub/", "/sub": "/sub/"} {
		if resp, _ := rawGet(t, root+p); resp.StatusCode != http.StatusTemporaryRedirect || resp.Header.Get("Location") != loc {
			t.Errorf("GET %s = %d %q", p, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
}

func TestPrefixShell(t *testing.T) {
	e, _ := newPrefixEnv(t)
	_, body := rawGet(t, e.ts.URL+"/chores/1")
	for _, want := range []string{`href="/sub/manifest.webmanifest"`, `src="/sub/assets/app-1.js"`, `<meta name="gunkly-base" content="/sub" />`} {
		if !strings.Contains(body, want) {
			t.Errorf("index.html lacks %s: %s", want, body)
		}
	}
	_, mf := rawGet(t, e.ts.URL+"/manifest.webmanifest")
	for _, want := range []string{`"id":"/sub/"`, `"start_url":"/sub/"`, `"scope":"/sub/"`, `"src":"/sub/icons/icon-192.png"`} {
		if !strings.Contains(mf, want) {
			t.Errorf("manifest lacks %s: %s", want, mf)
		}
	}
	// No prefix: absolute paths from the root, no meta tag.
	r := newEnv(t, func(o *Options) { o.Static = prefixFS })
	_, body = rawGet(t, r.ts.URL+"/")
	if !strings.Contains(body, `src="/assets/app-1.js"`) || strings.Contains(body, "gunkly-base") {
		t.Errorf("root index.html: %s", body)
	}
	_, mf = rawGet(t, r.ts.URL+"/manifest.webmanifest")
	if !strings.Contains(mf, `"start_url":"/"`) || !strings.Contains(mf, `"src":"/icons/icon-192.png"`) {
		t.Errorf("root manifest: %s", mf)
	}
}

func TestPrefixCookies(t *testing.T) {
	e, _ := newPrefixEnv(t)
	e.user("a", false)
	c := e.client()
	c.h["Origin"] = "http://localhost" // a browser's Origin carries no path
	resp := c.raw("POST", "/api/v1/session", map[string]string{"login": "a", "password": "password-a"}, c.cookie(csrfCookie))
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("login = %d", resp.StatusCode)
	}
	seen := map[string]bool{}
	for _, ck := range resp.Cookies() {
		seen[ck.Name] = true
		if ck.Path != "/sub" {
			t.Errorf("cookie %s path %q, want /sub", ck.Name, ck.Path)
		}
	}
	if !seen[sessionCookie] {
		t.Errorf("no session cookie in %v", resp.Cookies())
	}
	if st, _ := c.do("GET", "/api/v1/session", nil); st != 200 {
		t.Errorf("session = %d", st)
	}
	if p := e.srv.ssoCookie("v", 60).Path; p != "/sub/api/v1/auth/oidc" {
		t.Errorf("sso cookie path %q", p)
	}
	// Root deployments keep Path=/.
	r := newEnv(t)
	rr, _ := rawGet(t, r.ts.URL+"/api/v1/session")

	if len(rr.Cookies()) == 0 {
		t.Fatal("no cookies at root")
	}
	for _, ck := range rr.Cookies() {
		if ck.Path != "/" {
			t.Errorf("root cookie %s path %q", ck.Name, ck.Path)
		}
	}
}

func TestPrefixLinks(t *testing.T) {
	e, _ := newPrefixEnv(t)
	s := e.srv
	for got, want := range map[string]string{
		s.feedURL("tok"):             "http://localhost/sub/ical/tok.ics",
		s.link("/verify-email", "t"): "http://localhost/sub/verify-email?token=t",
		s.resourceURL():              "http://localhost/sub/mcp",
		s.metadataURL():              "http://localhost/sub/.well-known/oauth-protected-resource",
		s.path("/signin"):            "/sub/signin",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	// A mark-done page (no session, no JS) links its stylesheet and icons under the prefix.
	_, body := rawGet(t, e.ts.URL+"/done/unknown-token")
	if !strings.Contains(body, `href="/sub/done.css"`) || !strings.Contains(body, `src="/sub/icons/mark-light.png"`) {
		t.Errorf("done page: %s", body)
	}
}

func TestPrefixOAuthMetadata(t *testing.T) {
	e, root := newPrefixEnv(t)
	setOAuth(t, e, true)
	pr := prefixBase
	for _, p := range []string{"/sub/.well-known/oauth-protected-resource", "/sub/.well-known/oauth-protected-resource/mcp",
		// RFC 9728 / RFC 8414 placement for a resource or issuer with a path: the well-known segment sits between host and path.
		"/.well-known/oauth-protected-resource/sub/mcp"} {
		m := asMap(mustBody(t, root+p))
		if m["resource"] != pr+"/mcp" || m["authorization_servers"].([]any)[0] != pr {
			t.Errorf("%s: %v", p, m)
		}
	}
	for _, p := range []string{"/sub/.well-known/oauth-authorization-server", "/.well-known/oauth-authorization-server/sub"} {
		m := asMap(mustBody(t, root+p))
		if m["issuer"] != pr || m["authorization_endpoint"] != pr+"/oauth/authorize" || m["token_endpoint"] != pr+"/oauth/token" ||
			m["registration_endpoint"] != pr+"/oauth/register" || m["revocation_endpoint"] != pr+"/oauth/revoke" {
			t.Errorf("%s: %v", p, m)
		}
	}
	for _, p := range []string{"/.well-known/oauth-authorization-server", "/.well-known/oauth-protected-resource", "/.well-known/oauth-authorization-server/other"} {
		if resp, _ := rawGet(t, root+p); resp.StatusCode != 404 {
			t.Errorf("%s = %d, want 404", p, resp.StatusCode)
		}
	}
	_, h, _ := e.rawMCP("POST", "", `{}`)
	if h.Get("WWW-Authenticate") != `Bearer resource_metadata="`+pr+`/.well-known/oauth-protected-resource"` {
		t.Errorf("challenge %q", h.Get("WWW-Authenticate"))
	}
}

func mustBody(t *testing.T, url string) []byte {
	t.Helper()
	resp, body := rawGet(t, url)
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s = %d", url, resp.StatusCode)
	}
	return []byte(body)
}
