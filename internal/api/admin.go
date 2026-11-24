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
	if s.emailEnabled() && u.Email != "" {
		// Sent on the admin's account quota, not the new user's request.
		if _, err := s.sendVerification(ctx, c, u, "", false); err != nil {
			s.log.Error("verification email", "err", err)
		}
	}
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
	OpenSignup            bool                `json:"openSignup"` // effective: hosted mode defaults to on
	RequirePoW            bool                `json:"requirePow"` // effective: hosted mode defaults to on
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
	UpdateCheck                bool     `json:"updateCheck"`       // ask GitHub for the latest release (Updates page)
	MCPEnabled                 bool     `json:"mcpEnabled"`        // serve the MCP endpoint /mcp (ADR 0015)
	WebhooksEnabled            bool     `json:"webhooksEnabled"`   // let household owners add outgoing webhooks (ADR 0017); off by default
	OAuthEnabled               bool     `json:"oauthEnabled"`      // let MCP clients sign in with OAuth (ADR 0016); off by default
	OAuthPossible              bool     `json:"oauthPossible"`     // GUNKLY_BASE_URL is https (or loopback), so it can be turned on
	// SSOConfigured: GUNKLY_OIDC_* is set (self-hosted). SSOCreateAccounts lets
	// a first sign-in by an unknown single sign-on user create an account.
	SSOConfigured     bool `json:"ssoConfigured"`
	SSOCreateAccounts bool `json:"ssoCreateAccounts"`
	// SSOName is the admin's provider name for the sign-in button ("" = not
	// set); SSONameDefault is GUNKLY_OIDC_NAME, used when SSOName is empty.
	SSOName        string `json:"ssoName"`
	SSONameDefault string `json:"ssoNameDefault"`
	// SSO describes the GUNKLY_OIDC_* setup (read-only; never the secret).
	// SSOAvailable is false in hosted mode, where single sign-on is off.
	SSOAvailable bool     `json:"ssoAvailable"`
	SSOIssuer    string   `json:"ssoIssuer,omitempty"`
	SSOClientID  string   `json:"ssoClientId,omitempty"`
	SSOScopes    []string `json:"ssoScopes,omitempty"`
	SSOCallback  string   `json:"ssoCallback"` // the redirect URI to register at the provider
	// Inactive-account lifecycle (default off): warn, then delete.
	InactiveEnabled         bool `json:"inactiveEnabled"`
	InactiveWarnAfterDays   int  `json:"inactiveWarnAfterDays"`
	InactiveDeleteAfterDays int  `json:"inactiveDeleteAfterDays"`
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
	open, pow := s.signupSettings(st)
	v := settingsView{OpenSignup: open, RequirePoW: pow, InviteRegistration: st.InviteRegistration, MinPasswordLength: minLen,
		SessionIdleDays: st.SessionIdleDays, SessionMaxDays: st.SessionMaxDays,
		AuditRetentionDays: st.AuditRetentionDays, DeliveryRetentionDays: st.DeliveryRetentionDays,
		Limits: map[limits.Kind]int{}, LimitDefaults: limits.Defaults,
		EnabledNotificationKinds: append([]string{}, slices.Sorted(maps.Keys(kindSet(st)))...), AvailableNotificationKinds: notify.AllKinds(),
		EgressGuard: s.hosted || st.EgressGuard, EgressGuardLocked: s.hosted, UpdateCheck: st.UpdateCheck, MCPEnabled: st.MCPEnabled,
		OAuthEnabled: st.OAuthEnabled, WebhooksEnabled: st.WebhooksEnabled, OAuthPossible: oauthBaseURLOK(s.baseURL), SSOConfigured: s.sso != nil, SSOCreateAccounts: st.SSOCreateAccounts, SSOName: st.SSOName, SSONameDefault: s.ssoNameDefault(),
		InactiveEnabled: st.InactiveEnabled, InactiveWarnAfterDays: st.InactiveWarnAfterDays, InactiveDeleteAfterDays: st.InactiveDeleteAfterDays}
	v.SSOAvailable, v.SSOCallback = !s.hosted, s.baseURL+"/api/v1/auth/oidc/callback"
	if s.sso != nil {
		v.SSOIssuer, v.SSOClientID, v.SSOScopes, v.SSOCallback = s.sso.Issuer(), s.sso.ClientID(), s.sso.Scopes(), s.sso.RedirectURL()
	}
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
		RequirePoW            *bool          `json:"requirePow"`
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
		UpdateCheck              *bool              `json:"updateCheck"`
		MCPEnabled               *bool              `json:"mcpEnabled"`
		OAuthEnabled             *bool              `json:"oauthEnabled"`
		WebhooksEnabled          *bool              `json:"webhooksEnabled"`
		SSOCreateAccounts        *bool              `json:"ssoCreateAccounts"`
		SSOName                  *string            `json:"ssoName"`
		InactiveEnabled          *bool              `json:"inactiveEnabled"`
		InactiveWarnAfterDays    *int               `json:"inactiveWarnAfterDays"`
		InactiveDeleteAfterDays  *int               `json:"inactiveDeleteAfterDays"`
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
	upd := store.SettingsUpdate{OpenSignup: in.OpenSignup, RequirePoW: in.RequirePoW,
		InviteRegistration: in.InviteRegistration, MinPasswordLength: in.MinPasswordLength, Limits: in.Limits, EgressGuard: in.EgressGuard, UpdateCheck: in.UpdateCheck, MCPEnabled: in.MCPEnabled, OAuthEnabled: in.OAuthEnabled, WebhooksEnabled: in.WebhooksEnabled, SSOCreateAccounts: in.SSOCreateAccounts, SSOName: in.SSOName,
		SessionIdleDays: in.SessionIdleDays, SessionMaxDays: in.SessionMaxDays,
		AuditRetentionDays: in.AuditRetentionDays, DeliveryRetentionDays: in.DeliveryRetentionDays,
		InactiveEnabled: in.InactiveEnabled, InactiveWarnAfterDays: in.InactiveWarnAfterDays, InactiveDeleteAfterDays: in.InactiveDeleteAfterDays}
	if in.OAuthEnabled != nil && *in.OAuthEnabled && !oauthBaseURLOK(s.baseURL) {
		return errf(http.StatusBadRequest, "oauth_requires_https") // OAuth needs a public https GUNKLY_BASE_URL
	}
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
	s.forgetMCP()
	if err != nil {
		return err
	}
	s.audit(r.Context(), c, c.user.ID, store.AuditAdminSettings, "", nil)
	return s.adminGetSettings(w, r, c)
}
