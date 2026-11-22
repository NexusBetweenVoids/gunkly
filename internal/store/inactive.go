package store

import (
	"context"
	"database/sql"
	"time"
)

// InactiveUser is an account idle since before a cutoff, with the state of
// the inactivity warnings (system use: the daily inactive-account job).
type InactiveUser struct {
	ID            string
	Email         string
	EmailVerified bool
	DisplayName   string
	Locale        string
	IsAdmin       bool
	LastActiveAt  time.Time
	WarnedAt      time.Time // zero = not warned for this period
	FinalWarnedAt time.Time // zero = no final warning yet
}

// InactiveUsers returns up to limit accounts whose last activity is before
// idleBefore, ordered by ID after afterID (page by passing the last ID).
func (s *Store) InactiveUsers(ctx context.Context, idleBefore time.Time, afterID string, limit int) ([]InactiveUser, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var out []InactiveUser
	err := s.inTx(ctx, false, func(t *tx) error {
		rows, err := t.query(ctx, `SELECT id, COALESCE(email, ''), email_verified, display_name, locale, is_admin,
			last_active_at, inactive_warned_at, inactive_final_warned_at
			FROM users WHERE last_active_at < ? AND id > ? ORDER BY id LIMIT ?`, idleBefore.UnixMilli(), afterID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var u InactiveUser
			var active int64
			var warned, final sql.NullInt64
			if err := rows.Scan(&u.ID, &u.Email, &u.EmailVerified, &u.DisplayName, &u.Locale, &u.IsAdmin, &active, &warned, &final); err != nil {
				return err
			}
			u.LastActiveAt, u.WarnedAt, u.FinalWarnedAt = time.UnixMilli(active).UTC(), fromMS(warned), fromMS(final)
			out = append(out, u)
		}
		return rows.Err()
	})
	return out, err
}

// MarkInactiveWarned records that the first (or, with final, the final)
// warning went out at at. It only applies while the user has not been active
// since at, so a sign-in racing the job is not overwritten: ErrConflict then.
func (s *Store) MarkInactiveWarned(ctx context.Context, userID string, final bool, at time.Time) error {
	col := "inactive_warned_at"
	if final {
		col = "inactive_final_warned_at"
	}
	return s.inTx(ctx, true, func(t *tx) error {
		res, err := t.exec(ctx, "UPDATE users SET "+col+" = ? WHERE id = ? AND last_active_at < ?", at.UnixMilli(), userID, at.UnixMilli())
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return conflictIfNone(err)
		}
		return nil
	})
}

// WarningChannel is a sealed personal notification channel.
type WarningChannel struct {
	ID         string
	Service    string
	Ciphertext string
}

// WarningPush is a push subscription with its sealed keys.
type WarningPush struct {
	ID         string
	Endpoint   string
	Ciphertext string
}

// WarningTargets lists the user's personal channels and push subscriptions
// (system use: inactivity warnings). Household channels are not included.
func (s *Store) WarningTargets(ctx context.Context, userID string) ([]WarningChannel, []WarningPush, error) {
	var chs []WarningChannel
	var pushes []WarningPush
	err := s.inTx(ctx, false, func(t *tx) error {
		rows, err := t.query(ctx, `SELECT id, service, url_ciphertext FROM notification_channels
			WHERE scope = 'user' AND user_id = ? ORDER BY created_at, id`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c WarningChannel
			if err := rows.Scan(&c.ID, &c.Service, &c.Ciphertext); err != nil {
				return err
			}
			chs = append(chs, c)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		prows, err := t.query(ctx, "SELECT id, endpoint, keys_ciphertext FROM push_subscriptions WHERE user_id = ? ORDER BY created_at, id", userID)
		if err != nil {
			return err
		}
		defer prows.Close()
		for prows.Next() {
			var p WarningPush
			if err := prows.Scan(&p.ID, &p.Endpoint, &p.Ciphertext); err != nil {
				return err
			}
			pushes = append(pushes, p)
		}
		return prows.Err()
	})
	return chs, pushes, err
}
