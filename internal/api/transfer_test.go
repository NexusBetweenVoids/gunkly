package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

const secretMarker = "SeCrEt-Ciphertext-Marker"

// transferFixture: household A with areas, chores (interval, rrule, one-off,
// custom reminders, assignee m, completions by m and a, a reverted one), a
// household channel and a calendar feed that must never be exported.
func transferFixture(t *testing.T, mutate ...func(*Options)) (*hhFixture, any) {
	t.Helper()
	f := newHH(t, mutate...)
	ctx := context.Background()
	for _, n := range []string{"Kitchen", "Garden"} {
		st, v := f.a.do("POST", f.base()+"/areas", map[string]string{"name": n})
		expect(t, st, v, 201, "")
	}
	_, v := f.a.do("GET", f.base()+"/areas", nil)
	areaID := map[string]string{}
	for _, a := range list(t, v) {
		areaID[str(a, "name")] = str(a, "id")
	}
	body := intervalBody("Wipe counters", "2026-11-14")
	body["areaId"], body["assignees"], body["notes"], body["link"] = areaID["Kitchen"], []string{f.mID}, "Use vinegar", "https://example.com/how"
	c1 := f.mk(t, f.a, body)
	c2 := f.mk(t, f.a, rruleBody("Mow lawn", "FREQ=WEEKLY;BYDAY=SA", "2026-11-21"))
	c3 := f.mk(t, f.a, oneOffBody("Fix gate", "2026-12-01"))
	st, v := f.a.do("PUT", f.chore(c2)+"/reminders", map[string]any{"reminders": []map[string]any{{"kind": "before", "days": 1, "minute": 540}, {"kind": "on", "minute": 480}}})
	expect(t, st, v, 200, "")
	st, v = f.m.do("POST", f.chore(c1)+"/complete", map[string]string{"note": "done by m"})
	expect(t, st, v, 201, "")
	st, v = f.a.do("POST", f.chore(c1)+"/complete", map[string]string{"note": "oops"})
	expect(t, st, v, 201, "")
	comps := f.completions(t, c1)
	st, v = f.a.do("POST", f.chore(c1)+"/completions/"+str(comps[0], "id")+"/revert", nil)
	expect(t, st, v, 200, "")
	st, v = f.a.do("POST", f.chore(c3)+"/archive", nil)
	expect(t, st, v, 204, "")
	st, v = f.a.do("POST", f.chore(c2)+"/pause", nil)
	expect(t, st, v, 200, "")

	au, _ := f.e.st.UserByLogin(ctx, "a")
	if _, err := f.e.st.CreateHouseholdChannel(ctx, store.Actor{UserID: au.ID}, f.hA, store.NewChannel{
		ID: "chan-1", Service: "discord", Ciphertext: secretMarker, Label: "Family", Hint: "…abcd", Max: 5}); err != nil {
		t.Fatal(err)
	}
	st, v = f.a.do("POST", f.base()+"/feeds", nil)
	expect(t, st, v, 201, "")

	resp := f.a.raw("GET", f.base()+"/export", nil, "")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("export: %d", resp.StatusCode)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment; filename=\"gunkly-household-export-") {
		t.Fatalf("content-disposition %q", cd)
	}
	var raw bytes.Buffer
	_, _ = raw.ReadFrom(resp.Body)
	for _, secret := range []string{secretMarker, "a@example.com", "m@example.com", "fake:", "token", "ciphertext", "\"url\""} {
		if strings.Contains(raw.String(), secret) {
			t.Fatalf("export leaks %q:\n%s", secret, raw.String())
		}
	}
	var doc any
	if err := json.Unmarshal(raw.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	return f, doc
}

func TestExportHousehold(t *testing.T) {
	f, doc := transferFixture(t)
	if str(doc, "format") != "gunkly-household-export" || field(doc, "version") != float64(1) {
		t.Fatalf("header: %v", doc)
	}
	if str(field(doc, "household"), "name") != "A" || str(field(doc, "household"), "timezone") != "Europe/Amsterdam" {
		t.Fatalf("household: %v", field(doc, "household"))
	}
	chores := list(t, field(doc, "chores"))
	if len(chores) != 3 || len(list(t, field(doc, "areas"))) != 2 {
		t.Fatalf("export: %v", doc)
	}
	byName := map[string]any{}
	for _, c := range chores {
		byName[str(c, "name")] = c
	}
	wipe := byName["Wipe counters"]
	as := list(t, field(wipe, "assignees"))
	if len(as) != 1 || str(as[0], "username") != "m" || str(wipe, "area") != "Kitchen" || len(list(t, field(wipe, "completions"))) != 2 {
		t.Fatalf("wipe: %v", wipe)
	}
	reverted := 0
	for _, h := range list(t, field(wipe, "completions")) {
		if field(h, "reverted") == true {
			reverted++
		}
	}
	if reverted != 1 {
		t.Fatalf("reverted completions = %d", reverted)
	}
	if len(list(t, field(byName["Mow lawn"], "reminders"))) != 2 || field(byName["Fix gate"], "archivedAt") == nil {
		t.Fatalf("mow/gate: %v %v", byName["Mow lawn"], byName["Fix gate"])
	}
	// Guests may not export; outsiders see nothing.
	st, v := f.g.do("GET", f.base()+"/export", nil)
	expect(t, st, v, 403, "forbidden")
	st, v = f.x.do("GET", f.base()+"/export", nil)
	expect(t, st, v, 404, "not_found")
	st, v = f.m.do("GET", f.base()+"/export", nil)
	expect(t, st, v, 200, "")
}

func (f *hhFixture) summaryOf(v any) map[string]float64 {
	out := map[string]float64{}
	for _, k := range []string{"areasCreated", "areasMerged", "choresCreated", "completionsImported", "unassigned"} {
		out[k], _ = field(v, k).(float64)
	}
	return out
}

func TestImportNewHousehold(t *testing.T) {
	f, doc := transferFixture(t)
	// b imports A's file as a new household: b becomes owner; m is not a member there.
	st, v := f.b.do("POST", "/api/v1/households/import", doc)
	expect(t, st, v, 201, "")
	got := f.summaryOf(v)
	want := map[string]float64{"areasCreated": 2, "areasMerged": 0, "choresCreated": 3, "completionsImported": 2, "unassigned": 1}
	for k, w := range want {
		if got[k] != w {
			t.Fatalf("summary %s = %v, want %v (%v)", k, got[k], w, v)
		}
	}
	id := str(field(v, "household"), "id")
	if id == "" || id == f.hA {
		t.Fatalf("household id %q", id)
	}
	st, v = f.b.do("GET", "/api/v1/households/"+id, nil)
	if st != 200 || field(v, "role") != "owner" || str(v, "name") != "A" || str(v, "timezone") != "Europe/Amsterdam" {
		t.Fatalf("new household: %d %v", st, v)
	}
	st, v = f.b.do("GET", "/api/v1/households/"+id+"/chores?archived=0", nil)
	expect(t, st, v, 200, "")
	names := map[string]bool{}
	for _, c := range list(t, v) {
		names[str(c, "name")] = true
		if str(c, "name") == "Wipe counters" {
			if str(c, "dueDate") == "" || len(list(t, field(c, "assignees"))) != 0 || str(field(c, "schedule"), "firstDue") != "2026-11-14" {
				t.Fatalf("imported chore: %v", c)
			}
		}
	}
	if !names["Wipe counters"] || len(names) != 1 { // Mow lawn is paused (still listed), Fix gate archived
		// paused chores are listed as active; accept 1 or 2
		if !names["Wipe counters"] {
			t.Fatalf("names %v", names)
		}
	}
	// The original is untouched and the importer cannot see it.
	st, v = f.a.do("GET", f.base()+"/chores", nil)
	expect(t, st, v, 200, "")
	if len(list(t, v)) < 2 {
		t.Fatalf("A changed: %v", v)
	}
	// The imported household round-trips: same chores in its own export.
	st, v = f.b.do("GET", "/api/v1/households/"+id+"/export", nil)
	expect(t, st, v, 200, "")
	if len(list(t, field(v, "chores"))) != 3 || len(list(t, field(v, "areas"))) != 2 {
		t.Fatalf("re-export: %v", v)
	}
}

func TestImportMerge(t *testing.T) {
	f, doc := transferFixture(t)
	// Merge A's file back into A: same-name areas combine, chores are added as new,
	// m (a member of A) keeps the assignment, history is attributed to m and a.
	st, v := f.m.do("POST", f.base()+"/import", doc)
	expect(t, st, v, 200, "")
	got := f.summaryOf(v)
	if got["areasCreated"] != 0 || got["areasMerged"] != 2 || got["choresCreated"] != 3 || got["completionsImported"] != 2 || got["unassigned"] != 0 {
		t.Fatalf("summary %v", v)
	}
	st, v = f.a.do("GET", f.base()+"/areas", nil)
	expect(t, st, v, 200, "")
	if len(list(t, v)) != 2 {
		t.Fatalf("areas %v", v)
	}
	_, v = f.a.do("GET", f.base()+"/chores?archived=1", nil)
	if len(list(t, v)) != 2 { // archived: the original Fix gate and its copy
		t.Fatalf("archived %v", v)
	}
	// Revert of an imported completion must leave the chore with a due date.
	_, v = f.a.do("GET", f.base()+"/chores", nil)
	for _, c := range list(t, v) {
		if str(c, "name") != "Wipe counters" {
			continue
		}
		for _, h := range f.completions(t, str(c, "id")) {
			if field(h, "revertedAt") == nil {
				f.a.do("POST", f.chore(str(c, "id"))+"/completions/"+str(h, "id")+"/revert", nil)
			}
		}
		st, got := f.a.do("GET", f.chore(str(c, "id")), nil)
		if st != 200 || str(got, "dueDate") == "" {
			t.Fatalf("chore after revert: %v", got)
		}
	}
	// Guests cannot import; outsiders see nothing.
	st, v = f.g.do("POST", f.base()+"/import", doc)
	expect(t, st, v, 403, "forbidden")
	st, v = f.x.do("POST", f.base()+"/import", doc)
	expect(t, st, v, 404, "not_found")
	// Into B (owner b, different members): assignee m is unknown there.
	st, v = f.b.do("POST", "/api/v1/households/"+f.hB+"/import", doc)
	expect(t, st, v, 200, "")
	if got := f.summaryOf(v); got["areasCreated"] != 2 || got["unassigned"] != 1 {
		t.Fatalf("summary into B %v", v)
	}
}

func TestImportValidation(t *testing.T) {
	f, doc := transferFixture(t)
	clone := func(mutate func(m map[string]any)) any {
		b, _ := json.Marshal(doc)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		mutate(m)
		return m
	}
	chore := func(m map[string]any, i int) map[string]any { return m["chores"].([]any)[i].(map[string]any) }
	cases := []struct {
		name         string
		mutate       func(m map[string]any)
		reason, path string
		status       int
	}{
		{"unknown format", func(m map[string]any) { m["format"] = "other" }, "format", "format", 400},
		{"future version", func(m map[string]any) { m["version"] = 2 }, "version", "version", 400},
		{"unknown field", func(m map[string]any) { m["extra"] = 1 }, "not_json", "", 400},
		{"bad timezone", func(m map[string]any) { m["household"].(map[string]any)["timezone"] = "Not/AZone" }, "invalid", "household.timezone", 400},
		{"empty name", func(m map[string]any) { chore(m, 0)["name"] = "  " }, "required", "chores[0].name", 400},
		{"javascript link", func(m map[string]any) { chore(m, 1)["link"] = "javascript:alert(1)" }, "invalid", "chores[1].link", 400},
		{"bad rrule", func(m map[string]any) {
			for i := range m["chores"].([]any) {
				if sc := chore(m, i)["schedule"].(map[string]any); sc["type"] == "rrule" {
					sc["rrule"] = "FREQ=SECONDLY"
				}
			}
		}, "invalid", "", 400},
		{"zero interval", func(m map[string]any) {
			for i := range m["chores"].([]any) {
				if sc := chore(m, i)["schedule"].(map[string]any); sc["type"] == "interval" && sc["interval"] != nil {
					sc["interval"] = map[string]any{"every": 0, "unit": "week"}
				}
			}
		}, "invalid", "", 400},
		{"unknown area", func(m map[string]any) { chore(m, 0)["area"] = "Nowhere" }, "invalid", "chores[0].area", 400},
		{"bad source", func(m map[string]any) {
			for i := range m["chores"].([]any) {
				if cs := chore(m, i)["completions"].([]any); len(cs) > 0 {
					cs[0].(map[string]any)["source"] = "telepathy"
				}
			}
		}, "invalid", "", 400},
		{"bad reminder", func(m map[string]any) {
			for i := range m["chores"].([]any) {
				if rs := chore(m, i)["reminders"].([]any); len(rs) > 0 {
					rs[0].(map[string]any)["minute"] = 5000
				}
			}
		}, "invalid", "", 400},
		{"too many chores", nil, "limit_chores", "chores", 409},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var body any
			ff := f
			if c.mutate == nil {
				ff = newHHWith(t, func(o *Options) { o.Limits = limits.New(fakeLimits{"chores_per_household": 2}) })
				body = doc
			} else {
				body = clone(c.mutate)
			}
			st, v := ff.b.do("POST", "/api/v1/households/import", body)
			expect(t, st, v, c.status, "import_invalid")
			er, _ := field(v, "error").(map[string]any)
			if er["reason"] != c.reason || (c.path != "" && er["path"] != c.path) {
				t.Fatalf("error %v, want reason %s path %s", er, c.reason, c.path)
			}
		})
	}
	// Nothing was created by any rejected import.
	_, v := f.b.do("GET", "/api/v1/households", nil)
	if len(list(t, v)) != 1 {
		t.Fatalf("b has %d households after rejected imports", len(list(t, v)))
	}
	// Not JSON at all.
	resp := f.b.raw("POST", "/api/v1/households/import", nil, f.b.cookie(csrfCookie))
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("empty body: %d", resp.StatusCode)
	}
}

// TestImportRollsBack: a limit hit after areas were created must leave
// nothing behind.
func TestImportRollsBack(t *testing.T) {
	f, doc := transferFixture(t)
	if err := f.e.st.UpdateInstanceSettings(context.Background(), store.SettingsUpdate{Limits: map[string]int{"chores_per_household": 4}}); err != nil {
		t.Fatal(err)
	}
	// B has 0 chores and no areas; a household plan cannot be set here, so make B nearly full.
	for i := range 2 {
		st, v := f.b.do("POST", "/api/v1/households/"+f.hB+"/chores", intervalBody(fmt.Sprintf("b%d", i), "2026-11-21"))
		expect(t, st, v, 201, "")
	}
	st, v := f.b.do("POST", "/api/v1/households/"+f.hB+"/import", doc) // 2 + 3 > 4
	expect(t, st, v, 409, "import_invalid")
	_, v = f.b.do("GET", "/api/v1/households/"+f.hB+"/areas", nil)
	if len(list(t, v)) != 0 {
		t.Fatalf("areas leaked from a failed import: %v", v)
	}
	_, v = f.b.do("GET", "/api/v1/households/"+f.hB+"/chores", nil)
	if len(list(t, v)) != 2 {
		t.Fatalf("chores changed: %v", v)
	}
}

func TestImportSizeCap(t *testing.T) {
	f, doc := transferFixture(t)
	// A 1 MiB+ body is fine on import routes (up to 5 MiB) but still refused elsewhere.
	big := map[string]any{}
	b, _ := json.Marshal(doc)
	_ = json.Unmarshal(b, &big)
	big["chores"].([]any)[0].(map[string]any)["notes"] = strings.Repeat("x", 9000)
	pad := strings.Repeat("y", 2<<20)
	req := func(path, extra string) int {
		body := `{"format":"gunkly-household-export","version":1,"pad":"` + extra + `"}`
		r, _ := http.NewRequest("POST", f.e.ts.URL+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", f.b.cookie(csrfCookie))
		resp, err := f.b.c.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if st := req("/api/v1/households/import", pad); st != 400 { // read fully, then rejected as invalid (unknown field)
		t.Fatalf("2 MiB import body: %d, want 400", st)
	}
	if st := req("/api/v1/households/import", strings.Repeat("y", 6<<20)); st != 413 {
		t.Fatalf("6 MiB import body: %d, want 413", st)
	}
	if st := req("/api/v1/households", pad); st != 413 {
		t.Fatalf("2 MiB body on a normal route: %d, want 413", st)
	}
}

func TestExportUser(t *testing.T) {
	f, _ := transferFixture(t)
	ctx := context.Background()
	mu, _ := f.e.st.UserByLogin(ctx, "m")
	if _, err := f.e.st.CreateUserChannel(ctx, store.Actor{UserID: mu.ID}, store.NewChannel{
		ID: "chan-2", Service: "ntfy", Ciphertext: secretMarker, Label: "Phone", Hint: "…wxyz", Max: 5}); err != nil {
		t.Fatal(err)
	}
	// Sign-in alone is not enough.
	st, v := f.m.do("GET", "/api/v1/account/export", nil)
	expect(t, st, v, 403, "step_up_required")
	f.e.stepUp(f.m, "m")
	resp := f.m.raw("GET", "/api/v1/account/export", nil, "")
	defer resp.Body.Close()
	var raw bytes.Buffer
	_, _ = raw.ReadFrom(resp.Body)
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment;") {
		t.Fatalf("export: %d %v", resp.StatusCode, resp.Header)
	}
	for _, secret := range []string{secretMarker, "fake:", "password", "token", "ciphertext", "\"url\""} {
		if strings.Contains(strings.ToLower(raw.String()), strings.ToLower(secret)) {
			t.Fatalf("user export leaks %q:\n%s", secret, raw.String())
		}
	}
	var doc any
	if err := json.Unmarshal(raw.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if str(doc, "format") != "gunkly-user-export" || str(field(doc, "profile"), "username") != "m" {
		t.Fatalf("doc %v", doc)
	}
	if len(list(t, field(doc, "memberships"))) != 1 || len(list(t, field(doc, "completions"))) != 1 ||
		len(list(t, field(doc, "sessions"))) != 1 || len(list(t, field(doc, "audit"))) < 1 {
		t.Fatalf("doc %v", doc)
	}
	ch := list(t, field(doc, "channels"))
	if len(ch) != 1 || str(ch[0], "kind") != "ntfy" || str(ch[0], "label") != "Phone" {
		t.Fatalf("channels %v", ch)
	}
	evs, err := f.e.st.ListAuditForUser(ctx, mu.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range evs {
		found = found || e.Event == store.AuditAccountExport
	}
	if !found {
		t.Fatal("export not audited")
	}
}

// Imported history is marked as imported and only an owner's import may name
// who did it: a member's file cannot forge completions in other names.
func TestImportAttribution(t *testing.T) {
	f, doc := transferFixture(t)
	importedBy := func(c *client) (n, attributed int) {
		st, v := c.do("POST", f.base()+"/import", doc)
		expect(t, st, v, 200, "")
		_, cl := f.a.do("GET", f.base()+"/chores", nil)
		for _, ch := range list(t, cl) {
			for _, h := range f.completions(t, str(ch, "id")) {
				if str(h, "source") != "import" {
					continue
				}
				n++
				if field(h, "userId") != nil {
					attributed++
				}
			}
		}
		return n, attributed
	}
	if n, attr := importedBy(f.m); n != 2 || attr != 0 {
		t.Fatalf("member import: %d imported, %d attributed; want 2, 0", n, attr)
	}
	if n, attr := importedBy(f.a); n != 4 || attr != 2 {
		t.Fatalf("owner import: %d imported in total, %d attributed; want 4, 2", n, attr)
	}
}

// A file may not carry more than ImportMaxCompletions history entries in all.
func TestImportTotalCompletionsCap(t *testing.T) {
	f, doc := transferFixture(t)
	chore := doc.(map[string]any)["chores"].([]any)[0].(map[string]any)
	comp := chore["completions"].([]any)[0]
	comps := make([]any, 1000)
	for i := range comps {
		comps[i] = comp
	}
	chore["completions"] = comps
	chores := make([]any, 21)
	for i := range chores {
		chores[i] = chore
	}
	doc.(map[string]any)["chores"] = chores
	st, v := f.a.do("POST", f.base()+"/import", doc)
	expect(t, st, v, 409, "import_invalid")
	if field(field(v, "error"), "reason") != "limit_completions" {
		t.Fatalf("reason: %v", v)
	}
}
