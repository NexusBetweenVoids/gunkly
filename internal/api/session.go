package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/auth"
	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

const (
	sessionCookie = "gunkly_session" // HttpOnly; random token, DB keeps its hash
	csrfCookie    = "gunkly_csrf"    // readable by the SPA, echoed in X-CSRF-Token
)

func (s *Server) cookie(name, value string, httpOnly bool, expires time.Time) *http.Cookie {
	//nolint:gosec // G124: Secure follows BASE_URL scheme; the CSRF cookie must be readable by the SPA
	return &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: httpOnly, Secure: s.secure,
		SameSite: http.SameSiteLaxMode, Expires: expires}
}

func (s *Server) clearCookies(w http.ResponseWriter) {
	for _, n := range []string{sessionCookie, csrfCookie} {
		c := s.cookie(n, "", n == sessionCookie, time.Time{}) //nolint:gosec // G124: see cookie
		c.MaxAge = -1
		http.SetCookie(w, c)
	}
}

// ensureCSRF returns the request's CSRF cookie value, setting a new one if
// missing.
func (s *Server) ensureCSRF(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(csrfCookie); err == nil && len(c.Value) >= 32 {
		return c.Value
	}
	v := ids.Token()
	http.SetCookie(w, s.cookie(csrfCookie, v, false, time.Time{}))
	return v
}

// authenticate resolves the session cookie, enforcing idle and absolute
// expiry and throttled last-seen updates.
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (store.Session, store.User, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" || len(c.Value) > 128 {
		return store.Session{}, store.User{}, false
	}
	ctx := r.Context()
	ss, u, err := s.st.SessionByTokenHash(ctx, ids.Hash(c.Value))
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.log.Error("session lookup", "err", err)
		}
		s.clearCookies(w)
		return store.Session{}, store.User{}, false
	}
	now := s.now()
	if !now.Before(ss.ExpiresAt) || now.Sub(ss.LastSeenAt) > sessionIdle {
		_ = s.st.DeleteSession(ctx, ss.UserID, ss.ID)
		s.clearCookies(w)
		return store.Session{}, store.User{}, false
	}
	if now.Sub(ss.LastSeenAt) > sessionTouchEvery {
		if err := s.st.TouchSession(ctx, ss.ID); err != nil {
			s.log.Error("session touch", "err", err)
		}
	}
	return ss, u, true
}

// startSession signs u in on this device and responds with the session.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, c *call, u store.User, status int) error {
	token := ids.Token()
	ss, err := s.st.CreateSession(r.Context(), store.NewSession{
		UserID: u.ID, TokenHash: ids.Hash(token), Label: deviceLabel(r.UserAgent()), ExpiresAt: s.now().Add(sessionAbsolute),
	})
	if err != nil {
		return err
	}
	csrf := ids.Token() // rotate on sign-in
	http.SetCookie(w, s.cookie(sessionCookie, token, true, ss.ExpiresAt))
	http.SetCookie(w, s.cookie(csrfCookie, csrf, false, ss.ExpiresAt))
	c.user, c.sess = u, ss
	writeJSON(w, status, sessionView{Authenticated: true, User: viewMe(u), CSRF: csrf})
	return nil
}

type sessionView struct {
	Authenticated     bool    `json:"authenticated"`
	TwoFactorRequired bool    `json:"twoFactorRequired,omitempty"`
	User              *meView `json:"user,omitempty"`
	CSRF              string  `json:"csrf,omitempty"`
}

// dummy returns a fixed hash verified for unknown users, so a login for a
// nonexistent account costs the same as a wrong password.
func (s *Server) dummy() string {
	s.dummyOnce.Do(func() { s.dummyHash = s.hashPassword(ids.Token()) })
	return s.dummyHash
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request, _ *call) error {
	csrf := s.ensureCSRF(w, r)
	if _, u, signedIn := s.authenticate(w, r); signedIn {
		return ok(w, sessionView{Authenticated: true, User: viewMe(u), CSRF: csrf})
	}
	return ok(w, sessionView{CSRF: csrf})
}

type credentials struct {
	Password     string `json:"password"`
	TOTP         string `json:"totp"`
	RecoveryCode string `json:"recoveryCode"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request, c *call) error {
	var in struct {
		Login string `json:"login"`
		credentials
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	login := strings.ToLower(strings.TrimSpace(in.Login))
	if login == "" || in.Password == "" || len(login) > 254 {
		return errInvalid
	}
	if !s.allow(w, rateLogin+":acct:"+c.ip+"|"+login) {
		return nil
	}
	ctx := r.Context()
	u, err := s.st.UserByLogin(ctx, login)
	if errors.Is(err, store.ErrNotFound) {
		_, _ = s.verifyPassword(s.dummy(), in.Password)
		return errInvalidCredentials
	} else if err != nil {
		return err
	}
	if good, err := s.verifyPassword(u.PasswordHash, in.Password); err != nil || !good {
		s.audit(ctx, c, u.ID, store.AuditLoginFailure, "", nil)
		return errInvalidCredentials
	}
	if u.TOTPEnabled {
		if in.TOTP == "" && in.RecoveryCode == "" {
			return ok(w, sessionView{TwoFactorRequired: true})
		}
		if err := s.secondFactor(ctx, c, u, in.credentials); err != nil {
			s.audit(ctx, c, u.ID, store.AuditLoginFailure, "", map[string]string{"reason": "second_factor"})
			return err
		}
	}
	if auth.NeedsRehash(u.PasswordHash) {
		if err := s.st.RehashPassword(ctx, u.ID, s.hashPassword(in.Password)); err != nil {
			s.log.Error("rehash", "err", err)
		}
	}
	if err := s.startSession(w, r, c, u, http.StatusOK); err != nil {
		return err
	}
	s.audit(ctx, c, u.ID, store.AuditLoginSuccess, "", nil)
	return nil
}

// secondFactor checks a TOTP code (replay-protected) or consumes a
// single-use recovery code.
func (s *Server) secondFactor(ctx context.Context, c *call, u store.User, cr credentials) error {
	if !u.TOTPEnabled {
		return nil
	}
	if cr.TOTP != "" {
		secret, err := s.totpSecret(u)
		if err != nil {
			return err
		}
		step, valid := auth.ValidateTOTP(secret, cr.TOTP, s.now())
		if !valid {
			return errInvalidCode
		}
		if err := s.st.UseTOTPStep(ctx, u.ID, step); errors.Is(err, store.ErrConflict) {
			return errInvalidCode
		} else if err != nil {
			return err
		}
		return nil
	}
	h, valid := auth.HashRecoveryCode(cr.RecoveryCode)
	if !valid {
		return errInvalidCode
	}
	if err := s.st.UseRecoveryCode(ctx, u.ID, h); errors.Is(err, store.ErrNotFound) {
		return errInvalidCode
	} else if err != nil {
		return err
	}
	s.audit(ctx, c, u.ID, store.AuditRecoveryCodeUse, "", nil)
	return nil
}

func (s *Server) totpSecret(u store.User) (string, error) {
	pt, err := s.keyring.Decrypt(u.TOTPSecret, []byte("totp:"+u.ID))
	return string(pt), err
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, c *call) error {
	if err := s.st.DeleteSession(r.Context(), c.user.ID, c.sess.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	s.clearCookies(w)
	s.audit(r.Context(), c, c.user.ID, store.AuditLogout, "", nil)
	return noContent(w)
}

func (s *Server) stepUp(w http.ResponseWriter, r *http.Request, c *call) error {
	var in credentials
	if err := decode(r, &in); err != nil {
		return err
	}
	if !s.allow(w, rateTwoFA+":acct:"+c.ip+"|"+c.user.ID) {
		return nil
	}
	ctx := r.Context()
	if good, err := s.verifyPassword(c.user.PasswordHash, in.Password); err != nil || !good {
		return errInvalidCredentials
	}
	if c.user.TOTPEnabled {
		if in.TOTP == "" && in.RecoveryCode == "" {
			return errf(http.StatusUnauthorized, "two_factor_required")
		}
		if err := s.secondFactor(ctx, c, c.user, in); err != nil {
			return err
		}
	}
	if err := s.st.StepUpSession(ctx, c.user.ID, c.sess.ID); err != nil {
		return err
	}
	return noContent(w)
}

type deviceView struct {
	ID         string    `json:"id"`
	Browser    string    `json:"browser"`
	OS         string    `json:"os"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	Current    bool      `json:"current"`
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request, c *call) error {
	list, err := s.st.ListSessions(r.Context(), c.user.ID)
	if err != nil {
		return err
	}
	out := make([]deviceView, 0, len(list))
	now := s.now()
	for _, ss := range list {
		if !now.Before(ss.ExpiresAt) || now.Sub(ss.LastSeenAt) > sessionIdle {
			continue
		}
		b, o, _ := strings.Cut(ss.Label, "/")
		out = append(out, deviceView{ID: ss.ID, Browser: b, OS: o, CreatedAt: ss.CreatedAt, LastSeenAt: ss.LastSeenAt, Current: ss.ID == c.sess.ID})
	}
	return ok(w, out)
}

func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request, c *call) error {
	id := r.PathValue("id")
	if err := s.st.DeleteSession(r.Context(), c.user.ID, id); err != nil {
		return err
	}
	if id == c.sess.ID {
		s.clearCookies(w)
	}
	s.audit(r.Context(), c, c.user.ID, store.AuditSessionRevoke, "", map[string]string{"session": id})
	return noContent(w)
}

func (s *Server) revokeOtherSessions(w http.ResponseWriter, r *http.Request, c *call) error {
	n, err := s.st.DeleteOtherSessions(r.Context(), c.user.ID, c.sess.ID)
	if err != nil {
		return err
	}
	s.audit(r.Context(), c, c.user.ID, store.AuditSessionRevoke, "", map[string]string{"scope": "others"})
	return ok(w, map[string]int{"revoked": n})
}

// deviceLabel reduces a User-Agent to "Browser/OS" for the device list.
// Product names only; nothing user-facing to translate.
func deviceLabel(ua string) string {
	pick := func(pairs [][2]string) string {
		for _, p := range pairs {
			if strings.Contains(ua, p[0]) {
				return p[1]
			}
		}
		return ""
	}
	browser := pick([][2]string{{"Edg", "Edge"}, {"OPR/", "Opera"}, {"Firefox/", "Firefox"}, {"FxiOS", "Firefox"},
		{"SamsungBrowser", "Samsung Internet"}, {"Chrome/", "Chrome"}, {"CriOS", "Chrome"}, {"Safari/", "Safari"}})
	os := pick([][2]string{{"Android", "Android"}, {"iPhone", "iOS"}, {"iPad", "iPadOS"}, {"Windows", "Windows"},
		{"CrOS", "ChromeOS"}, {"Mac OS X", "macOS"}, {"Linux", "Linux"}})
	return browser + "/" + os
}

// audit records an event; failures are logged, never shown to the client.
func (s *Server) audit(ctx context.Context, c *call, actor, event, hh string, meta map[string]string) {
	if err := s.st.Audit(ctx, store.AuditEvent{ActorUserID: actor, HouseholdID: hh, Event: event, IP: c.ip, Meta: meta}); err != nil {
		s.log.Error("audit", "event", event, "err", err)
	}
}
