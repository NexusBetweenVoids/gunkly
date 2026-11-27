package api

import (
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"
)

// clientIP returns the peer address, or the first untrusted address in the
// forwarding headers (walking right to left) when the peer is a trusted
// proxy. RFC 7239 Forwarded wins over X-Forwarded-For when both are present.
// A hop that is not an IP address (obfuscated, unknown, malformed) ends the
// walk at the last trusted hop: nothing to its left can be believed.
func clientIP(r *http.Request, trusted []netip.Prefix) netip.Addr {
	addr := peerAddr(r)
	if !addr.IsValid() || !isTrusted(addr, trusted) {
		return addr
	}
	var hops []netip.Addr
	if fwd := r.Header.Values("Forwarded"); len(fwd) > 0 {
		hops = parseForwarded(fwd)
	} else {
		hops = parseXFF(r.Header.Values("X-Forwarded-For"))
	}
	for i := len(hops) - 1; i >= 0; i-- {
		if !hops[i].IsValid() {
			return addr
		}
		addr = hops[i]
		if !isTrusted(addr, trusted) {
			return addr
		}
	}
	return addr
}

// peerAddr is the address of the TCP peer (zero if unparsable).
func peerAddr(r *http.Request) netip.Addr {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}
	}
	return ap.Addr().Unmap()
}

func isTrusted(a netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// parseXFF returns one entry per X-Forwarded-For hop, left to right; an
// invalid (zero) Addr marks a hop that is not an IP address. Empty hops are
// skipped.
func parseXFF(values []string) []netip.Addr {
	var out []netip.Addr
	for _, hop := range strings.Split(strings.Join(values, ","), ",") {
		hop = strings.TrimSpace(hop)
		if hop == "" {
			continue
		}
		a, err := netip.ParseAddr(hop)
		if err != nil {
			out = append(out, netip.Addr{})
			continue
		}
		out = append(out, a.Unmap())
	}
	return out
}

// parseForwarded returns one entry per element of RFC 7239 Forwarded headers,
// left to right, holding that element's for= address without any port. An
// element whose for= is missing, obfuscated ("_hidden"), "unknown" or
// malformed yields the zero Addr. Quoted strings (needed for IPv6 and ports)
// are honoured, so a comma or semicolon inside quotes does not split.
func parseForwarded(values []string) []netip.Addr {
	var out []netip.Addr
	for _, v := range values {
		for _, elem := range splitQuoted(v, ',') {
			if strings.TrimSpace(elem) == "" {
				continue
			}
			var a netip.Addr
			for _, pair := range splitQuoted(elem, ';') {
				k, val, found := strings.Cut(pair, "=")
				if !found || !strings.EqualFold(strings.TrimSpace(k), "for") {
					continue
				}
				a = parseNode(unquote(strings.TrimSpace(val)))
				break
			}
			out = append(out, a)
		}
	}
	return out
}

// splitQuoted splits on sep outside double quotes (backslash escapes the next
// byte inside quotes).
func splitQuoted(s string, sep byte) []string {
	var parts []string
	start, inQ := 0, false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case inQ && c == '\\':
			i++
		case c == '"':
			inQ = !inQ
		case c == sep && !inQ:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

func unquote(s string) string {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return s
	}
	var b strings.Builder
	for i := 1; i < len(s)-1; i++ {
		if s[i] == '\\' && i+1 < len(s)-1 {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// parseNode reads an RFC 7239 node: IPv4, "[IPv6]", either with an optional
// ":port". Obfuscated identifiers and "unknown" give the zero Addr.
func parseNode(n string) netip.Addr {
	if ap, err := netip.ParseAddrPort(n); err == nil {
		return ap.Addr().WithZone("").Unmap()
	}
	if strings.HasPrefix(n, "[") {
		if end := strings.IndexByte(n, ']'); end > 0 {
			if rest := n[end+1:]; rest == "" || (rest[0] == ':' && rest != ":") {
				if a, err := netip.ParseAddr(n[1:end]); err == nil && a.Is6() {
					return a.WithZone("").Unmap()
				}
			}
		}
		return netip.Addr{}
	}
	host, port, found := strings.Cut(n, ":")
	if found && (port == "" || strings.Contains(port, ":")) {
		return netip.Addr{} // bare IPv6 is not valid in Forwarded; ports follow a single colon
	}
	if a, err := netip.ParseAddr(host); err == nil && a.Is4() {
		return a
	}
	return netip.Addr{}
}

const (
	untrustedMax = 5
	untrustedTTL = 24 * time.Hour
)

// untrustedProxies remembers, in memory only, the last few direct peers that
// are not trusted proxies but sent forwarding headers: a sign that Gunkly sits
// behind a reverse proxy whose address is missing from GUNKLY_TRUSTED_PROXIES.
// It is shown to admins and never logged. Peers are never trusted
// automatically.
type untrustedProxies struct {
	mu   sync.Mutex
	seen []untrustedPeer
}

type untrustedPeer struct {
	IP       netip.Addr
	LastSeen time.Time
}

func hasForwardingHeaders(r *http.Request) bool {
	h := r.Header
	return len(h.Values("X-Forwarded-For")) > 0 || len(h.Values("Forwarded")) > 0 || len(h.Values("X-Real-IP")) > 0
}

// observe records the request's peer when it is untrusted yet forwards.
func (u *untrustedProxies) observe(r *http.Request, trusted []netip.Prefix, now time.Time) {
	peer := peerAddr(r)
	if !peer.IsValid() || isTrusted(peer, trusted) || !hasForwardingHeaders(r) {
		return
	}
	// Only private, loopback and link-local peers can be a self-hosted reverse
	// proxy worth suggesting. A public peer sending forwarding headers is far
	// more likely a client trying to get its own address suggested as
	// "trusted" (which would let it spoof IPs), so it is never listed.
	if p := peer.Unmap(); !p.IsPrivate() && !p.IsLoopback() && !p.IsLinkLocalUnicast() {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.pruneLocked(now)
	for i := range u.seen {
		if u.seen[i].IP == peer {
			u.seen[i].LastSeen = now
			return
		}
	}
	if len(u.seen) >= untrustedMax { // evict the least recently seen
		oldest := 0
		for i := range u.seen {
			if u.seen[i].LastSeen.Before(u.seen[oldest].LastSeen) {
				oldest = i
			}
		}
		u.seen = append(u.seen[:oldest], u.seen[oldest+1:]...)
	}
	u.seen = append(u.seen, untrustedPeer{IP: peer, LastSeen: now})
}

func (u *untrustedProxies) pruneLocked(now time.Time) {
	kept := u.seen[:0]
	for _, p := range u.seen {
		if now.Sub(p.LastSeen) < untrustedTTL {
			kept = append(kept, p)
		}
	}
	u.seen = kept
}

// list returns the peers seen within the last 24 hours, most recent first.
func (u *untrustedProxies) list(now time.Time) []untrustedPeer {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.pruneLocked(now)
	out := append([]untrustedPeer(nil), u.seen...)
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	return out
}

type untrustedProxyView struct {
	IP       string    `json:"ip"`
	LastSeen time.Time `json:"lastSeen"`
}

// adminHealth lists the direct peers that forward client addresses but are not
// trusted proxies, so the admin panel can say what to add to
// GUNKLY_TRUSTED_PROXIES. It never trusts anything by itself.
func (s *Server) adminHealth(w http.ResponseWriter, r *http.Request, c *call) error {
	peers := s.untrusted.list(s.now())
	view := make([]untrustedProxyView, 0, len(peers))
	for _, p := range peers {
		view = append(view, untrustedProxyView{IP: p.IP.String(), LastSeen: p.LastSeen})
	}
	// Security alert events of the last 7 days (store.AlertEvents); byYou marks the viewing admin's own.
	evs, err := s.st.RecentSecurityEvents(r.Context(), s.now().Add(-7*24*time.Hour))
	if err != nil {
		return err
	}
	events := make([]map[string]any, 0, len(evs))
	for _, e := range evs {
		meta := e.Meta
		delete(meta, "scope")
		events = append(events, map[string]any{"event": e.Event, "householdId": e.HouseholdID, "username": e.Username, "meta": meta, "at": e.At, "byYou": e.ActorID != "" && e.ActorID == c.user.ID})
	}
	out := map[string]any{"untrustedProxies": view, "securityEvents": events}
	if s.emailEnabled() {
		f, err := s.st.MailFailing(r.Context(), s.now())
		if err != nil {
			return err
		}
		if f != nil {
			out["emailFailing"] = map[string]any{"since": f.Since, "lastError": f.LastError}
		}
	}
	return ok(w, out)
}
