package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/auth"
	"github.com/NexusBetweenVoids/gunkly/internal/config"
	"github.com/NexusBetweenVoids/gunkly/internal/db"
	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/mail"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
	"github.com/NexusBetweenVoids/gunkly/internal/updates"
)

// app wires the long-lived components shared by serve and the CLI commands.
// Background workers (the scheduler) are started from serve and register a
// shutdown hook with onShutdown.
type app struct {
	cfg     *config.Config
	log     *slog.Logger
	db      *db.DB
	store   *store.Store
	keyring *auth.Keyring
	limits  *limits.Service
	updater *updates.Checker

	sqlitePath string // empty on Postgres
	dbExisted  bool   // the SQLite file existed before opening (snapshot worthwhile)
	hooks      []func(context.Context) error
}

// newApp loads configuration (refusing to start on invalid settings or a
// bad secret key) and opens the database. It does not migrate.
func newApp(ctx context.Context, logOut io.Writer) (*app, error) {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return nil, err
	}
	a := &app{cfg: cfg, log: slog.New(slog.NewJSONHandler(logOut, &slog.HandlerOptions{Level: cfg.LogLevel}))}
	secrets := make(map[uint32]string, len(cfg.SecretKeys))
	for v, k := range cfg.SecretKeys {
		if v < 1 || v > math.MaxUint32 {
			return nil, fmt.Errorf("secret keys: version %d out of range", v)
		}
		secrets[uint32(v)] = string(k)
	}
	if cfg.CurrentKey < 1 || cfg.CurrentKey > math.MaxUint32 {
		return nil, fmt.Errorf("secret keys: current version %d out of range", cfg.CurrentKey)
	}
	if a.keyring, err = auth.NewKeyring(uint32(cfg.CurrentKey), secrets); err != nil {
		return nil, fmt.Errorf("secret keys: %w", err)
	}
	if dir := os.Getenv("SQLITE_TMPDIR"); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil { //nolint:gosec // G703: operator-set path
			return nil, fmt.Errorf("SQLITE_TMPDIR: %w", err)
		}
	}
	if p, isSQLite := strings.CutPrefix(cfg.DatabaseURL, "sqlite://"); isSQLite {
		a.sqlitePath = p
		_, statErr := os.Stat(p)
		a.dbExisted = statErr == nil
	}
	if a.db, err = db.Open(ctx, cfg.DatabaseURL); err != nil {
		return nil, err
	}
	a.onShutdown(func(context.Context) error { return a.db.Close() })
	a.store = store.New(a.db)
	a.limits = limits.New(a.store)
	return a, nil
}

// smtp returns the mail settings when email is enabled, else nil.
func (a *app) smtp() *mail.Config {
	if !a.cfg.EmailEnabled {
		return nil
	}
	s := a.cfg.SMTP
	return &mail.Config{Host: s.Host, Port: s.Port, Username: s.Username, Password: s.Password, From: s.From, TLS: s.TLS}
}

// withApp runs fn with an app and closes it afterwards.
func withApp(ctx context.Context, logOut io.Writer, fn func(*app) error) error {
	a, err := newApp(ctx, logOut)
	if err != nil {
		return err
	}
	defer a.shutdown(context.Background())
	return fn(a)
}

// onShutdown registers a hook run at shutdown, after the HTTP server has
// drained. Hooks run in reverse order of registration (like defer), so the
// database, registered first, closes last.
func (a *app) onShutdown(fn func(context.Context) error) { a.hooks = append(a.hooks, fn) }

func (a *app) shutdown(ctx context.Context) {
	for _, h := range slices.Backward(a.hooks) {
		if err := h(ctx); err != nil {
			a.log.Error("shutdown hook", "err", err)
		}
	}
	a.hooks = nil
}

// requireCurrent fails if migrations are pending (CLI commands that touch
// data must not run against an old schema).
func (a *app) requireCurrent(ctx context.Context) error {
	pending, err := a.db.Pending(ctx)
	if err != nil {
		return err
	}
	if len(pending) > 0 {
		return fmt.Errorf("database migrations pending (%s): run `gunkly migrate` first", strings.Join(pending, ", "))
	}
	return nil
}

// migrate applies pending migrations, snapshotting an existing SQLite
// database to <data dir>/backups/pre-migrate-<timestamp>.db first.
func (a *app) migrate(ctx context.Context) error {
	pending, err := a.db.Pending(ctx)
	if err != nil || len(pending) == 0 {
		return err
	}
	if a.db.Dialect == db.SQLite && a.dbExisted {
		dir := filepath.Join(filepath.Dir(a.sqlitePath), "backups")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("pre-migration snapshot: %w", err)
		}
		dst := filepath.Join(dir, "pre-migrate-"+time.Now().UTC().Format("20060102T150405Z")+".db")
		if err := a.db.BackupFile(ctx, dst); err != nil {
			return fmt.Errorf("pre-migration snapshot: %w", err)
		}
		a.log.Info("pre-migration snapshot written", "path", dst)
	}
	if err := a.db.Migrate(ctx); err != nil {
		return err
	}
	a.log.Info("migrations applied", "versions", pending)
	return nil
}

// ready is /readyz: the database answers and the schema is current.
func (a *app) ready(ctx context.Context) error {
	if err := a.db.PingContext(ctx); err != nil {
		return err
	}
	pending, err := a.db.Pending(ctx)
	if err != nil {
		return err
	}
	if len(pending) > 0 {
		return errors.New("migrations pending")
	}
	return nil
}
