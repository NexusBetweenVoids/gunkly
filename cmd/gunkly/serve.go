package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/api"
	"github.com/NexusBetweenVoids/gunkly/internal/config"
	"github.com/NexusBetweenVoids/gunkly/internal/egress"
	"github.com/NexusBetweenVoids/gunkly/internal/geoip"
	"github.com/NexusBetweenVoids/gunkly/internal/metrics"
	"github.com/NexusBetweenVoids/gunkly/internal/scheduler"
	"github.com/NexusBetweenVoids/gunkly/internal/sso"
	"github.com/NexusBetweenVoids/gunkly/internal/updates"
	"github.com/NexusBetweenVoids/gunkly/web"
)

// ssoConfig is the single sign-on setup from GUNKLY_OIDC_*, or nil when off.
func (a *app) ssoConfig() *sso.Config {
	o := a.cfg.OIDC
	if o.Issuer == "" {
		return nil
	}
	return &sso.Config{Issuer: o.Issuer, ClientID: o.ClientID, ClientSecret: o.ClientSecret, Name: o.Name, Scopes: o.Scopes}
}

// geoIP opens the optional location databases (GUNKLY_GEOIP_*_DB); a file that
// cannot be read is a start-up error rather than a silently missing feature.
func (a *app) geoIP() (api.GeoIP, error) {
	g := a.cfg.GeoIP
	out := api.GeoIP{CountryHeader: g.CountryHeader, ASNHeader: g.ASNHeader}
	var err error
	if g.CountryDB != "" {
		if out.CountryDB, err = geoip.Open(g.CountryDB); err != nil {
			return out, fmt.Errorf("GUNKLY_GEOIP_COUNTRY_DB: %w", err)
		}
	}
	if g.ASNDB != "" {
		if out.ASNDB, err = geoip.Open(g.ASNDB); err != nil {
			return out, fmt.Errorf("GUNKLY_GEOIP_ASN_DB: %w", err)
		}
	}
	return out, nil
}

func serve(ctx context.Context, logOut io.Writer) error {
	a, err := newApp(ctx, logOut)
	if err != nil {
		return err
	}
	defer a.shutdown(context.Background())
	a.log.Info("starting gunkly", "version", version, "db", a.db.Dialect.String())

	if a.cfg.AutoMigrate {
		if err := a.migrate(ctx); err != nil {
			return err
		}
	} else if err := a.requireCurrent(ctx); err != nil {
		a.log.Error("refusing to start", "err", err)
		return err
	}

	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		return err
	}
	// Always built: the token can also be set in Administration, and /metrics
	// answers 404 while no token is configured.
	reg := a.newMetrics()
	a.updater = &updates.Checker{Store: a.store, Client: egress.NewClient(egress.Guard{Enabled: true}), Log: a.log}
	geo, err := a.geoIP()
	if err != nil {
		return err
	}
	srv := api.New(api.Options{
		GeoIP:   geo,
		Env:     a.cfg.Overrides,
		Updates: a.updater,
		Store:   a.store, Limits: a.limits, Keyring: a.keyring, BaseURL: a.cfg.BaseURL,
		TrustedProxies: a.cfg.TrustedProxies, Logger: a.log, Ready: a.ready, Static: dist, HostedMode: a.cfg.HostedMode,
		PrivacyURL: a.cfg.PrivacyURL, TermsURL: a.cfg.TermsURL, SecurityContact: a.cfg.SecurityContact, SecurityPolicy: a.cfg.SecurityPolicy,
		Metrics: reg, MetricsToken: a.cfg.MetricsToken, Version: version,
		PoWMaxNumber: a.cfg.PoWMaxNumber, SMTP: a.smtp(), SSO: a.ssoConfig(),
	})
	if err := srv.InitSetup(ctx); err != nil {
		return err
	}
	httpSrv := &http.Server{
		Addr:              a.cfg.ListenAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(a.log.Handler(), slog.LevelWarn),
	}

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if !a.cfg.DisableScheduler {
		a.startScheduler(ctx, srv, reg)
	}
	a.startUpdateCheck(ctx)

	errc := make(chan error, 1)
	go func() { errc <- httpSrv.ListenAndServe() }()
	a.log.Info("listening", "addr", a.cfg.ListenAddr)
	select {
	case err := <-errc:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}
	a.log.Info("shutting down", "timeout", a.cfg.ShutdownTimeout.String())
	sctx, cancel := context.WithTimeout(context.Background(), a.cfg.ShutdownTimeout)
	defer cancel()
	if err := httpSrv.Shutdown(sctx); err != nil {
		a.log.Error("http drain incomplete", "err", err)
	}
	a.shutdown(sctx)
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	a.log.Info("stopped")
	return nil
}

// healthcheck probes /healthz on the local listener (the distroless image
// has no curl). listen is GUNKLY_LISTEN_ADDR (default :4865).
func healthcheck(ctx context.Context, listen string) error {
	url, err := healthURL(listen)
	if err != nil {
		return err
	}
	// Served under a path prefix (GUNKLY_BASE_URL=https://host/gunkly): so is /healthz.
	url = strings.Replace(url, "/healthz", config.BasePathOf(os.Getenv("GUNKLY_BASE_URL"))+"/healthz", 1)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil) //nolint:gosec // G704: local probe built from operator config
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // G704: as above
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz: status %d", resp.StatusCode)
	}
	return nil
}

func healthURL(listen string) (string, error) {
	if strings.TrimSpace(listen) == "" {
		listen = ":4865"
	}
	host, port, err := splitHostPort(listen)
	if err != nil {
		return "", err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return "http://" + host + ":" + port + "/healthz", nil
}

// startScheduler runs the reminder worker until shutdown. Its hook (run after
// the HTTP drain, before the database closes) stops new sends, waits for
// in-flight ones and releases the Postgres lock, within the shutdown timeout.
func (a *app) startScheduler(ctx context.Context, srv *api.Server, reg *metrics.Registry) {
	cfg := scheduler.Config{
		Store: a.store, DB: a.db, Keyring: a.keyring, Limits: a.limits, BaseURL: a.cfg.BaseURL, Logger: a.log,
		// The server's sender and allowlist follow the admin settings (egress
		// guard, enabled notification kinds), read at send time.
		Sender: srv.Sender(), EnabledKinds: srv.EnabledKinds, Pusher: srv.Pusher(), Mailer: srv.Mailer(), Hosted: a.cfg.HostedMode, Webhooks: srv.WebhookSender(),
	}
	cfg.OnQuotaHit = srv.NotificationLimitReached
	if reg != nil {
		cfg.OnDelivery = reg.Delivery
	}
	w := scheduler.New(cfg)
	wctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := w.Run(wctx); err != nil {
			a.log.Error("scheduler stopped", "err", err)
		}
	}()
	a.onShutdown(func(sctx context.Context) error {
		cancel()
		select {
		case <-done:
			return nil
		case <-sctx.Done():
			return fmt.Errorf("scheduler: %w", sctx.Err())
		}
	})
}

// newMetrics builds the Prometheus registry with the gauges read at scrape
// time: scheduler tick age (from the database, so any instance reports the
// leader's progress), delivery queue depth and open database connections.
func (a *app) newMetrics() *metrics.Registry {
	reg := metrics.New(version)
	reg.AddGauge("gunkly_scheduler_last_tick_age_seconds", "Seconds since the scheduler last processed a window (absent if it never ran).",
		func(ctx context.Context) (float64, error) {
			at, err := a.store.LastSchedulerTick(ctx)
			if err != nil || at.IsZero() {
				return 0, errors.New("no tick")
			}
			return max(0, time.Since(at).Seconds()), nil
		})
	reg.AddGauge("gunkly_delivery_queue_depth", "Reminder deliveries waiting to be sent or retried.",
		func(ctx context.Context) (float64, error) {
			n, err := a.store.PendingDeliveries(ctx)
			return float64(n), err
		})
	reg.AddGauge("gunkly_db_open_connections", "Open database connections (in use and idle).",
		func(context.Context) (float64, error) { return float64(a.db.Stats().OpenConnections), nil })
	return reg
}

// startUpdateCheck runs (a.updater, also used by the Updates page for refreshes) the background release check (admin Updates page). It
// uses the always-guarded egress client and asks only if the admin has not
// switched the check off.
func (a *app) startUpdateCheck(ctx context.Context) {
	c := a.updater
	uctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Run(uctx)
	}()
	a.onShutdown(func(sctx context.Context) error {
		cancel()
		select {
		case <-done:
			return nil
		case <-sctx.Done():
			return fmt.Errorf("update check: %w", sctx.Err())
		}
	})
}
