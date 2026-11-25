package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Admin configuration values (AI endpoint, email, single sign-on, update
// webhook, ...) live in the settings table under the "cfg." prefix. Which keys
// exist, their types, validation, environment overrides and which ones are
// secret is decided by the caller (internal/api); this layer only stores
// strings. Secret values are stored as keyring ciphertext, never in clear text.
const configPrefix = "cfg."

// MaxConfigValueLen caps a stored configuration value (ciphertext included).
const MaxConfigValueLen = 4096

// ConfigValues returns every stored configuration value by key (without the
// prefix). Unscoped: configuration only, no user or household content.
func (s *Store) ConfigValues(ctx context.Context) (map[string]string, error) {
	out := map[string]string{}
	err := s.inTx(ctx, false, func(t *tx) error {
		rows, err := t.query(ctx, "SELECT key, value FROM settings WHERE key LIKE ?", configPrefix+"%")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k, v string
			if err := rows.Scan(&k, &v); err != nil {
				return err
			}
			out[strings.TrimPrefix(k, configPrefix)] = v
		}
		return rows.Err()
	})
	return out, err
}

// SetConfig writes configuration values in one transaction: a nil value
// deletes the key. Keys must be non-empty and values at most
// MaxConfigValueLen bytes (ErrInvalid otherwise).
func (s *Store) SetConfig(ctx context.Context, values map[string]*string) error {
	for k, v := range values {
		if k == "" || len(k) > 64 || (v != nil && len(*v) > MaxConfigValueLen) {
			return ErrInvalid
		}
	}
	return s.inTx(ctx, true, func(t *tx) error {
		now := s.nowMS()
		for k, v := range values {
			var err error
			if v == nil {
				_, err = t.exec(ctx, "DELETE FROM settings WHERE key = ?", configPrefix+k)
			} else {
				_, err = t.exec(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
					ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, configPrefix+k, *v, now)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// RotateConfigSecrets rewrites the given secret configuration keys through fn
// (gunkly rotate-key), in one transaction; it returns how many changed.
func (s *Store) RotateConfigSecrets(ctx context.Context, keys []string, fn func(key, ciphertext string) (string, error)) (int, error) {
	n := 0
	err := s.inTx(ctx, true, func(t *tx) error {
		for _, k := range keys {
			var ct string
			switch err := t.queryRow(ctx, "SELECT value FROM settings WHERE key = ?", configPrefix+k).Scan(&ct); {
			case errors.Is(err, sql.ErrNoRows):
				continue
			case err != nil:
				return err
			}
			next, err := fn(k, ct)
			if err != nil {
				return err
			}
			if next == ct {
				continue
			}
			if _, err := t.exec(ctx, "UPDATE settings SET value = ?, updated_at = ? WHERE key = ?", next, s.nowMS(), configPrefix+k); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}
