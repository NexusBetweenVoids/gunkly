package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// migrateLockKey is the Postgres advisory lock held while migrating, so
// containers starting concurrently cannot race. Arbitrary constant ("gunkly").
const migrateLockKey = 0x67756e6b6c79

type migration struct {
	version string // file base name without extension, e.g. 0001_init
	sql     string
}

// loadMigrations returns migrations in order for dialect d. Each version is
// NNNN_name.sql; a NNNN_name.<dialect>.sql file replaces it for that dialect
// only. Dialect-only files without a shared base are also allowed.
func loadMigrations(fsys fs.FS, d Dialect) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	chosen := map[string]string{} // version -> file
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".sql")
		if !ok || e.IsDir() {
			continue
		}
		version, dialect, hasDialect := strings.Cut(name, ".")
		if hasDialect && dialect != SQLite.String() && dialect != Postgres.String() {
			return nil, fmt.Errorf("db: migration %s: unknown dialect suffix", e.Name())
		}
		if hasDialect && dialect != d.String() {
			continue
		}
		if _, exists := chosen[version]; exists && !hasDialect {
			continue // a dialect override already won
		}
		chosen[version] = e.Name()
	}
	out := make([]migration, 0, len(chosen))
	for v, f := range chosen {
		b, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: v, sql: string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

func (db *DB) migrations() ([]migration, error) {
	sub, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	return loadMigrations(sub, db.Dialect)
}

// Pending lists migration versions not yet applied. Used to refuse to start
// when AUTO_MIGRATE is off.
func (db *DB) Pending(ctx context.Context) ([]string, error) {
	ms, err := db.migrations()
	if err != nil {
		return nil, err
	}
	applied, err := appliedVersions(ctx, db.DB, db.Dialect)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, m := range ms {
		if !applied[m.version] {
			out = append(out, m.version)
		}
	}
	return out, nil
}

type querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func appliedVersions(ctx context.Context, q querier, d Dialect) (map[string]bool, error) {
	var exists bool
	check := "SELECT count(*) > 0 FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'"
	if d == Postgres {
		check = "SELECT to_regclass('schema_migrations') IS NOT NULL"
	}
	if err := q.QueryRowContext(ctx, check).Scan(&exists); err != nil {
		return nil, fmt.Errorf("db: read migrations: %w", err)
	}
	applied := map[string]bool{}
	if !exists {
		return applied, nil
	}
	rows, err := q.QueryContext(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("db: read migrations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

// Migrate applies all pending migrations, each in its own transaction. On
// Postgres it holds an advisory lock for the duration. It never rolls back
// applied migrations.
func (db *DB) Migrate(ctx context.Context) error {
	ms, err := db.migrations()
	if err != nil {
		return err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if db.Dialect == Postgres {
		if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", migrateLockKey); err != nil {
			return fmt.Errorf("db: migration lock: %w", err)
		}
		defer func() {
			_, _ = conn.ExecContext(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrateLockKey)
		}()
	}
	if _, err := conn.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at BIGINT NOT NULL)"); err != nil {
		return fmt.Errorf("db: create schema_migrations: %w", err)
	}
	applied, err := appliedVersions(ctx, conn, db.Dialect)
	if err != nil {
		return err
	}
	for _, m := range ms {
		if applied[m.version] {
			continue
		}
		if err := db.apply(ctx, conn, m); err != nil {
			return fmt.Errorf("db: migration %s: %w", m.version, err)
		}
	}
	return db.secureFiles()
}

func (db *DB) apply(ctx context.Context, conn *sql.Conn, m migration) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit
	// Re-check inside the (write-locked) transaction: another SQLite process
	// may have applied it since we read the list.
	var n int
	if err := tx.QueryRowContext(ctx, db.Dialect.Rebind("SELECT count(*) FROM schema_migrations WHERE version = ?"), m.version).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, db.Dialect.Rebind("INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)"), m.version, time.Now().UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}
