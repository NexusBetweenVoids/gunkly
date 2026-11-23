package store

import (
	"context"
	"slices"
)

// Rotating assignees ("Take turns", PRD "Chores & scheduling"). A chore with
// Rotation on has 2+ assignees whose order is the rotation order and one
// current-turn person (always an assignee). Completing the chore passes the
// turn on; skip and snooze do not.

// nextTurn is the person after from in order, wrapping around; the first
// person when from is not in the order.
func nextTurn(order []string, from string) string {
	i := slices.Index(order, from)
	if i < 0 {
		return order[0]
	}
	return order[(i+1)%len(order)]
}

// assigneeOrder lists a chore's assignees in rotation order.
func (t *tx) assigneeOrder(ctx context.Context, choreID string) ([]string, error) {
	rows, err := t.query(ctx, "SELECT user_id FROM chore_assignees WHERE chore_id = ? ORDER BY position, user_id", choreID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// advanceTurn passes the turn on after a completion by userID ("" = a
// household channel) and returns the new current-turn person. With
// AdvanceAny the successor of the completer comes next (of the person whose
// turn it was when the completer is not in the rotation); with
// AdvanceAssignee only a completion by the person whose turn it is moves on.
func (t *tx) advanceTurn(ctx context.Context, choreID, advance, turn, userID string) (string, error) {
	order, err := t.assigneeOrder(ctx, choreID)
	if err != nil || len(order) < 2 {
		return turn, err
	}
	from := turn
	if advance == AdvanceAssignee {
		if userID != turn {
			return turn, nil
		}
	} else if slices.Contains(order, userID) {
		from = userID
	}
	return nextTurn(order, from), nil
}

// SetChoreTurn makes userID, who must be an assignee, the current-turn person
// of a rotating chore (ErrInvalid otherwise; ErrConflict when the chore does
// not take turns or is archived). Member or owner. It does not touch mark-done
// links: they complete the same occurrence as before.
func (s *Store) SetChoreTurn(ctx context.Context, a Actor, householdID, choreID, userID string) (Chore, error) {
	var out Chore
	err := s.write(ctx, a, householdID, RoleMember, func(t *tx, _ Role) error {
		c, err := t.chore(ctx, householdID, choreID)
		if err != nil {
			return err
		}
		if !c.ArchivedAt.IsZero() || !c.Rotation {
			return ErrConflict
		}
		if !slices.Contains(c.Assignees, userID) {
			return ErrInvalid
		}
		if err := t.execOne(ctx, "UPDATE chores SET turn_user_id = ?, updated_at = ? WHERE id = ? AND household_id = ?",
			userID, s.nowMS(), choreID, householdID); err != nil {
			return err
		}
		if out, err = t.chore(ctx, householdID, choreID); err != nil {
			return err
		}
		return t.emitWebhook(ctx, householdID, EventChoreUpdated, webhookEvent{choreID: choreID, chore: &out, actorID: a.UserID}, s.nowMS())
	})
	return out, err
}

// leaveRotations takes userID out of every rotating chore of the household
// (or of every household when householdID is ""): the assignee row goes, the
// turn moves on to the next person when it was theirs, and a rotation left
// with fewer than two people is switched off (the remaining person stays the
// plain assignee). Called when a member leaves or becomes a guest.
func (t *tx) leaveRotations(ctx context.Context, householdID, userID string) error {
	q := `SELECT c.id, COALESCE(c.turn_user_id, '') FROM chores c JOIN chore_assignees ca ON ca.chore_id = c.id
		WHERE c.rotation = TRUE AND ca.user_id = ?`
	args := []any{userID}
	if householdID != "" {
		q += " AND c.household_id = ?"
		args = append(args, householdID)
	}
	type rot struct{ id, turn string }
	var rots []rot
	err := func() error {
		rows, err := t.query(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r rot
			if err := rows.Scan(&r.id, &r.turn); err != nil {
				return err
			}
			rots = append(rots, r)
		}
		return rows.Err()
	}()
	if err != nil {
		return err
	}
	for _, r := range rots {
		order, err := t.assigneeOrder(ctx, r.id)
		if err != nil {
			return err
		}
		turn := r.turn
		if turn == userID {
			turn = nextTurn(order, userID)
		}
		rest := slices.DeleteFunc(slices.Clone(order), func(u string) bool { return u == userID })
		if _, err := t.exec(ctx, "DELETE FROM chore_assignees WHERE chore_id = ? AND user_id = ?", r.id, userID); err != nil {
			return err
		}
		if len(rest) < 2 {
			_, err = t.exec(ctx, "UPDATE chores SET rotation = FALSE, turn_user_id = NULL WHERE id = ?", r.id)
		} else {
			_, err = t.exec(ctx, "UPDATE chores SET turn_user_id = ? WHERE id = ?", turn, r.id)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
