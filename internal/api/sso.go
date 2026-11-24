package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/sso"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// Single sign-on with OpenID Connect (ADR 0018). Self-hosted only: without
// GUNKLY_OIDC_* (and always in hosted mode) every route here answers 404.
//
// The browser leaves for the provider and comes back to the callback, so what
// the flow needs in between (state, nonce, PKCE verifier, what it is for) rides
// in a short-lived sealed cookie: it ties the flow to the browser that started
// it (login CSRF) and needs no server-side state, so it works behind several
// instances. The callback always ends in a redirect to the app with a result
// code in the query, never an error page.

const (
	ssoCookie  = "gunkly_sso"
	ssoPath    = "/api/v1/auth/oidc"
	ssoTTL     = 10 * time.Minute
	ssoMaxAuth = 5 * time.Minute // how old auth_time may be for a step-up

	ssoModeLogin  = "login"
	ssoModeLink   = "link"
	ssoModeStepUp = "stepup"
)

// ssoState is what the sealed cookie carries.
type ssoState struct {
	State     string `json:"s"`
	Nonce     string `json:"n"`
	Verifier  string `json:"v"`
	Mode      string `json:"m"`
	UserID    string `json:"u,omitempty"`
	SessionID string `json:"i,omitempty"`
	Next      string `json:"x,omitempty"`
	Expires   int64  `json:"e"`
}

func (s *Server) ssoCookie(value string, maxAge int) *http.Cookie {
	//nolint:gosec // G124: Secure follows BASE_URL; HttpOnly and SameSite=Lax are set
	return &http.Cookie{Name: ssoCookie, Value: value, Path: ssoPath, HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode, MaxAge: maxAge}
}

// ssoBegin starts a flow: it sets the state cookie and returns the provider's
// authorization URL.
func (s *Server) ssoBegin(ctx context.Context, w http.ResponseWriter, mode, userID, sessionID, next string) (string, error) {
	st := ssoState{State: ids.Token(), Nonce: ids.Token(), Verifier: sso.NewVerifier(), Mode: mode, UserID: userID, SessionID: sessionID,
		Next: safeNext(next), Expires: s.now().Add(ssoTTL).Unix()}
	u, err := s.sso.AuthURL(ctx, st.State, st.Nonce, st.Verifier, mode == ssoModeStepUp)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(st)
	if err != nil {
		return "", err
	}
	http.SetCookie(w, s.ssoCookie(s.keyring.Encrypt(b, []byte("sso-state")), int(ssoTTL.Seconds())))
	return u, nil
}

// readSSOState opens and clears the flow cookie (a flow is single use).
func (s *Server) readSSOState(w http.ResponseWriter, r *http.Request) (ssoState, bool) {
	c, err := r.Cookie(ssoCookie)
	if err != nil || c.Value == "" || len(c.Value) > 2048 {
		return ssoState{}, false
	}
	http.SetCookie(w, s.ssoCookie("", -1))
	b, err := s.keyring.Decrypt(c.Value, []byte("sso-state"))
	if err != nil {
		return ssoState{}, false
	}
	var st ssoState
	if json.Unmarshal(b, &st) != nil || st.Expires < s.now().Unix() || st.State == "" || st.Nonce == "" || st.Verifier == "" {
		return ssoState{}, false
	}
	return st, true
}

// safeNext keeps a post-sign-in path on this site: one path, no scheme or host.
func safeNext(n string) string {
	if n == "" || len(n) > 512 || n[0] != '/' || strings.HasPrefix(n, "//") || strings.ContainsAny(n, "\\\r\n\x00") {
		return "/"
	}
	return n
}

// withParam adds sso=<code> to a path (keeping its query).
func withParam(path, code string) string {
	u, err := url.Parse(path)
	if err != nil {
		return "/?sso=" + code
	}
	q := u.Query()
	q.Set("sso", code)
	u.RawQuery = q.Encode()
	return u.String()
}

// landing is where a finished or failed flow sends the browser.
func landing(st ssoState, ok bool, code string) string {
	switch {
	case st.Mode == ssoModeLogin && ok:
		return st.Next
	case st.Mode == ssoModeLogin || st.Mode == "":
		return "/signin?sso=" + code
	case ok:
		return withParam(st.Next, code)
	}
	return "/account/security?sso=" + code
}

func redirect(w http.ResponseWriter, r *http.Request, to string) {
	w.Header().Set("Cache-Control", "no-store")
	// to is the provider's URL built from the configured issuer, or a path that safeNext vetted.
	http.Redirect(w, r, to, http.StatusSeeOther) //nolint:gosec // G710: see above
}

// ssoStart begins a sign-in: the sign-in page's "Sign in with ..." button
// navigates here.
func (s *Server) ssoStart(w http.ResponseWriter, r *http.Request, _ *call) error {
	u, err := s.ssoBegin(r.Context(), w, ssoModeLogin, "", "", r.URL.Query().Get("next"))
	if err != nil {
		s.log.Warn("sso start", "err", err)
		redirect(w, r, "/signin?sso=unavailable")
		return nil
	}
	redirect(w, r, u)
	return nil
}

// ssoCallback finishes any flow: it checks state, redeems the code, verifies
// the ID token and then acts on what the flow was for.
func (s *Server) ssoCallback(w http.ResponseWriter, r *http.Request, c *call) error {
	st, ok := s.readSSOState(w, r)
	if !ok {
		redirect(w, r, "/signin?sso=failed")
		return nil
	}
	fail := func(code string) error { redirect(w, r, landing(st, false, code)); return nil }
	q := r.URL.Query()
	switch {
	case q.Get("error") != "":
		s.log.Info("sso: the provider refused", "error", logSafe(q.Get("error")))
		if q.Get("error") == "access_denied" {
			return fail("denied")
		}
		return fail("failed")
	case subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(st.State)) != 1,
		q.Get("code") == "",
		q.Has("iss") && q.Get("iss") != s.sso.Issuer(): // RFC 9207: a response from another provider
		return fail("failed")
	}
	ctx := r.Context()
	id, err := s.sso.Exchange(ctx, q.Get("code"), st.Verifier, st.Nonce)
	if err != nil {
		s.log.Warn("sso: sign-in refused", "err", err)
		if errors.Is(err, sso.ErrUnavailable) {
			return fail("unavailable")
		}
		return fail("failed")
	}
	switch st.Mode {
	case ssoModeLogin:
		return s.ssoLogin(w, r, c, st, id)
	case ssoModeLink, ssoModeStepUp:
		return s.ssoSessionFlow(w, r, c, st, id)
	}
	return fail("failed")
}

func (s *Server) ssoLogin(w http.ResponseWriter, r *http.Request, c *call, st ssoState, id sso.Identity) error {
	ctx := r.Context()
	ident, err := s.st.SSOIdentityBySubject(ctx, id.Issuer, id.Subject)
	var u store.User
	switch {
	case err == nil:
		u, err = s.st.UserByID(ctx, ident.UserID)
	case errors.Is(err, store.ErrNotFound):
		set, serr := s.st.InstanceSettings(ctx)
		if serr != nil {
			return serr
		}
		if !set.SSOCreateAccounts {
			redirect(w, r, landing(st, false, "no_account"))
			return nil
		}
		u, ident, err = s.ssoCreateAccount(ctx, c, id)
	}
	if err != nil {
		s.log.Error("sso sign-in", "err", err)
		redirect(w, r, landing(st, false, "failed"))
		return nil
	}
	if err := s.st.TouchSSO(ctx, ident.ID); err != nil {
		s.log.Error("sso touch", "err", err)
	}
	// The provider authenticated the user, including its own multi-factor, so
	// no second factor is asked here (docs/sso.md).
	if err := s.createSession(w, r, c, u); err != nil {
		return err
	}
	s.audit(ctx, c, u.ID, store.AuditLoginSuccess, "", map[string]string{"via": "sso"})
	redirect(w, r, landing(st, true, ""))
	return nil
}

// ssoSessionFlow completes a link or step-up flow for the signed-in user who
// started it.
func (s *Server) ssoSessionFlow(w http.ResponseWriter, r *http.Request, c *call, st ssoState, id sso.Identity) error {
	ctx := r.Context()
	fail := func(code string) error { redirect(w, r, landing(st, false, code)); return nil }
	ss, u, signedIn := s.authenticate(w, r)
	if !signedIn || ss.ID != st.SessionID || u.ID != st.UserID {
		return fail("failed")
	}
	c.user, c.sess = u, ss
	if st.Mode == ssoModeStepUp {
		own, err := s.st.SSOIdentityForUser(ctx, u.ID, id.Issuer)
		if err != nil || own.Subject != id.Subject {
			return fail("failed") // another provider account than the one linked
		}
		if !id.AuthTime.IsZero() && s.now().Sub(id.AuthTime) > ssoMaxAuth {
			return fail("failed") // the provider did not ask the user again
		}
		if err := s.st.StepUpSession(ctx, u.ID, ss.ID); err != nil {
			return err
		}
		redirect(w, r, landing(st, true, "stepup"))
		return nil
	}
	// Linking needs the step-up it was started with to still count.
	if ss.StepUpAt.IsZero() || s.now().Sub(ss.StepUpAt) > stepUpWindow {
		return fail("failed")
	}
	switch err := s.st.LinkSSO(ctx, u.ID, ss.ID, id.Issuer, id.Subject); {
	case errors.Is(err, store.ErrConflict):
		return fail("already_linked")
	case err != nil:
		return err
	}
	s.audit(ctx, c, u.ID, store.AuditSSOLink, "", nil)
	redirect(w, r, landing(st, true, "linked"))
	return nil
}

// ssoCreateAccount makes the account of a first sign-in by an unknown
// subject (only when the admin allows it). The username comes from
// preferred_username, made safe and unique; the email is kept only when the
// provider vouches for it and email is on here.
func (s *Server) ssoCreateAccount(ctx context.Context, c *call, id sso.Identity) (store.User, store.SSOIdentity, error) {
	in := store.NewSSOUser{Issuer: id.Issuer, Subject: id.Subject}
	base := ssoUsername(id.Username)
	if base == "" {
		base, _, _ = strings.Cut(id.Email, "@")
		base = ssoUsername(base)
	}
	if base == "" {
		base = "user"
	}
	in.DisplayName = strings.TrimSpace(id.Name)
	if !validName(in.DisplayName) {
		in.DisplayName = base
	}
	if id.EmailVerified && id.Email != "" && s.emailEnabled() && emailRE.MatchString(id.Email) {
		if taken, err := s.st.EmailTaken(ctx, id.Email, ""); err != nil {
			return store.User{}, store.SSOIdentity{}, err
		} else if !taken {
			in.Email = id.Email
		}
	}
	for i := 0; i < 25; i++ {
		in.Username = base
		switch {
		case i == 1:
			in.Username = base + "-2"
		case i > 1 && i < 20:
			in.Username = base + "-" + strconv.Itoa(i+1)
		case i >= 20:
			in.Username = base + "-" + ids.Token()[:6]
		}
		u, err := s.st.CreateSSOUser(ctx, in)
		if errors.Is(err, store.ErrConflict) {
			// A concurrent first sign-in of the same subject won: use its account.
			if ident, ierr := s.st.SSOIdentityBySubject(ctx, id.Issuer, id.Subject); ierr == nil {
				u, uerr := s.st.UserByID(ctx, ident.UserID)
				return u, ident, uerr
			}
			if in.Email != "" {
				in.Email = "" // the address may be what clashed: retry this username without it
				i--
			}
			continue
		} else if err != nil {
			return store.User{}, store.SSOIdentity{}, err
		}
		s.audit(ctx, c, u.ID, store.AuditSSOSignup, "", nil)
		ident, err := s.st.SSOIdentityBySubject(ctx, id.Issuer, id.Subject)
		return u, ident, err
	}
	return store.User{}, store.SSOIdentity{}, errors.New("no free username")
}

// ssoUsername reduces a provider's preferred_username to the characters a
// Gunkly username allows (letters, digits, dot, dash, underscore; starts with
// a letter or digit; at most 28 so a suffix still fits in 32).
func ssoUsername(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.' || r == '-' || r == '_':
			if b.Len() > 0 {
				b.WriteRune(r)
			}
		case b.Len() > 0:
			b.WriteByte('-')
		}
		if b.Len() >= 28 {
			break
		}
	}
	out := strings.TrimRight(b.String(), "._-")
	if utf8.RuneCountInString(out) < 2 {
		return ""
	}
	return out
}

type ssoStatus struct {
	Name      string     `json:"name"`
	Linked    bool       `json:"linked"`
	LinkedAt  *time.Time `json:"linkedAt"`
	CanUnlink bool       `json:"canUnlink"` // there is a password or a passkey to sign in with instead
}

func (s *Server) getSSO(w http.ResponseWriter, r *http.Request, c *call) error {
	v := ssoStatus{Name: s.sso.Name(), CanUnlink: c.user.HasPassword() || c.user.Passkeys > 0}
	ident, err := s.st.SSOIdentityForUser(r.Context(), c.user.ID, s.sso.Issuer())
	switch {
	case err == nil:
		v.Linked, v.LinkedAt = true, &ident.CreatedAt
	case !errors.Is(err, store.ErrNotFound):
		return err
	}
	return ok(w, v)
}

// beginSessionFlow answers a link or step-up request with the provider's URL
// for the browser to go to.
func (s *Server) beginSessionFlow(w http.ResponseWriter, r *http.Request, c *call, mode, next string) error {
	u, err := s.ssoBegin(r.Context(), w, mode, c.user.ID, c.sess.ID, next)
	if err != nil {
		s.log.Warn("sso begin", "err", err)
		return errf(http.StatusBadGateway, "sso_unavailable")
	}
	return ok(w, map[string]string{"url": u})
}

func (s *Server) linkSSO(w http.ResponseWriter, r *http.Request, c *call) error {
	if _, err := s.st.SSOIdentityForUser(r.Context(), c.user.ID, s.sso.Issuer()); err == nil {
		return errf(http.StatusConflict, "sso_already_linked")
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	return s.beginSessionFlow(w, r, c, ssoModeLink, "/account/security")
}

func (s *Server) unlinkSSO(w http.ResponseWriter, r *http.Request, c *call) error {
	err := s.st.UnlinkSSO(r.Context(), c.user.ID, c.sess.ID, s.sso.Issuer())
	if errors.Is(err, store.ErrConflict) {
		return errf(http.StatusConflict, "sso_last_login")
	} else if err != nil {
		return err
	}
	s.audit(r.Context(), c, c.user.ID, store.AuditSSOUnlink, "", nil)
	return noContent(w)
}

// stepUpSSO starts a step-up through the provider (it is asked to
// authenticate the user again): the way back for accounts without a password.
func (s *Server) stepUpSSO(w http.ResponseWriter, r *http.Request, c *call) error {
	var in struct {
		Next string `json:"next"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if _, err := s.st.SSOIdentityForUser(r.Context(), c.user.ID, s.sso.Issuer()); errors.Is(err, store.ErrNotFound) {
		return errNotFound
	} else if err != nil {
		return err
	}
	return s.beginSessionFlow(w, r, c, ssoModeStepUp, in.Next)
}
