package api

import (
	"sync"
	"time"
)

// rateLimiter is an in-memory token bucket per key with exponential backoff
// for keys that keep hitting the limit. It never locks anything for longer
// than maxBackoff, so a third party cannot lock a user out. Memory is
// bounded by maxKeys. Single-instance only: a shared Postgres limiter for
// multiple instances is 1B (ADR 0013).
type rateLimiter struct {
	mu      sync.Mutex
	rate    float64 // tokens per second
	burst   float64
	maxKeys int
	now     func() time.Time
	m       map[string]*bucket
}

type bucket struct {
	tokens  float64
	last    time.Time
	strikes int       // consecutive denials; doubles the backoff
	until   time.Time // denied until
}

const maxBackoff = 15 * time.Minute

func newRateLimiter(perMinute, maxKeys int, now func() time.Time) *rateLimiter {
	return &rateLimiter{rate: float64(perMinute) / 60, burst: float64(perMinute), maxKeys: maxKeys, now: now, m: map[string]*bucket{}}
}

// allow takes a token for key. It returns 0 if allowed, otherwise how long
// the caller should wait.
func (l *rateLimiter) allow(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b := l.m[key]
	if b == nil {
		if len(l.m) >= l.maxKeys {
			l.evict(now)
		}
		b = &bucket{tokens: l.burst, last: now}
		l.m[key] = b
	}
	if now.Before(b.until) {
		return b.until.Sub(now)
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		if b.strikes > 0 && now.Sub(b.until) > l.backoff(b.strikes) {
			b.strikes = 0 // quiet for a full backoff period: forgive
		}
		return 0
	}
	b.strikes++
	d := l.backoff(b.strikes)
	b.until = now.Add(d)
	return d
}

// backoff is the time for one token, doubled per strike, capped.
func (l *rateLimiter) backoff(strikes int) time.Duration {
	d := time.Duration(float64(time.Second) / l.rate)
	for i := 1; i < strikes && d < maxBackoff; i++ {
		d *= 2
	}
	return min(d, maxBackoff)
}

// evict drops idle keys (bucket full, not blocked); if that is not enough,
// arbitrary keys, so memory stays bounded under a flood of new keys.
func (l *rateLimiter) evict(now time.Time) {
	full := time.Duration(l.burst / l.rate * float64(time.Second))
	for k, b := range l.m {
		if now.After(b.until) && now.Sub(b.last) > full {
			delete(l.m, k)
		}
	}
	for k := range l.m {
		if len(l.m) < l.maxKeys*9/10 {
			break
		}
		delete(l.m, k)
	}
}
