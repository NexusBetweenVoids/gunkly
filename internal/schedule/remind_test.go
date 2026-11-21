package schedule

import (
	"reflect"
	"testing"
	"time"
)

func TestReminderValidate(t *testing.T) {
	for _, ok := range []Reminder{{OnDue, 0, 0}, {Before, 1, 1439}, {Overdue, 365, 600}} {
		if err := ok.Validate(); err != nil {
			t.Errorf("%+v: %v", ok, err)
		}
	}
	for _, bad := range []Reminder{{0, 1, 0}, {OnDue, 0, -1}, {OnDue, 0, 1440}, {Before, 0, 0}, {Overdue, 0, 0}, {Overdue, 366, 0}} {
		if bad.Validate() == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}

func TestReminders(t *testing.T) {
	ams, ny := mustLoc(t, "Europe/Amsterdam"), mustLoc(t, "America/New_York")
	all := []time.Time{utc("2000-01-01 00:00"), utc("2100-01-01 00:00")}
	tests := []struct {
		name     string
		due      string
		specs    []Reminder
		loc      *time.Location
		from, to time.Time
		want     []Fire
	}{
		{"before and on, on lands in gap", "2025-03-30", []Reminder{{OnDue, 0, 150}, {Before, 1, 540}}, ams, all[0], all[1],
			[]Fire{{Before, utc("2025-03-29 08:00")}, {OnDue, utc("2025-03-30 01:00")}}},
		{"overdue daily skips 23h spring-forward gap", "2025-03-28", []Reminder{{Overdue, 1, 540}}, ams,
			utc("2025-03-28 00:00"), utc("2025-04-01 00:00"),
			[]Fire{{Overdue, utc("2025-03-29 08:00")}, {Overdue, utc("2025-03-31 07:00")}}},
		{"overdue window mid-sequence", "2025-03-28", []Reminder{{Overdue, 1, 540}}, ams,
			utc("2025-03-30 12:00"), utc("2025-04-02 00:00"),
			[]Fire{{Overdue, utc("2025-03-31 07:00")}, {Overdue, utc("2025-04-01 07:00")}}},
		{"overdue every 2 days", "2025-01-01", []Reminder{{Overdue, 2, 540}}, ny,
			utc("2025-01-01 00:00"), utc("2025-01-08 00:00"),
			[]Fire{{Overdue, utc("2025-01-03 14:00")}, {Overdue, utc("2025-01-05 14:00")}, {Overdue, utc("2025-01-07 14:00")}}},
		{"overdue across fall-back, ambiguous uses first", "2025-11-01", []Reminder{{Overdue, 1, 90}}, ny,
			utc("2025-11-01 00:00"), utc("2025-11-04 00:00"),
			[]Fire{{Overdue, utc("2025-11-02 05:30")}, {Overdue, utc("2025-11-03 06:30")}}},
		{"window is half-open", "2025-01-10", []Reminder{{OnDue, 0, 540}, {Before, 2, 540}}, ny,
			utc("2025-01-08 14:00"), utc("2025-01-10 14:00"),
			[]Fire{{Before, utc("2025-01-08 14:00")}}},
		{"invalid specs ignored", "2025-01-10", []Reminder{{Overdue, 0, 540}, {0, 0, 0}}, ny, all[0], all[1], nil},
	}
	for _, tt := range tests {
		got := Reminders(d(tt.due), tt.specs, tt.loc, tt.from, tt.to)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s:\n got %v\nwant %v", tt.name, got, tt.want)
		}
	}
}

func TestQuietHoursDefer(t *testing.T) {
	ams, ny := mustLoc(t, "Europe/Amsterdam"), mustLoc(t, "America/New_York")
	night := QuietHours{22 * 60, 7 * 60}
	tests := []struct {
		name string
		q    QuietHours
		loc  *time.Location
		in   string
		want string
	}{
		{"wrap: late evening defers to next morning", night, ams, "2025-01-10 21:30", "2025-01-11 06:00"},
		{"wrap: start is inclusive", night, ams, "2025-01-10 21:00", "2025-01-11 06:00"},
		{"wrap: early morning defers to same morning", night, ams, "2025-01-11 05:59", "2025-01-11 06:00"},
		{"wrap: end is exclusive", night, ams, "2025-01-11 06:00", "2025-01-11 06:00"},
		{"wrap: daytime untouched", night, ams, "2025-01-10 20:59", "2025-01-10 20:59"},
		{"wrap: spring-forward night", night, ams, "2025-03-29 22:00", "2025-03-30 05:00"},
		{"plain window", QuietHours{13 * 60, 15 * 60}, ams, "2025-01-10 13:00", "2025-01-10 14:00"},
		{"plain window outside", QuietHours{13 * 60, 15 * 60}, ams, "2025-01-10 14:00", "2025-01-10 14:00"},
		{"disabled", QuietHours{0, 0}, ams, "2025-01-10 02:00", "2025-01-10 02:00"},
		{"end in DST gap goes to next valid time", QuietHours{22 * 60, 150}, ny, "2025-03-09 04:00", "2025-03-09 07:00"},
	}
	for _, tt := range tests {
		got := tt.q.Defer(utc(tt.in), tt.loc)
		if !got.Equal(utc(tt.want)) {
			t.Errorf("%s: Defer(%s) = %v, want %s", tt.name, tt.in, got.UTC(), tt.want)
		}
	}
}

func TestCoalesce(t *testing.T) {
	x, y := utc("2025-01-10 06:00"), utc("2025-01-10 08:00")
	got := Coalesce([]Fire{{OnDue, y}, {Before, x}, {Overdue, x}, {OnDue, x}})
	if want := []Fire{{Overdue, x}, {OnDue, y}}; !reflect.DeepEqual(got, want) {
		t.Errorf("Coalesce = %v, want %v", got, want)
	}

	// Pipeline: two reminders in quiet hours are deferred to the same release
	// time and become one, never dropped entirely.
	ams := mustLoc(t, "Europe/Amsterdam")
	q := QuietHours{22 * 60, 7 * 60}
	fs := Reminders(d("2025-01-10"), []Reminder{{Before, 1, 23 * 60}, {OnDue, 0, 6 * 60}, {OnDue, 0, 12 * 60}}, ams,
		utc("2025-01-01 00:00"), utc("2025-02-01 00:00"))
	for i := range fs {
		fs[i].At = q.Defer(fs[i].At, ams)
	}
	got = Coalesce(fs)
	if want := []Fire{{OnDue, utc("2025-01-10 06:00")}, {OnDue, utc("2025-01-10 11:00")}}; !reflect.DeepEqual(got, want) {
		t.Errorf("deferred Coalesce = %v, want %v", got, want)
	}
}

func TestCatchUp(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	specs := []Reminder{{Before, 1, 540}, {OnDue, 0, 540}, {Overdue, 1, 540}}
	due := d("2025-01-06")
	tests := []struct {
		name         string
		lastRun, now string
		want         Fire
		ok           bool
	}{
		{"long downtime: only the latest", "2025-01-04 12:00", "2025-01-10 15:00", Fire{Overdue, utc("2025-01-10 14:00")}, true},
		{"now is exclusive", "2025-01-04 12:00", "2025-01-10 14:00", Fire{Overdue, utc("2025-01-09 14:00")}, true},
		{"lastRun is inclusive", "2025-01-10 14:00", "2025-01-10 14:01", Fire{Overdue, utc("2025-01-10 14:00")}, true},
		{"nothing missed", "2025-01-10 14:01", "2025-01-10 20:00", Fire{}, false},
		{"before-reminder missed", "2025-01-05 13:00", "2025-01-05 15:00", Fire{Before, utc("2025-01-05 14:00")}, true},
	}
	for _, tt := range tests {
		got, ok := CatchUp(due, specs, ny, utc(tt.lastRun), utc(tt.now))
		if ok != tt.ok || got != tt.want {
			t.Errorf("%s: CatchUp = %v,%v want %v,%v", tt.name, got, ok, tt.want, tt.ok)
		}
	}
}
