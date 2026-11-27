package api

import (
	"context"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

func geoEnv(t *testing.T, proxies bool) *mailEnv {
	t.Helper()
	return newMailEnv(t, func(o *Options) {
		o.GeoIP = GeoIP{CountryHeader: "CF-IPCountry", ASNHeader: "X-ASN"}
		if proxies {
			o.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
		}
	})
}

// signinFrom signs ann in on a fresh device whose proxy reports the headers.
func (e *mailEnv) signinFrom(name string, hdr map[string]string) {
	e.t.Helper()
	c := e.client()
	for k, v := range hdr {
		c.h[k] = v
	}
	if st, v := c.login(name, nil); st != 200 {
		e.t.Fatalf("login: %d %v", st, v)
	}
}

func (e *mailEnv) auditCount(name, event string) int {
	e.t.Helper()
	u, err := e.st.UserByLogin(context.Background(), name)
	if err != nil {
		e.t.Fatal(err)
	}
	evs, err := e.st.ListAuditForUser(context.Background(), u.ID, 200)
	if err != nil {
		e.t.Fatal(err)
	}
	n := 0
	for _, ev := range evs {
		if ev.Event == event {
			n++
		}
	}
	return n
}

func TestSigninAlertCountry(t *testing.T) {
	e := geoEnv(t, true)
	e.user("ann", false)
	e.verified("ann")
	de := map[string]string{"CF-IPCountry": "de"}
	e.signinFrom("ann", de) // the first sign-in: nothing to compare with
	if got := e.mails(); len(got) != 0 {
		t.Fatalf("first sign-in mailed: %v", got)
	}
	e.signinFrom("ann", de) // same country
	if got := e.mails(); len(got) != 0 {
		t.Fatalf("same country mailed: %v", got)
	}
	e.signinFrom("ann", map[string]string{"CF-IPCountry": "FR"})
	msg := e.oneMail("ann@example.com")
	for _, want := range []string{"(FR)", "UTC", "127.0.0.1", "http://app.test/settings/security", "sign out other devices"} {
		if !strings.Contains(msg.Body, want) {
			t.Errorf("body lacks %q:\n%s", want, msg.Body)
		}
	}
	if e.auditCount("ann", store.AuditSigninUnusual) != 1 {
		t.Error("no signin_unusual audit event")
	}
	// Only the country and the network flag are kept.
	u, _ := e.st.UserByLogin(context.Background(), "ann")
	evs, _ := e.st.ListAuditForUser(context.Background(), u.ID, 10)
	for _, ev := range evs {
		if ev.Event == store.AuditLoginSuccess && ev.Meta["country"] == "FR" && ev.Meta["hosting"] == "no" && len(ev.Meta) == 2 {
			goto stored
		}
	}
	t.Fatalf("login_success lacks country/hosting meta: %+v", evs)
stored:
	// A second unusual sign-in within the hour: recorded, not mailed again.
	e.signinFrom("ann", map[string]string{"CF-IPCountry": "US"})
	if got := e.mails(); len(got) != 0 {
		t.Fatalf("second email within the hour: %v", got)
	}
	if e.auditCount("ann", store.AuditSigninUnusual) != 2 {
		t.Error("second unusual sign-in not audited")
	}
	e.clock.Add(2 * time.Hour)
	e.signinFrom("ann", map[string]string{"CF-IPCountry": "IT"})
	e.oneMail("ann@example.com")
}

func TestSigninAlertHosting(t *testing.T) {
	e := geoEnv(t, true)
	e.user("ann", false)
	e.verified("ann")
	home := map[string]string{"CF-IPCountry": "DE", "X-ASN": "3320"}
	e.signinFrom("ann", home)
	e.signinFrom("ann", map[string]string{"CF-IPCountry": "DE", "X-ASN": "AS16509"})
	msg := e.oneMail("ann@example.com")
	if !strings.Contains(msg.Body, "hosting, cloud") || strings.Contains(msg.Body, "(DE)") {
		t.Errorf("body:\n%s", msg.Body)
	}
	// Once a hosting network is part of the history it is expected.
	e.clock.Add(2 * time.Hour)
	e.signinFrom("ann", map[string]string{"CF-IPCountry": "DE", "X-ASN": "14061"})
	if got := e.mails(); len(got) != 0 {
		t.Fatalf("expected hosting mailed: %v", got)
	}
	// Tor (T1) counts as an unusual network for an account that never used one.
	e2 := geoEnv(t, true)
	e2.user("bob", false)
	e2.verified("bob")
	e2.signinFrom("bob", home)
	e2.signinFrom("bob", map[string]string{"CF-IPCountry": "T1"})
	e2.oneMail("bob@example.com")
}

func TestSigninAlertHeadersNeedTrustedProxy(t *testing.T) {
	e := geoEnv(t, false) // no trusted proxy: the headers are just client input
	if e.srv.signinLocation() != "none" {
		t.Fatalf("location %q", e.srv.signinLocation())
	}
	e.user("ann", false)
	e.verified("ann")
	e.signinFrom("ann", map[string]string{"CF-IPCountry": "DE"})
	e.signinFrom("ann", map[string]string{"CF-IPCountry": "FR"})
	if got := e.mails(); len(got) != 0 {
		t.Fatalf("untrusted header believed: %v", got)
	}
	u, _ := e.st.UserByLogin(context.Background(), "ann")
	evs, _ := e.st.ListAuditForUser(context.Background(), u.ID, 10)
	for _, ev := range evs {
		if len(ev.Meta["country"]) > 0 || len(ev.Meta["hosting"]) > 0 {
			t.Fatalf("location stored without a source: %+v", ev)
		}
	}
}

func TestSigninAlertIgnoresBadHeaderValues(t *testing.T) {
	e := geoEnv(t, true)
	e.user("ann", false)
	e.verified("ann")
	e.signinFrom("ann", map[string]string{"CF-IPCountry": "DE"})
	for _, v := range []string{"XX", "", "Germany", "1A", "<b>"} {
		e.signinFrom("ann", map[string]string{"CF-IPCountry": v, "X-ASN": "bogus"})
	}
	if got := e.mails(); len(got) != 0 {
		t.Fatalf("junk header mailed: %v", got)
	}
}

func TestSigninAlertUserSettingOff(t *testing.T) {
	e := geoEnv(t, true)
	e.user("ann", false)
	e.verified("ann")
	c := e.signedIn("ann")
	st, v := c.do("PATCH", "/api/v1/me", map[string]any{"signinAlerts": false})
	expect(t, st, v, 200, "")
	if field(v, "signinAlerts") != false {
		t.Fatalf("me: %v", v)
	}
	e.signinFrom("ann", map[string]string{"CF-IPCountry": "DE"})
	e.signinFrom("ann", map[string]string{"CF-IPCountry": "FR"})
	if got := e.mails(); len(got) != 0 {
		t.Fatalf("mailed with the setting off: %v", got)
	}
	if e.auditCount("ann", store.AuditSigninUnusual) != 1 {
		t.Error("the event is still recorded")
	}
}

func TestSigninAlertNeedsConfirmedEmail(t *testing.T) {
	e := geoEnv(t, true)
	e.user("ann", false) // address not confirmed
	e.signinFrom("ann", map[string]string{"CF-IPCountry": "DE"})
	e.signinFrom("ann", map[string]string{"CF-IPCountry": "FR"})
	if got := e.mails(); len(got) != 0 {
		t.Fatalf("mailed an unconfirmed address: %v", got)
	}
}

func TestSigninAlertDefaultOnAndInstanceFlag(t *testing.T) {
	e := geoEnv(t, true)
	e.user("ann", false)
	c := e.signedIn("ann")
	st, v := c.do("GET", "/api/v1/me", nil)
	if st != 200 || field(v, "signinAlerts") != true {
		t.Fatalf("default: %d %v", st, v)
	}
	if _, v := e.client().do("GET", "/api/v1/instance", nil); field(v, "signinAlerts") != true {
		t.Errorf("instance: %v", v)
	}
	e2 := newMailEnv(t)
	if _, v := e2.client().do("GET", "/api/v1/instance", nil); field(v, "signinAlerts") != nil {
		t.Errorf("instance without a source: %v", v)
	}
	// The admin page names the source.
	e.user("root", true)
	root := e.signedInStepUp("root")
	if _, v := root.do("GET", "/api/v1/admin/settings", nil); field(v, "signinLocation") != "header" {
		t.Errorf("admin: %v", v)
	}
}

func TestUnusualSignin(t *testing.T) {
	de := store.SigninPlace{Country: "DE", Hosting: "no"}
	cases := []struct {
		name         string
		hist         []store.SigninPlace
		p            place
		country, net bool
	}{
		{"no history", nil, place{country: "DE", known: true}, false, false},
		{"history without places", []store.SigninPlace{{}}, place{country: "FR", known: true}, false, false},
		{"same", []store.SigninPlace{de}, place{country: "DE", known: true}, false, false},
		{"one of several", []store.SigninPlace{de, {Country: "FR", Hosting: "no"}}, place{country: "FR", known: true}, false, false},
		{"new", []store.SigninPlace{de}, place{country: "FR", known: true}, true, false},
		{"hosting new", []store.SigninPlace{de}, place{country: "DE", hosting: true, known: true}, false, true},
		{"hosting known", []store.SigninPlace{de, {Country: "DE", Hosting: "yes"}}, place{country: "DE", hosting: true, known: true}, false, false},
		{"both", []store.SigninPlace{de}, place{country: "NL", hosting: true, known: true}, true, true},
		{"no country info", []store.SigninPlace{{Hosting: "no"}}, place{hosting: true, known: true}, false, true},
	}
	for _, c := range cases {
		if n, h := unusualSignin(c.hist, c.p); n != c.country || h != c.net {
			t.Errorf("%s: got %v %v", c.name, n, h)
		}
	}
}
