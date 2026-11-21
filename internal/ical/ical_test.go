package ical

import (
	"flag"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/NexusBetweenVoids/gunkly/internal/schedule"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

var update = flag.Bool("update", false, "rewrite golden files")

func feed() store.FeedData {
	loc, _ := time.LoadLocation("Europe/Amsterdam")
	chore := func(id, name, area, kind, typ, rrule, anchor, due string, n int, unit string) store.FeedChore {
		return store.FeedChore{Chore: store.Chore{ID: id, Name: name, Kind: kind, ScheduleType: typ, RRule: rrule,
			Anchor: anchor, DueDate: due, FirstDue: due, IntervalN: n, IntervalUnit: unit}, AreaName: area}
	}
	return store.FeedData{HouseholdName: "Home; sweet, home", Location: loc, Chores: []store.FeedChore{
		// Calendar rule: every April and October on the 1st Saturday, 12 months.
		chore("c1", "Clean gutters", "Garden", store.KindRecurring, store.ScheduleRRule, "FREQ=YEARLY;BYMONTH=4,10;BYDAY=1SA",
			store.AnchorFromScheduled, "2026-10-03", 0, ""),
		// From completion: next due only.
		chore("c2", "Descale kettle, then\nrinse \\ twice", "", store.KindRecurring, store.ScheduleInterval, "",
			store.AnchorFromCompletion, "2026-11-30", 3, "month"),
		// One-off.
		chore("c3", "Fix the very long-named garden gate hinge that squeaks — ünïcödé 日本語テキスト to test folding", "Garden",
			store.KindOneOff, store.ScheduleInterval, "", store.AnchorFromScheduled, "2026-12-05", 0, ""),
	}}
}

func TestGolden(t *testing.T) {
	got := Render(feed(), "en", schedule.Date{Year: 2026, Month: 11, Day: 21}, time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC))
	path := "testdata/feed.ics"
	if *update {
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("feed differs from %s (run with -update after checking):\n%s", path, got)
	}
	s := string(got)
	for _, line := range strings.Split(strings.TrimSuffix(s, "\r\n"), "\r\n") {
		if len(line) > 75 {
			t.Errorf("line over 75 octets: %q", line)
		}
	}
}

func TestEvents(t *testing.T) {
	ev := Events(feed(), schedule.Date{Year: 2026, Month: 11, Day: 21})
	var dates []string
	for _, e := range ev {
		dates = append(dates, e.UID)
	}
	want := []string{"c1-20261003@gunkly", "c1-20270403@gunkly", "c1-20271002@gunkly", "c2-20261130@gunkly", "c3-20261205@gunkly"}
	if strings.Join(dates, " ") != strings.Join(want, " ") {
		t.Fatalf("UIDs = %v, want %v", dates, want)
	}
}

func TestEscape(t *testing.T) {
	if got := Escape("a\\b;c,d\ne\r\nf\x00g"); got != `a\\b\;c\,d\ne\nfg` {
		t.Fatalf("Escape = %q", got)
	}
}

// FuzzEscapeFold: escaped text has no raw separators or controls, and folding
// keeps lines within 75 octets, valid UTF-8 and the content unchanged.
func FuzzEscapeFold(f *testing.F) {
	for _, s := range []string{"", "a,b;c\\d\ne", strings.Repeat("日本", 60), strings.Repeat("x", 200), "\xff\xfe"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		e := Escape(s)
		for i := 0; i < len(e); i++ {
			c := e[i]
			if c < 0x20 || c == 0x7f {
				t.Fatalf("control char in %q", e)
			}
			if c == '\\' {
				i++
				if i >= len(e) || !strings.ContainsRune(`\;,n`, rune(e[i])) {
					t.Fatalf("bad escape in %q", e)
				}
				continue
			}
			if c == ';' || c == ',' {
				t.Fatalf("unescaped %q in %q", c, e)
			}
		}
		folded := Fold("SUMMARY:" + e)
		if !strings.HasSuffix(folded, "\r\n") {
			t.Fatal("no CRLF")
		}
		lines := strings.Split(strings.TrimSuffix(folded, "\r\n"), "\r\n")
		var b strings.Builder
		for i, l := range lines {
			if len(l) > 75 || !utf8.ValidString(l) {
				t.Fatalf("bad line %q", l)
			}
			if i > 0 {
				if !strings.HasPrefix(l, " ") {
					t.Fatalf("continuation without space: %q", l)
				}
				l = l[1:]
			}
			b.WriteString(l)
		}
		if b.String() != "SUMMARY:"+e {
			t.Fatal("unfolding changed the content")
		}
	})
}
