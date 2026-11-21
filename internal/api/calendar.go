package api

import (
	"net/http"
	"slices"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/schedule"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// maxCalendarDays bounds a calendar query (inclusive range).
const maxCalendarDays = 62

type calendarEntry struct {
	ChoreID string   `json:"choreId"`
	Name    string   `json:"name"`
	AreaID  *string  `json:"areaId"`
	Dates   []string `json:"dates"` // local dates YYYY-MM-DD, ascending
}

// occurrencesIn lists the dates a chore falls due within [from, to]: all
// occurrences of calendar-anchored (from-scheduled) chores, only the next due
// date for one-off and from-completion chores.
func occurrencesIn(c store.Chore, from, to schedule.Date) []string {
	cur, err := schedule.ParseDate(c.DueDate)
	if err != nil || c.NextDue.IsZero() {
		return nil
	}
	var ds []schedule.Date
	if c.Kind == store.KindRecurring && c.Anchor == store.AnchorFromScheduled {
		if rule, err := store.RuleOf(c); err == nil && rule != nil {
			ds = schedule.Occurrences(rule, cur, from, to)
		}
	}
	if !cur.Before(from) && !cur.After(to) && !slices.Contains(ds, cur) {
		ds = append(ds, cur)
	}
	slices.SortFunc(ds, schedule.Date.Compare)
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.String()
	}
	return out
}

func (s *Server) calendar(w http.ResponseWriter, r *http.Request, c *call) error {
	q := r.URL.Query()
	from, err1 := time.Parse(time.DateOnly, q.Get("from"))
	to, err2 := time.Parse(time.DateOnly, q.Get("to"))
	if err1 != nil || err2 != nil || to.Before(from) || to.Sub(from) >= maxCalendarDays*24*time.Hour || from.Year() < 1970 || to.Year() > 9000 {
		return errInvalid
	}
	fd, _ := schedule.ParseDate(q.Get("from"))
	td, _ := schedule.ParseDate(q.Get("to"))
	cs, err := s.st.ListChores(r.Context(), c.actor(), c.hh, false)
	if err != nil {
		return err
	}
	items := []calendarEntry{}
	for _, ch := range cs {
		ds := occurrencesIn(ch, fd, td)
		if len(ds) == 0 {
			continue
		}
		e := calendarEntry{ChoreID: ch.ID, Name: ch.Name, Dates: ds}
		if ch.AreaID != "" {
			e.AreaID = &ch.AreaID
		}
		items = append(items, e)
	}
	return ok(w, map[string]any{"from": q.Get("from"), "to": q.Get("to"), "items": items})
}
