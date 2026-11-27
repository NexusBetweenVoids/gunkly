package api

import (
	"context"
	"maps"
	"time"
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
