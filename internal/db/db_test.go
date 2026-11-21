package db_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NexusBetweenVoids/gunkly/internal/db"
	"github.com/NexusBetweenVoids/gunkly/internal/db/dbtest"
)

func TestRebind(t *testing.T) {
	q := "SELECT a FROM t WHERE x = ? AND y IN (?, ?)"
	if got := db.SQLite.Rebind(q); got != q {
		t.Errorf("sqlite rebind changed query: %q", got)
	}
	if got, want := db.Postgres.Rebind(q), "SELECT a FROM t WHERE x = $1 AND y IN ($2, $3)"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
	if got := db.Postgres.Rebind("SELECT 1"); got != "SELECT 1" {
		t.Errorf("got %q", got)
	}
	// Documented limitation: a literal '?' is rewritten too. Queries must
	// pass such values as parameters.
	if got := db.Postgres.Rebind("SELECT '?' WHERE a = ?"); got != "SELECT '$1' WHERE a = $2" {
		t.Errorf("got %q", got)
	}
}

func TestMigrateIdempotent(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, dbtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	pending, err := d.Pending(ctx)
	if err != nil || len(pending) == 0 || pending[0] != "0001_init" {
		t.Fatalf("pending before = %v, %v", pending, err)
	}
	for i := 0; i < 2; i++ {
		if err := d.Migrate(ctx); err != nil {
			t.Fatalf("migrate #%d: %v", i+1, err)
		}
	}
	if pending, err = d.Pending(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("pending after = %v, %v", pending, err)
	}
	var n int
	if err := d.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations").Scan(&n); err != nil || n < 1 {
		t.Fatalf("schema_migrations rows = %d, %v", n, err)
	}
	// Spot-check a few tables exist.
	for _, tbl := range []string{"users", "households", "memberships", "chores", "completions", "deliveries", "audit_log"} {
		if _, err := d.ExecContext(ctx, "SELECT count(*) FROM "+tbl); err != nil {
			t.Errorf("table %s: %v", tbl, err)
		}
	}
}

func TestConcurrentMigrate(t *testing.T) {
	ctx := context.Background()
	url := dbtest.URL(t)
	// Open sequentially (production opens a SQLite file once; simultaneous
	// first opens can race on the WAL switch), then migrate concurrently.
	dbs := make([]*db.DB, 3)
	for i := range dbs {
		d, err := db.Open(ctx, url)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = d.Close() })
		dbs[i] = d
	}
	errs := make(chan error, len(dbs))
	for _, d := range dbs {
		go func() { errs <- d.Migrate(ctx) }()
	}
	for range dbs {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}

func TestSQLitePragmas(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	// Check on several distinct connections: pragmas must apply to each.
	var conns []interface{ Close() error }
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()
	for i := 0; i < 3; i++ {
		c, err := d.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
		for pragma, want := range map[string]string{
			"auto_vacuum":  "2", // INCREMENTAL
			"journal_mode": "wal",
			"busy_timeout": "5000",
			"foreign_keys": "1",
			"synchronous":  "1", // NORMAL
		} {
			var got string
			if err := c.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if !strings.EqualFold(got, want) {
				t.Errorf("conn %d: %s = %s, want %s", i, pragma, got, want)
			}
		}
	}
	if err := d.IncrementalVacuum(ctx, 10); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteBackup(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	d, err := db.Open(ctx, "sqlite://"+filepath.Join(dir, "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, "INSERT INTO settings (key, value, updated_at) VALUES ('k', 'v', 1)"); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := d.Backup(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "streamed.db")
	if err := os.WriteFile(out, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".gunkly-backup-*")); len(left) != 0 {
		t.Errorf("staging not cleaned up: %v", left)
	}
	for _, path := range []string{out, filepath.Join(dir, "it's.db")} {
		if path != out {
			if err := d.BackupFile(ctx, path); err != nil {
				t.Fatal(err)
			}
		}
		b, err := db.Open(ctx, "sqlite://"+path)
		if err != nil {
			t.Fatal(err)
		}
		var v string
		err = b.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = 'k'").Scan(&v)
		b.Close()
		if err != nil || v != "v" {
			t.Fatalf("%s: value = %q, %v", path, v, err)
		}
	}
}

func TestOpenRejects(t *testing.T) {
	for _, u := range []string{"mysql://x", "sqlite://", "sqlite://:memory:", "sqlite://a.db?mode=ro"} {
		if d, err := db.Open(context.Background(), u); err == nil {
			d.Close()
			t.Errorf("%s: expected error", u)
		}
	}
}
