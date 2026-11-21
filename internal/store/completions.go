package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/ids"
)

// Completion sources.
const (
	SourceApp          = "app"
	SourceMarkDoneLink = "mark_done_link"
	SourceAPIToken     = "api_token"
)

// Completion is one entry in a chore's history.
type Completion struct {
	ID          string
	ChoreID     string
	UserID      string // "" = deleted user or household channel
	CompletedAt time.Time
	Source      string
	TokenID     string
	Note        string
	RevertedAt  time.Time
}

// CompletionInput records a completion. NextDue/DueDate are the chore's new
// occurrence, computed by the caller with internal/schedule (ignored for
// one-off chores, which are archived instead).
type CompletionInput struct {
	CompletedAt time.Time
	Source      string
	TokenID     string
	Note        string
	NextDue     time.Time
	DueDate     string
}

// CompleteChore records a completion by the actor and advances the chore.
// Member or owner (guests cannot complete). Archived chores are ErrConflict.
// Outstanding mark-done links for the chore are invalidated.
func (s *Store) CompleteChore(ctx context.Context, a Actor, householdID, choreID string, in CompletionInput) (Completion, error) {
	if in.Source != SourceApp && in.Source != SourceMarkDoneLink && in.Source != SourceAPIToken || in.CompletedAt.IsZero() || len(in.Note) > 2000 {
		return Completion{}, ErrInvalid
	}
	var c Completion
	err := s.write(ctx, a, householdID, RoleMember, func(t *tx, _ Role) error {
		var err error
		c, err = t.complete(ctx, householdID, choreID, a.UserID, in, s.nowMS())
		return err
	})
	if err != nil {
		return Completion{}, err
	}
	return c, nil
}

// complete records a completion by userID ("" = household channel) and
// advances the chore, inside t. The caller has authorized the completion.
func (t *tx) complete(ctx context.Context, householdID, choreID, userID string, in CompletionInput, now int64) (Completion, error) {
	c := Completion{ID: ids.New(), ChoreID: choreID, UserID: userID, CompletedAt: in.CompletedAt.UTC().Truncate(time.Millisecond),
		Source: in.Source, TokenID: in.TokenID, Note: in.Note}
	var kind, dueDate string
	var next, archived sql.NullInt64
	err := t.queryRow(ctx, "SELECT kind, due_date, next_due, archived_at FROM chores WHERE id = ? AND household_id = ?",
		choreID, householdID).Scan(&kind, &dueDate, &next, &archived)
	if err != nil {
		return Completion{}, notFound(err)
	}
	if archived.Valid {
		return Completion{}, ErrConflict
	}
	if _, err := t.exec(ctx, `INSERT INTO completions (id, chore_id, user_id, completed_at, source, token_id, note,
		prev_next_due, prev_due_date, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, choreID, nullStr(userID), c.CompletedAt.UnixMilli(), in.Source, nullStr(in.TokenID), in.Note, next, dueDate, now); err != nil {
		return Completion{}, err
	}
	if kind == KindOneOff {
		err = t.execOne(ctx, "UPDATE chores SET archived_at = ?, updated_at = ? WHERE id = ? AND household_id = ?",
			now, now, choreID, householdID)
	} else {
		if _, perr := time.Parse(time.DateOnly, in.DueDate); perr != nil {
			return Completion{}, ErrInvalid
		}
		err = t.execOne(ctx, "UPDATE chores SET next_due = ?, due_date = ?, updated_at = ? WHERE id = ? AND household_id = ?",
			ms(in.NextDue), in.DueDate, now, choreID, householdID)
	}
	if err != nil {
		return Completion{}, err
	}
	_, err = t.exec(ctx, "DELETE FROM mark_done_tokens WHERE chore_id = ?", choreID)
	return c, err
}

// ListCompletions returns a chore's history, newest first (limit <= 0 means 100).
func (s *Store) ListCompletions(ctx context.Context, a Actor, householdID, choreID string, limit int) ([]Completion, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	var out []Completion
	err := s.read(ctx, a, householdID, func(t *tx, _ Role) error {
		var n int
		if err := t.queryRow(ctx, "SELECT count(*) FROM chores WHERE id = ? AND household_id = ?", choreID, householdID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		rows, err := t.query(ctx, `SELECT id, COALESCE(user_id, ''), completed_at, source, COALESCE(token_id, ''), note, reverted_at
			FROM completions WHERE chore_id = ? ORDER BY completed_at DESC, id DESC LIMIT ?`, choreID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c := Completion{ChoreID: choreID}
			var at int64
			var rev sql.NullInt64
			if err := rows.Scan(&c.ID, &c.UserID, &at, &c.Source, &c.TokenID, &c.Note, &rev); err != nil {
				return err
			}
			c.CompletedAt, c.RevertedAt = time.UnixMilli(at).UTC(), fromMS(rev)
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}

// RevertCompletion marks a completion reverted (kept for the audit trail).
// Member or owner. If it is the chore's latest active completion, the chore's
// previous due date is restored (and a one-off chore is un-archived).
// ErrConflict if already reverted.
func (s *Store) RevertCompletion(ctx context.Context, a Actor, householdID, completionID string) error {
	return s.write(ctx, a, householdID, RoleMember, func(t *tx, _ Role) error {
		return t.revertCompletion(ctx, householdID, completionID, s.nowMS())
	})
}

// revertCompletion is RevertCompletion's body inside t, after authorization.
func (t *tx) revertCompletion(ctx context.Context, householdID, completionID string, now int64) error {
	var choreID, prevDueDate, kind string
	var prevNext, reverted sql.NullInt64
	err := t.queryRow(ctx, `SELECT c.chore_id, c.prev_next_due, c.prev_due_date, c.reverted_at, ch.kind
		FROM completions c JOIN chores ch ON ch.id = c.chore_id
		WHERE c.id = ? AND ch.household_id = ?`, completionID, householdID).Scan(&choreID, &prevNext, &prevDueDate, &reverted, &kind)
	if err != nil {
		return notFound(err)
	}
	if reverted.Valid {
		return ErrConflict
	}
	var latest string
	if err := t.queryRow(ctx, `SELECT id FROM completions WHERE chore_id = ? AND reverted_at IS NULL
		ORDER BY created_at DESC, id DESC LIMIT 1`, choreID).Scan(&latest); err != nil {
		return err
	}
	if err := t.execOne(ctx, "UPDATE completions SET reverted_at = ? WHERE id = ?", now, completionID); err != nil {
		return err
	}
	if latest != completionID {
		return nil
	}
	if kind == KindOneOff {
		return t.execOne(ctx, "UPDATE chores SET archived_at = NULL, updated_at = ? WHERE id = ? AND household_id = ?",
			now, choreID, householdID)
	}
	return t.execOne(ctx, "UPDATE chores SET next_due = ?, due_date = ?, updated_at = ? WHERE id = ? AND household_id = ?",
		prevNext, prevDueDate, now, choreID, householdID)
}
