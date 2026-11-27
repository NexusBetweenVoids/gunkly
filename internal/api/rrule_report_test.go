package api

import "testing"

func TestRRuleReportRepeatChangeMovesDue(t *testing.T) {
	f := mcpTestEnv(t)
	secret, _ := mkToken(t, f.aStep, []string{scRead, scWrite}, f.hA)
	cs := f.connect(t, secret)
	id := f.mk(t, f.a, rruleBody("Bins", "FREQ=WEEKLY;BYDAY=SA", "2027-10-09"))
	mustOK(t, cs, "update_chore", map[string]any{"chore": id, "repeat": "FREQ=WEEKLY;BYDAY=MO"})
	_, v := f.a.do("GET", f.chore(id), nil)
	if str(v, "dueDate") != "2027-10-11" {
		t.Fatalf("MCP: due %v", str(v, "dueDate"))
	}
	// REST PUT with the unchanged firstDue behaves the same.
	b := rruleBody("Bins", "FREQ=WEEKLY;BYDAY=WE", "2027-10-09")
	st, v := f.a.do("PUT", f.chore(id), b)
	expect(t, st, v, 200, "")
	if str(v, "dueDate") != "2027-10-13" {
		t.Fatalf("REST: due %v", str(v, "dueDate"))
	}
}

func TestRRuleReportImpossibleRules(t *testing.T) {
	f := mcpTestEnv(t)
	secret, _ := mkToken(t, f.aStep, []string{scRead, scWrite}, f.hA)
	cs := f.connect(t, secret)
	id := f.mk(t, f.a, rruleBody("Bins", "FREQ=WEEKLY;BYDAY=SA", "2027-10-09"))
	for _, rule := range []string{"FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=30", "FREQ=YEARLY;BYMONTH=4;BYMONTHDAY=31",
		"FREQ=MONTHLY;BYMONTHDAY=1,2;BYDAY=SA;BYSETPOS=3"} {
		st, v := f.a.do("POST", f.base()+"/chores", rruleBody("X", rule, "2027-10-09"))
		if st != 400 {
			t.Errorf("create %s: %d %v", rule, st, v)
		}
		st, v = f.a.do("PUT", f.chore(id), rruleBody("Bins", rule, "2027-10-09"))
		if st != 400 {
			t.Errorf("REST update %s: %d %v", rule, st, v)
		}
		if out, isErr := callTool(t, cs, "update_chore", map[string]any{"chore": id, "repeat": rule}); !isErr {
			t.Errorf("MCP update %s accepted: %v", rule, out)
		}
	}
}

func TestRRuleReportWhitespace(t *testing.T) {
	f := mcpTestEnv(t)
	for _, rule := range []string{"FREQ=WEEKLY; BYDAY=MO", "FREQ=WEEKLY;BYDAY=MO;", " FREQ=WEEKLY;;BYDAY=MO "} {
		st, v := f.a.do("POST", f.base()+"/chores", rruleBody("X", rule, "2027-10-09"))
		expect(t, st, v, 201, "")
		if got := str(field(v, "schedule"), "rrule"); got != "FREQ=WEEKLY;BYDAY=MO" {
			t.Errorf("%q stored as %q", rule, got)
		}
	}
}

func TestRRuleChangeKeepsOrMovesDue(t *testing.T) {
	f := mcpTestEnv(t)
	secret, _ := mkToken(t, f.aStep, []string{scRead, scWrite}, f.hA)
	cs := f.connect(t, secret)
	// Overdue (the fixture clock is 2026-11-21): the new rule's first date on or after the old due date, still overdue.
	id := f.mk(t, f.a, rruleBody("Old", "FREQ=WEEKLY;BYDAY=TU", "2026-11-03"))
	mustOK(t, cs, "update_chore", map[string]any{"chore": id, "repeat": "FREQ=WEEKLY;BYDAY=FR"})
	if _, v := f.a.do("GET", f.chore(id), nil); str(v, "dueDate") != "2026-11-06" {
		t.Fatalf("overdue: %v", str(v, "dueDate"))
	}
	// Same schedule resubmitted, or only the name changed: the due date stays, even when off-rule.
	st, v := f.a.do("PUT", f.chore(id), rruleBody("Renamed", "FREQ=WEEKLY;BYDAY=FR", "2026-11-03"))
	expect(t, st, v, 200, "")
	if str(v, "dueDate") != "2026-11-06" {
		t.Fatalf("same schedule: %v", str(v, "dueDate"))
	}
	// A rule that already produces the due date keeps it.
	mustOK(t, cs, "update_chore", map[string]any{"chore": id, "repeat": "FREQ=WEEKLY;BYDAY=MO,FR"})
	if _, v := f.a.do("GET", f.chore(id), nil); str(v, "dueDate") != "2026-11-06" {
		t.Fatalf("kept: %v", str(v, "dueDate"))
	}
	// Interval changes keep the due date too.
	mustOK(t, cs, "update_chore", map[string]any{"chore": id, "repeat": "every 3 days"})
	if _, v := f.a.do("GET", f.chore(id), nil); str(v, "dueDate") != "2026-11-06" {
		t.Fatalf("interval: %v", str(v, "dueDate"))
	}
}
