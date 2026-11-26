package schedule

import (
	"errors"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // tests must not depend on host zoneinfo
)

func d(s string) Date {
	v, err := ParseDate(s)
	if err != nil {
		panic(s)
	}
	return v
}

func mustLoc(t testing.TB, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func utc(s string) time.Time {
	v, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		panic(s)
	}
	return v
}

func TestDate(t *testing.T) {
	if got := d("2024-02-28").AddDays(1); got != d("2024-02-29") {
		t.Errorf("leap AddDays = %v", got)
	}
	if got := d("2025-01-01").AddDays(-1); got.String() != "2024-12-31" {
		t.Errorf("AddDays(-1) = %v", got)
	}
	if d("2025-03-01").Weekday() != time.Saturday || d("1970-01-01").Weekday() != time.Thursday {
		t.Error("Weekday wrong")
	}
	for _, bad := range []string{"2025-02-29", "2025-13-01", "2025-1-01", "", "2025-01-01T00:00"} {
		if _, err := ParseDate(bad); err == nil {
			t.Errorf("ParseDate(%q) accepted", bad)
		}
	}
	if !d("2025-01-01").Before(d("2025-01-02")) || !d("2025-02-01").After(d("2025-01-31")) || d("2025-01-01").Compare(d("2025-01-01")) != 0 {
		t.Error("Compare wrong")
	}
	if got := DateOf(utc("2025-01-01 23:30"), mustLoc(t, "Europe/Amsterdam")); got != d("2025-01-02") {
		t.Errorf("DateOf = %v", got)
	}
}

func TestLocalTime(t *testing.T) {
	ams, ny := mustLoc(t, "Europe/Amsterdam"), mustLoc(t, "America/New_York")
	tests := []struct {
		name   string
		loc    *time.Location
		date   string
		minute int
		want   string // UTC
	}{
		{"ams normal", ams, "2025-01-15", 9 * 60, "2025-01-15 08:00"},
		{"ams before gap", ams, "2025-03-30", 1*60 + 59, "2025-03-30 00:59"},
		{"ams gap start", ams, "2025-03-30", 2 * 60, "2025-03-30 01:00"},
		{"ams gap middle", ams, "2025-03-30", 2*60 + 30, "2025-03-30 01:00"},
		{"ams after gap", ams, "2025-03-30", 3 * 60, "2025-03-30 01:00"},
		{"ams after gap+1", ams, "2025-03-30", 3*60 + 1, "2025-03-30 01:01"},
		{"ams ambiguous start", ams, "2025-10-26", 2 * 60, "2025-10-26 00:00"},
		{"ams ambiguous middle", ams, "2025-10-26", 2*60 + 30, "2025-10-26 00:30"},
		{"ams after ambiguous", ams, "2025-10-26", 3 * 60, "2025-10-26 02:00"},
		{"ny before gap", ny, "2025-03-09", 1*60 + 59, "2025-03-09 06:59"},
		{"ny gap start", ny, "2025-03-09", 2 * 60, "2025-03-09 07:00"},
		{"ny gap middle", ny, "2025-03-09", 2*60 + 30, "2025-03-09 07:00"},
		{"ny after gap", ny, "2025-03-09", 3 * 60, "2025-03-09 07:00"},
		{"ny ambiguous start", ny, "2025-11-02", 1 * 60, "2025-11-02 05:00"},
		{"ny ambiguous middle", ny, "2025-11-02", 1*60 + 30, "2025-11-02 05:30"},
		{"ny after ambiguous", ny, "2025-11-02", 2 * 60, "2025-11-02 07:00"},
		{"minute 1440 rolls", time.UTC, "2025-12-31", 1440, "2026-01-01 00:00"},
		{"negative minute rolls", time.UTC, "2025-01-01", -60, "2024-12-31 23:00"},
		{"nil loc is UTC", nil, "2025-01-01", 61, "2025-01-01 01:01"},
	}
	for _, tt := range tests {
		got := LocalTime(d(tt.date), tt.minute, tt.loc)
		if !got.Equal(utc(tt.want)) || got.Location() != time.UTC {
			t.Errorf("%s: LocalTime = %v, want %s UTC", tt.name, got, tt.want)
		}
	}

	// time.Date does not give these answers; that is why LocalTime exists.
	if td := time.Date(2025, 3, 9, 2, 30, 0, 0, ny); td.Equal(LocalTime(d("2025-03-09"), 150, ny)) {
		t.Error("time.Date unexpectedly agrees in NY gap")
	}
	if td := time.Date(2025, 10, 26, 2, 30, 0, 0, ams); td.Equal(LocalTime(d("2025-10-26"), 150, ams)) {
		t.Error("time.Date unexpectedly agrees on Amsterdam ambiguous time")
	}
}

func TestInterval(t *testing.T) {
	tests := []struct {
		iv           Interval
		start, after string
		want         string
	}{
		{Interval{1, Months, 31}, "2024-01-31", "2024-01-31", "2024-02-29"},
		{Interval{1, Months, 31}, "2024-01-31", "2024-02-29", "2024-03-31"},
		{Interval{1, Months, 31}, "2025-02-28", "2025-02-28", "2025-03-31"},
		{Interval{1, Months, 31}, "2025-01-31", "2025-04-01", "2025-04-30"},
		{Interval{1, Months, 0}, "2025-01-30", "2025-01-30", "2025-02-28"},
		{Interval{1, Months, 0}, "2025-01-30", "2025-02-28", "2025-03-30"},
		{Interval{3, Months, 0}, "2025-11-30", "2025-11-30", "2026-02-28"},
		{Interval{1, Years, 29}, "2024-02-29", "2024-02-29", "2025-02-28"},
		{Interval{1, Years, 29}, "2025-02-28", "2025-02-28", "2026-02-28"},
		{Interval{1, Years, 29}, "2027-02-28", "2027-02-28", "2028-02-29"},
		{Interval{2, Weeks, 0}, "2025-01-06", "2025-01-06", "2025-01-20"},
		{Interval{2, Weeks, 0}, "2025-01-06", "2025-02-01", "2025-02-03"},
		{Interval{3, Days, 0}, "2025-12-30", "2025-12-30", "2026-01-02"},
		{Interval{3, Days, 0}, "2025-12-30", "2025-01-01", "2025-12-30"}, // start is occurrence 0
	}
	for _, tt := range tests {
		got, ok := tt.iv.Next(d(tt.start), d(tt.after))
		if !ok || got != d(tt.want) {
			t.Errorf("%+v.Next(%s, %s) = %v,%v want %s", tt.iv, tt.start, tt.after, got, ok, tt.want)
		}
	}
	for _, bad := range []Interval{{0, Days, 0}, {1001, Days, 0}, {1, 0, 0}} {
		if _, ok := bad.Next(d("2025-01-01"), d("2025-01-01")); ok {
			t.Errorf("%+v accepted", bad)
		}
	}
}

func TestRRuleNext(t *testing.T) {
	tests := []struct {
		rule, start, after, want string // want "" = no occurrence
	}{
		{"FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=29", "2024-02-29", "2024-02-29", "2028-02-29"},
		{"FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=29", "2096-02-29", "2096-02-29", "2104-02-29"},
		{"FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=30", "2024-01-01", "2024-01-01", ""},
		{"FREQ=MONTHLY;BYMONTH=4;BYMONTHDAY=31", "2024-01-01", "2024-01-01", ""},
		{"FREQ=YEARLY", "2024-02-29", "2024-02-29", "2028-02-29"},
		{"FREQ=MONTHLY", "2025-01-31", "2025-01-31", "2025-03-31"},
		{"FREQ=MONTHLY;BYMONTHDAY=31", "2025-01-31", "2025-01-31", "2025-03-31"},
		{"FREQ=MONTHLY;BYMONTHDAY=-1", "2025-01-31", "2025-01-31", "2025-02-28"},
		{"FREQ=MONTHLY;BYMONTHDAY=-1", "2024-01-31", "2024-01-31", "2024-02-29"},
		{"FREQ=MONTHLY;BYDAY=-1FR", "2025-01-01", "2025-01-01", "2025-01-31"},
		{"FREQ=MONTHLY;BYDAY=1SA", "2025-03-01", "2025-03-01", "2025-04-05"},
		{"FREQ=MONTHLY;INTERVAL=2;BYDAY=1SA", "2025-03-01", "2025-03-01", "2025-05-03"},
		{"FREQ=MONTHLY;BYDAY=5MO", "2025-01-01", "2025-01-01", "2025-03-31"},
		{"FREQ=MONTHLY;BYMONTHDAY=13;BYDAY=FR", "2025-01-01", "2025-01-01", "2025-06-13"},
		{"FREQ=YEARLY;BYMONTH=4,10;BYDAY=1SA", "2025-01-01", "2025-01-01", "2025-04-05"},
		{"FREQ=YEARLY;BYMONTH=4,10;BYDAY=1SA", "2025-01-01", "2025-04-05", "2025-10-04"},
		{"FREQ=YEARLY;BYMONTH=4,10", "2025-01-15", "2025-01-15", "2025-04-15"},
		{"FREQ=YEARLY;BYMONTHDAY=1", "2025-01-01", "2025-01-01", "2025-02-01"},
		{"FREQ=YEARLY;INTERVAL=2", "2025-06-01", "2025-06-01", "2027-06-01"},
		{"FREQ=WEEKLY", "2025-01-08", "2025-01-08", "2025-01-15"},
		{"FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,TH", "2025-01-06", "2025-01-06", "2025-01-09"},
		{"FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,TH", "2025-01-06", "2025-01-09", "2025-01-20"},
		{"FREQ=WEEKLY;BYDAY=SU", "2025-01-05", "2025-01-05", "2025-01-12"},
		{"FREQ=WEEKLY;BYMONTH=3;BYDAY=MO", "2025-01-06", "2025-01-06", "2025-03-03"},
		{"FREQ=DAILY;INTERVAL=3", "2025-01-01", "2025-01-02", "2025-01-04"},
		{"FREQ=DAILY;BYDAY=SA,SU", "2025-01-01", "2025-01-01", "2025-01-04"},
		{"FREQ=DAILY;BYMONTH=2;BYMONTHDAY=29;BYDAY=MO", "2017-01-01", "2017-01-01", "2044-02-29"},
		{"FREQ=MONTHLY;BYMONTHDAY=15", "2025-03-01", "2024-01-01", "2025-03-15"}, // nothing before start
		// BYSETPOS picks from each period's dates: first Saturday, last weekday of the month.
		{"FREQ=MONTHLY;BYDAY=SA;BYSETPOS=1", "2026-11-26", "2026-11-26", "2026-12-05"},
		{"FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=-1", "2026-01-01", "2026-01-01", "2026-01-30"},
		{"FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=-1", "2026-01-01", "2026-01-30", "2026-02-27"},
		{"FREQ=MONTHLY;BYDAY=SA;BYSETPOS=7", "2026-01-01", "2026-01-01", ""}, // no month has 7 Saturdays
	}
	for _, tt := range tests {
		r, err := ParseRRule(tt.rule)
		if err != nil {
			t.Fatalf("%s: %v", tt.rule, err)
		}
		got, ok := r.Next(d(tt.start), d(tt.after))
		if tt.want == "" {
			if ok {
				t.Errorf("%s from %s: got %v, want none", tt.rule, tt.after, got)
			}
			continue
		}
		if !ok || got != d(tt.want) {
			t.Errorf("%s start %s after %s = %v,%v want %s", tt.rule, tt.start, tt.after, got, ok, tt.want)
		}
	}
}

func TestParseRRuleRejects(t *testing.T) {
	for _, s := range []string{
		"", "RRULE:FREQ=DAILY", "FREQ=HOURLY", "FREQ=SECONDLY", "freq=daily", "FREQ=daily",
		"FREQ=DAILY;COUNT=3", "FREQ=DAILY;UNTIL=20250101", "FREQ=MONTHLY;BYSETPOS=1",
		"FREQ=MONTHLY;BYDAY=SA;BYSETPOS=0", "FREQ=MONTHLY;BYDAY=SA;BYSETPOS=367", "FREQ=MONTHLY;BYDAY=SA;BYSETPOS=-367",
		"FREQ=MONTHLY;BYDAY=SA;BYSETPOS=1,1", "FREQ=MONTHLY;BYDAY=SA;BYSETPOS=", "FREQ=MONTHLY;BYDAY=SA;BYSETPOS=+1",
		"FREQ=DAILY;BYHOUR=9", "FREQ=DAILY;WKST=MO", "FREQ=DAILY;X-FOO=1",
		"FREQ=DAILY;FREQ=DAILY", "FREQ=DAILY;INTERVAL=2;INTERVAL=2", "INTERVAL=2",
		"FREQ=DAILY;", ";FREQ=DAILY", "FREQ=DAILY;;INTERVAL=2", "FREQ=", "FREQ", " FREQ=DAILY",
		"FREQ=DAILY;INTERVAL=0", "FREQ=DAILY;INTERVAL=1001", "FREQ=DAILY;INTERVAL=01",
		"FREQ=DAILY;INTERVAL=+2", "FREQ=DAILY;INTERVAL=-1", "FREQ=DAILY;INTERVAL=99999",
		"FREQ=WEEKLY;BYDAY=1MO", "FREQ=DAILY;BYDAY=-1FR", "FREQ=YEARLY;BYDAY=1SA",
		"FREQ=MONTHLY;BYDAY=6MO", "FREQ=MONTHLY;BYDAY=0MO", "FREQ=MONTHLY;BYDAY=-0MO",
		"FREQ=MONTHLY;BYDAY=MON", "FREQ=MONTHLY;BYDAY=mo", "FREQ=MONTHLY;BYDAY=",
		"FREQ=MONTHLY;BYDAY=MO,", "FREQ=MONTHLY;BYDAY=MO,MO", "FREQ=MONTHLY;BYDAY=1MO,1MO",
		"FREQ=MONTHLY;BYMONTHDAY=0", "FREQ=MONTHLY;BYMONTHDAY=32", "FREQ=MONTHLY;BYMONTHDAY=-32",
		"FREQ=MONTHLY;BYMONTHDAY=1,1", "FREQ=WEEKLY;BYMONTHDAY=1",
		"FREQ=YEARLY;BYMONTH=0", "FREQ=YEARLY;BYMONTH=13", "FREQ=YEARLY;BYMONTH=-1", "FREQ=YEARLY;BYMONTH=4,4",
		"FREQ=DAILY" + strings.Repeat(";BYDAY=MO", 30),
	} {
		if r, err := ParseRRule(s); err == nil {
			t.Errorf("ParseRRule(%q) = %v, want error", s, r)
		} else if !errors.Is(err, ErrInvalidRRule) {
			t.Errorf("ParseRRule(%q) error %v does not wrap ErrInvalidRRule", s, err)
		}
	}
}

func TestRRuleCanonical(t *testing.T) {
	for in, want := range map[string]string{
		"FREQ=DAILY":                                      "FREQ=DAILY",
		"FREQ=DAILY;INTERVAL=1":                           "FREQ=DAILY",
		"INTERVAL=2;FREQ=WEEKLY;BYDAY=TH,MO":              "FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,TH",
		"FREQ=YEARLY;BYDAY=1SA;BYMONTH=10,4":              "FREQ=YEARLY;BYMONTH=4,10;BYDAY=1SA",
		"FREQ=MONTHLY;BYMONTHDAY=15,-1,1":                 "FREQ=MONTHLY;BYMONTHDAY=-1,1,15",
		"FREQ=MONTHLY;BYDAY=SU,-1FR,1MO,MO":               "FREQ=MONTHLY;BYDAY=MO,1MO,-1FR,SU",
		"FREQ=MONTHLY;INTERVAL=1000;BYDAY=-5SU":           "FREQ=MONTHLY;INTERVAL=1000;BYDAY=-5SU",
		"FREQ=DAILY;BYMONTH=2;BYMONTHDAY=29;BYDAY=MO":     "FREQ=DAILY;BYMONTH=2;BYMONTHDAY=29;BYDAY=MO",
		"FREQ=YEARLY;BYMONTH=12;BYMONTHDAY=-31;BYDAY=2WE": "FREQ=YEARLY;BYMONTH=12;BYMONTHDAY=-31;BYDAY=2WE",
		"FREQ=MONTHLY;BYSETPOS=-1,1;BYDAY=SA":             "FREQ=MONTHLY;BYDAY=SA;BYSETPOS=-1,1",
	} {
		r, err := ParseRRule(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got := r.String(); got != want {
			t.Errorf("String(%s) = %s, want %s", in, got, want)
		}
		r2, err := ParseRRule(r.String())
		if err != nil || !reflect.DeepEqual(r, r2) {
			t.Errorf("round trip %s: %+v vs %+v (%v)", in, r, r2, err)
		}
	}
}

func TestNextDue(t *testing.T) {
	monthly1SA, _ := ParseRRule("FREQ=MONTHLY;BYDAY=1SA")
	bimonthly, _ := ParseRRule("FREQ=MONTHLY;INTERVAL=2;BYDAY=1SA")
	weekly := Interval{1, Weeks, 0}
	monthEnd := Interval{1, Months, 31}
	tests := []struct {
		name            string
		r               Rule
		a               Anchor
		scheduled, done string
		want            string
	}{
		{"interval from completion on time", weekly, FromCompletion, "2025-01-06", "2025-01-06", "2025-01-13"},
		{"interval from completion late", weekly, FromCompletion, "2025-01-06", "2025-01-09", "2025-01-16"},
		{"interval from completion early", weekly, FromCompletion, "2025-01-06", "2025-01-04", "2025-01-11"},
		{"interval from scheduled overdue many cycles", weekly, FromScheduled, "2025-01-06", "2025-02-01", "2025-02-03"},
		{"interval from scheduled overdue on occurrence", weekly, FromScheduled, "2025-01-06", "2025-02-03", "2025-02-10"},
		{"interval from scheduled early", weekly, FromScheduled, "2025-01-06", "2025-01-02", "2025-01-13"},
		{"month end keeps anchor", monthEnd, FromScheduled, "2025-02-28", "2025-02-28", "2025-03-31"},
		{"month end overdue", monthEnd, FromScheduled, "2025-01-31", "2025-04-15", "2025-04-30"},
		{"month end from completion", monthEnd, FromCompletion, "2025-01-31", "2025-01-31", "2025-02-28"},
		{"from completion uses completion day", monthEnd, FromCompletion, "2025-02-28", "2025-02-28", "2025-03-28"},
		{"rrule scheduled on time", monthly1SA, FromScheduled, "2025-03-01", "2025-03-01", "2025-04-05"},
		{"rrule scheduled overdue many cycles", monthly1SA, FromScheduled, "2025-03-01", "2025-05-10", "2025-06-07"},
		{"rrule scheduled early", monthly1SA, FromScheduled, "2025-03-01", "2025-02-27", "2025-04-05"},
		{"rrule interval keeps series", bimonthly, FromScheduled, "2025-03-01", "2025-04-10", "2025-05-03"},
		{"rrule from completion", monthly1SA, FromCompletion, "2025-03-01", "2025-03-10", "2025-04-05"},
		{"rrule from completion early", monthly1SA, FromCompletion, "2025-03-01", "2025-02-27", "2025-04-05"},
		{"rrule from completion interval counts from completion", bimonthly, FromCompletion, "2025-03-01", "2025-04-10", "2025-06-07"},
	}
	for _, tt := range tests {
		got, ok := NextDue(tt.r, tt.a, d(tt.scheduled), d(tt.done))
		if !ok || got != d(tt.want) {
			t.Errorf("%s: got %v,%v want %s", tt.name, got, ok, tt.want)
		}
	}
	if _, ok := NextDue(weekly, 0, d("2025-01-01"), d("2025-01-01")); ok {
		t.Error("unknown anchor accepted")
	}
}

func TestOccurrences(t *testing.T) {
	r, _ := ParseRRule("FREQ=YEARLY;BYMONTH=4,10;BYDAY=1SA")
	got := Occurrences(r, d("2025-01-01"), d("2025-04-05"), d("2026-04-04"))
	want := []Date{d("2025-04-05"), d("2025-10-04"), d("2026-04-04")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Occurrences = %v", got)
	}
	daily, _ := ParseRRule("FREQ=DAILY")
	if n := len(Occurrences(daily, d("2024-01-01"), d("2024-01-01"), d("2024-12-31"))); n != 366 {
		t.Errorf("daily over leap year = %d", n)
	}
	if got := Occurrences(Interval{1, Months, 31}, d("2025-01-31"), d("2025-01-01"), d("2025-04-30")); !reflect.DeepEqual(got,
		[]Date{d("2025-01-31"), d("2025-02-28"), d("2025-03-31"), d("2025-04-30")}) {
		t.Errorf("interval Occurrences = %v", got)
	}
}

// randRule builds a random valid rule from components.
func randRule(rng *rand.Rand) RRule {
	r := RRule{Freq: Freq(rng.IntN(4) + 1), Interval: 1}
	if rng.IntN(3) == 0 {
		r.Interval = rng.IntN(MaxInterval) + 1
	} else if rng.IntN(2) == 0 {
		r.Interval = rng.IntN(4) + 1
	}
	if rng.IntN(3) == 0 {
		for m := time.January; m <= time.December; m++ {
			if rng.IntN(4) == 0 {
				r.ByMonth = append(r.ByMonth, m)
			}
		}
	}
	if r.Freq != Weekly && rng.IntN(3) == 0 {
		for n := -31; n <= 31; n++ {
			if n != 0 && rng.IntN(20) == 0 {
				r.ByMonthDay = append(r.ByMonthDay, n)
			}
		}
	}
	ordinalOK := r.Freq == Monthly || (r.Freq == Yearly && len(r.ByMonth) > 0)
	if rng.IntN(2) == 0 {
		for _, day := range []time.Weekday{1, 2, 3, 4, 5, 6, 0} {
			for _, n := range []int{-5, -4, -3, -2, -1, 0, 1, 2, 3, 4, 5} {
				if (n == 0 || ordinalOK) && rng.IntN(25) == 0 {
					r.ByDay = append(r.ByDay, WeekdayNum{n, day})
				}
			}
		}
	}
	return r
}

func randDate(rng *rand.Rand) Date { return d("1990-01-01").AddDays(rng.IntN(60 * 366)) }

func TestRRuleProperties(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for range 3000 {
		r := randRule(rng)
		s := r.String()
		p, err := ParseRRule(s)
		if err != nil {
			t.Fatalf("generated rule %s rejected: %v", s, err)
		}
		if !reflect.DeepEqual(nilEmpty(r), nilEmpty(p)) {
			t.Fatalf("round trip %s: %+v != %+v", s, r, p)
		}
		start := randDate(rng)
		after := start.AddDays(rng.IntN(800) - 100)
		got, ok := p.Next(start, after)
		if ok && (!got.After(after) || got.Before(start)) {
			t.Fatalf("%s.Next(%v, %v) = %v", s, start, after, got)
		}
		done := start.AddDays(rng.IntN(400) - 30)
		if nd, ok := NextDue(p, FromScheduled, start, done); ok && !nd.After(done) {
			t.Fatalf("%s NextDue(sched %v, done %v) = %v", s, start, done, nd)
		}
	}
}

func TestIntervalProperties(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	for range 20000 {
		iv := Interval{rng.IntN(40) + 1, Unit(rng.IntN(4) + 1), rng.IntN(32)}
		start := randDate(rng)
		after := start.AddDays(rng.IntN(3000) - 100)
		got, ok := iv.Next(start, after)
		if !ok || !got.After(after) {
			t.Fatalf("%+v.Next(%v, %v) = %v,%v", iv, start, after, got, ok)
		}
		if iv.Unit >= Months && iv.AnchorDay > 0 && got != start && got.Day != min(iv.AnchorDay, daysIn(got.Year, got.Month)) {
			t.Fatalf("%+v.Next(%v, %v) = %v lost anchor", iv, start, after, got)
		}
		for _, a := range []Anchor{FromScheduled, FromCompletion} {
			nd, ok := NextDue(iv, a, start, after)
			if !ok || !nd.After(after) {
				t.Fatalf("%+v NextDue(%d, %v, %v) = %v", iv, a, start, after, nd)
			}
		}
	}
}

// nilEmpty normalises empty slices to nil for comparison.
func nilEmpty(r RRule) RRule {
	if len(r.ByMonth) == 0 {
		r.ByMonth = nil
	}
	if len(r.ByMonthDay) == 0 {
		r.ByMonthDay = nil
	}
	if len(r.ByDay) == 0 {
		r.ByDay = nil
	}
	return r
}

// The rule an AI assistant sent through MCP: of the Saturdays in the first 14 days of April and
// October, the first and the last of each year.
func TestRRuleBySetPosYearly(t *testing.T) {
	r, err := ParseRRule("FREQ=YEARLY;BYMONTH=4,10;BYDAY=SA;BYMONTHDAY=1,2,3,4,5,6,7,8,9,10,11,12,13,14;BYSETPOS=1,-1")
	if err != nil {
		t.Fatal(err)
	}
	start, after := d("2026-11-26"), d("2026-11-26")
	var got []string
	for range 4 {
		next, ok := r.Next(start, after)
		if !ok {
			t.Fatalf("no occurrence after %v", after)
		}
		got = append(got, next.String())
		after = next
	}
	if want := "2027-04-03 2027-10-09 2028-04-01 2028-10-14"; strings.Join(got, " ") != want {
		t.Fatalf("got %v, want %s", got, want)
	}
}
