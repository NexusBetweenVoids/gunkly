package api

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// rotationFixture: household A with owner a and members m and b, and the
// chore "Bins" taking turns in the order a, m, b.
func rotationFixture(t *testing.T, advanceOn string) (f *hhFixture, chore string, a, m, b string) {
	t.Helper()
	f = newHH(t)
	ctx := context.Background()
	au, _ := f.e.st.UserByLogin(ctx, "a")
	bu, _ := f.e.st.UserByLogin(ctx, "b")
	if err := f.e.st.AddMember(ctx, store.Actor{UserID: au.ID}, f.hA, bu.ID, store.RoleMember); err != nil {
		t.Fatal(err)
	}
	a, m, b = au.ID, f.mID, bu.ID
	body := intervalBody("Bins", "2026-11-14")
	body["assignees"], body["rotation"] = []string{a, m, b}, true
	if advanceOn != "" {
		body["advanceOn"] = advanceOn
	}
	chore = f.mk(t, f.a, body)
	return
}

func stringsOf(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func TestRotationAdvanceAnyAndRevert(t *testing.T) {
	f, chore, a, m, b := rotationFixture(t, "")
	st, v := f.a.do("GET", f.chore(chore), nil)
	expect(t, st, v, 200, "")
	if str(v, "advanceOn") != "any" || str(v, "turnUserId") != a || !slices.Equal(stringsOf(field(v, "assignees")), []string{a, m, b}) {
		t.Fatalf("created: %v", v)
	}
	// m completes while it is a's turn: the turn goes to m's successor.
	st, v = f.m.do("POST", f.chore(chore)+"/complete", nil)
	expect(t, st, v, 201, "")
	if got := str(field(v, "chore"), "turnUserId"); got != b {
		t.Fatalf("turn after m completes = %s, want b", got)
	}
	older := str(field(v, "completion"), "id")
	f.e.clock.Add(time.Minute)
	// The order wraps: b completes, a is next.
	st, v = f.b.do("POST", f.chore(chore)+"/complete", nil)
	expect(t, st, v, 201, "")
	if got := str(field(v, "chore"), "turnUserId"); got != a {
		t.Fatalf("turn after b completes = %s, want a", got)
	}
	latest := str(field(v, "completion"), "id")
	// Reverting an older completion leaves the turn; the latest gives it back.
	st, v = f.a.do("POST", f.chore(chore)+"/completions/"+older+"/revert", nil)
	expect(t, st, v, 200, "")
	if got := str(v, "turnUserId"); got != a {
		t.Fatalf("reverting an older completion changed the turn to %s", got)
	}
	st, v = f.a.do("POST", f.chore(chore)+"/completions/"+latest+"/revert", nil)
	expect(t, st, v, 200, "")
	if got := str(v, "turnUserId"); got != b {
		t.Fatalf("turn after revert = %s, want b", got)
	}
}

func TestRotationAdvanceAssigneeOnly(t *testing.T) {
	f, chore, a, m, _ := rotationFixture(t, "assignee")
	st, v := f.m.do("POST", f.chore(chore)+"/complete", nil) // not m's turn
	expect(t, st, v, 201, "")
	if got := str(field(v, "chore"), "turnUserId"); got != a {
		t.Fatalf("turn moved to %s although the person whose turn it is did not complete", got)
	}
	f.e.clock.Add(time.Minute)
	st, v = f.a.do("POST", f.chore(chore)+"/complete", nil)
	expect(t, st, v, 201, "")
	if got := str(field(v, "chore"), "turnUserId"); got != m {
		t.Fatalf("turn = %s, want m", got)
	}
}

func TestRotationSkipKeepsTurnAndSetTurn(t *testing.T) {
	f, chore, a, m, b := rotationFixture(t, "")
	st, v := f.a.do("POST", f.chore(chore)+"/skip", nil)
	expect(t, st, v, 200, "")
	if got := str(v, "turnUserId"); got != a {
		t.Fatalf("skip moved the turn to %s", got)
	}
	st, v = f.m.do("PATCH", f.chore(chore), map[string]string{"turnUserId": b})
	expect(t, st, v, 200, "")
	if str(v, "turnUserId") != b {
		t.Fatalf("set turn: %v", v)
	}
	st, v = f.g.do("PATCH", f.chore(chore), map[string]string{"turnUserId": m})
	expect(t, st, v, 403, "forbidden")
	st, v = f.a.do("PATCH", f.chore(chore), map[string]string{"turnUserId": f.gID})
	expect(t, st, v, 400, "invalid")
	st, v = f.a.do("PATCH", f.chore(chore), map[string]string{})
	expect(t, st, v, 400, "invalid")
	plain := f.mk(t, f.a, intervalBody("Plain", "2026-11-14"))
	st, v = f.a.do("PATCH", f.chore(plain), map[string]string{"turnUserId": a})
	expect(t, st, v, 409, "conflict")
}

func TestRotationValidationAndKeep(t *testing.T) {
	f, chore, a, m, b := rotationFixture(t, "")
	one := intervalBody("One", "2026-11-14")
	one["assignees"], one["rotation"] = []string{a}, true
	st, v := f.a.do("POST", f.base()+"/chores", one)
	expect(t, st, v, 400, "invalid")
	oneoff := oneOffBody("Once", "2026-11-14")
	oneoff["assignees"], oneoff["rotation"] = []string{a, m}, true
	st, v = f.a.do("POST", f.base()+"/chores", oneoff)
	expect(t, st, v, 400, "invalid")
	bad := intervalBody("Bad", "2026-11-14")
	bad["assignees"], bad["rotation"], bad["advanceOn"] = []string{a, m}, true, "nobody"
	st, v = f.a.do("POST", f.base()+"/chores", bad)
	expect(t, st, v, 400, "invalid")

	// Reordering keeps rotation (omitted = keep) and the current turn (m).
	f.a.do("PATCH", f.chore(chore), map[string]string{"turnUserId": m})
	body := intervalBody("Bins", "2026-11-14")
	body["assignees"] = []string{b, m, a}
	st, v = f.a.do("PUT", f.chore(chore), body)
	expect(t, st, v, 200, "")
	if field(v, "rotation") != true || str(v, "turnUserId") != m || !slices.Equal(stringsOf(field(v, "assignees")), []string{b, m, a}) {
		t.Fatalf("after reorder: %v", v)
	}
	// Dropping a person keeps the turn; dropping the turn person gives it to the first.
	body["assignees"] = []string{b, m}
	_, v = f.a.do("PUT", f.chore(chore), body)
	if str(v, "turnUserId") != m {
		t.Fatalf("turn after removing a: %v", v)
	}
	body["assignees"] = []string{b, a}
	_, v = f.a.do("PUT", f.chore(chore), body)
	if str(v, "turnUserId") != b {
		t.Fatalf("turn after removing the turn person: %v", v)
	}
	// Down to one assignee: a kept rotation stops, an explicit one is refused.
	body["assignees"] = []string{b}
	st, v = f.a.do("PUT", f.chore(chore), body)
	expect(t, st, v, 200, "")
	if field(v, "rotation") != false || field(v, "turnUserId") != nil {
		t.Fatalf("one assignee left: %v", v)
	}
	body["rotation"] = true
	st, v = f.a.do("PUT", f.chore(chore), body)
	expect(t, st, v, 400, "invalid")
}

func TestRotationMineFilter(t *testing.T) {
	f, chore, _, m, _ := rotationFixture(t, "")
	f.mk(t, f.a, intervalBody("Anyone", "2026-11-14"))
	mine := func(c *client) []string {
		st, v := c.do("GET", f.base()+"/chores?mine=1", nil)
		expect(t, st, v, 200, "")
		var out []string
		for _, ch := range list(t, v) {
			out = append(out, str(ch, "name"))
		}
		slices.Sort(out)
		return out
	}
	if got := mine(f.a); !slices.Equal(got, []string{"Anyone", "Bins"}) {
		t.Fatalf("a's turn: %v", got)
	}
	if got := mine(f.m); !slices.Equal(got, []string{"Anyone"}) {
		t.Fatalf("m is assignee but not on turn: %v", got)
	}
	f.a.do("PATCH", f.chore(chore), map[string]string{"turnUserId": m})
	if got := mine(f.a); !slices.Equal(got, []string{"Anyone"}) {
		t.Fatalf("a after the turn moved: %v", got)
	}
}

func TestRotationLeaveAndDemote(t *testing.T) {
	f, chore, a, m, b := rotationFixture(t, "")
	ctx := context.Background()
	f.a.do("PATCH", f.chore(chore), map[string]string{"turnUserId": m})
	// Demoting m (whose turn it is) to guest: out of the rotation, the turn moves on to b.
	if err := f.e.st.ChangeRole(ctx, store.Actor{UserID: a}, f.hA, m, store.RoleGuest); err != nil {
		t.Fatal(err)
	}
	st, v := f.a.do("GET", f.chore(chore), nil)
	expect(t, st, v, 200, "")
	if str(v, "turnUserId") != b || !slices.Equal(stringsOf(field(v, "assignees")), []string{a, b}) || field(v, "rotation") != true {
		t.Fatalf("after demote: %v", v)
	}
	// b leaves: one person left, rotation is off, a stays the plain assignee.
	if err := f.e.st.RemoveMember(ctx, store.Actor{UserID: b}, f.hA, b); err != nil {
		t.Fatal(err)
	}
	_, v = f.a.do("GET", f.chore(chore), nil)
	if field(v, "rotation") != false || field(v, "turnUserId") != nil || !slices.Equal(stringsOf(field(v, "assignees")), []string{a}) {
		t.Fatalf("after leave: %v", v)
	}
}

func TestRotationExportImportRoundTrip(t *testing.T) {
	f, chore, a, m, b := rotationFixture(t, "assignee")
	f.a.do("PATCH", f.chore(chore), map[string]string{"turnUserId": m})
	resp := f.a.raw("GET", f.base()+"/export", nil, "")
	defer resp.Body.Close()
	var doc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	var exp any
	for _, c := range doc["chores"].([]any) {
		if str(c, "name") == "Bins" {
			exp = c
		}
	}
	if exp == nil || field(exp, "rotation") != true || str(exp, "rotationAdvance") != "assignee" || str(field(exp, "turn"), "username") != "m" {
		t.Fatalf("exported chore: %v", exp)
	}
	var names []string
	for _, p := range list(t, field(exp, "assignees")) {
		names = append(names, str(p, "username"))
	}
	if !slices.Equal(names, []string{"a", "m", "b"}) {
		t.Fatalf("assignee order %v", names)
	}
	// Merge into A: the copy keeps order, rotation, advance mode and turn.
	st, v := f.a.do("POST", f.base()+"/import", doc)
	expect(t, st, v, 200, "")
	_, cl := f.a.do("GET", f.base()+"/chores", nil)
	copies := 0
	for _, c := range list(t, cl) {
		if str(c, "name") != "Bins" || str(c, "id") == chore {
			continue
		}
		copies++
		if field(c, "rotation") != true || str(c, "advanceOn") != "assignee" || str(c, "turnUserId") != m ||
			!slices.Equal(stringsOf(field(c, "assignees")), []string{a, m, b}) {
			t.Fatalf("imported copy: %v", c)
		}
	}
	if copies != 1 {
		t.Fatalf("copies = %d", copies)
	}
	// The strict parser refuses a bad advance mode and a turn without rotation.
	for _, edit := range []func(c map[string]any){
		func(c map[string]any) { c["rotationAdvance"] = "everyone" },
		func(c map[string]any) { c["rotation"] = false },
	} {
		raw, _ := json.Marshal(doc)
		var cp map[string]any
		_ = json.Unmarshal(raw, &cp)
		for _, c := range cp["chores"].([]any) {
			if str(c, "name") == "Bins" {
				edit(c.(map[string]any))
			}
		}
		if st, v = f.a.do("POST", f.base()+"/import", cp); st != 400 {
			t.Fatalf("invalid rotation accepted: %d %v", st, v)
		}
	}
	// Into B (only b is a member there): the assignees, and so the rotation, are dropped.
	st, v = f.b.do("POST", "/api/v1/households/"+f.hB+"/import", doc)
	expect(t, st, v, 200, "")
}

func TestRotationMCP(t *testing.T) {
	f := mcpTestEnv(t)
	ctx := context.Background()
	au, _ := f.e.st.UserByLogin(ctx, "a")
	body := intervalBody("Bins", "2026-11-14")
	body["assignees"], body["rotation"] = []string{f.mID, au.ID}, true
	f.mk(t, f.a, body)
	secret, _ := mkToken(t, f.aStep, []string{scRead}, f.hA)
	cs := f.connect(t, secret)
	one := func(args map[string]any) []any { return mustOK(t, cs, "list_chores", args)["chores"].([]any) }
	all := one(nil)
	if len(all) != 1 || field(all[0], "takesTurns") != true || str(all[0], "turnUserId") != f.mID || field(all[0], "myTurn") == true {
		t.Fatalf("list_chores: %v", all)
	}
	if got := one(map[string]any{"mine": true}); len(got) != 0 {
		t.Fatalf("mine while it is m's turn: %v", got)
	}
	f.a.do("PATCH", f.base()+"/chores/"+str(all[0], "id"), map[string]string{"turnUserId": au.ID})
	got := one(map[string]any{"mine": true})
	if len(got) != 1 || field(got[0], "myTurn") != true {
		t.Fatalf("mine on a's turn: %v", got)
	}
}
