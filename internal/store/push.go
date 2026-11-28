package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// PushSubscription is one browser's Web Push subscription as shown to its
// owner. The endpoint is a capability (anyone holding it and the keys can push
// to the browser): it is never returned, only its hash, so a device can
// recognise its own subscription.
type PushSubscription struct {
	ID           string
	EndpointHash string // hex SHA-256 of the endpoint
	UserAgent    string
	CreatedAt    time.Time
	LastUsedAt   time.Time
}

// NewPushSubscription is a validated subscription (internal/notify seals the
// keys, bound to ID).
type NewPushSubscription struct {
	ID         string
	Endpoint   string
	Ciphertext string // sealed keys
	UserAgent  string
	Max        int // limits.PushSubscriptionsPerUser, resolved by the caller
}

// EndpointHash is the identifier of an endpoint in PushSubscription.
func EndpointHash(endpoint string) string {
	h := sha256.Sum256([]byte(endpoint))
	return hex.EncodeToString(h[:])
}

const maxUserAgentLen = 200

// CreatePushSubscription stores the actor's subscription. An endpoint is one
// browser: if it is already registered (by the actor or by someone who used
// the same browser before) the old registration is replaced, so reminders
// follow whoever is signed in there now. ErrLimit at the per-user limit.
func (s *Store) CreatePushSubscription(ctx context.Context, a Actor, in NewPushSubscription) (PushSubscription, error) {
	if a.UserID == "" || in.ID == "" || in.Endpoint == "" || in.Ciphertext == "" || in.Max <= 0 {
		return PushSubscription{}, ErrInvalid
	}
	ua := strings.ToValidUTF8(in.UserAgent, "")
	if len(ua) > maxUserAgentLen {
		ua = strings.ToValidUTF8(ua[:maxUserAgentLen], "")
	}
	now := s.nowMS()
	err := s.inTx(ctx, true, func(t *tx) error {
		if err := t.lockUser(ctx, a.UserID); err != nil { // the limit holds under concurrency
			return err
		}
		if _, err := t.exec(ctx, "DELETE FROM push_subscriptions WHERE endpoint = ?", in.Endpoint); err != nil {
			return err
		}
		var n int
		if err := t.queryRow(ctx, "SELECT count(*) FROM push_subscriptions WHERE user_id = ?", a.UserID).Scan(&n); err != nil {
			return err
		}
		if n >= in.Max {
			return ErrLimit
		}
		_, err := t.exec(ctx, `INSERT INTO push_subscriptions (id, user_id, endpoint, keys_ciphertext, user_agent, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`, in.ID, a.UserID, in.Endpoint, in.Ciphertext, ua, now)
		return notFoundFK(conflict(err))
	})
	if err != nil {
		return PushSubscription{}, err
	}
	return PushSubscription{ID: in.ID, EndpointHash: EndpointHash(in.Endpoint), UserAgent: ua, CreatedAt: time.UnixMilli(now).UTC()}, nil
}

// ListPushSubscriptions returns the actor's subscriptions, oldest first.
func (s *Store) ListPushSubscriptions(ctx context.Context, a Actor) ([]PushSubscription, error) {
	var out []PushSubscription
	err := s.inTx(ctx, false, func(t *tx) error {
		rows, err := t.query(ctx, `SELECT id, endpoint, user_agent, created_at, last_used_at FROM push_subscriptions
			WHERE user_id = ? ORDER BY created_at, id`, a.UserID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var p PushSubscription
			var endpoint string
			var created int64
			var used sql.NullInt64
			if err := rows.Scan(&p.ID, &endpoint, &p.UserAgent, &created, &used); err != nil {
				return err
			}
			p.EndpointHash, p.CreatedAt, p.LastUsedAt = EndpointHash(endpoint), time.UnixMilli(created).UTC(), fromMS(used)
			out = append(out, p)
		}
		return rows.Err()
	})
	return out, err
}

// DeletePushSubscription removes one of the actor's subscriptions. ErrNotFound
// if it is not theirs.
func (s *Store) DeletePushSubscription(ctx context.Context, a Actor, id string) error {
	return s.inTx(ctx, true, func(t *tx) error {
		return t.execOne(ctx, "DELETE FROM push_subscriptions WHERE id = ? AND user_id = ?", id, a.UserID)
	})
}

// DeleteGonePushSubscription removes a subscription the push service reported
// gone (system use: the scheduler). Missing is not an error.
func (s *Store) DeleteGonePushSubscription(ctx context.Context, id string) error {
	return s.inTx(ctx, true, func(t *tx) error {
		_, err := t.exec(ctx, "DELETE FROM push_subscriptions WHERE id = ?", id)
		return err
	})
}

// MarkPushUsed records a successful push (system use: the scheduler).
func (s *Store) MarkPushUsed(ctx context.Context, id string, now time.Time) error {
	return s.inTx(ctx, true, func(t *tx) error {
		_, err := t.exec(ctx, "UPDATE push_subscriptions SET last_used_at = ? WHERE id = ?", now.UnixMilli(), id)
		return err
	})
}

// PushSecrets pages through every subscription's sealed keys by ID (system
// use: gunkly rotate-key).
func (s *Store) PushSecrets(ctx context.Context, afterID string, limit int) ([]ChannelSecret, error) {
	return s.secretsPage(ctx, "push_subscriptions", "keys_ciphertext", afterID, limit)
}

// ReplacePushCiphertext swaps a subscription's sealed keys if they still equal
// old (system use: rotate-key). ErrConflict if they changed meanwhile.
func (s *Store) ReplacePushCiphertext(ctx context.Context, id, old, replacement string) error {
	return s.replaceSecret(ctx, "push_subscriptions", "keys_ciphertext", id, old, replacement)
}

// --- VAPID key (instance-wide, sealed) ---

const settingVAPID = "vapid_private_key"

// VAPIDSecret returns the sealed VAPID private key; "" if none was generated.
func (s *Store) VAPIDSecret(ctx context.Context) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, s.d.Rebind("SELECT value FROM settings WHERE key = ?"), settingVAPID).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// InsertVAPIDSecret stores the sealed key unless one exists (concurrent first
// use on several instances: the first wins). Reports whether it was stored.
func (s *Store) InsertVAPIDSecret(ctx context.Context, sealed string) (bool, error) {
	stored := false
	err := s.inTx(ctx, true, func(t *tx) error {
		res, err := t.exec(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?) ON CONFLICT (key) DO NOTHING`,
			settingVAPID, sealed, s.nowMS())
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		stored = n > 0
		return err
	})
	return stored, err
}

// ReplaceVAPIDSecret swaps the sealed key if it still equals old (system use:
// rotate-key). ErrConflict if it changed meanwhile.
func (s *Store) ReplaceVAPIDSecret(ctx context.Context, old, replacement string) error {
	return s.inTx(ctx, true, func(t *tx) error {
		res, err := t.exec(ctx, "UPDATE settings SET value = ? WHERE key = ? AND value = ?", replacement, settingVAPID, old)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return ErrConflict
		}
		return nil
	})
}
