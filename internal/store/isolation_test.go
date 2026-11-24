package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/schedule"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
	"github.com/NexusBetweenVoids/gunkly/internal/transfer"
)

// fixture: household A (owner a, member m, guest g) with an area, a chore and
// a completion; household B (owner b) with its own chore. x is a user in no
// household.
type fixture struct {
	s                 *store.Store
	a, m, g, b, x     store.Actor
	hA, hB            string
	areaA, choreA     string
	completionA       string
	choreB, areaB     string
	choreAName, areaN string
}

func setup(t *testing.T) *fixture {
	ctx := context.Background()
	f := &fixture{s: newStore(t)}
	s := f.s
	f.a, f.m, f.g, f.b, f.x = mkUser(t, s, "a"), mkUser(t, s, "m"), mkUser(t, s, "g"), mkUser(t, s, "b"), mkUser(t, s, "x")
	f.hA, f.hB = mkHousehold(t, s, f.a, "A"), mkHousehold(t, s, f.b, "B")
	noErr(t, s.AddMember(ctx, f.a, f.hA, f.m.UserID, store.RoleMember))
	noErr(t, s.AddMember(ctx, f.a, f.hA, f.g.UserID, store.RoleGuest))
	f.areaN = "Garden"
	f.areaA = must(s.CreateArea(ctx, f.a, f.hA, f.areaN, 0)).ID
	in := choreIn("Clean gutters")
	in.AreaID, in.Assignees = f.areaA, []string{f.m.UserID}
	f.choreAName = in.Name
	f.choreA = must(s.CreateChore(ctx, f.a, f.hA, in)).ID
	f.completionA = must(s.CompleteChore(ctx, f.m, f.hA, f.choreA, store.CompletionInput{
		CompletedAt: time.Now(), Source: store.SourceApp, NextDue: in.NextDue.AddDate(0, 0, 7), DueDate: "2026-11-28",
	})).ID
	f.areaB = must(s.CreateArea(ctx, f.b, f.hB, "Shed", 0)).ID
	f.choreB = must(s.CreateChore(ctx, f.b, f.hB, choreIn("Oil hinges"))).ID
	return f
}

type op struct {
	name  string
	write bool // needs member
	owner bool // needs owner
	run   func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error
}

func ignore[T any](_ T, err error) error { return err }

func ignore3[T, U any](_ T, _ U, err error) error { return err }

// ops covers every household-scoped store method. IDs always refer to
// household A's data; hh is the household the caller claims.
var ops = []op{
	{name: "Household", run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return ignore(s.Household(ctx, a, hh))
	}},
	{name: "Role", run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return ignore(s.Role(ctx, a, hh))
	}},
	{name: "ListMembers", run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return ignore(s.ListMembers(ctx, a, hh))
	}},
	{name: "ListAreas", run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return ignore(s.ListAreas(ctx, a, hh))
	}},
	{name: "Area", run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return ignore(s.Area(ctx, a, hh, f.areaA))
	}},
	{name: "ListChores", run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return ignore(s.ListChores(ctx, a, hh, true))
	}},
	{name: "Chore", run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return ignore(s.Chore(ctx, a, hh, f.choreA))
	}},
	{name: "ListCompletions", run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return ignore(s.ListCompletions(ctx, a, hh, f.choreA, 0))
	}},
	{name: "CreateArea", write: true, run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return ignore(s.CreateArea(ctx, a, hh, "Attic", 0))
	}},
	{name: "RenameArea", write: true, run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return s.RenameArea(ctx, a, hh, f.areaA, "Hacked")
	}},
	{name: "DeleteArea", write: true, run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return s.DeleteArea(ctx, a, hh, f.areaA)
	}},
	{name: "CreateChore", write: true, run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return ignore(s.CreateChore(ctx, a, hh, choreIn("Intruder")))
	}},
	{name: "UpdateChore", write: true, run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return ignore(s.UpdateChore(ctx, a, hh, f.choreA, choreIn("Hacked")))
	}},
	{name: "ArchiveChore", write: true, run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return s.ArchiveChore(ctx, a, hh, f.choreA)
	}},
	{name: "DeleteChore", write: true, run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return s.DeleteChore(ctx, a, hh, f.choreA)
	}},
	{name: "CompleteChore", write: true, run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return ignore(s.CompleteChore(ctx, a, hh, f.choreA, store.CompletionInput{
			CompletedAt: time.Now(), Source: store.SourceApp, NextDue: time.Now(), DueDate: "2027-01-01",
		}))
	}},
	{name: "RevertCompletion", write: true, run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return s.RevertCompletion(ctx, a, hh, f.completionA)
	}},
	{name: "CompletionsPage", run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return ignore3(s.CompletionsPage(ctx, a, hh, f.choreA, 10, ""))
	}},
	{name: "ChoreReminders", run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return ignore3(s.ChoreReminders(ctx, a, hh, f.choreA))
	}},
	{name: "SetChoreReminders", write: true, run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return s.SetChoreReminders(ctx, a, hh, f.choreA, "nag", nil, 10)
	}},
	{name: "SnoozeChore", write: true, run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return ignore(s.SnoozeChore(ctx, a, hh, f.choreA, schedule.Date{Year: 2999, Month: 1, Day: 1}))
	}},
	{name: "SkipChore", write: true, run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return ignore(s.SkipChore(ctx, a, hh, f.choreA))
	}},
	{name: "SetChorePause", write: true, run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return ignore(s.SetChorePause(ctx, a, hh, f.choreA, time.Time{}, true))
	}},
	{name: "UnarchiveChore", write: true, run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return ignore(s.UnarchiveChore(ctx, a, hh, f.choreA))
	}},
	{name: "CompleteChoreAuto", write: true, run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return ignore3(s.CompleteChoreAuto(ctx, a, hh, f.choreA, "x", "app", ""))
	}},
	{name: "RevertChoreCompletion", write: true, run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return ignore(s.RevertChoreCompletion(ctx, a, hh, f.choreA, f.completionA))
	}},
	{name: "ExportHousehold", write: true, run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return ignore(s.ExportHousehold(ctx, a, hh))
	}},
	{name: "MergeHousehold", write: true, run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return ignore(s.MergeHousehold(ctx, a, hh, &transfer.Household{
			Areas: []transfer.Area{{Name: "Imported"}},
			Chores: []transfer.Chore{{Name: "Imported", Kind: store.KindRecurring, Area: "Imported", Schedule: transfer.Schedule{
				Type: store.ScheduleInterval, Interval: &transfer.Interval{Every: 1, Unit: "week"}, Anchor: store.AnchorFromCompletion, FirstDue: "2026-11-21"}}},
		}, nil))
	}},
	{name: "ExportWebhooks", write: true, owner: true, run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return ignore(s.ExportWebhooks(ctx, a, hh))
	}},
	{name: "AddMember", write: true, owner: true, run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return s.AddMember(ctx, a, hh, f.x.UserID, store.RoleOwner)
	}},
	{name: "ChangeRole", write: true, owner: true, run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return s.ChangeRole(ctx, a, hh, f.m.UserID, store.RoleOwner)
	}},
	{name: "RemoveMember", write: true, owner: true, run: func(ctx context.Context, s *store.Store, a store.Actor, hh string, f *fixture) error {
		return s.RemoveMember(ctx, a, hh, f.a.UserID)
	}},
}

// assertIntact checks household A is exactly as setup left it.
func assertIntact(t *testing.T, f *fixture) {
	t.Helper()
	ctx, s := context.Background(), f.s
	c, err := s.Chore(ctx, f.a, f.hA, f.choreA)
	if err != nil || c.Name != f.choreAName || !c.ArchivedAt.IsZero() || c.AreaID != f.areaA || c.DueDate != "2026-11-28" ||
		len(c.Assignees) != 1 || c.Assignees[0] != f.m.UserID {
		t.Fatalf("chore A changed: %+v %v", c, err)
	}
	if cs := must(s.ListChores(ctx, f.a, f.hA, true)); len(cs) != 1 {
		t.Fatalf("household A has %d chores", len(cs))
	}
	ar, err := s.Area(ctx, f.a, f.hA, f.areaA)
	if err != nil || ar.Name != f.areaN {
		t.Fatalf("area A changed: %+v %v", ar, err)
	}
	if as := must(s.ListAreas(ctx, f.a, f.hA)); len(as) != 1 {
		t.Fatalf("household A has %d areas", len(as))
	}
	h := must(s.ListCompletions(ctx, f.a, f.hA, f.choreA, 0))
	if len(h) != 1 || !h[0].RevertedAt.IsZero() {
		t.Fatalf("history A changed: %+v", h)
	}
	ms := must(s.ListMembers(ctx, f.a, f.hA))
	want := map[string]store.Role{f.a.UserID: store.RoleOwner, f.m.UserID: store.RoleMember, f.g.UserID: store.RoleGuest}
	if len(ms) != len(want) {
		t.Fatalf("members A changed: %+v", ms)
	}
	for _, m := range ms {
		if want[m.UserID] != m.Role {
			t.Fatalf("members A changed: %+v", ms)
		}
	}
}

func TestIsolationOutsider(t *testing.T) {
	// b (owner of B) and x (no household) try every operation on household A,
	// both naming A directly and naming B with A's IDs (ID guessing).
	f := setup(t)
	ctx := context.Background()
	cases := []struct {
		who   string
		actor store.Actor
		hh    string
	}{
		{"b on A", f.b, f.hA},
		{"x on A", f.x, f.hA},
		{"b on B with A ids", f.b, f.hB},
		{"x on unknown household", f.x, "00000000-0000-7000-8000-000000000000"},
	}
	for _, c := range cases {
		for _, o := range ops {
			t.Run(c.who+"/"+o.name, func(t *testing.T) {
				err := o.run(ctx, f.s, c.actor, c.hh, f)
				if c.hh == f.hB && !o.owner && (o.name == "Household" || o.name == "Role" || o.name == "ListMembers" ||
					o.name == "ListAreas" || o.name == "ListChores" || o.name == "CreateArea" || o.name == "CreateChore" ||
					o.name == "ExportHousehold" || o.name == "MergeHousehold") {
					// Household-level ops on b's own household legitimately succeed;
					// they must just not expose A's data (checked below).
					if err != nil {
						t.Fatalf("own-household %s: %v", o.name, err)
					}
					return
				}
				if c.hh == f.hB && o.owner {
					// Targets user m, who is not a member of B.
					if o.name == "AddMember" || o.name == "ExportWebhooks" {
						if err != nil {
							t.Fatalf("own-household AddMember: %v", err)
						}
						return
					}
				}
				if !errors.Is(err, store.ErrNotFound) {
					t.Fatalf("err = %v, want ErrNotFound", err)
				}
			})
		}
	}
	assertIntact(t, f)

	// b's own listings contain nothing from A.
	for _, c := range must(f.s.ListChores(ctx, f.b, f.hB, true)) {
		if c.ID == f.choreA || c.HouseholdID != f.hB {
			t.Fatalf("b sees foreign chore %+v", c)
		}
	}
	for _, a := range must(f.s.ListAreas(ctx, f.b, f.hB)) {
		if a.ID == f.areaA {
			t.Fatal("b sees area A")
		}
	}
	for _, m := range must(f.s.ListMembers(ctx, f.b, f.hB)) {
		if m.UserID == f.a.UserID || m.UserID == f.m.UserID || m.UserID == f.g.UserID {
			t.Fatal("b sees a member of A")
		}
	}
	for _, h := range must(f.s.ListHouseholds(ctx, f.b)) {
		if h.ID == f.hA {
			t.Fatal("b lists household A")
		}
	}

	// References across households are rejected when writing in one's own.
	in := choreIn("Sneaky")
	in.AreaID = f.areaA
	_, err := f.s.CreateChore(ctx, f.b, f.hB, in)
	is(t, err, store.ErrInvalid)
	in = choreIn("Sneaky")
	in.Assignees = []string{f.a.UserID}
	_, err = f.s.CreateChore(ctx, f.b, f.hB, in)
	is(t, err, store.ErrInvalid)
	in = choreIn("Sneaky")
	in.AreaID = f.areaA
	_, err = f.s.UpdateChore(ctx, f.b, f.hB, f.choreB, in)
	is(t, err, store.ErrInvalid)
	// And A's members cannot reach B's data either.
	_, err = f.s.Chore(ctx, f.a, f.hA, f.choreB)
	is(t, err, store.ErrNotFound)
	is(t, f.s.RenameArea(ctx, f.a, f.hA, f.areaB, "x"), store.ErrNotFound)
	assertIntact(t, f)
}

func TestIsolationGuest(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	for _, o := range ops {
		t.Run(o.name, func(t *testing.T) {
			err := o.run(ctx, f.s, f.g, f.hA, f)
			if o.write {
				is(t, err, store.ErrForbidden)
			} else if err != nil {
				t.Fatalf("guest read: %v", err)
			}
		})
	}
	assertIntact(t, f)
}

func TestIsolationMemberCannotManage(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	for _, o := range ops {
		if !o.owner {
			continue
		}
		t.Run(o.name, func(t *testing.T) {
			is(t, o.run(ctx, f.s, f.m, f.hA, f), store.ErrForbidden)
		})
	}
	// Removing someone else is owner-only; even the guest.
	is(t, f.s.RemoveMember(ctx, f.m, f.hA, f.g.UserID), store.ErrForbidden)
	assertIntact(t, f)
}
