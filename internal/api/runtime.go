package api

import (
	"cmp"
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	netmail "net/mail"
	"strconv"
	"strings"

	"github.com/NexusBetweenVoids/gunkly/internal/auth"
	"github.com/NexusBetweenVoids/gunkly/internal/egress"
	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/mail"
	"github.com/NexusBetweenVoids/gunkly/internal/scheduler"
	"github.com/NexusBetweenVoids/gunkly/internal/sso"
)

// runtimeServices are the parts of the server that follow admin configuration
// (email, single sign-on, the metrics token, the sign-up proof-of-work
// difficulty). They are rebuilt as a whole when the configuration changes and
// swapped atomically; a request reads the snapshot once (s.cur()) and keeps
// using it, so a change never leaves it half-configured.
type runtimeServices struct {
	mailer      Mailer        // nil: email is off
	sso         *sso.Provider // nil: single sign-on is off (always in hosted mode)
	pow         *auth.PoW
	metricsHash [sha256.Size]byte
	metricsOn   bool
	// Why a configured service is not running (shown to admins): "" when fine.
	emailProblem, ssoProblem string
}

func (s *Server) cur() *runtimeServices { return s.rt.Load() }

// envServices is what the environment configured (Options), validated at
// start-up by internal/config; it wins over stored values.
type envServices struct {
	smtp         *mail.Config
	sso          *sso.Config
	metricsToken string
	powMax       int
	testMailer   Mailer // tests replace the SMTP sender
}

// applyConfig rebuilds the runtime services from the environment and the
// stored configuration. Called at start-up (InitSetup) and after every change
// in Administration.
func (s *Server) applyConfig(ctx context.Context) error {
	cfg, err := s.config(ctx, true)
	if err != nil {
		return err
	}
	// Options given directly (tests, or no Env) count as set by the environment.
	env := s.envOnlyConfig()
	for k, on := range env.fromEnv {
		if on {
			cfg.fromEnv[k] = true
		}
	}
	s.rt.Store(s.buildServices(cfg))
	return nil
}

// envOnlyConfig is the configuration before the database has been read: only
// what the environment sets.
func (s *Server) envOnlyConfig() cfgSnapshot {
	out := cfgSnapshot{vals: map[string]string{}, fromEnv: map[string]bool{}, stored: map[string]bool{}}
	envGroup := s.envGroups()
	for _, f := range cfgFields {
		v, ok := s.envValue(f)
		if !ok && envGroup[f.group] {
			v, ok = s.env[f.env], true
		}
		out.fromEnv[f.key] = ok || envGroup[f.group]
		out.vals[f.key] = f.def
		if ok {
			out.vals[f.key] = v
		}
	}
	// Options set directly (tests, or a caller without Env) count as the environment.
	if s.envSvc.smtp != nil {
		out.fromEnv["email.enabled"] = true
	}
	if s.envSvc.sso != nil {
		out.fromEnv["sso.issuer"] = true
	}
	if s.envSvc.metricsToken != "" {
		out.fromEnv["metrics.token"] = true
	}
	if s.envSvc.powMax != 0 {
		out.fromEnv["signup.pow_max"] = true
	}
	return out
}

func (s *Server) buildServices(cfg cfgSnapshot) *runtimeServices {
	rs := &runtimeServices{}
	env := s.envSvc

	// Email.
	switch {
	case env.testMailer != nil:
		rs.mailer = env.testMailer
	case cfg.fromEnv["email.enabled"] || cfg.fromEnv["email.host"]:
		if env.smtp != nil {
			rs.mailer = mail.New(*env.smtp, s.EgressGuardEnabled)
		}
	case cfg.on("email.enabled"):
		port, _ := strconv.Atoi(cfg.str("email.port"))
		mc := mail.Config{Host: cfg.str("email.host"), Port: port, Username: cfg.str("email.username"),
			Password: cfg.str("email.password"), From: cfg.str("email.from"), TLS: cmp.Or(cfg.str("email.tls"), "starttls")}
		if mc.Host == "" || mc.From == "" {
			rs.emailProblem = "incomplete"
		} else {
			rs.mailer = mail.New(mc, s.EgressGuardEnabled)
		}
	}

	// Single sign-on (never in hosted mode).
	var sc *sso.Config
	switch {
	case s.hosted:
	case cfg.fromEnv["sso.issuer"]:
		sc = env.sso
	case cfg.str("sso.issuer") != "" || cfg.str("sso.client_id") != "":
		if cfg.str("sso.issuer") == "" || cfg.str("sso.client_id") == "" {
			rs.ssoProblem = "incomplete"
			break
		}
		if !s.secure && !localBase(s.baseURL) {
			rs.ssoProblem = "needs_https"
			break
		}
		scopes := strings.Fields(cfg.str("sso.scopes"))
		if !contains(scopes, "openid") {
			scopes = append([]string{"openid"}, scopes...)
		}
		sc = &sso.Config{Issuer: cfg.str("sso.issuer"), ClientID: cfg.str("sso.client_id"), ClientSecret: cfg.str("sso.client_secret"), Scopes: scopes}
	}
	if sc != nil {
		c := *sc
		c.RedirectURL = s.baseURL + "/api/v1/auth/oidc/callback"
		// Keep the current provider (and its cached discovery) when nothing changed.
		if old := s.cur(); old != nil && old.sso != nil && sameSSO(old.sso, c) {
			rs.sso = old.sso
		} else {
			rs.sso = sso.New(c, egress.NewIdPClient(c.Issuer, s.EgressGuardEnabled), s.now)
		}
	}

	// Metrics token.
	token := cfg.str("metrics.token")
	if cfg.fromEnv["metrics.token"] {
		token = env.metricsToken
	}
	if token != "" {
		rs.metricsOn, rs.metricsHash = true, sha256.Sum256([]byte(token))
	}

	// Sign-up proof of work.
	powMax := 0
	if cfg.fromEnv["signup.pow_max"] {
		powMax = env.powMax
	} else if n, err := strconv.Atoi(cfg.str("signup.pow_max")); err == nil {
		powMax = n
	}
	rs.pow = auth.NewPoW(s.keyring.DeriveKey("gunkly signup proof of work v1"), cmp.Or(powMax, auth.DefaultPoWMaxNumber))
	return rs
}

func sameSSO(p *sso.Provider, c sso.Config) bool {
	return p.Issuer() == c.Issuer && p.ClientID() == c.ClientID && p.RedirectURL() == c.RedirectURL &&
		strings.Join(p.Scopes(), " ") == strings.Join(c.Scopes, " ") && p.SecretMatches(c.ClientSecret)
}

func localBase(base string) bool {
	return strings.HasPrefix(base, "http://localhost") || strings.HasPrefix(base, "http://127.0.0.1") || strings.HasPrefix(base, "http://[::1]")
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

var errEmailOff = errors.New("email is not configured")

// dynMailer is the mailer handed to the scheduler: it always sends through the
// current configuration.
type dynMailer struct{ s *Server }

func (d dynMailer) Send(ctx context.Context, m mail.Message) error {
	ml := d.s.cur().mailer
	if ml == nil {
		return errEmailOff
	}
	return ml.Send(ctx, m)
}

// Enabled lets the scheduler skip email while it is off.
func (d dynMailer) Enabled() bool { return d.s.cur().mailer != nil }

// adminServiceStatus tells the admin screens whether email and single sign-on
// are running, and why not when they are configured but cannot run.
func (s *Server) adminServiceStatus(w http.ResponseWriter, _ *http.Request, _ *call) error {
	rt := s.cur()
	writeJSON(w, http.StatusOK, map[string]any{
		"emailOn": rt.mailer != nil, "emailProblem": rt.emailProblem,
		"ssoOn": rt.sso != nil, "ssoProblem": rt.ssoProblem, "ssoAvailable": !s.hosted,
		"ssoCallback": s.baseURL + "/api/v1/auth/oidc/callback",
		"metricsOn":   rt.metricsOn,
	})
	return nil
}

// adminTestEmail sends a test message to the given address with the current
// email settings, synchronously, so the admin sees the error at once. It
// counts against the admin's own hourly email quota.
func (s *Server) adminTestEmail(w http.ResponseWriter, r *http.Request, c *call) error {
	var in struct {
		To string `json:"to"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	addr, err := netmail.ParseAddress(strings.TrimSpace(in.To))
	if err != nil || len(in.To) > 320 {
		return errInvalid
	}
	ml := s.cur().mailer
	if ml == nil {
		return errf(http.StatusConflict, "email_disabled")
	}
	if wait := s.mailQuota(r.Context(), c.user.ID, "", limits.EmailsAccountHour); wait > 0 {
		return tooMany(w, wait)
	}
	err = ml.Send(r.Context(), mail.Message{To: addr.Address, Subject: "Gunkly test email",
		Body: "This is a test email from your Gunkly server. If you can read this, email works.\n"})
	out := map[string]any{"ok": err == nil}
	if err != nil {
		out["error"] = "send_failed"
		s.log.Warn("test email failed", "reason", scheduler.MailErrReason(err)) // the error can carry the address
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// adminTestSSO fetches the provider's discovery document with the current
// settings: it proves the issuer is reachable and answers as an OpenID
// provider (the client ID and secret are only checked by a real sign-in).
func (s *Server) adminTestSSO(w http.ResponseWriter, r *http.Request, _ *call) error {
	sp := s.cur().sso
	if sp == nil {
		return errf(http.StatusConflict, "sso_disabled")
	}
	origin := sp.AuthOrigin(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"ok": origin != "", "authOrigin": origin})
	return nil
}
