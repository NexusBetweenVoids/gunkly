package transfer

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/NexusBetweenVoids/gunkly/internal/schedule"
)

// Reasons an import file is rejected. They are stable codes the UI
// translates; Error.Path says where.
const (
	ReasonNotJSON    = "not_json" // not a single JSON document of the expected shape
	ReasonFormat     = "format"   // wrong or missing format name
	ReasonVersion    = "version"  // version this build cannot read
	ReasonRequired   = "required" // required field missing or empty
	ReasonInvalid    = "invalid"  // value fails the same validation as the normal create paths
	ReasonTooLong    = "too_long" // text longer than allowed
	ReasonLimitChore = "limit_chores"
	ReasonLimitArea  = "limit_areas"
	ReasonLimitRem   = "limit_reminders"
	ReasonLimitComp  = "limit_completions"
)

// Error is a validation failure: Reason is a code above, Path locates the
// offending field, e.g. "chores[3].schedule.rrule".
type Error struct {
	Reason string
	Path   string
}

func (e *Error) Error() string { return "transfer: " + e.Reason + " at " + e.Path }

func bad(reason, path string) error { return &Error{Reason: reason, Path: path} }

// Options carry what validation needs from the rest of the system.
type Options struct {
	// Ceilings from internal/limits. A file over any of them is rejected;
	// the store re-checks the totals against the target household.
	MaxChores, MaxAreas, MaxReminders, MaxCompletions int
	// MaxTotalCompletions caps the history entries in the whole file (0 =
	// unchecked), so a file of many full chores cannot be huge.
	MaxTotalCompletions int
	// ValidPreset reports whether name is a built-in reminder preset.
	ValidPreset func(name string) bool
}

const (
	maxName    = 200
	maxNotes   = 10000
	maxLink    = 2000
	maxNote    = 2000
	maxHHName  = 100
	maxPerson  = 100
	minYear    = 1970
	maxYear    = 9000
	maxAssigns = 100
)

// Parse decodes and validates a household import file. On success the result
// is normalised (names trimmed, areas de-duplicated case-insensitively,
// RRULEs canonical) and safe to hand to the store.
func Parse(data []byte, o Options) (*Household, error) {
	var head struct {
		Format  string `json:"format"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, bad(ReasonNotJSON, "")
	}
	if head.Format != HouseholdFormat {
		return nil, bad(ReasonFormat, "format")
	}
	if head.Version != Version {
		return nil, bad(ReasonVersion, "version")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f Household
	if err := dec.Decode(&f); err != nil {
		return nil, bad(ReasonNotJSON, "")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, bad(ReasonNotJSON, "")
	}
	if err := f.normalise(o); err != nil {
		return nil, err
	}
	return &f, nil
}

// text validates and trims one string field.
func text(path, s string, max int, required bool) (string, error) {
	s = strings.TrimSpace(s)
	switch {
	case !utf8.ValidString(s) || strings.ContainsRune(s, 0):
		return "", bad(ReasonInvalid, path)
	case required && s == "":
		return "", bad(ReasonRequired, path)
	case utf8.RuneCountInString(s) > max:
		return "", bad(ReasonTooLong, path)
	}
	return s, nil
}

// body validates text that keeps its surrounding whitespace (notes).
func body(path, s string, max int) error {
	switch {
	case !utf8.ValidString(s) || strings.ContainsRune(s, 0):
		return bad(ReasonInvalid, path)
	case utf8.RuneCountInString(s) > max:
		return bad(ReasonTooLong, path)
	}
	return nil
}

func inRange(t time.Time) bool { return t.Year() >= minYear && t.Year() <= maxYear }

func optTime(path string, t *time.Time) error {
	if t != nil && !inRange(*t) {
		return bad(ReasonInvalid, path)
	}
	return nil
}

func idx(path string, i int) string { return path + "[" + strconv.Itoa(i) + "]" }

func (f *Household) normalise(o Options) error {
	var err error
	h := &f.Household
	if h.Name, err = text("household.name", h.Name, maxHHName, true); err != nil {
		return err
	}
	if _, err := time.LoadLocation(h.Timezone); err != nil || h.Timezone == "" || h.Timezone == "Local" {
		return bad(ReasonInvalid, "household.timezone")
	}
	if q := h.QuietHours; q != nil && (q.Start < 0 || q.Start > 1439 || q.End < 0 || q.End > 1439 || q.Start == q.End) {
		return bad(ReasonInvalid, "household.quietHours")
	}
	if p := h.DefaultReminderPreset; p != "" && (len(p) > 32 || !o.ValidPreset(p)) {
		return bad(ReasonInvalid, "household.defaultReminderPreset")
	}
	if len(f.Chores) > o.MaxChores {
		return bad(ReasonLimitChore, "chores")
	}
	total := 0
	for i := range f.Chores {
		total += len(f.Chores[i].Completions)
	}
	if o.MaxTotalCompletions > 0 && total > o.MaxTotalCompletions {
		return bad(ReasonLimitComp, "chores")
	}
	areas := make([]Area, 0, len(f.Areas))
	seen := map[string]string{} // lower-case name -> canonical name
	for i, a := range f.Areas {
		name, err := text(idx("areas", i)+".name", a.Name, maxName, true)
		if err != nil {
			return err
		}
		if _, dup := seen[strings.ToLower(name)]; !dup {
			seen[strings.ToLower(name)] = name
			areas = append(areas, Area{Name: name})
		}
		if len(areas) > o.MaxAreas {
			return bad(ReasonLimitArea, "areas")
		}
	}
	f.Areas = areas
	if len(f.Webhooks) > MaxWebhooks {
		return bad(ReasonTooLong, "webhooks")
	}
	// Events and URLs are checked at import time, where an invalid one skips
	// that webhook instead of failing the file.
	for i := range f.Webhooks {
		w := &f.Webhooks[i]
		if w.Name, err = text(idx("webhooks", i)+".name", w.Name, maxName, false); err != nil {
			return err
		}
		if !utf8.ValidString(w.URL) || len(w.URL) > 4096 || len(w.Events) > 64 {
			return bad(ReasonInvalid, idx("webhooks", i))
		}
	}
	for i := range f.Chores {
		if err := f.Chores[i].normalise(idx("chores", i), seen, o); err != nil {
			return err
		}
	}
	return nil
}

func (c *Chore) normalise(p string, areas map[string]string, o Options) error {
	var err error
	if c.Name, err = text(p+".name", c.Name, maxName, true); err != nil {
		return err
	}
	if err := body(p+".notes", c.Notes, maxNotes); err != nil {
		return err
	}
	if len(c.Link) > maxLink || !utf8.ValidString(c.Link) || !validLink(c.Link) {
		return bad(ReasonInvalid, p+".link")
	}
	if c.Area != "" {
		name, err := text(p+".area", c.Area, maxName, false)
		if err != nil {
			return err
		}
		canon, found := areas[strings.ToLower(name)]
		if !found {
			return bad(ReasonInvalid, p+".area")
		}
		c.Area = canon
	}
	if err := c.Schedule.normalise(p, c.Kind); err != nil {
		return err
	}
	if c.DueDate != "" {
		d, err := schedule.ParseDate(c.DueDate)
		if err != nil || d.Year < minYear || d.Year > maxYear {
			return bad(ReasonInvalid, p+".dueDate")
		}
		c.DueDate = d.String()
	}
	for _, t := range []struct {
		path string
		v    *time.Time
	}{{".nextDue", c.NextDue}, {".pausedUntil", c.PausedUntil}, {".archivedAt", c.ArchivedAt}} {
		if err := optTime(p+t.path, t.v); err != nil {
			return err
		}
	}
	if err := c.normaliseReminders(p, o); err != nil {
		return err
	}
	if len(c.Assignees) > maxAssigns {
		return bad(ReasonInvalid, p+".assignees")
	}
	for i, a := range c.Assignees {
		if c.Assignees[i].DisplayName, c.Assignees[i].Username, err = person(idx(p+".assignees", i), a); err != nil {
			return err
		}
	}
	if c.RotationAdvance != "" && c.RotationAdvance != "any" && c.RotationAdvance != "assignee" {
		return bad(ReasonInvalid, p+".rotationAdvance")
	}
	if c.Rotation && len(c.Assignees) < 2 || c.Turn != nil && !c.Rotation {
		return bad(ReasonInvalid, p+".rotation")
	}
	if c.Turn != nil {
		if c.Turn.DisplayName, c.Turn.Username, err = person(p+".turn", *c.Turn); err != nil {
			return err
		}
	}
	if len(c.Completions) > o.MaxCompletions {
		return bad(ReasonLimitComp, p+".completions")
	}
	for i := range c.Completions {
		if err := c.Completions[i].normalise(idx(p+".completions", i)); err != nil {
			return err
		}
	}
	return nil
}

func (c *Chore) normaliseReminders(p string, o Options) error {
	if c.ReminderPreset != "custom" && len(c.Reminders) > 0 {
		return bad(ReasonInvalid, p+".reminders")
	}
	if c.ReminderPreset != "" && c.ReminderPreset != "custom" && (len(c.ReminderPreset) > 32 || !o.ValidPreset(c.ReminderPreset)) {
		return bad(ReasonInvalid, p+".reminderPreset")
	}
	if len(c.Reminders) > o.MaxReminders {
		return bad(ReasonLimitRem, p+".reminders")
	}
	kinds := map[string]schedule.ReminderKind{"before": schedule.Before, "on": schedule.OnDue, "overdue": schedule.Overdue}
	for i, r := range c.Reminders {
		k, found := kinds[r.Kind]
		if found && k == schedule.OnDue {
			c.Reminders[i].Days, r.Days = 0, 0
		}
		if !found || (schedule.Reminder{Kind: k, Days: r.Days, Minute: r.Minute}).Validate() != nil {
			return bad(ReasonInvalid, idx(p+".reminders", i))
		}
	}
	return nil
}

func (s *Schedule) normalise(chore, kind string) error {
	p := chore + ".schedule"
	first, err := schedule.ParseDate(s.FirstDue)
	if err != nil || first.Year < minYear || first.Year > maxYear {
		return bad(ReasonInvalid, p+".firstDue")
	}
	s.FirstDue = first.String()
	switch kind {
	case "oneoff":
		if s.Interval != nil || s.RRule != "" {
			return bad(ReasonInvalid, p)
		}
		s.Type, s.Anchor = "interval", "from_completion"
		return nil
	case "recurring":
	default:
		return bad(ReasonInvalid, chore+".kind")
	}
	switch s.Type {
	case "interval":
		if s.Interval == nil || s.RRule != "" || s.Interval.Every < 1 || s.Interval.Every > schedule.MaxInterval ||
			(s.Interval.Unit != "day" && s.Interval.Unit != "week" && s.Interval.Unit != "month") {
			return bad(ReasonInvalid, p+".interval")
		}
		if s.Anchor == "" {
			s.Anchor = "from_completion"
		}
	case "rrule":
		rule, err := schedule.ParseRRule(s.RRule)
		if err != nil || s.Interval != nil {
			return bad(ReasonInvalid, p+".rrule")
		}
		s.RRule = rule.String()
		if s.Anchor == "" {
			s.Anchor = "from_scheduled"
		}
	default:
		return bad(ReasonInvalid, p+".type")
	}
	if s.Anchor != "from_completion" && s.Anchor != "from_scheduled" {
		return bad(ReasonInvalid, p+".anchor")
	}
	return nil
}

func (c *Completion) normalise(p string) error {
	if c.CompletedAt.IsZero() || !inRange(c.CompletedAt) {
		return bad(ReasonInvalid, p+".completedAt")
	}
	if err := body(p+".note", c.Note, maxNote); err != nil {
		return err
	}
	switch c.Source {
	case "app", "mark_done_link", "api_token", "oauth", "import":
	default:
		return bad(ReasonInvalid, p+".source")
	}
	if c.By != nil {
		name, user, err := person(p+".by", *c.By)
		if err != nil {
			return err
		}
		c.By = &Person{DisplayName: name, Username: user}
	}
	return nil
}

// person validates a display name / username pair. Both may be empty (a
// hand-written file).
func person(p string, in Person) (name, user string, err error) {
	if name, err = text(p+".displayName", in.DisplayName, maxPerson, false); err != nil {
		return "", "", err
	}
	if user, err = text(p+".username", in.Username, maxPerson, false); err != nil {
		return "", "", err
	}
	return name, user, nil
}

func validLink(s string) bool {
	if s == "" {
		return true
	}
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
