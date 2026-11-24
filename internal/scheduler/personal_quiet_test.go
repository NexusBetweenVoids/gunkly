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

func (f *fixture) personalZone(a store.Actor, zone string) {
	f.t.Helper()
	z := &zone
	if _, err := f.st.UpdateProfile(f.ctx, a.UserID, store.ProfileUpdate{TimeZone: &z}); err != nil {
		f.t.Fatal(err)
	}
}

// A user's own time zone decides where their quiet hours fall, in the weeks
// when the US has not yet left daylight time but Europe has (the offset
// between Amsterdam and New York is 5 hours, not 6), and the household's own
// quiet hours still stack on top in the household's zone.
func TestPersonalQuietHoursInOwnZoneAcrossDST(t *testing.T) {
	f := newFixture(t) // household in Europe/Amsterdam (CET on 2026-10-28)
	f.channel(f.owner, "owner")
	f.personalZone(f.owner, "America/New_York")
	f.personalQuiet(f.owner, 22*60, 7*60) // 22:00-07:00 New York (EDT)
	c := f.chore("Bins", "2026-10-28", PresetCustom)
	f.custom(c.ID, "on", 0, 3*60) // 03:00 Amsterdam = 22:00 the evening before in New York
	w := f.worker()
	for _, at := range []string{"2026-10-28 02:59:00", "2026-10-28 03:00:30", "2026-10-28 07:30:00", "2026-10-28 11:59:00"} {
		f.tickAt(w, at)
		if got := f.sender.take(); len(got) != 0 {
			t.Fatalf("sent at %s, inside New York quiet hours", at)
		}
	}
	f.tickAt(w, "2026-10-28 12:00:30") // 07:00 EDT
	if got := f.sender.take(); len(got) != 1 {
		t.Fatalf("after the user's quiet hours: %d sends, want 1", len(got))
	}
	f.tickAt(w, "2026-10-28 12:10:00")
	if got := f.sender.take(); len(got) != 0 {
		t.Fatalf("second send: %d", len(got))
	}
}

// Without a zone of their own the window is read in the household's zone, and
// a reminder that is inside the household's window but outside the user's
// zone-shifted one is still held back by the household.
func TestPersonalQuietHoursZoneDefaultsAndStacks(t *testing.T) {
	f := newFixture(t)
	f.channel(f.owner, "owner")
	f.personalQuiet(f.owner, 22*60, 7*60) // household zone: 03:00 Amsterdam is quiet
	c := f.chore("Bins", "2026-10-28", PresetCustom)
	f.custom(c.ID, "on", 0, 3*60)
	w := f.worker()
	f.tickAt(w, "2026-10-28 02:59:00")
	f.tickAt(w, "2026-10-28 03:00:30")
	if got := f.sender.take(); len(got) != 0 {
		t.Fatalf("no zone: sent inside the household-zone window")
	}
	f.tickAt(w, "2026-10-28 07:00:30")
	if got := f.sender.take(); len(got) != 1 {
		t.Fatalf("no zone: want 1 send after 07:00 Amsterdam")
	}

	// Now a zone where 03:00 Amsterdam is daytime, but the household is quiet.
	g := newFixture(t)
	g.channel(g.owner, "owner")
	g.personalZone(g.owner, "Asia/Tokyo") // 03:00 CET = 11:00 JST
	g.personalQuiet(g.owner, 22*60, 7*60)
	hq := &store.QuietHours{StartMin: 2 * 60, EndMin: 5 * 60}
	if _, err := g.st.UpdateHousehold(g.ctx, g.owner, g.hid, store.HouseholdUpdate{QuietHours: &hq}); err != nil {
		t.Fatal(err)
	}
	c = g.chore("Bins", "2026-10-28", PresetCustom)
	g.custom(c.ID, "on", 0, 3*60)
	w = g.worker()
	g.tickAt(w, "2026-10-28 02:59:00")
	g.tickAt(w, "2026-10-28 03:00:30")
	if got := g.sender.take(); len(got) != 0 {
		t.Fatalf("household quiet hours ignored")
	}
	g.tickAt(w, "2026-10-28 05:00:30")
	if got := g.sender.take(); len(got) != 1 {
		t.Fatalf("want 1 send after the household window")
	}
}
