package egress

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// Addresses an admin-configured endpoint may never reach, even though private
// and loopback addresses are allowed for it: link-local (which includes the
// cloud metadata service at 169.254.169.254), the other known metadata
// addresses, multicast, broadcast and the unspecified address.
var lanBlocked = prefixes(
	"0.0.0.0/8",
	"169.254.0.0/16",
	"100.100.100.200/32", // Alibaba Cloud metadata
	"224.0.0.0/4",
	"240.0.0.0/4",
	"::/128",
	"fe80::/10",
	"fd00:ec2::254/128", // AWS metadata over IPv6
	"ff00::/8",
)

// ipv4Compatible is the deprecated IPv4-compatible IPv6 range (::a.b.c.d).
var ipv4Compatible = netip.MustParsePrefix("::/96")

// lanAllowed reports whether an admin-configured endpoint may connect to a.
// IPv6 forms that embed an IPv4 address (mapped, NAT64, 6to4,
// IPv4-compatible) are judged by the embedded address, as in blocked.
func lanAllowed(a netip.Addr) bool {
	if !a.IsValid() {
		return false
	}
	a = a.WithZone("").Unmap()
	if v4, ok := embedded4(a); ok {
		a = v4
	} else if ipv4Compatible.Contains(a) && a != netip.IPv6Unspecified() && a != netip.IPv6Loopback() {
		b := a.As16()
		a = netip.AddrFrom4([4]byte(b[12:16]))
	}
	return !inAny(a, lanBlocked)
}

func lanControl(network, address string, _ syscall.RawConn) error {
	switch network {
	case "tcp4", "tcp6":
	default:
		return fmt.Errorf("%w: network %q", ErrBlocked, network)
	}
	ap, err := netip.ParseAddrPort(address)
	if err != nil || !lanAllowed(ap.Addr()) {
		return fmt.Errorf("%w: %q", ErrBlocked, address)
	}
	return nil
}

// NewAdminEndpointClient is for a URL that only an instance admin sets, such as
// the AI endpoint: a service on the admin's own network (a local model) is a
// normal target, so private and loopback addresses are allowed. Link-local and
// cloud metadata addresses stay blocked, so a stolen admin session cannot read
// the host's cloud credentials through it. Proxy variables are ignored,
// redirects are never followed (the 3xx is an error), bodies are capped at
// MaxBodyBytes and the whole request is bounded by timeout.
func NewAdminEndpointClient(timeout time.Duration) *http.Client {
	d := &net.Dialer{Timeout: DialTimeout, KeepAlive: 30 * time.Second, Control: lanControl}
	t := &http.Transport{
		Proxy:                  nil,
		DialContext:            func(ctx context.Context, n, a string) (net.Conn, error) { return d.DialContext(ctx, n, a) },
		ForceAttemptHTTP2:      true,
		TLSHandshakeTimeout:    DialTimeout,
		ResponseHeaderTimeout:  timeout,
		ExpectContinueTimeout:  time.Second,
		IdleConnTimeout:        90 * time.Second,
		MaxIdleConns:           10,
		MaxResponseHeaderBytes: 64 << 10,
	}
	return &http.Client{
		Transport: &capTransport{base: t},
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return fmt.Errorf("%w: the endpoint answered with a redirect", ErrRedirect)
		},
	}
}
