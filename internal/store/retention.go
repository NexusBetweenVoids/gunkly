package store

import (
	"context"
	"fmt"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/db"
)

// RetentionPolicy holds the configurable retention periods (PRD "Data
// retention"). Expired sessions, tokens and invites are always deleted.
type RetentionPolicy struct {
	AuditLog   time.Duration // default 90 days
	Deliveries time.Duration // default 30 days
	// Session lifetimes (zero = skip): sessions idle longer than SessionIdle
	// or older than SessionMax are deleted alongside expired ones.
	SessionIdle, SessionMax time.Duration
}

// Policy returns the retention policy for the instance settings.
func (st InstanceSettings) Policy() RetentionPolicy {
	const day = 24 * time.Hour
	return RetentionPolicy{
		AuditLog: time.Duration(st.AuditRetentionDays) * day, Deliveries: time.Duration(st.DeliveryRetentionDays) * day,
		SessionIdle: time.Duration(st.SessionIdleDays) * day, SessionMax: time.Duration(st.SessionMaxDays) * day,
	}
}

// purgeBatch bounds each DELETE so no single statement holds the write lock long.
const purgeBatch = 1000

// PurgeExpired deletes data past retention and expired credentials, in
// bounded batches (system use: the daily retention job). Returns rows deleted.
func (s *Store) PurgeExpired(ctx context.Context, p RetentionPolicy, now time.Time) (int64, error) {
	n := now.UnixMilli()
	jobs := []struct {
		table, pk, where string
		arg              int64
		skip             bool
	}{
		{"audit_log", "id", "created_at < ?", now.Add(-p.AuditLog).UnixMilli(), p.AuditLog <= 0},
		{"deliveries", "id", "created_at < ?", now.Add(-p.Deliveries).UnixMilli(), p.Deliveries <= 0},
		{"sessions", "id", "expires_at < ?", n, false},
		{"sessions", "id", "last_seen_at < ?", now.Add(-p.SessionIdle).UnixMilli(), p.SessionIdle <= 0},
		{"sessions", "id", "created_at < ?", now.Add(-p.SessionMax).UnixMilli(), p.SessionMax <= 0},
		{"user_tokens", "token_hash", "expires_at < ?", n, false},
		{"pow_used", "challenge_hash", "expires_at < ?", n, false},
		{"invites", "id", "expires_at < ? OR revoked_at IS NOT NULL", n, false},
		{"mark_done_tokens", "token_hash", "expires_at < ?", n, false},
		{"undo_tokens", "token_hash", "expires_at < ?", n, false},
		{"rate_limits", "key", "expires_at < ?", n, false},
		{"calendar_feeds", "id", "revoked_at IS NOT NULL AND revoked_at < ?", n, false},
	}
	var total int64
	for _, j := range jobs {
		if j.skip {
			continue
		}
		// Table and column names are constants above, never input.
		q := fmt.Sprintf("DELETE FROM %s WHERE %s IN (SELECT %s FROM %s WHERE %s LIMIT %d)", j.table, j.pk, j.pk, j.table, j.where, purgeBatch)
		for {
			var affected int64
			err := s.inTx(ctx, true, func(t *tx) error {
				res, err := t.exec(ctx, q, j.arg)
				if err != nil {
					return err
				}
				affected, err = res.RowsAffected()
				return err
			})
			if err != nil {
				return total, fmt.Errorf("store: purge %s: %w", j.table, err)
			}
			total += affected
			if affected < purgeBatch {
				break
			}
		}
	}
	return total, nil
}

// ReclaimSpace returns free SQLite pages to the filesystem with at most
// maxBatches bounded PRAGMA incremental_vacuum(pagesPerBatch) runs — never a
// full VACUUM. A no-op on Postgres (autovacuum).
func (s *Store) ReclaimSpace(ctx context.Context, pagesPerBatch, maxBatches int) error {
	if s.d != db.SQLite || pagesPerBatch <= 0 {
		return nil
	}
	for range maxBatches {
		var free int
		if err := s.db.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&free); err != nil {
			return err
		}
		if free == 0 {
			return nil
		}
		if err := s.vacuumBatch(ctx, pagesPerBatch); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) vacuumBatch(ctx context.Context, pages int) error {
	// PRAGMA takes no bound parameters; pages is an int.
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf("PRAGMA incremental_vacuum(%d)", pages))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
	}
	return rows.Err()
}
