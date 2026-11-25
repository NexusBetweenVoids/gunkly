package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/NexusBetweenVoids/gunkly/internal/auth"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

type meView struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Email       string `json:"email,omitempty"`
	DisplayName string `json:"displayName"`
	Locale      string `json:"locale"`
	WeekStart   *int   `json:"weekStart"`
	IsAdmin     bool   `json:"isAdmin"`
	TOTPEnabled bool   `json:"totpEnabled"`
	// EmailVerified is false for an address not yet confirmed; EmailOTPEnabled
	// means emailed codes are the second factor; TwoFactor names the active
	// method ("totp", "email" or "").
	EmailVerified   bool   `json:"emailVerified"`
	EmailOTPEnabled bool   `json:"emailOtpEnabled"`
	TwoFactor       string `json:"twoFactor"`
	// EmailReminders: reminders go to the verified address by email.
	EmailReminders bool `json:"emailReminders"`
	// QuietHours are the user's own; null = none. TimeZone is the zone they are
	// read in; null = each household's.
	QuietHours *quietView `json:"quietHours"`
	TimeZone   *string    `json:"timeZone"`
	// Passkeys is the number of registered passkeys.
	Passkeys int `json:"passkeys"`
	// HasPassword is false for an account created by single sign-on until it
	// sets one; SSOLinked: it can sign in through the provider.
	HasPassword bool `json:"hasPassword"`
	SSOLinked   bool `json:"ssoLinked"`
}

func viewMe(u store.User) *meView {
	v := &meView{ID: u.ID, Username: u.Username, Email: u.Email, DisplayName: u.DisplayName, Locale: u.Locale,
		WeekStart: u.WeekStart, IsAdmin: u.IsAdmin, TOTPEnabled: u.TOTPEnabled,
		EmailVerified: u.EmailVerified, EmailOTPEnabled: u.EmailOTPEnabled,
		EmailReminders: u.EmailReminders && u.EmailVerified, Passkeys: u.Passkeys, HasPassword: u.HasPassword(), SSOLinked: u.SSOLinked}
	if u.QuietStartMin != nil && u.QuietEndMin != nil {
		v.QuietHours = &quietView{Start: *u.QuietStartMin, End: *u.QuietEndMin}
	}
	if u.TimeZone != "" {
		v.TimeZone = &u.TimeZone
	}
	switch {
	case u.TOTPEnabled:
		v.TwoFactor = "totp"
	case u.EmailOTPEnabled:
		v.TwoFactor = "email"
	}
	return v
}

func (s *Server) getMe(w http.ResponseWriter, _ *http.Request, c *call) error {
	v := viewMe(c.user)
	v.EmailReminders = v.EmailReminders && s.emailEnabled()
	return ok(w, v)
}

// BCP 47-ish language tag, or empty for "follow the browser".
var localeRE = regexp.MustCompile(`^([a-zA-Z]{2,3}(-[a-zA-Z0-9]{2,8}){0,3})?$`)

func validName(s string) bool {
	n := utf8.RuneCountInString(s)
	return utf8.ValidString(s) && n >= 1 && n <= 100
}

func (s *Server) patchMe(w http.ResponseWriter, r *http.Request, c *call) error {
	var in struct {
		DisplayName *string       `json:"displayName"`
		Locale      *string       `json:"locale"`
		WeekStart   optional[int] `json:"weekStart"`
		// EmailReminders needs email on the instance and a verified address.
		EmailReminders *bool `json:"emailReminders"`
		// QuietHours: null clears. Personal routes only; household quiet hours still apply.
		QuietHours optional[quietView] `json:"quietHours"`
		// TimeZone: IANA name; null or "" follows the household. Used for personal quiet hours and for
		// "today" in chore views of the signed-in user (never for API tokens or MCP).
		TimeZone optional[string] `json:"timeZone"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	var p store.ProfileUpdate
	if in.DisplayName != nil {
		if !validName(*in.DisplayName) {
			return errInvalid
		}
		p.DisplayName = in.DisplayName
	}
	if in.Locale != nil {
		if !localeRE.MatchString(*in.Locale) {
			return errInvalid
		}
		p.Locale = in.Locale
	}
	if in.WeekStart.set {
		p.WeekStart = &in.WeekStart.Value
	}
	if in.QuietHours.set {
		var q *store.QuietHours
		if v := in.QuietHours.Value; v != nil {
			q = &store.QuietHours{StartMin: v.Start, EndMin: v.End}
		}
		p.QuietHours = &q
	}
	if in.TimeZone.set {
		if z := in.TimeZone.Value; z != nil && *z != "" {
			if _, err := time.LoadLocation(*z); err != nil {
				return errInvalid
			}
		}
		p.TimeZone = &in.TimeZone.Value
	}
	if in.EmailReminders != nil && *in.EmailReminders && (!s.emailEnabled() || !c.user.EmailVerified) {
		return errInvalid
	}
	u, err := s.st.UpdateProfile(r.Context(), c.user.ID, p)
	if err != nil {
		return err
	}
	if in.EmailReminders != nil {
		if u, err = s.st.SetEmailReminders(r.Context(), c.user.ID, *in.EmailReminders); err != nil {
			return err
		}
	}
	v := viewMe(u)
	v.EmailReminders = v.EmailReminders && s.emailEnabled()
	return ok(w, v)
}

// checkNewPassword validates against the instance's minimum length.
func (s *Server) checkNewPassword(ctx context.Context, pw string) error {
	st, err := s.st.InstanceSettings(ctx)
	if err != nil {
		return err
	}
	switch err := auth.ValidatePassword(pw, st.MinPasswordLength); {
	case errors.Is(err, auth.ErrPasswordTooShort):
		return errf(http.StatusBadRequest, "password_too_short")
	case errors.Is(err, auth.ErrPasswordTooLong):
		return errf(http.StatusBadRequest, "password_too_long")
	case err != nil:
		return errf(http.StatusBadRequest, "password_invalid")
	}
	return nil
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request, c *call) error {
	var in struct {
		NewPassword string `json:"newPassword"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	if err := s.checkNewPassword(ctx, in.NewPassword); err != nil {
		return err
	}
	if err := s.st.SetPassword(ctx, c.user.ID, s.hashPassword(in.NewPassword), c.sess.ID); err != nil {
		return err
	}
	s.audit(ctx, c, c.user.ID, store.AuditPasswordChange, "", nil)
	return noContent(w)
}

func (s *Server) totpSetup(w http.ResponseWriter, r *http.Request, c *call) error {
	if c.user.TOTPEnabled {
		return errf(http.StatusConflict, "totp_already_enabled")
	}
	secret := auth.GenerateTOTPSecret()
	ct := s.keyring.Encrypt([]byte(secret), []byte("totp:"+c.user.ID))
	if err := s.st.SetPendingTOTP(r.Context(), c.user.ID, ct); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return errf(http.StatusConflict, "totp_already_enabled")
		}
		return err
	}
	account := c.user.Email
	if account == "" {
		account = c.user.Username
	}
	return ok(w, map[string]string{"secret": secret, "uri": auth.TOTPURI(secret, account)})
}

func (s *Server) totpConfirm(w http.ResponseWriter, r *http.Request, c *call) error {
	var in struct {
		Code string `json:"code"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if c.user.TOTPEnabled || c.user.TOTPSecret == "" {
		return errf(http.StatusConflict, "totp_not_pending")
	}
	secret, err := s.totpSecret(c.user)
	if err != nil {
		return err
	}
	step, valid := auth.ValidateTOTP(secret, in.Code, s.now())
	if !valid {
		return errInvalidCode
	}
	codes, hashes := auth.GenerateRecoveryCodes(auth.RecoveryCodeCount)
	if err := s.st.EnableTOTP(r.Context(), c.user.ID, c.sess.ID, step, hashes); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return errf(http.StatusConflict, "totp_not_pending")
		}
		return err
	}
	s.audit(r.Context(), c, c.user.ID, store.AuditTOTPEnable, "", nil)
	return ok(w, map[string][]string{"recoveryCodes": codes})
}

func (s *Server) totpDisable(w http.ResponseWriter, r *http.Request, c *call) error {
	if !c.user.TOTPEnabled {
		return errNotFound
	}
	if s.hosted {
		// 2FA is mandatory there: switch to emailed codes instead.
		return errf(http.StatusConflict, "two_factor_mandatory")
	}
	if err := s.st.DisableTOTP(r.Context(), c.user.ID, c.sess.ID); err != nil {
		return err
	}
	s.audit(r.Context(), c, c.user.ID, store.AuditTOTPDisable, "", nil)
	return noContent(w)
}

type auditView struct {
	Event       string            `json:"event"`
	HouseholdID string            `json:"householdId,omitempty"`
	IP          string            `json:"ip"`
	Meta        map[string]string `json:"meta,omitempty"`
	CreatedAt   time.Time         `json:"createdAt"`
}

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request, c *call) error {
	events, err := s.st.ListAuditForUser(r.Context(), c.user.ID, 200)
	if err != nil {
		return err
	}
	out := make([]auditView, 0, len(events))
	for _, e := range events {
		out = append(out, auditView{Event: e.Event, HouseholdID: e.HouseholdID, IP: e.IP, Meta: e.Meta, CreatedAt: e.CreatedAt})
	}
	return ok(w, out)
}
