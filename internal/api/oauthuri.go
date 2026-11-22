package api

import (
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Redirect URI rules for dynamically registered OAuth clients (ADR 0016,
// RFC 8252): https, or http on a loopback address (any port); never a
// fragment, user info, wildcard, or a host with anything but letters, digits,
// dots and hyphens (or a bracketed IPv6 literal). Hosts that are not ASCII
// must be given in punycode, so what the consent page shows is what the
// browser is sent to.

const (
	maxRedirectURIs = 5
	maxRedirectLen  = 512
)

// validRedirectURI reports whether raw may be registered as a redirect URI.
func validRedirectURI(raw string) bool {
	if raw == "" || len(raw) > maxRedirectLen {
		return false
	}
	for i := 0; i < len(raw); i++ {
		if c := raw[i]; c <= ' ' || c >= 0x7f || c == '\\' || c == '*' || c == '#' {
			return false
		}
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Opaque != "" || u.User != nil || u.Fragment != "" || u.RawFragment != "" || u.Host == "" {
		return false
	}
	if !validHost(u.Hostname()) {
		return false
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return false
		}
	} else if strings.HasSuffix(u.Host, ":") { // "host:" with an empty port
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		return loopbackHost(u.Hostname())
	}
	return false
}

func loopbackHost(h string) bool {
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

func validHost(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	if strings.Contains(h, ":") { // IPv6 literal (url.Hostname strips the brackets)
		ip := net.ParseIP(h)
		return ip != nil
	}
	for i := 0; i < len(h); i++ {
		c := h[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '.' && c != '-' {
			return false
		}
	}
	return !strings.HasPrefix(h, ".") && !strings.HasPrefix(h, "-")
}

// redirectMatches reports whether got is one of the registered URIs: an exact
// string match, except that a loopback http URI also matches on another port
// (RFC 8252 section 7.3: native apps pick a free port at run time).
func redirectMatches(registered []string, got string) bool {
	if !validRedirectURI(got) {
		return false
	}
	gu, _ := url.Parse(got)
	for _, r := range registered {
		if r == got {
			return true
		}
		ru, err := url.Parse(r)
		if err != nil || ru.Scheme != "http" || gu.Scheme != "http" || !loopbackHost(ru.Hostname()) {
			continue
		}
		if ru.Hostname() == gu.Hostname() && ru.EscapedPath() == gu.EscapedPath() && ru.RawQuery == gu.RawQuery {
			return true
		}
	}
	return false
}

// redirectOrigin is scheme://host[:port] of a validated redirect URI, the
// form-action source the consent page needs so browsers follow the 303 back to
// the client.
func redirectOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
