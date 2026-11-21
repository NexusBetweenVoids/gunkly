// Package ical renders read-only calendar feeds (RFC 5545) with the standard
// library only. Feeds show chore names, areas and due dates — never notes or
// links (THREAT_MODEL "Calendar feed leak").
package ical

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/NexusBetweenVoids/gunkly/internal/i18n"
	"github.com/NexusBetweenVoids/gunkly/internal/schedule"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// ContentType is the feed's media type.
const ContentType = "text/calendar; charset=utf-8"

// horizon is how far calendar-rule chores are expanded.
const horizonDays = 365

// Event is one all-day calendar entry.
type Event struct {
	UID      string
	Date     schedule.Date
	Summary  string
	Category string // "" = none
}

// Events expands a feed's chores: scheduled-anchored (calendar) chores show
// every occurrence from their current due date to 12 months after today;
// completion-anchored and one-off chores show only their next due date.
func Events(f store.FeedData, today schedule.Date) []Event {
	var out []Event
	for _, c := range f.Chores {
		due, err := schedule.ParseDate(c.DueDate)
		if err != nil {
			continue
		}
		dates := []schedule.Date{due}
		if rule, err := store.RuleOf(c.Chore); err == nil && rule != nil && c.Anchor == store.AnchorFromScheduled {
			dates = schedule.Occurrences(rule, due, due, today.AddDays(horizonDays))
		}
		for _, d := range dates {
			out = append(out, Event{UID: c.ID + "-" + compact(d) + "@gunkly", Date: d, Summary: c.Name, Category: c.AreaName})
		}
	}
	return out
}

// Render writes a VCALENDAR named after the household. stamp is DTSTAMP
// (the generation time).
func Render(f store.FeedData, locale string, today schedule.Date, stamp time.Time) []byte {
	var b strings.Builder
	w := func(line string) { b.WriteString(Fold(line)) }
	w("BEGIN:VCALENDAR")
	w("VERSION:2.0")
	w("PRODID:-//Gunkly//Gunkly//EN")
	w("CALSCALE:GREGORIAN")
	w("METHOD:PUBLISH")
	w("X-WR-CALNAME:" + Escape(i18n.T(locale, "ical.calendar_name", map[string]string{"household": f.HouseholdName})))
	ts := stamp.UTC().Format("20060102T150405Z")
	for _, e := range Events(f, today) {
		w("BEGIN:VEVENT")
		w("UID:" + Escape(e.UID))
		w("DTSTAMP:" + ts)
		w("DTSTART;VALUE=DATE:" + compact(e.Date))
		w("DTEND;VALUE=DATE:" + compact(e.Date.AddDays(1)))
		w("SUMMARY:" + Escape(e.Summary))
		if e.Category != "" {
			w("CATEGORIES:" + Escape(e.Category))
		}
		w("TRANSP:TRANSPARENT")
		w("END:VEVENT")
	}
	w("END:VCALENDAR")
	return []byte(b.String())
}

func compact(d schedule.Date) string { return strings.ReplaceAll(d.String(), "-", "") }

// Escape escapes a TEXT value (RFC 5545 3.3.11): backslash, semicolon, comma
// and newlines; other control characters are dropped.
func Escape(s string) string {
	var b strings.Builder
	s = strings.ToValidUTF8(s, "�")
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		i += n
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == ';':
			b.WriteString(`\;`)
		case r == ',':
			b.WriteString(`\,`)
		case r == '\r':
			if i < len(s) && s[i] == '\n' {
				i++
			}
			b.WriteString(`\n`)
		case r == '\n':
			b.WriteString(`\n`)
		case r < 0x20 || r == 0x7f:
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Fold splits a content line into CRLF-terminated lines of at most 75
// octets, continuation lines starting with a space, never inside a UTF-8
// sequence (RFC 5545 3.1).
func Fold(line string) string {
	var b strings.Builder
	limit := 75
	for len(line) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(line[cut]) {
			cut--
		}
		b.WriteString(line[:cut])
		b.WriteString("\r\n ")
		line = line[cut:]
		limit = 74 // the leading space counts
	}
	b.WriteString(line)
	b.WriteString("\r\n")
	return b.String()
}
