package api

import (
	"context"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

type delFixture struct {
	e                *env
	ann, bob, cy, gu store.User
	home, solo       string
}

// newDelFixture: Ann owns Home (with Bob, Cy and a guest) and Solo; Dee and Eve
// share an unrelated household.
func newDelFixture(t *testing.T) *delFixture {
	t.Helper()
	e := newEnv(t)
	f := &delFixture{e: e, ann: e.user("ann", false), bob: e.user("bob", false), cy: e.user("cy", false), gu: e.user("gu", false)}
	ctx := context.Background()
	act := func(u store.User) store.Actor { return store.Actor{UserID: u.ID} }
	mk := func(owner store.User, name string) string {
		h, err := e.st.CreateHousehold(ctx, act(owner), store.NewHousehold{Name: name, Timezone: "Europe/Amsterdam"})
		if err != nil {
			t.Fatal(err)
		}
		return h.ID
	}
	add := func(hh string, owner, u store.User, role store.Role) {
		e.clock.Add(time.Minute) // distinct join times
		if err := e.st.AddMember(ctx, act(owner), hh, u.ID, role); err != nil {
			t.Fatal(err)
		}
	}
	f.home, f.solo = mk(f.ann, "Home"), mk(f.ann, "Solo")
	add(f.home, f.ann, f.gu, store.RoleGuest)
	add(f.home, f.ann, f.bob, store.RoleMember)
	add(f.home, f.ann, f.cy, store.RoleMember)
	return f
}

func TestAccountDeletion(t *testing.T) {
	f := newDelFixture(t)
	e := f.e
	dee, eve := e.user("dee", false), e.user("eve", false)
	other, err := e.st.CreateHousehold(context.Background(), store.Actor{UserID: dee.ID}, store.NewHousehold{Name: "Other", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.AddMember(context.Background(), store.Actor{UserID: dee.ID}, other.ID, eve.ID, store.RoleMember); err != nil {
		t.Fatal(err)
	}

	// Anonymous: refused. Signed in without step-up: refused.
	st, v := e.client().do("DELETE", "/api/v1/account", map[string]string{"confirmUsername": "ann"})
	expect(t, st, v, 401, "unauthenticated")
	st, v = e.client().do("GET", "/api/v1/account/deletion-preview", nil)
	expect(t, st, v, 401, "unauthenticated")
	annNoStep := e.signedIn("ann")
	st, v = annNoStep.do("DELETE", "/api/v1/account", map[string]string{"confirmUsername": "ann"})
	expect(t, st, v, 403, "step_up_required")

	// Preview: Solo is deleted, Home needs an owner (candidates: Bob and Cy, no guest).
	st, v = annNoStep.do("GET", "/api/v1/account/deletion-preview", nil)
	expect(t, st, v, 200, "")
	del, _ := field(v, "deleteHouseholds").([]any)
	need, _ := field(v, "needOwner").([]any)
	if len(del) != 1 || field(del[0], "id") != f.solo || len(need) != 1 || field(need[0], "id") != f.home {
		t.Fatalf("preview = %v", v)
	}
	if cands, _ := field(need[0], "candidates").([]any); len(cands) != 2 || field(cands[0], "userId") != f.bob.ID || field(cands[1], "userId") != f.cy.ID {
		t.Fatalf("candidates = %v", field(need[0], "candidates"))
	}
	// Another user's preview shows only their own households.
	st, v = e.signedIn("dee").do("GET", "/api/v1/account/deletion-preview", nil)
	if l, _ := field(v, "needOwner").([]any); st != 200 || len(l) != 1 || field(l[0], "id") != other.ID {
		t.Fatalf("dee preview = %d %v", st, v)
	}

	ann := e.signedInStepUp("ann")
	// Wrong confirmation, unknown fields, missing owner, guest as owner.
	st, v = ann.do("DELETE", "/api/v1/account", map[string]string{"confirmUsername": "someone"})
	expect(t, st, v, 400, "confirmation_mismatch")
	st, v = ann.do("DELETE", "/api/v1/account", nil)
	expect(t, st, v, 400, "invalid_json")
	st, v = ann.do("DELETE", "/api/v1/account", map[string]string{"confirmUsername": "Ann"})
	expect(t, st, v, 409, "owner_required")
	if hs, _ := field(v, "households").([]any); len(hs) != 1 || field(hs[0], "id") != f.home {
		t.Fatalf("409 body = %v", v)
	}
	st, v = ann.do("DELETE", "/api/v1/account", map[string]any{"confirmUsername": "ann", "newOwners": map[string]string{f.home: f.gu.ID}})
	expect(t, st, v, 400, "invalid")
	st, _ = e.signedIn("ann").do("GET", "/api/v1/households/"+f.solo, nil)
	if st != 200 {
		t.Fatalf("a refused deletion changed something: %d", st)
	}

	// Delete, passing Home to Cy.
	st, v = ann.do("DELETE", "/api/v1/account", map[string]any{"confirmUsername": "ann", "newOwners": map[string]string{f.home: f.cy.ID}})
	expect(t, st, v, 204, "")
	st, v = ann.do("GET", "/api/v1/session", nil)
	if field(v, "authenticated") == true {
		t.Fatalf("still signed in: %d %v", st, v)
	}
	st, v = e.client().login("ann", nil)
	expect(t, st, v, 401, "invalid_credentials")

	cy := e.signedIn("cy")
	st, v = cy.do("GET", "/api/v1/households/"+f.home, nil)
	if st != 200 || field(v, "role") != "owner" {
		t.Fatalf("cy's role: %d %v", st, v)
	}
	st, v = e.signedIn("bob").do("GET", "/api/v1/households/"+f.solo, nil)
	expect(t, st, v, 404, "not_found")
	// Isolation: the other household and its members are untouched.
	st, v = e.signedIn("eve").do("GET", "/api/v1/households/"+other.ID+"/members", nil)
	if l, _ := v.([]any); st != 200 || len(l) != 2 {
		t.Fatalf("other household: %d %v", st, v)
	}
	// Ann's audit trail is anonymised and the deletion itself is recorded.
	if evs, err := e.st.ListAuditForUser(context.Background(), f.ann.ID, 50); err != nil || len(evs) != 0 {
		t.Fatalf("audit for deleted user = %v, %v", evs, err)
	}
}

func TestAccountDeletionLastAdmin(t *testing.T) {
	e := newEnv(t)
	e.user("root", true)
	root := e.signedInStepUp("root")
	st, v := root.do("DELETE", "/api/v1/account", map[string]string{"confirmUsername": "root"})
	expect(t, st, v, 409, "last_admin")
}

func TestLeaveHouseholdAsLastOwner(t *testing.T) {
	f := newDelFixture(t)
	e := f.e
	base := "/api/v1/households/" + f.home + "/members/" + f.ann.ID
	ann := e.signedIn("ann")

	st, v := ann.do("DELETE", base, nil)
	expect(t, st, v, 409, "owner_required")
	if hs, _ := field(v, "households").([]any); len(hs) != 1 {
		t.Fatalf("409 body = %v", v)
	}
	st, v = ann.do("DELETE", base, map[string]any{"newOwner": f.gu.ID}) // a guest is never promoted
	expect(t, st, v, 400, "invalid")
	st, v = ann.do("DELETE", base, map[string]any{"newOwner": f.bob.ID})
	expect(t, st, v, 204, "")
	st, v = e.signedIn("bob").do("GET", "/api/v1/households/"+f.home, nil)
	if st != 200 || field(v, "role") != "owner" {
		t.Fatalf("bob: %d %v", st, v)
	}
	st, v = ann.do("GET", "/api/v1/households/"+f.home, nil)
	expect(t, st, v, 404, "not_found")

	// Sole member: deleting the household takes an explicit confirmation.
	solo := "/api/v1/households/" + f.solo + "/members/" + f.ann.ID
	st, v = ann.do("DELETE", solo, nil)
	expect(t, st, v, 409, "household_would_be_deleted")
	st, v = ann.do("DELETE", solo, map[string]any{"confirmDelete": true})
	expect(t, st, v, 204, "")
	st, v = ann.do("GET", "/api/v1/households/"+f.solo, nil)
	expect(t, st, v, 404, "not_found")

	// Another household's member cannot leave someone else's household for them.
	st, v = e.signedIn("cy").do("DELETE", "/api/v1/households/"+f.home+"/members/"+f.bob.ID, nil)
	expect(t, st, v, 403, "forbidden")
}

func TestAdminInactiveSettings(t *testing.T) {
	e := newEnv(t)
	e.user("root", true)
	root := e.signedInStepUp("root")
	st, v := root.do("GET", "/api/v1/admin/settings", nil)
	if st != 200 || field(v, "inactiveEnabled") != false || field(v, "inactiveWarnAfterDays") != float64(365) || field(v, "inactiveDeleteAfterDays") != float64(548) {
		t.Fatalf("defaults: %d %v", st, v)
	}
	st, v = root.do("PATCH", "/api/v1/admin/settings", map[string]any{"inactiveEnabled": true, "inactiveWarnAfterDays": 200, "inactiveDeleteAfterDays": 300})
	if st != 200 || field(v, "inactiveEnabled") != true || field(v, "inactiveWarnAfterDays") != float64(200) || field(v, "inactiveDeleteAfterDays") != float64(300) {
		t.Fatalf("patch: %d %v", st, v)
	}
	for _, bad := range []map[string]any{
		{"inactiveWarnAfterDays": 10},                                  // below the minimum
		{"inactiveDeleteAfterDays": 210},                               // less than 30 days after the warning
		{"inactiveWarnAfterDays": 290},                                 // leaves under 30 days before deletion
		{"inactiveDeleteAfterDays": 99999},                             // above the maximum
		{"inactiveWarnAfterDays": 400, "inactiveDeleteAfterDays": 400}, // not after the warning
	} {
		st, v = root.do("PATCH", "/api/v1/admin/settings", bad)
		expect(t, st, v, 400, "invalid")
	}
	e.user("ann", false)
	st, v = e.signedIn("ann").do("PATCH", "/api/v1/admin/settings", map[string]any{"inactiveEnabled": false})
	expect(t, st, v, 403, "forbidden")
}
