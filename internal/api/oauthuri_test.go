package api

import (
	"net/url"
	"strings"
	"testing"
)

func TestRedirectURIRules(t *testing.T) {
	for uri, want := range map[string]bool{
		"https://app.example/callback":        true,
		"https://app.example:8443/cb?x=1":     true,
		"https://localhost/cb":                true,
		"http://localhost/cb":                 true,
		"http://localhost:5173/cb":            true,
		"http://127.0.0.1:1234/cb":            true,
		"http://[::1]:1234/cb":                true,
		"http://app.example/cb":               false, // plain http only on loopback
		"http://127.0.0.1.evil.example/cb":    false,
		"http://localhost.evil.example/cb":    false,
		"http://0.0.0.0/cb":                   false,
		"https://app.example/cb#frag":         false,
		"https://app.example/cb#":             false,
		"https://*.example/cb":                false,
		"https://app.example/*":               false,
		"https://u@app.example/cb":            false,
		"https://u:p@app.example/cb":          false,
		"https://app.example\\@evil.example/": false,
		"https://app.example;evil.example/cb": false,
		"https://app.example,evil.example/cb": false,
		"https://app .example/cb":             false,
		"https://münchen.example/cb":          false, // punycode only
		"https://app.example/\u0000":          false,
		"https:app.example/cb":                false,
		"https:///cb":                         false,
		"https://":                            false,
		"//app.example/cb":                    false,
		"/cb":                                 false,
		"":                                    false,
		"app.example/cb":                      false,
		"javascript:alert(1)":                 false,
		"data:text/html,x":                    false,
		"myapp://callback":                    false,
		"https://[fe80::1%25eth0]/cb":         false,
		"https://app.example:99999/cb":        false,
		"https://app.example:abc/cb":          false,
		"https://-app.example/cb":             false,
		"https://" + strings.Repeat("a", 600): false,
	} {
		if got := validRedirectURI(uri); got != want {
			t.Errorf("validRedirectURI(%q) = %v, want %v", uri, got, want)
		}
	}
}

func TestRedirectMatches(t *testing.T) {
	reg := []string{"https://app.example/cb", "http://127.0.0.1:5000/cb", "http://localhost/cb"}
	for uri, want := range map[string]bool{
		"https://app.example/cb":        true,
		"https://app.example/cb/":       false,
		"https://app.example/cb?x=1":    false,
		"https://app.example:444/cb":    false, // exact for non-loopback
		"https://APP.example/cb":        false,
		"http://127.0.0.1:5000/cb":      true,
		"http://127.0.0.1:61234/cb":     true, // RFC 8252: any port on loopback
		"http://127.0.0.1:61234/other":  false,
		"http://localhost:8080/cb":      true,
		"http://localhost/cb":           true,
		"http://[::1]:5000/cb":          false, // a different loopback host
		"https://127.0.0.1:61234/cb":    false, // scheme must match
		"https://evil.example/cb":       false,
		"http://127.0.0.1:61234/cb#x":   false,
		"http://127.0.0.1:61234/cb?a=b": false,
	} {
		if got := redirectMatches(reg, uri); got != want {
			t.Errorf("redirectMatches(%q) = %v, want %v", uri, got, want)
		}
	}
	if redirectMatches(nil, "https://app.example/cb") {
		t.Error("matched with nothing registered")
	}
}

func FuzzValidRedirectURI(f *testing.F) {
	for _, s := range []string{"https://app.example/cb", "http://127.0.0.1:1/x", "http://[::1]:80/", "https://a.b/c?d=e", "javascript:alert(1)",
		"https://a.example/#x", "https://*/", "http://localhost@evil.example/", "https://a.example\\@b/", "", "//x", "https://[::1]/"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if !validRedirectURI(raw) {
			if redirectMatches([]string{raw}, raw) {
				t.Fatalf("an invalid URI matches itself: %q", raw)
			}
			return
		}
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("accepted %q but it does not parse: %v", raw, err)
		}
		switch {
		case u.Scheme == "https":
		case u.Scheme == "http" && loopbackHost(u.Hostname()):
		default:
			t.Fatalf("accepted %q with scheme %q host %q", raw, u.Scheme, u.Hostname())
		}
		if u.Fragment != "" || u.User != nil || strings.ContainsAny(raw, "#*\\ \t\r\n;,") || len(raw) > maxRedirectLen {
			t.Fatalf("accepted %q", raw)
		}
		// What the consent page shows and the CSP names is the host the browser goes to.
		if o := redirectOrigin(raw); strings.ContainsAny(o, " ;,'\"\n") || !strings.HasPrefix(o, u.Scheme+"://") {
			t.Fatalf("origin %q of %q", o, raw)
		}
		if !redirectMatches([]string{raw}, raw) {
			t.Fatalf("a valid URI does not match itself: %q", raw)
		}
	})
}
