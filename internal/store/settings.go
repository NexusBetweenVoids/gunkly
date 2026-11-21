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
	settingMinPasswordLength  = "min_password_length"
	settingSetupCompleted     = "setup_completed"
	settingNotificationKinds  = "notification_kinds" // JSON array; absent = notify.DefaultKinds
	settingEgressGuard        = "egress_guard"
	settingLimitPrefix        = "limit."
)

// InstanceSettings are the admin-editable instance settings, with defaults
// applied for keys never set.
type InstanceSettings struct {
	OpenSignup         bool           // default false (self-hosted)
	InviteRegistration bool           // default true: a valid invite lets someone register
	MinPasswordLength  int            // 0 = auth.DefaultMinPasswordLen
	Limits             map[string]int // instance-wide overrides of internal/limits defaults
	// NotificationKinds are the admin-enabled notification service kinds
	// (internal/notify); nil = never set, meaning the built-in defaults.
	NotificationKinds []string
	// EgressGuard blocks outbound connections to non-public addresses.
	// Default true. Hosted mode forces it on regardless (internal/api).
	EgressGuard bool
}

// SettingsUpdate changes instance settings; nil fields are unchanged. A
// limit value of 0 removes the override (back to the built-in default).
type SettingsUpdate struct {
	OpenSignup         *bool
	InviteRegistration *bool
	MinPasswordLength  *int
	Limits             map[string]int
	// NotificationKinds replaces the enabled kinds. A non-nil slice (possibly
	// empty: nothing enabled) is stored; a pointer to a nil slice resets to
	// the defaults.
	NotificationKinds *[]string
	EgressGuard       *bool
}

// InstanceSettings reads the instance settings.
func (s *Store) InstanceSettings(ctx context.Context) (InstanceSettings, error) {
	out := InstanceSettings{InviteRegistration: true, EgressGuard: true, Limits: map[string]int{}}
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
				out.OpenSignup = v == "true"
			case k == settingInviteRegistration:
				out.InviteRegistration = v == "true"
			case k == settingNotificationKinds:
				var kinds []string
				if json.Unmarshal([]byte(v), &kinds) == nil {
					out.NotificationKinds = append([]string{}, kinds...)
				}
			case k == settingEgressGuard:
				out.EgressGuard = v != "false"
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

// UpdateInstanceSettings applies u atomically.
func (s *Store) UpdateInstanceSettings(ctx context.Context, u SettingsUpdate) error {
	if u.MinPasswordLength != nil && *u.MinPasswordLength < 1 {
		return ErrInvalid
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
		if err == nil && u.InviteRegistration != nil {
			err = set(settingInviteRegistration, strconv.FormatBool(*u.InviteRegistration))
		}
		if err == nil && u.MinPasswordLength != nil {
			err = set(settingMinPasswordLength, strconv.Itoa(*u.MinPasswordLength))
		}
		if err == nil && u.EgressGuard != nil {
			err = set(settingEgressGuard, strconv.FormatBool(*u.EgressGuard))
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
