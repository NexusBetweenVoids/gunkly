package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// Instance settings keys in the settings table.
const (
	settingOpenSignup         = "open_signup"
	settingInviteRegistration = "invite_registration"
	settingRequirePoW         = "require_pow"
	settingMinPasswordLength  = "min_password_length"
	settingSetupCompleted     = "setup_completed"
	settingNotificationKinds  = "notification_kinds" // JSON array; absent = notify.DefaultKinds
	settingEgressGuard        = "egress_guard"
	settingSessionIdleDays    = "session_idle_days"
	settingSessionMaxDays     = "session_max_days"
	settingAuditRetentionDays = "audit_retention_days"
	settingDeliveryRetention  = "delivery_retention_days"
	settingUpdateCheck        = "update_check"
	settingMCPEnabled         = "mcp_enabled"
	settingInactiveEnabled    = "inactive_enabled"
	settingInactiveWarnDays   = "inactive_warn_after_days"
	settingInactiveDeleteDays = "inactive_delete_after_days"
	settingLimitPrefix        = "limit."
)

// InstanceSettings are the admin-editable instance settings, with defaults
// applied for keys never set.
type InstanceSettings struct {
	OpenSignup         bool           // default false (self-hosted); see OpenSignupSet
	OpenSignupSet      bool           // the admin has chosen; otherwise the mode decides (hosted: on)
	RequirePoW         bool           // sign-up proof-of-work; see RequirePoWSet
	RequirePoWSet      bool           // as OpenSignupSet (hosted default: on)
	InviteRegistration bool           // default true: a valid invite lets someone register
	MinPasswordLength  int            // 0 = auth.DefaultMinPasswordLen
	Limits             map[string]int // instance-wide overrides of internal/limits defaults
	// NotificationKinds are the admin-enabled notification service kinds
	// (internal/notify); nil = never set, meaning the built-in defaults.
	NotificationKinds []string
	// EgressGuard blocks outbound connections to non-public addresses.
	// Default true. Hosted mode forces it on regardless (internal/api).
	EgressGuard bool
	// Session lifetimes and data retention, in days; defaults applied
	// (PRD: sessions 30 idle / 90 absolute; audit 90, deliveries 30).
	SessionIdleDays, SessionMaxDays           int
	AuditRetentionDays, DeliveryRetentionDays int
	// UpdateCheck lets the server ask GitHub for the latest release (the
	// Updates page). Default true.
	UpdateCheck bool
	// MCPEnabled serves the MCP endpoint (/mcp, ADR 0015). Default true.
	MCPEnabled bool
	// Inactive-account lifecycle (PRD "Inactive accounts"; default off):
	// warn after InactiveWarnAfterDays without activity, delete after
	// InactiveDeleteAfterDays.
	InactiveEnabled                                bool
	InactiveWarnAfterDays, InactiveDeleteAfterDays int
}

// Bounds and defaults for the day-valued settings.
const (
	DefaultSessionIdleDays       = 30
	DefaultSessionMaxDays        = 90
	MaxSessionIdleDays           = 365
	MaxSessionMaxDays            = 730
	DefaultAuditRetentionDays    = 90
	DefaultDeliveryRetentionDays = 30
	MaxRetentionDays             = 3650
	DefaultInactiveWarnDays      = 365
	DefaultInactiveDeleteDays    = 548 // about 18 months
	MinInactiveWarnDays          = 30
	MinInactiveGapDays           = 30 // the final warning comes this long before deletion
	MaxInactiveDays              = 3650
)

// SettingsUpdate changes instance settings; nil fields are unchanged. A
// limit value of 0 removes the override (back to the built-in default).
type SettingsUpdate struct {
	OpenSignup         *bool
	RequirePoW         *bool
	InviteRegistration *bool
	MinPasswordLength  *int
	Limits             map[string]int
	// NotificationKinds replaces the enabled kinds. A non-nil slice (possibly
	// empty: nothing enabled) is stored; a pointer to a nil slice resets to
	// the defaults.
	NotificationKinds *[]string
	EgressGuard       *bool
	UpdateCheck       *bool
	MCPEnabled        *bool
	InactiveEnabled   *bool
	// Inactive-account periods; the delete period must exceed the warn period
	// by at least MinInactiveGapDays (checked against the stored value for
	// whichever is unset).
	InactiveWarnAfterDays, InactiveDeleteAfterDays *int
	// Day-valued settings; validated against the bounds above, and idle must
	// not exceed max (checked against the stored value for whichever is unset).
	SessionIdleDays, SessionMaxDays           *int
	AuditRetentionDays, DeliveryRetentionDays *int
}

// InstanceSettings reads the instance settings.
func (s *Store) InstanceSettings(ctx context.Context) (InstanceSettings, error) {
	out := InstanceSettings{InviteRegistration: true, EgressGuard: true, UpdateCheck: true, MCPEnabled: true, Limits: map[string]int{},
		SessionIdleDays: DefaultSessionIdleDays, SessionMaxDays: DefaultSessionMaxDays,
		AuditRetentionDays: DefaultAuditRetentionDays, DeliveryRetentionDays: DefaultDeliveryRetentionDays,
		InactiveWarnAfterDays: DefaultInactiveWarnDays, InactiveDeleteAfterDays: DefaultInactiveDeleteDays}
	err := s.inTx(ctx, false, func(t *tx) error {
		rows, err := t.query(ctx, "SELECT key, value FROM settings")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k, v string
			if err := rows.Scan(&k, &v); err != nil {
				return err
			}
			switch {
			case k == settingOpenSignup:
				out.OpenSignup, out.OpenSignupSet = v == "true", true
			case k == settingRequirePoW:
				out.RequirePoW, out.RequirePoWSet = v == "true", true
			case k == settingInviteRegistration:
				out.InviteRegistration = v == "true"
			case k == settingNotificationKinds:
				var kinds []string
				if json.Unmarshal([]byte(v), &kinds) == nil {
					out.NotificationKinds = append([]string{}, kinds...)
				}
			case k == settingEgressGuard:
				out.EgressGuard = v != "false"
			case k == settingUpdateCheck:
				out.UpdateCheck = v != "false"
			case k == settingMCPEnabled:
				out.MCPEnabled = v != "false"
			case k == settingInactiveEnabled:
				out.InactiveEnabled = v == "true"
			case k == settingInactiveWarnDays:
				out.InactiveWarnAfterDays = atoiOr(v, out.InactiveWarnAfterDays)
			case k == settingInactiveDeleteDays:
				out.InactiveDeleteAfterDays = atoiOr(v, out.InactiveDeleteAfterDays)
			case k == settingSessionIdleDays:
				out.SessionIdleDays = atoiOr(v, out.SessionIdleDays)
			case k == settingSessionMaxDays:
				out.SessionMaxDays = atoiOr(v, out.SessionMaxDays)
			case k == settingAuditRetentionDays:
				out.AuditRetentionDays = atoiOr(v, out.AuditRetentionDays)
			case k == settingDeliveryRetention:
				out.DeliveryRetentionDays = atoiOr(v, out.DeliveryRetentionDays)
			case k == settingMinPasswordLength:
				out.MinPasswordLength, _ = strconv.Atoi(v)
			case strings.HasPrefix(k, settingLimitPrefix):
				if n, err := strconv.Atoi(v); err == nil {
					out.Limits[strings.TrimPrefix(k, settingLimitPrefix)] = n
				}
			}
		}
		return rows.Err()
	})
	return out, err
}

func atoiOr(v string, def int) int {
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return n
	}
	return def
}

func inRange(p *int, lo, hi int) bool { return p == nil || (*p >= lo && *p <= hi) }

// UpdateInstanceSettings applies u atomically.
func (s *Store) UpdateInstanceSettings(ctx context.Context, u SettingsUpdate) error {
	if (u.MinPasswordLength != nil && *u.MinPasswordLength < 1) ||
		!inRange(u.SessionIdleDays, 1, MaxSessionIdleDays) || !inRange(u.SessionMaxDays, 1, MaxSessionMaxDays) ||
		!inRange(u.AuditRetentionDays, 1, MaxRetentionDays) || !inRange(u.DeliveryRetentionDays, 1, MaxRetentionDays) {
		return ErrInvalid
	}
	if u.InactiveWarnAfterDays != nil || u.InactiveDeleteAfterDays != nil {
		if !inRange(u.InactiveWarnAfterDays, MinInactiveWarnDays, MaxInactiveDays) ||
			!inRange(u.InactiveDeleteAfterDays, MinInactiveWarnDays+MinInactiveGapDays, MaxInactiveDays) {
			return ErrInvalid
		}
		cur, err := s.InstanceSettings(ctx)
		if err != nil {
			return err
		}
		warn, del := cur.InactiveWarnAfterDays, cur.InactiveDeleteAfterDays
		if u.InactiveWarnAfterDays != nil {
			warn = *u.InactiveWarnAfterDays
		}
		if u.InactiveDeleteAfterDays != nil {
			del = *u.InactiveDeleteAfterDays
		}
		if del-warn < MinInactiveGapDays {
			return ErrInvalid
		}
	}
	if u.SessionIdleDays != nil || u.SessionMaxDays != nil {
		cur, err := s.InstanceSettings(ctx)
		if err != nil {
			return err
		}
		idle, maxD := cur.SessionIdleDays, cur.SessionMaxDays
		if u.SessionIdleDays != nil {
			idle = *u.SessionIdleDays
		}
		if u.SessionMaxDays != nil {
			maxD = *u.SessionMaxDays
		}
		if idle > maxD {
			return ErrInvalid
		}
	}
	return s.inTx(ctx, true, func(t *tx) error {
		now := s.nowMS()
		set := func(k, v string) error {
			_, err := t.exec(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
				ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, k, v, now)
			return err
		}
		var err error
		if u.OpenSignup != nil {
			err = set(settingOpenSignup, strconv.FormatBool(*u.OpenSignup))
		}
		if err == nil && u.RequirePoW != nil {
			err = set(settingRequirePoW, strconv.FormatBool(*u.RequirePoW))
		}
		if err == nil && u.InviteRegistration != nil {
			err = set(settingInviteRegistration, strconv.FormatBool(*u.InviteRegistration))
		}
		if err == nil && u.MinPasswordLength != nil {
			err = set(settingMinPasswordLength, strconv.Itoa(*u.MinPasswordLength))
		}
		for _, d := range []struct {
			v   *int
			key string
		}{{u.SessionIdleDays, settingSessionIdleDays}, {u.SessionMaxDays, settingSessionMaxDays},
			{u.AuditRetentionDays, settingAuditRetentionDays}, {u.DeliveryRetentionDays, settingDeliveryRetention}} {
			if err == nil && d.v != nil {
				err = set(d.key, strconv.Itoa(*d.v))
			}
		}
		if err == nil && u.EgressGuard != nil {
			err = set(settingEgressGuard, strconv.FormatBool(*u.EgressGuard))
		}
		if err == nil && u.UpdateCheck != nil {
			err = set(settingUpdateCheck, strconv.FormatBool(*u.UpdateCheck))
		}
		if err == nil && u.MCPEnabled != nil {
			err = set(settingMCPEnabled, strconv.FormatBool(*u.MCPEnabled))
		}
		if err == nil && u.InactiveEnabled != nil {
			err = set(settingInactiveEnabled, strconv.FormatBool(*u.InactiveEnabled))
		}
		if err == nil && u.InactiveWarnAfterDays != nil {
			err = set(settingInactiveWarnDays, strconv.Itoa(*u.InactiveWarnAfterDays))
		}
		if err == nil && u.InactiveDeleteAfterDays != nil {
			err = set(settingInactiveDeleteDays, strconv.Itoa(*u.InactiveDeleteAfterDays))
		}
		if err == nil && u.NotificationKinds != nil {
			if *u.NotificationKinds == nil {
				_, err = t.exec(ctx, "DELETE FROM settings WHERE key = ?", settingNotificationKinds)
			} else {
				var b []byte
				if b, err = json.Marshal(*u.NotificationKinds); err == nil {
					err = set(settingNotificationKinds, string(b))
				}
			}
		}
		for k, v := range u.Limits {
			switch {
			case err != nil:
			case v < 0:
				err = ErrInvalid
			case v == 0:
				_, err = t.exec(ctx, "DELETE FROM settings WHERE key = ?", settingLimitPrefix+k)
			default:
				err = set(settingLimitPrefix+k, strconv.Itoa(v))
			}
		}
		return err
	})
}

// LimitOverrides returns the instance-wide limit overrides and the
// household's plan overrides (householdID may be empty). Unscoped: it reads
// configuration only, no household content. Used by internal/limits.
func (s *Store) LimitOverrides(ctx context.Context, householdID string) (instance, plan map[string]int, err error) {
	st, err := s.InstanceSettings(ctx)
	if err != nil {
		return nil, nil, err
	}
	if householdID == "" {
		return st.Limits, nil, nil
	}
	var raw string
	err = s.db.QueryRowContext(ctx, s.d.Rebind(
		"SELECT p.limits FROM households h JOIN plans p ON p.id = h.plan_id WHERE h.id = ?"), householdID).Scan(&raw)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return st.Limits, nil, nil
	case err != nil:
		return nil, nil, err
	}
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		return nil, nil, err
	}
	return st.Limits, plan, nil
}

// HasUsers reports whether any account exists (first-run setup check).
func (s *Store) HasUsers(ctx context.Context) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM (SELECT 1 FROM users LIMIT 1) x").Scan(&n)
	return n > 0, err
}

// CreateFirstAdmin creates the instance admin during first-run setup. It
// succeeds at most once per database: the setup_completed settings row is
// the guard (its primary key makes concurrent attempts conflict), and it
// fails with ErrConflict if any user already exists.
func (s *Store) CreateFirstAdmin(ctx context.Context, in NewUser) (User, error) {
	in.IsAdmin = true
	var u User
	err := s.inTx(ctx, true, func(t *tx) error {
		now := s.nowMS()
		var n int
		if err := t.queryRow(ctx, "SELECT count(*) FROM users").Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrConflict
		}
		if _, err := t.exec(ctx, "INSERT INTO settings (key, value, updated_at) VALUES (?, 'true', ?)", settingSetupCompleted, now); err != nil {
			return conflict(err)
		}
		var err error
		u, err = t.insertUser(ctx, in, now)
		return err
	})
	return u, err
}
