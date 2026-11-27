package api

import (
	"context"
	"strconv"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// Thresholds of the abuse alerts (security alerts for the admins). Below them nothing is
// recorded; every alert is emailed at most once per day and subject.
const (
	// channelsBurstThreshold: this many notification channels created in one household within
	// channelsBurstWindow suggests the household is used as a relay.
	channelsBurstThreshold = 5
	channelsBurstWindow    = time.Hour
	// tokenRateLimitThreshold: this many requests of one API or OAuth token rejected with 429
	// within an hour.
	tokenRateLimitThreshold = 20
	// tokenBurstThreshold: this many API tokens created, or OAuth grants approved, by one
	// account within tokenBurstWindow.
	tokenBurstThreshold = 5
	tokenBurstWindow    = time.Hour
	// quotaAlertEvery bounds how often one household's notification-limit hits are recorded.
	quotaAlertEvery = time.Hour
	// abuseCounterKeys bounds the in-memory counters of the alerts above.
	abuseCounterKeys = 5000
)

// hit records one event under key and returns how many fall in the window, or 0 when the table
// is full of live keys (the alert is then skipped: fail quiet, memory stays bounded).
func (w *windowCounter) hit(key string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	cut := now.Add(-w.window)
	ev := w.prune(key, cut)
	if _, known := w.events[key]; !known && len(w.events) >= w.maxKeys {
		w.sweep(cut)
		if len(w.events) >= w.maxKeys {
			return 0
		}
	}
	w.events[key] = append(ev, now)
	return len(ev) + 1
}

// NotificationLimitReached is the scheduler's hook: a household's hourly or daily notification
// limit turned reminders away. Recorded once per household and hour; never fails the caller.
func (s *Server) NotificationLimitReached(ctx context.Context, h store.QuotaHit) {
	if s.quotaHits.hit(h.HouseholdID) != 1 {
		return
	}
	s.securityAlert(ctx, nil, securityAlert{event: store.AuditNotifyLimit, scope: h.HouseholdID, household: h.HouseholdID,
		mail: "securitynotifylimit", params: map[string]string{"householdId": h.HouseholdID, "limit": strconv.Itoa(h.Max), "hours": strconv.Itoa(h.Hours)}})
}

// channelCreated audits a new household channel and alerts on a burst of them.
func (s *Server) channelCreated(ctx context.Context, c *call, channelID string) {
	s.audit(ctx, c, c.user.ID, store.AuditChannelCreate, c.hh, map[string]string{"channel": channelID})
	n, err := s.st.AuditCountSince(ctx, store.AuditChannelCreate, "", c.hh, s.now().Add(-channelsBurstWindow))
	if err != nil {
		s.log.Error("channels burst check", "err", err)
		return
	}
	if n >= channelsBurstThreshold {
		s.securityAlert(ctx, c, securityAlert{event: store.AuditChannelsBurst, scope: c.hh, household: c.hh, actor: c.user.ID,
			mail: "securitychannels", params: map[string]string{"username": c.user.Username, "householdId": c.hh, "count": strconv.Itoa(n)}})
	}
}

// tokenRateLimited counts a request of a token rejected with 429 and alerts at the threshold.
func (s *Server) tokenRateLimited(ctx context.Context, c *call, tok store.APIToken, u store.User) {
	if s.tokenHits.hit(tok.ID) != tokenRateLimitThreshold {
		return
	}
	s.securityAlert(ctx, c, securityAlert{event: store.AuditTokenRateLimited, scope: tok.ID, actor: u.ID,
		mail: "securitytokenratelimit", params: map[string]string{"username": u.Username, "token": tok.ID,
			"count": strconv.Itoa(tokenRateLimitThreshold)}})
}

// tokenBurstCheck alerts when the account created many API tokens or approved many OAuth
// connections within the window. Called after one of them was created.
func (s *Server) tokenBurstCheck(ctx context.Context, c *call, u store.User) {
	since := s.now().Add(-tokenBurstWindow)
	for _, ev := range []string{store.AuditTokenCreate, store.AuditOAuthGrant} {
		n, err := s.st.AuditCountSince(ctx, ev, u.ID, "", since)
		if err != nil {
			s.log.Error("token burst check", "err", err)
			return
		}
		if n >= tokenBurstThreshold {
			s.securityAlert(ctx, c, securityAlert{event: store.AuditTokenBurst, scope: u.ID, actor: u.ID,
				mail: "securitytokens", params: map[string]string{"username": u.Username, "count": strconv.Itoa(n)}})
			return
		}
	}
}
