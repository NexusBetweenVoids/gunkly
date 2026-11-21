package egress

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
)

// ErrPushEndpoint is returned for a Web Push endpoint outside the allowlist.
var ErrPushEndpoint = errors.New("egress: web push endpoint not allowed")

const maxPushEndpointLen = 2048

var pushHosts = map[string]bool{
	"fcm.googleapis.com":                true,
	"web.push.apple.com":                true,
	"updates.push.services.mozilla.com": true,
}

const pushSuffix = ".notify.windows.com"

// WebPushAllowed checks a browser push subscription endpoint against the
// provider allowlist: https only, port 443, no userinfo, no IP literals, and
// a host that is one of the standard push services or a subdomain of
// notify.windows.com. It complements, never replaces, the dial-time guard.
func WebPushAllowed(endpoint string) error {
	if len(endpoint) > maxPushEndpointLen {
		return fmt.Errorf("%w: too long", ErrPushEndpoint)
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("%w: unparseable", ErrPushEndpoint)
	}
	if u.Scheme != "https" || u.Opaque != "" {
		return fmt.Errorf("%w: not https", ErrPushEndpoint)
	}
	if u.User != nil {
		return fmt.Errorf("%w: userinfo", ErrPushEndpoint)
	}
	if p := u.Port(); p != "" && p != "443" {
		return fmt.Errorf("%w: port", ErrPushEndpoint)
	}
	host := u.Hostname()
	if _, err := netip.ParseAddr(host); err == nil {
		return fmt.Errorf("%w: IP literal", ErrPushEndpoint)
	}
	if !validHostname(host) {
		return fmt.Errorf("%w: host", ErrPushEndpoint)
	}
	if pushHosts[host] {
		return nil
	}
	if sub, ok := strings.CutSuffix(host, pushSuffix); ok && sub != "" {
		return nil
	}
	return fmt.Errorf("%w: host", ErrPushEndpoint)
}

// validHostname accepts lower-case LDH labels separated by single dots, with
// no leading or trailing dot. Upper case is rejected rather than folded so the
// checked string is exactly what will be dialled.
func validHostname(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	for label := range strings.SplitSeq(h, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}
	return true
}
