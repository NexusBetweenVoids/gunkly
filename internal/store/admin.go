package store

import (
	"context"
	"time"
)

// AdminUser is the instance admin's view of an account: identity and
// security state only, never household content.
type AdminUser struct {
	ID           string
	Username     string
	Email        string
	DisplayName  string
	IsAdmin      bool
	TOTPEnabled  bool
	CreatedAt    time.Time
	LastActiveAt time.Time
}

// AdminListUsers is an explicitly unscoped, admin-only listing of accounts.
func (s *Store) AdminListUsers(ctx context.Context) ([]AdminUser, error) {
	var out []AdminUser
	err := s.inTx(ctx, false, func(t *tx) error {
		rows, err := t.query(ctx, `SELECT id, username, COALESCE(email, ''), display_name, is_admin, totp_enabled, created_at, last_active_at
			FROM users ORDER BY username_lower`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var u AdminUser
			var created, active int64
			if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.DisplayName, &u.IsAdmin, &u.TOTPEnabled, &created, &active); err != nil {
				return err
			}
			u.CreatedAt, u.LastActiveAt = time.UnixMilli(created).UTC(), time.UnixMilli(active).UTC()
			out = append(out, u)
		}
		return rows.Err()
	})
	return out, err
}
