package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// authLevel is who may call a route. The zero value is deliberately invalid
// so a route cannot be registered without choosing one.
type authLevel int

const (
	authUnset     authLevel = iota
	authPublic              // anyone
	authSession             // any signed-in user
	authHousehold           // signed-in member of {hh} with at least minRole
	authAdmin               // signed-in instance admin
)

// policy is the access declaration every route carries. The router enforces
// it before the handler runs; a fitness test walks the route table.
type policy struct {
	auth    authLevel
	minRole store.Role // authHousehold only
	stepUp  bool       // requires re-verification within stepUpWindow
	rate    string     // rate-limit bucket keyed by client IP; "" = none
	// tokenAuth marks a public route whose credential is an unguessable token
	// in the URL (mark-done links). Unsafe methods skip the cookie CSRF
	// double-submit, which cannot apply without a session, and get the
	// cross-site request check only (crossSiteOK).
	tokenAuth bool
}

var (
	public  = policy{auth: authPublic}
	session = policy{auth: authSession}
	admin   = policy{auth: authAdmin}
)

func publicRateLimited(bucket string) policy { return policy{auth: authPublic, rate: bucket} }

func household(min store.Role) policy { return policy{auth: authHousehold, minRole: min} }

func (p policy) withStepUp() policy { p.stepUp = true; return p }

func (p policy) withRate(bucket string) policy { p.rate = bucket; return p }

// withTokenAuth declares the route authenticated by a URL token (see policy).
func (p policy) withTokenAuth() policy { p.tokenAuth = true; return p }

// Rate-limit buckets (login-attempt rate from internal/limits unless overridden in
// bucketRates).
const (
	rateLogin    = "login"
	rateSetup    = "setup"
	rateTwoFA    = "2fa"
	ratePassword = "password"
	rateInvite   = "invite"
	rateMarkDone = "markdone" // unauthenticated mark-done pages
	rateICal     = "ical"     // unauthenticated calendar feeds (apps poll)
	rateTest     = "test"     // test notifications, per user

	rateUserWrite      = "uwrite" // authenticated non-GET requests, per user
	rateHouseholdWrite = "hwrite" // household write requests, per household
)

// bucketRates gives the unauthenticated link buckets their own rate per IP:
// several people may sit behind one NAT and calendar apps poll, while login
// stays at the strict default.
var bucketRates = map[string]int{
	rateMarkDone: limits.MarkDonePerMinute, rateICal: limits.ICalPerMinute,
	rateUserWrite: limits.UserWritesPerMinute, rateHouseholdWrite: limits.HouseholdWritesPerMinute,
}

// bucketBursts overrides the bucket capacity where it differs from one minute of tokens.
var bucketBursts = map[string]int{rateUserWrite: limits.UserWriteBurst, rateHouseholdWrite: limits.HouseholdWriteBurst}

type route struct {
	pattern string
	policy  policy
}

// call is the per-request context handed to handlers.
type call struct {
	user store.User    // zero unless the policy requires a session
	sess store.Session // zero unless the policy requires a session
	hh   string        // {hh} path value for household routes
	ip   string        // client address, for audit
	rip  string        // rate-limit identity (IPv6 collapsed to /64)
}

func (c *call) actor() store.Actor { return store.Actor{UserID: c.user.ID} }

type handlerFunc func(w http.ResponseWriter, r *http.Request, c *call) error

// handle registers pattern with its policy. Panics on an invalid
// declaration so mistakes fail at start-up, not in production traffic.
func (s *Server) handle(pattern string, p policy, h handlerFunc) {
	if err := p.validate(pattern); err != nil {
		panic(err)
	}
	s.routes = append(s.routes, route{pattern: pattern, policy: p})
	s.mux.Handle(pattern, s.enforce(p, h))
}

func (p policy) validate(pattern string) error {
	hasHH := strings.Contains(pattern, "{hh}")
	switch {
	case p.auth <= authUnset || p.auth > authAdmin:
		return fmt.Errorf("api: route %q has no auth policy", pattern)
	case p.auth == authHousehold && (!hasHH || !p.minRole.Valid()):
		return fmt.Errorf("api: household route %q needs {hh} and a minimum role", pattern)
	case p.auth != authHousehold && hasHH:
		return fmt.Errorf("api: route %q names {hh} but is not household-scoped", pattern)
	case p.stepUp && p.auth == authPublic:
		return fmt.Errorf("api: public route %q cannot require step-up", pattern)
	case p.tokenAuth && (p.auth != authPublic || p.rate == ""):
		return fmt.Errorf("api: token route %q must be public and rate limited", pattern)
	}
	return nil
}

// enforce applies CSRF, rate limiting, authentication, step-up and
// household membership in that order, then runs h.
func (s *Server) enforce(p policy, h handlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := infoFrom(r)
		info.pattern = r.Pattern
		c := &call{ip: info.ip.String(), rip: ipKey(info.ip)}
		if !safeMethod(r.Method) && !s.requestOK(p, r) {
			s.fail(w, r, errCSRF)
			return
		}
		if p.rate != "" && !s.allow(w, p.rate+":ip:"+c.rip) {
			return
		}
		if p.auth >= authSession {
			ss, u, ok := s.authenticate(w, r)
			if !ok {
				s.fail(w, r, errUnauthenticated)
				return
			}
			c.sess, c.user = ss, u
			if p.auth == authAdmin && !u.IsAdmin {
				s.fail(w, r, errForbidden)
				return
			}
			if !safeMethod(r.Method) {
				if !s.allow(w, rateUserWrite+":user:"+u.ID) {
					return
				}
			}
			if p.stepUp && (ss.StepUpAt.IsZero() || s.now().Sub(ss.StepUpAt) > stepUpWindow) {
				s.fail(w, r, errStepUp)
				return
			}
		}
		if p.auth == authHousehold {
			c.hh = r.PathValue("hh")
			// Defence in depth: the store re-checks membership in the same
			// transaction as the data access.
			if !safeMethod(r.Method) && !s.allow(w, rateHouseholdWrite+":hh:"+c.hh) {
				return
			}
			role, err := s.st.Role(r.Context(), c.actor(), c.hh)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			if !role.AtLeast(p.minRole) {
				s.fail(w, r, store.ErrForbidden)
				return
			}
		}
		if err := h(w, r, c); err != nil {
			s.fail(w, r, err)
		}
	})
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// Session timing constants. The idle and absolute lifetimes are instance
// settings (see sessionLifetimes).
const (
	sessionTouchEvery = 5 * time.Minute
	stepUpWindow      = 15 * time.Minute
)
