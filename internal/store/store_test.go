package store_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/db/dbtest"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	return store.New(dbtest.Open(t))
}

func mkUser(t *testing.T, s *store.Store, name string) store.Actor {
	t.Helper()
	u, err := s.CreateUser(context.Background(), store.NewUser{
		Email: name + "@example.com", Username: name, DisplayName: name, PasswordHash: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	return store.Actor{UserID: u.ID}
}

func mkHousehold(t *testing.T, s *store.Store, a store.Actor, name string) string {
	t.Helper()
	h, err := s.CreateHousehold(context.Background(), a, store.NewHousehold{Name: name, Timezone: "Europe/Amsterdam"})
	if err != nil {
		t.Fatal(err)
	}
	return h.ID
}

func choreIn(name string) store.ChoreInput {
	return store.ChoreInput{
		Name: name, Kind: store.KindRecurring, ScheduleType: store.ScheduleInterval, IntervalN: 1, IntervalUnit: "week",
		Anchor: store.AnchorFromCompletion, FirstDue: "2026-11-21", DueDate: "2026-11-21",
		NextDue: time.Date(2026, 11, 21, 8, 0, 0, 0, time.UTC),
	}
}

// must panics on error; the test fails with the error message.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func noErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func is(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestUsers(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	u := must(s.CreateUser(ctx, store.NewUser{Email: "Ann@Example.com", Username: "Ann", DisplayName: "Ann", PasswordHash: "h"}))
	for _, login := range []string{"ann", "ANN", "ann@example.com", " Ann@EXAMPLE.com "} {
		got, err := s.UserByLogin(ctx, login)
		if err != nil || got.ID != u.ID {
			t.Errorf("UserByLogin(%q) = %v, %v", login, got.ID, err)
		}
	}
	got := must(s.UserByID(ctx, u.ID))
	if got.Email != "Ann@Example.com" || got.Username != "Ann" || got.PasswordHash != "h" || got.CreatedAt.IsZero() {
		t.Errorf("UserByID = %+v", got)
	}
	_, err := s.CreateUser(ctx, store.NewUser{Email: "ann@example.COM", Username: "other", DisplayName: "x", PasswordHash: "h"})
	is(t, err, store.ErrConflict)
	_, err = s.CreateUser(ctx, store.NewUser{Username: "ANN", DisplayName: "x", PasswordHash: "h"})
	is(t, err, store.ErrConflict)
	_, err = s.CreateUser(ctx, store.NewUser{Username: "a@b", DisplayName: "x", PasswordHash: "h"})
	is(t, err, store.ErrInvalid)
	// Email-less users are allowed (email disabled); several may coexist.
	must(s.CreateUser(ctx, store.NewUser{Username: "nomail1", DisplayName: "x", PasswordHash: "h"}))
	must(s.CreateUser(ctx, store.NewUser{Username: "nomail2", DisplayName: "x", PasswordHash: "h"}))
	_, err = s.UserByLogin(ctx, "nobody")
	is(t, err, store.ErrNotFound)
}

func TestHouseholdsAndMembers(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	a, b := mkUser(t, s, "a"), mkUser(t, s, "b")
	_, err := s.CreateHousehold(ctx, a, store.NewHousehold{Name: "x", Timezone: "Not/AZone"})
	is(t, err, store.ErrInvalid)
	h := mkHousehold(t, s, a, "Home")
	hs := must(s.ListHouseholds(ctx, a))
	if len(hs) != 1 || hs[0].ID != h || hs[0].Role != store.RoleOwner || hs[0].Timezone != "Europe/Amsterdam" {
		t.Fatalf("ListHouseholds = %+v", hs)
	}
	if got := must(s.ListHouseholds(ctx, b)); len(got) != 0 {
		t.Fatalf("b sees %v", got)
	}

	is(t, s.AddMember(ctx, a, h, "no-such-user", store.RoleMember), store.ErrNotFound)
	is(t, s.AddMember(ctx, a, h, b.UserID, "admin"), store.ErrInvalid)
	time.Sleep(3 * time.Millisecond) // distinct joined_at: members list by join time
	noErr(t, s.AddMember(ctx, a, h, b.UserID, store.RoleGuest))
	is(t, s.AddMember(ctx, a, h, b.UserID, store.RoleMember), store.ErrConflict)
	if r := must(s.Role(ctx, b, h)); r != store.RoleGuest {
		t.Fatalf("role = %s", r)
	}
	ms := must(s.ListMembers(ctx, b, h))
	if len(ms) != 2 || ms[0].UserID != a.UserID || ms[1].Role != store.RoleGuest {
		t.Fatalf("members = %+v", ms)
	}

	// The last owner cannot be demoted or leave.
	is(t, s.ChangeRole(ctx, a, h, a.UserID, store.RoleMember), store.ErrConflict)
	is(t, s.RemoveMember(ctx, a, h, a.UserID), store.ErrConflict)
	noErr(t, s.ChangeRole(ctx, a, h, b.UserID, store.RoleOwner))
	noErr(t, s.ChangeRole(ctx, b, h, a.UserID, store.RoleMember))
	// Member a may leave by themselves.
	noErr(t, s.RemoveMember(ctx, a, h, a.UserID))
	_, err = s.Household(ctx, a, h)
	is(t, err, store.ErrNotFound)
}

func TestChoreLifecycle(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	a, m, g := mkUser(t, s, "a"), mkUser(t, s, "m"), mkUser(t, s, "g")
	h := mkHousehold(t, s, a, "Home")
	noErr(t, s.AddMember(ctx, a, h, m.UserID, store.RoleMember))
	noErr(t, s.AddMember(ctx, a, h, g.UserID, store.RoleGuest))
	area := must(s.CreateArea(ctx, a, h, "Kitchen", 0))
	_, err := s.CreateArea(ctx, m, h, " kitchen ", 0)
	is(t, err, store.ErrConflict)

	in := choreIn("Descale kettle")
	in.AreaID, in.Assignees, in.Link = area.ID, []string{m.UserID}, "https://example.com/kettle"
	c := must(s.CreateChore(ctx, m, h, in))
	if c.AreaID != area.ID || len(c.Assignees) != 1 || c.Assignees[0] != m.UserID || !c.NextDue.Equal(in.NextDue) {
		t.Fatalf("chore = %+v", c)
	}

	bad := in
	bad.Link = "javascript:alert(1)"
	_, err = s.CreateChore(ctx, m, h, bad)
	is(t, err, store.ErrInvalid)
	bad = in
	bad.Assignees = []string{g.UserID} // guests cannot be assignees
	_, err = s.CreateChore(ctx, m, h, bad)
	is(t, err, store.ErrInvalid)

	in.Name, in.Assignees = "Descale the kettle", nil
	c = must(s.UpdateChore(ctx, m, h, c.ID, in))
	if c.Name != "Descale the kettle" || len(c.Assignees) != 0 {
		t.Fatalf("updated = %+v", c)
	}

	// Complete twice, revert the latest: due date goes back one step.
	next1 := in.NextDue.AddDate(0, 0, 7)
	must(s.CompleteChore(ctx, m, h, c.ID, store.CompletionInput{CompletedAt: time.Now(), Source: store.SourceApp, NextDue: next1, DueDate: "2026-11-28"}))
	time.Sleep(2 * time.Millisecond)
	next2 := next1.AddDate(0, 0, 7)
	done := must(s.CompleteChore(ctx, a, h, c.ID, store.CompletionInput{CompletedAt: time.Now(), Source: store.SourceApp, Note: "ok", NextDue: next2, DueDate: "2026-12-05"}))
	if got := must(s.Chore(ctx, g, h, c.ID)); !got.NextDue.Equal(next2) || got.DueDate != "2026-12-05" {
		t.Fatalf("after complete: %v %s", got.NextDue, got.DueDate)
	}
	noErr(t, s.RevertCompletion(ctx, m, h, done.ID))
	is(t, s.RevertCompletion(ctx, m, h, done.ID), store.ErrConflict)
	if got := must(s.Chore(ctx, g, h, c.ID)); !got.NextDue.Equal(next1) || got.DueDate != "2026-11-28" {
		t.Fatalf("after revert: %v %s", got.NextDue, got.DueDate)
	}
	hist := must(s.ListCompletions(ctx, g, h, c.ID, 0))
	if len(hist) != 2 || hist[0].ID != done.ID || hist[0].RevertedAt.IsZero() || hist[0].Note != "ok" || hist[1].UserID != m.UserID {
		t.Fatalf("history = %+v", hist)
	}

	// One-off: completion archives, revert un-archives.
	oin := choreIn("Fix fence")
	oin.Kind = store.KindOneOff
	o := must(s.CreateChore(ctx, m, h, oin))
	oc := must(s.CompleteChore(ctx, m, h, o.ID, store.CompletionInput{CompletedAt: time.Now(), Source: store.SourceApp}))
	if got := must(s.ListChores(ctx, m, h, false)); len(got) != 1 {
		t.Fatalf("active chores = %d", len(got))
	}
	_, err = s.CompleteChore(ctx, m, h, o.ID, store.CompletionInput{CompletedAt: time.Now(), Source: store.SourceApp})
	is(t, err, store.ErrConflict)
	noErr(t, s.RevertCompletion(ctx, m, h, oc.ID))
	if got := must(s.Chore(ctx, m, h, o.ID)); !got.ArchivedAt.IsZero() {
		t.Fatal("one-off still archived after revert")
	}

	noErr(t, s.ArchiveChore(ctx, m, h, c.ID))
	if got := must(s.ListChores(ctx, m, h, true)); len(got) != 2 {
		t.Fatalf("all chores = %d", len(got))
	}
	// Deleting the area leaves its chores area-less.
	noErr(t, s.DeleteArea(ctx, m, h, area.ID))
	if got := must(s.Chore(ctx, m, h, c.ID)); got.AreaID != "" {
		t.Fatalf("area = %q", got.AreaID)
	}
	noErr(t, s.DeleteChore(ctx, m, h, c.ID))
	_, err = s.Chore(ctx, m, h, c.ID)
	is(t, err, store.ErrNotFound)
	_, err = s.ListCompletions(ctx, m, h, c.ID, 0)
	is(t, err, store.ErrNotFound)
}

// Two owners demoting each other at the same time must never leave the
// household without an owner (and must not deadlock).
func TestConcurrentOwnerDemotion(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	for i := 0; i < 5; i++ {
		a, b := mkUser(t, s, fmt.Sprintf("a%d", i)), mkUser(t, s, fmt.Sprintf("b%d", i))
		h := mkHousehold(t, s, a, "Home")
		noErr(t, s.AddMember(ctx, a, h, b.UserID, store.RoleOwner))
		errs := make(chan error, 2)
		go func() { errs <- s.ChangeRole(ctx, a, h, b.UserID, store.RoleMember) }()
		go func() { errs <- s.ChangeRole(ctx, b, h, a.UserID, store.RoleMember) }()
		for j := 0; j < 2; j++ {
			if err := <-errs; err != nil && !errors.Is(err, store.ErrConflict) && !errors.Is(err, store.ErrForbidden) {
				t.Fatal(err)
			}
		}
		owners := 0
		for _, m := range must(s.ListMembers(ctx, a, h)) {
			if m.Role == store.RoleOwner {
				owners++
			}
		}
		if owners != 1 {
			t.Fatalf("owners = %d", owners)
		}
	}
}
