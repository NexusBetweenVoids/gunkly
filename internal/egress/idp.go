package egress

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// NewIdPClient returns the HTTP client for talking to the OpenID Connect
// identity provider the administrator configured with GUNKLY_OIDC_ISSUER
// (ADR 0018).
//
// It deliberately lets requests to the issuer's own host and port skip the
// private-address block: self-hosted identity providers (Authelia, Authentik,
// Keycloak, Pocket ID, ...) usually sit on the LAN or on the same machine, and
// the address comes from the server's environment, which only the operator
// controls, not from any user input. Every other destination (a token or JWKS
// endpoint on another host that the issuer's discovery document names) still
// goes through the guard while guardOn reports true, so a hostile or hijacked
// discovery document cannot point the server at the internal network.
//
// Everything else is as strict as the other clients: no redirects at all (a 3xx
// is an error, never a hop to another host), no proxies, timeouts, and response
// bodies capped at MaxBodyBytes.
func NewIdPClient(issuer string, guardOn func(context.Context) bool) *http.Client {
	trusted := hostPort(issuer)
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		on := guardOn(ctx)
		if strings.EqualFold(address, trusted) {
			on = false
		}
		return Guard{Enabled: on}.DialContext(ctx, network, address)
	}
	c := NewNoRedirectClient(Guard{Enabled: true})
	t := c.Transport.(*capTransport).base
	t.DialContext = dial
	return c
}

// hostPort returns host:port of a URL with the scheme's default port filled in.
func hostPort(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	port := u.Port()
	if port == "" {
		port = "443"
		if u.Scheme == "http" {
			port = "80"
		}
	}
	return net.JoinHostPort(u.Hostname(), port)
}
