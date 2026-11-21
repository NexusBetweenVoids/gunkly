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
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/auth"
	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/notify"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// Options configures a Server.
type Options struct {
	Store   *store.Store
	Limits  *limits.Service
	Keyring *auth.Keyring
	BaseURL string // scheme://host[:port] or empty
	// HostedMode forces the egress guard on, whatever the admin setting says.
	HostedMode bool
	// Sender delivers test notifications. Nil builds the Shoutrrr sender
	// whose egress guard follows the instance setting; use Server.Sender()
	// for the scheduler so both behave alike.
	Sender         notify.Sender
	TrustedProxies []netip.Prefix
	Logger         *slog.Logger
	// Ready reports whether the server can take traffic (database reachable,
	// migrations current). Used by /readyz.
	Ready func(context.Context) error
	// Static is the built SPA (web/dist). Nil serves no front end.
	Static fs.FS
	// Now is the clock; nil means time.Now. Tests inject a fake clock.
	Now func() time.Time
}

// Server is the HTTP API. Create with New.
type Server struct {
	st      *store.Store
	limits  *limits.Service
	keyring *auth.Keyring
	baseURL string
	hosted  bool
	sender  notify.Sender
	secure  bool // cookies get Secure; HSTS is sent
	proxies []netip.Prefix
	log     *slog.Logger
	ready   func(context.Context) error
	static  fs.FS
	now     func() time.Time
	rl      *rateLimiter

	// Password hashing hooks; tests swap in fast fakes and counters.
	hashPassword   func(string) string
	verifyPassword func(hash, pw string) (bool, error)
	dummyOnce      sync.Once
	dummyHash      string

	setupMu   sync.Mutex
	setupHash string // hash of the first-run setup code; empty when not in setup

	mux    *http.ServeMux
	routes []route
}

// New builds the server and registers every route.
func New(o Options) *Server {
	s := &Server{
		st: o.Store, limits: o.Limits, keyring: o.Keyring,
		baseURL: o.BaseURL, secure: strings.HasPrefix(o.BaseURL, "https://"),
		proxies: o.TrustedProxies, log: o.Logger, ready: o.Ready, static: o.Static, now: o.Now,
		hashPassword: auth.HashPassword, verifyPassword: auth.VerifyPassword,
		mux: http.NewServeMux(),
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
	if s.ready == nil {
		s.ready = func(context.Context) error { return nil }
	}
	s.rl = newRateLimiter(limits.Defaults[limits.LoginAttemptsPerMinute], 10000, s.now)
	s.rl.perMinute = bucketRates
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
