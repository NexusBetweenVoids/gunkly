package store

import (
	"context"
	"database/sql"
	"errors"
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
	// UnverifiedAccounts (zero = skip; the caller sets it only when email is
	// enabled) deletes accounts with an email address that was never verified
	// and that belong to no household, once they are this old. It frees
	// addresses squatted at sign-up.
	UnverifiedAccounts time.Duration
}

// UnverifiedAccountMaxAge is how long an unverified, household-less account
// with an email address is kept (PRD "Data retention").
const UnverifiedAccountMaxAge = 7 * 24 * time.Hour

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
		{"webhook_deliveries", "id", "created_at < ?", now.Add(-p.Deliveries).UnixMilli(), p.Deliveries <= 0},
		{"sessions", "id", "expires_at < ?", n, false},
		{"sessions", "id", "last_seen_at < ?", now.Add(-p.SessionIdle).UnixMilli(), p.SessionIdle <= 0},
		{"sessions", "id", "created_at < ?", now.Add(-p.SessionMax).UnixMilli(), p.SessionMax <= 0},
		{"user_tokens", "token_hash", "expires_at < ?", n, false},
		{"pow_used", "challenge_hash", "expires_at < ?", n, false},
		{"passkey_challenges", "id", "expires_at < ?", n, false},
		{"invites", "id", "expires_at < ? OR revoked_at IS NOT NULL", n, false},
		{"mark_done_tokens", "token_hash", "expires_at < ?", n, false},
		{"undo_tokens", "token_hash", "expires_at < ?", n, false},
		{"rate_limits", "key", "expires_at < ?", n, false},
		{"api_tokens", "id", "expires_at < ? OR revoked_at IS NOT NULL", n, false},
		// OAuth (ADR 0016): spent and expired codes and tokens, revoked, expired
		// and never-redeemed grants, then clients nobody used (grants first:
		// a client with a grant is kept).
		{"oauth_codes", "code_hash", "expires_at < ?", n, false},
		{"oauth_tokens", "token_hash", "expires_at < ?", n, false},
		{"oauth_grants", "id", "expires_at < ? OR revoked_at IS NOT NULL", n, false},
		{"oauth_grants", "id", "activated_at IS NULL AND created_at < ?", now.Add(-time.Hour).UnixMilli(), false},
		{"oauth_clients", "id", unusedClient + " AND created_at < ? AND NOT EXISTS (SELECT 1 FROM oauth_grants g WHERE g.client_id = oauth_clients.id)",
			now.Add(-OAuthUnusedClientAge).UnixMilli(), false},
		{"oauth_clients", "id", "last_used_at < ? AND NOT EXISTS (SELECT 1 FROM oauth_grants g WHERE g.client_id = oauth_clients.id)",
			now.Add(-OAuthIdleClientAge).UnixMilli(), false},
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
	if p.UnverifiedAccounts > 0 {
		n, err := s.purgeUnverified(ctx, now.Add(-p.UnverifiedAccounts))
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// purgeUnverified deletes accounts created before cutoff whose email was
// never verified and that have no household membership (never admins), in
// bounded batches, through DeleteAccount so everything personal goes.
func (s *Store) purgeUnverified(ctx context.Context, cutoff time.Time) (int64, error) {
	var total int64
	for {
		var ids []string
		err := s.inTx(ctx, false, func(t *tx) error {
			return t.each(ctx, func(r *sql.Rows) error {
				var id string
				if err := r.Scan(&id); err != nil {
					return err
				}
				ids = append(ids, id)
				return nil
			}, `SELECT id FROM users WHERE email IS NOT NULL AND email_verified = ? AND is_admin = ? AND created_at < ?
				AND NOT EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = users.id) ORDER BY id LIMIT ?`,
				false, false, cutoff.UnixMilli(), purgeBatch)
		})
		if err != nil {
			return total, fmt.Errorf("store: purge unverified accounts: %w", err)
		}
		deleted := 0
		for _, id := range ids {
			// A user who joined a household since the query is left alone: it
			// would need an owner decision, so DeleteAccount refuses.
			switch _, err := s.DeleteAccount(ctx, id, AccountDeletion{RefuseAdmin: true}); {
			case err == nil:
				deleted++
			case errors.Is(err, ErrNotFound), errors.Is(err, ErrOwnerRequired), errors.Is(err, ErrLastAdmin), errors.Is(err, ErrConflict):
			default:
				return total, fmt.Errorf("store: purge unverified accounts: %w", err)
			}
		}
		total += int64(deleted)
		if deleted == 0 || len(ids) < purgeBatch {
			return total, nil
		}
	}
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
