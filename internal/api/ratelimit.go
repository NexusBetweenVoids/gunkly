package api

import (
	"strings"
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
	// perMinute overrides the default rate for keys of a bucket (the part of
	// the key before the first colon), e.g. "ical" for "ical:ip:1.2.3.4".
	perMinute map[string]int
	now       func() time.Time
	m         map[string]*bucket
}

type bucket struct {
	rate    float64 // tokens per second
	burst   float64
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
		rate, burst := l.rate, l.burst
		if n, ok := l.perMinute[bucketOf(key)]; ok {
			rate, burst = float64(n)/60, float64(n)
		}
		if len(l.m) >= l.maxKeys {
			l.evict(now)
		}
		b = &bucket{rate: rate, burst: burst, tokens: burst, last: now}
		l.m[key] = b
	}
	if now.Before(b.until) {
		return b.until.Sub(now)
	}
	b.tokens = min(b.burst, b.tokens+now.Sub(b.last).Seconds()*b.rate)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		if b.strikes > 0 && now.Sub(b.until) > backoff(b.rate, b.strikes) {
			b.strikes = 0 // quiet for a full backoff period: forgive
		}
		return 0
	}
	b.strikes++
	d := backoff(b.rate, b.strikes)
	b.until = now.Add(d)
	return d
}

// bucketOf is the bucket name of a limiter key ("login:ip:1.2.3.4" -> "login").
func bucketOf(key string) string {
	b, _, _ := strings.Cut(key, ":")
	return b
}

// backoff is the time for one token, doubled per strike, capped.
func backoff(rate float64, strikes int) time.Duration {
	d := time.Duration(float64(time.Second) / rate)
	for i := 1; i < strikes && d < maxBackoff; i++ {
		d *= 2
	}
	return min(d, maxBackoff)
}

// evict drops idle keys (bucket full, not blocked); if that is not enough,
// arbitrary keys, so memory stays bounded under a flood of new keys.
func (l *rateLimiter) evict(now time.Time) {
	for k, b := range l.m {
		full := time.Duration(b.burst / b.rate * float64(time.Second))
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
