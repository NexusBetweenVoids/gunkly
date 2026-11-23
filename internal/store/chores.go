package store

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/schedule"
)

// Chore kinds, schedule types, anchors and interval units.
const (
	KindRecurring = "recurring"
	KindOneOff    = "oneoff"

	ScheduleInterval = "interval"
	ScheduleRRule    = "rrule"

	AnchorFromCompletion = "from_completion"
	AnchorFromScheduled  = "from_scheduled"
)

// Chore is a household chore. Scheduling math lives in internal/schedule;
// the store only persists the computed NextDue/DueDate.
type Chore struct {
	ID                 string
	HouseholdID        string
	AreaID             string // "" = none
	Name               string
	Notes              string
	Link               string
	Kind               string
	ScheduleType       string
	IntervalN          int
	IntervalUnit       string // day | week | month
	RRule              string
	Anchor             string
	FirstDue           string    // local date YYYY-MM-DD
	DueDate            string    // local date of the current occurrence
	NextDue            time.Time // UTC instant of the current occurrence; zero = none
	PausedUntil        time.Time
	PausedIndefinitely bool
	ArchivedAt         time.Time
	ReminderPreset     string
	Assignees          []string // user IDs; empty = anyone
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// ChoreInput holds the editable fields for CreateChore and UpdateChore.
type ChoreInput struct {
	AreaID             string
	Name               string
	Notes              string
	Link               string
	Kind               string
	ScheduleType       string
	IntervalN          int
	IntervalUnit       string
	RRule              string
	Anchor             string
	FirstDue           string
	DueDate            string
	NextDue            time.Time
	PausedUntil        time.Time
	PausedIndefinitely bool
	ReminderPreset     string
	// Reminders, when non-nil, replaces the chore's reminder rows in the same
	// transaction; ReminderPreset must then be "custom".
	Reminders []schedule.Reminder
	Assignees []string
	// MaxChores is the household's ChoresPerHousehold limit, resolved by the
	// caller from internal/limits; CreateChore returns ErrLimit when the
	// household already has that many (archived ones included). 0 = no cap.
	MaxChores int
}

func (in *ChoreInput) validate() bool {
	if in.Reminders != nil {
		if in.ReminderPreset != "custom" {
			return false
		}
		for _, r := range in.Reminders {
			if r.Validate() != nil {
				return false
			}
		}
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || !utf8.ValidString(in.Name+in.Notes) || utf8.RuneCountInString(in.Name) > 200 ||
		utf8.RuneCountInString(in.Notes) > 10000 || len(in.Link) > 2000 {
		return false
	}
	if in.Link != "" {
		u, err := url.Parse(in.Link)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return false
		}
	}
	if in.Kind != KindRecurring && in.Kind != KindOneOff {
		return false
	}
	switch in.ScheduleType {
	case ScheduleInterval:
		if in.Kind == KindRecurring && (in.IntervalN < 1 || in.IntervalN > schedule.MaxInterval || (in.IntervalUnit != "day" && in.IntervalUnit != "week" && in.IntervalUnit != "month")) {
			return false
		}
	case ScheduleRRule:
		if in.Kind == KindRecurring {
			if _, err := schedule.ParseRRule(in.RRule); err != nil {
				return false
			}
		}
	default:
		return false
	}
	if in.Anchor != AnchorFromCompletion && in.Anchor != AnchorFromScheduled {
		return false
	}
	if _, err := time.Parse(time.DateOnly, in.FirstDue); err != nil {
		return false
	}
	if _, err := time.Parse(time.DateOnly, in.DueDate); err != nil {
		return false
	}
	return true
}

// checkRefs verifies the area and assignees belong to the household. Assignees
// must be non-guest members.
func (t *tx) checkRefs(ctx context.Context, householdID string, in *ChoreInput) error {
	if in.AreaID != "" {
		var n int
		if err := t.queryRow(ctx, "SELECT count(*) FROM areas WHERE id = ? AND household_id = ?", in.AreaID, householdID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrInvalid
		}
	}
	seen := map[string]bool{}
	for _, u := range in.Assignees {
		if seen[u] {
			return ErrInvalid
		}
		seen[u] = true
		var r Role
		err := t.queryRow(ctx, "SELECT role FROM memberships WHERE household_id = ? AND user_id = ?", householdID, u).Scan(&r)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !r.AtLeast(RoleMember)) {
			return ErrInvalid
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (t *tx) setAssignees(ctx context.Context, choreID string, users []string) error {
	if _, err := t.exec(ctx, "DELETE FROM chore_assignees WHERE chore_id = ?", choreID); err != nil {
		return err
	}
	for _, u := range users {
		if _, err := t.exec(ctx, "INSERT INTO chore_assignees (chore_id, user_id) VALUES (?, ?)", choreID, u); err != nil {
			return err
		}
	}
	return nil
}

// CreateChore adds a chore. Member or owner.
func (s *Store) CreateChore(ctx context.Context, a Actor, householdID string, in ChoreInput) (Chore, error) {
	if !in.validate() {
		return Chore{}, ErrInvalid
	}
	id := ids.New()
	var c Chore
	err := s.write(ctx, a, householdID, RoleMember, func(t *tx, _ Role) error {
		if err := t.checkRefs(ctx, householdID, &in); err != nil {
			return err
		}
		if in.MaxChores > 0 {
			var n int
			if err := t.queryRow(ctx, "SELECT count(*) FROM chores WHERE household_id = ?", householdID).Scan(&n); err != nil {
				return err
			}
			if n >= in.MaxChores {
				return ErrLimit
			}
		}
		if err := t.insertChore(ctx, id, householdID, a.UserID, in, s.nowMS()); err != nil {
			return err
		}
		if err := t.setAssignees(ctx, id, in.Assignees); err != nil {
			return err
		}
		if in.Reminders != nil {
			if err := t.replaceReminders(ctx, id, in.Reminders); err != nil {
				return err
			}
		}
		var err error
		if c, err = t.chore(ctx, householdID, id); err != nil {
			return err
		}
		return t.emitWebhook(ctx, householdID, EventChoreCreated, webhookEvent{choreID: id, chore: &c, actorID: a.UserID}, s.nowMS())
	})
	return c, err
}

// insertChore writes the chore row (not its assignees).
func (t *tx) insertChore(ctx context.Context, id, householdID, createdBy string, in ChoreInput, now int64) error {
	_, err := t.exec(ctx, `INSERT INTO chores (id, household_id, area_id, name, notes, link, kind, schedule_type,
		interval_n, interval_unit, rrule, anchor, first_due, due_date, next_due, paused_until, paused_indefinitely,
		reminder_preset, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, householdID, nullStr(in.AreaID), in.Name, in.Notes, in.Link, in.Kind, in.ScheduleType,
		nullInt(in.IntervalN), nullStr(in.IntervalUnit), in.RRule, in.Anchor, in.FirstDue, in.DueDate,
		ms(in.NextDue), ms(in.PausedUntil), in.PausedIndefinitely, in.ReminderPreset, createdBy, now, now)
	return err
}

const choreCols = `id, household_id, COALESCE(area_id, ''), name, notes, link, kind, schedule_type,
	COALESCE(interval_n, 0), COALESCE(interval_unit, ''), rrule, anchor, first_due, due_date, next_due, paused_until,
	paused_indefinitely, archived_at, reminder_preset, created_at, updated_at`

func scanChore(row interface{ Scan(...any) error }) (Chore, error) {
	var c Chore
	var next, paused, archived sql.NullInt64
	var created, updated int64
	err := row.Scan(&c.ID, &c.HouseholdID, &c.AreaID, &c.Name, &c.Notes, &c.Link, &c.Kind, &c.ScheduleType,
		&c.IntervalN, &c.IntervalUnit, &c.RRule, &c.Anchor, &c.FirstDue, &c.DueDate, &next, &paused,
		&c.PausedIndefinitely, &archived, &c.ReminderPreset, &created, &updated)
	if err != nil {
		return Chore{}, notFound(err)
	}
	c.NextDue, c.PausedUntil, c.ArchivedAt = fromMS(next), fromMS(paused), fromMS(archived)
	c.CreatedAt, c.UpdatedAt = time.UnixMilli(created).UTC(), time.UnixMilli(updated).UTC()
	return c, nil
}

// chore loads one chore constrained to the household, with assignees.
func (t *tx) chore(ctx context.Context, householdID, choreID string) (Chore, error) {
	c, err := scanChore(t.queryRow(ctx, "SELECT "+choreCols+" FROM chores WHERE id = ? AND household_id = ?", choreID, householdID))
	if err != nil {
		return Chore{}, err
	}
	rows, err := t.query(ctx, "SELECT user_id FROM chore_assignees WHERE chore_id = ? ORDER BY user_id", choreID)
	if err != nil {
		return Chore{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return Chore{}, err
		}
		c.Assignees = append(c.Assignees, u)
	}
	return c, rows.Err()
}

// Chore returns one chore of the household.
func (s *Store) Chore(ctx context.Context, a Actor, householdID, choreID string) (Chore, error) {
	var c Chore
	err := s.read(ctx, a, householdID, func(t *tx, _ Role) error {
		var err error
		c, err = t.chore(ctx, householdID, choreID)
		return err
	})
	return c, err
}

// ListChores lists the household's chores by due time (archived ones only
// when includeArchived).
func (s *Store) ListChores(ctx context.Context, a Actor, householdID string, includeArchived bool) ([]Chore, error) {
	var out []Chore
	err := s.read(ctx, a, householdID, func(t *tx, _ Role) error {
		q := "SELECT " + choreCols + " FROM chores WHERE household_id = ?"
		if !includeArchived {
			q += " AND archived_at IS NULL"
		}
		idx := map[string]int{}
		err := func() error {
			rows, err := t.query(ctx, q+" ORDER BY next_due IS NULL, next_due, id", householdID)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				c, err := scanChore(rows)
				if err != nil {
					return err
				}
				idx[c.ID] = len(out)
				out = append(out, c)
			}
			return rows.Err()
		}()
		if err != nil {
			return err
		}
		rows, err := t.query(ctx, `SELECT ca.chore_id, ca.user_id FROM chore_assignees ca
			JOIN chores c ON c.id = ca.chore_id WHERE c.household_id = ? ORDER BY ca.user_id`, householdID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var cid, uid string
			if err := rows.Scan(&cid, &uid); err != nil {
				return err
			}
			if i, ok := idx[cid]; ok {
				out[i].Assignees = append(out[i].Assignees, uid)
			}
		}
		return rows.Err()
	})
	return out, err
}

// UpdateChore replaces the editable fields. Member or owner. Schedule- or
// state-affecting changes invalidate outstanding mark-done links; cosmetic
// edits (name, notes, area, link, assignees) do not.
func (s *Store) UpdateChore(ctx context.Context, a Actor, householdID, choreID string, in ChoreInput) (Chore, error) {
	if !in.validate() {
		return Chore{}, ErrInvalid
	}
	var c Chore
	err := s.write(ctx, a, householdID, RoleMember, func(t *tx, _ Role) error {
		old, err := t.chore(ctx, householdID, choreID)
		if err != nil {
			return err
		}
		if err := t.checkRefs(ctx, householdID, &in); err != nil {
			return err
		}
		if err := t.execOne(ctx, `UPDATE chores SET area_id = ?, name = ?, notes = ?, link = ?, kind = ?, schedule_type = ?,
			interval_n = ?, interval_unit = ?, rrule = ?, anchor = ?, first_due = ?, due_date = ?, next_due = ?,
			paused_until = ?, paused_indefinitely = ?, reminder_preset = ?, updated_at = ?
			WHERE id = ? AND household_id = ?`,
			nullStr(in.AreaID), in.Name, in.Notes, in.Link, in.Kind, in.ScheduleType, nullInt(in.IntervalN),
			nullStr(in.IntervalUnit), in.RRule, in.Anchor, in.FirstDue, in.DueDate, ms(in.NextDue), ms(in.PausedUntil),
			in.PausedIndefinitely, in.ReminderPreset, s.nowMS(), choreID, householdID); err != nil {
			return err
		}
		if err := t.setAssignees(ctx, choreID, in.Assignees); err != nil {
			return err
		}
		if in.Reminders != nil {
			if err := t.replaceReminders(ctx, choreID, in.Reminders); err != nil {
				return err
			}
		}
		if old.Kind != in.Kind || old.ScheduleType != in.ScheduleType || old.IntervalN != in.IntervalN ||
			old.IntervalUnit != in.IntervalUnit || old.RRule != in.RRule || old.Anchor != in.Anchor ||
			old.DueDate != in.DueDate || !old.NextDue.Equal(in.NextDue) || !old.PausedUntil.Equal(in.PausedUntil) ||
			old.PausedIndefinitely != in.PausedIndefinitely {
			if _, err := t.exec(ctx, "DELETE FROM mark_done_tokens WHERE chore_id = ?", choreID); err != nil {
				return err
			}
		}
		if c, err = t.chore(ctx, householdID, choreID); err != nil {
			return err
		}
		return t.emitWebhook(ctx, householdID, EventChoreUpdated, webhookEvent{choreID: choreID, chore: &c, actorID: a.UserID}, s.nowMS())
	})
	return c, err
}

// ArchiveChore archives a chore (keeps history, stops reminders). Member or owner.
func (s *Store) ArchiveChore(ctx context.Context, a Actor, householdID, choreID string) error {
	return s.write(ctx, a, householdID, RoleMember, func(t *tx, _ Role) error {
		now := s.nowMS()
		if err := t.execOne(ctx, "UPDATE chores SET archived_at = COALESCE(archived_at, ?), updated_at = ? WHERE id = ? AND household_id = ?",
			now, now, choreID, householdID); err != nil {
			return err
		}
		_, err := t.exec(ctx, "DELETE FROM mark_done_tokens WHERE chore_id = ?", choreID)
		return err
	})
}

// DeleteChore deletes a chore and its history. Member or owner.
func (s *Store) DeleteChore(ctx context.Context, a Actor, householdID, choreID string) error {
	return s.write(ctx, a, householdID, RoleMember, func(t *tx, _ Role) error {
		c, err := t.chore(ctx, householdID, choreID) // the event carries the chore as it was
		if err != nil {
			return err
		}
		if err := t.emitWebhook(ctx, householdID, EventChoreDeleted, webhookEvent{choreID: choreID, chore: &c, actorID: a.UserID}, s.nowMS()); err != nil {
			return err
		}
		return t.execOne(ctx, "DELETE FROM chores WHERE id = ? AND household_id = ?", choreID, householdID)
	})
}

func nullInt(n int) sql.NullInt64 { return sql.NullInt64{Int64: int64(n), Valid: n != 0} }
