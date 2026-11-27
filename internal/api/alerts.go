package api

import (
	"context"
	"crypto/sha256"
	"maps"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// securityAlert is one suspicious event for the admins (see store.AlertEvents).
type securityAlert struct {
	event     string            // audit event; must be listed in store.AlertEvents
	scope     string            // deduplication key: household, account, address, or "" for instance-wide
	household string            // the household it concerns, if any
	actor     string            // the account that caused it, if any
	mail      string            // i18n stem of the admin email (mail.<mail>.subject/body); "" sends none
	params    map[string]string // email parameters; also stored as audit meta (never secrets)
	period    time.Duration     // at most one email per event and scope in this period; 0 = 24 hours
	always    bool              // email even when the instance's securityAlerts setting is off (only for turning it off)
}

// securityAlert records an alert in the audit log (always; the Administration page lists recent
// ones) and, when security alerts are on and email works, emails the admins with a confirmed
// address, at most once per event and scope per period. c may be nil (background work).
func (s *Server) securityAlert(ctx context.Context, c *call, a securityAlert) {
	period := a.period
	if period == 0 {
		period = 24 * time.Hour
	}
	prior, err := s.st.AlertCountSince(ctx, a.event, a.scope, s.now().Add(-period))
	if err != nil {
		s.log.Error("security alert", "event", a.event, "err", err)
		return
	}
	meta := maps.Clone(a.params)
	if meta == nil {
		meta = map[string]string{}
	}
	meta["scope"] = a.scope
	if c == nil {
		c = &call{}
	}
	s.audit(ctx, c, a.actor, a.event, a.household, meta)
	if prior > 0 || a.mail == "" || !s.emailEnabled() {
		return
	}
	st, err := s.st.InstanceSettings(ctx)
	if err != nil || (!st.SecurityAlerts && !a.always) {
		return
	}
	admins, err := s.st.AlertRecipients(ctx)
	if err != nil {
		s.log.Error("security alert", "event", a.event, "err", err)
		return
	}
	params := maps.Clone(a.params)
	if params == nil {
		params = map[string]string{}
	}
	params["link"] = s.baseURL + "/admin"
	for _, ad := range admins {
		s.dispatchMail(ctx, mailRequest{user: ad, kind: a.mail, params: params})
	}
}

// adminChangeAlert records a change of admin power (a new admin, security alerts, sign-up, proof of
// work or the egress guard switched) and tells every admin, who made it and when. scope is the new
// value, so each real change mails once and flip-flopping within a minute does not flood.
func (s *Server) adminChangeAlert(ctx context.Context, c *call, event, scope, mail string, always bool, params map[string]string) {
	p := maps.Clone(params)
	if p == nil {
		p = map[string]string{}
	}
	p["admin"] = c.user.Username
	p["time"] = s.now().UTC().Format("2006-01-02 15:04 UTC")
	s.securityAlert(ctx, c, securityAlert{event: event, scope: scope, actor: c.user.ID, mail: mail, params: p,
		period: time.Minute, always: always})
}

// Thresholds of the sign-in and sign-up alerts. Fixed on purpose (not admin settings): high enough
// that normal use never reaches them, low enough to notice an attack early.
const (
	signinAccountFailures = 20 // failed password or second-factor attempts against one account within an hour
	signinIPAccounts      = 10 // different accounts one client address failed to sign in to within an hour
	signupWaveCount       = 20 // new accounts instance-wide within an hour
	attackWindow          = time.Hour
	signupWavePeriod      = 6 * time.Hour // at most one signup-wave email per this period
	spreadMaxIPs          = 10000         // bound of the in-memory per-address tracker
)

// signupEvents are the audit events of a newly created account.
var signupEvents = []string{store.AuditSignup, store.AuditRegisterByInvite, store.AuditSSOSignup}

// afterAudit runs cheap detection after an audit entry was written. Alerting never fails the
// request: errors are logged and ignored. The alerts themselves are audited with other events, so
// this does not recurse. An alert fires when the count reaches its threshold exactly, so one burst
// writes one alert entry.
func (s *Server) afterAudit(ctx context.Context, c *call, actor, event string) {
	switch {
	case event == store.AuditLoginFailure && actor != "":
		n, err := s.st.CountAuditSince(ctx, []string{event}, actor, s.now().Add(-attackWindow))
		if err != nil {
			s.log.Error("sign-in alert", "err", err)
			return
		}
		if n != signinAccountFailures {
			return
		}
		username := ""
		if u, err := s.st.UserByID(ctx, actor); err == nil {
			username = u.Username
		}
		s.securityAlert(ctx, c, securityAlert{event: store.AuditSigninAttackAccount, scope: actor, actor: actor, mail: "securitysigninaccount",
			params: map[string]string{"username": username, "count": strconv.Itoa(n)}})
	case slices.Contains(signupEvents, event):
		n, err := s.st.CountAuditSince(ctx, signupEvents, "", s.now().Add(-attackWindow))
		if err != nil {
			s.log.Error("sign-up alert", "err", err)
			return
		}
		if n != signupWaveCount {
			return
		}
		s.securityAlert(ctx, c, securityAlert{event: store.AuditSignupWave, mail: "securitysignupwave", period: signupWavePeriod,
			params: map[string]string{"count": strconv.Itoa(n)}})
	}
}

// signinSpread remembers, in memory only, which different logins each client address failed to
// sign in to (credential stuffing). Logins are kept as short hashes, never as text; passwords are
// never seen here. Bounded: at most spreadMaxIPs addresses and signinIPAccounts logins each.
type signinSpread struct {
	mu sync.Mutex
	m  map[string]*spreadEntry
}

type spreadEntry struct {
	start  time.Time
	logins map[[8]byte]struct{}
}

// fail records a failed sign-in of login from ip. It returns the number of different logins that
// address has failed within the window only for the attempt that reaches the threshold (else 0),
// so the caller alerts once per burst.
func (t *signinSpread) fail(ip, login string, now time.Time) int {
	sum := sha256.Sum256([]byte(login))
	key := [8]byte(sum[:8])
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.m == nil {
		t.m = map[string]*spreadEntry{}
	}
	e := t.m[ip]
	if e != nil && now.Sub(e.start) >= attackWindow {
		delete(t.m, ip)
		e = nil
	}
	if e == nil {
		if len(t.m) >= spreadMaxIPs {
			for k, v := range t.m {
				if now.Sub(v.start) >= attackWindow {
					delete(t.m, k)
				}
			}
			if len(t.m) >= spreadMaxIPs {
				return 0 // full of recent addresses: not tracked
			}
		}
		e = &spreadEntry{start: now, logins: map[[8]byte]struct{}{}}
		t.m[ip] = e
	}
	if len(e.logins) >= signinIPAccounts {
		return 0 // already alerted in this window
	}
	e.logins[key] = struct{}{}
	if len(e.logins) == signinIPAccounts {
		return len(e.logins)
	}
	return 0
}

// signinSpreadFail is called for every failed password check (also for logins that do not exist).
func (s *Server) signinSpreadFail(ctx context.Context, c *call, login string) {
	if n := s.spread.fail(c.rip, login, s.now()); n > 0 {
		s.securityAlert(ctx, c, securityAlert{event: store.AuditSigninAttackIP, scope: c.rip, mail: "securitysigninip",
			params: map[string]string{"ip": c.ip, "count": strconv.Itoa(n)}})
	}
}
