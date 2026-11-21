package api

import (
	"net/http"
	"regexp"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

var (
	usernameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{1,31}$`)
	emailRE    = regexp.MustCompile(`^[^@\s]{1,64}@[^@\s]{1,189}$`)
)

type adminUserView struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	Email        string    `json:"email,omitempty"`
	DisplayName  string    `json:"displayName"`
	IsAdmin      bool      `json:"isAdmin"`
	TOTPEnabled  bool      `json:"totpEnabled"`
	CreatedAt    time.Time `json:"createdAt"`
	LastActiveAt time.Time `json:"lastActiveAt"`
}

func (s *Server) adminListUsers(w http.ResponseWriter, r *http.Request, _ *call) error {
	us, err := s.st.AdminListUsers(r.Context())
	if err != nil {
		return err
	}
	out := make([]adminUserView, 0, len(us))
	for _, u := range us {
		out = append(out, adminUserView{ID: u.ID, Username: u.Username, Email: u.Email, DisplayName: u.DisplayName,
			IsAdmin: u.IsAdmin, TOTPEnabled: u.TOTPEnabled, CreatedAt: u.CreatedAt, LastActiveAt: u.LastActiveAt})
	}
	return ok(w, out)
}

func (s *Server) adminCreateUser(w http.ResponseWriter, r *http.Request, c *call) error {
	var in struct {
		newAccount
		IsAdmin bool `json:"isAdmin"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	nu, err := s.validate(ctx, in.newAccount)
	if err != nil {
		return err
	}
	nu.IsAdmin = in.IsAdmin
	u, err := s.st.CreateUser(ctx, nu)
	if err != nil {
		return err
	}
	s.audit(ctx, c, c.user.ID, store.AuditAdminUserCreate, "", map[string]string{"user": u.ID})
	writeJSON(w, http.StatusCreated, adminUserView{ID: u.ID, Username: u.Username, Email: u.Email, DisplayName: u.DisplayName,
		IsAdmin: u.IsAdmin, CreatedAt: u.CreatedAt, LastActiveAt: u.LastActiveAt})
	return nil
}

// NewRandomPassword returns a password for admin and CLI resets (shown once).
func NewRandomPassword() string { return ids.Token()[:24] }

func (s *Server) adminResetPassword(w http.ResponseWriter, r *http.Request, c *call) error {
	ctx := r.Context()
	id := r.PathValue("id")
	pw := NewRandomPassword()
	if err := s.st.SetPassword(ctx, id, s.hashPassword(pw), ""); err != nil {
		return err
	}
	if err := s.st.InvalidateUserLinks(ctx, id); err != nil {
		return err
	}
	s.audit(ctx, c, c.user.ID, store.AuditPasswordReset, "", map[string]string{"user": id})
	return ok(w, map[string]string{"password": pw})
}

type settingsView struct {
	OpenSignup         bool                `json:"openSignup"`
	InviteRegistration bool                `json:"inviteRegistration"`
	MinPasswordLength  int                 `json:"minPasswordLength"`
	Limits             map[limits.Kind]int `json:"limits"`        // effective instance-wide values
	LimitDefaults      map[limits.Kind]int `json:"limitDefaults"` // built-in defaults
}

func (s *Server) adminGetSettings(w http.ResponseWriter, r *http.Request, _ *call) error {
	st, err := s.st.InstanceSettings(r.Context())
	if err != nil {
		return err
	}
	v := settingsView{OpenSignup: st.OpenSignup, InviteRegistration: st.InviteRegistration, MinPasswordLength: st.MinPasswordLength,
		Limits: map[limits.Kind]int{}, LimitDefaults: limits.Defaults}
	for k, d := range limits.Defaults {
		v.Limits[k] = d
		if o, set := st.Limits[string(k)]; set && o > 0 {
			v.Limits[k] = o
		}
	}
	return ok(w, v)
}

func (s *Server) adminPatchSettings(w http.ResponseWriter, r *http.Request, c *call) error {
	var in struct {
		OpenSignup         *bool          `json:"openSignup"`
		InviteRegistration *bool          `json:"inviteRegistration"`
		MinPasswordLength  *int           `json:"minPasswordLength"`
		Limits             map[string]int `json:"limits"` // 0 resets to the default
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if in.MinPasswordLength != nil && (*in.MinPasswordLength < 8 || *in.MinPasswordLength > 128) {
		return errInvalid
	}
	for k, v := range in.Limits {
		if !limits.Known(limits.Kind(k)) || v < 0 || v > 1_000_000 {
			return errInvalid
		}
	}
	err := s.st.UpdateInstanceSettings(r.Context(), store.SettingsUpdate{OpenSignup: in.OpenSignup,
		InviteRegistration: in.InviteRegistration, MinPasswordLength: in.MinPasswordLength, Limits: in.Limits})
	if err != nil {
		return err
	}
	s.audit(r.Context(), c, c.user.ID, store.AuditAdminSettings, "", nil)
	return s.adminGetSettings(w, r, c)
}
