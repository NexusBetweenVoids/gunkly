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
}

// DefaultRetention is the PRD default.
var DefaultRetention = RetentionPolicy{AuditLog: 90 * 24 * time.Hour, Deliveries: 30 * 24 * time.Hour}

// purgeBatch bounds each DELETE so no single statement holds the write lock long.
const purgeBatch = 1000

// PurgeExpired deletes data past retention and expired credentials, in
// bounded batches (system use: the daily retention job). Returns rows deleted.
func (s *Store) PurgeExpired(ctx context.Context, p RetentionPolicy, now time.Time) (int64, error) {
	n := now.UnixMilli()
	jobs := []struct {
		table, pk, where string
		arg              int64
	}{
		{"audit_log", "id", "created_at < ?", now.Add(-p.AuditLog).UnixMilli()},
		{"deliveries", "id", "created_at < ?", now.Add(-p.Deliveries).UnixMilli()},
		{"sessions", "id", "expires_at < ?", n},
		{"user_tokens", "token_hash", "expires_at < ?", n},
		{"invites", "id", "expires_at < ? OR revoked_at IS NOT NULL", n},
		{"mark_done_tokens", "token_hash", "expires_at < ?", n},
		{"undo_tokens", "token_hash", "expires_at < ?", n},
		{"rate_limits", "key", "expires_at < ?", n},
		{"calendar_feeds", "id", "revoked_at IS NOT NULL AND revoked_at < ?", n},
	}
	var total int64
	for _, j := range jobs {
		if (j.table == "audit_log" && p.AuditLog <= 0) || (j.table == "deliveries" && p.Deliveries <= 0) {
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
