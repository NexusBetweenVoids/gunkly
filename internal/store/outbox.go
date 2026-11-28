package store

import (
	"context"
	"strconv"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/db"
)

// AuditMailGaveUp records an email that was never delivered; its meta holds
// only the kind, the attempts made and the reason, never the address or text.
const AuditMailGaveUp = "mail_gave_up"

// Reasons a queued email is given up.
const (
	MailGaveUpAttempts   = "attempts"   // every attempt failed
	MailGaveUpExpired    = "expired"    // the message outlived its usefulness (an expired code)
	MailGaveUpUnreadable = "unreadable" // the sealed payload could not be opened
)

// OutboxMail is one queued email. Payload is sealed by the caller (it holds
// the address and the text); the store never sees them.
type OutboxMail struct {
	ID        string // chosen by the caller: it is part of the seal's context
	Kind      string
	UserID    string // recipient account; "" = none
	Payload   string
	Attempts  int // attempts made so far
	ExpiresAt time.Time
}

// EnqueueMail queues m for its first attempt at now.
func (s *Store) EnqueueMail(ctx context.Context, m OutboxMail, now time.Time) error {
	return s.inTx(ctx, true, func(t *tx) error {
		_, err := t.exec(ctx, `INSERT INTO mail_outbox (id, kind, user_id, payload, attempts, next_attempt_at, expires_at, created_at)
			VALUES (?, ?, ?, ?, 0, ?, ?, ?)`, m.ID, m.Kind, nullStr(m.UserID), m.Payload, now.UnixMilli(), m.ExpiresAt.UnixMilli(), now.UnixMilli())
		return err
	})
}

// OutboxSecrets pages through every queued email's sealed payload by ID
// (system use: gunkly rotate-key). Pass the last ID of the previous page as
// afterID.
func (s *Store) OutboxSecrets(ctx context.Context, afterID string, limit int) ([]ChannelSecret, error) {
	return s.secretsPage(ctx, "mail_outbox", "payload", afterID, limit)
}

// ReplaceOutboxPayload swaps a queued email's sealed payload if it still
// equals old (system use: rotate-key). ErrNotFound if the email was sent or
// given up meanwhile, ErrConflict if it changed.
func (s *Store) ReplaceOutboxPayload(ctx context.Context, id, old, replacement string) error {
	return s.replaceSecret(ctx, "mail_outbox", "payload", id, old, replacement)
}

// ClaimMail leases up to limit emails due at now: their next attempt moves to
// now+lease, so a crashed sender's claims come back after the lease. Messages
// past their expiry are given up first (and returned as expired for logging,
// without any address or text).
func (s *Store) ClaimMail(ctx context.Context, now time.Time, lease time.Duration, limit int) (claimed, expired []OutboxMail, err error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	err = s.inTx(ctx, true, func(t *tx) error {
		claimed, expired = nil, nil
		err := func() error {
			rows, err := t.query(ctx, "SELECT id, kind, COALESCE(user_id, ''), attempts FROM mail_outbox WHERE expires_at <= ?", now.UnixMilli())
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var m OutboxMail
				if err := rows.Scan(&m.ID, &m.Kind, &m.UserID, &m.Attempts); err != nil {
					return err
				}
				expired = append(expired, m)
			}
			return rows.Err()
		}()
		if err != nil {
			return err
		}
		for _, m := range expired {
			if err := t.dropMail(ctx, m, MailGaveUpExpired, now); err != nil {
				return err
			}
		}
		q := `SELECT id, kind, COALESCE(user_id, ''), payload, attempts, expires_at FROM mail_outbox
			WHERE next_attempt_at <= ? ORDER BY next_attempt_at, id LIMIT ?`
		if t.d == db.Postgres {
			q += " FOR UPDATE SKIP LOCKED"
		}
		err = func() error {
			rows, err := t.query(ctx, q, now.UnixMilli(), limit)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var m OutboxMail
				var exp int64
				if err := rows.Scan(&m.ID, &m.Kind, &m.UserID, &m.Payload, &m.Attempts, &exp); err != nil {
					return err
				}
				m.ExpiresAt = time.UnixMilli(exp).UTC()
				claimed = append(claimed, m)
			}
			return rows.Err()
		}()
		if err != nil {
			return err
		}
		for _, m := range claimed {
			if _, err := t.exec(ctx, "UPDATE mail_outbox SET next_attempt_at = ? WHERE id = ?", now.Add(lease).UnixMilli(), m.ID); err != nil {
				return err
			}
		}
		return nil
	})
	return claimed, expired, err
}

// MailSent removes a delivered email (address and text with it).
func (s *Store) MailSent(ctx context.Context, id string) error {
	return s.inTx(ctx, true, func(t *tx) error {
		_, err := t.exec(ctx, "DELETE FROM mail_outbox WHERE id = ?", id)
		return err
	})
}

// RetryMail records a failed attempt and schedules the next one.
func (s *Store) RetryMail(ctx context.Context, id string, attempts int, next time.Time) error {
	return s.inTx(ctx, true, func(t *tx) error {
		_, err := t.exec(ctx, "UPDATE mail_outbox SET attempts = ?, next_attempt_at = ? WHERE id = ?", attempts, next.UnixMilli(), id)
		return err
	})
}

// GiveUpMail removes an email that will not be sent (reason: a MailGaveUp
// constant) and audits it for the recipient without address or text.
func (s *Store) GiveUpMail(ctx context.Context, m OutboxMail, reason string, now time.Time) error {
	return s.inTx(ctx, true, func(t *tx) error { return t.dropMail(ctx, m, reason, now) })
}

func (t *tx) dropMail(ctx context.Context, m OutboxMail, reason string, now time.Time) error {
	res, err := t.exec(ctx, "DELETE FROM mail_outbox WHERE id = ?", m.ID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return err // already gone (sent by another process)
	}
	var actor string
	if m.UserID != "" {
		if err := t.queryRow(ctx, "SELECT id FROM users WHERE id = ?", m.UserID).Scan(&actor); err != nil {
			actor = ""
		}
	}
	return t.insertAudit(ctx, AuditEvent{ActorUserID: actor, Event: AuditMailGaveUp,
		Meta: map[string]string{"kind": m.Kind, "attempts": strconv.Itoa(m.Attempts), "reason": reason}}, now.UnixMilli())
}
