package api

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/i18n"
	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/mail"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// Mailer sends one email. *mail.Sender implements it; tests inject a fake so
// nothing ever contacts a real host.
type Mailer interface {
	Send(ctx context.Context, m mail.Message) error
}

// Lifetimes of the emailed credentials (PRD: verification 24 h, reset 1 h,
// codes 10 min; ADR 0008).
const (
	verifyTTL = 24 * time.Hour
	resetTTL  = time.Hour
	otpTTL    = 10 * time.Minute
)

// windowCounter counts events per key over a sliding hour, in memory and per
// instance like the other limiters (ADR 0013). Memory is bounded: when the
// table is full of live keys new keys are refused (fail closed).
type windowCounter struct {
	mu      sync.Mutex
	now     func() time.Time
	maxKeys int
	events  map[string][]time.Time
}

func newWindowCounter(now func() time.Time, maxKeys int) *windowCounter {
	return &windowCounter{now: now, maxKeys: maxKeys, events: map[string][]time.Time{}}
}

type quota struct {
	key   string
	limit int
}

// take records one event under every quota, or none if any is full. It
// returns 0 on success, otherwise how long until the earliest full quota has
// room again.
func (w *windowCounter) take(qs ...quota) time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	cut := now.Add(-time.Hour)
	var wait time.Duration
	for _, q := range qs {
		ev := w.prune(q.key, cut)
		if len(ev) >= q.limit && q.limit > 0 {
			wait = max(wait, ev[len(ev)-q.limit].Add(time.Hour).Sub(now))
		} else if _, known := w.events[q.key]; !known && len(w.events) >= w.maxKeys {
			w.sweep(cut)
			if len(w.events) >= w.maxKeys {
				wait = max(wait, time.Minute)
			}
		}
	}
	if wait > 0 {
		return wait
	}
	for _, q := range qs {
		w.events[q.key] = append(w.events[q.key], now)
	}
	return 0
}

func (w *windowCounter) prune(key string, cut time.Time) []time.Time {
	ev := w.events[key]
	i := 0
	for i < len(ev) && !ev[i].After(cut) {
		i++
	}
	if i > 0 {
		ev = append([]time.Time(nil), ev[i:]...)
		if len(ev) == 0 {
			delete(w.events, key)
		} else {
			w.events[key] = ev
		}
	}
	return ev
}

func (w *windowCounter) sweep(cut time.Time) {
	for k := range w.events {
		w.prune(k, cut)
	}
}

// emailEnabled reports whether this instance sends email.
func (s *Server) emailEnabled() bool { return s.mailer != nil }

// mailQuota takes the outbound email caps (PRD Limits): per account, per IP
// for requests without a session (ip empty for authenticated ones) and
// global, each admin-overridable through internal/limits. It returns how long
// to wait when one is full.
func (s *Server) mailQuota(ctx context.Context, accountID, ip string) time.Duration {
	qs := make([]quota, 0, 3)
	add := func(kind limits.Kind, key string) {
		n, err := s.limits.Limit(ctx, kind, "")
		if err != nil {
			n = limits.Defaults[kind]
		}
		qs = append(qs, quota{key, n})
	}
	add(limits.EmailsGlobalHour, "mail:all")
	if ip != "" {
		add(limits.EmailsIPHour, "mail:ip:"+ip)
	}
	if accountID != "" {
		add(limits.EmailsAccountHour, "mail:acct:"+accountID)
	}
	return s.counts.take(qs...)
}

// mailRequest describes one email to queue.
type mailRequest struct {
	user   store.User // recipient account (locale, display name, quota key)
	to     string     // address; empty means user.Email
	kind   string     // i18n key stem: "verify", "reset", "otp", "emailchange"
	params map[string]string
	// unauth marks a request without a session: the per-IP cap applies too.
	unauth bool
}

// queueMail takes the quotas and sends the email in the background, so the
// response does not wait on (or reveal anything about) the SMTP server. It
// returns the wait when a quota is full (nothing is sent then).
func (s *Server) queueMail(ctx context.Context, c *call, r mailRequest) time.Duration {
	ip := ""
	if r.unauth {
		ip = c.rip
	}
	if wait := s.mailQuota(ctx, r.user.ID, ip); wait > 0 {
		return wait
	}
	s.dispatchMail(ctx, r)
	return 0
}

// dispatchMail sends r in the background without taking a quota (the caller
// already did, so it could act only when the email is certain to go out).
func (s *Server) dispatchMail(ctx context.Context, r mailRequest) {
	to := r.to
	if to == "" {
		to = r.user.Email
	}
	if r.params == nil {
		r.params = map[string]string{}
	}
	r.params["name"] = r.user.DisplayName
	msg := mail.Message{
		To:      to,
		Subject: i18n.T(r.user.Locale, "mail."+r.kind+".subject", r.params),
		Body:    i18n.T(r.user.Locale, "mail."+r.kind+".body", r.params),
	}
	s.mailWG.Add(1)
	go func() {
		defer s.mailWG.Done()
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), mail.SendTimeout)
		defer cancel()
		if err := s.mailer.Send(sctx, msg); err != nil {
			s.log.Error("send email", "kind", r.kind, "err", err)
		}
	}()
}

// tooMany answers a refused email or sign-up with 429 and Retry-After.
func tooMany(w http.ResponseWriter, wait time.Duration) error {
	w.Header().Set("Retry-After", strconv.Itoa(max(int((wait+time.Second-1)/time.Second), 1)))
	writeError(w, http.StatusTooManyRequests, "rate_limited")
	return nil
}

func (s *Server) link(path, token string) string { return s.baseURL + path + "?token=" + token }
