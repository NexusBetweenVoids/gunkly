package egress

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIdPClientTrustsOnlyTheIssuerHost(t *testing.T) {
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(strings.Repeat("x", MaxBodyBytes+10)))
	}))
	defer issuer.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer other.Close()

	guard := true
	c := NewIdPClient(issuer.URL, func(context.Context) bool { return guard })
	get := func(u string) (*http.Response, error) {
		req, _ := http.NewRequest(http.MethodGet, u, nil)
		return c.Do(req)
	}

	// The issuer is on loopback, which the guard blocks for everyone else, yet it is reachable.
	resp, err := get(issuer.URL + "/x")
	if err != nil {
		t.Fatalf("issuer host: %v", err)
	}
	// ...with the body capped.
	buf := make([]byte, MaxBodyBytes+10)
	var total int
	var rerr error
	for rerr == nil {
		var n int
		n, rerr = resp.Body.Read(buf)
		total += n
	}
	resp.Body.Close()
	if !errors.Is(rerr, ErrBodyTooLarge) || total > MaxBodyBytes {
		t.Fatalf("body cap: read %d, err %v", total, rerr)
	}

	// Another loopback host is blocked while the guard is on...
	if resp, err := get(other.URL); err == nil {
		resp.Body.Close()
		t.Fatal("other host reachable with the guard on")
	} else if !errors.Is(err, ErrBlocked) {
		t.Fatalf("other host with the guard on: %v", err)
	}
	// ...and reachable when the administrator turned the guard off.
	guard = false
	if resp, err := get(other.URL); err != nil {
		t.Fatalf("other host with the guard off: %v", err)
	} else {
		resp.Body.Close()
	}

	// A redirect is returned as it is, never followed.
	resp, err = get(issuer.URL + "/redirect")
	if err != nil || resp.StatusCode != http.StatusFound {
		t.Fatalf("redirect: %v %v", resp, err)
	}
	resp.Body.Close()
}

func TestHostPort(t *testing.T) {
	for in, want := range map[string]string{
		"https://auth.example.com":        "auth.example.com:443",
		"http://localhost":                "localhost:80",
		"https://auth.example.com:9091/x": "auth.example.com:9091",
		"http://[::1]:8080":               "[::1]:8080",
	} {
		if got := hostPort(in); got != want {
			t.Errorf("hostPort(%q) = %q, want %q", in, got, want)
		}
	}
}
