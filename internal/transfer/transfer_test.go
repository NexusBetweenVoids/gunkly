package transfer

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

var testOpts = Options{
	MaxChores: 2000, MaxAreas: 100, MaxReminders: 10, MaxCompletions: 1000,
	ValidPreset: func(n string) bool { return n == "gentle" || n == "nag" || n == "none" },
}

const validFile = `{
  "format": "gunkly-household-export", "version": 1,
  "household": {"name": " Home ", "timezone": "Europe/Amsterdam", "quietHours": {"start": 1320, "end": 420}, "defaultReminderPreset": "gentle"},
  "areas": [{"name": "Kitchen"}, {"name": "kitchen"}, {"name": "Garden"}],
  "chores": [
    {"name": "Wipe", "kind": "recurring", "area": "KITCHEN", "link": "https://example.com",
     "schedule": {"type": "interval", "interval": {"every": 1, "unit": "week"}, "firstDue": "2026-11-14"},
     "dueDate": "2026-11-28", "reminderPreset": "custom", "reminders": [{"kind": "on", "days": 5, "minute": 540}],
     "assignees": [{"displayName": "Ann", "username": "ann"}],
     "completions": [{"completedAt": "2026-11-21T12:00:00Z", "by": {"username": "ann"}, "source": "app", "reverted": false}]},
    {"name": "Gate", "kind": "oneoff", "schedule": {"firstDue": "2026-12-01"}}
  ]
}`

func reason(err error) string {
	var te *Error
	if errors.As(err, &te) {
		return te.Reason + "@" + te.Path
	}
	return ""
}

func TestParseNormalises(t *testing.T) {
	f, err := Parse([]byte(validFile), testOpts)
	if err != nil {
		t.Fatal(err)
	}
	if f.Household.Name != "Home" || len(f.Areas) != 2 || f.Chores[0].Area != "Kitchen" {
		t.Fatalf("not normalised: %+v", f)
	}
	if f.Chores[0].Reminders[0].Days != 0 || f.Chores[0].Schedule.Anchor != "from_completion" {
		t.Fatalf("chore: %+v", f.Chores[0])
	}
	if f.Chores[1].Schedule.Type != "interval" || f.Chores[1].Schedule.Anchor != "from_completion" {
		t.Fatalf("one-off: %+v", f.Chores[1])
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct{ name, old, new, want string }{
		{"not json", validFile, "nope", "not_json@"},
		{"format", `"gunkly-household-export"`, `"x"`, "format@format"},
		{"version", `"version": 1`, `"version": 2`, "version@version"},
		{"unknown field", `"areas"`, `"extra": 1, "areas"`, "not_json@"},
		{"timezone", "Europe/Amsterdam", "Mars/Base", "invalid@household.timezone"},
		{"quiet", `"start": 1320, "end": 420`, `"start": 5, "end": 5`, "invalid@household.quietHours"},
		{"preset", `"gentle"`, `"loud"`, "invalid@household.defaultReminderPreset"},
		{"link scheme", "https://example.com", "ftp://example.com", "invalid@chores[0].link"},
		{"area missing", `"area": "KITCHEN"`, `"area": "Attic"`, "invalid@chores[0].area"},
		{"interval", `"every": 1`, `"every": 0`, "invalid@chores[0].schedule.interval"},
		{"rrule", `"type": "interval", "interval": {"every": 1, "unit": "week"}`, `"type": "rrule", "rrule": "FREQ=NOPE"`, "invalid@chores[0].schedule.rrule"},
		{"impossible rrule", `"type": "interval", "interval": {"every": 1, "unit": "week"}`, `"type": "rrule", "rrule": "FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=30"`, "invalid@chores[0].schedule.rrule"},
		{"first due", "2026-11-14", "14-11-2026", "invalid@chores[0].schedule.firstDue"},
		{"due date", `"dueDate": "2026-11-28"`, `"dueDate": "2026-13-45"`, "invalid@chores[0].dueDate"},
		{"reminder kind", `"kind": "on"`, `"kind": "sometimes"`, "invalid@chores[0].reminders[0]"},
		{"reminder minute", `"minute": 540`, `"minute": 1440`, "invalid@chores[0].reminders[0]"},
		{"source", `"source": "app"`, `"source": "x"`, "invalid@chores[0].completions[0].source"},
		{"time range", "2026-11-21T12:00:00Z", "0001-01-01T00:00:00Z", "invalid@chores[0].completions[0].completedAt"},
		{"nul", `"name": "Wipe"`, `"name": "Wi\u0000pe"`, "invalid@chores[0].name"},
		{"empty name", `"name": "Wipe"`, `"name": " "`, "required@chores[0].name"},
		{"long name", `"name": "Wipe"`, `"name": "` + strings.Repeat("x", 201) + `"`, "too_long@chores[0].name"},
		{"kind", `"kind": "oneoff"`, `"kind": "weekly"`, "invalid@chores[1].kind"},
		{"trailing data", validFile, validFile + ` {}`, "not_json@"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := strings.Replace(validFile, c.old, c.new, 1)
			if c.name == "not json" || c.name == "trailing data" {
				in = c.new
			}
			_, err := Parse([]byte(in), testOpts)
			if got := reason(err); got != c.want {
				t.Fatalf("got %q (%v), want %q", got, err, c.want)
			}
		})
	}
}

func TestParseAcceptsEverySource(t *testing.T) {
	for _, src := range []string{"app", "mark_done_link", "api_token", "oauth", "import"} {
		in := strings.Replace(validFile, `"source": "app"`, `"source": "`+src+`"`, 1)
		if _, err := Parse([]byte(in), testOpts); err != nil {
			t.Errorf("source %q: %v", src, err)
		}
	}
}

func TestParseLimits(t *testing.T) {
	for name, tc := range map[string]struct {
		o    Options
		want string
	}{
		"chores":      {Options{MaxChores: 1, MaxAreas: 100, MaxReminders: 10, MaxCompletions: 10, ValidPreset: testOpts.ValidPreset}, "limit_chores@chores"},
		"areas":       {Options{MaxChores: 10, MaxAreas: 1, MaxReminders: 10, MaxCompletions: 10, ValidPreset: testOpts.ValidPreset}, "limit_areas@areas"},
		"completions": {Options{MaxChores: 10, MaxAreas: 10, MaxReminders: 10, MaxCompletions: 0, ValidPreset: testOpts.ValidPreset}, "limit_completions@chores[0].completions"},
		"reminders":   {Options{MaxChores: 10, MaxAreas: 10, MaxReminders: 0, MaxCompletions: 10, ValidPreset: testOpts.ValidPreset}, "limit_reminders@chores[0].reminders"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(validFile), tc.o); reason(err) != tc.want {
				t.Fatalf("got %q, want %q", reason(err), tc.want)
			}
		})
	}
}

// FuzzImport: Parse never panics, and a file it accepts is stable (its
// normal form parses again to the same document).
func FuzzImport(f *testing.F) {
	f.Add([]byte(validFile))
	f.Add([]byte(`{"format":"gunkly-household-export","version":1}`))
	f.Add([]byte(`{"format":"gunkly-household-export","version":1,"household":{"name":"x","timezone":"UTC"},"chores":[{"name":"a","kind":"recurring","schedule":{"type":"rrule","rrule":"FREQ=MONTHLY;BYMONTHDAY=-1","firstDue":"2026-01-31"}}]}`))
	f.Add([]byte(`[]`))
	f.Add([]byte(``))
	f.Fuzz(func(t *testing.T, data []byte) {
		doc, err := Parse(data, testOpts)
		if err != nil {
			var te *Error
			if !errors.As(err, &te) {
				t.Fatalf("non-validation error: %v", err)
			}
			return
		}
		b, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		again, err := Parse(b, testOpts)
		if err != nil {
			t.Fatalf("normal form rejected: %v\n%s", err, b)
		}
		b2, _ := json.Marshal(again)
		if string(b) != string(b2) {
			t.Fatalf("normal form unstable:\n%s\n%s", b, b2)
		}
	})
}
