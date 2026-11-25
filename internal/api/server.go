// Package api serves the versioned JSON API under /api/v1, the health
// probes and the embedded single-page app. It never touches the database
// directly: all data goes through internal/store, which enforces household
// membership (ADR 0009).
package api

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/NexusBetweenVoids/gunkly/internal/auth"
	"github.com/NexusBetweenVoids/gunkly/internal/egress"
	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/mail"
	"github.com/NexusBetweenVoids/gunkly/internal/metrics"
	"github.com/NexusBetweenVoids/gunkly/internal/notify"
	"github.com/NexusBetweenVoids/gunkly/internal/sso"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
	"github.com/NexusBetweenVoids/gunkly/internal/updates"
	"github.com/NexusBetweenVoids/gunkly/internal/webhook"
)

// Options configures a Server.
type Options struct {
	Store   *store.Store
	Limits  *limits.Service
	Keyring *auth.Keyring
	BaseURL string // scheme://host[:port] or empty
	// Metrics enables GET /metrics (Prometheus text format) behind
	// MetricsToken (a bearer token). Both must be set; otherwise the route is
	// not registered.
	Metrics      *metrics.Registry
	MetricsToken string
	// Version is the running release ("dev" for a development build), shown on
	// the admin Updates page.
	Version string
	// Updates refreshes the cached release check on demand. Nil: the Updates
	// page only reads the cache (tests, so nothing ever calls out).
	Updates *updates.Checker
	// HostedMode forces the egress guard on, whatever the admin setting says.
	HostedMode bool
	// Sender delivers test notifications. Nil builds the Shoutrrr sender
	// whose egress guard follows the instance setting; use Server.Sender()
	// for the scheduler so both behave alike.
	Sender         notify.Sender
	TrustedProxies []netip.Prefix
	// SMTP enables email (verification, password reset, email codes) when set;
	// mail is sent through the egress guard like every outbound connection.
	// Mailer replaces the SMTP sender (tests). Both nil: email is disabled.
	SMTP   *mail.Config
	Mailer Mailer
	// SSO configures OpenID Connect single sign-on (ADR 0018). Nil, or hosted
	// mode: off, and its routes answer 404.
	SSO *sso.Config
	// PoWMaxNumber is the sign-up proof-of-work difficulty; 0 means the default.
	PoWMaxNumber int
	Logger       *slog.Logger
	// Ready reports whether the server can take traffic (database reachable,
	// migrations current). Used by /readyz.
	Ready func(context.Context) error
	// Static is the built SPA (web/dist). Nil serves no front end.
	Static fs.FS
	// Now is the clock; nil means time.Now. Tests inject a fake clock.
	Now func() time.Time
	// Env holds the environment variables that override admin configuration
	// (see ConfigEnvNames), only those that are set. They always win over the
	// values stored through Administration.
	Env map[string]string
	// AIClient replaces the HTTP client used for the AI endpoint (tests).
	AIClient *http.Client
}

// Server is the HTTP API. Create with New.
type Server struct {
	st *store.Store

	baseURLWarned atomic.Int64 // unix time of the last base-URL mismatch warning
	limits        *limits.Service
	keyring       *auth.Keyring
	baseURL       string
	metrics       *metrics.Registry
	version       string
	updates       *updates.Checker
	hosted        bool
	sender        notify.Sender
	push          *notify.WebPush
	webhooks      *webhook.Sender
	secure        bool // cookies get Secure; HSTS is sent
	proxies       []netip.Prefix
	untrusted     untrustedProxies
	log           *slog.Logger
	ready         func(context.Context) error
	static        fs.FS
	now           func() time.Time
	rl            *rateLimiter
	acct          *acctGuard
	mailWG        sync.WaitGroup // in-flight background sends
	counts        *windowCounter // hourly email and sign-up caps
	pkOnce        sync.Once
	pk            *webauthn.WebAuthn // passkey relying party; nil without an https (or localhost) base URL

	// Password hashing hooks; tests swap in fast fakes and counters.
	hashPassword   func(string) string
	verifyPassword func(hash, pw string) (bool, error)
	dummyOnce      sync.Once
	dummyHash      string

	// Cached session lifetimes (see sessionLifetimes).
	lifeMu            sync.Mutex
	lifeIdle, lifeMax time.Duration
	lifeAt            time.Time

	// Cached mcp_enabled and oauth_enabled instance settings (see features).
	mcpMu sync.Mutex
	feat  features
	mcpAt time.Time

	setupMu   sync.Mutex
	setupHash string // hash of the first-run setup code; empty when not in setup

	env map[string]string // configuration overrides from the environment (Options.Env)
	// Email, single sign-on, metrics token and proof of work: rebuilt from the
	// environment and Administration settings (runtime.go).
	rt     atomic.Pointer[runtimeServices]
	envSvc envServices

	// AI chore suggestions: the admin endpoint's client, the 24-hour per-user
	// request counter and the instance-wide cap on requests in flight.
	aiHTTP *http.Client
	// updateHTTP calls the admin's update webhook (may be on the local network).
	updateHTTP *http.Client
	aiCounts   *windowCounter
	aiSem      chan struct{}

	mux    *http.ServeMux
	routes []route
	// bodyLimits maps a route pattern to its raised request body cap.
	bodyLimits map[string]int64
}

// New builds the server and registers every route.
func New(o Options) *Server {
	s := &Server{
		st: o.Store, limits: o.Limits, keyring: o.Keyring,
		baseURL: o.BaseURL, version: o.Version, updates: o.Updates, secure: strings.HasPrefix(o.BaseURL, "https://"),
		proxies: o.TrustedProxies, log: o.Logger, ready: o.Ready, static: o.Static, now: o.Now,
		hashPassword: auth.HashPassword, verifyPassword: auth.VerifyPassword,
		mux: http.NewServeMux(), bodyLimits: map[string]int64{}, env: o.Env,
	}
	if s.env == nil {
		s.env = map[string]string{}
	}
	s.hosted, s.sender = o.HostedMode, o.Sender
	if s.sender == nil {
		s.sender = notify.NewSwitchSender(s.EgressGuardEnabled)
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.now == nil {
		s.now = time.Now
	}
	// The VAPID contact claim: this instance's https URL when it has one.
	subject := "https://github.com/NexusBetweenVoids/gunkly"
	if s.secure {
		subject = o.BaseURL
	}
	s.push = notify.NewWebPush(s.st, s.keyring, subject, s.EgressGuardEnabled, s.now)
	s.webhooks = webhook.NewSender(s.EgressGuardEnabled, s.version, s.now)
	if s.ready == nil {
		s.ready = func(context.Context) error { return nil }
	}
	if o.SSO != nil && s.hosted {
		s.log.Warn("single sign-on is not available in hosted mode; GUNKLY_OIDC_* ignored")
	}
	s.counts = newWindowCounter(s.now, 20000)
	s.aiCounts = newWindowCounter(s.now, 20000)
	s.aiCounts.window = 24 * time.Hour
	s.aiSem = make(chan struct{}, aiConcurrent)
	if s.aiHTTP = o.AIClient; s.aiHTTP == nil {
		s.aiHTTP = newAIHTTP()
	}
	if s.updateHTTP = o.AIClient; s.updateHTTP == nil { // tests reuse the injected client
		s.updateHTTP = egress.NewAdminEndpointClient(updateHookTimeout)
	}
	s.envSvc = envServices{smtp: o.SMTP, sso: o.SSO, metricsToken: o.MetricsToken, powMax: o.PoWMaxNumber, testMailer: o.Mailer}
	// Until InitSetup reads the stored configuration: the environment alone.
	s.rt.Store(s.buildServices(s.envOnlyConfig()))
	s.rl = newRateLimiter(limits.Defaults[limits.LoginAttemptsPerMinute], 10000, s.now)
	s.acct = newAcctGuard(s.now, 10000)
	s.rl.perMinute, s.rl.burstOf = bucketRates, bucketBursts
	s.rl.defaultRate = func() int {
		if s.limits == nil {
			return 0
		}
		n, err := s.limits.Limit(context.Background(), limits.LoginAttemptsPerMinute, "")
		if err != nil {
			return 0
		}
		return n
	}
	if o.Metrics != nil {
		s.metrics = o.Metrics
	}
	s.registerRoutes()
	return s
}

// Handler returns the root handler with all middleware applied.
func (s *Server) Handler() http.Handler { return s.middleware(s.mux) }

// InitSetup prepares first-run setup when no account exists: it generates a
// one-time setup code, logs it once and keeps only its hash. This is the one
// secret Gunkly ever logs (ADR 0013): it is only useful until the first
// admin exists, and the container logs are only visible to the operator who
// must type it in.
func (s *Server) InitSetup(ctx context.Context) error {
	if err := s.applyConfig(ctx); err != nil {
		return err
	}
	has, err := s.st.HasUsers(ctx)
	if err != nil || has {
		return err
	}
	codes, hashes := auth.GenerateRecoveryCodes(1)
	s.setupMu.Lock()
	s.setupHash = hashes[0]
	s.setupMu.Unlock()
	s.log.Warn("FIRST-RUN SETUP: open Gunkly in a browser and enter this one-time setup code to create the admin account",
		"setup_code", codes[0])
	return nil
}

// checkSetupCode verifies and, if valid, keeps the code until consumeSetup.
func (s *Server) checkSetupCode(code string) bool {
	h, ok := auth.HashRecoveryCode(code)
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	return ok && s.setupHash != "" && ids.Equal(h, s.setupHash)
}

func (s *Server) setupPending() bool {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	return s.setupHash != ""
}

func (s *Server) consumeSetup() {
	s.setupMu.Lock()
	s.setupHash = ""
	s.setupMu.Unlock()
}
