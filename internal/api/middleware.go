package api

import (
	"context"
	"crypto/subtle"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxBodyBytes = 1 << 20 // 1 MiB

const csp = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; " +
	"manifest-src 'self'; worker-src 'self'; font-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// reqInfo is per-request state shared between the outer middleware and the
// route wrapper (which fills in the matched pattern for the access log).
type reqInfo struct {
	ip      netip.Addr
	pattern string
}

type infoKey struct{}

func infoFrom(r *http.Request) *reqInfo {
	if i, ok := r.Context().Value(infoKey{}).(*reqInfo); ok {
		return i
	}
	return &reqInfo{}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// bodyLimit is the request body cap for r: the default, or the raised cap of
// an upload route.
func (s *Server) bodyLimit(r *http.Request) int64 {
	if r.Method == http.MethodPost {
		if _, pattern := s.mux.Handler(r); s.bodyLimits[pattern] > 0 {
			return s.bodyLimits[pattern]
		}
	}
	return maxBodyBytes
}

// middleware wraps every response: security headers, client IP, body cap,
// panic recovery and the access log.
func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := s.now()
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=(), interest-cohort=()")
		if s.secure {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		info := &reqInfo{ip: clientIP(r, s.proxies)}
		r = r.WithContext(context.WithValue(r.Context(), infoKey{}, info))
		r.Body = http.MaxBytesReader(w, r.Body, s.bodyLimit(r))
		sw := &statusWriter{ResponseWriter: w}
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler { //nolint:errorlint // sentinel compared as panic value
					panic(v)
				}
				s.log.Error("panic", "route", info.pattern, "panic", v)
				if sw.status == 0 {
					writeError(sw, http.StatusInternalServerError, "internal")
				}
			}
			pattern := info.pattern
			if pattern == "" {
				pattern = "unmatched"
			}
			// Path templates only: never raw paths, query strings or tokens.
			s.log.Info("http", "method", r.Method, "route", pattern, "status", sw.status,
				"ms", s.now().Sub(start).Milliseconds())
			if s.metrics != nil {
				s.metrics.ObserveRequest(pattern, sw.status, s.now().Sub(start))
			}
		}()
		next.ServeHTTP(sw, r)
	})
}

// clientIP returns the peer address, or the first untrusted address in
// X-Forwarded-For (walking right to left) when the peer is a trusted proxy.
func clientIP(r *http.Request, trusted []netip.Prefix) netip.Addr {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}
	}
	addr := ap.Addr().Unmap()
	if !isTrusted(addr, trusted) {
		return addr
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop := strings.TrimSpace(hops[i])
		if hop == "" {
			continue
		}
		a, err := netip.ParseAddr(hop)
		if err != nil {
			return addr // malformed: stop at the last trusted hop
		}
		addr = a.Unmap()
		if !isTrusted(addr, trusted) {
			return addr
		}
	}
	return addr
}

func isTrusted(a netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// csrfOK implements double-submit plus a cross-site request check: the
// gunkly_csrf cookie must equal the X-CSRF-Token header, and browsers'
// Sec-Fetch-Site / Origin must not indicate another site.
func (s *Server) csrfOK(r *http.Request) bool {
	if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" && sfs != "same-origin" && sfs != "none" {
		return false
	}
	if o := r.Header.Get("Origin"); o != "" && !s.sameOrigin(o, r) {
		return false
	}
	c, err := r.Cookie(csrfCookie)
	if err != nil || len(c.Value) < 32 {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(r.Header.Get("X-CSRF-Token"))) == 1
}

// csrfError picks the error for a rejected unsafe request. The common
// misconfiguration — opening Gunkly at an address other than GUNKLY_BASE_URL,
// or over http when it is https (Secure cookies are then dropped) — gets its
// own code and a rate-limited warning naming both addresses, so admins are
// not left with a generic "session expired".
func (s *Server) csrfError(r *http.Request) error {
	if s.baseURL == "" {
		return errCSRF
	}
	used := r.Header.Get("Origin")
	_, noCookie := r.Cookie(csrfCookie)
	switch {
	case used != "" && used != "null" && !strings.EqualFold(used, s.baseURL):
	case noCookie != nil && strings.HasPrefix(s.baseURL, "https://") && r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https":
		used = "http://" + r.Host
	default:
		return errCSRF
	}
	if now := time.Now().Unix(); s.baseURLWarned.Swap(now) < now-60 {
		s.log.Warn("request from an address that does not match GUNKLY_BASE_URL; set GUNKLY_BASE_URL to the exact address you open Gunkly at",
			"opened_at", logSafe(used), "base_url", s.baseURL)
	}
	return errBaseURL
}

// logSafe renders a client-supplied value for logs: ASCII-quoted (control
// characters escaped) and capped, so it can never forge or flood log lines.
func logSafe(v string) string {
	if len(v) > 200 {
		v = v[:200]
	}
	v = strings.ReplaceAll(strings.ReplaceAll(v, "\n", ""), "\r", "") // explicit for scanners; QuoteToASCII escapes them anyway
	return strconv.QuoteToASCII(v)
}

// requestOK is the CSRF gate for unsafe methods. Session routes need the
// double-submit cookie plus the cross-site check. Token routes (mark-done
// links) have no cookie to submit and nothing ambient for an attacker to
// ride: the capability is the unguessable URL token itself, which a
// cross-site page cannot know. They only get tokenRequestOK, so a
// browser never submits them from another site.
func (s *Server) requestOK(p policy, r *http.Request) bool {
	if p.tokenAuth {
		return s.tokenRequestOK(r)
	}
	return s.csrfOK(r)
}

// tokenRequestOK rejects browser requests initiated from another site.
// Sec-Fetch-Site is authoritative when present ("same-origin" or "none" pass). Otherwise a
// present Origin must match this server; "null" is tolerated there because
// Referrer-Policy: no-referrer (which every page here sends) makes some
// browsers serialise a same-origin form POST's Origin as "null". Requests
// with neither header (curl, old clients) pass: they are not browser-ambient.
func (s *Server) tokenRequestOK(r *http.Request) bool {
	if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" {
		return sfs == "same-origin" || sfs == "none"
	}
	o := r.Header.Get("Origin")
	return o == "" || o == "null" || s.sameOrigin(o, r)
}

func (s *Server) sameOrigin(origin string, r *http.Request) bool {
	if s.baseURL != "" {
		return strings.EqualFold(origin, s.baseURL)
	}
	u, err := url.Parse(origin)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && strings.EqualFold(u.Host, r.Host)
}

// allow consumes a rate-limit token for key, writing 429 if denied.
func (s *Server) allow(w http.ResponseWriter, key string) bool {
	wait := s.rl.allow(key)
	if wait <= 0 {
		return true
	}
	secs := int((wait + time.Second - 1) / time.Second)
	w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
	writeError(w, http.StatusTooManyRequests, "rate_limited")
	return false
}
