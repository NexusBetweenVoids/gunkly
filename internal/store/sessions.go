package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/ids"
)

// Session is a signed-in browser or device. The token itself is never
// stored; only its SHA-256 hash.
type Session struct {
	ID         string
	UserID     string
	Label      string // simplified user agent, e.g. "Firefox/Android"
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time // absolute maximum
	StepUpAt   time.Time // zero = never
}

// NewSession is the input to CreateSession.
type NewSession struct {
	UserID    string
	TokenHash string
	Label     string
	ExpiresAt time.Time
}

// CreateSession stores a new session. The caller has just verified the
// user's credentials, so the session starts with a fresh step-up.
func (s *Store) CreateSession(ctx context.Context, in NewSession) (Session, error) {
	now := s.nowMS()
	ss := Session{
		ID: ids.New(), UserID: in.UserID, Label: in.Label, ExpiresAt: in.ExpiresAt.UTC(),
		CreatedAt: time.UnixMilli(now).UTC(), LastSeenAt: time.UnixMilli(now).UTC(), StepUpAt: time.UnixMilli(now).UTC(),
	}
	err := s.inTx(ctx, true, func(t *tx) error {
		_, err := t.exec(ctx, `INSERT INTO sessions (id, token_hash, user_id, user_agent, created_at, last_seen_at, expires_at, step_up_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, ss.ID, in.TokenHash, in.UserID, in.Label, now, now, in.ExpiresAt.UnixMilli(), now)
		return notFoundFK(err)
	})
	if err != nil {
		return Session{}, err
	}
	return ss, nil
}

const sessionCols = `s.id, s.user_id, s.user_agent, s.created_at, s.last_seen_at, s.expires_at, s.step_up_at`

func scanSession(row interface{ Scan(...any) error }, extra ...any) (Session, error) {
	var ss Session
	var created, seen, exp int64
	var step sql.NullInt64
	if err := row.Scan(append([]any{&ss.ID, &ss.UserID, &ss.Label, &created, &seen, &exp, &step}, extra...)...); err != nil {
		return Session{}, notFound(err)
	}
	ss.CreatedAt, ss.LastSeenAt, ss.ExpiresAt = time.UnixMilli(created).UTC(), time.UnixMilli(seen).UTC(), time.UnixMilli(exp).UTC()
	ss.StepUpAt = fromMS(step)
	return ss, nil
}

// SessionByTokenHash returns the session and its user. Expiry policy is the
// caller's (internal/api), which deletes expired sessions it finds.
func (s *Store) SessionByTokenHash(ctx context.Context, tokenHash string) (Session, User, error) {
	var ss Session
	var u User
	err := s.inTx(ctx, false, func(t *tx) error {
		var err error
		if ss, err = scanSession(t.queryRow(ctx, "SELECT "+sessionCols+" FROM sessions s WHERE s.token_hash = ?", tokenHash)); err != nil {
			return err
		}
		u, err = scanUser(t.queryRow(ctx, "SELECT "+userCols+" FROM users WHERE id = ?", ss.UserID))
		return err
	})
	return ss, u, err
}

// TouchSession records activity on a session and its user. Callers throttle
// it (it is a write on every request otherwise).
func (s *Store) TouchSession(ctx context.Context, sessionID string) error {
	now := s.nowMS()
	return s.inTx(ctx, true, func(t *tx) error {
		var userID string
		if err := t.queryRow(ctx, "SELECT user_id FROM sessions WHERE id = ?", sessionID).Scan(&userID); err != nil {
			return notFound(err)
		}
		if _, err := t.exec(ctx, "UPDATE sessions SET last_seen_at = ? WHERE id = ?", now, sessionID); err != nil {
			return err
		}
		_, err := t.exec(ctx, "UPDATE users SET last_active_at = ? WHERE id = ?", now, userID)
		return err
	})
}

// StepUpSession marks the session as freshly re-verified.
func (s *Store) StepUpSession(ctx context.Context, userID, sessionID string) error {
	return s.inTx(ctx, true, func(t *tx) error {
		return t.execOne(ctx, "UPDATE sessions SET step_up_at = ? WHERE id = ? AND user_id = ?", s.nowMS(), sessionID, userID)
	})
}

// ListSessions lists the user's sessions, most recently used first.
func (s *Store) ListSessions(ctx context.Context, userID string) ([]Session, error) {
	var out []Session
	err := s.inTx(ctx, false, func(t *tx) error {
		rows, err := t.query(ctx, "SELECT "+sessionCols+" FROM sessions s WHERE s.user_id = ? ORDER BY s.last_seen_at DESC, s.id", userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			ss, err := scanSession(rows)
			if err != nil {
				return err
			}
			out = append(out, ss)
		}
		return rows.Err()
	})
	return out, err
}

// DeleteSession removes one of the user's sessions; ErrNotFound if it is not
// theirs.
func (s *Store) DeleteSession(ctx context.Context, userID, sessionID string) error {
	return s.inTx(ctx, true, func(t *tx) error {
		return t.execOne(ctx, "DELETE FROM sessions WHERE id = ? AND user_id = ?", sessionID, userID)
	})
}

// DeleteOtherSessions removes all of the user's sessions except keepID
// (empty keepID removes all). Returns how many were removed.
func (s *Store) DeleteOtherSessions(ctx context.Context, userID, keepID string) (int, error) {
	var n int64
	err := s.inTx(ctx, true, func(t *tx) error {
		res, err := t.exec(ctx, "DELETE FROM sessions WHERE user_id = ? AND id <> ?", userID, keepID)
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	return int(n), err
}
