package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/ids"
)

// Purposes of rows in user_tokens (the schema constrains them to these).
const (
	tokenVerifyEmail   = "verify_email"
	tokenResetPassword = "reset_password"
	tokenEmailOTP      = "email_otp"
)

// MaxOTPAttempts is how many guesses one emailed code allows before it is
// invalidated.
const MaxOTPAttempts = 5

// IssueVerifyToken stores the hash of a single-use email verification token,
// replacing any outstanding one for the user (so only the newest link works).
// newEmail is the address being verified for an email change; empty verifies
// the user's current address.
func (s *Store) IssueVerifyToken(ctx context.Context, userID, tokenHash, newEmail string, expires time.Time) error {
	return s.issue(ctx, userID, tokenVerifyEmail, tokenHash, "", newEmail, expires)
}

// MaxResetTokens is how many unexpired, unused reset tokens one account may
// have outstanding.
const MaxResetTokens = 3

// IssueResetToken stores the hash of a single-use password reset token and
// reports whether it did. Earlier unexpired tokens stay valid (a stranger
// requesting resets for someone else's account must not invalidate the link
// the owner already holds), up to MaxResetTokens; beyond that nothing is
// issued. Using any one of them drops the rest (ResetPassword).
func (s *Store) IssueResetToken(ctx context.Context, userID, tokenHash string, expires time.Time) (bool, error) {
	issued := false
	err := s.inTx(ctx, true, func(t *tx) error {
		now := s.nowMS()
		if _, err := t.exec(ctx, "DELETE FROM user_tokens WHERE user_id = ? AND purpose = ? AND (used_at IS NOT NULL OR expires_at <= ?)",
			userID, tokenResetPassword, now); err != nil {
			return err
		}
		var n int
		if err := t.queryRow(ctx, "SELECT count(*) FROM user_tokens WHERE user_id = ? AND purpose = ?", userID, tokenResetPassword).Scan(&n); err != nil {
			return err
		}
		if n >= MaxResetTokens {
			return nil
		}
		_, err := t.exec(ctx, `INSERT INTO user_tokens (token_hash, user_id, purpose, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?)`, tokenHash, userID, tokenResetPassword, now, expires.UnixMilli())
		issued = err == nil
		return notFoundFK(err)
	})
	return issued, err
}

// IssueEmailOTP stores a new emailed second-factor code for the user: the
// challenge ID and the hash of the code bound to it (auth.HashEmailOTP). It
// replaces any outstanding code, so only the newest one works.
func (s *Store) IssueEmailOTP(ctx context.Context, userID, challengeID, codeHash string, expires time.Time) error {
	return s.issue(ctx, userID, tokenEmailOTP, challengeID, codeHash, "", expires)
}

func (s *Store) issue(ctx context.Context, userID, purpose, key, codeHash, newEmail string, expires time.Time) error {
	return s.inTx(ctx, true, func(t *tx) error {
		if _, err := t.exec(ctx, "DELETE FROM user_tokens WHERE user_id = ? AND purpose = ?", userID, purpose); err != nil {
			return err
		}
		_, err := t.exec(ctx, `INSERT INTO user_tokens (token_hash, user_id, purpose, code_hash, new_email, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, key, userID, purpose, nullStr(codeHash), nullStr(newEmail), s.nowMS(), expires.UnixMilli())
		return notFoundFK(err)
	})
}

// useToken marks the unexpired, unused token of purpose as used and returns
// its user and pending email. ErrNotFound if there is no such token.
func (t *tx) useToken(ctx context.Context, purpose, hash string, now int64) (userID, newEmail string, err error) {
	var ne sql.NullString
	err = t.queryRow(ctx, `SELECT user_id, new_email FROM user_tokens
		WHERE token_hash = ? AND purpose = ? AND used_at IS NULL AND expires_at > ?`, hash, purpose, now).Scan(&userID, &ne)
	if err != nil {
		return "", "", notFound(err)
	}
	// The guard in the WHERE clause makes concurrent use of one token succeed once.
	if err := t.execOne(ctx, "UPDATE user_tokens SET used_at = ? WHERE token_hash = ? AND used_at IS NULL", now, hash); err != nil {
		return "", "", err
	}
	return userID, ne.String, nil
}

// VerifyEmail consumes a verification token. It marks the user's email
// verified, or, for an email change, switches the account to the new address
// (verified). With autoEmailOTP, a user who then has no second factor gets
// email codes enabled with recoveryHashes as recovery codes (hosted mode
// requires 2FA); enrolled reports whether that happened. ErrNotFound for an
// unknown, used or expired token; ErrConflict if the new address meanwhile
// belongs to another account.
func (s *Store) VerifyEmail(ctx context.Context, tokenHash string, autoEmailOTP bool, recoveryHashes []string) (u User, enrolled bool, err error) {
	err = s.inTx(ctx, true, func(t *tx) error {
		now := s.nowMS()
		id, newEmail, err := t.useToken(ctx, tokenVerifyEmail, tokenHash, now)
		if err != nil {
			return err
		}
		if newEmail != "" {
			_, err = t.exec(ctx, "UPDATE users SET email = ?, email_lower = ?, email_verified = TRUE, email_reminders = FALSE WHERE id = ?",
				newEmail, normLogin(newEmail), id)
			if err = conflict(err); err != nil {
				return err
			}
		} else if err := t.execOne(ctx, "UPDATE users SET email_verified = TRUE WHERE id = ? AND email IS NOT NULL", id); err != nil {
			return err
		}
		if autoEmailOTP {
			res, err := t.exec(ctx, `UPDATE users SET email_otp_enabled = TRUE WHERE id = ? AND totp_enabled = FALSE AND email_otp_enabled = FALSE`, id)
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err != nil {
				return err
			} else if n > 0 {
				enrolled = true
				if err := t.replaceRecoveryCodes(ctx, id, recoveryHashes, now); err != nil {
					return err
				}
			}
		}
		u, err = scanUser(t.queryRow(ctx, "SELECT "+userCols+" FROM users WHERE id = ?", id))
		return err
	})
	return u, enrolled, err
}

// PendingEmail returns the address of the user's outstanding email-change
// verification, or "" if there is none.
func (s *Store) PendingEmail(ctx context.Context, userID string) (string, error) {
	var ne sql.NullString
	err := s.db.QueryRowContext(ctx, s.d.Rebind(`SELECT new_email FROM user_tokens
		WHERE user_id = ? AND purpose = ? AND used_at IS NULL AND expires_at > ?`), userID, tokenVerifyEmail, s.nowMS()).Scan(&ne)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return ne.String, err
}

// EmailTaken reports whether another account uses the address.
func (s *Store) EmailTaken(ctx context.Context, email, exceptUserID string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, s.d.Rebind("SELECT count(*) FROM users WHERE email_lower = ? AND id <> ?"),
		normLogin(email), exceptUserID).Scan(&n)
	return n > 0, err
}

// ResetPassword consumes a password-reset token and sets the new password in
// one transaction: the user's sessions are all revoked and their mark-done
// links deleted (like SetPassword), and every other outstanding reset or
// email-code token is dropped. It returns the user's ID. ErrNotFound for an
// unknown, used or expired token.
func (s *Store) ResetPassword(ctx context.Context, tokenHash, newPasswordHash string) (string, error) {
	var id string
	err := s.inTx(ctx, true, func(t *tx) error {
		now := s.nowMS()
		var err error
		if id, _, err = t.useToken(ctx, tokenResetPassword, tokenHash, now); err != nil {
			return err
		}
		if err := t.setPassword(ctx, id, newPasswordHash, "", now); err != nil {
			return err
		}
		_, err = t.exec(ctx, "DELETE FROM user_tokens WHERE user_id = ? AND purpose IN (?, ?)", id, tokenResetPassword, tokenEmailOTP)
		return err
	})
	return id, err
}

// ConsumeEmailOTP verifies the user's emailed code. hashOf maps the challenge
// ID to the stored hash of the code (auth.HashEmailOTP). Every call spends one
// of MaxOTPAttempts for the current challenge, success consumes it, and a
// challenge that is expired, used or out of attempts never verifies. Wrong,
// expired, used and exhausted all report (false, nil): the attempt must be
// recorded, so a wrong code is not an error (which would roll the count back).
func (s *Store) ConsumeEmailOTP(ctx context.Context, userID string, hashOf func(challengeID string) string) (bool, error) {
	good := false
	err := s.inTx(ctx, true, func(t *tx) error {
		now := s.nowMS()
		var challenge string
		var want sql.NullString
		err := t.queryRow(ctx, `SELECT token_hash, code_hash FROM user_tokens
			WHERE user_id = ? AND purpose = ? AND used_at IS NULL AND expires_at > ? AND attempts < ?
			ORDER BY created_at DESC LIMIT 1`, userID, tokenEmailOTP, now, MaxOTPAttempts).Scan(&challenge, &want)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		} else if err != nil {
			return err
		}
		// Count the attempt first; the guard keeps concurrent guesses within the limit.
		res, err := t.exec(ctx, "UPDATE user_tokens SET attempts = attempts + 1 WHERE token_hash = ? AND used_at IS NULL AND attempts < ?",
			challenge, MaxOTPAttempts)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return err
		}
		if !want.Valid || !ids.Equal(hashOf(challenge), want.String) {
			return nil
		}
		good = true
		return t.execOne(ctx, "UPDATE user_tokens SET used_at = ? WHERE token_hash = ? AND used_at IS NULL", now, challenge)
	})
	if errors.Is(err, ErrNotFound) {
		return false, nil // used concurrently
	}
	return good && err == nil, err
}

// EnableEmailOTP makes email codes the user's second factor (replacing TOTP)
// with codeHashes as the new recovery codes, and revokes every session except
// keepSessionID. ErrConflict if the user has no verified email.
func (s *Store) EnableEmailOTP(ctx context.Context, userID, keepSessionID string, codeHashes []string) error {
	return s.inTx(ctx, true, func(t *tx) error {
		res, err := t.exec(ctx, `UPDATE users SET email_otp_enabled = TRUE, totp_enabled = FALSE, totp_secret_ciphertext = NULL, totp_last_step = 0
			WHERE id = ? AND email_verified = TRUE AND email IS NOT NULL`, userID)
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

// DisableEmailOTP turns email codes off and deletes the recovery codes,
// revoking every session except keepSessionID.
func (s *Store) DisableEmailOTP(ctx context.Context, userID, keepSessionID string) error {
	return s.inTx(ctx, true, func(t *tx) error {
		if err := t.execOne(ctx, "UPDATE users SET email_otp_enabled = FALSE WHERE id = ?", userID); err != nil {
			return err
		}
		if _, err := t.exec(ctx, "DELETE FROM user_tokens WHERE user_id = ? AND purpose = ?", userID, tokenEmailOTP); err != nil {
			return err
		}
		if err := t.replaceRecoveryCodes(ctx, userID, nil, 0); err != nil {
			return err
		}
		if err := t.revokeSessionsExcept(ctx, userID, keepSessionID); err != nil {
			return err
		}
		return t.revokeUserTokens(ctx, userID, "2fa_disable", s.nowMS())
	})
}

// UsePoW records a solved sign-up challenge (by hash) until expires so it
// cannot be used twice. ErrConflict if it was already used.
func (s *Store) UsePoW(ctx context.Context, challengeHash string, expires time.Time) error {
	return s.inTx(ctx, true, func(t *tx) error {
		_, err := t.exec(ctx, "INSERT INTO pow_used (challenge_hash, expires_at) VALUES (?, ?)", challengeHash, expires.UnixMilli())
		return conflict(err)
	})
}
