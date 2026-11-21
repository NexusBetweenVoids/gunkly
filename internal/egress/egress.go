// Package egress provides the SSRF-guarded HTTP client and dial function used
// for every outbound request whose destination a user controls (notification
// URLs, Web Push endpoints).
//
// The guard checks the address actually being connected to, after DNS
// resolution, in a net.Dialer Control hook. Checking a hostname before the
// request would be useless: DNS can change between the check and the dial.
package egress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// Fixed limits for outbound requests.
const (
	DialTimeout    = 5 * time.Second
	RequestTimeout = 15 * time.Second
	MaxBodyBytes   = 1 << 20 // 1 MiB
	MaxRedirects   = 3
)

var (
	// ErrBlocked is returned when a connection targets a forbidden address.
	ErrBlocked = errors.New("egress: destination address not allowed")
	// ErrBodyTooLarge is returned when reading a response body past MaxBodyBytes.
	ErrBodyTooLarge = errors.New("egress: response body too large")
	// ErrRedirect is returned when a redirect breaks the redirect policy.
	ErrRedirect = errors.New("egress: redirect not allowed")
)

// Guard configures outbound request protection.
type Guard struct {
	// Enabled blocks connections to private, loopback, link-local, CGNAT,
	// metadata and other non-public addresses. Always on for the hosted
	// service; self-hosters may turn it off to reach services on their LAN.
	// Timeouts, the body cap and the redirect policy apply either way.
	Enabled bool
}

func (g Guard) dialer() *net.Dialer {
	d := &net.Dialer{Timeout: DialTimeout, KeepAlive: 30 * time.Second}
	if g.Enabled {
		d.Control = control
	}
	return d
}

// DialContext dials through the guard. Use it for non-HTTP transports
// (Shoutrrr's SetDialContext for SMTP, MQTT, XMPP).
func (g Guard) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return g.dialer().DialContext(ctx, network, address)
}

// control runs after DNS resolution, once per connection attempt, with the
// literal IP the socket is about to connect to.
func control(network, address string, _ syscall.RawConn) error {
	switch network {
	case "tcp4", "tcp6", "udp4", "udp6":
	default:
		return fmt.Errorf("%w: network %q", ErrBlocked, network)
	}
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("%w: %q", ErrBlocked, address)
	}
	if blocked(ap.Addr()) {
		return fmt.Errorf("%w: %s", ErrBlocked, ap.Addr())
	}
	return nil
}

// NewClient returns an HTTP client that dials through g, ignores proxy
// environment variables, enforces RequestTimeout, caps response bodies at
// MaxBodyBytes and follows at most MaxRedirects redirects, HTTPS only. Every
// redirected connection is dialled through the same guard.
func NewClient(g Guard) *http.Client {
	t := &http.Transport{
		Proxy:                  nil,
		DialContext:            g.DialContext,
		ForceAttemptHTTP2:      true,
		TLSHandshakeTimeout:    DialTimeout,
		ResponseHeaderTimeout:  RequestTimeout,
		ExpectContinueTimeout:  time.Second,
		IdleConnTimeout:        90 * time.Second,
		MaxIdleConns:           100,
		MaxResponseHeaderBytes: 64 << 10,
	}
	return &http.Client{
		Transport:     &capTransport{base: t},
		Timeout:       RequestTimeout,
		CheckRedirect: checkRedirect,
	}
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > MaxRedirects {
		return fmt.Errorf("%w: more than %d redirects", ErrRedirect, MaxRedirects)
	}
	if req.URL.Scheme != "https" {
		return fmt.Errorf("%w: non-HTTPS redirect", ErrRedirect)
	}
	return nil
}

// capTransport wraps response bodies so reads past MaxBodyBytes fail.
type capTransport struct {
	base *http.Transport
}

func (c *capTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := c.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	resp.Body = &capReader{rc: resp.Body, left: MaxBodyBytes}
	return resp, nil
}

type capReader struct {
	rc   io.ReadCloser
	left int64
}

func (r *capReader) Read(p []byte) (int, error) {
	if r.left < 0 {
		return 0, ErrBodyTooLarge
	}
	// Allow reading one byte past the cap to detect an oversized body.
	if int64(len(p)) > r.left+1 {
		p = p[:r.left+1]
	}
	n, err := r.rc.Read(p)
	r.left -= int64(n)
	if r.left < 0 {
		return n - 1, ErrBodyTooLarge
	}
	return n, err
}

func (r *capReader) Close() error { return r.rc.Close() }
