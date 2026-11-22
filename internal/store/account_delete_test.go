package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/db"
	"github.com/NexusBetweenVoids/gunkly/internal/db/dbtest"
	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

type delClock struct{ t time.Time }

func (c *delClock) now() time.Time { return c.t }

// tick moves the clock so memberships get distinct, ordered join times.
func (c *delClock) tick() { c.t = c.t.Add(time.Minute) }

type delEnv struct {
	t   *testing.T
	d   *db.DB
	s   *store.Store
	clk *delClock
	ctx context.Context
}

func newDelEnv(t *testing.T) *delEnv {
	t.Helper()
	d := dbtest.Open(t)
	clk := &delClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	return &delEnv{t: t, d: d, s: store.New(d).WithClock(clk.now), clk: clk, ctx: context.Background()}
}

func (e *delEnv) user(name string) store.Actor {
	e.t.Helper()
	e.clk.tick()
	return mkUser(e.t, e.s, name)
}

func (e *delEnv) join(owner store.Actor, hid string, u store.Actor, role store.Role) {
	e.t.Helper()
	e.clk.tick()
	noErr(e.t, e.s.AddMember(e.ctx, owner, hid, u.UserID, role))
}

func (e *delEnv) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.d.QueryRowContext(e.ctx, e.d.Dialect.Rebind(q), args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *delEnv) role(a store.Actor, hid string) (store.Role, error) { return e.s.Role(e.ctx, a, hid) }

func ownerRequired(t *testing.T, err error) *store.OwnerRequiredError {
	t.Helper()
	var oe *store.OwnerRequiredError
	if !errors.As(err, &oe) || !errors.Is(err, store.ErrOwnerRequired) {
		t.Fatalf("err = %v, want OwnerRequiredError", err)
	}
	return oe
}

// world: household H (leaver A owner, B member, G guest, C member joined
// later), a solo household of A, and a household of A with only a guest.
type delWorld struct {
	a, b, c, g     store.Actor
	h, solo, gonly string
}

func (e *delEnv) world() delWorld {
	w := delWorld{a: e.user("alice"), b: e.user("bob"), c: e.user("carol"), g: e.user("guest")}
	w.h = mkHousehold(e.t, e.s, w.a, "Home")
	e.join(w.a, w.h, w.g, store.RoleGuest) // joined before B, but guests never inherit
	e.join(w.a, w.h, w.b, store.RoleMember)
	e.join(w.a, w.h, w.c, store.RoleMember)
	w.solo = mkHousehold(e.t, e.s, w.a, "Solo")
	w.gonly = mkHousehold(e.t, e.s, w.a, "GuestsOnly")
	e.join(w.a, w.gonly, w.g, store.RoleGuest)
	return w
}

func TestAccountDeletionPreview(t *testing.T) {
	e := newDelEnv(t)
	w := e.world()
	p := must(e.s.AccountDeletionPreview(e.ctx, w.a.UserID))
	if len(p.DeleteHouseholds) != 2 || len(p.NeedOwner) != 1 || p.NeedOwner[0].HouseholdID != w.h {
		t.Fatalf("preview = %+v", p)
	}
	got := p.NeedOwner[0].Candidates
	if len(got) != 2 || got[0].UserID != w.b.UserID || got[1].UserID != w.c.UserID {
		t.Fatalf("candidates (longest-standing first, no guests) = %+v", got)
	}
	// Bob is not the last owner of anything.
	if p := must(e.s.AccountDeletionPreview(e.ctx, w.b.UserID)); len(p.DeleteHouseholds)+len(p.NeedOwner) != 0 {
		t.Fatalf("member preview = %+v", p)
	}
}

func TestDeleteAccount(t *testing.T) {
	e := newDelEnv(t)
	w := e.world()
	s, ctx := e.s, e.ctx

	// Chores: one assigned only to A, one to A and B, one to B; completions by A.
	c1 := must(s.CreateChore(ctx, w.b, w.h, withAssignees(choreIn("only-a"), w.a.UserID)))
	c2 := must(s.CreateChore(ctx, w.b, w.h, withAssignees(choreIn("a-and-b"), w.a.UserID, w.b.UserID)))
	c3 := must(s.CreateChore(ctx, w.b, w.h, withAssignees(choreIn("only-b"), w.b.UserID)))
	for _, id := range []string{c1.ID, c2.ID} {
		if _, _, err := s.CompleteChoreAuto(ctx, w.a, w.h, id, "", "app", ""); err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.CreateChore(ctx, w.a, w.solo, choreIn("solo-chore"))
	noErr(t, err)

	// Personal data of A, and an audit trail naming A.
	must(s.CreateUserChannel(ctx, w.a, store.NewChannel{ID: ids.New(), Service: "telegram", Ciphertext: "v1:x", Max: 5}))
	must(s.CreatePushSubscription(ctx, w.a, store.NewPushSubscription{ID: ids.New(), Endpoint: "https://push.example/a", Ciphertext: "v1:k", Max: 5}))
	must(s.CreateSession(ctx, store.NewSession{UserID: w.a.UserID, TokenHash: "h1", ExpiresAt: e.clk.t.Add(time.Hour)}))
	noErr(t, s.Audit(ctx, store.AuditEvent{ActorUserID: w.a.UserID, Event: store.AuditLoginSuccess, IP: "203.0.113.9", Meta: map[string]string{"k": "v"}}))
	noErr(t, s.Audit(ctx, store.AuditEvent{ActorUserID: w.b.UserID, Event: store.AuditMemberRemove, HouseholdID: w.h, Meta: map[string]string{"user": w.a.UserID}}))

	// Another tenant that must not be touched.
	d, f := e.user("dora"), e.user("fred")
	other := mkHousehold(t, s, d, "Other")
	e.join(d, other, f, store.RoleMember)
	oc := must(s.CreateChore(ctx, d, other, withAssignees(choreIn("other"), d.UserID, f.UserID)))

	// No successor chosen: nothing is deleted and the households are listed.
	_, err = s.DeleteAccount(ctx, w.a.UserID, store.AccountDeletion{})
	if oe := ownerRequired(t, err); len(oe.Households) != 1 || oe.Households[0].HouseholdID != w.h {
		t.Fatalf("owner required = %+v", oe)
	}
	must(s.UserByID(ctx, w.a.UserID))
	if _, err := s.Household(ctx, w.a, w.solo); err != nil {
		t.Fatalf("solo household deleted by a failed deletion: %v", err)
	}
	// A guest, a stranger or a non-member cannot be chosen.
	for _, bad := range []string{w.g.UserID, d.UserID, "nobody"} {
		_, err = s.DeleteAccount(ctx, w.a.UserID, store.AccountDeletion{NewOwners: map[string]string{w.h: bad}})
		is(t, err, store.ErrInvalid)
	}

	// Explicit choice: Carol, although Bob has been there longer.
	res := must(s.DeleteAccount(ctx, w.a.UserID, store.AccountDeletion{NewOwners: map[string]string{w.h: w.c.UserID}}))
	if len(res.DeletedHouseholds) != 2 || len(res.Transfers) != 1 || res.Transfers[0].NewOwnerID != w.c.UserID {
		t.Fatalf("result = %+v", res)
	}
	_, err = s.UserByID(ctx, w.a.UserID)
	is(t, err, store.ErrNotFound)
	if r, err := e.role(w.c, w.h); err != nil || r != store.RoleOwner {
		t.Fatalf("carol role = %v, %v", r, err)
	}
	if r, _ := e.role(w.g, w.h); r != store.RoleGuest {
		t.Fatalf("guest role = %v", r)
	}
	for _, hid := range []string{w.solo, w.gonly} {
		if n := e.count("SELECT count(*) FROM households WHERE id = ?", hid); n != 0 {
			t.Errorf("household %s not deleted", hid)
		}
	}
	if n := e.count("SELECT count(*) FROM chores WHERE household_id = ?", w.solo); n != 0 {
		t.Errorf("solo chores remain: %d", n)
	}

	// Chores: only-a is unassigned, a-and-b keeps B, only-b untouched; the
	// completions survive without a user.
	assignees := func(hid, id string) []string { return must(s.Chore(ctx, w.b, hid, id)).Assignees }
	if got := assignees(w.h, c1.ID); len(got) != 0 {
		t.Errorf("only-a assignees = %v, want none", got)
	}
	if got := assignees(w.h, c2.ID); len(got) != 1 || got[0] != w.b.UserID {
		t.Errorf("a-and-b assignees = %v", got)
	}
	if got := assignees(w.h, c3.ID); len(got) != 1 || got[0] != w.b.UserID {
		t.Errorf("only-b assignees = %v", got)
	}
	comps := must(s.ListCompletions(ctx, w.b, w.h, c1.ID, 10))
	if len(comps) != 1 || comps[0].UserID != "" {
		t.Errorf("completions = %+v, want one anonymised", comps)
	}

	// Everything personal is gone.
	for _, q := range []string{
		"SELECT count(*) FROM sessions WHERE user_id = ?", "SELECT count(*) FROM notification_channels WHERE user_id = ?",
		"SELECT count(*) FROM push_subscriptions WHERE user_id = ?", "SELECT count(*) FROM memberships WHERE user_id = ?",
		"SELECT count(*) FROM audit_log WHERE actor_user_id = ?",
	} {
		if n := e.count(q, w.a.UserID); n != 0 {
			t.Errorf("%s: %d rows remain", q, n)
		}
	}
	// Audit rows keep no personal data: no IP, no metadata naming the user.
	if n := e.count("SELECT count(*) FROM audit_log WHERE ip <> ''"); n != 0 {
		t.Errorf("audit rows with IP: %d", n)
	}
	if n := e.count("SELECT count(*) FROM audit_log WHERE meta LIKE ?", "%"+w.a.UserID+"%"); n != 0 {
		t.Errorf("audit rows naming the user: %d", n)
	}
	// ... but another actor's row about A keeps its actor.
	if n := e.count("SELECT count(*) FROM audit_log WHERE event = ? AND actor_user_id = ?", store.AuditMemberRemove, w.b.UserID); n != 1 {
		t.Errorf("B's member_remove row lost its actor: %d", n)
	}
	if n := e.count("SELECT count(*) FROM audit_log WHERE event = ?", store.AuditAccountDelete); n != 1 {
		t.Errorf("account_delete audit rows: %d", n)
	}
	if n := e.count("SELECT count(*) FROM audit_log WHERE event = ? AND household_id = ?", store.AuditOwnerTransfer, w.h); n != 1 {
		t.Errorf("owner_transfer audit rows: %d", n)
	}

	// Isolation: the other household is untouched.
	if ms := must(s.ListMembers(ctx, d, other)); len(ms) != 2 {
		t.Errorf("other members = %v", ms)
	}
	if got := must(s.Chore(ctx, d, other, oc.ID)).Assignees; len(got) != 2 {
		t.Errorf("other assignees = %v", got)
	}
}

func withAssignees(in store.ChoreInput, users ...string) store.ChoreInput {
	in.Assignees = users
	return in
}

func TestDeleteAccountAutoTransfer(t *testing.T) {
	e := newDelEnv(t)
	w := e.world()
	res := must(e.s.DeleteAccount(e.ctx, w.a.UserID, store.AccountDeletion{AutoTransfer: true}))
	// Bob has been a member longest (the guest joined earlier but is never promoted).
	if len(res.Transfers) != 1 || res.Transfers[0].NewOwnerID != w.b.UserID {
		t.Fatalf("transfers = %+v", res.Transfers)
	}
	if r, _ := e.role(w.b, w.h); r != store.RoleOwner {
		t.Fatalf("bob = %v", r)
	}
	if r, _ := e.role(w.g, w.h); r != store.RoleGuest {
		t.Fatalf("guest = %v", r)
	}
}

func TestDeleteAccountAdminAndIdle(t *testing.T) {
	e := newDelEnv(t)
	admin := must(e.s.CreateUser(e.ctx, store.NewUser{Username: "adm", DisplayName: "adm", PasswordHash: "x", IsAdmin: true}))
	_, err := e.s.DeleteAccount(e.ctx, admin.ID, store.AccountDeletion{})
	is(t, err, store.ErrLastAdmin) // the only admin
	admin2 := must(e.s.CreateUser(e.ctx, store.NewUser{Username: "adm2", DisplayName: "adm2", PasswordHash: "x", IsAdmin: true}))
	_, err = e.s.DeleteAccount(e.ctx, admin2.ID, store.AccountDeletion{RefuseAdmin: true})
	is(t, err, store.ErrLastAdmin)                                     // system processes never delete admins
	must(e.s.DeleteAccount(e.ctx, admin2.ID, store.AccountDeletion{})) // a user may delete themselves while another admin remains

	// A conditional deletion loses against later activity.
	u := e.user("idle")
	_, err = e.s.DeleteAccount(e.ctx, u.UserID, store.AccountDeletion{IdleBefore: e.clk.t.Add(-time.Hour)})
	is(t, err, store.ErrConflict)
	must(e.s.DeleteAccount(e.ctx, u.UserID, store.AccountDeletion{IdleBefore: e.clk.t.Add(time.Hour)}))
	_, err = e.s.DeleteAccount(e.ctx, "nobody", store.AccountDeletion{})
	is(t, err, store.ErrNotFound)
}

func TestLeaveHousehold(t *testing.T) {
	e := newDelEnv(t)
	w := e.world()
	s, ctx := e.s, e.ctx

	// Last owner with other members: a successor is required and must be a non-guest member.
	_, err := s.LeaveHousehold(ctx, w.a, w.h, store.LeaveOptions{})
	_ = ownerRequired(t, err)
	_, err = s.LeaveHousehold(ctx, w.a, w.h, store.LeaveOptions{NewOwner: w.g.UserID})
	is(t, err, store.ErrInvalid)
	if r, err := e.role(w.a, w.h); err != nil || r != store.RoleOwner {
		t.Fatalf("failed leave changed alice: %v %v", r, err)
	}
	// A guest or member leaves without ceremony.
	must(s.LeaveHousehold(ctx, w.g, w.h, store.LeaveOptions{}))
	must(s.LeaveHousehold(ctx, w.c, w.h, store.LeaveOptions{}))
	// Alone with Bob: he takes over.
	res := must(s.LeaveHousehold(ctx, w.a, w.h, store.LeaveOptions{NewOwner: w.b.UserID}))
	if len(res.Transfers) != 1 {
		t.Fatalf("res = %+v", res)
	}
	if r, _ := e.role(w.b, w.h); r != store.RoleOwner {
		t.Fatalf("bob = %v", r)
	}
	if _, err := s.Household(ctx, w.a, w.h); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("alice still sees the household: %v", err)
	}

	// Sole member: leaving deletes the household, but only when confirmed.
	_, err = s.LeaveHousehold(ctx, w.a, w.solo, store.LeaveOptions{})
	is(t, err, store.ErrConflict)
	res = must(s.LeaveHousehold(ctx, w.a, w.solo, store.LeaveOptions{ConfirmDelete: true}))
	if len(res.DeletedHouseholds) != 1 || e.count("SELECT count(*) FROM households WHERE id = ?", w.solo) != 0 {
		t.Fatalf("solo household not deleted: %+v", res)
	}
	// Owner with only guests left: the household goes too (guests are never promoted).
	_, err = s.LeaveHousehold(ctx, w.a, w.gonly, store.LeaveOptions{ConfirmDelete: true})
	noErr(t, err)
	if e.count("SELECT count(*) FROM households WHERE id = ?", w.gonly) != 0 {
		t.Fatal("guest-only household kept")
	}
	// A non-member learns nothing.
	_, err = s.LeaveHousehold(ctx, w.c, w.h, store.LeaveOptions{})
	is(t, err, store.ErrNotFound)
}
