// Package dbtest opens fresh, migrated databases for tests.
//
// By default each call gets a new SQLite file in t.TempDir(). With
// GUNKLY_TEST_DB=postgres it connects to GUNKLY_TEST_POSTGRES_URL and creates
// a unique schema per test (selected via search_path on every connection),
// dropped at cleanup.
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/NexusBetweenVoids/gunkly/internal/db"
)

// URL returns a database URL for a fresh, empty database and registers its
// cleanup. Use Open unless the test needs to control migration itself.
func URL(t testing.TB) string {
	t.Helper()
	if os.Getenv("GUNKLY_TEST_DB") != "postgres" {
		return "sqlite://" + filepath.Join(t.TempDir(), "test.db")
	}
	base := os.Getenv("GUNKLY_TEST_POSTGRES_URL")
	if base == "" {
		t.Fatal("GUNKLY_TEST_DB=postgres requires GUNKLY_TEST_POSTGRES_URL")
	}
	ctx := context.Background()
	admin, err := db.Open(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	var b [8]byte
	rand.Read(b[:])
	schema := "test_" + hex.EncodeToString(b[:])
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		_ = admin.Close()
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema) // pgx sends unknown params as runtime parameters
	u.RawQuery = q.Encode()
	return u.String()
}

// Open returns a fresh, fully migrated database closed at cleanup.
func Open(t testing.TB) *db.DB {
	t.Helper()
	d, err := db.Open(context.Background(), URL(t))
	if err != nil {
		t.Fatal(err)
	}
	// Registered after URL's cleanup, so it runs first (LIFO): close before drop.
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return d
}
