package store_test

import (
	"context"
	"slices"
	"testing"

	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

func TestRotationReminderTargetsAndAccountDelete(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	a, b, c := mkUser(t, s, "a"), mkUser(t, s, "b"), mkUser(t, s, "c")
	hh := mkHousehold(t, s, a, "H")
	noErr(t, s.AddMember(ctx, a, hh, b.UserID, store.RoleMember))
	noErr(t, s.AddMember(ctx, a, hh, c.UserID, store.RoleMember))
	for i, u := range []store.Actor{a, b, c} {
		_, err := s.CreateUserChannel(ctx, u, store.NewChannel{ID: "ch" + string(rune('a'+i)), Service: "discord", Ciphertext: "x", Label: "l", Hint: "h", Max: 5})
		noErr(t, err)
	}
	in := choreIn("Bins")
	in.Assignees, in.Rotation = []string{a.UserID, b.UserID, c.UserID}, true
	ch := must(s.CreateChore(ctx, a, hh, in))
	if ch.TurnUserID != a.UserID || ch.RotationAdvance != store.AdvanceAny {
		t.Fatalf("created chore: %+v", ch)
	}
	users := func() []string {
		ts := must(s.ReminderTargets(ctx, hh, ch.ID))
		var out []string
		for _, x := range ts {
			out = append(out, x.UserID)
		}
		return out
	}
	// Only the person whose turn it is is reminded.
	if got := users(); !slices.Equal(got, []string{a.UserID}) {
		t.Fatalf("targets = %v, want only a", got)
	}
	ch = must(s.SetChoreTurn(ctx, b, hh, ch.ID, c.UserID))
	if got := users(); !slices.Equal(got, []string{c.UserID}) {
		t.Fatalf("targets after set turn = %v, want only c", got)
	}
	// Without rotation, every assignee is reminded again.
	in.Rotation = false
	must(s.UpdateChore(ctx, a, hh, ch.ID, in))
	if got := users(); len(got) != 3 {
		t.Fatalf("targets without rotation = %v", got)
	}
	in.Rotation = true
	ch = must(s.UpdateChore(ctx, a, hh, ch.ID, in))
	if ch.TurnUserID != a.UserID {
		t.Fatalf("turn after re-enabling = %s", ch.TurnUserID)
	}
	// Deleting c's account: out of the rotation, a and b remain.
	ch = must(s.SetChoreTurn(ctx, a, hh, ch.ID, c.UserID))
	_, err := s.DeleteAccount(ctx, c.UserID, store.AccountDeletion{})
	noErr(t, err)
	ch = must(s.Chore(ctx, a, hh, ch.ID))
	if !ch.Rotation || ch.TurnUserID != a.UserID || !slices.Equal(ch.Assignees, []string{a.UserID, b.UserID}) {
		t.Fatalf("after account delete: %+v", ch)
	}
	// SetChoreTurn guards: not an assignee, archived.
	is(t, errOnly(s.SetChoreTurn(ctx, a, hh, ch.ID, c.UserID)), store.ErrInvalid)
	noErr(t, s.ArchiveChore(ctx, a, hh, ch.ID))
	is(t, errOnly(s.SetChoreTurn(ctx, a, hh, ch.ID, b.UserID)), store.ErrConflict)
}

func errOnly[T any](_ T, err error) error { return err }

func TestRotationInputValidation(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	a, b := mkUser(t, s, "a"), mkUser(t, s, "b")
	hh := mkHousehold(t, s, a, "H")
	noErr(t, s.AddMember(ctx, a, hh, b.UserID, store.RoleMember))
	in := choreIn("x")
	in.Rotation = true
	is(t, errOnly(s.CreateChore(ctx, a, hh, in)), store.ErrInvalid) // no assignees
	in.Assignees = []string{a.UserID, b.UserID}
	in.RotationAdvance = "weird"
	is(t, errOnly(s.CreateChore(ctx, a, hh, in)), store.ErrInvalid)
	in.RotationAdvance = ""
	in.Kind = store.KindOneOff
	is(t, errOnly(s.CreateChore(ctx, a, hh, in)), store.ErrInvalid)
}
