package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/ids"
)

// NoPassword is the password hash of an account created by single sign-on
// (ADR 0018): not a valid hash of anything, so no password ever verifies,
// until a real one is set.
const NoPassword = "!sso"

// HasPassword reports whether the account can sign in with a password.
func (u User) HasPassword() bool { return !strings.HasPrefix(u.PasswordHash, "!") }

// SSOIdentity links an account to a user of the OpenID Connect provider.
type SSOIdentity struct {
	ID         string
	UserID     string
	Issuer     string
	Subject    string
	CreatedAt  time.Time
	LastUsedAt time.Time // zero = never
}

const ssoCols = `id, user_id, issuer, subject, created_at, last_used_at`

func scanSSO(row interface{ Scan(...any) error }) (SSOIdentity, error) {
	var i SSOIdentity
	var created int64
	var used sql.NullInt64
	if err := row.Scan(&i.ID, &i.UserID, &i.Issuer, &i.Subject, &created, &used); err != nil {
		return SSOIdentity{}, notFound(err)
	}
	i.CreatedAt, i.LastUsedAt = time.UnixMilli(created).UTC(), fromMS(used)
	return i, nil
}

// SSOIdentityBySubject finds the identity of (issuer, subject): the first
// step of a sign-in, which says whose account it is.
func (s *Store) SSOIdentityBySubject(ctx context.Context, issuer, subject string) (SSOIdentity, error) {
	return scanSSO(s.db.QueryRowContext(ctx, s.d.Rebind("SELECT "+ssoCols+" FROM sso_identities WHERE issuer = ? AND subject = ?"), issuer, subject))
}

// SSOIdentityForUser returns the account's identity at issuer (ErrNotFound if none).
func (s *Store) SSOIdentityForUser(ctx context.Context, userID, issuer string) (SSOIdentity, error) {
	return scanSSO(s.db.QueryRowContext(ctx, s.d.Rebind("SELECT "+ssoCols+" FROM sso_identities WHERE user_id = ? AND issuer = ?"), userID, issuer))
}

// TouchSSO records a sign-in with the identity.
func (s *Store) TouchSSO(ctx context.Context, id string) error {
	return s.inTx(ctx, true, func(t *tx) error {
		_, err := t.exec(ctx, "UPDATE sso_identities SET last_used_at = ? WHERE id = ?", s.nowMS(), id)
		return err
	})
}

// LinkSSO links the account to (issuer, subject) and signs it out everywhere
// but keepSessionID, like adding another way to sign in. ErrConflict if the
// subject belongs to an account already, or this account has an identity at
// the issuer.
func (s *Store) LinkSSO(ctx context.Context, userID, keepSessionID, issuer, subject string) error {
	return s.inTx(ctx, true, func(t *tx) error {
		if err := t.lockUser(ctx, userID); err != nil {
			return err
		}
		if err := t.insertSSO(ctx, userID, issuer, subject, s.nowMS()); err != nil {
			return err
		}
		return t.revokeSessionsExcept(ctx, userID, keepSessionID)
	})
}

func (t *tx) insertSSO(ctx context.Context, userID, issuer, subject string, now int64) error {
	_, err := t.exec(ctx, "INSERT INTO sso_identities (id, user_id, issuer, subject, created_at) VALUES (?, ?, ?, ?, ?)",
		ids.New(), userID, issuer, subject, now)
	return conflict(err)
}

// UnlinkSSO removes the account's identity at issuer, only if the account can
// still sign in some other way (a password or a passkey): ErrConflict
// otherwise, ErrNotFound if there is none. Other sessions are signed out and
// API tokens revoked, like removing a second factor.
func (s *Store) UnlinkSSO(ctx context.Context, userID, keepSessionID, issuer string) error {
	return s.inTx(ctx, true, func(t *tx) error {
		if err := t.lockUser(ctx, userID); err != nil {
			return err
		}
		var hash string
		var passkeys int
		if err := t.queryRow(ctx, "SELECT password_hash, (SELECT count(*) FROM passkeys WHERE user_id = users.id) FROM users WHERE id = ?", userID).Scan(&hash, &passkeys); err != nil {
			return notFound(err)
		}
		if strings.HasPrefix(hash, "!") && passkeys == 0 {
			return ErrConflict
		}
		if err := t.execOne(ctx, "DELETE FROM sso_identities WHERE user_id = ? AND issuer = ?", userID, issuer); err != nil {
			return err
		}
		if err := t.revokeSessionsExcept(ctx, userID, keepSessionID); err != nil {
			return err
		}
		return t.revokeUserTokens(ctx, userID, "sso_unlink", s.nowMS())
	})
}

// NewSSOUser is the input to CreateSSOUser.
type NewSSOUser struct {
	Username, DisplayName, Locale string
	Email                         string // already known to be verified by the provider; may be empty
	Issuer, Subject               string
}

// CreateSSOUser creates an account without a password and links it to
// (issuer, subject) in one transaction. ErrConflict if the username, the
// email or the subject is taken (the caller tells them apart).
func (s *Store) CreateSSOUser(ctx context.Context, in NewSSOUser) (User, error) {
	var u User
	err := s.inTx(ctx, true, func(t *tx) error {
		now := s.nowMS()
		var err error
		u, err = t.insertUser(ctx, NewUser{Username: in.Username, DisplayName: in.DisplayName, Email: in.Email,
			PasswordHash: NoPassword, Locale: in.Locale}, now)
		if err != nil {
			return err
		}
		if in.Email != "" { // the provider vouched for it
			if err := t.execOne(ctx, "UPDATE users SET email_verified = TRUE WHERE id = ?", u.ID); err != nil {
				return err
			}
			u.EmailVerified = true
		}
		return t.insertSSO(ctx, u.ID, in.Issuer, in.Subject, now)
	})
	return u, err
}
