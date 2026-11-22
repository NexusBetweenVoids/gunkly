package store

import (
	"context"
	"strings"

	"github.com/NexusBetweenVoids/gunkly/internal/ids"
)

// The methods in this file act on the caller's own account (userID comes
// from the authenticated session, never from request input) or are
// operator-only (CLI, admin).

// SetPassword replaces the user's password hash, revokes every session except
// keepSessionID (empty revokes all) and deletes the mark-done links addressed
// to the user, all in one transaction: a failure leaves the old password and
// sessions intact rather than a half-applied change.
func (s *Store) SetPassword(ctx context.Context, userID, hash, keepSessionID string) error {
	if hash == "" {
		return ErrInvalid
	}
	return s.inTx(ctx, true, func(t *tx) error {
		if err := t.execOne(ctx, "UPDATE users SET password_hash = ?, password_changed_at = ? WHERE id = ?", hash, s.nowMS(), userID); err != nil {
			return err
		}
		if err := t.revokeSessionsExcept(ctx, userID, keepSessionID); err != nil {
			return err
		}
		_, err := t.exec(ctx, "DELETE FROM mark_done_tokens WHERE user_id = ?", userID)
		return err
	})
}

// RehashPassword upgrades a hash made with old parameters after a successful
// login. Unlike SetPassword it does not count as a password change.
func (s *Store) RehashPassword(ctx context.Context, userID, hash string) error {
	return s.inTx(ctx, true, func(t *tx) error {
		return t.execOne(ctx, "UPDATE users SET password_hash = ? WHERE id = ?", hash, userID)
	})
}

// ProfileUpdate changes the caller's own profile; nil fields are unchanged.
type ProfileUpdate struct {
	DisplayName *string
	Locale      *string
	WeekStart   **int // set to a nil *int to clear (locale default)
}

// UpdateProfile applies p to the user's profile.
func (s *Store) UpdateProfile(ctx context.Context, userID string, p ProfileUpdate) (User, error) {
	var u User
	err := s.inTx(ctx, true, func(t *tx) error {
		if p.DisplayName != nil {
			name := strings.TrimSpace(*p.DisplayName)
			if name == "" {
				return ErrInvalid
			}
			if err := t.execOne(ctx, "UPDATE users SET display_name = ? WHERE id = ?", name, userID); err != nil {
				return err
			}
		}
		if p.Locale != nil {
			if err := t.execOne(ctx, "UPDATE users SET locale = ? WHERE id = ?", *p.Locale, userID); err != nil {
				return err
			}
		}
		if p.WeekStart != nil {
			ws := *p.WeekStart
			if ws != nil && (*ws < 0 || *ws > 6) {
				return ErrInvalid
			}
			var v any
			if ws != nil {
				v = *ws
			}
			if err := t.execOne(ctx, "UPDATE users SET week_start = ? WHERE id = ?", v, userID); err != nil {
				return err
			}
		}
		var err error
		u, err = scanUser(t.queryRow(ctx, "SELECT "+userCols+" FROM users WHERE id = ?", userID))
		return err
	})
	return u, err
}

// SetPendingTOTP stores a new, not yet confirmed TOTP secret (ciphertext).
// ErrConflict if TOTP is already enabled.
func (s *Store) SetPendingTOTP(ctx context.Context, userID, ciphertext string) error {
	return s.inTx(ctx, true, func(t *tx) error {
		var enabled bool
		if err := t.queryRow(ctx, "SELECT totp_enabled FROM users WHERE id = ?", userID).Scan(&enabled); err != nil {
			return notFound(err)
		}
		if enabled {
			return ErrConflict
		}
		return t.execOne(ctx, "UPDATE users SET totp_secret_ciphertext = ?, totp_last_step = 0 WHERE id = ?", ciphertext, userID)
	})
}

// EnableTOTP confirms the pending secret: it records step as the last used
// step (so the confirming code cannot be replayed) and replaces the user's
// recovery codes with codeHashes. Every session except keepSessionID is
// revoked in the same transaction. ErrConflict if already enabled or nothing
// is pending.
func (s *Store) EnableTOTP(ctx context.Context, userID, keepSessionID string, step int64, codeHashes []string) error {
	return s.inTx(ctx, true, func(t *tx) error {
		res, err := t.exec(ctx, `UPDATE users SET totp_enabled = TRUE, totp_last_step = ?
			WHERE id = ? AND totp_enabled = FALSE AND totp_secret_ciphertext IS NOT NULL`, step, userID)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return conflictIfNone(err)
		}
		if err := t.replaceRecoveryCodes(ctx, userID, codeHashes, s.nowMS()); err != nil {
			return err
		}
		return t.revokeSessionsExcept(ctx, userID, keepSessionID)
	})
}

// conflictIfNone maps "no row affected" (err == nil) to ErrConflict.
func conflictIfNone(err error) error {
	if err != nil {
		return err
	}
	return ErrConflict
}

func (t *tx) replaceRecoveryCodes(ctx context.Context, userID string, hashes []string, now int64) error {
	if _, err := t.exec(ctx, "DELETE FROM recovery_codes WHERE user_id = ?", userID); err != nil {
		return err
	}
	for _, h := range hashes {
		if _, err := t.exec(ctx, "INSERT INTO recovery_codes (id, user_id, code_hash, created_at) VALUES (?, ?, ?, ?)",
			ids.New(), userID, h, now); err != nil {
			return err
		}
	}
	return nil
}

// DisableTOTP removes the user's TOTP secret and recovery codes. Used by the
// account settings and by `gunkly admin reset-2fa`. Every session except
// keepSessionID (empty revokes all) is revoked in the same transaction.
func (s *Store) DisableTOTP(ctx context.Context, userID, keepSessionID string) error {
	return s.inTx(ctx, true, func(t *tx) error {
		if err := t.execOne(ctx, `UPDATE users SET totp_secret_ciphertext = NULL, totp_enabled = FALSE, totp_last_step = 0
			WHERE id = ?`, userID); err != nil {
			return err
		}
		if err := t.replaceRecoveryCodes(ctx, userID, nil, 0); err != nil {
			return err
		}
		return t.revokeSessionsExcept(ctx, userID, keepSessionID)
	})
}

func (t *tx) revokeSessionsExcept(ctx context.Context, userID, keepSessionID string) error {
	_, err := t.exec(ctx, "DELETE FROM sessions WHERE user_id = ? AND id <> ?", userID, keepSessionID)
	return err
}

// UseTOTPStep atomically records step as used. ErrConflict if a code with
// this or a later step was already accepted (replay).
func (s *Store) UseTOTPStep(ctx context.Context, userID string, step int64) error {
	return s.inTx(ctx, true, func(t *tx) error {
		res, err := t.exec(ctx, "UPDATE users SET totp_last_step = ? WHERE id = ? AND totp_enabled = TRUE AND totp_last_step < ?",
			step, userID, step)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return conflictIfNone(err)
		}
		return nil
	})
}

// UseRecoveryCode consumes one unused recovery code. ErrNotFound if no
// unused code has this hash.
func (s *Store) UseRecoveryCode(ctx context.Context, userID, codeHash string) error {
	return s.inTx(ctx, true, func(t *tx) error {
		return t.execOne(ctx, "UPDATE recovery_codes SET used_at = ? WHERE user_id = ? AND code_hash = ? AND used_at IS NULL",
			s.nowMS(), userID, codeHash)
	})
}

// RotateTOTPSecrets is an explicitly unscoped operator function for `gunkly
// rotate-key`: it calls fn for every stored TOTP secret and writes back the
// returned ciphertext when changed. Returns the number rewritten.
func (s *Store) RotateTOTPSecrets(ctx context.Context, fn func(userID, ciphertext string) (string, error)) (int, error) {
	n := 0
	err := s.inTx(ctx, true, func(t *tx) error {
		all, err := t.totpSecrets(ctx)
		if err != nil {
			return err
		}
		for _, r := range all {
			ct, err := fn(r.id, r.ct)
			if err != nil {
				return err
			}
			if ct == r.ct {
				continue
			}
			if err := t.execOne(ctx, "UPDATE users SET totp_secret_ciphertext = ? WHERE id = ?", ct, r.id); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

type totpRow struct{ id, ct string }

func (t *tx) totpSecrets(ctx context.Context) ([]totpRow, error) {
	rows, err := t.query(ctx, "SELECT id, totp_secret_ciphertext FROM users WHERE totp_secret_ciphertext IS NOT NULL")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var all []totpRow
	for rows.Next() {
		var r totpRow
		if err := rows.Scan(&r.id, &r.ct); err != nil {
			return nil, err
		}
		all = append(all, r)
	}
	return all, rows.Err()
}
