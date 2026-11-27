package api

import (
	"context"
	"maps"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/geoip"
	"github.com/NexusBetweenVoids/gunkly/internal/i18n"
	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// Unusual sign-in email. Where a sign-in came from is known only when the
// operator provides a source: a country (and AS number) header set by a
// trusted proxy such as Cloudflare, and/or MaxMind-format databases. Without
// one nothing is looked up, stored or sent. Only the country code and a
// "hosting or Tor network" yes/no are kept, in the audit entry of the sign-in.

// GeoIP is the optional location source of sign-ins (GUNKLY_GEOIP_*).
type GeoIP struct {
	CountryHeader, ASNHeader string // request headers, read only from trusted proxies
	CountryDB, ASNDB         *geoip.DB
}

const (
	signinHistory     = 90 * 24 * time.Hour // earlier sign-ins that count as "expected"
	signinAlertPeriod = time.Hour           // at most one unusual sign-in email per account per period
)

// signinLocation names where sign-in locations come from: "none", "header",
// "database" or "both". Header sources only count behind a trusted proxy.
func (s *Server) signinLocation() string {
	header := (s.geo.CountryHeader != "" || s.geo.ASNHeader != "") && len(s.proxies) > 0
	db := s.geo.CountryDB != nil || s.geo.ASNDB != nil
	switch {
	case header && db:
		return "both"
	case header:
		return "header"
	case db:
		return "database"
	}
	return "none"
}

// place is what is known about the network a sign-in came from.
type place struct {
	country string // ISO 3166 code, "" = unknown
	hosting bool   // a cloud/VPS/hosting network, or Tor
	known   bool   // anything at all is known
}

func asciiUpper2(s string) bool {
	return len(s) == 2 && s[0] >= 'A' && s[0] <= 'Z' && s[1] >= 'A' && s[1] <= 'Z'
}

// locate reads the place of the request: from the configured headers when the
// peer is a trusted proxy (anyone else could send them), else or additionally
// from the databases by the client address.
func (s *Server) locate(r *http.Request, c *call) place {
	var country string
	var asn uint32
	tor := false
	if isTrusted(peerAddr(r), s.proxies) {
		if h := s.geo.CountryHeader; h != "" {
			switch v := strings.ToUpper(strings.TrimSpace(r.Header.Get(h))); {
			case v == "T1": // Cloudflare: Tor
				tor = true
			case v != "XX" && asciiUpper2(v):
				country = v
			}
		}
		if h := s.geo.ASNHeader; h != "" {
			v := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(r.Header.Get(h))), "AS")
			if n, err := strconv.ParseUint(v, 10, 32); err == nil {
				asn = uint32(n)
			}
		}
	}
	if addr, err := netip.ParseAddr(c.ip); err == nil {
		if country == "" && !tor && s.geo.CountryDB != nil {
			country = s.geo.CountryDB.Country(addr)
		}
		if asn == 0 && s.geo.ASNDB != nil {
			asn = s.geo.ASNDB.ASN(addr)
		}
	}
	return place{country: country, hosting: tor || geoip.IsHostingASN(asn), known: country != "" || asn != 0 || tor}
}

// unusualSignin compares a sign-in with the account's earlier ones. Nothing to
// compare with (no earlier sign-ins, or none with a known place) is never
// unusual.
func unusualSignin(history []store.SigninPlace, p place) (newCountry, newNetwork bool) {
	var countries, withCountry, hostingNo, hostingYes int
	for _, h := range history {
		if h.Country != "" {
			withCountry++
			if h.Country == p.country {
				countries++
			}
		}
		switch h.Hosting {
		case "no":
			hostingNo++
		case "yes":
			hostingYes++
		}
	}
	newCountry = p.country != "" && withCountry > 0 && countries == 0
	newNetwork = p.hosting && hostingNo > 0 && hostingYes == 0
	return newCountry, newNetwork
}

// recordSignin audits a successful sign-in that created a session and, when a
// location source is configured, checks whether it looks unexpected.
func (s *Server) recordSignin(ctx context.Context, r *http.Request, c *call, u store.User, meta map[string]string) {
	if s.signinLocation() == "none" {
		s.audit(ctx, c, u.ID, store.AuditLoginSuccess, "", meta)
		return
	}
	p := s.locate(r, c)
	var history []store.SigninPlace
	if p.known {
		var err error
		if history, err = s.st.SigninPlaces(ctx, u.ID, s.now().Add(-signinHistory)); err != nil {
			s.log.Error("sign-in history", "err", err)
			p.known = false
		}
	}
	meta = maps.Clone(meta)
	if meta == nil {
		meta = map[string]string{}
	}
	if p.country != "" {
		meta["country"] = p.country
	}
	if p.known {
		meta["hosting"] = map[bool]string{true: "yes", false: "no"}[p.hosting]
	}
	s.audit(ctx, c, u.ID, store.AuditLoginSuccess, "", meta) // after reading the history: it is not part of it
	if !p.known {
		return
	}
	if newCountry, newNetwork := unusualSignin(history, p); newCountry || newNetwork {
		s.signinUnusual(ctx, c, u.ID, p, newCountry, newNetwork)
	}
}

// signinUnusual records the event and, if the user wants it and the account
// has a confirmed address, emails them (at most once per signinAlertPeriod).
func (s *Server) signinUnusual(ctx context.Context, c *call, userID string, p place, newCountry, newNetwork bool) {
	now := s.now()
	recent, err := s.st.AuditCountSince(ctx, store.AuditSigninUnusual, userID, "", now.Add(-signinAlertPeriod))
	if err != nil {
		s.log.Error("sign-in alert", "err", err)
		return
	}
	u, err := s.st.UserByID(ctx, userID) // fresh: the setting and address may have just changed
	if err != nil {
		s.log.Error("sign-in alert", "err", err)
		return
	}
	emailed := false
	if recent == 0 && u.SigninAlerts && u.EmailVerified && u.Email != "" && s.emailEnabled() &&
		s.mailQuota(ctx, u.ID, "", limits.EmailsAccountHour) == 0 {
		var reasons []string
		if newCountry {
			reasons = append(reasons, i18n.T(u.Locale, "mail.signinunusual.country", map[string]string{"country": p.country}))
		}
		if newNetwork {
			reasons = append(reasons, i18n.T(u.Locale, "mail.signinunusual.network", nil))
		}
		s.dispatchMail(ctx, mailRequest{user: u, kind: "signinunusual", ttl: 24 * time.Hour, params: map[string]string{
			"time": now.UTC().Format("2006-01-02 15:04") + " UTC", "ip": c.ip, "reasons": strings.Join(reasons, "\n"),
			"link": s.baseURL + "/settings/security",
		}})
		emailed = true
	}
	meta := map[string]string{"hosting": map[bool]string{true: "yes", false: "no"}[p.hosting]}
	if p.country != "" {
		meta["country"] = p.country
	}
	if emailed {
		meta["emailed"] = "1"
	}
	s.audit(ctx, c, u.ID, store.AuditSigninUnusual, "", meta)
}
