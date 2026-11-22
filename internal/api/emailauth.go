package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/auth"
	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// Email verification, email change, password reset and email-code second
// factor (ADR 0008). Every emailed credential is a random single-use token
// stored only as a hash.

// requireEmail answers 404 on instances without email.
func (s *Server) requireEmail() error {
	if !s.emailEnabled() {
		return errNotFound
	}
	return nil
}

// sendVerification emails a verification link for u: for the current address,
// or newEmail for an email change. It returns the wait if an email cap is full
// (then nothing is issued or sent). unauth: the request had no session.
func (s *Server) sendVerification(ctx context.Context, c *call, u store.User, newEmail string, unauth bool) (time.Duration, error) {
	ip := ""
	if unauth {
		ip = c.rip
	}
	if wait := s.mailQuota(ctx, u.ID, ip); wait > 0 {
		return wait, nil
	}
	token := ids.Token()
	if err := s.st.IssueVerifyToken(ctx, u.ID, ids.Hash(token), newEmail, s.now().Add(verifyTTL)); err != nil {
		return 0, err
	}
	s.dispatchMail(ctx, mailRequest{user: u, to: newEmail, kind: "verify", params: map[string]string{
		"link": s.link("/verify-email", token), "hours": strconv.Itoa(int(verifyTTL.Hours()))}})
	return 0, nil
}

// sendLoginCode emails the user a fresh second-factor code (replacing any
// earlier one). It returns the wait if an email cap is full.
func (s *Server) sendLoginCode(ctx context.Context, c *call, u store.User, unauth bool) (time.Duration, error) {
	ip := ""
	if unauth {
		ip = c.rip
	}
	if wait := s.mailQuota(ctx, u.ID, ip); wait > 0 {
		return wait, nil
	}
	challenge, code := ids.New(), auth.GenerateEmailOTP()
	if err := s.st.IssueEmailOTP(ctx, u.ID, challenge, auth.HashEmailOTP(challenge, code), s.now().Add(otpTTL)); err != nil {
		return 0, err
	}
	s.dispatchMail(ctx, mailRequest{user: u, kind: "otp", params: map[string]string{
		"code": code, "minutes": strconv.Itoa(int(otpTTL.Minutes()))}})
	return 0, nil
}

func accepted(w http.ResponseWriter) error {
	writeJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
	return nil
}

func validToken(t string) bool { return t != "" && len(t) <= 128 }

// verifyEmail consumes the link's token. It needs no session: the link is
// often opened on another device. In hosted mode, 2FA is mandatory, so a user
// without any second factor is enrolled in email codes right here and the
// recovery codes are shown once.
func (s *Server) verifyEmail(w http.ResponseWriter, r *http.Request, c *call) error {
	if err := s.requireEmail(); err != nil {
		return err
	}
	var in struct {
		Token string `json:"token"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if !validToken(in.Token) {
		return errf(http.StatusBadRequest, "invalid_token")
	}
	ctx := r.Context()
	codes, hashes := auth.GenerateRecoveryCodes(auth.RecoveryCodeCount)
	u, enrolled, err := s.st.VerifyEmail(ctx, ids.Hash(in.Token), s.hosted, hashes)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return errf(http.StatusBadRequest, "invalid_token")
	case errors.Is(err, store.ErrConflict):
		return errf(http.StatusConflict, "email_unavailable")
	case err != nil:
		return err
	}
	s.audit(ctx, c, u.ID, store.AuditEmailVerify, "", nil)
	if enrolled {
		s.audit(ctx, c, u.ID, store.AuditEmailOTPEnable, "", map[string]string{"via": "verification"})
	}
	out := map[string]any{"verified": true}
	if enrolled {
		out["recoveryCodes"] = codes
	}
	return ok(w, out)
}

// resendVerification sends a fresh link to the pending new address, or to the
// current address while it is unverified.
func (s *Server) resendVerification(w http.ResponseWriter, r *http.Request, c *call) error {
	if err := s.requireEmail(); err != nil {
		return err
	}
	ctx := r.Context()
	pending, err := s.st.PendingEmail(ctx, c.user.ID)
	if err != nil {
		return err
	}
	if pending == "" && (c.user.Email == "" || c.user.EmailVerified) {
		return errf(http.StatusConflict, "already_verified")
	}
	wait, err := s.sendVerification(ctx, c, c.user, pending, false)
	if err != nil {
		return err
	}
	if wait > 0 {
		return tooMany(w, wait)
	}
	return accepted(w)
}

// changeEmail starts an email change: a verification link goes to the new
// address and the old one is told. The answer is the same whether or not the
// new address belongs to another account (no oracle); the address only
// changes once its owner opens the link.
func (s *Server) changeEmail(w http.ResponseWriter, r *http.Request, c *call) error {
	if err := s.requireEmail(); err != nil {
		return err
	}
	var in struct {
		Email string `json:"email"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	email := strings.TrimSpace(in.Email)
	if !emailRE.MatchString(email) || strings.EqualFold(email, c.user.Email) {
		return errInvalid
	}
	ctx := r.Context()
	taken, err := s.st.EmailTaken(ctx, email, c.user.ID)
	if err != nil {
		return err
	}
	if taken {
		return accepted(w)
	}
	wait, err := s.sendVerification(ctx, c, c.user, email, false)
	if err != nil {
		return err
	}
	if wait > 0 {
		return tooMany(w, wait)
	}
	if c.user.Email != "" {
		// Best effort: the notice is not worth failing the change for.
		s.queueMail(ctx, c, mailRequest{user: c.user, kind: "emailchange", params: map[string]string{"newEmail": email}})
	}
	s.audit(ctx, c, c.user.ID, store.AuditEmailChange, "", nil)
	return accepted(w)
}

// requestPasswordReset always answers 202, whether or not the login exists,
// has an email, or the caps are full, so it cannot be used to find accounts.
// Unknown logins do the same token work as known ones.
func (s *Server) requestPasswordReset(w http.ResponseWriter, r *http.Request, c *call) error {
	if err := s.requireEmail(); err != nil {
		return err
	}
	var in struct {
		Login string `json:"login"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	login := strings.ToLower(strings.TrimSpace(in.Login))
	if login == "" || len(login) > 254 {
		return errInvalid
	}
	ctx := r.Context()
	token := ids.Token()
	hash := ids.Hash(token)
	// Per login string over all IPs, so mail bombing one account from many
	// addresses is capped too; over the limit it is silent (still 202).
	throttled := s.rl.allow(rateReset+":acct:"+login) > 0
	u, err := s.st.UserByLogin(ctx, login)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return accepted(w)
	case err != nil:
		return err
	}
	if u.Email == "" || throttled {
		return accepted(w)
	}
	if wait := s.mailQuota(ctx, u.ID, c.rip); wait > 0 {
		return accepted(w)
	}
	if err := s.st.IssueResetToken(ctx, u.ID, hash, s.now().Add(resetTTL)); err != nil {
		return err
	}
	s.dispatchMail(ctx, mailRequest{user: u, kind: "reset", params: map[string]string{
		"link": s.link("/reset-password", token), "hours": strconv.Itoa(int(resetTTL.Hours()))}})
	return accepted(w)
}

// confirmPasswordReset sets a new password with a reset token. The token is
// single use; the store call also signs the account out everywhere and
// deletes its mark-done links, like any password change. Step-up dies with
// the sessions. A second factor, if any, still applies at the next sign-in.
func (s *Server) confirmPasswordReset(w http.ResponseWriter, r *http.Request, c *call) error {
	if err := s.requireEmail(); err != nil {
		return err
	}
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if !validToken(in.Token) {
		return errf(http.StatusBadRequest, "invalid_token")
	}
	ctx := r.Context()
	if err := s.checkNewPassword(ctx, in.Password); err != nil {
		return err
	}
	id, err := s.st.ResetPassword(ctx, ids.Hash(in.Token), s.hashPassword(in.Password))
	if errors.Is(err, store.ErrNotFound) {
		return errf(http.StatusBadRequest, "invalid_token")
	} else if err != nil {
		return err
	}
	s.acct.reset("u:" + id) // the owner proved control of the mailbox
	s.audit(ctx, c, id, store.AuditPasswordReset, "", map[string]string{"via": "email"})
	return noContent(w)
}

// enableEmailOTP makes emailed codes the second factor (replacing TOTP) and
// returns fresh recovery codes.
func (s *Server) enableEmailOTP(w http.ResponseWriter, r *http.Request, c *call) error {
	if err := s.requireEmail(); err != nil {
		return err
	}
	if c.user.EmailOTPEnabled {
		return errf(http.StatusConflict, "email_otp_already_enabled")
	}
	codes, hashes := auth.GenerateRecoveryCodes(auth.RecoveryCodeCount)
	if err := s.st.EnableEmailOTP(r.Context(), c.user.ID, c.sess.ID, hashes); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return errf(http.StatusConflict, "email_not_verified")
		}
		return err
	}
	s.audit(r.Context(), c, c.user.ID, store.AuditEmailOTPEnable, "", nil)
	return ok(w, map[string][]string{"recoveryCodes": codes})
}

// disableEmailOTP turns emailed codes off. Hosted mode requires 2FA, so the
// user must switch to an authenticator app instead.
func (s *Server) disableEmailOTP(w http.ResponseWriter, r *http.Request, c *call) error {
	if !c.user.EmailOTPEnabled {
		return errNotFound
	}
	if s.hosted {
		return errf(http.StatusConflict, "two_factor_mandatory")
	}
	if err := s.st.DisableEmailOTP(r.Context(), c.user.ID, c.sess.ID); err != nil {
		return err
	}
	s.audit(r.Context(), c, c.user.ID, store.AuditEmailOTPDisable, "", nil)
	return noContent(w)
}

// requireAccess is the gate for creating or joining a household. With email
// on, an account with an unverified email may sign in but not do that: always
// in hosted mode, and on instances with open sign-up (where addresses are
// self-asserted). Hosted mode also requires a second factor (2FA is mandatory
// there; verification enrols email codes automatically).
func (s *Server) requireAccess(ctx context.Context, u store.User) error {
	if !s.emailEnabled() { // hosted mode always has email (config enforces it)
		return nil
	}
	if u.EmailVerified {
		return s.requireTwoFactor(u)
	}
	if s.hosted {
		return errf(http.StatusForbidden, "email_not_verified")
	}
	st, err := s.st.InstanceSettings(ctx)
	if err != nil {
		return err
	}
	if open, _ := s.signupSettings(st); open && u.Email != "" {
		return errf(http.StatusForbidden, "email_not_verified")
	}
	return nil
}

func (s *Server) requireTwoFactor(u store.User) error {
	if s.hosted && !u.TOTPEnabled && !u.EmailOTPEnabled {
		return errf(http.StatusForbidden, "two_factor_setup_required")
	}
	return nil
}
