package scheduler

import (
	"context"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/db"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Two workers: only one leads; when the leader's lock connection dies, the
// standby takes over; a cancelled leader releases the lock.
func TestPostgresLeadership(t *testing.T) {
	f := newFixture(t)
	if f.db.Dialect != db.Postgres {
		t.Skip("advisory-lock leadership is Postgres only")
	}
	f.set(time.Date(2026, 11, 21, 8, 0, 0, 0, time.UTC))
	key := int64(rand.Int32N(1<<30) + 1) // unique per run: advisory locks are database-wide
	start := func() (*Worker, context.CancelFunc, chan struct{}) {
		w := f.worker()
		w.cfg.LockKey = key
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); _ = w.Run(ctx) }()
		return w, cancel, done
	}
	a, cancelA, doneA := start()
	defer func() { cancelA(); <-doneA }()
	waitFor(t, "a to lead", a.leading.Load)
	b, cancelB, doneB := start()
	defer func() { cancelB(); <-doneB }()
	time.Sleep(200 * time.Millisecond) // several standby retries
	if b.leading.Load() {
		t.Fatal("two leaders")
	}

	// Kill a's lock session from outside: b takes over, a steps down.
	var n int
	if err := f.db.QueryRowContext(context.Background(), `SELECT count(pg_terminate_backend(pid)) FROM pg_locks
		WHERE locktype = 'advisory' AND classid = 0 AND objid = $1 AND granted`, key).Scan(&n); err != nil || n != 1 {
		t.Fatalf("terminate leader: %d, %v", n, err)
	}
	waitFor(t, "b to take over", b.leading.Load)
	waitFor(t, "a to step down", func() bool { return !a.leading.Load() })

	// Clean shutdown of b releases the lock; a (standing by) leads again.
	cancelB()
	<-doneB
	waitFor(t, "a to lead again", a.leading.Load)
}
