package auth

import (
	"net"
	"net/url"
	"strings"
)

// PasskeyRP derives the WebAuthn relying party from GUNKLY_BASE_URL (ADR
// 0018): the ID is the host without port, the origin is scheme://host[:port].
// Passkeys need a domain name on a secure origin, so it reports ok only for an
// https URL or http://localhost; an IP address is never a valid RP ID.
func PasskeyRP(baseURL string) (rpID, origin string, ok bool) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return "", "", false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || net.ParseIP(host) != nil {
		return "", "", false
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && host == "localhost":
	default:
		return "", "", false
	}
	return host, u.Scheme + "://" + strings.ToLower(u.Host), true
}
