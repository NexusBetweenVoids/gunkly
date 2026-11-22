package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

type candidateView struct {
	UserID      string     `json:"userId"`
	DisplayName string     `json:"displayName"`
	Role        store.Role `json:"role"`
}

type ownerChoiceView struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Candidates []candidateView `json:"candidates"`
}

type householdRefView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func viewOwnerChoices(cs []store.OwnerChoice) []ownerChoiceView {
	out := make([]ownerChoiceView, 0, len(cs))
	for _, c := range cs {
		v := ownerChoiceView{ID: c.HouseholdID, Name: c.HouseholdName, Candidates: make([]candidateView, 0, len(c.Candidates))}
		for _, m := range c.Candidates {
			v.Candidates = append(v.Candidates, candidateView{UserID: m.UserID, DisplayName: m.DisplayName, Role: m.Role})
		}
		out = append(out, v)
	}
	return out
}

// writeOwnerRequired answers 409 owner_required with the households that need
// a successor, so the client can ask who takes over.
func writeOwnerRequired(w http.ResponseWriter, e *store.OwnerRequiredError) {
	writeJSON(w, http.StatusConflict, map[string]any{
		"error":      map[string]string{"code": "owner_required"},
		"households": viewOwnerChoices(e.Households),
	})
}

// accountDeletionPreview lists what deleting the account would do.
func (s *Server) accountDeletionPreview(w http.ResponseWriter, r *http.Request, c *call) error {
	p, err := s.st.AccountDeletionPreview(r.Context(), c.user.ID)
	if err != nil {
		return err
	}
	del := make([]householdRefView, 0, len(p.DeleteHouseholds))
	for _, h := range p.DeleteHouseholds {
		del = append(del, householdRefView{ID: h.ID, Name: h.Name})
	}
	return ok(w, map[string]any{"deleteHouseholds": del, "needOwner": viewOwnerChoices(p.NeedOwner), "isAdmin": c.user.IsAdmin})
}

// deleteAccount permanently deletes the caller's account. The body must
// repeat the username (explicit confirmation) and name a new owner for every
// household where the caller is the last owner and others remain.
func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request, c *call) error {
	var in struct {
		ConfirmUsername string            `json:"confirmUsername"`
		NewOwners       map[string]string `json:"newOwners"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(in.ConfirmUsername), c.user.Username) {
		return errf(http.StatusBadRequest, "confirmation_mismatch")
	}
	_, err := s.st.DeleteAccount(r.Context(), c.user.ID, store.AccountDeletion{NewOwners: in.NewOwners})
	var oe *store.OwnerRequiredError
	switch {
	case errors.As(err, &oe):
		writeOwnerRequired(w, oe)
		return nil
	case errors.Is(err, store.ErrLastAdmin):
		return errf(http.StatusConflict, "last_admin")
	case err != nil:
		return err
	}
	s.clearCookies(w)
	return noContent(w)
}

// leaveHousehold is the self-service branch of removeMember: the caller
// leaves, naming a successor if they are the last owner and confirming that
// the household is deleted when nobody else (non-guest) remains.
func (s *Server) leaveHousehold(w http.ResponseWriter, r *http.Request, c *call) error {
	var in struct {
		NewOwner      string `json:"newOwner"`
		ConfirmDelete bool   `json:"confirmDelete"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &in); err != nil {
			return err
		}
	}
	res, err := s.st.LeaveHousehold(r.Context(), c.actor(), c.hh, store.LeaveOptions{NewOwner: in.NewOwner, ConfirmDelete: in.ConfirmDelete})
	var oe *store.OwnerRequiredError
	switch {
	case errors.As(err, &oe):
		writeOwnerRequired(w, oe)
		return nil
	case errors.Is(err, store.ErrConflict):
		return errf(http.StatusConflict, "household_would_be_deleted")
	case err != nil:
		return err
	}
	meta := map[string]string{"user": c.user.ID}
	if len(res.DeletedHouseholds) > 0 {
		meta["householdDeleted"] = "true"
	}
	if len(res.Transfers) > 0 {
		meta["newOwner"] = res.Transfers[0].NewOwnerID
	}
	// A deleted household cannot be referenced by the audit row.
	hh := c.hh
	if len(res.DeletedHouseholds) > 0 {
		hh = ""
	}
	s.audit(r.Context(), c, c.user.ID, store.AuditMemberRemove, hh, meta)
	return noContent(w)
}
