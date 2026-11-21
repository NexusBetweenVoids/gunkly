package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/schedule"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

type completionView struct {
	ID          string     `json:"id"`
	ChoreID     string     `json:"choreId"`
	UserID      *string    `json:"userId"` // null: deleted user or household channel
	CompletedAt time.Time  `json:"completedAt"`
	Source      string     `json:"source"`
	Note        string     `json:"note"`
	RevertedAt  *time.Time `json:"revertedAt"`
}

func viewCompletion(c store.Completion) completionView {
	v := completionView{ID: c.ID, ChoreID: c.ChoreID, CompletedAt: c.CompletedAt, Source: c.Source, Note: c.Note, RevertedAt: timePtr(c.RevertedAt)}
	if c.UserID != "" {
		v.UserID = &c.UserID
	}
	return v
}

func (s *Server) completeChore(w http.ResponseWriter, r *http.Request, c *call) error {
	var in struct {
		Note string `json:"note"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &in); err != nil {
			return err
		}
	}
	if len(in.Note) > 2000 {
		return errInvalid
	}
	comp, ch, err := s.st.CompleteChoreAuto(r.Context(), c.actor(), c.hh, r.PathValue("chore"), in.Note)
	if err != nil {
		return err
	}
	h, err := s.st.Household(r.Context(), c.actor(), c.hh)
	if err != nil {
		return err
	}
	v, err := s.loadChoreView(r, c, ch, h.Timezone)
	if err != nil {
		return err
	}
	// Undo is reverting this completion.
	undo := "/api/v1/households/" + c.hh + "/chores/" + ch.ID + "/completions/" + comp.ID + "/revert"
	writeJSON(w, http.StatusCreated, map[string]any{"completion": viewCompletion(comp), "chore": v, "undoPath": undo})
	return nil
}

func (s *Server) listCompletions(w http.ResponseWriter, r *http.Request, c *call) error {
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			return errInvalid
		}
		limit = n
	}
	items, next, err := s.st.CompletionsPage(r.Context(), c.actor(), c.hh, r.PathValue("chore"), limit, r.URL.Query().Get("after"))
	if err != nil {
		return err
	}
	out := make([]completionView, 0, len(items))
	for _, it := range items {
		out = append(out, viewCompletion(it))
	}
	return ok(w, map[string]any{"items": out, "next": next})
}

func (s *Server) revertCompletion(w http.ResponseWriter, r *http.Request, c *call) error {
	chore, id := r.PathValue("chore"), r.PathValue("completion")
	ch, err := s.st.RevertChoreCompletion(r.Context(), c.actor(), c.hh, chore, id)
	if err != nil {
		return err
	}
	s.audit(r.Context(), c, c.user.ID, store.AuditCompletionRevert, c.hh, map[string]string{"chore": chore, "completion": id})
	return s.respondChore(w, r, c, ch)
}

// stateCtx loads what snooze, skip and pause need: the chore, the household
// zone and today's date there.
func (s *Server) stateCtx(r *http.Request, c *call) (store.Chore, *time.Location, schedule.Date, error) {
	ctx := r.Context()
	ch, err := s.st.Chore(ctx, c.actor(), c.hh, r.PathValue("chore"))
	if err != nil {
		return ch, nil, schedule.Date{}, err
	}
	h, err := s.st.Household(ctx, c.actor(), c.hh)
	if err != nil {
		return ch, nil, schedule.Date{}, err
	}
	loc, err := time.LoadLocation(h.Timezone)
	if err != nil {
		return ch, nil, schedule.Date{}, err
	}
	return ch, loc, schedule.DateOf(s.now(), loc), nil
}

// futureDate parses a request date that must not be before minDate.
func futureDate(v string, minDate schedule.Date) (schedule.Date, error) {
	d, err := schedule.ParseDate(v)
	if err != nil || d.Before(minDate) || d.Year > 9000 {
		return d, errInvalid
	}
	return d, nil
}

func (s *Server) snoozeChore(w http.ResponseWriter, r *http.Request, c *call) error {
	var in struct {
		Until string `json:"until"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	ch, loc, today, err := s.stateCtx(r, c)
	if err != nil {
		return err
	}
	until, err := futureDate(in.Until, today)
	if err != nil {
		return err
	}
	ch, err = s.st.MoveChoreOccurrence(r.Context(), c.actor(), c.hh, ch.ID, until.String(), store.OccurrenceInstant(until, loc))
	if err != nil {
		return err
	}
	return s.respondChore(w, r, c, ch)
}

// skipChore advances a recurring chore to its next occurrence after today (and
// after the current one) without recording a completion.
func (s *Server) skipChore(w http.ResponseWriter, r *http.Request, c *call) error {
	ch, loc, today, err := s.stateCtx(r, c)
	if err != nil {
		return err
	}
	rule, err := store.RuleOf(ch)
	if err != nil {
		return err
	}
	cur, perr := schedule.ParseDate(ch.DueDate)
	if rule == nil || perr != nil {
		return errInvalid // one-off chores do not repeat
	}
	next, found := schedule.NextDue(rule, schedule.FromScheduled, cur, today)
	if !found {
		return errf(http.StatusConflict, "no_next_occurrence")
	}
	ch, err = s.st.MoveChoreOccurrence(r.Context(), c.actor(), c.hh, ch.ID, next.String(), store.OccurrenceInstant(next, loc))
	if err != nil {
		return err
	}
	s.audit(r.Context(), c, c.user.ID, store.AuditChoreSkip, c.hh, map[string]string{"chore": ch.ID})
	return s.respondChore(w, r, c, ch)
}

// pauseChore pauses until a local date (reminders resume at the start of that
// day) or, without a date, indefinitely.
func (s *Server) pauseChore(w http.ResponseWriter, r *http.Request, c *call) error {
	var in struct {
		Until *string `json:"until"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &in); err != nil {
			return err
		}
	}
	ch, loc, today, err := s.stateCtx(r, c)
	if err != nil {
		return err
	}
	var until time.Time
	if in.Until != nil {
		d, err := futureDate(*in.Until, today.AddDays(1))
		if err != nil {
			return err
		}
		until = store.OccurrenceInstant(d, loc)
	}
	ch, err = s.st.SetChorePause(r.Context(), c.actor(), c.hh, ch.ID, until, in.Until == nil)
	if err != nil {
		return err
	}
	return s.respondChore(w, r, c, ch)
}

func (s *Server) resumeChore(w http.ResponseWriter, r *http.Request, c *call) error {
	ch, err := s.st.SetChorePause(r.Context(), c.actor(), c.hh, r.PathValue("chore"), time.Time{}, false)
	if err != nil {
		return err
	}
	return s.respondChore(w, r, c, ch)
}
