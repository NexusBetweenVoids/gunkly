package api

import (
	"errors"
	"net/http"

	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

func (s *Server) getSetup(w http.ResponseWriter, _ *http.Request, _ *call) error {
	return ok(w, map[string]bool{"needed": s.setupPending()})
}

// postSetup creates the instance admin with the one-time setup code from
// the logs and signs them in. It disappears once used.
func (s *Server) postSetup(w http.ResponseWriter, r *http.Request, c *call) error {
	var in struct {
		Code string `json:"code"`
		newAccount
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if !s.setupPending() {
		return errNotFound
	}
	if !s.checkSetupCode(in.Code) {
		return errf(http.StatusForbidden, "invalid_setup_code")
	}
	ctx := r.Context()
	nu, err := s.validate(ctx, in.newAccount)
	if err != nil {
		return err
	}
	u, err := s.st.CreateFirstAdmin(ctx, nu)
	if errors.Is(err, store.ErrConflict) {
		s.consumeSetup() // completed elsewhere (another instance)
		return errNotFound
	} else if err != nil {
		return err
	}
	s.consumeSetup()
	if err := s.startSession(w, r, c, u, http.StatusCreated); err != nil {
		return err
	}
	s.audit(ctx, c, u.ID, store.AuditSetup, "", nil)
	s.verifyNewAccount(ctx, c, u)
	return nil
}
