package api

import (
	"strings"
	"testing"
)

func TestAreasEnabledSetting(t *testing.T) {
	f := newHH(t)
	hh := "/api/v1/households/" + f.hA
	_, v := f.a.do("GET", hh, nil)
	if field(v, "areasEnabled") != false {
		t.Fatalf("default areasEnabled = %v", field(v, "areasEnabled"))
	}
	// Only owners may change it.
	for name, c := range map[string]*client{"member": f.m, "guest": f.g} {
		if st, v := c.do("PATCH", hh, map[string]any{"areasEnabled": true}); st != 403 {
			t.Fatalf("%s PATCH = %d %v", name, st, v)
		}
	}
	st, v := f.a.do("PATCH", hh, map[string]any{"areasEnabled": true})
	expect(t, st, v, 200, "")
	if field(v, "areasEnabled") != true {
		t.Fatalf("patched = %v", v)
	}
	// Other settings keep it; guests and members can read it.
	f.a.do("PATCH", hh, map[string]any{"name": "Renamed"})
	if _, v = f.g.do("GET", hh, nil); field(v, "areasEnabled") != true {
		t.Fatalf("guest sees %v", v)
	}
	// Starter areas are created once, only when there are none.
	for i := 0; i < 2; i++ {
		st, v = f.a.do("PATCH", hh, map[string]any{"starterAreas": true})
		expect(t, st, v, 200, "")
	}
	if _, v = f.a.do("GET", hh+"/areas", nil); len(list(t, v)) != 6 {
		t.Fatalf("starter areas: %v", v)
	}
	// Turning it off keeps the areas.
	f.a.do("PATCH", hh, map[string]any{"areasEnabled": false})
	if _, v = f.a.do("GET", hh+"/areas", nil); len(list(t, v)) != 6 {
		t.Fatalf("areas after off: %v", v)
	}
}

func TestCreateHouseholdStarterNeedsAreasEnabled(t *testing.T) {
	f := newHH(t)
	st, v := f.a.do("POST", "/api/v1/households", map[string]any{"name": "S", "timezone": "UTC", "starterAreas": true})
	expect(t, st, v, 201, "")
	if field(v, "areasEnabled") != false {
		t.Fatalf("areasEnabled = %v", v)
	}
	if _, l := f.a.do("GET", "/api/v1/households/"+str(v, "id")+"/areas", nil); len(list(t, l)) != 0 {
		t.Fatalf("areas = %v", l)
	}
}

// PUT without areaId keeps the chore's area; an explicit null clears it.
func TestChoreUpdateKeepsArea(t *testing.T) {
	f := newHH(t)
	_, a := f.a.do("POST", f.base()+"/areas", map[string]string{"name": "Kitchen"})
	body := intervalBody("Dishes", "2026-11-21")
	body["areaId"] = str(a, "id")
	id := f.mk(t, f.a, body)
	put := intervalBody("Dishes 2", "2026-11-21")
	st, v := f.a.do("PUT", f.chore(id), put)
	expect(t, st, v, 200, "")
	if str(v, "areaId") != str(a, "id") {
		t.Fatalf("area wiped: %v", v)
	}
	put["areaId"] = nil
	st, v = f.a.do("PUT", f.chore(id), put)
	expect(t, st, v, 200, "")
	if field(v, "areaId") != nil {
		t.Fatalf("area not cleared: %v", v)
	}
}

func TestExportImportAreasEnabled(t *testing.T) {
	f, doc := transferFixture(t)
	if field(field(doc, "household"), "areasEnabled") != false {
		t.Fatalf("export: %v", field(doc, "household"))
	}
	f.a.do("PATCH", f.base(), map[string]any{"areasEnabled": true})
	// A new household from a file with areas turns areas on.
	st, v := f.b.do("POST", "/api/v1/households/import", doc)
	expect(t, st, v, 201, "")
	_, h := f.b.do("GET", "/api/v1/households/"+str(field(v, "household"), "id"), nil)
	if field(h, "areasEnabled") != true {
		t.Fatalf("imported household: %v", h)
	}
	// Merging into a household with areas off keeps the data and the setting.
	st, v = f.b.do("POST", "/api/v1/households/"+f.hB+"/import", doc)
	expect(t, st, v, 200, "")
	_, h = f.b.do("GET", "/api/v1/households/"+f.hB, nil)
	if field(h, "areasEnabled") != false {
		t.Fatalf("merge changed the setting: %v", h)
	}
	if _, l := f.b.do("GET", "/api/v1/households/"+f.hB+"/areas", nil); len(list(t, l)) != 2 {
		t.Fatalf("merged areas: %v", l)
	}
}

func TestICalCategoriesFollowAreasSetting(t *testing.T) {
	f := newHH(t)
	_, a := f.a.do("POST", f.base()+"/areas", map[string]string{"name": "Kitchen"})
	body := intervalBody("Dishes", "2026-11-21")
	body["areaId"] = str(a, "id")
	f.mk(t, f.a, body)
	st, v := f.a.do("POST", f.base()+"/feeds", nil)
	expect(t, st, v, 201, "")
	path := field(v, "url").(string)
	if r := f.e.anon("GET", path, nil); r.status != 200 || strings.Contains(r.body, "CATEGORIES") {
		t.Fatalf("areas off: %d %q", r.status, r.body)
	}
	f.a.do("PATCH", f.base(), map[string]any{"areasEnabled": true})
	if r := f.e.anon("GET", path, nil); !strings.Contains(r.body, "CATEGORIES:Kitchen") {
		t.Fatalf("areas on: %q", r.body)
	}
}
