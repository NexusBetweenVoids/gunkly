package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/db"
	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/schedule"
)

// Every method here changes what a mark-done link would complete (the
// occurrence or whether the chore is active), so each one invalidates the
// chore's outstanding links in the same transaction.

// ErrNoNextOccurrence is returned by SkipChore when the series has ended.
var ErrNoNextOccurrence = errors.New("store: no next occurrence")

// SnoozeChore moves the chore's current occurrence to the local date until,
// which must not be before today in the household zone (ErrInvalid). Member
// or owner. Archived chores are ErrConflict. The read, the computation and the
// write happen in one transaction (the chore row is locked on Postgres), so a
// concurrent edit is never overwritten.
func (s *Store) SnoozeChore(ctx context.Context, a Actor, householdID, choreID string, until schedule.Date) (Chore, error) {
	return s.moveOccurrence(ctx, a, householdID, choreID, EventChoreSnoozed, func(_ Chore, _ *time.Location, today schedule.Date) (schedule.Date, error) {
		if until.Before(today) || until.Year > 9000 {
			return schedule.Date{}, ErrInvalid
		}
		return until, nil
	})
}

// SkipChore advances a recurring chore to its next occurrence after today (and
// after the current one) without recording a completion. One-off chores are
// ErrInvalid, an ended series ErrNoNextOccurrence. Member or owner; archived
// chores are ErrConflict. Transactional like SnoozeChore.
func (s *Store) SkipChore(ctx context.Context, a Actor, householdID, choreID string) (Chore, error) {
	return s.moveOccurrence(ctx, a, householdID, choreID, EventChoreSkipped, func(c Chore, _ *time.Location, today schedule.Date) (schedule.Date, error) {
		rule, err := RuleOf(c)
		if err != nil {
			return schedule.Date{}, err
		}
		cur, perr := schedule.ParseDate(c.DueDate)
		if rule == nil || perr != nil {
			return schedule.Date{}, ErrInvalid // one-off chores do not repeat
		}
		next, found := schedule.NextDue(rule, schedule.FromScheduled, cur, today)
		if !found {
			return schedule.Date{}, ErrNoNextOccurrence
		}
		return next, nil
	})
}

func (s *Store) moveOccurrence(ctx context.Context, a Actor, householdID, choreID, event string,
	pick func(c Chore, loc *time.Location, today schedule.Date) (schedule.Date, error)) (Chore, error) {
	var out Chore
	err := s.write(ctx, a, householdID, RoleMember, func(t *tx, _ Role) error {
		if err := t.lockChore(ctx, householdID, choreID); err != nil {
			return err
		}
		c, err := t.chore(ctx, householdID, choreID)
		if err != nil {
			return err
		}
		if !c.ArchivedAt.IsZero() {
			return ErrConflict
		}
		var tz string
		if err := t.queryRow(ctx, "SELECT timezone FROM households WHERE id = ?", householdID).Scan(&tz); err != nil {
			return notFound(err)
		}
		loc, err := time.LoadLocation(tz)
		if err != nil {
			return err
		}
		d, err := pick(c, loc, schedule.DateOf(s.now(), loc))
		if err != nil {
			return err
		}
		if err := t.execOne(ctx, "UPDATE chores SET due_date = ?, next_due = ?, updated_at = ? WHERE id = ? AND household_id = ?",
			d.String(), ms(OccurrenceInstant(d, loc)), s.nowMS(), choreID, householdID); err != nil {
			return err
		}
		if err := t.finishState(ctx, householdID, choreID, &out); err != nil {
			return err
		}
		return t.emitWebhook(ctx, householdID, event, webhookEvent{choreID: choreID, chore: &out, actorID: a.UserID}, s.nowMS())
	})
	return out, err
}

// SetChorePause pauses a chore until the given instant, or indefinitely
// (until zero), or resumes it (until zero and !indefinitely). Member or owner.
// Archived chores are ErrConflict.
func (s *Store) SetChorePause(ctx context.Context, a Actor, householdID, choreID string, until time.Time, indefinitely bool) (Chore, error) {
	if indefinitely && !until.IsZero() {
		return Chore{}, ErrInvalid
	}
	var c Chore
	err := s.write(ctx, a, householdID, RoleMember, func(t *tx, _ Role) error {
		if err := t.requireActive(ctx, householdID, choreID); err != nil {
			return err
		}
		if err := t.execOne(ctx, "UPDATE chores SET paused_until = ?, paused_indefinitely = ?, updated_at = ? WHERE id = ? AND household_id = ?",
			ms(until), indefinitely, s.nowMS(), choreID, householdID); err != nil {
			return err
		}
		return t.finishState(ctx, householdID, choreID, &c)
	})
	return c, err
}

// UnarchiveChore makes an archived chore active again. Member or owner.
func (s *Store) UnarchiveChore(ctx context.Context, a Actor, householdID, choreID string) (Chore, error) {
	var c Chore
	err := s.write(ctx, a, householdID, RoleMember, func(t *tx, _ Role) error {
		if err := t.execOne(ctx, "UPDATE chores SET archived_at = NULL, updated_at = ? WHERE id = ? AND household_id = ?",
			s.nowMS(), choreID, householdID); err != nil {
			return err
		}
		return t.finishState(ctx, householdID, choreID, &c)
	})
	return c, err
}

func (t *tx) requireActive(ctx context.Context, householdID, choreID string) error {
	var archived sql.NullInt64
	if err := t.queryRow(ctx, "SELECT archived_at FROM chores WHERE id = ? AND household_id = ?", choreID, householdID).Scan(&archived); err != nil {
		return notFound(err)
	}
	if archived.Valid {
		return ErrConflict
	}
	return nil
}

func (t *tx) finishState(ctx context.Context, householdID, choreID string, out *Chore) error {
	if _, err := t.exec(ctx, "DELETE FROM mark_done_tokens WHERE chore_id = ?", choreID); err != nil {
		return err
	}
	var err error
	*out, err = t.chore(ctx, householdID, choreID)
	return err
}

// lockChore takes the chore's row lock on Postgres (SQLite write transactions
// are already serialised), so concurrent changes to its schedule run one after
// the other. ErrNotFound if the chore is not in the household.
func (t *tx) lockChore(ctx context.Context, householdID, choreID string) error {
	if t.d != db.Postgres {
		return nil
	}
	var id string
	return notFound(t.queryRow(ctx, "SELECT id FROM chores WHERE id = ? AND household_id = ? FOR UPDATE", choreID, householdID).Scan(&id))
}

// CompleteChoreAuto completes a chore now, recorded with the given source (SourceApp, SourceAPIToken or SourceOAuth; tokenID names the token or grant for the latter two), and computes the next
// occurrence inside the transaction with internal/schedule, under the chore's
// row lock (so concurrent completions cannot work from a stale due date). Member or owner. Archived
// chores are ErrConflict. It returns the completion and the updated chore.
func (s *Store) CompleteChoreAuto(ctx context.Context, a Actor, householdID, choreID, note, source, tokenID string) (Completion, Chore, error) {
	if len(note) > 2000 || source != SourceApp && source != SourceAPIToken && source != SourceOAuth {
		return Completion{}, Chore{}, ErrInvalid
	}
	var comp Completion
	var ch Chore
	err := s.write(ctx, a, householdID, RoleMember, func(t *tx, _ Role) error {
		if err := t.lockChore(ctx, householdID, choreID); err != nil {
			return err
		}
		c, err := t.chore(ctx, householdID, choreID)
		if err != nil {
			return err
		}
		var tz string
		if err := t.queryRow(ctx, "SELECT timezone FROM households WHERE id = ?", householdID).Scan(&tz); err != nil {
			return notFound(err)
		}
		loc, err := time.LoadLocation(tz)
		if err != nil {
			return err
		}
		now := s.now()
		in := CompletionInput{CompletedAt: now, Source: source, TokenID: tokenID, Note: note, DueDate: c.DueDate}
		if c.Kind != KindOneOff {
			due, next, ok, err := NextOccurrence(c, loc, now)
			if err != nil {
				return err
			}
			if ok {
				in.DueDate, in.NextDue = due.String(), next
			}
		}
		if comp, err = t.complete(ctx, householdID, choreID, a.UserID, in, now.UnixMilli()); err != nil {
			return err
		}
		ch, err = t.chore(ctx, householdID, choreID)
		return err
	})
	if err != nil {
		return Completion{}, Chore{}, err
	}
	return comp, ch, nil
}

// RevertChoreCompletion reverts one of the chore's completions (see
// RevertCompletion) and returns the chore as it is afterwards. A completion
// of another chore is ErrNotFound. Member or owner.
func (s *Store) RevertChoreCompletion(ctx context.Context, a Actor, householdID, choreID, completionID string) (Chore, error) {
	var ch Chore
	err := s.write(ctx, a, householdID, RoleMember, func(t *tx, _ Role) error {
		var n int
		if err := t.queryRow(ctx, `SELECT count(*) FROM completions c JOIN chores ch ON ch.id = c.chore_id
			WHERE c.id = ? AND c.chore_id = ? AND ch.household_id = ?`, completionID, choreID, householdID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		if err := t.revertCompletion(ctx, householdID, completionID, a.UserID, s.nowMS()); err != nil {
			return err
		}
		var err error
		ch, err = t.chore(ctx, householdID, choreID)
		return err
	})
	return ch, err
}

// CompletionChore returns the ID of the chore a completion belongs to, within
// the household (ErrNotFound otherwise). Any member, guests included.
func (s *Store) CompletionChore(ctx context.Context, a Actor, householdID, completionID string) (string, error) {
	var id string
	err := s.read(ctx, a, householdID, func(t *tx, _ Role) error {
		return notFound(t.queryRow(ctx, `SELECT c.chore_id FROM completions c JOIN chores ch ON ch.id = c.chore_id
			WHERE c.id = ? AND ch.household_id = ?`, completionID, householdID).Scan(&id))
	})
	return id, err
}

// CompletionsPage returns a chore's history, newest first, at most limit
// entries (1..200, default 50) after the cursor (the last entry of the
// previous page; zero = first page). next is the cursor of the following page,
// empty on the last one.
func (s *Store) CompletionsPage(ctx context.Context, a Actor, householdID, choreID string, limit int, after string) (items []Completion, next string, err error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var atMS int64 = 1<<62 - 1
	afterID := "\U0010ffff"
	if after != "" {
		if atMS, afterID, err = parseCursor(after); err != nil {
			return nil, "", err
		}
	}
	err = s.read(ctx, a, householdID, func(t *tx, _ Role) error {
		var n int
		if err := t.queryRow(ctx, "SELECT count(*) FROM chores WHERE id = ? AND household_id = ?", choreID, householdID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		rows, err := t.query(ctx, `SELECT id, COALESCE(user_id, ''), completed_at, source, COALESCE(token_id, ''), note, reverted_at,
			COALESCE((SELECT oc.name FROM oauth_grants og JOIN oauth_clients oc ON oc.id = og.client_id
				WHERE og.id = completions.token_id AND completions.source = 'oauth'), '')
			FROM completions WHERE chore_id = ? AND (completed_at < ? OR (completed_at = ? AND id < ?))
			ORDER BY completed_at DESC, id DESC LIMIT ?`, choreID, atMS, atMS, afterID, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c := Completion{ChoreID: choreID}
			var at int64
			var rev sql.NullInt64
			if err := rows.Scan(&c.ID, &c.UserID, &at, &c.Source, &c.TokenID, &c.Note, &rev, &c.Via); err != nil {
				return err
			}
			c.CompletedAt, c.RevertedAt = time.UnixMilli(at).UTC(), fromMS(rev)
			items = append(items, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", err
	}
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		next = strconv.FormatInt(last.CompletedAt.UnixMilli(), 10) + "_" + last.ID
	}
	return items, next, nil
}

func parseCursor(s string) (int64, string, error) {
	for i := 0; i < len(s); i++ {
		if s[i] == '_' {
			at, err := strconv.ParseInt(s[:i], 10, 64)
			if err != nil || at < 0 || i+1 == len(s) || len(s) > 100 {
				return 0, "", ErrInvalid
			}
			return at, s[i+1:], nil
		}
	}
	return 0, "", ErrInvalid
}

// ChoreReminders returns the chore's reminder preset ("" = household
// default, "custom" = the reminders returned) and its custom reminders.
func (s *Store) ChoreReminders(ctx context.Context, a Actor, householdID, choreID string) (string, []schedule.Reminder, error) {
	var preset string
	var out []schedule.Reminder
	err := s.read(ctx, a, householdID, func(t *tx, _ Role) error {
		if err := t.queryRow(ctx, "SELECT reminder_preset FROM chores WHERE id = ? AND household_id = ?", choreID, householdID).Scan(&preset); err != nil {
			return notFound(err)
		}
		rows, err := t.query(ctx, "SELECT kind, days_before, time_of_day FROM chore_reminders WHERE chore_id = ?", choreID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var kind string
			r := schedule.Reminder{}
			if err := rows.Scan(&kind, &r.Days, &r.Minute); err != nil {
				return err
			}
			switch kind {
			case "before":
				r.Kind = schedule.Before
			case "on":
				r.Kind, r.Days = schedule.OnDue, 0
			default:
				r.Kind, r.Days = schedule.Overdue, max(1, r.Days)
			}
			out = append(out, r)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		// Stable, meaningful order: before (furthest first), on due, overdue.
		slices.SortFunc(out, func(a, b schedule.Reminder) int {
			if a.Kind != b.Kind {
				return int(a.Kind) - int(b.Kind)
			}
			if a.Days != b.Days {
				if a.Kind == schedule.Before {
					return b.Days - a.Days
				}
				return a.Days - b.Days
			}
			return a.Minute - b.Minute
		})
		return nil
	})
	return preset, out, err
}

// SetChoreReminders replaces the chore's reminder setup: a preset name ("" =
// household default), or "custom" with its own reminders (at most maxReminders,
// from internal/limits; ErrLimit beyond). Member or owner. Preset names other
// than custom carry no reminders.
func (s *Store) SetChoreReminders(ctx context.Context, a Actor, householdID, choreID, preset string, rs []schedule.Reminder, maxReminders int) error {
	if len(preset) > 32 || (preset != "custom" && len(rs) > 0) {
		return ErrInvalid
	}
	for _, r := range rs {
		if r.Validate() != nil {
			return ErrInvalid
		}
	}
	if len(rs) > maxReminders {
		return ErrLimit
	}
	return s.write(ctx, a, householdID, RoleMember, func(t *tx, _ Role) error {
		if err := t.execOne(ctx, "UPDATE chores SET reminder_preset = ?, updated_at = ? WHERE id = ? AND household_id = ?",
			preset, s.nowMS(), choreID, householdID); err != nil {
			return err
		}
		return t.replaceReminders(ctx, choreID, rs)
	})
}

// replaceReminders swaps the chore's reminder rows for rs.
func (t *tx) replaceReminders(ctx context.Context, choreID string, rs []schedule.Reminder) error {
	if _, err := t.exec(ctx, "DELETE FROM chore_reminders WHERE chore_id = ?", choreID); err != nil {
		return err
	}
	for _, r := range rs {
		kind := map[schedule.ReminderKind]string{schedule.Before: "before", schedule.OnDue: "on", schedule.Overdue: "overdue"}[r.Kind]
		days := r.Days
		if r.Kind == schedule.OnDue {
			days = 0
		}
		if _, err := t.exec(ctx, "INSERT INTO chore_reminders (id, chore_id, kind, days_before, time_of_day) VALUES (?, ?, ?, ?, ?)",
			ids.New(), choreID, kind, days, r.Minute); err != nil {
			return err
		}
	}
	return nil
}
