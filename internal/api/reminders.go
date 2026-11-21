package api

import (
	"net/http"
	"slices"
	"strings"

	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/schedule"
	"github.com/NexusBetweenVoids/gunkly/internal/scheduler"
)

// reminderIO is one reminder spec: kind before | on | overdue; days counts
// before the due date or between overdue repeats (at least 1); minute is the
// local time of day in minutes after midnight.
type reminderIO struct {
	Kind   string `json:"kind"`
	Days   int    `json:"days"`
	Minute int    `json:"minute"`
}

var reminderKinds = map[string]schedule.ReminderKind{"before": schedule.Before, "on": schedule.OnDue, "overdue": schedule.Overdue}

func viewReminders(rs []schedule.Reminder) []reminderIO {
	out := make([]reminderIO, 0, len(rs))
	for _, r := range rs {
		for name, k := range reminderKinds {
			if k == r.Kind {
				out = append(out, reminderIO{Kind: name, Days: r.Days, Minute: r.Minute})
			}
		}
	}
	return out
}

func (in reminderIO) spec() (schedule.Reminder, error) {
	k, found := reminderKinds[in.Kind]
	if !found {
		return schedule.Reminder{}, errInvalid
	}
	r := schedule.Reminder{Kind: k, Days: in.Days, Minute: in.Minute}
	if k == schedule.OnDue {
		r.Days = 0
	}
	if r.Validate() != nil {
		return r, errInvalid
	}
	return r, nil
}

func (s *Server) getReminders(w http.ResponseWriter, r *http.Request, c *call) error {
	ctx := r.Context()
	preset, custom, err := s.st.ChoreReminders(ctx, c.actor(), c.hh, r.PathValue("chore"))
	if err != nil {
		return err
	}
	h, err := s.st.Household(ctx, c.actor(), c.hh)
	if err != nil {
		return err
	}
	effective := preset
	if effective == "" {
		effective = h.DefaultReminderPreset
	}
	list := custom
	if effective != scheduler.PresetCustom {
		list = scheduler.Presets[effective]
	}
	return ok(w, map[string]any{"preset": preset, "effectivePreset": effective, "reminders": viewReminders(list)})
}

// putReminders replaces the chore's reminder setup: a preset name, "" for the
// household default, or a custom list (preset "custom", implied when only
// reminders are sent).
func (s *Server) putReminders(w http.ResponseWriter, r *http.Request, c *call) error {
	var in struct {
		Preset    *string       `json:"preset"`
		Reminders *[]reminderIO `json:"reminders"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	preset := ""
	if in.Preset != nil {
		preset = *in.Preset
	}
	if preset == "" && in.Reminders != nil {
		preset = scheduler.PresetCustom
	}
	if preset != scheduler.PresetCustom && (!validPreset(preset) || in.Reminders != nil) {
		return errInvalid
	}
	var specs []schedule.Reminder
	if in.Reminders != nil {
		for _, ri := range *in.Reminders {
			sp, err := ri.spec()
			if err != nil {
				return err
			}
			specs = append(specs, sp)
		}
	}
	ctx := r.Context()
	maxR, err := s.limits.Limit(ctx, limits.RemindersPerChore, c.hh)
	if err != nil {
		return err
	}
	if err := s.st.SetChoreReminders(ctx, c.actor(), c.hh, r.PathValue("chore"), preset, specs, maxR); err != nil {
		return err
	}
	return s.getReminders(w, r, c)
}

// listReminderPresets returns the built-in presets and the household default.
func (s *Server) listReminderPresets(w http.ResponseWriter, r *http.Request, c *call) error {
	h, err := s.st.Household(r.Context(), c.actor(), c.hh)
	if err != nil {
		return err
	}
	type presetView struct {
		Name      string       `json:"name"`
		Reminders []reminderIO `json:"reminders"`
	}
	var names []string
	for n := range scheduler.Presets {
		names = append(names, n)
	}
	slices.SortFunc(names, strings.Compare)
	ps := make([]presetView, 0, len(names))
	for _, n := range names {
		ps = append(ps, presetView{Name: n, Reminders: viewReminders(scheduler.Presets[n])})
	}
	return ok(w, map[string]any{"default": h.DefaultReminderPreset, "presets": ps})
}

func (s *Server) muteChore(w http.ResponseWriter, r *http.Request, c *call) error {
	if err := s.st.SetChoreMuted(r.Context(), c.actor(), c.hh, r.PathValue("chore"), true); err != nil {
		return err
	}
	return noContent(w)
}

func (s *Server) unmuteChore(w http.ResponseWriter, r *http.Request, c *call) error {
	if err := s.st.SetChoreMuted(r.Context(), c.actor(), c.hh, r.PathValue("chore"), false); err != nil {
		return err
	}
	return noContent(w)
}
