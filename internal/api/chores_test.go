package api

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/schedule"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

func str(v any, k string) string { s, _ := field(v, k).(string); return s }

func list(t *testing.T, v any) []any {
	t.Helper()
	l, ok := v.([]any)
	if !ok {
		t.Fatalf("not a list: %v", v)
	}
	return l
}

func (f *hhFixture) base() string { return "/api/v1/households/" + f.hA }

// intervalBody is a weekly chore due on the given local date.
func intervalBody(name, due string) map[string]any {
	return map[string]any{"name": name, "schedule": map[string]any{
		"type": "interval", "interval": map[string]any{"every": 1, "unit": "week"}, "firstDue": due}}
}

func rruleBody(name, rule, due string) map[string]any {
	return map[string]any{"name": name, "schedule": map[string]any{"type": "rrule", "rrule": rule, "firstDue": due}}
}

func oneOffBody(name, due string) map[string]any {
	return map[string]any{"name": name, "kind": "oneoff", "schedule": map[string]any{"firstDue": due}}
}

// mk creates a chore as client c in household A and returns its ID.
func (f *hhFixture) mk(t *testing.T, c *client, body any) string {
	t.Helper()
	st, v := c.do("POST", f.base()+"/chores", body)
	expect(t, st, v, 201, "")
	return str(v, "id")
}

func (f *hhFixture) chore(id string) string { return f.base() + "/chores/" + id }

func TestAreas(t *testing.T) {
	f := newHH(t)
	st, v := f.a.do("POST", f.base()+"/areas", map[string]string{"name": "Kitchen"})
	expect(t, st, v, 201, "")
	id := str(v, "id")
	st, v = f.m.do("POST", f.base()+"/areas", map[string]string{"name": "kitchen"})
	expect(t, st, v, 409, "conflict")
	st, v = f.a.do("POST", f.base()+"/areas", map[string]string{"name": "  "})
	expect(t, st, v, 400, "invalid")
	st, v = f.g.do("POST", f.base()+"/areas", map[string]string{"name": "Nope"})
	expect(t, st, v, 403, "forbidden")
	st, v = f.m.do("PATCH", f.base()+"/areas/"+id, map[string]string{"name": "Galley"})
	if st != 200 || str(v, "name") != "Galley" {
		t.Fatalf("rename: %d %v", st, v)
	}
	st, v = f.g.do("GET", f.base()+"/areas", nil)
	if l := list(t, v); st != 200 || len(l) != 1 {
		t.Fatalf("list: %d %v", st, v)
	}
	st, v = f.a.do("PATCH", f.base()+"/areas/nope", map[string]string{"name": "x"})
	expect(t, st, v, 404, "not_found")
	// Deleting an area leaves its chores area-less.
	cid := f.mk(t, f.a, map[string]any{"name": "Dishes", "areaId": id, "schedule": intervalBody("", "2026-11-21")["schedule"]})
	st, v = f.a.do("DELETE", f.base()+"/areas/"+id, nil)
	expect(t, st, v, 204, "")
	_, v = f.a.do("GET", f.chore(cid), nil)
	if field(v, "areaId") != nil {
		t.Fatalf("areaId = %v", field(v, "areaId"))
	}
}

func TestStarterAreas(t *testing.T) {
	e := newEnv(t)
	e.user("u", false)
	c := e.signedIn("u")
	st, v := c.do("POST", "/api/v1/households", map[string]any{"name": "Plain", "timezone": "UTC"})
	expect(t, st, v, 201, "")
	_, v = c.do("GET", "/api/v1/households/"+str(v, "id")+"/areas", nil)
	if len(list(t, v)) != 0 {
		t.Fatalf("areas without the flag: %v", v)
	}
	st, v = c.do("POST", "/api/v1/households", map[string]any{"name": "Starter", "timezone": "UTC", "areasEnabled": true, "starterAreas": true})
	expect(t, st, v, 201, "")
	_, v = c.do("GET", "/api/v1/households/"+str(v, "id")+"/areas", nil)
	var names []string
	for _, a := range list(t, v) {
		names = append(names, str(a, "name"))
	}
	if got := strings.Join(names, ","); got != "Appliances,Bathroom,Exterior,Garden,General,Kitchen" {
		t.Fatalf("starter areas = %s", got)
	}
}

func TestChoreCreateValidation(t *testing.T) {
	f := newHH(t)
	_, other := f.b.do("POST", "/api/v1/households/"+f.hB+"/areas", map[string]string{"name": "Shed"})
	good := func() map[string]any { return intervalBody("Vacuum", "2026-11-21") }
	with := func(k string, v any) map[string]any { b := good(); b[k] = v; return b }
	sched := func(k string, v any) map[string]any {
		b := good()
		s := b["schedule"].(map[string]any)
		s[k] = v
		return b
	}
	bad := map[string]any{
		"empty name":      with("name", " "),
		"long name":       with("name", strings.Repeat("x", 201)),
		"long notes":      with("notes", strings.Repeat("x", 10001)),
		"js link":         with("link", "javascript:alert(1)"),
		"ftp link":        with("link", "ftp://example.com/x"),
		"hostless link":   with("link", "https://"),
		"bad kind":        with("kind", "weekly"),
		"bad preset":      with("reminderPreset", "shouty"),
		"custom preset":   with("reminderPreset", "custom"),
		"guest assignee":  with("assignees", []string{f.gID}),
		"stranger":        with("assignees", []string{"nobody"}),
		"dup assignee":    with("assignees", []string{f.mID, f.mID}),
		"foreign area":    with("areaId", str(other, "id")),
		"unknown area":    with("areaId", "nope"),
		"zero interval":   sched("interval", map[string]any{"every": 0, "unit": "week"}),
		"huge interval":   sched("interval", map[string]any{"every": 1001, "unit": "day"}),
		"bad unit":        sched("interval", map[string]any{"every": 1, "unit": "year"}),
		"no interval":     sched("interval", nil),
		"bad type":        sched("type", "cron"),
		"bad date":        sched("firstDue", "2026-02-30"),
		"no date":         sched("firstDue", ""),
		"ancient date":    sched("firstDue", "1900-01-01"),
		"bad anchor":      sched("anchor", "whenever"),
		"rrule+interval":  sched("rrule", "FREQ=DAILY"),
		"garbage rrule":   rruleBody("R", "EVERY DAY", "2026-11-21"),
		"count rrule":     rruleBody("R", "FREQ=DAILY;COUNT=3", "2026-11-21"),
		"lowercase rrule": rruleBody("R", "freq=daily", "2026-11-21"),
		"impossible":      rruleBody("R", "FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=30", "2026-11-21"),
		"unknown field":   with("colour", "red"),
	}
	for name, body := range bad {
		st, v := f.a.do("POST", f.base()+"/chores", body)
		if st != 400 {
			t.Errorf("%s: %d %v", name, st, v)
		}
	}
	st, v := f.a.do("POST", f.base()+"/chores", "not an object")
	expect(t, st, v, 400, "invalid_json")
	_, v = f.a.do("GET", f.base()+"/chores", nil)
	if len(list(t, v)) != 0 {
		t.Fatalf("invalid creates left chores: %v", v)
	}
}

func TestChoreLifecycle(t *testing.T) {
	f := newHH(t)
	_, ar := f.a.do("POST", f.base()+"/areas", map[string]string{"name": "Garden"})
	body := rruleBody("Mow", "FREQ=MONTHLY;BYDAY=1SA", "2026-11-21") // Nov's first Saturday has passed
	body["areaId"], body["assignees"], body["link"] = str(ar, "id"), []string{f.mID}, "https://example.com/mower"
	st, v := f.a.do("POST", f.base()+"/chores", body)
	expect(t, st, v, 201, "")
	id := str(v, "id")
	if str(v, "dueDate") != "2026-12-05" || str(v, "status") != "later" || str(v, "kind") != "recurring" ||
		str(field(v, "schedule"), "anchor") != "from_scheduled" || field(v, "nextDue") == nil {
		t.Fatalf("created: %v", v)
	}
	// Member and owner see the same; guests read.
	for _, c := range []*client{f.m, f.g} {
		st, v = c.do("GET", f.chore(id), nil)
		if st != 200 || str(v, "name") != "Mow" {
			t.Fatalf("get: %d %v", st, v)
		}
	}
	st, v = f.a.do("GET", f.chore("nope"), nil)
	expect(t, st, v, 404, "not_found")

	// Update: same endpoint for every editor. Unchanged first due keeps the occurrence.
	upd := rruleBody("Mow lawn", "FREQ=MONTHLY;BYDAY=1SA", "2026-11-21")
	upd["areaId"] = nil
	st, v = f.m.do("PUT", f.chore(id), upd)
	if st != 200 || str(v, "name") != "Mow lawn" || str(v, "dueDate") != "2026-12-05" || field(v, "areaId") != nil {
		t.Fatalf("update: %d %v", st, v)
	}
	st, v = f.g.do("PUT", f.chore(id), upd)
	expect(t, st, v, 403, "forbidden")
	// A new first due date moves the occurrence; one-off and interval kinds too.
	st, v = f.a.do("PUT", f.chore(id), intervalBody("Mow lawn", "2026-11-22"))
	if st != 200 || str(v, "dueDate") != "2026-11-22" || str(v, "status") != "thisWeek" || field(field(v, "schedule"), "interval") == nil {
		t.Fatalf("update 2: %d %v", st, v)
	}
	st, v = f.a.do("PUT", f.chore(id), oneOffBody("Mow lawn", "2026-11-21"))
	if st != 200 || str(v, "kind") != "oneoff" || str(v, "status") != "today" || field(field(v, "schedule"), "interval") != nil {
		t.Fatalf("update 3: %d %v", st, v)
	}
	st, v = f.a.do("PUT", f.chore(id), with(upd, "name", ""))
	expect(t, st, v, 400, "invalid")

	// Archive hides from the default list; archived=1 lists only archived.
	st, v = f.m.do("POST", f.chore(id)+"/archive", nil)
	expect(t, st, v, 204, "")
	_, v = f.a.do("GET", f.base()+"/chores", nil)
	if len(list(t, v)) != 0 {
		t.Fatalf("archived listed: %v", v)
	}
	_, v = f.a.do("GET", f.base()+"/chores?archived=1", nil)
	if l := list(t, v); len(l) != 1 || str(l[0], "status") != "archived" {
		t.Fatalf("archived list: %v", v)
	}
	st, v = f.m.do("POST", f.chore(id)+"/unarchive", nil)
	if st != 200 || field(v, "archivedAt") != nil {
		t.Fatalf("unarchive: %d %v", st, v)
	}

	// Delete is audited and removes the chore.
	st, v = f.g.do("DELETE", f.chore(id), nil)
	expect(t, st, v, 403, "forbidden")
	st, v = f.m.do("DELETE", f.chore(id), nil)
	expect(t, st, v, 204, "")
	st, v = f.a.do("GET", f.chore(id), nil)
	expect(t, st, v, 404, "not_found")
	if !f.audited(t, "m", store.AuditChoreDelete) {
		t.Fatal("chore delete not audited")
	}
}

func with(m map[string]any, k string, v any) map[string]any {
	out := map[string]any{}
	for a, b := range m {
		out[a] = b
	}
	out[k] = v
	return out
}

func (f *hhFixture) audited(t *testing.T, user, event string) bool {
	t.Helper()
	u, err := f.e.st.UserByLogin(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	evs, err := f.e.st.ListAuditForUser(context.Background(), u.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Event == event && e.HouseholdID == f.hA {
			return true
		}
	}
	return false
}

func TestChoreFilters(t *testing.T) {
	f := newHH(t)
	_, ar := f.a.do("POST", f.base()+"/areas", map[string]string{"name": "Garden"})
	mine := with(intervalBody("Mine", "2026-11-21"), "assignees", []string{f.mID})
	mine["areaId"] = str(ar, "id")
	f.mk(t, f.a, mine)
	f.mk(t, f.a, with(intervalBody("Hers", "2026-11-21"), "assignees", []string{f.userID("a")}))
	f.mk(t, f.a, intervalBody("Anyone", "2026-11-21"))
	names := func(c *client, q string) string {
		st, v := c.do("GET", f.base()+"/chores"+q, nil)
		expect(t, st, v, 200, "")
		var ns []string
		for _, ch := range list(t, v) {
			ns = append(ns, str(ch, "name"))
		}
		return strings.Join(sorted(ns), ",")
	}
	if got := names(f.m, ""); got != "Anyone,Hers,Mine" {
		t.Fatalf("all = %s", got)
	}
	if got := names(f.m, "?mine=1"); got != "Anyone,Mine" {
		t.Fatalf("mine (m) = %s", got)
	}
	if got := names(f.a, "?mine=1"); got != "Anyone,Hers" {
		t.Fatalf("mine (a) = %s", got)
	}
	if got := names(f.a, "?area="+str(ar, "id")); got != "Mine" {
		t.Fatalf("area = %s", got)
	}
}

func sorted(s []string) []string {
	for i := range s {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
	return s
}

func (f *hhFixture) userID(name string) string {
	u, err := f.e.st.UserByLogin(context.Background(), name)
	if err != nil {
		panic(err)
	}
	return u.ID
}

func TestDueStatusBuckets(t *testing.T) {
	today := schedule.Date{Year: 2026, Month: time.November, Day: 21} // a Saturday
	for _, c := range []struct {
		due       string
		weekStart int
		want      string
	}{
		{"2026-11-20", 1, statusOverdue},
		{"2026-11-21", 1, statusToday},
		{"2026-11-22", 1, statusThisWeek}, // Sunday closes a Monday-start week
		{"2026-11-23", 1, statusLater},
		{"2026-11-22", 0, statusLater}, // Saturday closes a Sunday-start week
		{"2026-11-21", 0, statusToday},
		{"2026-11-27", 6, statusThisWeek}, // Saturday-start week runs to Friday
		{"2026-11-28", 6, statusLater},
		{"2026-11-25", 4, statusThisWeek}, // Thursday-start: Thu 19..Wed 25
		{"2026-11-26", 4, statusLater},
	} {
		ch := store.Chore{DueDate: c.due, NextDue: time.Unix(1, 0)}
		got := dueStatus(ch, time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC), today, c.weekStart)
		if got != c.want {
			t.Errorf("due %s weekStart %d: %s, want %s", c.due, c.weekStart, got, c.want)
		}
	}
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	for name, ch := range map[string]store.Chore{
		"archived":     {DueDate: "2026-11-21", NextDue: now, ArchivedAt: now},
		"paused":       {DueDate: "2026-11-21", NextDue: now, PausedIndefinitely: true},
		"paused until": {DueDate: "2026-11-21", NextDue: now, PausedUntil: now.Add(time.Hour)},
	} {
		want := map[string]string{"archived": statusArchived, "paused": statusPaused, "paused until": statusPaused}[name]
		if got := dueStatus(ch, now, today, 1); got != want {
			t.Errorf("%s: %s, want %s", name, got, want)
		}
	}
	if got := dueStatus(store.Chore{DueDate: "2026-11-21", PausedUntil: now.Add(-time.Hour), NextDue: now}, now, today, 1); got != statusToday {
		t.Errorf("expired pause: %s", got)
	}
	if got := dueStatus(store.Chore{DueDate: "2026-11-21"}, now, today, 1); got != statusNone {
		t.Errorf("no due: %s", got)
	}
}

// The household's zone decides what "today" is; the user's week start decides
// where "this week" ends.
func TestDueStatusTimezoneAndWeekStart(t *testing.T) {
	f := newHH(t)
	// 2026-11-21 12:00 UTC is Sat 13:00 in Amsterdam and Sun 01:00 in Auckland.
	st, v := f.a.do("POST", "/api/v1/households", map[string]any{"name": "Kiwi", "timezone": "Pacific/Auckland"})
	expect(t, st, v, 201, "")
	hk := str(v, "id")
	mkIn := func(hh, due string) string {
		st, v := f.a.do("POST", "/api/v1/households/"+hh+"/chores", intervalBody("C", due))
		expect(t, st, v, 201, "")
		return str(v, "status")
	}
	if got := mkIn(f.hA, "2026-11-21"); got != "today" {
		t.Errorf("Amsterdam: %s", got)
	}
	if got := mkIn(hk, "2026-11-21"); got != "overdue" {
		t.Errorf("Auckland: %s", got)
	}
	if got := mkIn(hk, "2026-11-22"); got != "today" {
		t.Errorf("Auckland next day: %s", got)
	}
	// Auckland Sunday: Monday-start week ends Sunday (today), Sunday-start ends Saturday.
	if got := mkIn(hk, "2026-11-23"); got != "later" {
		t.Errorf("Auckland Mon-start: %s", got)
	}
	st, v = f.a.do("PATCH", "/api/v1/me", map[string]any{"weekStart": 0})
	expect(t, st, v, 200, "")
	if got := mkIn(hk, "2026-11-23"); got != "thisWeek" {
		t.Errorf("Auckland Sun-start: %s", got)
	}
}

// TestDueStatusPersonalZone: a user's own time zone decides "today" in their
// session's chore views; API tokens keep the household's zone.
func TestDueStatusPersonalZone(t *testing.T) {
	f := newTokenEnv(t)
	// 2026-11-21 12:00 UTC: Saturday 13:00 in Amsterdam (the household), Sunday 01:00 in Auckland.
	id := f.mk(t, f.a, intervalBody("C", "2026-11-21"))
	secret, _ := mkToken(t, f.aStep, []string{"chores:read"}, f.hA)
	status := func() (session, token string) {
		st, v := f.a.do("GET", f.chore(id), nil)
		expect(t, st, v, 200, "")
		st2, _, v2 := f.e.bearerDo("GET", f.chore(id), secret, nil)
		if st2 != 200 {
			t.Fatalf("token: %d %v", st2, v2)
		}
		return str(v, "status"), str(v2, "status")
	}
	if s, tk := status(); s != "today" || tk != "today" {
		t.Fatalf("household zone: session %s, token %s", s, tk)
	}
	st, v := f.a.do("PATCH", "/api/v1/me", map[string]any{"timeZone": "Pacific/Auckland"})
	expect(t, st, v, 200, "")
	if s, tk := status(); s != "overdue" || tk != "today" {
		t.Errorf("personal zone: session %s (want overdue), token %s (want today)", s, tk)
	}
	st, v = f.a.do("PATCH", "/api/v1/me", map[string]any{"timeZone": nil})
	expect(t, st, v, 200, "")
	if s, _ := status(); s != "today" {
		t.Errorf("back to the household zone: %s", s)
	}
}

func TestChoreLimits(t *testing.T) {
	f := newHHWith(t, func(o *Options) {
		o.Limits = limits.New(fakeLimits{"chores_per_household": 2, "reminders_per_chore": 2})
	})
	f.mk(t, f.a, intervalBody("1", "2026-11-21"))
	id := f.mk(t, f.m, intervalBody("2", "2026-11-21"))
	st, v := f.a.do("POST", f.base()+"/chores", intervalBody("3", "2026-11-21"))
	expect(t, st, v, 409, "limit_reached")
	// Archived chores still count.
	f.a.do("POST", f.chore(id)+"/archive", nil)
	st, v = f.a.do("POST", f.base()+"/chores", intervalBody("3", "2026-11-21"))
	expect(t, st, v, 409, "limit_reached")
	// Reminders per chore.
	rem := func(n int) any {
		var rs []map[string]any
		for range n {
			rs = append(rs, map[string]any{"kind": "on", "minute": 540})
		}
		return map[string]any{"reminders": rs}
	}
	st, v = f.a.do("PUT", f.chore(id)+"/reminders", rem(2))
	expect(t, st, v, 200, "")
	st, v = f.a.do("PUT", f.chore(id)+"/reminders", rem(3))
	expect(t, st, v, 409, "limit_reached")
}

type fakeLimits map[string]int

func (l fakeLimits) LimitOverrides(context.Context, string) (map[string]int, map[string]int, error) {
	return l, nil, nil
}

func newHHWith(t *testing.T, mutate func(*Options)) *hhFixture {
	t.Helper()
	return newHH(t, mutate)
}

func TestCompleteHistoryRevert(t *testing.T) {
	f := newHH(t)
	id := f.mk(t, f.a, intervalBody("Vacuum", "2026-11-14")) // overdue, weekly from completion
	st, v := f.g.do("POST", f.chore(id)+"/complete", nil)
	expect(t, st, v, 403, "forbidden")
	st, v = f.m.do("POST", f.chore(id)+"/complete", map[string]string{"note": "done early"})
	expect(t, st, v, 201, "")
	comp, ch := field(v, "completion"), field(v, "chore")
	if str(comp, "source") != "app" || str(comp, "note") != "done early" || str(comp, "userId") != f.mID {
		t.Fatalf("completion: %v", comp)
	}
	if str(ch, "dueDate") != "2026-11-28" || str(ch, "status") != "later" {
		t.Fatalf("chore after completion: %v", ch)
	}
	undo := str(v, "undoPath")
	if !strings.HasSuffix(undo, "/completions/"+str(comp, "id")+"/revert") {
		t.Fatalf("undoPath = %s", undo)
	}
	// A second completion with an empty body.
	f.e.clock.Add(time.Minute)
	st, v = f.a.do("POST", f.chore(id)+"/complete", nil)
	expect(t, st, v, 201, "")
	second := str(field(v, "completion"), "id")
	st, v = f.a.do("POST", f.chore(id)+"/complete", map[string]string{"note": strings.Repeat("n", 2001)})
	expect(t, st, v, 400, "invalid")

	// History: newest first, paginated.
	st, v = f.g.do("GET", f.chore(id)+"/completions?limit=1", nil)
	if l := list(t, field(v, "items")); st != 200 || len(l) != 1 || str(l[0], "id") != second || str(v, "next") == "" {
		t.Fatalf("page 1: %d %v", st, v)
	}
	st, v = f.g.do("GET", f.chore(id)+"/completions?limit=1&after="+str(v, "next"), nil)
	if l := list(t, field(v, "items")); st != 200 || len(l) != 1 || str(l[0], "id") != str(comp, "id") || str(v, "next") != "" {
		t.Fatalf("page 2: %d %v", st, v)
	}
	for _, q := range []string{"?limit=0", "?limit=201", "?limit=x", "?after=garbage"} {
		if st, v = f.a.do("GET", f.chore(id)+"/completions"+q, nil); st != 400 {
			t.Errorf("%s: %d %v", q, st, v)
		}
	}

	// Revert the latest: due date goes back to what the first completion produced.
	st, v = f.g.do("POST", f.chore(id)+"/completions/"+second+"/revert", nil)
	expect(t, st, v, 403, "forbidden")
	st, v = f.a.do("POST", f.chore(id)+"/completions/"+second+"/revert", nil)
	if st != 200 || str(v, "dueDate") != "2026-11-28" {
		t.Fatalf("revert: %d %v", st, v)
	}
	st, v = f.a.do("POST", f.chore(id)+"/completions/"+second+"/revert", nil)
	expect(t, st, v, 409, "conflict")
	// Undo through the affordance returned by the first completion: back to the original due date.
	st, v = f.m.do("POST", undo, nil)
	if st != 200 || str(v, "dueDate") != "2026-11-14" || str(v, "status") != "overdue" {
		t.Fatalf("undo: %d %v", st, v)
	}
	_, v = f.a.do("GET", f.chore(id)+"/completions", nil)
	for _, it := range list(t, field(v, "items")) {
		if field(it, "revertedAt") == nil {
			t.Fatalf("not marked reverted: %v", it)
		}
	}
	if !f.audited(t, "a", store.AuditCompletionRevert) || !f.audited(t, "m", store.AuditCompletionRevert) {
		t.Fatal("revert not audited")
	}
	// A completion of another chore cannot be reverted through this one.
	other := f.mk(t, f.a, intervalBody("Other", "2026-11-21"))
	st, v = f.a.do("POST", f.chore(other)+"/completions/"+second+"/revert", nil)
	expect(t, st, v, 404, "not_found")
}

func TestOneOffArchivesOnCompletion(t *testing.T) {
	f := newHH(t)
	id := f.mk(t, f.a, oneOffBody("Fix gate", "2026-11-21"))
	st, v := f.m.do("POST", f.chore(id)+"/complete", nil)
	expect(t, st, v, 201, "")
	ch := field(v, "chore")
	if str(ch, "status") != "archived" || field(ch, "archivedAt") == nil {
		t.Fatalf("one-off after completion: %v", ch)
	}
	_, v = f.a.do("GET", f.base()+"/chores", nil)
	if len(list(t, v)) != 0 {
		t.Fatalf("listed: %v", v)
	}
	st, v = f.a.do("POST", f.chore(id)+"/complete", nil)
	expect(t, st, v, 409, "conflict")
	items := f.completions(t, id)
	st, v = f.a.do("POST", f.chore(id)+"/completions/"+str(items[0], "id")+"/revert", nil)
	if st != 200 || field(v, "archivedAt") != nil || str(v, "status") != "today" {
		t.Fatalf("revert one-off: %d %v", st, v)
	}
}

func (f *hhFixture) completions(t *testing.T, id string) []any {
	t.Helper()
	st, v := f.a.do("GET", f.chore(id)+"/completions", nil)
	expect(t, st, v, 200, "")
	return list(t, field(v, "items"))
}

// markDone issues a mark-done link for the chore's current occurrence.
func (f *hhFixture) markDone(t *testing.T, id string) string {
	t.Helper()
	ctx := context.Background()
	ch, err := f.e.st.Chore(ctx, store.Actor{UserID: f.userID("a")}, f.hA, id)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := f.e.st.CreateMarkDone(ctx, store.MarkDoneLink{ChoreID: id, OccurrenceDue: ch.NextDue, UserID: f.mID,
		ExpiresAt: f.e.clock.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func (f *hhFixture) linkValid(tok string) bool {
	_, err := f.e.st.PeekMarkDone(context.Background(), tok)
	return err == nil
}

func TestSnoozeSkipPause(t *testing.T) {
	f := newHH(t)
	id := f.mk(t, f.a, rruleBody("Bins", "FREQ=WEEKLY;BYDAY=SA", "2026-11-21"))

	// Snooze.
	tok := f.markDone(t, id)
	if !f.linkValid(tok) {
		t.Fatal("fresh link invalid")
	}
	for name, body := range map[string]any{"past": map[string]string{"until": "2026-11-20"}, "garbage": map[string]string{"until": "tomorrow"}, "empty": map[string]string{}} {
		if st, v := f.a.do("POST", f.chore(id)+"/snooze", body); st != 400 {
			t.Errorf("snooze %s: %d %v", name, st, v)
		}
	}
	if !f.linkValid(tok) {
		t.Fatal("rejected snooze invalidated the link")
	}
	st, v := f.g.do("POST", f.chore(id)+"/snooze", map[string]string{"until": "2026-11-24"})
	expect(t, st, v, 403, "forbidden")
	st, v = f.m.do("POST", f.chore(id)+"/snooze", map[string]string{"until": "2026-11-24"})
	if st != 200 || str(v, "dueDate") != "2026-11-24" || str(v, "status") != "later" {
		t.Fatalf("snooze: %d %v", st, v)
	}
	if f.linkValid(tok) {
		t.Fatal("snooze left the mark-done link valid")
	}

	// Skip: next occurrence after the snoozed date, no completion, audited.
	tok = f.markDone(t, id)
	st, v = f.m.do("POST", f.chore(id)+"/skip", nil)
	if st != 200 || str(v, "dueDate") != "2026-11-28" {
		t.Fatalf("skip: %d %v", st, v)
	}
	if f.linkValid(tok) {
		t.Fatal("skip left the mark-done link valid")
	}
	if n := len(f.completions(t, id)); n != 0 {
		t.Fatalf("skip recorded %d completions", n)
	}
	if !f.audited(t, "m", store.AuditChoreSkip) {
		t.Fatal("skip not audited")
	}
	one := f.mk(t, f.a, oneOffBody("Once", "2026-11-21"))
	st, v = f.a.do("POST", f.chore(one)+"/skip", nil)
	expect(t, st, v, 400, "invalid")

	// Pause until a date, then resume; pause indefinitely.
	tok = f.markDone(t, id)
	for name, body := range map[string]any{"today": map[string]string{"until": "2026-11-21"}, "bad": map[string]string{"until": "x"}} {
		if st, v := f.a.do("POST", f.chore(id)+"/pause", body); st != 400 {
			t.Errorf("pause %s: %d %v", name, st, v)
		}
	}
	st, v = f.m.do("POST", f.chore(id)+"/pause", map[string]string{"until": "2026-12-01"})
	if st != 200 || str(v, "status") != "paused" || field(v, "pausedUntil") == nil || field(v, "pausedIndefinitely") != false {
		t.Fatalf("pause: %d %v", st, v)
	}
	if f.linkValid(tok) {
		t.Fatal("pause left the mark-done link valid")
	}
	st, v = f.m.do("POST", f.chore(id)+"/resume", nil)
	if st != 200 || str(v, "status") != "later" || field(v, "pausedUntil") != nil {
		t.Fatalf("resume: %d %v", st, v)
	}
	if !f.linkValid(f.markDone(t, id)) {
		t.Fatal("link after resume invalid")
	}
	st, v = f.m.do("POST", f.chore(id)+"/pause", nil)
	if st != 200 || field(v, "pausedIndefinitely") != true || str(v, "status") != "paused" {
		t.Fatalf("pause indefinitely: %d %v", st, v)
	}
	// Paused or archived chores reject state changes appropriately; archived ones are conflicts.
	f.a.do("POST", f.chore(id)+"/archive", nil)
	st, v = f.a.do("POST", f.chore(id)+"/snooze", map[string]string{"until": "2026-12-01"})
	expect(t, st, v, 409, "conflict")
	st, v = f.a.do("POST", f.chore(id)+"/pause", nil)
	expect(t, st, v, 409, "conflict")
}

func TestCompleteArchiveRevertInvalidateLinks(t *testing.T) {
	f := newHH(t)
	id := f.mk(t, f.a, intervalBody("Gutters", "2026-11-21"))
	tok := f.markDone(t, id)
	st, v := f.a.do("POST", f.chore(id)+"/complete", nil)
	expect(t, st, v, 201, "")
	if f.linkValid(tok) {
		t.Fatal("completion left the link valid")
	}
	tok = f.markDone(t, id)
	f.a.do("POST", f.chore(id)+"/completions/"+str(field(v, "completion"), "id")+"/revert", nil)
	if f.linkValid(tok) {
		t.Fatal("revert left the link valid")
	}
	// A pure edit of the name keeps the occurrence, so links stay valid.
	tok = f.markDone(t, id)
	st, v = f.a.do("GET", f.chore(id), nil)
	expect(t, st, v, 200, "")
	f.a.do("PUT", f.chore(id), intervalBody("Gutters!", str(field(v, "schedule"), "firstDue")))
	if !f.linkValid(tok) {
		t.Fatal("cosmetic edit invalidated the link")
	}
}

func TestReminders(t *testing.T) {
	f := newHH(t)
	id := f.mk(t, f.a, with(intervalBody("Dishes", "2026-11-21"), "reminderPreset", "nag"))
	rem := f.chore(id) + "/reminders"
	st, v := f.g.do("GET", rem, nil)
	if st != 200 || str(v, "preset") != "nag" || len(list(t, field(v, "reminders"))) != 3 {
		t.Fatalf("nag: %d %v", st, v)
	}
	// A new household defaults to "gentle"; it applies to chores without a preset.
	id2 := f.mk(t, f.a, intervalBody("Floors", "2026-11-21"))
	st, v = f.a.do("GET", f.chore(id2)+"/reminders", nil)
	if st != 200 || str(v, "effectivePreset") != "gentle" || len(list(t, field(v, "reminders"))) != 1 {
		t.Fatalf("new household default: %d %v", st, v)
	}
	st, v = f.a.do("PATCH", f.base(), map[string]string{"defaultReminderPreset": "none"})
	if st != 200 {
		t.Fatalf("none: %d %v", st, v)
	}
	_, v = f.a.do("GET", f.chore(id2)+"/reminders", nil)
	if str(v, "effectivePreset") != "none" || len(list(t, field(v, "reminders"))) != 0 {
		t.Fatalf("none default: %v", v)
	}
	st, v = f.m.do("PATCH", f.base(), map[string]string{"defaultReminderPreset": "gentle"})
	expect(t, st, v, 403, "forbidden")
	st, v = f.a.do("PATCH", f.base(), map[string]string{"defaultReminderPreset": "bogus"})
	expect(t, st, v, 400, "invalid")
	st, v = f.a.do("PATCH", f.base(), map[string]string{"defaultReminderPreset": "gentle"})
	if st != 200 || str(v, "defaultReminderPreset") != "gentle" {
		t.Fatalf("household preset: %d %v", st, v)
	}
	_, v = f.a.do("GET", f.chore(id2)+"/reminders", nil)
	if str(v, "effectivePreset") != "gentle" || len(list(t, field(v, "reminders"))) != 1 {
		t.Fatalf("effective gentle: %v", v)
	}
	st, v = f.g.do("GET", f.base()+"/reminder-presets", nil)
	if p := list(t, field(v, "presets")); st != 200 || len(p) != 3 || str(v, "default") != "gentle" || str(p[0], "name") != "gentle" {
		t.Fatalf("presets: %d %v", st, v)
	}

	// Custom set replaces the previous one; update keeps the custom preset.
	custom := map[string]any{"reminders": []map[string]any{
		{"kind": "before", "days": 3, "minute": 480}, {"kind": "on", "minute": 540}, {"kind": "overdue", "days": 2, "minute": 600}}}
	st, v = f.m.do("PUT", rem, custom)
	if st != 200 || str(v, "preset") != "custom" || len(list(t, field(v, "reminders"))) != 3 {
		t.Fatalf("custom: %d %v", st, v)
	}
	_, v = f.a.do("PUT", f.chore(id), intervalBody("Dishes", "2026-11-21"))
	if str(v, "reminderPreset") != "custom" {
		t.Fatalf("update dropped custom preset: %v", v)
	}
	_, v = f.a.do("GET", rem, nil)
	if r := list(t, field(v, "reminders")); len(r) != 3 || str(r[2], "kind") != "overdue" || field(r[2], "days") != float64(2) {
		t.Fatalf("custom stored: %v", v)
	}
	st, v = f.a.do("PUT", rem, map[string]any{"preset": "none"})
	if st != 200 || str(v, "preset") != "none" || len(list(t, field(v, "reminders"))) != 0 {
		t.Fatalf("none: %d %v", st, v)
	}
	st, v = f.a.do("PUT", rem, map[string]any{})
	if st != 200 || str(v, "preset") != "" {
		t.Fatalf("reset: %d %v", st, v)
	}

	bad := map[string]any{
		"unknown kind":   map[string]any{"reminders": []map[string]any{{"kind": "sometimes", "minute": 1}}},
		"overdue daily0": map[string]any{"reminders": []map[string]any{{"kind": "overdue", "days": 0, "minute": 1}}},
		"before 0":       map[string]any{"reminders": []map[string]any{{"kind": "before", "days": 0, "minute": 1}}},
		"before 366":     map[string]any{"reminders": []map[string]any{{"kind": "before", "days": 366, "minute": 1}}},
		"minute 1440":    map[string]any{"reminders": []map[string]any{{"kind": "on", "minute": 1440}}},
		"negative":       map[string]any{"reminders": []map[string]any{{"kind": "on", "minute": -1}}},
		"unknown preset": map[string]any{"preset": "loud"},
		"preset+list":    map[string]any{"preset": "nag", "reminders": []map[string]any{}},
	}
	for name, body := range bad {
		if st, v := f.a.do("PUT", rem, body); st != 400 {
			t.Errorf("%s: %d %v", name, st, v)
		}
	}
	st, v = f.g.do("PUT", rem, custom)
	expect(t, st, v, 403, "forbidden")
	// Up to the limit (10) is fine, one more is not.
	var many []map[string]any
	for range 11 {
		many = append(many, map[string]any{"kind": "on", "minute": 60})
	}
	st, v = f.a.do("PUT", rem, map[string]any{"reminders": many[:10]})
	expect(t, st, v, 200, "")
	st, v = f.a.do("PUT", rem, map[string]any{"reminders": many})
	expect(t, st, v, 409, "limit_reached")
	st, v = f.a.do("GET", f.chore("nope")+"/reminders", nil)
	expect(t, st, v, 404, "not_found")
}

func TestMute(t *testing.T) {
	f := newHH(t)
	id := f.mk(t, f.a, intervalBody("Dishes", "2026-11-21"))
	st, v := f.g.do("PUT", f.chore(id)+"/mute", nil)
	expect(t, st, v, 403, "forbidden")
	st, v = f.m.do("PUT", f.chore(id)+"/mute", nil)
	expect(t, st, v, 204, "")
	st, v = f.m.do("PUT", f.chore(id)+"/mute", nil) // idempotent
	expect(t, st, v, 204, "")
	_, v = f.m.do("GET", f.chore(id), nil)
	if field(v, "muted") != true {
		t.Fatalf("m: %v", v)
	}
	_, v = f.a.do("GET", f.chore(id), nil)
	if field(v, "muted") != false {
		t.Fatalf("mutes are personal: %v", v)
	}
	st, v = f.m.do("DELETE", f.chore(id)+"/mute", nil)
	expect(t, st, v, 204, "")
	_, v = f.m.do("GET", f.chore(id), nil)
	if field(v, "muted") != false {
		t.Fatalf("unmute: %v", v)
	}
	st, v = f.m.do("PUT", f.chore("nope")+"/mute", nil)
	expect(t, st, v, 404, "not_found")
}

func TestTemplates(t *testing.T) {
	f := newHH(t)
	st, v := f.x.do("GET", "/api/v1/templates", nil)
	if l := list(t, v); st != 200 || len(l) < 10 {
		t.Fatalf("templates: %d %v", st, v)
	}
	areas := map[string]bool{}
	for _, k := range starterAreaKeys {
		areas[k] = true
	}
	seen := map[string]bool{}
	for _, tp := range choreTemplates {
		if tp.Key == "" || seen[tp.Key] || !areas[tp.Area] || tp.Interval.Every < 1 || tp.Interval.Every > schedule.MaxInterval {
			t.Errorf("bad template %+v", tp)
		}
		seen[tp.Key] = true
		if !slicesContains([]string{"day", "week", "month"}, tp.Interval.Unit) {
			t.Errorf("template %s unit %s", tp.Key, tp.Interval.Unit)
		}
	}
	anon := f.e.client()
	st, v = anon.do("GET", "/api/v1/templates", nil)
	expect(t, st, v, 401, "unauthenticated")
}

func slicesContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestCalendar(t *testing.T) {
	f := newHH(t)
	f.mk(t, f.a, rruleBody("Bins", "FREQ=WEEKLY;BYDAY=SA", "2026-11-21"))
	f.mk(t, f.a, intervalBody("Vacuum", "2026-11-24")) // from completion: next due only
	f.mk(t, f.a, oneOffBody("Once", "2026-12-02"))
	arch := f.mk(t, f.a, oneOffBody("Gone", "2026-12-02"))
	f.a.do("POST", f.chore(arch)+"/archive", nil)
	f.mk(t, f.a, with(intervalBody("Sched", "2026-11-23"), "schedule", map[string]any{
		"type": "interval", "interval": map[string]any{"every": 1, "unit": "week"}, "anchor": "from_scheduled", "firstDue": "2026-11-23"}))

	st, v := f.g.do("GET", f.base()+"/calendar?from=2026-11-21&to=2026-12-12", nil)
	expect(t, st, v, 200, "")
	got := map[string]string{}
	for _, it := range list(t, field(v, "items")) {
		var ds []string
		for _, d := range list(t, field(it, "dates")) {
			ds = append(ds, d.(string))
		}
		got[str(it, "name")] = strings.Join(ds, ",")
	}
	want := map[string]string{
		"Bins":   "2026-11-21,2026-11-28,2026-12-05,2026-12-12",
		"Vacuum": "2026-11-24",
		"Once":   "2026-12-02",
		"Sched":  "2026-11-23,2026-11-30,2026-12-07",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("calendar:\n got %v\nwant %v", got, want)
	}
	st, v = f.a.do("GET", f.base()+"/calendar?from=2026-11-29&to=2026-11-29", nil)
	if l := list(t, field(v, "items")); st != 200 || len(l) != 0 {
		t.Fatalf("single empty day: %d %v", st, v)
	}
	// Range checks: 62 days inclusive is the maximum.
	for _, c := range []struct {
		q  string
		ok bool
	}{
		{"from=2026-11-01&to=2026-12-31", true},  // 61 days
		{"from=2026-11-01&to=2027-01-01", true},  // 62 days inclusive
		{"from=2026-11-01&to=2027-01-02", false}, // 63
		{"from=2026-11-10&to=2026-11-09", false},
		{"from=2026-11-10", false},
		{"from=x&to=y", false},
	} {
		st, v := f.a.do("GET", f.base()+"/calendar?"+c.q, nil)
		if (st == 200) != c.ok {
			t.Errorf("%s: %d %v", c.q, st, v)
		}
	}
}

// Every household route refuses another household's IDs, even for an owner of
// a household that really exists: B's owner names household B with A's IDs.
func TestChoreRoutesRejectForeignIDs(t *testing.T) {
	f := newHH(t)
	_, ar := f.a.do("POST", f.base()+"/areas", map[string]string{"name": "Garden"})
	area := str(ar, "id")
	chore := f.mk(t, f.a, intervalBody("Secret", "2026-11-21"))
	st, v := f.a.do("POST", f.chore(chore)+"/complete", nil)
	expect(t, st, v, 201, "")
	comp := str(field(v, "completion"), "id")
	f.a.do("PUT", f.chore(chore)+"/mute", nil)
	bodies := map[string]any{
		"PATCH /areas/{area}":           map[string]string{"name": "Pwned"},
		"PUT /chores/{chore}":           intervalBody("Pwned", "2026-11-21"),
		"PATCH /chores/{chore}":         map[string]string{"turnUserId": f.hB},
		"POST /chores/{chore}/snooze":   map[string]string{"until": "2026-12-01"},
		"POST /chores/{chore}/pause":    map[string]string{"until": "2026-12-01"},
		"PUT /chores/{chore}/reminders": map[string]any{"preset": "nag"},
	}
	n := 0
	for _, r := range f.e.srv.routes {
		if r.policy.auth != authHousehold || !strings.Contains(r.pattern, "{chore}") && !strings.Contains(r.pattern, "{area}") {
			continue
		}
		n++
		method, p, _ := strings.Cut(r.pattern, " ")
		p = strings.NewReplacer("{hh}", f.hB, "{chore}", chore, "{area}", area, "{completion}", comp).Replace(p)
		body := bodies[method+" "+strings.TrimPrefix(r.pattern[len(method)+1:], "/api/v1/households/{hh}")]
		if st, v := f.b.do(method, p, body); st != 404 {
			t.Errorf("%s with household A's IDs from B: %d %v", r.pattern, st, v)
		}
	}
	if n < 15 {
		t.Fatalf("only %d id routes", n)
	}
	// Nothing of A changed.
	_, v = f.a.do("GET", f.chore(chore), nil)
	if str(v, "name") != "Secret" || field(v, "muted") != true {
		t.Fatalf("chore A changed: %v", v)
	}
	if len(list(t, func() any { _, v := f.a.do("GET", f.base()+"/areas", nil); return v }())) != 1 {
		t.Fatal("areas of A changed")
	}
}

func minutes(t *testing.T, v any) []float64 {
	t.Helper()
	var out []float64
	for _, r := range list(t, field(v, "reminders")) {
		out = append(out, field(r, "minute").(float64))
	}
	return out
}

func TestChoreReminderTime(t *testing.T) {
	f := newHH(t)
	at := func(body map[string]any, hm string) map[string]any { return with(body, "reminderTime", hm) }

	// Preset + time: the preset's reminders, all at the chosen time.
	id := f.mk(t, f.a, at(with(intervalBody("Dishes", "2026-11-21"), "reminderPreset", "nag"), "07:30"))
	st, v := f.g.do("GET", f.chore(id)+"/reminders", nil)
	if st != 200 || str(v, "preset") != "custom" || len(list(t, field(v, "reminders"))) != 3 {
		t.Fatalf("nag at 07:30: %d %v", st, v)
	}
	for _, m := range minutes(t, v) {
		if m != 450 {
			t.Fatalf("minutes %v", minutes(t, v))
		}
	}
	// Editing other fields (no reminderTime) keeps rows and time.
	st, v = f.a.do("PUT", f.chore(id), intervalBody("Dishes 2", "2026-11-21"))
	if st != 200 || str(v, "reminderPreset") != "custom" {
		t.Fatalf("rename: %d %v", st, v)
	}
	_, v = f.a.do("GET", f.chore(id)+"/reminders", nil)
	if got := minutes(t, v); len(got) != 3 || got[0] != 450 {
		t.Fatalf("rename reset the time: %v", got)
	}
	// Changing the time later re-times the custom rows, keeping their shape.
	st, v = f.a.do("PUT", f.chore(id), at(intervalBody("Dishes 2", "2026-11-21"), "18:05"))
	if st != 200 {
		t.Fatalf("retime: %d %v", st, v)
	}
	_, v = f.a.do("GET", f.chore(id)+"/reminders", nil)
	r := list(t, field(v, "reminders"))
	if len(r) != 3 || str(r[0], "kind") != "before" || minutes(t, v)[2] != 1085 {
		t.Fatalf("retime: %v", v)
	}
	// Custom rows with different times are untouched unless a time is sent.
	custom := map[string]any{"reminders": []map[string]any{{"kind": "before", "days": 3, "minute": 480}, {"kind": "on", "minute": 540}}}
	st, v = f.a.do("PUT", f.chore(id)+"/reminders", custom)
	expect(t, st, v, 200, "")
	f.a.do("PUT", f.chore(id), intervalBody("Dishes 3", "2026-11-21"))
	_, v = f.a.do("GET", f.chore(id)+"/reminders", nil)
	if got := minutes(t, v); len(got) != 2 || got[0] != 480 || got[1] != 540 {
		t.Fatalf("custom touched: %v", got)
	}
	f.a.do("PUT", f.chore(id), at(intervalBody("Dishes 3", "2026-11-21"), "06:00"))
	_, v = f.a.do("GET", f.chore(id)+"/reminders", nil)
	if got := minutes(t, v); len(got) != 2 || got[0] != 360 || got[1] != 360 {
		t.Fatalf("custom retime: %v", got)
	}

	// Household default (gentle) + time; preset none ignores the time.
	id2 := f.mk(t, f.a, at(intervalBody("Floors", "2026-11-21"), "21:15"))
	_, v = f.a.do("GET", f.chore(id2)+"/reminders", nil)
	if got := minutes(t, v); len(got) != 1 || got[0] != 1275 || str(v, "preset") != "custom" {
		t.Fatalf("default + time: %v", v)
	}
	id3 := f.mk(t, f.a, at(with(intervalBody("Bins", "2026-11-21"), "reminderPreset", "none"), "07:30"))
	_, v = f.a.do("GET", f.chore(id3)+"/reminders", nil)
	if str(v, "preset") != "none" || len(list(t, field(v, "reminders"))) != 0 {
		t.Fatalf("none + time: %v", v)
	}
	// No time: today's behaviour, preset kept.
	id4 := f.mk(t, f.a, with(intervalBody("Windows", "2026-11-21"), "reminderPreset", "nag"))
	_, v = f.a.do("GET", f.chore(id4)+"/reminders", nil)
	if str(v, "preset") != "nag" || len(list(t, field(v, "reminders"))) != 3 {
		t.Fatalf("no time: %v", v)
	}

	// Validation and permissions.
	for _, bad := range []string{"24:00", "7:30", "07:60", "0730", "07:30:00", "noon", " 07:30"} {
		st, v = f.a.do("POST", f.base()+"/chores", at(intervalBody("Bad", "2026-11-21"), bad))
		expect(t, st, v, 400, "invalid")
	}
	for _, ok := range []string{"00:00", "23:59", ""} {
		st, v = f.a.do("POST", f.base()+"/chores", at(intervalBody("Ok "+ok, "2026-11-21"), ok))
		expect(t, st, v, 201, "")
	}
	st, v = f.g.do("PUT", f.chore(id), at(intervalBody("Dishes", "2026-11-21"), "07:30"))
	if st != 403 {
		t.Fatalf("guest: %d %v", st, v)
	}
}

// Editing an existing chore with a reminder time, in every state the chore can be in:
// stored preset or household default (no rows yet), preset none, and a household
// whose default is none.
func TestChoreReminderTimeOnEdit(t *testing.T) {
	f := newHH(t)
	body := func(name string, extra ...any) map[string]any {
		b := intervalBody(name, "2026-11-21")
		for i := 0; i < len(extra); i += 2 {
			b[extra[i].(string)] = extra[i+1]
		}
		return b
	}
	times := func(id string) []float64 {
		_, v := f.a.do("GET", f.chore(id)+"/reminders", nil)
		return minutes(t, v)
	}
	put := func(id string, b map[string]any) {
		t.Helper()
		if st, v := f.a.do("PUT", f.chore(id), b); st != 200 {
			t.Fatalf("PUT: %d %v", st, v)
		}
	}
	all := func(got []float64, n int, want float64) {
		t.Helper()
		if len(got) != n {
			t.Fatalf("rows %v, want %d", got, n)
		}
		for _, m := range got {
			if m != want {
				t.Fatalf("minutes %v, want all %v", got, want)
			}
		}
	}

	// Household default (gentle), no explicit rows; the editor sends the preset as stored ("").
	def := f.mk(t, f.a, body("Default"))
	put(def, body("Default", "reminderPreset", "", "reminderTime", "07:15"))
	all(times(def), 1, 435)
	// Same with the preset omitted.
	def2 := f.mk(t, f.a, body("Default 2"))
	put(def2, body("Default 2", "reminderTime", "07:15"))
	all(times(def2), 1, 435)

	// Stored presets.
	for preset, n := range map[string]int{"gentle": 1, "nag": 3} {
		id := f.mk(t, f.a, body(preset, "reminderPreset", preset))
		put(id, body(preset, "reminderPreset", preset, "reminderTime", "20:45"))
		all(times(id), n, 1245)
		_, v := f.a.do("GET", f.chore(id), nil)
		if str(v, "reminderPreset") != "custom" {
			t.Fatalf("%s: %v", preset, v)
		}
	}

	// Preset none: the time is ignored; choosing a preset in the same request applies it.
	none := f.mk(t, f.a, body("None", "reminderPreset", "none"))
	put(none, body("None", "reminderPreset", "none", "reminderTime", "07:15"))
	all(times(none), 0, 0)
	put(none, body("None", "reminderPreset", "nag", "reminderTime", "11:30"))
	all(times(none), 3, 690)

	// Household default none: the chore has no reminders until a preset is chosen.
	if st, v := f.a.do("PATCH", f.base(), map[string]string{"defaultReminderPreset": "none"}); st != 200 {
		t.Fatalf("PATCH: %d %v", st, v)
	}
	dn := f.mk(t, f.a, body("Default none"))
	put(dn, body("Default none", "reminderPreset", "", "reminderTime", "07:15"))
	all(times(dn), 0, 0)
	put(dn, body("Default none", "reminderPreset", "gentle", "reminderTime", "12:00"))
	all(times(dn), 1, 720)

	// Custom rows at different times are moved together.
	mixed := f.mk(t, f.a, body("Mixed"))
	custom := map[string]any{"reminders": []map[string]any{{"kind": "before", "days": 3, "minute": 480}, {"kind": "on", "minute": 600}}}
	if st, v := f.a.do("PUT", f.chore(mixed)+"/reminders", custom); st != 200 {
		t.Fatalf("custom: %d %v", st, v)
	}
	put(mixed, body("Mixed", "reminderTime", "08:00"))
	all(times(mixed), 2, 480)
}

// Chores carry reminderTime: when the due-day reminder goes off, from the
// chore's own reminders or its (default) preset; null without one.
func TestChoreViewReminderTime(t *testing.T) {
	f := newHH(t)
	rt := func(id string) any {
		t.Helper()
		st, v := f.g.do("GET", f.chore(id), nil)
		if st != 200 {
			t.Fatalf("get: %d %v", st, v)
		}
		return field(v, "reminderTime")
	}
	timed := f.mk(t, f.a, with(with(intervalBody("Dishes", "2026-11-21"), "reminderPreset", "nag"), "reminderTime", "07:30"))
	byDefault := f.mk(t, f.a, intervalBody("Floors", "2026-11-21")) // household default: gentle, 09:00
	none := f.mk(t, f.a, with(intervalBody("Bins", "2026-11-21"), "reminderPreset", "none"))
	before := f.mk(t, f.a, intervalBody("Windows", "2026-11-21"))
	st, v := f.a.do("PUT", f.chore(before)+"/reminders", map[string]any{"reminders": []map[string]any{{"kind": "before", "days": 1, "minute": 480}}})
	expect(t, st, v, 200, "")
	for id, want := range map[string]any{timed: "07:30", byDefault: "09:00", none: nil, before: nil} {
		if got := rt(id); got != want {
			t.Errorf("chore %s: reminderTime %v, want %v", id, got, want)
		}
	}
	// The list says the same, and follows the household default.
	st, v = f.a.do("PATCH", f.base(), map[string]any{"defaultReminderPreset": "none"})
	expect(t, st, v, 200, "")
	_, v = f.g.do("GET", f.base()+"/chores", nil)
	for _, c := range list(t, v) {
		want := map[string]any{timed: "07:30", byDefault: nil, none: nil, before: nil}[str(c, "id")]
		if got := field(c, "reminderTime"); got != want {
			t.Errorf("list %s: reminderTime %v, want %v", str(c, "name"), got, want)
		}
	}
}
