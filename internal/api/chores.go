package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
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
	Assignees          []string   `json:"assignees"` // in rotation order
	Rotation           bool       `json:"rotation"`  // "Take turns": the assignees take turns
	AdvanceOn          string     `json:"advanceOn"` // any | assignee: who completing passes the turn on
	TurnUserID         *string    `json:"turnUserId"`
	TurnName           *string    `json:"turnName"` // display name of the member whose turn it is; null unless rotation
	// ReminderTime is when the chore's due-day reminder goes off ("HH:MM",
	// household time zone), from its own reminders or its (default) preset;
	// null when it has none.
	ReminderTime *string   `json:"reminderTime"`
	Muted        bool      `json:"muted"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
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

func (s *Server) newViewCtx(ctx context.Context, c *call, tz string, withMutes bool) (viewCtx, error) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return viewCtx{}, err
	}
	now := s.now()
	v := viewCtx{now: now, loc: loc, today: schedule.DateOf(now, personalZone(c, loc)), weekStart: defaultWeekStart(c.user.Locale), muted: map[string]bool{}}
	if c.user.WeekStart != nil {
		v.weekStart = *c.user.WeekStart
	}
	if withMutes {
		ids, err := s.st.MutedChores(ctx, c.actor(), c.hh)
		if err != nil {
			return viewCtx{}, err
		}
		for _, id := range ids {
			v.muted[id] = true
		}
	}
	return v, nil
}

// personalZone is the zone "today" is read in: the signed-in user's own time zone
// when set, else the household's (loc). API tokens and MCP always use the
// household's, so integrations that work out dates themselves (Home Assistant)
// stay consistent. Due dates themselves are always household calendar dates.
func personalZone(c *call, loc *time.Location) *time.Location {
	if c.tok != nil || c.sess.ID == "" || c.user.TimeZone == "" {
		return loc
	}
	if z, err := time.LoadLocation(c.user.TimeZone); err == nil {
		return z
	}
	return loc
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

// onDueMinute is the time of the chore's due-day reminder in minutes after
// local midnight, or -1 when it has none.
func onDueMinute(c store.Chore) int {
	if c.EffectivePreset == scheduler.PresetCustom {
		return c.OnDueMinute
	}
	for _, r := range scheduler.Presets[c.EffectivePreset] {
		if r.Kind == schedule.OnDue {
			return r.Minute
		}
	}
	return -1
}

func (v viewCtx) view(c store.Chore) choreView {
	out := choreView{
		ID: c.ID, Name: c.Name, Notes: c.Notes, Link: c.Link, Kind: c.Kind, DueDate: c.DueDate,
		NextDue: timePtr(c.NextDue), Status: dueStatus(c, v.now, v.today, v.weekStart),
		PausedUntil: timePtr(c.PausedUntil), PausedIndefinitely: c.PausedIndefinitely, ArchivedAt: timePtr(c.ArchivedAt),
		ReminderPreset: c.ReminderPreset, Assignees: c.Assignees, Rotation: c.Rotation, AdvanceOn: c.RotationAdvance, Muted: v.muted[c.ID], CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
		Schedule: scheduleIO{Type: c.ScheduleType, Anchor: c.Anchor, FirstDue: c.FirstDue},
	}
	if out.Assignees == nil {
		out.Assignees = []string{}
	}
	if c.AreaID != "" {
		out.AreaID = &c.AreaID
	}
	if c.TurnUserID != "" {
		out.TurnUserID = &c.TurnUserID
		if c.TurnName != "" {
			out.TurnName = &c.TurnName
		}
	}
	if m := onDueMinute(c); m >= 0 {
		hm := fmt.Sprintf("%02d:%02d", m/60, m%60)
		out.ReminderTime = &hm
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
	Name           string           `json:"name"`
	Notes          string           `json:"notes"`
	Link           string           `json:"link"`
	AreaID         optional[string] `json:"areaId"` // absent on update = keep the current area
	Kind           string           `json:"kind"`   // recurring (default) | oneoff
	Schedule       scheduleIO       `json:"schedule"`
	Assignees      []string         `json:"assignees"`
	Rotation       *bool            `json:"rotation"` // absent on update = keep
	AdvanceOn      *string          `json:"advanceOn"`
	ReminderPreset *string          `json:"reminderPreset"`
	// ReminderTime ("HH:MM", household time zone) sets the time of day of all
	// the chore's reminders; absent or "" leaves the reminders as they are.
	ReminderTime *string `json:"reminderTime"`
}

var reminderTimeRE = regexp.MustCompile(`^([01][0-9]|2[0-3]):([0-5][0-9])$`)

// parseReminderTime turns "HH:MM" into minutes after midnight.
func parseReminderTime(s string) (int, error) {
	m := reminderTimeRE.FindStringSubmatch(s)
	if m == nil {
		return 0, errInvalid
	}
	h, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	return h*60 + mi, nil
}

// reminderMinute is the requested reminder time, if any.
func (in choreInput) reminderMinute() (int, bool, error) {
	if in.ReminderTime == nil || *in.ReminderTime == "" {
		return 0, false, nil
	}
	m, err := parseReminderTime(*in.ReminderTime)
	if err != nil {
		return 0, false, invalidAt("reminderTime", "must be a time HH:MM")
	}
	return m, true, nil
}

// applyReminderTime turns the requested reminder time into explicit reminder
// rows on si: the chore's effective preset (or its own custom reminders) with
// every time of day replaced. The chore is then stored as "custom"; the editor
// shows it as the preset again while the rows still match it apart from the
// time. Nothing changes when there is no time or no reminders to time.
func (s *Server) applyReminderTime(ctx context.Context, a store.Actor, hh string, in choreInput, si *store.ChoreInput, existingID, defaultPreset string) error {
	minute, set, err := in.reminderMinute()
	if err != nil || !set {
		return err
	}
	var base []schedule.Reminder
	switch preset := si.ReminderPreset; {
	case preset == scheduler.PresetCustom && existingID != "":
		if _, base, err = s.st.ChoreReminders(ctx, a, hh, existingID); err != nil {
			return err
		}
	case preset == "":
		base = scheduler.Presets[defaultPreset]
	case preset != scheduler.PresetCustom:
		base = scheduler.Presets[preset]
	}
	if len(base) == 0 {
		return nil
	}
	maxR, err := s.limits.Limit(ctx, limits.RemindersPerChore, hh)
	if err != nil {
		return err
	}
	if len(base) > maxR {
		return store.ErrLimit
	}
	si.Reminders = make([]schedule.Reminder, len(base))
	for i, r := range base {
		r.Minute = minute
		si.Reminders[i] = r
	}
	si.ReminderPreset = scheduler.PresetCustom
	return nil
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
		return out, invalidAt("kind", "must be recurring or oneoff")
	}
	if n := utf8.RuneCountInString(in.Name); n < 1 || n > 200 {
		return out, invalidAt("name", "must be 1 to 200 characters")
	}
	if utf8.RuneCountInString(in.Notes) > 10000 {
		return out, invalidAt("notes", "must be at most 10000 characters")
	}
	if len(in.Link) > 2000 || !validLink(in.Link) {
		return out, invalidAt("link", "must be an http or https address of at most 2000 characters")
	}
	if len(in.Assignees) > 100 {
		return out, invalidAt("assignees", "at most 100")
	}
	out.RotationAdvance = store.AdvanceAny
	if existing != nil && existing.RotationAdvance != "" {
		out.RotationAdvance = existing.RotationAdvance
	}
	if in.AdvanceOn != nil {
		if *in.AdvanceOn != store.AdvanceAny && *in.AdvanceOn != store.AdvanceAssignee {
			return out, invalidAt("advanceOn", "must be any or assignee")
		}
		out.RotationAdvance = *in.AdvanceOn
	}
	// Turning rotation on explicitly needs 2+ assignees and a recurring chore;
	// one kept from before simply stops when they no longer hold.
	out.Rotation = existing != nil && existing.Rotation
	if in.Rotation != nil {
		out.Rotation = *in.Rotation
		if out.Rotation && (len(in.Assignees) < 2 || out.Kind != store.KindRecurring) {
			return out, invalidAt("rotation", "taking turns needs 2 or more assignees and a recurring chore")
		}
	}
	if len(in.Assignees) < 2 || out.Kind != store.KindRecurring {
		out.Rotation = false
	}
	switch {
	case in.AreaID.set && in.AreaID.Value != nil:
		out.AreaID = *in.AreaID.Value
	case !in.AreaID.set && existing != nil:
		out.AreaID = existing.AreaID
	}
	first, err := schedule.ParseDate(in.Schedule.FirstDue)
	if err != nil || first.Year < 1970 || first.Year > 9000 {
		return out, invalidAt("schedule.firstDue", "must be a date YYYY-MM-DD between 1970 and 9000")
	}
	out.FirstDue = first.String()
	sc := in.Schedule
	switch {
	case out.Kind == store.KindOneOff:
		out.ScheduleType, out.Anchor = store.ScheduleInterval, store.AnchorFromCompletion
	case sc.Type == store.ScheduleInterval:
		if sc.Interval == nil || sc.RRule != "" || sc.Interval.Every < 1 || sc.Interval.Every > schedule.MaxInterval ||
			!slices.Contains([]string{"day", "week", "month"}, sc.Interval.Unit) {
			return out, invalidAt("schedule.interval", fmt.Sprintf("needs every 1..%d and unit day, week or month (and no rrule)", schedule.MaxInterval))
		}
		out.ScheduleType, out.IntervalN, out.IntervalUnit, out.Anchor = store.ScheduleInterval, sc.Interval.Every, sc.Interval.Unit, store.AnchorFromCompletion
	case sc.Type == store.ScheduleRRule:
		rule, err := schedule.ParseRRule(sc.RRule)
		if err != nil {
			return out, invalidAt("schedule.rrule", strings.TrimPrefix(err.Error(), schedule.ErrInvalidRRule.Error()+": "))
		}
		if sc.Interval != nil {
			return out, invalidAt("schedule.interval", "not allowed with an rrule")
		}
		out.ScheduleType, out.RRule, out.Anchor = store.ScheduleRRule, rule.String(), store.AnchorFromScheduled
	default:
		return out, invalidAt("schedule.type", "must be interval or rrule")
	}
	if sc.Anchor != "" && out.Kind == store.KindRecurring {
		if sc.Anchor != store.AnchorFromCompletion && sc.Anchor != store.AnchorFromScheduled {
			return out, invalidAt("schedule.anchor", "must be from_completion or from_scheduled")
		}
		out.Anchor = sc.Anchor
	}

	if _, _, err := in.reminderMinute(); err != nil {
		return out, err
	}
	switch {
	case in.ReminderPreset != nil:
		if !validPreset(*in.ReminderPreset) {
			return out, invalidAt("reminderPreset", "must be empty (household default), gentle, nag or none")
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
				return out, invalidAt("schedule", "the schedule has no occurrence on or after the first due date")
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
	vc, err := s.newViewCtx(ctx, c, h.Timezone, true)
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
		case mine && !isMine(ch, c.user.ID):
		default:
			out = append(out, vc.view(ch))
		}
	}
	return ok(w, out)
}

// isMine reports whether the chore is the user's to do: unassigned, assigned
// to them, or (when the assignees take turns) their turn.
func isMine(c store.Chore, userID string) bool {
	if c.Rotation {
		return c.TurnUserID == userID
	}
	return len(c.Assignees) == 0 || slices.Contains(c.Assignees, userID)
}

// choreView loads one chore as the caller sees it.
func (s *Server) loadChoreView(r *http.Request, c *call, ch store.Chore, tz string) (choreView, error) {
	vc, err := s.newViewCtx(r.Context(), c, tz, true)
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
	if err := s.applyReminderTime(ctx, c.actor(), c.hh, in, &si, "", h.DefaultReminderPreset); err != nil {
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
	if err := s.applyReminderTime(ctx, c.actor(), c.hh, in, &si, id, h.DefaultReminderPreset); err != nil {
		return err
	}
	ch, err := s.st.UpdateChore(ctx, c.actor(), c.hh, id, si)
	if err != nil {
		return err
	}
	return s.respondChore(w, r, c, ch)
}

// setChoreTurn is PATCH on a chore: today only turnUserId, "It's <name>'s turn
// now" for a chore whose assignees take turns.
func (s *Server) setChoreTurn(w http.ResponseWriter, r *http.Request, c *call) error {
	var in struct {
		TurnUserID string `json:"turnUserId"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if in.TurnUserID == "" {
		return errInvalid
	}
	ch, err := s.st.SetChoreTurn(r.Context(), c.actor(), c.hh, r.PathValue("chore"), in.TurnUserID)
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
