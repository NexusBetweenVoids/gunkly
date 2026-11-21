package egress

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
)

func TestBlocked(t *testing.T) {
	blockedAddrs := []string{
		// IPv4
		"0.0.0.0", "0.1.2.3", "10.0.0.1", "10.255.255.255", "100.64.0.1", "100.100.100.200",
		"100.127.255.255", "127.0.0.1", "127.255.255.254", "169.254.0.1", "169.254.169.254",
		"172.16.0.1", "172.31.255.255", "192.0.0.1", "192.0.2.1", "192.88.99.1", "192.168.1.1",
		"198.18.0.1", "198.19.255.255", "198.51.100.1", "203.0.113.1", "224.0.0.1",
		"239.255.255.250", "240.0.0.1", "255.255.255.255",
		// IPv6
		"::", "::1", "::127.0.0.1", "::a00:1", "fc00::1", "fd00::1", "fd00:ec2::254",
		"fe80::1", "fe80::1%eth0", "febf::1", "fec0::1", "ff02::1", "ff05::1:3", "100::1",
		"2001:db8::1", "3fff::1", "2001:2::1", "2001:10::1", "2001:20::1", "64:ff9b:1::1",
		"2001:0:4136:e378:8000:63bf:3fff:fdd2", // Teredo
		// IPv4-mapped forms
		"::ffff:127.0.0.1", "::ffff:7f00:1", "::ffff:10.0.0.1", "::ffff:169.254.169.254",
		"::ffff:100.64.0.1", "::ffff:0.0.0.0", "::ffff:255.255.255.255", "::ffff:192.168.0.1",
		"::ffff:0:127.0.0.1", // IPv4-translated
		// NAT64 embedding non-public IPv4
		"64:ff9b::127.0.0.1", "64:ff9b::a9fe:a9fe", "64:ff9b::10.1.2.3", "64:ff9b::0.0.0.0",
		// 6to4 embedding non-public IPv4
		"2002:7f00:1::", "2002:a9fe:a9fe::1", "2002:c0a8:101::1", "2002:a00:1::",
	}
	allowed := []string{
		"1.1.1.1", "8.8.8.8", "93.184.215.14", "100.63.255.255", "100.128.0.0",
		"172.15.255.255", "172.32.0.0", "192.169.0.1", "198.17.255.255", "198.20.0.0",
		"223.255.255.255", "169.253.255.255", "11.0.0.1",
		"2606:4700:4700::1111", "2a00:1450:4001:80b::200a", "::ffff:8.8.8.8",
		"64:ff9b::8.8.8.8", "2002:808:808::1", "2001:4860:4860::8888",
	}
	for _, s := range blockedAddrs {
		if !blocked(netip.MustParseAddr(s)) {
			t.Errorf("%s: want blocked", s)
		}
	}
	for _, s := range allowed {
		if blocked(netip.MustParseAddr(s)) {
			t.Errorf("%s: want allowed", s)
		}
	}
	if !blocked(netip.Addr{}) {
		t.Error("zero Addr: want blocked")
	}
}

// Decimal, octal and hex IPv4 spellings are not IP literals to Go's parsers,
// so they can only reach the dialer as hostnames, which DNS must resolve to a
// real address that the Control hook then checks.
func TestLiteralTricksDoNotParse(t *testing.T) {
	for _, s := range []string{"2130706433", "0177.0.0.1", "0x7f.0.0.1", "0x7f000001", "127.1", "017700000001", "127.0.0.01"} {
		if ip := net.ParseIP(s); ip != nil {
			t.Errorf("net.ParseIP(%q) = %v, want nil", s, ip)
		}
		if _, err := netip.ParseAddr(s); err == nil {
			t.Errorf("netip.ParseAddr(%q) succeeded", s)
		}
	}
}

func TestControl(t *testing.T) {
	cases := []struct {
		network, addr string
		ok            bool
	}{
		{"tcp4", "8.8.8.8:443", true},
		{"tcp6", "[2606:4700:4700::1111]:443", true},
		{"udp4", "1.1.1.1:53", true},
		{"tcp4", "127.0.0.1:80", false},
		{"tcp6", "[::ffff:127.0.0.1]:80", false},
		{"tcp6", "[fe80::1%eth0]:80", false},
		{"unix", "/run/docker.sock", false},
		{"tcp4", "localhost:80", false},
		{"tcp4", "garbage", false},
	}
	for _, c := range cases {
		err := control(c.network, c.addr, nil)
		if (err == nil) != c.ok {
			t.Errorf("control(%q, %q) = %v, want ok=%v", c.network, c.addr, err, c.ok)
		}
		if err != nil && !errors.Is(err, ErrBlocked) {
			t.Errorf("control(%q, %q) error %v does not wrap ErrBlocked", c.network, c.addr, err)
		}
	}
}

func TestLoopbackServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())

	urls := []string{
		srv.URL,
		"http://localhost:" + port, // hostname resolving to loopback: the hook sees the real IP
		"http://[::ffff:127.0.0.1]:" + port,
	}
	for _, u := range urls {
		resp, err := NewClient(Guard{Enabled: true}).Get(u)
		if err == nil {
			_ = resp.Body.Close()
		}
		if !errors.Is(err, ErrBlocked) {
			t.Errorf("enabled guard, GET %s: err = %v, want ErrBlocked", u, err)
		}
	}

	resp, err := NewClient(Guard{Enabled: false}).Get(srv.URL)
	if err != nil {
		t.Fatalf("disabled guard: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "ok" {
		t.Fatalf("body = %q", body)
	}
}

// A resolver that maps any name to 127.0.0.1 stands in for DNS rebinding:
// whatever the name resolved to at validation time, the dial sees loopback.
func TestRebindingResolverBlocked(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())

	dns := startFakeDNS(t, [4]byte{127, 0, 0, 1})
	d := Guard{Enabled: true}.dialer()
	d.Resolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return net.Dial("udp", dns)
	}}
	_, err := d.DialContext(context.Background(), "tcp4", "push.example.com:"+port)
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v, want ErrBlocked", err)
	}

	// Sanity check: the same resolver without the guard reaches the server.
	d.Control = nil
	c, err := d.DialContext(context.Background(), "tcp4", "push.example.com:"+port)
	if err != nil {
		t.Fatalf("unguarded dial: %v", err)
	}
	c.Close()
}

// startFakeDNS answers every A query with ip.
func startFakeDNS(t *testing.T, ip [4]byte) string {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 512)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if n < 12 {
				continue
			}
			q := buf[:n]
			// Find the end of the first question (QNAME labels, QTYPE, QCLASS).
			end := 12
			for end < n && q[end] != 0 {
				end += int(q[end]) + 1
			}
			end += 5
			if end > n {
				continue
			}
			isA := q[end-4] == 0 && q[end-3] == 1
			// Header: same ID, QR=1 RD RA, one question, one answer for A queries.
			resp := append([]byte{}, q[:2]...)
			resp = append(resp, 0x81, 0x80, 0, 1, 0, 0, 0, 0, 0, 0)
			resp = append(resp, q[12:end]...)
			if isA {
				resp[7] = 1
				resp = append(resp, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4)
				resp = append(resp, ip[:]...)
			}
			_, _ = pc.WriteTo(resp, addr)
		}
	}()
	return pc.LocalAddr().String()
}

// tlsClient returns an unguarded client that trusts srv's certificate, so
// redirect and body-cap behaviour can be tested against local servers.
func tlsClient(srv *httptest.Server) *http.Client {
	c := NewClient(Guard{})
	c.Transport.(*capTransport).base.TLSClientConfig = &tls.Config{RootCAs: srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs}
	return c
}

func TestRedirects(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/http":
			http.Redirect(w, r, "http://"+r.Host+"/done", http.StatusFound)
		case strings.HasPrefix(r.URL.Path, "/hop/"):
			n, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/hop/"))
			if n == 0 {
				_, _ = io.WriteString(w, "done")
				return
			}
			http.Redirect(w, r, fmt.Sprintf("%s/hop/%d", srv.URL, n-1), http.StatusFound)
		}
	}))
	defer srv.Close()
	c := tlsClient(srv)

	resp, err := c.Get(srv.URL + "/http")
	if err == nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, ErrRedirect) {
		t.Errorf("redirect to http: err = %v, want ErrRedirect", err)
	}
	resp, err = c.Get(srv.URL + fmt.Sprintf("/hop/%d", MaxRedirects))
	if err != nil {
		t.Fatalf("%d redirects: %v", MaxRedirects, err)
	}
	_ = resp.Body.Close()
	resp, err = c.Get(srv.URL + fmt.Sprintf("/hop/%d", MaxRedirects+1))
	if err == nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, ErrRedirect) {
		t.Errorf("%d redirects: err = %v, want ErrRedirect", MaxRedirects+1, err)
	}
}

func TestRedirectToBlockedAddress(t *testing.T) {
	// Every hop, including redirects, dials through the transport, so it is
	// enough to prove the transport's dialer is the guarded one.
	c := NewClient(Guard{Enabled: true})
	tr := c.Transport.(*capTransport).base
	if _, err := tr.DialContext(context.Background(), "tcp", "127.0.0.1:443"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("transport dial: err = %v, want ErrBlocked", err)
	}
	if tr.Proxy != nil {
		t.Fatal("transport must not use a proxy")
	}
}

func TestBodyCap(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("n"))
		_, _ = w.Write(make([]byte, n))
	}))
	defer srv.Close()
	c := tlsClient(srv)

	for _, tc := range []struct {
		n      int
		tooBig bool
	}{{0, false}, {MaxBodyBytes, false}, {MaxBodyBytes + 1, true}, {3 * MaxBodyBytes, true}} {
		resp, err := c.Get(fmt.Sprintf("%s/?n=%d", srv.URL, tc.n))
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if tc.tooBig {
			if !errors.Is(err, ErrBodyTooLarge) {
				t.Errorf("n=%d: err = %v, want ErrBodyTooLarge", tc.n, err)
			}
			if len(b) > MaxBodyBytes {
				t.Errorf("n=%d: read %d bytes, more than cap", tc.n, len(b))
			}
		} else if err != nil || len(b) != tc.n {
			t.Errorf("n=%d: read %d bytes, err %v", tc.n, len(b), err)
		}
	}
}

func TestWebPushAllowed(t *testing.T) {
	ok := []string{
		"https://fcm.googleapis.com/fcm/send/abc:def",
		"https://web.push.apple.com/QGx",
		"https://updates.push.services.mozilla.com/wpush/v2/gAAA",
		"https://wns2-par02p.notify.windows.com/w/?token=BQYAAA",
		"https://a.b.notify.windows.com/w/",
		"https://fcm.googleapis.com:443/fcm/send/x",
		"HTTPS://fcm.googleapis.com/x", // scheme is case-insensitive
	}
	bad := []string{
		"",
		"http://fcm.googleapis.com/fcm/send/x",
		"https://fcm.googleapis.com:8443/x",
		"https://user@fcm.googleapis.com/x",
		"https://user:pw@fcm.googleapis.com/x",
		"https://fcm.googleapis.com@evil.com/x",
		"https://evil.com#@fcm.googleapis.com/x",
		"https://fcm.googleapis.com.evil.com/x",
		"https://evilfcm.googleapis.com/x",
		"https://notify.windows.com/x",
		"https://evilnotify.windows.com/x",
		"https://.notify.windows.com/x",
		"https://a..notify.windows.com/x",
		"https://notify.windows.com.evil.com/x",
		"https://fcm.googleapis.com./x",
		"https://FCM.googleapis.com/x",
		"https://127.0.0.1/x",
		"https://[::1]/x",
		"https://[fd00:ec2::254]/x",
		"https://fcm.googleapis.com\\@evil.com/x",
		"https:fcm.googleapis.com/x",
		"//fcm.googleapis.com/x",
		"fcm.googleapis.com/x",
		"https://fcm%2egoogleapis.com/x",
		"https://xn--fcm-googleapis.com/x",
		"https://fcm.googleapis.com/" + strings.Repeat("a", maxPushEndpointLen),
	}
	for _, s := range ok {
		if err := WebPushAllowed(s); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
	for _, s := range bad {
		if err := WebPushAllowed(s); !errors.Is(err, ErrPushEndpoint) {
			t.Errorf("%q: err = %v, want ErrPushEndpoint", s, err)
		}
	}
}
