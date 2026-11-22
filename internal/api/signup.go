package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/auth"
	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// signupSettings resolves the instance's open sign-up and proof-of-work
// switches: the admin's choice if one was made, else on for hosted mode.
func (s *Server) signupSettings(st store.InstanceSettings) (open, pow bool) {
	open, pow = st.OpenSignup, st.RequirePoW
	if !st.OpenSignupSet {
		open = s.hosted
	}
	if !st.RequirePoWSet {
		pow = s.hosted
	}
	return open, pow
}

// getInstance tells the sign-in and sign-up screens what this instance
// offers. Public: it reveals only configuration the login page needs.
func (s *Server) getInstance(w http.ResponseWriter, r *http.Request, _ *call) error {
	st, err := s.st.InstanceSettings(r.Context())
	if err != nil {
		return err
	}
	open, pow := s.signupSettings(st)
	return ok(w, map[string]bool{"openSignup": open, "emailEnabled": s.emailEnabled(), "hostedMode": s.hosted, "requirePow": open && pow})
}

// signupChallenge hands out a proof-of-work challenge (ALTCHA format).
func (s *Server) signupChallenge(w http.ResponseWriter, r *http.Request, _ *call) error {
	st, err := s.st.InstanceSettings(r.Context())
	if err != nil {
		return err
	}
	if open, pow := s.signupSettings(st); !open || !pow {
		return errNotFound
	}
	return ok(w, s.pow.New(s.now()))
}

// signup creates an account without an invite (only when open sign-up is on).
// Order: validate input, spend the proof of work, take the per-IP sign-up cap,
// create the account, sign in, email the verification link. A failed attempt
// that got past the proof of work has still spent it, so the client fetches a
// new challenge each time.
func (s *Server) signup(w http.ResponseWriter, r *http.Request, c *call) error {
	var in struct {
		newAccount
		Locale string           `json:"locale"`
		PoW    auth.PoWSolution `json:"pow"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	st, err := s.st.InstanceSettings(ctx)
	if err != nil {
		return err
	}
	open, needPoW := s.signupSettings(st)
	if !open {
		return errf(http.StatusForbidden, "registration_disabled")
	}
	in.Email = strings.TrimSpace(in.Email)
	if !localeRE.MatchString(in.Locale) || (s.emailEnabled() && in.Email == "") {
		return errInvalid
	}
	// The stateless proof check comes first: it is cheap, while validating
	// hashes the password. Spending it (single use) waits until the input is valid.
	var challenge string
	var expires time.Time
	if needPoW {
		if challenge, expires, err = s.pow.Verify(in.PoW, s.now()); err != nil {
			return errf(http.StatusBadRequest, "pow_invalid")
		}
	}
	nu, err := s.validate(ctx, in.newAccount)
	if err != nil {
		return err
	}
	nu.Locale = in.Locale
	if needPoW {
		if err := s.st.UsePoW(ctx, challenge, expires); errors.Is(err, store.ErrConflict) {
			return errf(http.StatusBadRequest, "pow_invalid")
		} else if err != nil {
			return err
		}
	}
	perHour, err := s.limits.Limit(ctx, limits.SignupsPerIPHour, "")
	if err != nil {
		return err
	}
	if wait := s.counts.take(quota{"signup:ip:" + c.rip, perHour}); wait > 0 {
		return tooMany(w, wait)
	}
	u, err := s.st.CreateUser(ctx, nu)
	if errors.Is(err, store.ErrConflict) {
		return errf(http.StatusBadRequest, "registration_failed") // same for a taken username or email
	} else if err != nil {
		return err
	}
	if err := s.startSession(w, r, c, u, http.StatusCreated); err != nil {
		return err
	}
	s.audit(ctx, c, u.ID, store.AuditSignup, "", nil)
	s.verifyNewAccount(ctx, c, u)
	return nil
}

// verifyNewAccount emails the verification link of a just-created account
// that has an email address (best effort: the account exists either way and
// the banner offers a resend).
func (s *Server) verifyNewAccount(ctx context.Context, c *call, u store.User) {
	if !s.emailEnabled() || u.Email == "" {
		return
	}
	if _, err := s.sendVerification(ctx, c, u, "", true); err != nil {
		s.log.Error("verification email", "err", err)
	}
}
