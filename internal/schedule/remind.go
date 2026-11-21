package schedule

import (
	"errors"
	"slices"
	"time"
)

// ReminderKind says when a reminder fires relative to the due date.
type ReminderKind int

// Reminder kinds, in increasing urgency.
const (
	Before ReminderKind = iota + 1
	OnDue
	Overdue
)

// maxReminderDays bounds Reminder.Days.
const maxReminderDays = 365

// Reminder is one reminder spec of a chore, at local minute-of-day Minute
// (0..1439) in the household zone:
//   - Before: Days (1..365) days before the due date;
//   - OnDue: on the due date (Days ignored);
//   - Overdue: every Days (1..365) days after the due date, starting at
//     due+Days. Consecutive overdue reminders are never less than 24 hours
//     apart: with Days=1 the one that would land 23 hours after the previous
//     (spring-forward day) is skipped.
type Reminder struct {
	Kind   ReminderKind
	Days   int
	Minute int
}

// Validate reports whether r is a usable reminder spec.
func (r Reminder) Validate() error {
	if r.Minute < 0 || r.Minute >= 1440 {
		return errors.New("schedule: reminder minute must be 0..1439")
	}
	switch r.Kind {
	case OnDue:
		return nil
	case Before, Overdue:
		if r.Days < 1 || r.Days > maxReminderDays {
			return errors.New("schedule: reminder days must be 1..365")
		}
		return nil
	}
	return errors.New("schedule: unknown reminder kind")
}

// Fire is a reminder instant (UTC) and the kind of spec that produced it.
type Fire struct {
	Kind ReminderKind
	At   time.Time
}

// Reminders returns the fires of specs for a chore due on `due` whose instant
// lies in [from, to), sorted by time. Invalid specs are ignored (validate them
// on write). Overdue repeats are produced for as long as the window reaches;
// callers stop asking once the chore is completed.
func Reminders(due Date, specs []Reminder, loc *time.Location, from, to time.Time) []Fire {
	var out []Fire
	add := func(k ReminderKind, at time.Time) {
		if !at.Before(from) && at.Before(to) {
			out = append(out, Fire{k, at})
		}
	}
	for _, s := range specs {
		if s.Validate() != nil {
			continue
		}
		switch s.Kind {
		case Before:
			add(Before, LocalTime(due.AddDays(-s.Days), s.Minute, loc))
		case OnDue:
			add(OnDue, LocalTime(due, s.Minute, loc))
		case Overdue:
			at := func(k int) time.Time { return LocalTime(due.AddDays(k*s.Days), s.Minute, loc) }
			k := max(1, (DateOf(from, loc).days()-due.days()-1)/s.Days)
			for ; due.AddDays(k*s.Days).Year <= maxYear; k++ {
				t := at(k)
				if !t.Before(to) {
					break
				}
				if k > 1 && t.Sub(at(k-1)) < 24*time.Hour {
					continue
				}
				add(Overdue, t)
			}
		}
	}
	slices.SortStableFunc(out, func(a, b Fire) int { return a.At.Compare(b.At) })
	return out
}

// QuietHours is a household's quiet period in local minutes-of-day: [Start,
// End). It wraps midnight when Start > End; Start == End disables it.
type QuietHours struct {
	Start, End int
}

// Defer returns t if it is outside quiet hours in loc, otherwise the instant
// quiet hours end (DST rules as LocalTime). It never returns a time before t.
func (q QuietHours) Defer(t time.Time, loc *time.Location) time.Time {
	if q.Start == q.End || q.Start < 0 || q.End < 0 || q.Start >= 1440 || q.End >= 1440 {
		return t
	}
	l := t.In(loc)
	m := l.Hour()*60 + l.Minute()
	day := DateOf(t, loc)
	switch {
	case q.Start < q.End && m >= q.Start && m < q.End:
	case q.Start > q.End && m >= q.Start:
		day = day.AddDays(1)
	case q.Start > q.End && m < q.End:
	default:
		return t
	}
	if end := LocalTime(day, q.End, loc); end.After(t) {
		return end
	}
	return t // fall-back hour: the first occurrence of End already passed
}

// Coalesce takes one chore's (already deferred) fires and keeps at most one
// per release instant, preferring the most urgent kind. Output is sorted by time.
func Coalesce(fs []Fire) []Fire {
	out := slices.Clone(fs)
	slices.SortStableFunc(out, func(a, b Fire) int {
		if c := a.At.Compare(b.At); c != 0 {
			return c
		}
		return int(b.Kind - a.Kind)
	})
	return slices.CompactFunc(out, func(a, b Fire) bool { return a.At.Equal(b.At) })
}

// CatchUp returns the single most recent fire of a chore missed in
// [lastRun, now) — e.g. after downtime — and false if none was missed. The
// earlier missed fires are deliberately dropped: never send a backlog.
func CatchUp(due Date, specs []Reminder, loc *time.Location, lastRun, now time.Time) (Fire, bool) {
	fs := Coalesce(Reminders(due, specs, loc, lastRun, now))
	if len(fs) == 0 {
		return Fire{}, false
	}
	return fs[len(fs)-1], true
}
