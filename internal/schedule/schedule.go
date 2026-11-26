// Package schedule is Gunkly's pure scheduling math: civil dates, interval
// and RRULE-subset recurrence, next-due calculation, reminder instants, DST
// handling, quiet hours and catch-up. It performs no I/O and never reads the
// clock; callers pass every instant in (ADR 0005, 0011).
package schedule

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// maxYear bounds every computation; nothing is scheduled past it.
const maxYear = 9999

// Date is a civil (calendar) date with no time zone. Due dates are Dates in
// the household time zone. Methods assume a valid date; AddDays normalises.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// DateOf returns the civil date of t in loc.
func DateOf(t time.Time, loc *time.Location) Date {
	y, m, d := t.In(loc).Date()
	return Date{y, m, d}
}

// ParseDate parses a strict YYYY-MM-DD date.
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return Date{}, errors.New("schedule: invalid date")
	}
	return DateOf(t, time.UTC), nil
}

// String formats d as YYYY-MM-DD.
func (d Date) String() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, int(d.Month), d.Day)
}

// AddDays returns d shifted by n days (n may be negative).
func (d Date) AddDays(n int) Date { return fromDays(d.days() + n) }

// Compare returns -1, 0 or +1 as d is before, equal to or after o.
func (d Date) Compare(o Date) int {
	if c := d.Year - o.Year; c != 0 {
		return sign(c)
	}
	if c := int(d.Month) - int(o.Month); c != 0 {
		return sign(c)
	}
	return sign(d.Day - o.Day)
}

// Before reports whether d is before o.
func (d Date) Before(o Date) bool { return d.Compare(o) < 0 }

// After reports whether d is after o.
func (d Date) After(o Date) bool { return d.Compare(o) > 0 }

// Weekday returns the day of the week of d.
func (d Date) Weekday() time.Weekday { return time.Weekday(mod(d.days()+4, 7)) }

// days is the number of days since 1970-01-01 (a Thursday).
func (d Date) days() int {
	return floorDiv(int(time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC).Unix()), 86400)
}

func fromDays(n int) Date { return DateOf(time.Unix(int64(n)*86400, 0), time.UTC) }

func monthIndex(d Date) int { return d.Year*12 + int(d.Month) - 1 }

func daysIn(y int, m time.Month) int { return time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day() }

// clampDay returns day `day` of month index mi, clamped to the month's last day.
func clampDay(mi, day int) Date {
	y, m := floorDiv(mi, 12), time.Month(mod(mi, 12)+1)
	return Date{y, m, min(day, daysIn(y, m))}
}

func maxDate(a, b Date) Date {
	if a.After(b) {
		return a
	}
	return b
}

// LocalTime returns the instant (UTC) at minute-of-day `minute` on d in loc.
// Minutes outside 0..1439 roll into adjacent days. A wall time that does not
// exist (DST spring-forward gap) yields the first valid instant after the gap,
// i.e. the transition itself; an ambiguous wall time (fall-back) yields the
// first occurrence. time.Date makes neither guarantee: it may land after the
// gap by the gap length, before it, or on the second occurrence.
func LocalTime(d Date, minute int, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	d = d.AddDays(floorDiv(minute, 1440))
	minute = mod(minute, 1440)
	h, mi := minute/60, minute%60
	w := time.Date(d.Year, d.Month, d.Day, h, mi, 0, 0, time.UTC) // requested wall clock
	t := time.Date(d.Year, d.Month, d.Day, h, mi, 0, 0, loc)
	start, end := t.ZoneBounds()

	// Try the offset of t's zone and of its neighbours; keep the earliest
	// instant whose wall clock matches.
	probes := []time.Time{t}
	if !start.IsZero() {
		probes = append(probes, start.Add(-time.Nanosecond))
	}
	if !end.IsZero() {
		probes = append(probes, end)
	}
	var best time.Time
	for _, p := range probes {
		_, off := p.Zone()
		u := w.Add(-time.Duration(off) * time.Second)
		if wall(u, loc).Equal(w) && (best.IsZero() || u.Before(best)) {
			best = u
		}
	}
	if !best.IsZero() {
		return best.UTC()
	}
	// Gap: the answer is the transition at which the wall clock jumps past w.
	for _, b := range []time.Time{start, end} {
		if !b.IsZero() && !wall(b, loc).Before(w) && wall(b.Add(-time.Nanosecond), loc).Before(w) {
			return b.UTC()
		}
	}
	return t.UTC() // not reached with real tzdata
}

// wall returns u's wall clock in loc, expressed as a UTC time for comparison.
func wall(u time.Time, loc *time.Location) time.Time {
	l := u.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), l.Hour(), l.Minute(), l.Second(), l.Nanosecond(), time.UTC)
}

// Rule is a recurrence. Next returns the first occurrence strictly after
// `after` in the series anchored at start, or false if there is none within
// the search horizon (or the rule is invalid).
type Rule interface {
	Next(start, after Date) (Date, bool)
}

// Unit is the unit of an Interval.
type Unit int

// Interval units.
const (
	Days Unit = iota + 1
	Weeks
	Months
	Years
)

// MaxInterval is the largest accepted interval count (Interval.Every, RRULE INTERVAL).
const MaxInterval = 1000

// Interval repeats every Every units. For Months and Years, AnchorDay (1..31)
// is the intended day of month: occurrences clamp to the last day of shorter
// months but return to the anchor afterwards (Jan 31 -> Feb 28 -> Mar 31).
// AnchorDay 0 means start's day. The series start itself is always occurrence 0.
type Interval struct {
	Every     int
	Unit      Unit
	AnchorDay int
}

// Next implements Rule.
func (iv Interval) Next(start, after Date) (Date, bool) {
	if iv.Every < 1 || iv.Every > MaxInterval {
		return Date{}, false
	}
	if after.Before(start) {
		return start, true
	}
	var r Date
	switch iv.Unit {
	case Days, Weeks:
		step := iv.Every
		if iv.Unit == Weeks {
			step *= 7
		}
		k := (after.days()-start.days())/step + 1
		r = fromDays(start.days() + k*step)
	case Months, Years:
		step := iv.Every
		if iv.Unit == Years {
			step *= 12
		}
		anchor := iv.AnchorDay
		if anchor < 1 || anchor > 31 {
			anchor = start.Day
		}
		s := monthIndex(start)
		for k := max(1, (monthIndex(after)-s)/step); ; k++ {
			if r = clampDay(s+k*step, anchor); r.After(after) {
				break
			}
		}
	default:
		return Date{}, false
	}
	return r, r.Year <= maxYear
}

// Anchor selects what the next due date is computed from after a completion.
type Anchor int

// Anchors.
const (
	FromCompletion Anchor = iota + 1
	FromScheduled
)

// NextDue returns the due date following a completion.
//
//   - FromScheduled: the next occurrence of the series through scheduledDue
//     strictly after both scheduledDue and completedOn. Several overdue cycles
//     collapse into the next upcoming date (no backlog); an early completion
//     moves to the occurrence after scheduledDue.
//   - FromCompletion, Interval: completedOn + interval (anchor = completion day).
//   - FromCompletion, other rules: the next occurrence strictly after both
//     completedOn and scheduledDue, with INTERVAL counted from completedOn's
//     period. scheduledDue is still excluded so an early completion never
//     returns the date that was just done.
func NextDue(r Rule, a Anchor, scheduledDue, completedOn Date) (Date, bool) {
	switch a {
	case FromScheduled:
		return r.Next(scheduledDue, maxDate(scheduledDue, completedOn))
	case FromCompletion:
		if iv, ok := r.(Interval); ok {
			iv.AnchorDay = 0
			return iv.Next(completedOn, completedOn)
		}
		return r.Next(completedOn, maxDate(scheduledDue, completedOn))
	}
	return Date{}, false
}

// maxOccurrences caps Occurrences (a daily rule over 12 months fits).
const maxOccurrences = 1000

// Occurrences returns the occurrences of r (series anchored at start) in the
// inclusive range [from, to], at most 1000, for calendar feeds.
func Occurrences(r Rule, start, from, to Date) []Date {
	var out []Date
	d, ok := r.Next(start, from.AddDays(-1))
	for ok && !d.After(to) && len(out) < maxOccurrences {
		out = append(out, d)
		d, ok = r.Next(start, d)
	}
	return out
}

// Freq is an RRULE frequency.
type Freq int

// Frequencies.
const (
	Daily Freq = iota + 1
	Weekly
	Monthly
	Yearly
)

var freqNames = [...]string{Daily: "DAILY", Weekly: "WEEKLY", Monthly: "MONTHLY", Yearly: "YEARLY"}

var dayCodes = [...]string{time.Sunday: "SU", time.Monday: "MO", time.Tuesday: "TU",
	time.Wednesday: "WE", time.Thursday: "TH", time.Friday: "FR", time.Saturday: "SA"}

// WeekdayNum is a BYDAY entry: N is 0 for every such weekday, or the ordinal
// within the month (1..5 from the start, -1..-5 from the end).
type WeekdayNum struct {
	N   int
	Day time.Weekday
}

// RRule is the supported RFC 5545 RRULE subset (ADR 0005):
// FREQ=DAILY|WEEKLY|MONTHLY|YEARLY, INTERVAL=1..1000, BYMONTH=1..12,
// BYMONTHDAY=±1..31 (not with WEEKLY), BYDAY=[±1..5]MO..SU (ordinals only for
// MONTHLY, and YEARLY with BYMONTH, where they count within the month).
// BYSETPOS=±1..366 picks the n-th (or n-th from last) of each FREQ period's
// dates (a month for MONTHLY, a year for YEARLY) and needs another BY part.
// Days that do not exist are skipped as in RFC 5545 (BYMONTHDAY=31 skips
// 30-day months); the editor uses BYMONTHDAY=-1 for "last day of the month".
// Lists are sorted and duplicate-free; Interval is 1 when absent.
type RRule struct {
	Freq       Freq
	Interval   int
	ByMonth    []time.Month
	ByMonthDay []int
	ByDay      []WeekdayNum
	BySetPos   []int
}

// ErrInvalidRRule is wrapped by every ParseRRule error.
var ErrInvalidRRule = errors.New("invalid rrule")

const maxRRuleLen = 256

func rruleErr(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalidRRule}, args...)...)
}

// ParseRRule parses a strict, upper-case RRULE value (no "RRULE:" prefix).
func ParseRRule(s string) (RRule, error) {
	if s == "" || len(s) > maxRRuleLen {
		return RRule{}, rruleErr("empty or longer than %d bytes", maxRRuleLen)
	}
	var r RRule
	seen := map[string]bool{}
	for part := range strings.SplitSeq(s, ";") {
		k, v, ok := strings.Cut(part, "=")
		if !ok || v == "" {
			return RRule{}, rruleErr("malformed part %q", part)
		}
		if seen[k] {
			return RRule{}, rruleErr("duplicate %s", k)
		}
		seen[k] = true
		var err error
		switch k {
		case "FREQ":
			if r.Freq = Freq(slices.Index(freqNames[:], v)); r.Freq < Daily {
				return RRule{}, rruleErr("unsupported FREQ %q", v)
			}
		case "INTERVAL":
			if r.Interval, err = parseNum(v); err != nil || r.Interval < 1 {
				return RRule{}, rruleErr("bad INTERVAL")
			}
		case "BYMONTH":
			for item := range strings.SplitSeq(v, ",") {
				n, err := parseNum(item)
				if err != nil {
					return RRule{}, rruleErr("bad BYMONTH")
				}
				r.ByMonth = append(r.ByMonth, time.Month(n))
			}
		case "BYMONTHDAY":
			for item := range strings.SplitSeq(v, ",") {
				n, err := parseNum(item)
				if err != nil {
					return RRule{}, rruleErr("bad BYMONTHDAY")
				}
				r.ByMonthDay = append(r.ByMonthDay, n)
			}
		case "BYDAY":
			for item := range strings.SplitSeq(v, ",") {
				wd, err := parseWeekdayNum(item)
				if err != nil {
					return RRule{}, err
				}
				r.ByDay = append(r.ByDay, wd)
			}
		case "BYSETPOS":
			for item := range strings.SplitSeq(v, ",") {
				n, err := parseNum(item)
				if err != nil {
					return RRule{}, rruleErr("BYSETPOS must be 1..366 or -1..-366")
				}
				r.BySetPos = append(r.BySetPos, n)
			}
		default:
			return RRule{}, rruleErr("unsupported part %q", k)
		}
	}
	if r.Freq == 0 {
		return RRule{}, rruleErr("missing FREQ")
	}
	if r.Interval == 0 {
		r.Interval = 1
	}
	slices.Sort(r.ByMonth)
	slices.Sort(r.ByMonthDay)
	slices.SortFunc(r.ByDay, cmpWeekdayNum)
	slices.Sort(r.BySetPos)
	if len(slices.Compact(slices.Clone(r.BySetPos))) != len(r.BySetPos) ||
		len(slices.Compact(slices.Clone(r.ByMonth))) != len(r.ByMonth) ||
		len(slices.Compact(slices.Clone(r.ByMonthDay))) != len(r.ByMonthDay) ||
		len(slices.Compact(slices.Clone(r.ByDay))) != len(r.ByDay) {
		return RRule{}, rruleErr("duplicate list value")
	}
	if err := r.validate(); err != nil {
		return RRule{}, err
	}
	return r, nil
}

// parseNum accepts -?[1-9][0-9]{0,3}: no sign "+", no leading zeros.
func parseNum(s string) (int, error) {
	digits := strings.TrimPrefix(s, "-")
	if digits == "" || len(digits) > 4 || digits[0] == '0' || strings.Trim(digits, "0123456789") != "" {
		return 0, ErrInvalidRRule
	}
	return strconv.Atoi(s)
}

func parseWeekdayNum(s string) (WeekdayNum, error) {
	if len(s) < 2 {
		return WeekdayNum{}, rruleErr("bad BYDAY")
	}
	day := slices.Index(dayCodes[:], s[len(s)-2:])
	if day < 0 {
		return WeekdayNum{}, rruleErr("bad BYDAY")
	}
	wn := WeekdayNum{Day: time.Weekday(day)}
	if prefix := s[:len(s)-2]; prefix != "" {
		n, err := parseNum(prefix)
		if err != nil {
			return WeekdayNum{}, rruleErr("bad BYDAY")
		}
		wn.N = n
	}
	return wn, nil
}

// cmpWeekdayNum orders by weekday (Monday first), then ordinal.
func cmpWeekdayNum(a, b WeekdayNum) int {
	if c := mod(int(a.Day)+6, 7) - mod(int(b.Day)+6, 7); c != 0 {
		return c
	}
	return a.N - b.N
}

// validate checks the semantic constraints shared by ParseRRule and Next.
func (r RRule) validate() error {
	if r.Freq < Daily || r.Freq > Yearly {
		return rruleErr("unsupported FREQ")
	}
	if r.Interval < 1 || r.Interval > MaxInterval {
		return rruleErr("INTERVAL must be 1..%d", MaxInterval)
	}
	for _, m := range r.ByMonth {
		if m < time.January || m > time.December {
			return rruleErr("BYMONTH must be 1..12")
		}
	}
	if len(r.ByMonthDay) > 0 && r.Freq == Weekly {
		return rruleErr("BYMONTHDAY not allowed with WEEKLY")
	}
	for _, n := range r.ByMonthDay {
		if n == 0 || n < -31 || n > 31 {
			return rruleErr("BYMONTHDAY must be 1..31 or -1..-31")
		}
	}
	ordinalOK := r.Freq == Monthly || (r.Freq == Yearly && len(r.ByMonth) > 0)
	for _, wn := range r.ByDay {
		if wn.Day < time.Sunday || wn.Day > time.Saturday {
			return rruleErr("bad BYDAY weekday")
		}
		if wn.N != 0 && (!ordinalOK || wn.N < -5 || wn.N > 5) {
			return rruleErr("BYDAY ordinal must be ±1..5 and needs MONTHLY or YEARLY with BYMONTH")
		}
	}
	if len(r.BySetPos) > 0 && len(r.ByMonth)+len(r.ByMonthDay)+len(r.ByDay) == 0 {
		return rruleErr("BYSETPOS needs BYMONTH, BYMONTHDAY or BYDAY")
	}
	for _, n := range r.BySetPos {
		if n == 0 || n < -366 || n > 366 {
			return rruleErr("BYSETPOS must be 1..366 or -1..-366")
		}
	}
	return nil
}

// String returns the canonical form: FREQ;INTERVAL (omitted when 1);
// BYMONTH;BYMONTHDAY;BYDAY;BYSETPOS. ParseRRule(r.String()) equals r for parsed rules.
func (r RRule) String() string {
	var b strings.Builder
	if r.Freq >= Daily && r.Freq <= Yearly {
		b.WriteString("FREQ=" + freqNames[r.Freq])
	}
	if r.Interval > 1 {
		b.WriteString(";INTERVAL=" + strconv.Itoa(r.Interval))
	}
	writeList(&b, "BYMONTH", r.ByMonth, func(m time.Month) string { return strconv.Itoa(int(m)) })
	writeList(&b, "BYMONTHDAY", r.ByMonthDay, strconv.Itoa)
	writeList(&b, "BYDAY", r.ByDay, func(wn WeekdayNum) string {
		s := dayCodes[mod(int(wn.Day), 7)]
		if wn.N != 0 {
			s = strconv.Itoa(wn.N) + s
		}
		return s
	})
	writeList(&b, "BYSETPOS", r.BySetPos, strconv.Itoa)
	return b.String()
}

func writeList[T any](b *strings.Builder, key string, items []T, f func(T) string) {
	for i, it := range items {
		if i == 0 {
			b.WriteString(";" + key + "=")
		} else {
			b.WriteByte(',')
		}
		b.WriteString(f(it))
	}
}

// Next implements Rule. start acts as DTSTART: it aligns INTERVAL, supplies
// the default weekday (WEEKLY), day (MONTHLY/YEARLY) and month (YEARLY) when
// the BY parts leave them open, and no occurrence precedes it. The search
// covers 400 years (one full Gregorian cycle) and never passes year 9999, so
// impossible rules such as FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=30 return false.
func (r RRule) Next(start, after Date) (Date, bool) {
	if r.validate() != nil {
		return Date{}, false
	}
	lower := after.AddDays(1)
	if lower.Before(start) {
		lower = start
	}
	limitYear := min(lower.Year+400, maxYear)
	p := r.period(lower)
	p += mod(r.period(start)-p, r.Interval)
	var buf []Date
	for ; r.periodStart(p).Year <= limitYear; p += r.Interval {
		buf = r.expand(p, start, buf[:0])
		for _, c := range buf {
			if !c.Before(lower) {
				return c, c.Year <= maxYear
			}
		}
	}
	return Date{}, false
}

// period returns the index of the FREQ period containing d.
func (r RRule) period(d Date) int {
	switch r.Freq {
	case Daily:
		return d.days()
	case Weekly:
		return floorDiv(d.days()-4, 7) // weeks start on Monday (1970-01-05)
	case Monthly:
		return monthIndex(d)
	default:
		return d.Year
	}
}

func (r RRule) periodStart(p int) Date {
	switch r.Freq {
	case Daily:
		return fromDays(p)
	case Weekly:
		return fromDays(p*7 + 4)
	case Monthly:
		return clampDay(p, 1)
	default:
		return Date{p, time.January, 1}
	}
}

// expand appends period p's occurrences, in order, to buf.
func (r RRule) expand(p int, start Date, buf []Date) []Date {
	from := len(buf)
	switch r.Freq {
	case Daily:
		d := fromDays(p)
		if r.inMonth(d.Month) && r.matches(d) {
			buf = append(buf, d)
		}
	case Weekly:
		for i := range 7 {
			d := fromDays(p*7 + 4 + i)
			ok := d.Weekday() == start.Weekday()
			if len(r.ByDay) > 0 {
				ok = r.matches(d)
			}
			if ok && r.inMonth(d.Month) {
				buf = append(buf, d)
			}
		}
	case Monthly:
		if d := clampDay(p, 1); r.inMonth(d.Month) {
			buf = r.expandMonth(d.Year, d.Month, start.Day, buf)
		}
	case Yearly:
		months := r.ByMonth
		if len(months) == 0 {
			months = []time.Month{start.Month}
			if len(r.ByDay) > 0 || len(r.ByMonthDay) > 0 {
				months = []time.Month{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
			}
		}
		for _, m := range months {
			buf = r.expandMonth(p, m, start.Day, buf)
		}
	}
	return r.setPos(buf, from)
}

// setPos applies BYSETPOS to one period's dates, buf[from:] (in date order).
func (r RRule) setPos(buf []Date, from int) []Date {
	if len(r.BySetPos) == 0 {
		return buf
	}
	set := slices.Clone(buf[from:])
	out := buf[:from]
	for i, d := range set {
		if slices.ContainsFunc(r.BySetPos, func(n int) bool { return n == i+1 || n == i-len(set) }) {
			out = append(out, d)
		}
	}
	return out
}

func (r RRule) expandMonth(y int, m time.Month, defaultDay int, buf []Date) []Date {
	if len(r.ByMonthDay) == 0 && len(r.ByDay) == 0 {
		if defaultDay <= daysIn(y, m) {
			buf = append(buf, Date{y, m, defaultDay})
		}
		return buf
	}
	for day := 1; day <= daysIn(y, m); day++ {
		if d := (Date{y, m, day}); r.matches(d) {
			buf = append(buf, d)
		}
	}
	return buf
}

func (r RRule) inMonth(m time.Month) bool {
	return len(r.ByMonth) == 0 || slices.Contains(r.ByMonth, m)
}

// matches applies BYMONTHDAY and BYDAY (ordinals count within d's month).
func (r RRule) matches(d Date) bool {
	dim := daysIn(d.Year, d.Month)
	if len(r.ByMonthDay) > 0 && !slices.ContainsFunc(r.ByMonthDay, func(n int) bool {
		return n == d.Day || (n < 0 && dim+1+n == d.Day)
	}) {
		return false
	}
	if len(r.ByDay) == 0 {
		return true
	}
	wd := d.Weekday()
	return slices.ContainsFunc(r.ByDay, func(wn WeekdayNum) bool {
		return wn.Day == wd && (wn.N == 0 ||
			(wn.N > 0 && (d.Day-1)/7+1 == wn.N) ||
			(wn.N < 0 && (dim-d.Day)/7+1 == -wn.N))
	})
}

func floorDiv(a, b int) int {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

func mod(a, b int) int { return a - floorDiv(a, b)*b }

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}
