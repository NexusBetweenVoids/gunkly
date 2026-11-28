package api

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// acctGuard counts failed password and second-factor attempts per account
// across all client IPs, so credential guessing spread over many addresses
// still slows down. After freeFailures misses each further miss doubles the
// delay, capped at maxBackoff; it never locks an account for longer than that
// and a success resets it. In memory and per instance (ADR 0013).
type acctGuard struct {
	mu   sync.Mutex
	now  func() time.Time
	max  int
	m    map[string]*acctFails
	free int
}

type acctFails struct {
	n     int
	until time.Time
	last  time.Time
}

const freeFailures = 5

func newAcctGuard(now func() time.Time, maxKeys int) *acctGuard {
	return &acctGuard{now: now, max: maxKeys, free: freeFailures, m: map[string]*acctFails{}}
}

// wait returns how long key must still wait, or 0.
func (g *acctGuard) wait(key string) time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	if f := g.m[key]; f != nil {
		if d := f.until.Sub(g.now()); d > 0 {
			return d
		}
	}
	return 0
}

// fail records a failed attempt. When the table is full of recent keys it
// makes room by forgetting the least recently failed unknown login ("l:"
// keys: anyone can make those up), and only for a real account's key, when no
// unknown login is left, the least recently failed entry of any kind. So made-up
// logins can never push real accounts out or keep them from being counted.
func (g *acctGuard) fail(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	f := g.m[key]
	if f == nil {
		if len(g.m) >= g.max {
			for k, v := range g.m {
				if now.Sub(v.last) > 2*maxBackoff {
					delete(g.m, k)
				}
			}
		}
		if len(g.m) >= g.max && !g.evictLocked(!strings.HasPrefix(key, "l:")) {
			return
		}
		f = &acctFails{}
		g.m[key] = f
	}
	if now.Sub(f.last) > 2*maxBackoff {
		f.n = 0 // a long quiet period forgives earlier misses
	}
	f.n++
	f.last = now
	if f.n > g.free {
		d := time.Second
		for i := g.free + 1; i < f.n && d < maxBackoff; i++ {
			d *= 2
		}
		f.until = now.Add(min(d, maxBackoff))
	}
}

// evictLocked drops the least recently failed unknown-login entry, or,
// with orAny, the least recently failed entry of any kind when there is none. It
// reports whether it made room.
func (g *acctGuard) evictLocked(orAny bool) bool {
	var oldest, oldestAny string
	for k, v := range g.m {
		if strings.HasPrefix(k, "l:") && (oldest == "" || v.last.Before(g.m[oldest].last)) {
			oldest = k
		}
		if oldestAny == "" || v.last.Before(g.m[oldestAny].last) {
			oldestAny = k
		}
	}
	if oldest == "" && orAny {
		oldest = oldestAny
	}
	if oldest == "" {
		return false
	}
	delete(g.m, oldest)
	return true
}

// reset forgets key after a success.
func (g *acctGuard) reset(key string) {
	g.mu.Lock()
	delete(g.m, key)
	g.mu.Unlock()
}

// acctDelay writes 429 with Retry-After if key is waiting and reports whether
// the request was refused.
func (s *Server) acctDelay(w http.ResponseWriter, key string) bool {
	d := s.acct.wait(key)
	if d <= 0 {
		return false
	}
	w.Header().Set("Retry-After", strconv.Itoa(max(int((d+time.Second-1)/time.Second), 1)))
	writeError(w, http.StatusTooManyRequests, "rate_limited")
	return true
}
