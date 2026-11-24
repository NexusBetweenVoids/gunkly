package scheduler

import (
	"testing"

	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

func (f *fixture) personalQuiet(a store.Actor, start, end int) {
	f.t.Helper()
	q := &store.QuietHours{StartMin: start, EndMin: end}
	if _, err := f.st.UpdateProfile(f.ctx, a.UserID, store.ProfileUpdate{QuietHours: &q}); err != nil {
		f.t.Fatal(err)
	}
}

// Personal quiet hours defer the user's own routes and leave household
// channels alone.
func TestPersonalQuietHoursSpareHouseholdChannel(t *testing.T) {
	f := newFixture(t)
	f.channel(f.owner, "owner")
	f.householdChannel()
	f.chore("Dishes", "2026-11-21", "gentle")
	f.personalQuiet(f.owner, 8*60, 10*60)
	w := f.worker()
	f.tickAt(w, "2026-11-21 08:59:00")
	f.tickAt(w, "2026-11-21 09:01:00")
	got := f.sender.take()
	if len(got) != 1 {
		t.Fatalf("during personal quiet hours: %d sends, want only the household channel", len(got))
	}
	f.tickAt(w, "2026-11-21 10:00:30")
	if got := f.sender.take(); len(got) != 1 {
		t.Fatalf("after personal quiet hours: %d sends, want the deferred personal one", len(got))
	}
	f.tickAt(w, "2026-11-21 10:05:00")
	if got := f.sender.take(); len(got) != 0 {
		t.Fatalf("extra sends: %d", len(got))
	}
}

// A window wrapping midnight coalesces to one reminder per chore, and the
// household's quiet hours still apply on top.
func TestPersonalQuietHoursWrapCoalesceAndStack(t *testing.T) {
	f := newFixture(t)
	f.channel(f.owner, "owner")
	c := f.chore("Bins", "2026-11-21", PresetCustom)
	f.custom(c.ID, "before", 1, 23*60) // 2026-11-20 23:00
	f.custom(c.ID, "on", 0, 8*60)      // 2026-11-21 08:00
	f.personalQuiet(f.owner, 22*60, 10*60)
	hq := &store.QuietHours{StartMin: 10 * 60, EndMin: 11 * 60}
	if _, err := f.st.UpdateHousehold(f.ctx, f.owner, f.hid, store.HouseholdUpdate{QuietHours: &hq}); err != nil {
		t.Fatal(err)
	}
	w := f.worker()
	for _, at := range []string{"2026-11-20 21:00:00", "2026-11-20 23:00:30", "2026-11-21 08:00:30", "2026-11-21 10:00:30", "2026-11-21 10:59:59"} {
		f.tickAt(w, at)
		if got := f.sender.take(); len(got) != 0 {
			t.Fatalf("sent during quiet hours at %s", at)
		}
	}
	f.tickAt(w, "2026-11-21 11:00:01")
	if got := f.sender.take(); len(got) != 1 {
		t.Fatalf("after both: %d sends, want 1", len(got))
	}
	f.tickAt(w, "2026-11-21 11:05:00")
	if got := f.sender.take(); len(got) != 0 {
		t.Fatalf("second send after coalescing: %d", len(got))
	}
}
