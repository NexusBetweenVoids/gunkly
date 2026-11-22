package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

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
	return ok(w, map[string]any{
		"openSignup": open, "emailEnabled": s.emailEnabled(), "hostedMode": s.hosted, "requirePow": open && pow,
		"baseUrl": s.baseURL, "mcpEnabled": s.mcpEnabled(r.Context()),
	})
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
// Order: take the per-IP sign-up cap, check and spend the proof of work (before
// anything expensive, so a replayed or forged solution costs one cheap
// check), validate (hashes the password), create the account. A failed
// attempt that got past the proof of work has spent it, so the client fetches
// a new challenge each time.
//
// Hosted mode requires an email and answers 202 whether the account was
// created or the address already had one (that owner gets a "you already have
// an account" mail instead): sign-up is not an oracle for registered
// addresses, and nobody is signed in until the address is confirmed. A taken
// username still answers registration_failed (usernames are visible inside
// households). Self-hosted, the email is optional (with SMTP a verification
// mail is sent if one is given); the account is created and signed in at once
// (201), and a taken email or username answers registration_failed.
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
	if !localeRE.MatchString(in.Locale) || (s.hosted && in.Email == "") {
		return errInvalid
	}
	perHour, err := s.limits.Limit(ctx, limits.SignupsPerIPHour, "")
	if err != nil {
		return err
	}
	if wait := s.counts.take(quota{"signup:ip:" + c.rip, perHour}); wait > 0 {
		return tooMany(w, wait)
	}
	if needPoW {
		// Verify is a stateless check; spending the solution comes before
		// the Argon2id hash in validate, so replaying one never costs a hash.
		challenge, expires, err := s.pow.Verify(in.PoW, s.now())
		if err != nil {
			return errf(http.StatusBadRequest, "pow_invalid")
		}
		if err := s.st.UsePoW(ctx, challenge, expires); errors.Is(err, store.ErrConflict) {
			return errf(http.StatusBadRequest, "pow_invalid")
		} else if err != nil {
			return err
		}
	}
	nu, err := s.validate(ctx, in.newAccount)
	if err != nil {
		return err
	}
	nu.Locale = in.Locale
	if s.hosted || in.Email != "" {
		if taken, err := s.st.EmailTaken(ctx, in.Email, ""); err != nil {
			return err
		} else if taken {
			return s.signupExists(ctx, w, c, in.Email)
		}
	}
	u, err := s.st.CreateUser(ctx, nu)
	if errors.Is(err, store.ErrConflict) {
		if s.hosted || in.Email != "" {
			// Lost a race for the address (or it is the username).
			if taken, terr := s.st.EmailTaken(ctx, in.Email, ""); terr == nil && taken {
				return s.signupExists(ctx, w, c, in.Email)
			}
		}
		return errf(http.StatusBadRequest, "registration_failed")
	} else if err != nil {
		return err
	}
	s.audit(ctx, c, u.ID, store.AuditSignup, "", nil)
	s.verifyNewAccount(ctx, c, u)
	if s.hosted {
		return accepted(w)
	}
	return s.startSession(w, r, c, u, http.StatusCreated)
}

// signupExists is the sign-up answer for an address that already has an
// account: the same 202 as a fresh sign-up, plus a notice to that address
// (per-IP, global and per-address caps; never the account's own mail bucket).
func (s *Server) signupExists(ctx context.Context, w http.ResponseWriter, c *call, email string) error {
	if !s.hosted {
		return errf(http.StatusBadRequest, "registration_failed")
	}
	if s.rl.allow(rateSignup+":email:"+strings.ToLower(email)) == 0 {
		if u, err := s.st.UserByLogin(ctx, email); err == nil {
			s.queueMail(ctx, c, mailRequest{user: u, kind: "signupexists", unauth: true, params: map[string]string{
				"signin": s.baseURL + "/signin", "reset": s.baseURL + "/forgot-password"}})
		}
	}
	return accepted(w)
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
