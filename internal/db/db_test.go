package db_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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

func TestSQLiteFilesOwnerOnly(t *testing.T) {
	ctx := context.Background()
	old := syscall.Umask(0)
	defer syscall.Umask(old)
	dir := t.TempDir()
	path := filepath.Join(dir, "g.db")
	d, err := db.Open(ctx, "sqlite://"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	snap := filepath.Join(dir, "snap.db")
	if err := d.BackupFile(ctx, snap); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{path, path + "-wal", path + "-shm", snap} {
		fi, err := os.Stat(p)
		if os.IsNotExist(err) && p != path && p != snap {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %o, want 600", filepath.Base(p), fi.Mode().Perm())
		}
	}
}

// TestOAuthAttributionMigrationKeepsData applies every migration before
// 0014, fills the tables the SQLite rebuild of completions touches, applies
// 0014 and checks nothing was lost: rows, undo tokens, indexes, foreign keys.
func TestOAuthAttributionMigrationKeepsData(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, dbtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.MigrateBefore(ctx, "0014"); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := d.ExecContext(ctx, d.Dialect.Rebind(q), args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO users (id, username, username_lower, display_name, password_hash, password_changed_at, created_at, last_active_at)
		VALUES ('u1', 'u', 'u', 'u', 'x', 1, 1, 1)`)
	exec("INSERT INTO households (id, name, timezone, created_at) VALUES ('h1', 'H', 'UTC', 1)")
	exec(`INSERT INTO chores (id, household_id, name, kind, schedule_type, interval_n, interval_unit, anchor, first_due, due_date, created_at, updated_at)
		VALUES ('c1', 'h1', 'C', 'recurring', 'interval', 1, 'day', 'from_completion', '2026-01-01', '2026-01-01', 1, 1)`)
	for i, src := range []string{"app", "api_token", "import"} {
		exec(`INSERT INTO completions (id, chore_id, user_id, completed_at, source, token_id, note, created_at)
			VALUES (?, 'c1', 'u1', ?, ?, 'tok', 'n', 1)`, "k"+src, i+1, src)
	}
	exec("INSERT INTO undo_tokens (token_hash, completion_id, created_at, expires_at) VALUES ('th', 'kapp', 1, 2)")
	if _, err := d.ExecContext(ctx, "INSERT INTO oauth_clients (id, name, redirect_uris, created_at) VALUES ('oc', 'n', '[]', 1)"); err != nil {
		t.Fatal(err)
	}

	if err := d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	count := func(q string) int {
		t.Helper()
		var n int
		if err := d.QueryRowContext(ctx, q).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}
	if n := count("SELECT count(*) FROM completions WHERE note = 'n' AND token_id = 'tok' AND user_id = 'u1'"); n != 3 {
		t.Fatalf("completions kept: %d", n)
	}
	if n := count("SELECT count(*) FROM undo_tokens WHERE completion_id = 'kapp'"); n != 1 {
		t.Fatalf("undo tokens kept: %d", n)
	}
	if n := count("SELECT count(*) FROM oauth_clients WHERE reg_key = ''"); n != 1 {
		t.Fatalf("clients kept: %d", n)
	}
	exec(`INSERT INTO completions (id, chore_id, completed_at, source, token_id, created_at) VALUES ('koauth', 'c1', 9, 'oauth', 'grant', 1)`)
	if _, err := d.ExecContext(ctx, `INSERT INTO completions (id, chore_id, completed_at, source, created_at) VALUES ('kbad', 'c1', 9, 'bogus', 1)`); err == nil {
		t.Fatal("the CHECK no longer rejects an unknown source")
	}
	// Indexes and foreign keys survived: deleting the chore removes its completions and their undo tokens.
	for _, ix := range []string{"completions_chore", "completions_user"} {
		q := "SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = '" + ix + "'"
		if d.Dialect == db.Postgres {
			q = "SELECT count(*) FROM pg_indexes WHERE indexname = '" + ix + "' AND schemaname = current_schema()"
		}
		if count(q) != 1 {
			t.Errorf("index %s missing", ix)
		}
	}
	exec("DELETE FROM chores WHERE id = 'c1'")
	if n := count("SELECT count(*) FROM completions") + count("SELECT count(*) FROM undo_tokens"); n != 0 {
		t.Fatalf("cascade lost: %d rows left", n)
	}
}
