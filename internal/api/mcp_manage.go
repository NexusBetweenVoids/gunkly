package api

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// Tools that edit and archive chores and manage areas (ADR 0015). They reuse
// the REST validation (choreInput.build, applyReminderTime) and the same store
// methods, so the owner's role, the limits and the household scoping are the
// ones the REST routes enforce.

type updateIn struct {
	Household      string    `json:"household,omitempty" jsonschema:"household id or name; may be omitted when the token has exactly one household"`
	Chore          string    `json:"chore" jsonschema:"chore id, or its exact name (case-insensitive)"`
	Name           *string   `json:"name,omitempty" jsonschema:"new name, 1 to 200 characters"`
	Notes          *string   `json:"notes,omitempty" jsonschema:"new notes (at most 10000 characters); an empty string clears them"`
	Link           *string   `json:"link,omitempty" jsonschema:"new http or https link; an empty string clears it"`
	Area           *string   `json:"area,omitempty" jsonschema:"area id or exact name (see list_areas); an empty string removes the chore from its area"`
	Assignees      *[]string `json:"assignees,omitempty" jsonschema:"replaces the people responsible: 'me' for the token owner or member ids; an empty list means the whole household"`
	Repeat         *string   `json:"repeat,omitempty" jsonschema:"replaces the schedule: 'every 2 weeks', 'every day', 'monthly', 'every year', an RRULE such as 'FREQ=WEEKLY;BYDAY=MO,TH', or 'once' to make it a one-off chore"`
	FirstDue       *string   `json:"first_due,omitempty" jsonschema:"first due date YYYY-MM-DD in the household's time zone; changing it moves the chore's current due date"`
	Anchor         *string   `json:"anchor,omitempty" jsonschema:"from_completion (next due counts from when it was done) or from_scheduled (next due follows the schedule); recurring chores only"`
	ReminderPreset *string   `json:"reminder_preset,omitempty" jsonschema:"built-in reminder preset name, or an empty string for the household default"`
	RemindAt       *string   `json:"remind_at,omitempty" jsonschema:"time of day for all the chore's reminders, HH:MM (24-hour) in the household's time zone"`
	TakeTurns      *bool     `json:"take_turns,omitempty" jsonschema:"true: the assignees take turns (needs 2 or more assignees and a recurring chore); false: they do not"`
	AdvanceOn      *string   `json:"advance_on,omitempty" jsonschema:"for take-turns chores, who passes the turn on by completing: 'any' (whoever completes) or 'assignee' (only the person whose turn it is)"`
}

type listAreasIn struct {
	Household string `json:"household,omitempty" jsonschema:"household id or name; omit to include every household of this token"`
}

type createAreaIn struct {
	Household string `json:"household,omitempty" jsonschema:"household id or name; may be omitted when the token has exactly one household"`
	Name      string `json:"name" jsonschema:"area name, 1 to 200 characters, unique in the household (case-insensitive)"`
}

type renameAreaIn struct {
	Household string `json:"household,omitempty" jsonschema:"household id or name; may be omitted when the token has exactly one household"`
	Area      string `json:"area" jsonschema:"area id, or its exact name (case-insensitive)"`
	Name      string `json:"name" jsonschema:"the new name, 1 to 200 characters"`
}

// area finds an area of the household by id or exact, case-insensitive name.
func (m mcpCall) area(ctx context.Context, h hhCall, ref string) (store.Area, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return store.Area{}, toolErrf("area is required (an id or the exact name; see list_areas)")
	}
	as, err := m.s.st.ListAreas(ctx, h.actor(), h.hh)
	if err != nil {
		return store.Area{}, err
	}
	for _, a := range as {
		if a.ID == ref || strings.EqualFold(a.Name, ref) {
			return a, nil
		}
	}
	return store.Area{}, toolErrf("no area %q in household %q (use list_areas)", ref, h.name)
}

// areasOn reports the household's areas switch; the tools refuse to create or
// assign areas while the owner has the feature off (nothing would show them).
func (m mcpCall) areasOn(ctx context.Context, h hhCall) (bool, error) {
	hr, err := m.s.st.Household(ctx, h.actor(), h.hh)
	return hr.AreasEnabled, err
}

func (m mcpCall) areaFailure(err error) error {
	if errors.Is(err, store.ErrConflict) {
		return toolErrf("an area with that name already exists in this household")
	}
	return err
}

func (m mcpCall) listAreas(ctx context.Context, in listAreasIn) (any, error) {
	hs, err := m.households(ctx, in.Household)
	if err != nil {
		return nil, err
	}
	type areaOut struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	type hhOut struct {
		Household    string    `json:"household"`
		AreasEnabled bool      `json:"areasEnabled"`
		Areas        []areaOut `json:"areas"`
	}
	out := []hhOut{}
	for _, h := range hs {
		on, err := m.areasOn(ctx, h)
		if err != nil {
			return nil, err
		}
		as, err := m.s.st.ListAreas(ctx, h.actor(), h.hh)
		if err != nil {
			return nil, err
		}
		o := hhOut{Household: h.name, AreasEnabled: on, Areas: []areaOut{}}
		for _, a := range as {
			o.Areas = append(o.Areas, areaOut{a.ID, a.Name})
		}
		out = append(out, o)
	}
	return map[string]any{"households": out}, nil
}

func (m mcpCall) writableAreasHousehold(ctx context.Context, ref string) (hhCall, error) {
	h, err := m.household(ctx, ref, store.RoleMember)
	if err != nil {
		return h, err
	}
	on, err := m.areasOn(ctx, h)
	if err != nil {
		return h, err
	}
	if !on {
		return h, toolErrf("areas are turned off in household %q (an owner enables 'Group chores by area' in the household settings)", h.name)
	}
	return h, nil
}

func (m mcpCall) createArea(ctx context.Context, in createAreaIn) (any, error) {
	h, err := m.writableAreasHousehold(ctx, in.Household)
	if err != nil {
		return nil, err
	}
	if err := m.writeAllowed(h); err != nil {
		return nil, err
	}
	max, err := m.s.limits.Limit(ctx, limits.AreasPerHousehold, h.hh)
	if err != nil {
		return nil, err
	}
	a, err := m.s.st.CreateArea(ctx, h.actor(), h.hh, in.Name, max)
	if err != nil {
		return nil, m.areaFailure(err)
	}
	return map[string]any{"area": map[string]string{"id": a.ID, "name": a.Name, "household": h.name}}, nil
}

func (m mcpCall) renameArea(ctx context.Context, in renameAreaIn) (any, error) {
	h, err := m.writableAreasHousehold(ctx, in.Household)
	if err != nil {
		return nil, err
	}
	a, err := m.area(ctx, h, in.Area)
	if err != nil {
		return nil, err
	}
	if err := m.writeAllowed(h); err != nil {
		return nil, err
	}
	if err := m.s.st.RenameArea(ctx, h.actor(), h.hh, a.ID, in.Name); err != nil {
		return nil, m.areaFailure(err)
	}
	a, err = m.s.st.Area(ctx, h.actor(), h.hh, a.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"area": map[string]string{"id": a.ID, "name": a.Name, "household": h.name}}, nil
}

func (m mcpCall) updateChore(ctx context.Context, in updateIn) (any, error) {
	h, err := m.household(ctx, in.Household, store.RoleMember)
	if err != nil {
		return nil, err
	}
	old, err := m.chore(ctx, h, in.Chore)
	if err != nil {
		return nil, err
	}
	if !old.ArchivedAt.IsZero() {
		return nil, toolErrf("%q is archived; use unarchive_chore before editing it", old.Name)
	}
	loc, err := time.LoadLocation(h.tz)
	if err != nil {
		return nil, err
	}
	vc, err := m.viewCtx(ctx, h)
	if err != nil {
		return nil, err
	}
	// Start from the chore as the REST editor sends it back, then apply the
	// requested fields: the same document and validation as PUT.
	cur := vc.view(old)
	doc := choreInput{Name: cur.Name, Notes: cur.Notes, Link: cur.Link, Kind: cur.Kind, Schedule: cur.Schedule,
		Assignees: cur.Assignees}
	changed := false
	if in.Name != nil {
		doc.Name, changed = strings.TrimSpace(*in.Name), true
	}
	if in.Notes != nil {
		doc.Notes, changed = *in.Notes, true
	}
	if in.Link != nil {
		doc.Link, changed = strings.TrimSpace(*in.Link), true
	}
	if in.Area != nil {
		changed = true
		var id *string
		if strings.TrimSpace(*in.Area) != "" {
			on, err := m.areasOn(ctx, h)
			if err != nil {
				return nil, err
			}
			if !on {
				return nil, toolErrf("areas are turned off in household %q, so a chore cannot be put in one", h.name)
			}
			a, err := m.area(ctx, h, *in.Area)
			if err != nil {
				return nil, err
			}
			id = &a.ID
		}
		doc.AreaID = optional[string]{set: true, Value: id}
	}
	if in.Assignees != nil {
		changed = true
		doc.Assignees = make([]string, 0, len(*in.Assignees))
		for _, a := range *in.Assignees {
			if strings.EqualFold(strings.TrimSpace(a), "me") {
				a = m.c.user.ID
			}
			if !slices.Contains(doc.Assignees, a) {
				doc.Assignees = append(doc.Assignees, a)
			}
		}
	}
	if in.Repeat != nil {
		changed = true
		kind, sc, err := parseRepeat(*in.Repeat)
		if err != nil {
			return nil, err
		}
		sc.FirstDue = doc.Schedule.FirstDue
		if kind == store.KindRecurring && doc.Kind == store.KindRecurring {
			sc.Anchor = doc.Schedule.Anchor // kept unless anchor is given below
		}
		doc.Kind, doc.Schedule = kind, sc
	}
	if in.FirstDue != nil {
		changed = true
		doc.Schedule.FirstDue = strings.TrimSpace(*in.FirstDue)
	}
	if in.Anchor != nil {
		changed = true
		if *in.Anchor != store.AnchorFromCompletion && *in.Anchor != store.AnchorFromScheduled {
			return nil, toolErrf("anchor must be from_completion or from_scheduled")
		}
		doc.Schedule.Anchor = *in.Anchor
	}
	if in.ReminderPreset != nil {
		changed = true
		doc.ReminderPreset = in.ReminderPreset
	}
	if in.RemindAt != nil {
		changed = true
		doc.ReminderTime = in.RemindAt
	}
	if in.TakeTurns != nil {
		changed = true
		doc.Rotation = in.TakeTurns
	}
	if in.AdvanceOn != nil {
		changed = true
		doc.AdvanceOn = in.AdvanceOn
	}
	if !changed {
		return nil, toolErrf("nothing to change: pass at least one of name, notes, link, area, assignees, repeat, first_due, anchor, reminder_preset, remind_at, take_turns, advance_on")
	}
	si, err := doc.build(loc, &old)
	if err != nil {
		return nil, mcpInvalid(err, "invalid chore update", "check name (1 to 200 characters), notes, link (http or https), first_due (YYYY-MM-DD), repeat, anchor (from_completion or from_scheduled), reminder_preset, remind_at (HH:MM), advance_on (any or assignee), and that take_turns needs 2 or more assignees and a recurring chore")
	}
	if err := m.writeAllowed(h); err != nil {
		return nil, err
	}
	hr, err := m.s.st.Household(ctx, h.actor(), h.hh)
	if err != nil {
		return nil, err
	}
	if err := m.s.applyReminderTime(ctx, h.actor(), h.hh, doc, &si, old.ID, hr.DefaultReminderPreset); err != nil {
		return nil, err
	}
	ch, err := m.s.st.UpdateChore(ctx, h.actor(), h.hh, old.ID, si)
	if err != nil {
		return nil, err
	}
	if vc, err = m.viewCtx(ctx, h); err != nil {
		return nil, err
	}
	return map[string]any{"chore": m.view(vc, h, ch, true)}, nil
}

func (m mcpCall) archiveChore(ctx context.Context, in choreRefIn) (any, error) {
	h, err := m.household(ctx, in.Household, store.RoleMember)
	if err != nil {
		return nil, err
	}
	ch, err := m.chore(ctx, h, in.Chore)
	if err != nil {
		return nil, err
	}
	if err := m.writeAllowed(h); err != nil {
		return nil, err
	}
	if err := m.s.st.ArchiveChore(ctx, h.actor(), h.hh, ch.ID); err != nil {
		return nil, err
	}
	return map[string]any{"archived": true, "chore": ch.ID, "hint": "unarchive_chore with this id brings it back"}, nil
}

func (m mcpCall) unarchiveChore(ctx context.Context, in choreRefIn) (any, error) {
	h, err := m.household(ctx, in.Household, store.RoleMember)
	if err != nil {
		return nil, err
	}
	ch, err := m.chore(ctx, h, in.Chore)
	if err != nil {
		return nil, err
	}
	if err := m.writeAllowed(h); err != nil {
		return nil, err
	}
	ch, err = m.s.st.UnarchiveChore(ctx, h.actor(), h.hh, ch.ID)
	if err != nil {
		return nil, err
	}
	vc, err := m.viewCtx(ctx, h)
	if err != nil {
		return nil, err
	}
	return map[string]any{"chore": m.view(vc, h, ch, true)}, nil
}
