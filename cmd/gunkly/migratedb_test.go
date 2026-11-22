package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/db"
	"github.com/NexusBetweenVoids/gunkly/internal/db/dbtest"
	"github.com/NexusBetweenVoids/gunkly/internal/schedule"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// populatedSQLite builds a SQLite database through the store with data in
// many tables (users incl. booleans, households, chores, completions,
// sealed channels, sessions, audit, settings, feeds) and returns its URL.
func populatedSQLite(t *testing.T) (url, path string) {
	t.Helper()
	ctx := context.Background()
	path = filepath.Join(t.TempDir(), "gunkly.db")
	d, err := db.Open(ctx, "sqlite://"+path)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	st := store.New(d)
	ann, err := st.CreateUser(ctx, store.NewUser{Username: "ann", Email: "ann@example.com", DisplayName: "Ann Ünïcode", PasswordHash: "hash-ann", IsAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := st.CreateUser(ctx, store.NewUser{Username: "bob", DisplayName: "Bob", PasswordHash: "hash-bob"})
	if err != nil {
		t.Fatal(err)
	}
	a := store.Actor{UserID: ann.ID}
	h, err := st.CreateHousehold(ctx, a, store.NewHousehold{Name: "Home", Timezone: "Europe/Amsterdam", StarterAreas: []string{"Kitchen", "Garden"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddMember(ctx, a, h.ID, bob.ID, store.RoleMember); err != nil {
		t.Fatal(err)
	}
	next := time.Date(2026, 11, 28, 0, 0, 0, 0, time.UTC)
	c, err := st.CreateChore(ctx, a, h.ID, store.ChoreInput{Name: "Gutters", Notes: "tall ladder", Kind: store.KindRecurring,
		ScheduleType: store.ScheduleInterval, IntervalN: 1, IntervalUnit: "week", Anchor: store.AnchorFromCompletion,
		FirstDue: "2026-11-21", DueDate: "2026-11-21", NextDue: next, Assignees: []string{bob.ID}, MaxChores: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetChoreReminders(ctx, a, h.ID, c.ID, "custom", []schedule.Reminder{{Kind: schedule.OnDue, Minute: 540}}, 10); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"one", "two"} {
		if _, _, err := st.CompleteChoreAuto(ctx, store.Actor{UserID: bob.ID}, h.ID, c.ID, n); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.CreateUserChannel(ctx, a, store.NewChannel{ID: "chan-ann", Service: "ntfy", Ciphertext: "v1:sealed-bytes", Label: "Phone", Hint: "…abcd", Max: 5}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateHouseholdChannel(ctx, a, h.ID, store.NewChannel{ID: "chan-hh", Service: "discord", Ciphertext: "v1:other", Label: "Family", Max: 5}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateSession(ctx, store.NewSession{UserID: ann.ID, TokenHash: "tokhash", Label: "Firefox/Linux", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := st.Audit(ctx, store.AuditEvent{ActorUserID: ann.ID, HouseholdID: h.ID, Event: store.AuditHouseholdCreate, Meta: map[string]string{"k": "v"}}); err != nil {
		t.Fatal(err)
	}
	min := 12
	if err := st.UpdateInstanceSettings(ctx, store.SettingsUpdate{MinPasswordLength: &min, Limits: map[string]int{"chores_per_household": 77}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CreateFeed(ctx, a, h.ID, 5); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil { // checkpoints the WAL into the main file
		t.Fatal(err)
	}
	return "sqlite://" + path, path
}

func pgOnly(t *testing.T) {
	t.Helper()
	if os.Getenv("GUNKLY_TEST_DB") != "postgres" {
		t.Skip("migrate-db needs a Postgres target (GUNKLY_TEST_DB=postgres)")
	}
}

func fileSum(t *testing.T, path string) [32]byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(b)
}

func tableCounts(t *testing.T, d *db.DB) map[string]int {
	t.Helper()
	ctx := context.Background()
	q := "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'"
	if d.Dialect == db.Postgres {
		q = "SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() AND table_type = 'BASE TABLE'"
	}
	rows, err := d.QueryContext(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	rows.Close()
	out := map[string]int{}
	for _, n := range names {
		var c int
		if err := d.QueryRowContext(ctx, `SELECT count(*) FROM "`+n+`"`).Scan(&c); err != nil {
			t.Fatal(err)
		}
		out[n] = c
	}
	return out
}

func TestMigrateDB(t *testing.T) {
	pgOnly(t)
	ctx := context.Background()
	srcURL, srcPath := populatedSQLite(t)
	before := fileSum(t, srcPath)
	dstURL := dbtest.URL(t)

	// A target that holds only an empty schema_migrations table counts as empty.
	pre, err := db.Open(ctx, dstURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pre.ExecContext(ctx, "CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at BIGINT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	pre.Close()

	var out bytes.Buffer
	if err := migrateDB(ctx, srcURL, dstURL, &out); err != nil {
		t.Fatalf("migrate-db: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "Copied and verified") || strings.Contains(out.String(), "postgres://") {
		t.Fatalf("output: %s", out.String())
	}
	if fileSum(t, srcPath) != before {
		t.Fatal("the SQLite file was modified")
	}

	// Counts agree on every table, and the app's store reads the same data on Postgres.
	src, err := db.OpenSQLiteReadOnly(ctx, srcURL)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := db.Open(ctx, dstURL)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	sc, dc := tableCounts(t, src), tableCounts(t, dst)
	if len(sc) < 20 || len(sc) != len(dc) {
		t.Fatalf("tables: source %d, target %d", len(sc), len(dc))
	}
	for n, c := range sc {
		if dc[n] != c {
			t.Errorf("table %s: source %d rows, target %d", n, c, dc[n])
		}
	}
	if pending, err := dst.Pending(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("target pending %v %v", pending, err)
	}
	ss, ds := store.New(src), store.New(dst)
	ann, err := ds.UserByLogin(ctx, "ANN")
	if err != nil || !ann.IsAdmin || ann.DisplayName != "Ann Ünïcode" || ann.Email != "ann@example.com" {
		t.Fatalf("user on postgres: %+v %v", ann, err)
	}
	hs, err := ds.ListHouseholds(ctx, store.Actor{UserID: ann.ID})
	if err != nil || len(hs) != 1 || hs[0].Name != "Home" {
		t.Fatalf("households on postgres: %+v %v", hs, err)
	}
	cs, err := ds.ListChores(ctx, store.Actor{UserID: ann.ID}, hs[0].ID, true)
	if err != nil || len(cs) != 1 || len(cs[0].Assignees) != 1 || cs[0].Notes != "tall ladder" {
		t.Fatalf("chores on postgres: %+v %v", cs, err)
	}
	hist, err := ds.ListCompletions(ctx, store.Actor{UserID: ann.ID}, hs[0].ID, cs[0].ID, 10)
	if err != nil || len(hist) != 2 {
		t.Fatalf("history on postgres: %+v %v", hist, err)
	}
	want, err := ss.UserChannelCiphertext(ctx, store.Actor{UserID: ann.ID}, "chan-ann")
	got, err2 := ds.UserChannelCiphertext(ctx, store.Actor{UserID: ann.ID}, "chan-ann")
	if err != nil || err2 != nil || want != "v1:sealed-bytes" || got != want {
		t.Fatalf("channel ciphertext %q %q %v %v", want, got, err, err2)
	}
	is, err := ds.InstanceSettings(ctx)
	if err != nil || is.MinPasswordLength != 12 || is.Limits["chores_per_household"] != 77 {
		t.Fatalf("settings on postgres: %+v %v", is, err)
	}

	// A second run refuses: the target is no longer empty.
	out.Reset()
	err = migrateDB(ctx, srcURL, dstURL, &out)
	if err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("second run: %v", err)
	}
	if fileSum(t, srcPath) != before {
		t.Fatal("the SQLite file was modified")
	}
	if tc := tableCounts(t, dst); tc["users"] != 2 {
		t.Fatalf("refused run changed the target: %v", tc)
	}
}

func TestMigrateDBRefusesPendingSource(t *testing.T) {
	pgOnly(t)
	ctx := context.Background()
	srcURL, srcPath := populatedSQLite(t)
	d, err := db.Open(ctx, srcURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, "DELETE FROM schema_migrations WHERE version = (SELECT max(version) FROM schema_migrations)"); err != nil {
		t.Fatal(err)
	}
	d.Close()
	before := fileSum(t, srcPath)
	dstURL := dbtest.URL(t)
	err = migrateDB(ctx, srcURL, dstURL, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "pending") {
		t.Fatalf("pending source: %v", err)
	}
	dst, err := db.Open(ctx, dstURL)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if tc := tableCounts(t, dst); len(tc) != 0 {
		t.Fatalf("target touched by a refused run: %v", tc)
	}
	if fileSum(t, srcPath) != before {
		t.Fatal("the SQLite file was modified")
	}
}

// A copy that fails midway (a value Postgres cannot store) rolls back, leaves
// the target empty again, and can be retried after fixing the source.
func TestMigrateDBRollsBackOnFailure(t *testing.T) {
	pgOnly(t)
	ctx := context.Background()
	srcURL, _ := populatedSQLite(t)
	d, err := db.Open(ctx, srcURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, "INSERT INTO settings (key, value, updated_at) VALUES (?, ?, 1)", "bad", "a\x00b"); err != nil {
		t.Fatal(err)
	}
	dstURL := dbtest.URL(t)
	if err := migrateDB(ctx, srcURL, dstURL, &bytes.Buffer{}); err == nil {
		t.Fatal("copy of an unstorable value succeeded")
	}
	dst, err := db.Open(ctx, dstURL)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if tc := tableCounts(t, dst); len(tc) != 0 {
		t.Fatalf("failed copy left tables behind: %v", tc)
	}
	if _, err := d.ExecContext(ctx, "DELETE FROM settings WHERE key = 'bad'"); err != nil {
		t.Fatal(err)
	}
	d.Close()
	if err := migrateDB(ctx, srcURL, dstURL, &bytes.Buffer{}); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestMigrateDBArguments(t *testing.T) {
	ctx := context.Background()
	for _, c := range [][2]string{{"postgres://x/y", "postgres://x/z"}, {"sqlite:///x.db", "sqlite:///y.db"}} {
		if err := migrateDB(ctx, c[0], c[1], &bytes.Buffer{}); err == nil {
			t.Errorf("%v accepted", c)
		}
	}
	if err := migrateDB(ctx, "sqlite:///does/not/exist.db", "postgres://u:secret@127.0.0.1:1/x", &bytes.Buffer{}); err == nil || strings.Contains(err.Error(), "secret") {
		t.Errorf("missing source: %v", err)
	}
}
