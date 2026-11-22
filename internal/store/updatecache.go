package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const settingUpdateCache = "update_cache"

// UpdateCache is the last answer of the release check (internal/updates),
// kept in the settings table so every instance shares it.
type UpdateCache struct {
	Latest      string    // tag of the latest release; "" = never checked successfully
	Changelog   string    // release notes, plain text, length-capped by the checker
	CheckedAt   time.Time // last successful check
	AttemptedAt time.Time // last attempt, successful or not (spaces out retries)
}

type updateCacheJSON struct {
	Latest      string `json:"latest"`
	Changelog   string `json:"changelog"`
	CheckedAt   int64  `json:"checkedAt"`
	AttemptedAt int64  `json:"attemptedAt"`
}

// UpdateCache reads the cached release check; the zero value if none.
func (s *Store) UpdateCache(ctx context.Context) (UpdateCache, error) {
	var v string
	err := s.db.QueryRowContext(ctx, s.d.Rebind("SELECT value FROM settings WHERE key = ?"), settingUpdateCache).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return UpdateCache{}, nil
	}
	if err != nil {
		return UpdateCache{}, err
	}
	var j updateCacheJSON
	if json.Unmarshal([]byte(v), &j) != nil {
		return UpdateCache{}, nil // corrupt: behave as never checked
	}
	return UpdateCache{Latest: j.Latest, Changelog: j.Changelog, CheckedAt: fromMilli(j.CheckedAt), AttemptedAt: fromMilli(j.AttemptedAt)}, nil
}

// SetUpdateCache stores the release check result.
func (s *Store) SetUpdateCache(ctx context.Context, c UpdateCache) error {
	b, err := json.Marshal(updateCacheJSON{Latest: c.Latest, Changelog: c.Changelog,
		CheckedAt: toMilli(c.CheckedAt), AttemptedAt: toMilli(c.AttemptedAt)})
	if err != nil {
		return err
	}
	return s.inTx(ctx, true, func(t *tx) error {
		_, err := t.exec(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			settingUpdateCache, string(b), s.nowMS())
		return err
	})
}

func toMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMilli(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.UnixMilli(n).UTC()
}
