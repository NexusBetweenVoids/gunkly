package schedule

import (
	"reflect"
	"testing"
	"time"
)

func FuzzParseRRule(f *testing.F) {
	for _, s := range []string{
		"FREQ=DAILY", "FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,TH", "FREQ=MONTHLY;BYDAY=1SA",
		"FREQ=MONTHLY;BYDAY=-1FR", "FREQ=MONTHLY;BYMONTHDAY=-1", "FREQ=YEARLY;BYMONTH=4,10;BYDAY=1SA",
		"FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=29", "FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=30",
		"FREQ=DAILY;BYMONTH=2;BYMONTHDAY=29;BYDAY=MO", "FREQ=DAILY;INTERVAL=1000;BYMONTH=2;BYMONTHDAY=29",
		"FREQ=DAILY;COUNT=3", "FREQ=MONTHLY;BYDAY=6MO", "freq=daily", ";;", "FREQ=YEARLY;BYDAY=1SA",
	} {
		f.Add(s, int32(20000), int32(10))
	}
	f.Fuzz(func(t *testing.T, s string, startDays, delta int32) {
		r, err := ParseRRule(s)
		if err != nil {
			return
		}
		c := r.String()
		r2, err := ParseRRule(c)
		if err != nil || !reflect.DeepEqual(r, r2) || r2.String() != c {
			t.Fatalf("round trip %q -> %q: %+v vs %+v (%v)", s, c, r, r2, err)
		}
		start := d("1900-01-01").AddDays(int(startDays) % (300 * 366))
		after := start.AddDays(int(delta) % 5000)
		for range 3 { // Next must terminate and move strictly forward
			n, ok := r.Next(start, after)
			if !ok {
				break
			}
			if !n.After(after) || n.Before(start) {
				t.Fatalf("%s.Next(%v, %v) = %v", c, start, after, n)
			}
			after = n
		}
	})
}

var fuzzZones = []string{
	"Europe/Amsterdam", "America/New_York", "Australia/Lord_Howe", "America/Sao_Paulo",
	"Pacific/Apia", "Asia/Kolkata", "America/St_Johns", "Antarctica/Troll", "UTC",
}

func FuzzLocalTime(f *testing.F) {
	f.Add(uint8(0), int32(55970), uint16(150)) // far future
	f.Add(uint8(0), int32(20177), uint16(150)) // 2025-03-30 02:30 Amsterdam gap
	f.Add(uint8(0), int32(20387), uint16(150)) // 2025-10-26 02:30 Amsterdam ambiguous
	f.Add(uint8(1), int32(20156), uint16(150)) // 2025-03-09 NY gap
	f.Add(uint8(1), int32(20394), uint16(90))  // 2025-11-02 NY ambiguous
	f.Add(uint8(4), int32(15338), uint16(600)) // 2011-12-30, skipped day in Apia
	locs := make([]*time.Location, len(fuzzZones))
	for i, z := range fuzzZones {
		locs[i] = mustLoc(f, z)
	}
	f.Fuzz(func(t *testing.T, zone uint8, days int32, minute uint16) {
		loc := locs[int(zone)%len(locs)]
		date := fromDays(int(days) % (200 * 366)) // roughly 1770..2170
		m := int(minute) % 1440
		got := LocalTime(date, m, loc)
		w := time.Date(date.Year, date.Month, date.Day, m/60, m%60, 0, 0, time.UTC)
		gw := wall(got, loc)
		switch {
		case gw.Equal(w):
			// First occurrence: no earlier instant shows the same wall clock.
			for _, back := range []time.Duration{30 * time.Minute, time.Hour, 2 * time.Hour} {
				if wall(got.Add(-back), loc).Equal(w) {
					t.Fatalf("%s %v %d: %v is not the first occurrence", loc, date, m, got)
				}
			}
		case gw.After(w):
			// Gap: got is the first instant whose wall clock is past w.
			if !wall(got.Add(-time.Nanosecond), loc).Before(w) {
				t.Fatalf("%s %v %d: %v (wall %v) is not the end of a gap", loc, date, m, got, gw)
			}
		default:
			t.Fatalf("%s %v %d: %v has wall clock %v before requested", loc, date, m, got, gw)
		}
	})
}
