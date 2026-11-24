package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/ids"
)

// Passkey is one WebAuthn credential of an account (ADR 0018). It holds only
// public material; the private key never leaves the user's authenticator.
type Passkey struct {
	ID           string
	UserID       string
	Name         string
	CredentialID []byte
	PublicKey    []byte
	SignCount    uint32
	Transports   []string
	AAGUID       []byte
	Flags        byte // authenticator flags as last seen (backup eligible/state, user verified)
	CreatedAt    time.Time
	LastUsedAt   time.Time // zero = never
}

// NewPasskey is the input to AddPasskey.
type NewPasskey struct {
	Name         string
	CredentialID []byte
	PublicKey    []byte
	SignCount    uint32
	Transports   []string
	AAGUID       []byte
	Flags        byte
}

var b64 = base64.RawURLEncoding

//nolint:gosec // G101: a column list, not a credential
const passkeyCols = `id, user_id, name, credential_id, public_key, sign_count, transports, aaguid, flags, created_at, last_used_at`

func scanPasskey(row interface{ Scan(...any) error }) (Passkey, error) {
	var p Passkey
	var cred, pub, transports, aaguid string
	var sc, created int64
	var flags int
	var used sql.NullInt64
	if err := row.Scan(&p.ID, &p.UserID, &p.Name, &cred, &pub, &sc, &transports, &aaguid, &flags, &created, &used); err != nil {
		return Passkey{}, notFound(err)
	}
	var err error
	if p.CredentialID, err = b64.DecodeString(cred); err != nil {
		return Passkey{}, err
	}
	if p.PublicKey, err = b64.DecodeString(pub); err != nil {
		return Passkey{}, err
	}
	if aaguid != "" {
		if p.AAGUID, err = hex.DecodeString(aaguid); err != nil {
			return Passkey{}, err
		}
	}
	if transports != "" {
		p.Transports = strings.Split(transports, ",")
	}
	p.SignCount, p.Flags = uint32(sc), byte(flags) //nolint:gosec // G115: stored from a uint32 and a byte
	p.CreatedAt, p.LastUsedAt = time.UnixMilli(created).UTC(), fromMS(used)
	return p, nil
}

// ListPasskeys returns the user's passkeys, oldest first.
func (s *Store) ListPasskeys(ctx context.Context, userID string) ([]Passkey, error) {
	rows, err := s.db.QueryContext(ctx, s.d.Rebind("SELECT "+passkeyCols+" FROM passkeys WHERE user_id = ? ORDER BY created_at, id"), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Passkey
	for rows.Next() {
		p, err := scanPasskey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PasskeyByCredentialID finds the passkey a sign-in names by credential ID
// (the first step of a passwordless sign-in: it says whose passkey it is).
func (s *Store) PasskeyByCredentialID(ctx context.Context, credentialID []byte) (Passkey, error) {
	return scanPasskey(s.db.QueryRowContext(ctx, s.d.Rebind("SELECT "+passkeyCols+" FROM passkeys WHERE credential_id = ?"), b64.EncodeToString(credentialID)))
}

// AddPasskey stores a passkey, at most max per user (ErrLimit), and signs the
// user out everywhere but keepSessionID like enabling TOTP does. When the
// account had no second factor before, recoveryHashes become its recovery
// codes (so losing the only passkey is not a lock-out) and issued reports it.
// ErrConflict if the credential is already registered.
func (s *Store) AddPasskey(ctx context.Context, userID, keepSessionID string, in NewPasskey, max int, recoveryHashes []string) (p Passkey, issued bool, err error) {
	p = Passkey{ID: ids.New(), UserID: userID, Name: in.Name, CredentialID: in.CredentialID, PublicKey: in.PublicKey,
		SignCount: in.SignCount, Transports: in.Transports, AAGUID: in.AAGUID, Flags: in.Flags}
	err = s.inTx(ctx, true, func(t *tx) error {
		var n int
		if err := t.queryRow(ctx, "SELECT count(*) FROM passkeys WHERE user_id = ?", userID).Scan(&n); err != nil {
			return err
		}
		if n >= max {
			return ErrLimit
		}
		var totp, emailOTP bool
		if err := t.queryRow(ctx, "SELECT totp_enabled, email_otp_enabled FROM users WHERE id = ?", userID).Scan(&totp, &emailOTP); err != nil {
			return notFound(err)
		}
		now := s.nowMS()
		p.CreatedAt = time.UnixMilli(now).UTC()
		if _, err := t.exec(ctx, `INSERT INTO passkeys (id, user_id, name, credential_id, public_key, sign_count, transports, aaguid, flags, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, p.ID, userID, p.Name, b64.EncodeToString(p.CredentialID), b64.EncodeToString(p.PublicKey),
			int64(p.SignCount), strings.Join(p.Transports, ","), hex.EncodeToString(p.AAGUID), int(p.Flags), now); err != nil {
			return conflict(err)
		}
		if issued = n == 0 && !totp && !emailOTP; issued {
			if err := t.replaceRecoveryCodes(ctx, userID, recoveryHashes, now); err != nil {
				return err
			}
		}
		return t.revokeSessionsExcept(ctx, userID, keepSessionID)
	})
	if err != nil {
		return Passkey{}, false, err
	}
	return p, issued, nil
}

// ErrLastSignIn: removing this would leave the account no way to sign in.
var ErrLastSignIn = errors.New("store: last way to sign in")

// DeletePasskey removes one of the user's passkeys (ErrNotFound if it is not
// theirs). Like removing another second factor it signs the user out
// everywhere but keepSessionID and revokes their API tokens and OAuth grants.
// When no second factor is left the recovery codes go too. ErrConflict when
// it is the last second factor and mandatory is set (hosted mode: 2FA cannot
// be turned off). ErrLastSignIn when it is the last passkey of an account with
// no password and no SSO link.
func (s *Store) DeletePasskey(ctx context.Context, userID, keepSessionID, id string, mandatory bool) error {
	return s.inTx(ctx, true, func(t *tx) error {
		if err := t.lockUser(ctx, userID); err != nil {
			return err
		}
		var totp, emailOTP, sso bool
		var hash string
		if err := t.queryRow(ctx, `SELECT totp_enabled, email_otp_enabled, password_hash,
			(SELECT count(*) > 0 FROM sso_identities WHERE user_id = users.id) FROM users WHERE id = ?`, userID).Scan(&totp, &emailOTP, &hash, &sso); err != nil {
			return notFound(err)
		}
		var n int
		if err := t.queryRow(ctx, "SELECT count(*) FROM passkeys WHERE user_id = ?", userID).Scan(&n); err != nil {
			return err
		}
		if n == 1 && strings.HasPrefix(hash, "!") && !sso {
			// No password and no SSO link: the last passkey is the only way in.
			return ErrLastSignIn
		}
		last := n == 1 && !totp && !emailOTP
		if mandatory && last {
			return ErrConflict
		}
		if err := t.execOne(ctx, "DELETE FROM passkeys WHERE id = ? AND user_id = ?", id, userID); err != nil {
			return err
		}
		if last {
			if err := t.replaceRecoveryCodes(ctx, userID, nil, 0); err != nil {
				return err
			}
		}
		if err := t.revokeSessionsExcept(ctx, userID, keepSessionID); err != nil {
			return err
		}
		return t.revokeUserTokens(ctx, userID, "passkey_remove", s.nowMS())
	})
}

// UsePasskey records a successful assertion: the new signature counter, the
// flags and the time. It is conditional, so two assertions racing with the
// same counter cannot both pass: ErrConflict means the counter did not move
// forward (a cloned authenticator, or a replay). Authenticators that never
// count (always 0) are accepted.
func (s *Store) UsePasskey(ctx context.Context, userID, id string, signCount uint32, flags byte) error {
	return s.inTx(ctx, true, func(t *tx) error {
		res, err := t.exec(ctx, `UPDATE passkeys SET sign_count = ?, flags = ?, last_used_at = ?
			WHERE id = ? AND user_id = ? AND (sign_count < ? OR (sign_count = 0 AND ? = 0))`,
			int64(signCount), int(flags), s.nowMS(), id, userID, int64(signCount), int64(signCount))
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return conflictIfNone(err)
		}
		return nil
	})
}

// Ceremony purposes of a stored challenge.
const (
	PasskeyRegister = "register"
	PasskeyLogin    = "login"  // passwordless sign-in (no user yet)
	PasskeySecond   = "second" // second factor after a password
	PasskeyStepUp   = "stepup" // re-verification of a signed-in user
)

const passkeyChallengeTTL = 5 * time.Minute

// SavePasskeyChallenge keeps a ceremony's session data (JSON) for a few
// minutes under a fresh ID (only its hash is stored), bound to userID ("" for
// a passwordless sign-in), and deletes expired ones.
func (s *Store) SavePasskeyChallenge(ctx context.Context, purpose, userID, data string) (string, error) {
	id := ids.Token()
	now := s.nowMS()
	err := s.inTx(ctx, true, func(t *tx) error {
		if _, err := t.exec(ctx, "DELETE FROM passkey_challenges WHERE expires_at < ?", now); err != nil {
			return err
		}
		_, err := t.exec(ctx, "INSERT INTO passkey_challenges (id, purpose, user_id, data, expires_at) VALUES (?, ?, ?, ?, ?)",
			ids.Hash(id), purpose, nullStr(userID), data, now+passkeyChallengeTTL.Milliseconds())
		return err
	})
	return id, err
}

// TakePasskeyChallenge returns and deletes a saved ceremony (single use).
// ErrNotFound if the ID is unknown, expired, for another purpose or bound to
// another user. A ceremony that fails to verify is spent: the client asks again.
func (s *Store) TakePasskeyChallenge(ctx context.Context, id, purpose, userID string) (string, error) {
	var data string
	var mismatch bool
	err := s.inTx(ctx, true, func(t *tx) error {
		var uid sql.NullString
		var p string
		var exp int64
		h := ids.Hash(id)
		if err := t.queryRow(ctx, "SELECT purpose, user_id, data, expires_at FROM passkey_challenges WHERE id = ?", h).Scan(&p, &uid, &data, &exp); err != nil {
			return notFound(err)
		}
		if err := t.execOne(ctx, "DELETE FROM passkey_challenges WHERE id = ?", h); err != nil {
			return err
		}
		mismatch = p != purpose || uid.String != userID || exp < s.nowMS()
		return nil // commit: a mismatching ceremony is spent too
	})
	if err != nil {
		return "", err
	}
	if mismatch {
		return "", ErrNotFound
	}
	return data, nil
}
