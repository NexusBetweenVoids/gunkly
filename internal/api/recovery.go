package api

import (
	"net/http"

	"github.com/NexusBetweenVoids/gunkly/internal/auth"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// getRecoveryCodes tells the account screen how many unused recovery codes are
// left (the codes themselves are only ever shown once, when they are created).
func (s *Server) getRecoveryCodes(w http.ResponseWriter, r *http.Request, c *call) error {
	n, err := s.st.RecoveryCodesLeft(r.Context(), c.user.ID)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"remaining": n, "available": c.user.HasSecondFactor()})
}

// newRecoveryCodes replaces the user's recovery codes with a fresh set and
// returns them once. Only for accounts with a second factor (codes stand in for it).
func (s *Server) newRecoveryCodes(w http.ResponseWriter, r *http.Request, c *call) error {
	if !c.user.HasSecondFactor() {
		return errf(http.StatusConflict, "no_second_factor")
	}
	codes, hashes := auth.GenerateRecoveryCodes(auth.RecoveryCodeCount)
	if err := s.st.ReplaceRecoveryCodes(r.Context(), c.user.ID, hashes); err != nil {
		return err
	}
	s.audit(r.Context(), c, c.user.ID, store.AuditRecoveryCodesNew, "", nil)
	return ok(w, map[string][]string{"recoveryCodes": codes})
}
