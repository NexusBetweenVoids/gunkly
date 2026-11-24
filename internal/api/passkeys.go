package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/NexusBetweenVoids/gunkly/internal/auth"
	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// Passkeys (WebAuthn, ADR 0018): a sign-in method that counts as password and
// second factor at once, and a second factor after a password. The ceremonies
// are two calls each: "options" stores the library's session data for five
// minutes (single use, bound to the account), "finish" consumes it.

var errInvalidPasskey = errf(http.StatusUnauthorized, "invalid_passkey")

// passkeyRP returns the relying party, built once from GUNKLY_BASE_URL, or nil
// when the instance has no https (or localhost) address passkeys could bind to.
func (s *Server) passkeyRP() *webauthn.WebAuthn {
	s.pkOnce.Do(func() {
		id, origin, ok := auth.PasskeyRP(s.baseURL)
		if !ok {
			return
		}
		w, err := webauthn.New(&webauthn.Config{RPID: id, RPDisplayName: "Gunkly", RPOrigins: []string{origin},
			AttestationPreference: protocol.PreferNoAttestation})
		if err != nil {
			s.log.Error("passkeys unavailable", "err", err)
			return
		}
		s.pk = w
	})
	return s.pk
}

// passkeyUser adapts an account and its passkeys to the library.
type passkeyUser struct {
	u    store.User
	keys []store.Passkey
}

func (p passkeyUser) WebAuthnID() []byte          { return []byte(p.u.ID) }
func (p passkeyUser) WebAuthnName() string        { return p.u.Username }
func (p passkeyUser) WebAuthnDisplayName() string { return p.u.DisplayName }
func (p passkeyUser) WebAuthnCredentials() []webauthn.Credential {
	out := make([]webauthn.Credential, 0, len(p.keys))
	for _, k := range p.keys {
		tr := make([]protocol.AuthenticatorTransport, 0, len(k.Transports))
		for _, t := range k.Transports {
			tr = append(tr, protocol.AuthenticatorTransport(t))
		}
		out = append(out, webauthn.Credential{ID: k.CredentialID, PublicKey: k.PublicKey, Transport: tr,
			Flags:         webauthn.NewCredentialFlags(protocol.AuthenticatorFlags(k.Flags)),
			Authenticator: webauthn.Authenticator{AAGUID: k.AAGUID, SignCount: k.SignCount}})
	}
	return out
}

// passkeyAnswer is the browser's reply to a ceremony: the challenge ID from
// the options call and the credential as navigator.credentials returned it
// (JSON form).
type passkeyAnswer struct {
	Challenge  string          `json:"challenge"`
	Credential json.RawMessage `json:"credential"`
}

// savePasskeySession stores the library's session data and answers with the
// options for the browser.
func (s *Server) savePasskeySession(ctx context.Context, w http.ResponseWriter, purpose, userID string, options any, sd *webauthn.SessionData) error {
	b, err := json.Marshal(sd)
	if err != nil {
		return err
	}
	id, err := s.st.SavePasskeyChallenge(ctx, purpose, userID, string(b))
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"challenge": id, "options": options})
}

// takePasskeySession consumes the stored session data of a ceremony.
func (s *Server) takePasskeySession(ctx context.Context, ans *passkeyAnswer, purpose, userID string) (sd webauthn.SessionData, err error) {
	if ans == nil || ans.Challenge == "" || len(ans.Challenge) > 128 {
		return sd, errInvalidPasskey
	}
	data, err := s.st.TakePasskeyChallenge(ctx, ans.Challenge, purpose, userID)
	if errors.Is(err, store.ErrNotFound) {
		return sd, errInvalidPasskey
	} else if err != nil {
		return sd, err
	}
	if json.Unmarshal([]byte(data), &sd) != nil {
		return sd, errInvalidPasskey
	}
	return sd, nil
}

// afterAssertion records a verified assertion. A signature counter that did
// not move forward means a cloned authenticator or a replay: the sign-in is
// refused and logged (never the credential).
func (s *Server) afterAssertion(ctx context.Context, c *call, u store.User, keys []store.Passkey, cred *webauthn.Credential) error {
	var key *store.Passkey
	for i := range keys {
		if string(keys[i].CredentialID) == string(cred.ID) {
			key = &keys[i]
		}
	}
	if key == nil {
		return errInvalidPasskey
	}
	if cred.Authenticator.CloneWarning {
		return s.passkeyRegression(ctx, c, u, key)
	}
	if err := s.st.UsePasskey(ctx, u.ID, key.ID, cred.Authenticator.SignCount, cred.Flags.MsgpByte()); errors.Is(err, store.ErrConflict) {
		return s.passkeyRegression(ctx, c, u, key)
	} else if err != nil {
		return err
	}
	return nil
}

func (s *Server) passkeyRegression(ctx context.Context, c *call, u store.User, key *store.Passkey) error {
	s.log.Warn("passkey sign count did not increase: possible cloned authenticator", "user", u.ID, "passkey", key.ID)
	s.audit(ctx, c, u.ID, store.AuditLoginFailure, "", map[string]string{"reason": "passkey_sign_count", "passkey": key.ID})
	return errInvalidPasskey
}

// verifyPasskey checks a signed-in or password-verified user's assertion for
// the given purpose (second factor or step-up).
func (s *Server) verifyPasskey(ctx context.Context, c *call, u store.User, ans *passkeyAnswer, purpose string) error {
	rp := s.passkeyRP()
	if rp == nil {
		return errInvalidPasskey
	}
	sd, err := s.takePasskeySession(ctx, ans, purpose, u.ID)
	if err != nil {
		return err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(ans.Credential)
	if err != nil {
		return errInvalidPasskey
	}
	keys, err := s.st.ListPasskeys(ctx, u.ID)
	if err != nil {
		return err
	}
	cred, err := rp.ValidateLogin(passkeyUser{u, keys}, sd, parsed)
	if err != nil {
		s.log.Debug("passkey assertion rejected", "err", err)
		return errInvalidPasskey
	}
	return s.afterAssertion(ctx, c, u, keys, cred)
}

// beginAssertion starts an assertion for u's own passkeys.
func (s *Server) beginAssertion(w http.ResponseWriter, r *http.Request, u store.User, purpose string, uv protocol.UserVerificationRequirement) error {
	rp := s.passkeyRP()
	ctx := r.Context()
	if rp == nil || u.Passkeys == 0 {
		return errNotFound
	}
	keys, err := s.st.ListPasskeys(ctx, u.ID)
	if err != nil {
		return err
	}
	opts, sd, err := rp.BeginLogin(passkeyUser{u, keys}, webauthn.WithUserVerification(uv))
	if err != nil {
		return err
	}
	return s.savePasskeySession(ctx, w, purpose, u.ID, opts, sd)
}

// passkeySecondOptions: the options for a passkey as second factor. The
// password is checked first, like at sign-in, so the endpoint reveals nothing
// about accounts it has not authenticated.
func (s *Server) passkeySecondOptions(w http.ResponseWriter, r *http.Request, c *call) error {
	var in struct {
		Login    string `json:"login"`
		Password string `json:"password"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	u, stop, err := s.checkLoginPassword(w, r, c, in.Login, in.Password)
	if stop || err != nil {
		return err
	}
	return s.beginAssertion(w, r, u, store.PasskeySecond, protocol.VerificationPreferred)
}

// passkeyStepUpOptions: the options for re-verifying a signed-in user with a
// passkey (user verification required: it replaces password and second factor).
func (s *Server) passkeyStepUpOptions(w http.ResponseWriter, r *http.Request, c *call) error {
	return s.beginAssertion(w, r, c.user, store.PasskeyStepUp, protocol.VerificationRequired)
}

// passkeyLoginOptions: the options for a passwordless sign-in with a
// discoverable credential. User verification is required, which is what lets
// the passkey stand in for password and second factor.
func (s *Server) passkeyLoginOptions(w http.ResponseWriter, r *http.Request, _ *call) error {
	rp := s.passkeyRP()
	if rp == nil {
		return errNotFound
	}
	opts, sd, err := rp.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return err
	}
	return s.savePasskeySession(r.Context(), w, store.PasskeyLogin, "", opts, sd)
}

// passkeyLogin finishes a passwordless sign-in.
func (s *Server) passkeyLogin(w http.ResponseWriter, r *http.Request, c *call) error {
	var ans passkeyAnswer
	if err := decode(r, &ans); err != nil {
		return err
	}
	rp := s.passkeyRP()
	if rp == nil {
		return errNotFound
	}
	ctx := r.Context()
	sd, err := s.takePasskeySession(ctx, &ans, store.PasskeyLogin, "")
	if err != nil {
		return err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(ans.Credential)
	if err != nil {
		return errInvalidPasskey
	}
	var keys []store.Passkey
	user, cred, err := rp.ValidatePasskeyLogin(func(rawID, userHandle []byte) (webauthn.User, error) {
		k, err := s.st.PasskeyByCredentialID(ctx, rawID)
		if err != nil {
			return nil, err
		}
		if string(userHandle) != k.UserID {
			return nil, store.ErrNotFound
		}
		u, err := s.st.UserByID(ctx, k.UserID)
		if err != nil {
			return nil, err
		}
		if keys, err = s.st.ListPasskeys(ctx, u.ID); err != nil {
			return nil, err
		}
		return passkeyUser{u, keys}, nil
	}, sd, parsed)
	if err != nil {
		s.log.Debug("passkey sign-in rejected", "err", err)
		return errInvalidPasskey
	}
	u := user.(passkeyUser).u
	if err := s.afterAssertion(ctx, c, u, keys, cred); err != nil {
		return err
	}
	if err := s.startSession(w, r, c, u, http.StatusOK); err != nil {
		return err
	}
	s.audit(ctx, c, u.ID, store.AuditLoginSuccess, "", map[string]string{"via": "passkey"})
	return nil
}

type passkeyView struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
}

func viewPasskey(k store.Passkey) passkeyView {
	return passkeyView{ID: k.ID, Name: k.Name, CreatedAt: k.CreatedAt, LastUsedAt: timePtr(k.LastUsedAt)}
}

func (s *Server) listPasskeys(w http.ResponseWriter, r *http.Request, c *call) error {
	keys, err := s.st.ListPasskeys(r.Context(), c.user.ID)
	if err != nil {
		return err
	}
	out := make([]passkeyView, 0, len(keys))
	for _, k := range keys {
		out = append(out, viewPasskey(k))
	}
	return ok(w, out)
}

// passkeyRegisterOptions starts the registration of a new passkey (step-up).
// The credential must be discoverable and user-verified so it can sign in
// without a password; attestation is "none" (we never ask which device).
func (s *Server) passkeyRegisterOptions(w http.ResponseWriter, r *http.Request, c *call) error {
	rp := s.passkeyRP()
	if rp == nil {
		return errf(http.StatusBadRequest, "passkeys_require_https")
	}
	ctx := r.Context()
	max, err := s.limits.Limit(ctx, limits.PasskeysPerUser, "")
	if err != nil {
		return err
	}
	if c.user.Passkeys >= max {
		return limits.ErrExceeded
	}
	keys, err := s.st.ListPasskeys(ctx, c.user.ID)
	if err != nil {
		return err
	}
	pu := passkeyUser{c.user, keys}
	opts, sd, err := rp.BeginRegistration(pu,
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey: protocol.ResidentKeyRequirementRequired, RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification: protocol.VerificationRequired}),
		webauthn.WithConveyancePreference(protocol.PreferNoAttestation),
		webauthn.WithExclusions(webauthn.Credentials(pu.WebAuthnCredentials()).CredentialDescriptors()))
	if err != nil {
		return err
	}
	return s.savePasskeySession(ctx, w, store.PasskeyRegister, c.user.ID, opts, sd)
}

// registerPasskey finishes a registration (step-up). The first second factor
// of an account also issues recovery codes, shown once.
func (s *Server) registerPasskey(w http.ResponseWriter, r *http.Request, c *call) error {
	var in struct {
		passkeyAnswer
		Name string `json:"name"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	in.Name = strings.TrimSpace(in.Name)
	if !validName(in.Name) {
		return errInvalid
	}
	rp := s.passkeyRP()
	if rp == nil {
		return errf(http.StatusBadRequest, "passkeys_require_https")
	}
	ctx := r.Context()
	sd, err := s.takePasskeySession(ctx, &in.passkeyAnswer, store.PasskeyRegister, c.user.ID)
	if err != nil {
		return errf(http.StatusBadRequest, "passkey_failed")
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(in.Credential)
	if err != nil {
		return errf(http.StatusBadRequest, "passkey_failed")
	}
	keys, err := s.st.ListPasskeys(ctx, c.user.ID)
	if err != nil {
		return err
	}
	cred, err := rp.CreateCredential(passkeyUser{c.user, keys}, sd, parsed)
	if err != nil {
		s.log.Debug("passkey registration rejected", "err", err)
		return errf(http.StatusBadRequest, "passkey_failed")
	}
	max, err := s.limits.Limit(ctx, limits.PasskeysPerUser, "")
	if err != nil {
		return err
	}
	tr := make([]string, 0, len(cred.Transport))
	for _, t := range cred.Transport {
		tr = append(tr, string(t))
	}
	codes, hashes := auth.GenerateRecoveryCodes(auth.RecoveryCodeCount)
	k, issued, err := s.st.AddPasskey(ctx, c.user.ID, c.sess.ID, store.NewPasskey{Name: in.Name, CredentialID: cred.ID, PublicKey: cred.PublicKey,
		SignCount: cred.Authenticator.SignCount, Transports: tr, AAGUID: cred.Authenticator.AAGUID, Flags: cred.Flags.MsgpByte()}, max, hashes)
	if errors.Is(err, store.ErrConflict) {
		return errf(http.StatusConflict, "passkey_exists")
	} else if err != nil {
		return err
	}
	s.audit(ctx, c, c.user.ID, store.AuditPasskeyAdd, "", map[string]string{"passkey": k.ID})
	out := map[string]any{"passkey": viewPasskey(k)}
	if issued {
		out["recoveryCodes"] = codes
	}
	writeJSON(w, http.StatusCreated, out)
	return nil
}

// deletePasskey removes a passkey (step-up). Hosted mode needs a second
// factor, so its last one cannot go.
func (s *Server) deletePasskey(w http.ResponseWriter, r *http.Request, c *call) error {
	id := r.PathValue("id")
	err := s.st.DeletePasskey(r.Context(), c.user.ID, c.sess.ID, id, s.hosted)
	if errors.Is(err, store.ErrLastSignIn) {
		return errf(http.StatusConflict, "passkey_last_login")
	} else if errors.Is(err, store.ErrConflict) {
		return errf(http.StatusConflict, "two_factor_mandatory")
	} else if err != nil {
		return err
	}
	s.audit(r.Context(), c, c.user.ID, store.AuditPasskeyRemove, "", map[string]string{"passkey": id})
	return noContent(w)
}
