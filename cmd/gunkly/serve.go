package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/api"
	"github.com/NexusBetweenVoids/gunkly/web"
)

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
	srv := api.New(api.Options{
		Store: a.store, Limits: a.limits, Keyring: a.keyring, BaseURL: a.cfg.BaseURL,
		TrustedProxies: a.cfg.TrustedProxies, Logger: a.log, Ready: a.ready, Static: dist,
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
	// TODO(scheduler): if !a.cfg.DisableScheduler, start the scheduler here and a.onShutdown(its stop).

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
// has no curl). listen is GUNKLY_LISTEN_ADDR (default :8080).
func healthcheck(ctx context.Context, listen string) error {
	url, err := healthURL(listen)
	if err != nil {
		return err
	}
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
		listen = ":8080"
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
