package api

import (
	"context"
	"errors"
	"maps"
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

const settingsCacheTTL = 5 * time.Second

// sessionLifetimes returns the admin-configured idle and absolute session
// lifetimes. They are read on use, cached briefly, and the cache is dropped
// when settings change here. On a read error the last known (or default)
// values are used so a database hiccup does not sign everyone out.
func (s *Server) sessionLifetimes() (idle, abs time.Duration) {
	const day = 24 * time.Hour
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	if s.lifeIdle == 0 || time.Since(s.lifeAt) > settingsCacheTTL {
		st, err := s.st.InstanceSettings(context.Background())
		switch {
		case err == nil:
			s.lifeIdle, s.lifeMax = time.Duration(st.SessionIdleDays)*day, time.Duration(st.SessionMaxDays)*day
			s.lifeAt = time.Now()
		case s.lifeIdle == 0:
			s.lifeIdle, s.lifeMax = store.DefaultSessionIdleDays*day, store.DefaultSessionMaxDays*day
			s.log.Error("session lifetimes", "err", err)
		default:
			s.log.Error("session lifetimes", "err", err)
		}
	}
	return s.lifeIdle, s.lifeMax
}

func (s *Server) forgetLifetimes() {
	s.lifeMu.Lock()
	s.lifeIdle = 0
	s.lifeMu.Unlock()
}

// sessionExpired applies idle and absolute expiry. The absolute limit is
// measured from sign-in, so a changed setting also applies to existing
// sessions; ExpiresAt is only the hard ceiling set at creation.
func (s *Server) sessionExpired(ss store.Session, now time.Time) bool {
	idle, abs := s.sessionLifetimes()
	return !now.Before(ss.ExpiresAt) || now.Sub(ss.LastSeenAt) > idle || !now.Before(ss.CreatedAt.Add(abs))
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
	if s.sessionExpired(ss, now) {
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
		UserID: u.ID, TokenHash: ids.Hash(token), Label: deviceLabel(r.UserAgent()), ExpiresAt: s.now().Add(store.MaxSessionMaxDays * 24 * time.Hour),
	})
	if err != nil {
		return err
	}
	csrf := ids.Token() // rotate on sign-in
	_, abs := s.sessionLifetimes()
	cookieEnd := ss.CreatedAt.Add(abs)
	http.SetCookie(w, s.cookie(sessionCookie, token, true, cookieEnd))
	http.SetCookie(w, s.cookie(csrfCookie, csrf, false, cookieEnd))
	c.user, c.sess = u, ss
	writeJSON(w, status, sessionView{Authenticated: true, User: viewMe(u), CSRF: csrf})
	return nil
}

type sessionView struct {
	Authenticated     bool    `json:"authenticated"`
	TwoFactorRequired bool    `json:"twoFactorRequired,omitempty"`
	Method            string  `json:"method,omitempty"` // second factor asked for: "totp" or "email"
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
	EmailCode    string `json:"emailCode"`
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
	if !s.allow(w, rateLogin+":acct:"+c.rip+"|"+login) {
		return nil
	}
	ctx := r.Context()
	u, err := s.st.UserByLogin(ctx, login)
	// Per-account failure counter across all IPs. Unknown logins are counted
	// under the login string so both cases behave alike.
	pwKey := "l:" + login
	if err == nil {
		pwKey = "u:" + u.ID
	}
	if s.acctDelay(w, pwKey) {
		return nil
	}
	if errors.Is(err, store.ErrNotFound) {
		_, _ = s.verifyPassword(s.dummy(), in.Password)
		s.acct.fail(pwKey)
		return errInvalidCredentials
	} else if err != nil {
		return err
	}
	if good, err := s.verifyPassword(u.PasswordHash, in.Password); err != nil || !good {
		s.acct.fail(pwKey)
		s.audit(ctx, c, u.ID, store.AuditLoginFailure, "", nil)
		return errInvalidCredentials
	}
	if u.TOTPEnabled || u.EmailOTPEnabled {
		if in.TOTP == "" && in.RecoveryCode == "" && in.EmailCode == "" {
			if !u.EmailOTPEnabled {
				return ok(w, sessionView{TwoFactorRequired: true, Method: "totp"})
			}
			// The password was right: email the code (again, on a repeat).
			if wait, err := s.sendLoginCode(ctx, c, u, true); err != nil {
				return err
			} else if wait > 0 {
				return tooMany(w, wait)
			}
			return ok(w, sessionView{TwoFactorRequired: true, Method: "email"})
		}
		if s.acctDelay(w, "2fa:"+u.ID) {
			return nil
		}
		if err := s.secondFactor(ctx, c, u, in.credentials); err != nil {
			s.acct.fail("2fa:" + u.ID)
			s.audit(ctx, c, u.ID, store.AuditLoginFailure, "", map[string]string{"reason": "second_factor"})
			return err
		}
		s.acct.reset("2fa:" + u.ID)
	}
	s.acct.reset(pwKey)
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

// secondFactor checks the user's second factor: a TOTP code (replay-protected),
// an emailed code (single-use, attempt-limited) or a single-use recovery code.
func (s *Server) secondFactor(ctx context.Context, c *call, u store.User, cr credentials) error {
	if !u.TOTPEnabled && !u.EmailOTPEnabled {
		return nil
	}
	switch {
	case cr.EmailCode != "":
		if !u.EmailOTPEnabled {
			return errInvalidCode
		}
		good, err := s.st.ConsumeEmailOTP(ctx, u.ID, func(challenge string) string {
			return auth.HashEmailOTP(challenge, cr.EmailCode)
		})
		if err != nil {
			return err
		}
		if !good {
			return errInvalidCode
		}
		return nil
	case cr.TOTP != "":
		if !u.TOTPEnabled {
			return errInvalidCode
		}
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
	if !s.allow(w, rateTwoFA+":acct:"+c.rip+"|"+c.user.ID) {
		return nil
	}
	ctx := r.Context()
	pwKey, tfKey := "u:"+c.user.ID, "2fa:"+c.user.ID
	if s.acctDelay(w, pwKey) {
		return nil
	}
	if good, err := s.verifyPassword(c.user.PasswordHash, in.Password); err != nil || !good {
		s.acct.fail(pwKey)
		return errInvalidCredentials
	}
	if c.user.TOTPEnabled || c.user.EmailOTPEnabled {
		if in.TOTP == "" && in.RecoveryCode == "" && in.EmailCode == "" {
			if c.user.EmailOTPEnabled {
				if wait, err := s.sendLoginCode(ctx, c, c.user, false); err != nil {
					return err
				} else if wait > 0 {
					return tooMany(w, wait)
				}
			}
			return errf(http.StatusUnauthorized, "two_factor_required")
		}
		if s.acctDelay(w, tfKey) {
			return nil
		}
		if err := s.secondFactor(ctx, c, c.user, in); err != nil {
			s.acct.fail(tfKey)
			return err
		}
		s.acct.reset(tfKey)
	}
	s.acct.reset(pwKey)
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
		if s.sessionExpired(ss, now) {
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
	if c.tok != nil && meta["token"] == "" { // name the token behind an action it performed (never its secret)
		meta = maps.Clone(meta)
		if meta == nil {
			meta = map[string]string{}
		}
		meta["token"] = c.tok.ID
	}
	if err := s.st.Audit(ctx, store.AuditEvent{ActorUserID: actor, HouseholdID: hh, Event: event, IP: c.ip, Meta: meta}); err != nil {
		s.log.Error("audit", "event", event, "err", err)
	}
}
