package api

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

func TestParseForwarded(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string // "" is an unusable hop
	}{
		{"ipv4", []string{"for=192.0.2.43"}, []string{"192.0.2.43"}},
		{"ipv4 port", []string{"for=192.0.2.43:47011"}, []string{"192.0.2.43"}},
		{"quoted ipv4 port", []string{`for="192.0.2.43:47011"`}, []string{"192.0.2.43"}},
		{"ipv6", []string{`for="[2001:db8:cafe::17]"`}, []string{"2001:db8:cafe::17"}},
		{"ipv6 port", []string{`for="[2001:db8:cafe::17]:4711"`}, []string{"2001:db8:cafe::17"}},
		{"ipv6 mapped", []string{`for="[::ffff:192.0.2.1]"`}, []string{"192.0.2.1"}},
		{"case and spaces", []string{`For = 192.0.2.1 ; Proto=https`}, []string{"192.0.2.1"}},
		{"other params", []string{`by=203.0.113.1;for=192.0.2.60;proto=http`}, []string{"192.0.2.60"}},
		{"chain", []string{`for=198.51.100.17, for=192.0.2.43;proto=https`}, []string{"198.51.100.17", "192.0.2.43"}},
		{"two headers", []string{"for=1.1.1.1", "for=2.2.2.2"}, []string{"1.1.1.1", "2.2.2.2"}},
		{"comma in quotes", []string{`for="a,b", for=3.3.3.3`}, []string{"", "3.3.3.3"}},
		{"obfuscated", []string{"for=_hidden, for=_SEVKISEK:_port"}, []string{"", ""}},
		{"unknown", []string{"for=unknown"}, []string{""}},
		{"obfuscated port", []string{"for=192.0.2.1:_p"}, []string{"192.0.2.1"}},
		{"no for", []string{"by=192.0.2.1;proto=https"}, []string{""}},
		{"bare ipv6 refused", []string{"for=2001:db8::1"}, []string{""}},
		{"unclosed bracket", []string{`for="[2001:db8::1"`}, []string{""}},
		{"unclosed quote", []string{`for="1.2.3.4`}, []string{""}},
		{"empty value", []string{"for="}, []string{""}},
		{"empty port", []string{"for=1.2.3.4:"}, []string{""}},
		{"garbage", []string{";;;,,,==="}, []string{"", ""}},
		{"empty", []string{""}, nil},
	}
	for _, c := range cases {
		var got []string
		for _, a := range parseForwarded(c.in) {
			if a.IsValid() {
				got = append(got, a.String())
			} else {
				got = append(got, "")
			}
		}
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestClientIPForwarded(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("::1/128")}
	cases := []struct{ name, peer, fwd, xff, want string }{
		{"untrusted peer ignores Forwarded", "203.0.113.9:1", "for=1.2.3.4", "", "203.0.113.9"},
		{"one proxy", "10.0.0.2:1", "for=1.2.3.4", "", "1.2.3.4"},
		{"port stripped", "10.0.0.2:1", "for=1.2.3.4:5555", "", "1.2.3.4"},
		{"right to left skips spoof", "10.0.0.2:1", "for=6.6.6.6, for=1.2.3.4, for=10.0.0.7", "", "1.2.3.4"},
		{"ipv6 client", "[::1]:1", `for="[2001:db8::1]:99"`, "", "2001:db8::1"},
		{"obfuscated stops walk", "10.0.0.2:1", "for=6.6.6.6, for=_hidden, for=10.0.0.7", "", "10.0.0.7"},
		{"unknown stops walk", "10.0.0.2:1", "for=6.6.6.6, for=unknown", "", "10.0.0.2"},
		{"XFF preferred over client-sent Forwarded", "10.0.0.2:1", "for=1.2.3.4", "5.5.5.5", "5.5.5.5"},
		{"XFF used without Forwarded", "10.0.0.2:1", "", "5.5.5.5", "5.5.5.5"},
		{"malformed Forwarded ignored when XFF present", "10.0.0.2:1", "garbage", "5.5.5.5", "5.5.5.5"},
		{"spoofed Forwarded with trusted XFF chain", "10.0.0.2:1", "for=6.6.6.6", "6.6.6.7, 1.2.3.4, 10.0.0.7", "1.2.3.4"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.peer
		if c.fwd != "" {
			r.Header.Set("Forwarded", c.fwd)
		}
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := clientIP(r, trusted); got.String() != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func FuzzForwarded(f *testing.F) {
	for _, s := range []string{
		`for=192.0.2.43`, `for="[2001:db8:cafe::17]:4711"`, `for=_hidden, for=unknown`,
		`by=1.1.1.1;for="a,b;c"`, `for="\"`, `for=[::1`, ";,=\"", `for=1.2.3.4:_x, For="[::ffff:1.2.3.4]"`,
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		for _, a := range parseForwarded([]string{s}) {
			if !a.IsValid() {
				continue
			}
			if a.Zone() != "" || a.Is4In6() {
				t.Fatalf("unnormalised address %q from %q", a, s)
			}
		}
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "10.0.0.2:1"
		r.Header["Forwarded"] = []string{s}
		_ = clientIP(r, []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})
	})
}

func fwdReq(peer string, hdr ...string) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = peer
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Add(hdr[i], hdr[i+1])
	}
	return r
}

func TestUntrustedProxyTracking(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	var u untrustedProxies
	obs := func(at time.Time, peer string, hdr ...string) { u.observe(fwdReq(peer, hdr...), trusted, at) }

	obs(now, "10.0.0.2:1", "X-Forwarded-For", "1.2.3.4")    // trusted: not recorded
	obs(now, "192.168.1.1:1")                               // no headers: not recorded
	obs(now, "192.168.1.1:2", "X-Forwarded-For", "1.2.3.4") // recorded
	obs(now, "192.168.1.1:3", "Forwarded", "for=1.2.3.4")   // same peer, once
	obs(now, "[fd00::5]:1", "X-Real-IP", "1.2.3.4")         // IPv6 (ULA), X-Real-IP
	if got := u.list(now); len(got) != 2 {
		t.Fatalf("got %v", got)
	}

	// Entries expire after 24 hours.
	if got := u.list(now.Add(untrustedTTL - time.Second)); len(got) != 2 {
		t.Fatalf("expired early: %v", got)
	}
	obs(now.Add(time.Hour), "192.168.1.1:1", "X-Forwarded-For", "1.2.3.4") // refresh one
	got := u.list(now.Add(untrustedTTL + time.Minute))
	if len(got) != 1 || got[0].IP.String() != "192.168.1.1" {
		t.Fatalf("after expiry: %v", got)
	}
	if got := u.list(now.Add(3 * untrustedTTL)); len(got) != 0 {
		t.Fatalf("all should expire: %v", got)
	}

	// Bounded: the least recently seen peer is evicted.
	var b untrustedProxies
	for i := 0; i < untrustedMax+3; i++ {
		at := now.Add(time.Duration(i) * time.Minute)
		b.observe(fwdReq("192.168.9."+string(rune('0'+i))+":1", "X-Forwarded-For", "1.1.1.1"), trusted, at)
	}
	l := b.list(now.Add(time.Hour))
	if len(l) != untrustedMax || l[0].IP.String() != "192.168.9.7" || l[len(l)-1].IP.String() != "192.168.9.3" {
		t.Fatalf("bounded list: %v", l)
	}
}

func TestAdminHealthUntrustedProxies(t *testing.T) {
	var logs bytes.Buffer
	e := newEnv(t, func(o *Options) {
		o.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	})
	e.user("root", true)
	e.user("plain", false)
	root := e.signedInStepUp("root")
	plain := e.signedIn("plain")
	const path = "/api/v1/admin/health"

	st, v := root.do("GET", path, nil)
	expect(t, st, v, 200, "")
	if l, _ := field(v, "untrustedProxies").([]any); l == nil || len(l) != 0 {
		t.Fatalf("want empty list, got %v", v)
	}

	// A request forwarded by the (untrusted) test peer is recorded, never used as the client IP.
	root.h["X-Forwarded-For"] = "203.0.113.77"
	if st, _ := root.do("GET", "/api/v1/session", nil); st != 200 {
		t.Fatal(st)
	}
	delete(root.h, "X-Forwarded-For")
	st, v = root.do("GET", path, nil)
	expect(t, st, v, 200, "")
	l, _ := field(v, "untrustedProxies").([]any)
	if len(l) != 1 || field(l[0], "ip") != "127.0.0.1" || field(l[0], "lastSeen") == nil {
		t.Fatalf("got %v", v)
	}
	if ip := e.srv.untrusted.list(e.clock.Now())[0].IP; ip.String() != "127.0.0.1" {
		t.Fatal(ip)
	}

	// Admin only: members, anonymous callers and API tokens are refused.
	st, v = plain.do("GET", path, nil)
	expect(t, st, v, 403, "")
	st, v = e.client().do("GET", path, nil)
	expect(t, st, v, 401, "")

	// Nothing about it reaches the log.
	for _, bad := range []string{"203.0.113.77", "127.0.0.1", "untrusted", "proxies", "proxy"} {
		if strings.Contains(logs.String(), bad) {
			t.Errorf("log contains %q:\n%s", bad, logs.String())
		}
	}

	// Entries older than 24 hours disappear.
	e.clock.Add(25 * time.Hour)
	st, v = root.do("GET", path, nil)
	if l, _ := field(v, "untrustedProxies").([]any); st != 200 || len(l) != 0 {
		t.Fatalf("expired: %d %v", st, v)
	}
}

func TestAdminHealthTrustedPeerNotRecorded(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")} })
	e.user("root", true)
	root := e.signedInStepUp("root")
	root.h["X-Forwarded-For"] = "203.0.113.77"
	root.do("GET", "/api/v1/session", nil)
	st, v := root.do("GET", "/api/v1/admin/health", nil)
	if l, _ := field(v, "untrustedProxies").([]any); st != 200 || len(l) != 0 {
		t.Fatalf("trusted peer recorded: %d %v", st, v)
	}
}

func TestAdminHealthRefusesTokens(t *testing.T) {
	f := newTokenEnv(t)
	secret, _ := mkToken(t, f.aStep, []string{scRead}, f.hA)
	st, _, v := f.e.bearerDo("GET", "/api/v1/admin/health", secret, nil)
	expect(t, st, v, 403, "")
	for _, r := range f.e.srv.routes {
		if r.pattern == "GET /api/v1/admin/health" && r.policy != admin.noToken() {
			t.Errorf("policy %+v", r.policy)
		}
	}
}

// A public peer sending forwarding headers is never suggested as a proxy:
// otherwise any client could get its own address recommended for
// GUNKLY_TRUSTED_PROXIES and then spoof client IPs.
func TestUntrustedProxyIgnoresPublicPeers(t *testing.T) {
	var u untrustedProxies
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	for _, peer := range []string{"203.0.113.5:1234", "[2001:db8::1]:1234", "8.8.8.8:80"} {
		u.observe(fwdReq(peer, "X-Forwarded-For", "10.0.0.9"), nil, now)
	}
	u.observe(fwdReq("172.18.0.3:443", "X-Forwarded-For", "203.0.113.5"), nil, now)
	u.observe(fwdReq("[fd00::3]:443", "X-Forwarded-For", "203.0.113.5"), nil, now)
	got := u.list(now)
	if len(got) != 2 {
		t.Fatalf("want only the two private peers, got %+v", got)
	}
}

// A client behind a trusted proxy that maintains X-Forwarded-For (and passes
// the client's own Forwarded header through) cannot pick its address: not to
// escape the per-IP limits, and not in the audit log.
func TestClientIPForwardedSpoofIgnored(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")} })
	e.srv.rl = newRateLimiter(5, 10000, e.clock.Now)
	c := e.client()
	c.h["X-Forwarded-For"] = "203.0.113.9"
	limited := 0
	for i := 0; i < 20; i++ {
		c.h["Forwarded"] = "for=198.51.100." + strconv.Itoa(i+1)
		resp := c.raw("POST", "/api/v1/session", map[string]string{"login": "x" + strconv.Itoa(i), "password": "p"}, c.cookie(csrfCookie))
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("a spoofed Forwarded header escaped the per-IP login limit")
	}

	u := e.user("ann", false)
	a := e.client()
	a.h["X-Forwarded-For"] = "203.0.113.10"
	a.h["Forwarded"] = "for=192.0.2.66"
	if st, v := a.login("ann", nil); st != 200 {
		t.Fatalf("login %d %v", st, v)
	}
	evs, err := e.st.ListAuditForUser(context.Background(), u.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range evs {
		if ev.Event == store.AuditLoginSuccess {
			found = true
			if ev.IP != "203.0.113.10" {
				t.Fatalf("audit ip %s, want the proxy's X-Forwarded-For", ev.IP)
			}
		}
	}
	if !found {
		t.Fatal("no login audit entry")
	}
}
