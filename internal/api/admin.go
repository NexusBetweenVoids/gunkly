package api

import (
	"maps"
	"net/http"
	"regexp"
	"slices"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/auth"
	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/notify"
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

// adminResetPassword sets a random password for a non-admin account
// (self-hosted only; hosted accounts use the email reset flow). The reset is
// written to the target's own audit log and signs them out everywhere. The
// admin therefore cannot silently take over an account, but on a self-hosted
// instance an admin can still sign in as a non-admin user; see ADR 0009.
func (s *Server) adminResetPassword(w http.ResponseWriter, r *http.Request, c *call) error {
	if s.hosted {
		return errf(http.StatusForbidden, "disabled_in_hosted_mode")
	}
	ctx := r.Context()
	id := r.PathValue("id")
	target, err := s.st.UserByID(ctx, id)
	if err != nil {
		return err
	}
	if target.IsAdmin {
		return errf(http.StatusForbidden, "admin_target") // admins are reset with the CLI
	}
	pw := NewRandomPassword()
	if err := s.st.SetPassword(ctx, id, s.hashPassword(pw), ""); err != nil {
		return err
	}
	s.audit(ctx, c, c.user.ID, store.AuditPasswordReset, "", map[string]string{"user": id})
	s.audit(ctx, c, id, store.AuditPasswordReset, "", map[string]string{"by": "admin", "admin": c.user.ID})
	return ok(w, map[string]string{"password": pw})
}

type settingsView struct {
	OpenSignup            bool                `json:"openSignup"`
	InviteRegistration    bool                `json:"inviteRegistration"`
	MinPasswordLength     int                 `json:"minPasswordLength"`
	SessionIdleDays       int                 `json:"sessionIdleDays"`
	SessionMaxDays        int                 `json:"sessionMaxDays"`
	AuditRetentionDays    int                 `json:"auditRetentionDays"`
	DeliveryRetentionDays int                 `json:"deliveryRetentionDays"`
	Limits                map[limits.Kind]int `json:"limits"`        // effective instance-wide values
	LimitDefaults         map[limits.Kind]int `json:"limitDefaults"` // built-in defaults
	// EnabledNotificationKinds are the notification services users may add
	// channels for; AvailableNotificationKinds lists every kind that can be
	// enabled. EgressGuard is the effective guard (forced on when hosted).
	EnabledNotificationKinds   []string `json:"enabledNotificationKinds"`
	AvailableNotificationKinds []string `json:"availableNotificationKinds"`
	EgressGuard                bool     `json:"egressGuard"`
	EgressGuardLocked          bool     `json:"egressGuardLocked"` // hosted mode: cannot be turned off
}

func (s *Server) adminGetSettings(w http.ResponseWriter, r *http.Request, _ *call) error {
	st, err := s.st.InstanceSettings(r.Context())
	if err != nil {
		return err
	}
	minLen := st.MinPasswordLength
	if minLen <= 0 {
		minLen = auth.DefaultMinPasswordLen // 0 is stored as "unset"; show the effective value
	}
	v := settingsView{OpenSignup: st.OpenSignup, InviteRegistration: st.InviteRegistration, MinPasswordLength: minLen,
		SessionIdleDays: st.SessionIdleDays, SessionMaxDays: st.SessionMaxDays,
		AuditRetentionDays: st.AuditRetentionDays, DeliveryRetentionDays: st.DeliveryRetentionDays,
		Limits: map[limits.Kind]int{}, LimitDefaults: limits.Defaults,
		EnabledNotificationKinds: append([]string{}, slices.Sorted(maps.Keys(kindSet(st)))...), AvailableNotificationKinds: notify.AllKinds(),
		EgressGuard: s.hosted || st.EgressGuard, EgressGuardLocked: s.hosted}
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
		OpenSignup            *bool          `json:"openSignup"`
		InviteRegistration    *bool          `json:"inviteRegistration"`
		MinPasswordLength     *int           `json:"minPasswordLength"`
		SessionIdleDays       *int           `json:"sessionIdleDays"`
		SessionMaxDays        *int           `json:"sessionMaxDays"`
		AuditRetentionDays    *int           `json:"auditRetentionDays"`
		DeliveryRetentionDays *int           `json:"deliveryRetentionDays"`
		Limits                map[string]int `json:"limits"` // 0 resets to the default
		// null resets to the built-in defaults; [] enables nothing.
		EnabledNotificationKinds optional[[]string] `json:"enabledNotificationKinds"`
		EgressGuard              *bool              `json:"egressGuard"`
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
	upd := store.SettingsUpdate{OpenSignup: in.OpenSignup,
		InviteRegistration: in.InviteRegistration, MinPasswordLength: in.MinPasswordLength, Limits: in.Limits, EgressGuard: in.EgressGuard,
		SessionIdleDays: in.SessionIdleDays, SessionMaxDays: in.SessionMaxDays,
		AuditRetentionDays: in.AuditRetentionDays, DeliveryRetentionDays: in.DeliveryRetentionDays}
	if in.EgressGuard != nil && !*in.EgressGuard && s.hosted {
		return errf(http.StatusBadRequest, "egress_guard_locked")
	}
	if in.EnabledNotificationKinds.set {
		kinds := []string(nil)
		if in.EnabledNotificationKinds.Value != nil {
			kinds = []string{}
			for _, k := range *in.EnabledNotificationKinds.Value {
				if !slices.Contains(notify.AllKinds(), k) {
					return errInvalid
				}
				if !slices.Contains(kinds, k) {
					kinds = append(kinds, k)
				}
			}
			slices.Sort(kinds)
		}
		upd.NotificationKinds = &kinds
	}
	err := s.st.UpdateInstanceSettings(r.Context(), upd)
	s.forgetLifetimes()
	if err != nil {
		return err
	}
	s.audit(r.Context(), c, c.user.ID, store.AuditAdminSettings, "", nil)
	return s.adminGetSettings(w, r, c)
}
