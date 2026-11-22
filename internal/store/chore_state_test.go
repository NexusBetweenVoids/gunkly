package store_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/schedule"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

func TestChoreLimitAndValidation(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	a := mkUser(t, s, "a")
	hh := mkHousehold(t, s, a, "H")
	in := choreIn("One")
	in.MaxChores = 2
	must(s.CreateChore(ctx, a, hh, in))
	must(s.CreateChore(ctx, a, hh, in))
	_, err := s.CreateChore(ctx, a, hh, in)
	is(t, err, store.ErrLimit)

	for name, mut := range map[string]func(*store.ChoreInput){
		"interval too big": func(c *store.ChoreInput) { c.IntervalN = 1001 },
		"bad rrule":        func(c *store.ChoreInput) { c.ScheduleType, c.RRule = store.ScheduleRRule, "FREQ=DAILY;COUNT=2" },
		"long name":        func(c *store.ChoreInput) { c.Name = strings.Repeat("x", 201) },
	} {
		c := choreIn("X")
		mut(&c)
		if _, err := s.CreateChore(ctx, a, hh, c); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// 200 CJK characters are 600 bytes but a valid name.
	c := choreIn("x")
	runes := make([]rune, 200)
	for i := range runes {
		runes[i] = '掃'
	}
	c.Name = string(runes)
	if _, err := s.CreateChore(ctx, a, hh, c); err != nil {
		t.Fatalf("200-rune name: %v", err)
	}
}

func TestStarterAreasAtCreation(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	a := mkUser(t, s, "a")
	h, err := s.CreateHousehold(ctx, a, store.NewHousehold{Name: "H", Timezone: "UTC", StarterAreas: []string{"Kitchen", "Garden"}})
	if err != nil {
		t.Fatal(err)
	}
	if as := must(s.ListAreas(ctx, a, h.ID)); len(as) != 2 {
		t.Fatalf("areas = %v", as)
	}
	_, err = s.CreateHousehold(ctx, a, store.NewHousehold{Name: "H2", Timezone: "UTC", StarterAreas: []string{"Kitchen", "kitchen"}})
	is(t, err, store.ErrConflict)
	if hs := must(s.ListHouseholds(ctx, a)); len(hs) != 1 {
		t.Fatalf("failed create left a household: %d", len(hs))
	}
}

func TestCompletionsPageTies(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	for range 4 { // same millisecond: the ID breaks ties
		must(f.s.CompleteChore(ctx, f.m, f.hA, f.choreA, store.CompletionInput{CompletedAt: at, Source: store.SourceApp, NextDue: at, DueDate: "2026-11-28"}))
	}
	seen := map[string]bool{}
	after := ""
	for pages := 0; ; pages++ {
		items, next, err := f.s.CompletionsPage(ctx, f.a, f.hA, f.choreA, 2, after)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			if seen[it.ID] {
				t.Fatalf("duplicate %s across pages", it.ID)
			}
			seen[it.ID] = true
		}
		if next == "" {
			break
		}
		if pages > 5 {
			t.Fatal("pagination does not end")
		}
		after = next
	}
	if len(seen) != 5 { // 4 plus the fixture's
		t.Fatalf("saw %d completions", len(seen))
	}
	_, _, err := f.s.CompletionsPage(ctx, f.a, f.hA, f.choreA, 2, "junk")
	is(t, err, store.ErrInvalid)
}

func TestSetChoreReminders(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	rs := []schedule.Reminder{{Kind: schedule.Before, Days: 2, Minute: 60}, {Kind: schedule.OnDue, Days: 5, Minute: 120}}
	noErr(t, f.s.SetChoreReminders(ctx, f.m, f.hA, f.choreA, "custom", rs, 10))
	preset, got, err := f.s.ChoreReminders(ctx, f.g, f.hA, f.choreA)
	if err != nil || preset != "custom" || len(got) != 2 || got[0] != rs[0] || got[1].Days != 0 {
		t.Fatalf("got %q %v %v", preset, got, err)
	}
	is(t, f.s.SetChoreReminders(ctx, f.m, f.hA, f.choreA, "custom", rs, 1), store.ErrLimit)
	is(t, f.s.SetChoreReminders(ctx, f.m, f.hA, f.choreA, "nag", rs, 10), store.ErrInvalid)
	is(t, f.s.SetChoreReminders(ctx, f.m, f.hA, f.choreA, "custom", []schedule.Reminder{{Kind: schedule.Overdue, Minute: 1}}, 10), store.ErrInvalid)
	noErr(t, f.s.SetChoreReminders(ctx, f.m, f.hA, f.choreA, "nag", nil, 10))
	if preset, got, _ = f.s.ChoreReminders(ctx, f.g, f.hA, f.choreA); preset != "nag" || len(got) != 0 {
		t.Fatalf("after switching: %q %v", preset, got)
	}
}

func TestSnoozeSkipTransactional(t *testing.T) {
	f := newMD(t)
	tok := f.link(t, f.m.UserID, "")

	// Snooze: scoped, validated, and it invalidates outstanding mark-done links.
	until := schedule.Date{Year: 2026, Month: 12, Day: 5}
	c, err := f.s.SnoozeChore(f.ctx, f.m, f.hid, f.chore.ID, until)
	if err != nil || c.DueDate != "2026-12-05" {
		t.Fatalf("snooze: %v %+v", err, c)
	}
	if _, err := f.s.PeekMarkDone(f.ctx, tok); err == nil {
		t.Fatal("mark-done link survived a snooze")
	}
	if _, err := f.s.SnoozeChore(f.ctx, f.m, f.hid, f.chore.ID, schedule.Date{Year: 2026, Month: 11, Day: 1}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("past snooze: %v", err)
	}
	if _, err := f.s.SnoozeChore(f.ctx, f.g, f.hid, f.chore.ID, until); !errors.Is(err, store.ErrForbidden) {
		t.Fatalf("guest snooze: %v", err)
	}
	if _, err := f.s.SnoozeChore(f.ctx, f.m, f.otherHID, f.otherChore.ID, until); err == nil {
		t.Fatal("snoozed in a household the caller is not in")
	}
	if _, err := f.s.SnoozeChore(f.ctx, f.m, f.hid, f.otherChore.ID, until); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("chore of another household: %v", err)
	}

	// Skip moves to the next occurrence after today.
	c, err = f.s.SkipChore(f.ctx, f.m, f.hid, f.chore.ID)
	if err != nil || c.DueDate <= "2026-12-05" {
		t.Fatalf("skip: %v %+v", err, c)
	}

	// Concurrent snoozes and skips serialise: no error, a consistent result.
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var err error
			if i%2 == 0 {
				_, err = f.s.SkipChore(f.ctx, f.m, f.hid, f.chore.ID)
			} else {
				_, err = f.s.SnoozeChore(f.ctx, f.m, f.hid, f.chore.ID, until)
			}
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	noErr(t, f.s.ArchiveChore(f.ctx, f.owner, f.hid, f.chore.ID))
	if _, err := f.s.SkipChore(f.ctx, f.m, f.hid, f.chore.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("archived skip: %v", err)
	}
}

func TestChoreInputReminders(t *testing.T) {
	f := newMD(t)
	in := choreIn("Timed")
	in.Reminders = []schedule.Reminder{{Kind: schedule.OnDue, Minute: 450}}
	in.ReminderPreset = "gentle" // rows need the custom preset
	if _, err := f.s.CreateChore(f.ctx, f.m, f.hid, in); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("rows with a named preset: %v", err)
	}
	in.ReminderPreset = "custom"
	in.Reminders = []schedule.Reminder{{Kind: schedule.OnDue, Minute: 1440}}
	if _, err := f.s.CreateChore(f.ctx, f.m, f.hid, in); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("bad minute: %v", err)
	}
	in.Reminders = []schedule.Reminder{{Kind: schedule.OnDue, Minute: 450}}
	c, err := f.s.CreateChore(f.ctx, f.m, f.hid, in)
	if err != nil {
		t.Fatal(err)
	}
	preset, got, err := f.s.ChoreReminders(f.ctx, f.m, f.hid, c.ID)
	if err != nil || preset != "custom" || len(got) != 1 || got[0].Minute != 450 {
		t.Fatalf("%q %v %v", preset, got, err)
	}
}
