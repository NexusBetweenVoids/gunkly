package api

import (
	"net/http"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/i18n"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// starterAreaKeys are the areas offered when a household is created; their
// names come from the server catalog ("area.<key>").
var starterAreaKeys = []string{"kitchen", "bathroom", "garden", "exterior", "appliances", "general"}

func starterAreas(locale string) []string {
	out := make([]string, 0, len(starterAreaKeys))
	for _, k := range starterAreaKeys {
		out = append(out, i18n.T(locale, "area."+k, nil))
	}
	return out
}

type areaView struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}

func viewArea(a store.Area) areaView { return areaView{ID: a.ID, Name: a.Name, CreatedAt: a.CreatedAt} }

func (s *Server) listAreas(w http.ResponseWriter, r *http.Request, c *call) error {
	as, err := s.st.ListAreas(r.Context(), c.actor(), c.hh)
	if err != nil {
		return err
	}
	out := make([]areaView, 0, len(as))
	for _, a := range as {
		out = append(out, viewArea(a))
	}
	return ok(w, out)
}

type areaInput struct {
	Name string `json:"name"`
}

func (s *Server) createArea(w http.ResponseWriter, r *http.Request, c *call) error {
	var in areaInput
	if err := decode(r, &in); err != nil {
		return err
	}
	a, err := s.st.CreateArea(r.Context(), c.actor(), c.hh, in.Name)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, viewArea(a))
	return nil
}

func (s *Server) renameArea(w http.ResponseWriter, r *http.Request, c *call) error {
	var in areaInput
	if err := decode(r, &in); err != nil {
		return err
	}
	id := r.PathValue("area")
	if err := s.st.RenameArea(r.Context(), c.actor(), c.hh, id, in.Name); err != nil {
		return err
	}
	a, err := s.st.Area(r.Context(), c.actor(), c.hh, id)
	if err != nil {
		return err
	}
	return ok(w, viewArea(a))
}

func (s *Server) deleteArea(w http.ResponseWriter, r *http.Request, c *call) error {
	if err := s.st.DeleteArea(r.Context(), c.actor(), c.hh, r.PathValue("area")); err != nil {
		return err
	}
	return noContent(w)
}
