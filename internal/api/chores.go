package api

import (
	"net/http"
	"net/url"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/schedule"
	"github.com/NexusBetweenVoids/gunkly/internal/scheduler"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// Due status values of a chore (relative to the household's today).
const (
	statusOverdue  = "overdue"
	statusToday    = "today"
	statusThisWeek = "thisWeek"
	statusLater    = "later"
	statusNone     = "none"     // no due date (series ended)
	statusPaused   = "paused"   // paused indefinitely or until a future date
	statusArchived = "archived" // archived (incl. completed one-offs)
)

type intervalIO struct {
	Every int    `json:"every"`
	Unit  string `json:"unit"`
}

// scheduleIO is the schedule of a chore, in requests and responses.
type scheduleIO struct {
	Type     string      `json:"type"` // interval | rrule
	Interval *intervalIO `json:"interval,omitempty"`
	RRule    string      `json:"rrule,omitempty"`
	Anchor   string      `json:"anchor,omitempty"` // from_completion | from_scheduled
	FirstDue string      `json:"firstDue"`         // local date YYYY-MM-DD
}

type choreView struct {
	ID                 string     `json:"id"`
	AreaID             *string    `json:"areaId"`
	Name               string     `json:"name"`
	Notes              string     `json:"notes"`
	Link               string     `json:"link"`
	Kind               string     `json:"kind"`
	Schedule           scheduleIO `json:"schedule"`
	DueDate            string     `json:"dueDate"`
	NextDue            *time.Time `json:"nextDue"`
	Status             string     `json:"status"`
	PausedUntil        *time.Time `json:"pausedUntil"`
	PausedIndefinitely bool       `json:"pausedIndefinitely"`
	ArchivedAt         *time.Time `json:"archivedAt"`
	ReminderPreset     string     `json:"reminderPreset"`
	Assignees          []string   `json:"assignees"`
	Muted              bool       `json:"muted"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
}

// viewCtx is what turns stored chores into views for one caller.
type viewCtx struct {
	now       time.Time
	loc       *time.Location
	today     schedule.Date
	weekStart int
	muted     map[string]bool
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// defaultWeekStart is the first weekday (0 = Sunday) for locales where the
// user has not chosen one.
func defaultWeekStart(locale string) int {
	switch locale {
	case "en-US", "en-CA", "ja", "ja-JP":
		return 0
	}
	return 1
}

func (s *Server) newViewCtx(r *http.Request, c *call, tz string, withMutes bool) (viewCtx, error) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return viewCtx{}, err
	}
	now := s.now()
	v := viewCtx{now: now, loc: loc, today: schedule.DateOf(now, loc), weekStart: defaultWeekStart(c.user.Locale), muted: map[string]bool{}}
	if c.user.WeekStart != nil {
		v.weekStart = *c.user.WeekStart
	}
	if withMutes {
		ids, err := s.st.MutedChores(r.Context(), c.actor(), c.hh)
		if err != nil {
			return viewCtx{}, err
		}
		for _, id := range ids {
			v.muted[id] = true
		}
	}
	return v, nil
}

// isPaused reports whether reminders for the chore are suspended at now.
func isPaused(c store.Chore, now time.Time) bool {
	return c.PausedIndefinitely || c.PausedUntil.After(now)
}

// dueStatus buckets a chore: overdue (due before today), today, thisWeek
// (after today, up to the end of the user's week) or later.
func dueStatus(c store.Chore, now time.Time, today schedule.Date, weekStart int) string {
	if !c.ArchivedAt.IsZero() {
		return statusArchived
	}
	if isPaused(c, now) {
		return statusPaused
	}
	due, err := schedule.ParseDate(c.DueDate)
	if c.NextDue.IsZero() || err != nil {
		return statusNone
	}
	weekEnd := today.AddDays(6 - (int(today.Weekday())-weekStart+7)%7)
	switch {
	case due.Before(today):
		return statusOverdue
	case due.Compare(today) == 0:
		return statusToday
	case !due.After(weekEnd):
		return statusThisWeek
	}
	return statusLater
}

func (v viewCtx) view(c store.Chore) choreView {
	out := choreView{
		ID: c.ID, Name: c.Name, Notes: c.Notes, Link: c.Link, Kind: c.Kind, DueDate: c.DueDate,
		NextDue: timePtr(c.NextDue), Status: dueStatus(c, v.now, v.today, v.weekStart),
		PausedUntil: timePtr(c.PausedUntil), PausedIndefinitely: c.PausedIndefinitely, ArchivedAt: timePtr(c.ArchivedAt),
		ReminderPreset: c.ReminderPreset, Assignees: c.Assignees, Muted: v.muted[c.ID], CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
		Schedule: scheduleIO{Type: c.ScheduleType, Anchor: c.Anchor, FirstDue: c.FirstDue},
	}
	if out.Assignees == nil {
		out.Assignees = []string{}
	}
	if c.AreaID != "" {
		out.AreaID = &c.AreaID
	}
	if c.Kind == store.KindRecurring {
		if c.ScheduleType == store.ScheduleRRule {
			out.Schedule.RRule = c.RRule
		} else {
			out.Schedule.Interval = &intervalIO{Every: c.IntervalN, Unit: c.IntervalUnit}
		}
	}
	return out
}

// choreInput is the body of create and update: the simple and the advanced
// editor send the same document.
type choreInput struct {
	Name           string     `json:"name"`
	Notes          string     `json:"notes"`
	Link           string     `json:"link"`
	AreaID         *string    `json:"areaId"`
	Kind           string     `json:"kind"` // recurring (default) | oneoff
	Schedule       scheduleIO `json:"schedule"`
	Assignees      []string   `json:"assignees"`
	ReminderPreset *string    `json:"reminderPreset"`
}

// validPreset reports whether name is "" (household default) or a built-in preset.
func validPreset(name string) bool {
	if name == "" {
		return true
	}
	_, ok := scheduler.Presets[name]
	return ok
}

func validLink(s string) bool {
	if s == "" {
		return true
	}
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// build validates the document and turns it into the store input. existing is
// the chore being updated (nil on create): its pause state, reminder preset and
// current occurrence are kept unless the first due date or kind changed.
func (in choreInput) build(loc *time.Location, existing *store.Chore) (store.ChoreInput, error) {
	out := store.ChoreInput{Name: in.Name, Notes: in.Notes, Link: in.Link, Kind: in.Kind, Assignees: in.Assignees}
	if out.Kind == "" {
		out.Kind = store.KindRecurring
	}
	if out.Kind != store.KindRecurring && out.Kind != store.KindOneOff {
		return out, errInvalid
	}
	if n := utf8.RuneCountInString(in.Name); n < 1 || n > 200 || utf8.RuneCountInString(in.Notes) > 10000 || len(in.Link) > 2000 || !validLink(in.Link) {
		return out, errInvalid
	}
	if len(in.Assignees) > 100 {
		return out, errInvalid
	}
	if in.AreaID != nil {
		out.AreaID = *in.AreaID
	}
	first, err := schedule.ParseDate(in.Schedule.FirstDue)
	if err != nil || first.Year < 1970 || first.Year > 9000 {
		return out, errInvalid
	}
	out.FirstDue = first.String()
	sc := in.Schedule
	switch {
	case out.Kind == store.KindOneOff:
		out.ScheduleType, out.Anchor = store.ScheduleInterval, store.AnchorFromCompletion
	case sc.Type == store.ScheduleInterval:
		if sc.Interval == nil || sc.RRule != "" || sc.Interval.Every < 1 || sc.Interval.Every > schedule.MaxInterval ||
			!slices.Contains([]string{"day", "week", "month"}, sc.Interval.Unit) {
			return out, errInvalid
		}
		out.ScheduleType, out.IntervalN, out.IntervalUnit, out.Anchor = store.ScheduleInterval, sc.Interval.Every, sc.Interval.Unit, store.AnchorFromCompletion
	case sc.Type == store.ScheduleRRule:
		rule, err := schedule.ParseRRule(sc.RRule)
		if err != nil || sc.Interval != nil {
			return out, errInvalid
		}
		out.ScheduleType, out.RRule, out.Anchor = store.ScheduleRRule, rule.String(), store.AnchorFromScheduled
	default:
		return out, errInvalid
	}
	if sc.Anchor != "" && out.Kind == store.KindRecurring {
		if sc.Anchor != store.AnchorFromCompletion && sc.Anchor != store.AnchorFromScheduled {
			return out, errInvalid
		}
		out.Anchor = sc.Anchor
	}

	switch {
	case in.ReminderPreset != nil:
		if !validPreset(*in.ReminderPreset) {
			return out, errInvalid
		}
		out.ReminderPreset = *in.ReminderPreset
	case existing != nil:
		out.ReminderPreset = existing.ReminderPreset // possibly "custom"
	}

	if existing != nil && existing.FirstDue == out.FirstDue && existing.Kind == out.Kind && !existing.NextDue.IsZero() {
		out.DueDate, out.NextDue = existing.DueDate, existing.NextDue
	} else {
		due := first
		if out.Kind == store.KindRecurring {
			rule, err := store.RuleOf(store.Chore{Kind: out.Kind, ScheduleType: out.ScheduleType, IntervalN: out.IntervalN,
				IntervalUnit: out.IntervalUnit, RRule: out.RRule, FirstDue: out.FirstDue})
			if err != nil {
				return out, errInvalid
			}
			var ok bool
			if due, ok = rule.Next(first, first.AddDays(-1)); !ok {
				return out, errInvalid
			}
		}
		out.DueDate, out.NextDue = due.String(), store.OccurrenceInstant(due, loc)
	}
	if existing != nil {
		out.PausedUntil, out.PausedIndefinitely = existing.PausedUntil, existing.PausedIndefinitely
	}
	return out, nil
}

func (s *Server) listChores(w http.ResponseWriter, r *http.Request, c *call) error {
	ctx := r.Context()
	h, err := s.st.Household(ctx, c.actor(), c.hh)
	if err != nil {
		return err
	}
	vc, err := s.newViewCtx(r, c, h.Timezone, true)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	archived, mine, area := q.Get("archived") == "1", q.Get("mine") == "1", q.Get("area")
	cs, err := s.st.ListChores(ctx, c.actor(), c.hh, archived)
	if err != nil {
		return err
	}
	out := make([]choreView, 0, len(cs))
	for _, ch := range cs {
		switch {
		case ch.ArchivedAt.IsZero() == archived:
		case area != "" && ch.AreaID != area:
		case mine && len(ch.Assignees) > 0 && !slices.Contains(ch.Assignees, c.user.ID):
		default:
			out = append(out, vc.view(ch))
		}
	}
	return ok(w, out)
}

// choreView loads one chore as the caller sees it.
func (s *Server) loadChoreView(r *http.Request, c *call, ch store.Chore, tz string) (choreView, error) {
	vc, err := s.newViewCtx(r, c, tz, true)
	if err != nil {
		return choreView{}, err
	}
	return vc.view(ch), nil
}

func (s *Server) getChore(w http.ResponseWriter, r *http.Request, c *call) error {
	ctx := r.Context()
	ch, err := s.st.Chore(ctx, c.actor(), c.hh, r.PathValue("chore"))
	if err != nil {
		return err
	}
	return s.respondChore(w, r, c, ch)
}

func (s *Server) createChore(w http.ResponseWriter, r *http.Request, c *call) error {
	var in choreInput
	if err := decode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	h, err := s.st.Household(ctx, c.actor(), c.hh)
	if err != nil {
		return err
	}
	loc, err := time.LoadLocation(h.Timezone)
	if err != nil {
		return err
	}
	si, err := in.build(loc, nil)
	if err != nil {
		return err
	}
	if si.MaxChores, err = s.limits.Limit(ctx, limits.ChoresPerHousehold, c.hh); err != nil {
		return err
	}
	ch, err := s.st.CreateChore(ctx, c.actor(), c.hh, si)
	if err != nil {
		return err
	}
	v, err := s.loadChoreView(r, c, ch, h.Timezone)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, v)
	return nil
}

func (s *Server) updateChore(w http.ResponseWriter, r *http.Request, c *call) error {
	var in choreInput
	if err := decode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	id := r.PathValue("chore")
	old, err := s.st.Chore(ctx, c.actor(), c.hh, id)
	if err != nil {
		return err
	}
	h, err := s.st.Household(ctx, c.actor(), c.hh)
	if err != nil {
		return err
	}
	loc, err := time.LoadLocation(h.Timezone)
	if err != nil {
		return err
	}
	si, err := in.build(loc, &old)
	if err != nil {
		return err
	}
	ch, err := s.st.UpdateChore(ctx, c.actor(), c.hh, id, si)
	if err != nil {
		return err
	}
	return s.respondChore(w, r, c, ch)
}

func (s *Server) deleteChore(w http.ResponseWriter, r *http.Request, c *call) error {
	id := r.PathValue("chore")
	if err := s.st.DeleteChore(r.Context(), c.actor(), c.hh, id); err != nil {
		return err
	}
	s.audit(r.Context(), c, c.user.ID, store.AuditChoreDelete, c.hh, map[string]string{"chore": id})
	return noContent(w)
}

func (s *Server) archiveChore(w http.ResponseWriter, r *http.Request, c *call) error {
	if err := s.st.ArchiveChore(r.Context(), c.actor(), c.hh, r.PathValue("chore")); err != nil {
		return err
	}
	return noContent(w)
}

func (s *Server) unarchiveChore(w http.ResponseWriter, r *http.Request, c *call) error {
	ch, err := s.st.UnarchiveChore(r.Context(), c.actor(), c.hh, r.PathValue("chore"))
	if err != nil {
		return err
	}
	return s.respondChore(w, r, c, ch)
}

// respondChore writes the chore as the caller sees it.
func (s *Server) respondChore(w http.ResponseWriter, r *http.Request, c *call, ch store.Chore) error {
	h, err := s.st.Household(r.Context(), c.actor(), c.hh)
	if err != nil {
		return err
	}
	v, err := s.loadChoreView(r, c, ch, h.Timezone)
	if err != nil {
		return err
	}
	return ok(w, v)
}
