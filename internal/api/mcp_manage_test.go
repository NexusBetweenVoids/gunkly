package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func (f *tokenEnv) areasOn(t *testing.T) {
	t.Helper()
	st, v := f.a.do("PATCH", "/api/v1/households/"+f.hA, map[string]any{"areasEnabled": true})
	expect(t, st, v, 200, "")
}

func TestMCPUpdateChore(t *testing.T) {
	f := mcpTestEnv(t)
	f.areasOn(t)
	secret, _ := mkToken(t, f.aStep, []string{scRead, scWrite}, f.hA)
	cs := f.connect(t, secret)
	id := f.mk(t, f.a, intervalBody("Dishes", "2026-11-21"))

	kitchen := mustOK(t, cs, "create_area", map[string]any{"name": "Kitchen"})["area"].(map[string]any)["id"].(string)
	out := mustOK(t, cs, "update_chore", map[string]any{"chore": "dishes", "name": "Wash dishes", "notes": "hot water", "area": "kitchen",
		"assignees": []string{"me", f.mID}, "take_turns": true, "repeat": "FREQ=WEEKLY;BYDAY=MO,TH", "remind_at": "07:30"})["chore"].(map[string]any)
	if out["name"] != "Wash dishes" || out["notes"] != "hot water" || out["takesTurns"] != true || out["repeat"] != "RRULE:FREQ=WEEKLY;BYDAY=MO,TH" {
		t.Fatalf("updated %v", out)
	}
	st, v := f.a.do("GET", f.chore(id), nil)
	expect(t, st, v, 200, "")
	if str(v, "areaId") != kitchen || field(v, "rotation") != true || len(field(v, "assignees").([]any)) != 2 || str(v, "link") != "" {
		t.Fatalf("rest view %v", v)
	}
	if st, v := f.a.do("GET", f.chore(id)+"/reminders", nil); st != 200 || minutes(t, v)[0] != 450 {
		t.Fatalf("reminders %d %v", st, v)
	}

	// Partial: only the passed field changes; the rest (area, turns, schedule) stays.
	mustOK(t, cs, "update_chore", map[string]any{"chore": id, "link": "https://example.com/how"})
	_, v = f.a.do("GET", f.chore(id), nil)
	if str(v, "link") != "https://example.com/how" || str(v, "areaId") != kitchen || field(v, "rotation") != true || str(v, "name") != "Wash dishes" {
		t.Fatalf("partial update %v", v)
	}
	// Clearing the area, then back to a plain interval and one-off.
	mustOK(t, cs, "update_chore", map[string]any{"chore": id, "area": "", "take_turns": false, "repeat": "every 3 days", "advance_on": "assignee"})
	_, v = f.a.do("GET", f.chore(id), nil)
	if field(v, "areaId") != nil || field(v, "rotation") != false || str(v, "advanceOn") != "assignee" ||
		field(field(v, "schedule"), "interval").(map[string]any)["every"] != float64(3) {
		t.Fatalf("second update %v", v)
	}
	oneOff := mustOK(t, cs, "update_chore", map[string]any{"chore": id, "repeat": "once"})["chore"].(map[string]any)
	if oneOff["repeat"] != "once" {
		t.Fatalf("one-off %v", oneOff)
	}

	// Invalid and unknown input is an error and changes nothing.
	_, before := f.a.do("GET", f.chore(id), nil)
	for name, args := range map[string]map[string]any{
		"empty":        {"chore": id},
		"blank name":   {"chore": id, "name": "  "},
		"long name":    {"chore": id, "name": strings.Repeat("x", 201)},
		"bad link":     {"chore": id, "link": "javascript:alert(1)"},
		"bad repeat":   {"chore": id, "repeat": "sometimes"},
		"bad rrule":    {"chore": id, "repeat": "FREQ=SECONDLY"},
		"bad date":     {"chore": id, "first_due": "tomorrow"},
		"bad time":     {"chore": id, "remind_at": "25:00"},
		"bad preset":   {"chore": id, "reminder_preset": "nope"},
		"bad anchor":   {"chore": id, "anchor": "whenever"},
		"bad advance":  {"chore": id, "advance_on": "nobody"},
		"turns alone":  {"chore": id, "take_turns": true},
		"stranger":     {"chore": id, "assignees": []string{"0190f0e0-0000-7000-8000-000000000000"}},
		"unknown area": {"chore": id, "area": "Nowhere"},
		"unknown key":  {"chore": id, "name": "ok", "colour": "red"},
		"no chore":     {"chore": "ghost", "name": "x"},
	} {
		if out, isErr := callTool(t, cs, "update_chore", args); !isErr {
			t.Errorf("%s: accepted %v", name, out)
		}
	}
	_, v = f.a.do("GET", f.chore(id), nil)
	if str(v, "name") != "Wash dishes" || str(v, "updatedAt") != str(before, "updatedAt") {
		t.Fatalf("a refused update changed the chore: %v", v)
	}
}

func TestMCPAreasOffRefusesAreaWrites(t *testing.T) {
	f := mcpTestEnv(t)
	secret, _ := mkToken(t, f.aStep, []string{scRead, scWrite}, f.hA)
	cs := f.connect(t, secret)
	id := f.mk(t, f.a, intervalBody("Dishes", "2026-11-21"))
	mustFail(t, cs, "create_area", map[string]any{"name": "Kitchen"}, "turned off")
	mustFail(t, cs, "update_chore", map[string]any{"chore": id, "area": "Kitchen"}, "turned off")
	hs := mustOK(t, cs, "list_areas", nil)["households"].([]any)
	if len(hs) != 1 || field(hs[0], "areasEnabled") != false {
		t.Fatalf("list_areas %v", hs)
	}
}

func TestMCPAreas(t *testing.T) {
	f := mcpTestEnv(t)
	f.areasOn(t)
	secret, _ := mkToken(t, f.aStep, []string{scRead, scWrite}, f.hA)
	cs := f.connect(t, secret)
	a := mustOK(t, cs, "create_area", map[string]any{"name": "Garden"})["area"].(map[string]any)
	mustFail(t, cs, "create_area", map[string]any{"name": "garden"}, "already exists")
	mustFail(t, cs, "create_area", map[string]any{"name": " "}, "invalid")
	r := mustOK(t, cs, "rename_area", map[string]any{"area": "GARDEN", "name": "Yard"})["area"].(map[string]any)
	if r["id"] != a["id"] || r["name"] != "Yard" {
		t.Fatalf("renamed %v", r)
	}
	mustFail(t, cs, "rename_area", map[string]any{"area": "nope", "name": "x"}, "no area")
	hs := mustOK(t, cs, "list_areas", nil)["households"].([]any)
	as := field(hs[0], "areas").([]any)
	if field(hs[0], "areasEnabled") != true || len(as) != 1 || str(as[0], "name") != "Yard" {
		t.Fatalf("list %v", hs)
	}
	// Same data over REST.
	_, v := f.a.do("GET", "/api/v1/households/"+f.hA+"/areas", nil)
	if l := list(t, v); len(l) != 1 || str(l[0], "name") != "Yard" {
		t.Fatalf("rest %v", v)
	}
	// There is no delete tool.
	for _, n := range toolNames(t, cs) {
		if strings.Contains(n, "delete") {
			t.Fatalf("tool %s", n)
		}
	}
	// Read-only token: no write tools, and calling one is refused.
	rs, _ := mkToken(t, f.aStep, []string{scRead}, f.hA)
	cr := f.connect(t, rs)
	mustOK(t, cr, "list_areas", nil)
	for n, args := range map[string]map[string]any{"create_area": {"name": "x"}, "rename_area": {"area": "Yard", "name": "x"},
		"update_chore": {"chore": "x", "name": "y"}, "archive_chore": {"chore": "x"}, "unarchive_chore": {"chore": "x"}} {
		mustFail(t, cr, n, args, scWrite)
	}
}

func TestMCPArchiveUnarchive(t *testing.T) {
	f := mcpTestEnv(t)
	secret, _ := mkToken(t, f.aStep, []string{scRead, scWrite}, f.hA)
	cs := f.connect(t, secret)
	id := f.mk(t, f.a, intervalBody("Dishes", "2026-11-21"))
	mustOK(t, cs, "archive_chore", map[string]any{"chore": "Dishes"})
	if out := mustOK(t, cs, "list_chores", nil); out["total"] != float64(0) {
		t.Fatalf("still listed %v", out)
	}
	_, v := f.a.do("GET", f.chore(id), nil)
	if field(v, "archivedAt") == nil {
		t.Fatalf("not archived: %v", v)
	}
	mustFail(t, cs, "update_chore", map[string]any{"chore": id, "name": "x"}, "archived")
	out := mustOK(t, cs, "unarchive_chore", map[string]any{"chore": id})["chore"].(map[string]any)
	if out["id"] != id || out["status"] == "archived" {
		t.Fatalf("unarchived %v", out)
	}
	if out := mustOK(t, cs, "list_chores", nil); out["total"] != float64(1) {
		t.Fatalf("not back %v", out)
	}
	mustFail(t, cs, "archive_chore", map[string]any{"chore": "ghost"}, "no chore")
}

func TestMCPManageRolesAndIsolation(t *testing.T) {
	f := mcpTestEnv(t)
	f.areasOn(t)
	cA := f.mk(t, f.a, intervalBody("A chore", "2026-11-21"))
	cB := mkIn(t, f.b, f.hB, intervalBody("B chore", "2026-11-21"))
	st, v := f.b.do("POST", "/api/v1/households/"+f.hB+"/areas", map[string]string{"name": "B area"})
	expect(t, st, v, 201, "")

	// A guest's token with every scope can read but not write.
	gs, _ := mkToken(t, f.gStep, []string{scRead, scWrite}, f.hA)
	cg := f.connect(t, gs)
	mustOK(t, cg, "list_areas", nil)
	mustFail(t, cg, "update_chore", map[string]any{"chore": cA, "name": "x"}, "guest")
	mustFail(t, cg, "archive_chore", map[string]any{"chore": cA}, "guest")
	mustFail(t, cg, "unarchive_chore", map[string]any{"chore": cA}, "guest")
	mustFail(t, cg, "create_area", map[string]any{"name": "x"}, "guest")
	mustFail(t, cg, "rename_area", map[string]any{"area": "x", "name": "y"}, "guest")

	// A's owner token cannot reach household B, by any spelling.
	secret, _ := mkToken(t, f.aStep, []string{scRead, scWrite}, f.hA)
	cs := f.connect(t, secret)
	for _, hh := range []string{f.hB, "B"} {
		mustFail(t, cs, "update_chore", map[string]any{"household": hh, "chore": cB, "name": "x"}, "available to this token")
		mustFail(t, cs, "archive_chore", map[string]any{"household": hh, "chore": cB}, "available to this token")
		mustFail(t, cs, "create_area", map[string]any{"household": hh, "name": "x"}, "available to this token")
		mustFail(t, cs, "list_areas", map[string]any{"household": hh}, "available to this token")
	}
	mustFail(t, cs, "update_chore", map[string]any{"chore": cB, "name": "x"}, "no chore")
	mustFail(t, cs, "archive_chore", map[string]any{"chore": cB}, "no chore")
	mustFail(t, cs, "update_chore", map[string]any{"chore": cA, "area": "B area"}, "no area")
	if b, _ := json.Marshal(mustOK(t, cs, "list_areas", nil)); strings.Contains(string(b), "B area") {
		t.Fatalf("leak %s", b)
	}
	_, v = f.b.do("GET", "/api/v1/households/"+f.hB+"/chores/"+cB, nil)
	if str(v, "name") != "B chore" || field(v, "archivedAt") != nil {
		t.Fatalf("B changed %v", v)
	}
}

// Areas over REST with a token: same role rules as a session.
func TestTokenAreasREST(t *testing.T) {
	f := newTokenEnv(t)
	hh := "/api/v1/households/" + f.hA
	rs, _ := mkToken(t, f.aStep, []string{scRead}, f.hA)
	ws, _ := mkToken(t, f.aStep, []string{scWrite}, f.hA)
	gws, _ := mkToken(t, f.gStep, []string{scRead, scWrite}, f.hA)
	mws, _ := mkToken(t, f.mStep, []string{scRead, scWrite}, f.hA)

	st, _, v := f.e.bearerDo("POST", hh+"/areas", rs, map[string]string{"name": "X"})
	expect(t, st, v, 403, "insufficient_scope")
	st, _, v = f.e.bearerDo("POST", hh+"/areas", gws, map[string]string{"name": "X"})
	expect(t, st, v, 403, "forbidden")
	st, _, v = f.e.bearerDo("POST", hh+"/areas", ws, map[string]string{"name": "Attic"})
	expect(t, st, v, 201, "")
	id := str(v, "id")
	st, _, v = f.e.bearerDo("PATCH", hh+"/areas/"+id, mws, map[string]string{"name": "Loft"})
	expect(t, st, v, 200, "")
	st, _, v = f.e.bearerDo("PATCH", hh+"/areas/"+id, gws, map[string]string{"name": "Nope"})
	expect(t, st, v, 403, "forbidden")
	st, _, v = f.e.bearerDo("GET", hh+"/areas", rs, nil)
	expect(t, st, v, 200, "")
	if l := list(t, v); len(l) != 1 || str(l[0], "name") != "Loft" {
		t.Fatalf("list %v", v)
	}
	// Another household: indistinguishable from missing.
	st, _, v = f.e.bearerDo("POST", "/api/v1/households/"+f.hB+"/areas", ws, map[string]string{"name": "X"})
	expect(t, st, v, 404, "")
	st, _, v = f.e.bearerDo("DELETE", hh+"/areas/"+id, rs, nil)
	expect(t, st, v, 403, "insufficient_scope")
	st, _, v = f.e.bearerDo("DELETE", hh+"/areas/"+id, ws, nil)
	if st != 204 {
		t.Fatalf("delete %d %v", st, v)
	}
}
