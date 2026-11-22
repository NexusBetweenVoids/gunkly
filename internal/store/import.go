package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/schedule"
	"github.com/NexusBetweenVoids/gunkly/internal/transfer"
)

// ImportResult summarises an import.
type ImportResult struct {
	HouseholdID         string
	HouseholdName       string
	AreasCreated        int
	AreasMerged         int // file areas that matched an existing area by name
	ChoresCreated       int
	CompletionsImported int
	Unassigned          int // chores whose assignees were all unknown here, now "anyone"
}

// ImportLimitError says which limit an import would exceed. It matches ErrLimit.
type ImportLimitError struct{ Reason string } // a transfer.ReasonLimit* code

func (e *ImportLimitError) Error() string { return "store: import exceeds limit (" + e.Reason + ")" }

func (e *ImportLimitError) Unwrap() error { return ErrLimit }

// ImportNewHousehold creates a household from a validated file with the actor
// as its only member and owner, in one transaction: any failure leaves
// nothing behind. maxHouseholds is the user's HouseholdsPerUser limit (0 =
// unchecked), enforced like CreateHousehold.
func (s *Store) ImportNewHousehold(ctx context.Context, a Actor, f *transfer.Household, maxHouseholds int) (ImportResult, error) {
	var res ImportResult
	if a.UserID == "" || f == nil {
		return res, ErrInvalid
	}
	err := s.inTx(ctx, true, func(t *tx) error {
		if err := t.lockUser(ctx, a.UserID); err != nil {
			return err
		}
		if maxHouseholds > 0 {
			var n int
			if err := t.queryRow(ctx, "SELECT count(*) FROM memberships WHERE user_id = ?", a.UserID).Scan(&n); err != nil {
				return err
			}
			if n >= maxHouseholds {
				return ErrLimit
			}
		}
		h, now := f.Household, s.nowMS()
		hhID := ids.New()
		var qs, qe any
		if h.QuietHours != nil {
			qs, qe = h.QuietHours.Start, h.QuietHours.End
		}
		if _, err := t.exec(ctx, `INSERT INTO households (id, name, timezone, quiet_start_min, quiet_end_min, default_reminder_preset, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, hhID, h.Name, h.Timezone, qs, qe, h.DefaultReminderPreset, now); err != nil {
			return err
		}
		if _, err := t.exec(ctx, "INSERT INTO memberships (household_id, user_id, role, joined_at) VALUES (?, ?, ?, ?)",
			hhID, a.UserID, RoleOwner, now); err != nil {
			return notFoundFK(err)
		}
		var err error
		res, err = t.importInto(ctx, hhID, a.UserID, true, f, now)
		res.HouseholdName = h.Name
		return err
	})
	if err != nil {
		return ImportResult{}, err
	}
	return res, nil
}

// MergeHousehold merges a validated file into an existing household. Member
// or owner. Household settings in the file are ignored. One transaction; on
// Postgres the household row is locked first, so concurrent imports cannot
// each pass the chore-limit check and together exceed it. Completions are
// attributed to members named in the file only when the importer is an owner
// (see importChore).
func (s *Store) MergeHousehold(ctx context.Context, a Actor, householdID string, f *transfer.Household) (ImportResult, error) {
	var res ImportResult
	if f == nil {
		return res, ErrInvalid
	}
	err := s.manage(ctx, a, householdID, RoleMember, func(t *tx, role Role) error {
		var err error
		res, err = t.importInto(ctx, householdID, a.UserID, role == RoleOwner, f, s.nowMS())
		if err != nil {
			return err
		}
		return notFound(t.queryRow(ctx, "SELECT name FROM households WHERE id = ?", householdID).Scan(&res.HouseholdName))
	})
	if err != nil {
		return ImportResult{}, err
	}
	return res, nil
}

// each runs a query and calls fn for every row; the rows are closed before
// it returns, so the transaction can run other statements afterwards.
func (t *tx) each(ctx context.Context, fn func(*sql.Rows) error, q string, args ...any) error {
	rows, err := t.query(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

type importMember struct {
	id   string
	role Role
}

// importInto adds the file's areas, chores and history to a household whose
// membership already includes actorID. attribute says whether completions may
// be attributed to the members the file names (importer is an owner).
func (t *tx) importInto(ctx context.Context, hhID, actorID string, attribute bool, f *transfer.Household, now int64) (ImportResult, error) {
	res := ImportResult{HouseholdID: hhID}
	var tz string
	if err := t.queryRow(ctx, "SELECT timezone FROM households WHERE id = ?", hhID).Scan(&tz); err != nil {
		return res, notFound(err)
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return res, err
	}
	maxAreas, err := t.effectiveLimit(ctx, limits.AreasPerHousehold, hhID)
	if err != nil {
		return res, err
	}
	maxChores, err := t.effectiveLimit(ctx, limits.ChoresPerHousehold, hhID)
	if err != nil {
		return res, err
	}
	maxRem, err := t.effectiveLimit(ctx, limits.RemindersPerChore, hhID)
	if err != nil {
		return res, err
	}
	maxComp, err := t.effectiveLimit(ctx, limits.CompletionsPerChore, hhID)
	if err != nil {
		return res, err
	}

	// Areas: same normalised name = same area.
	areaIDs := map[string]string{} // lower-case name -> id
	if err := t.each(ctx, func(r *sql.Rows) error {
		var lower, id string
		if err := r.Scan(&lower, &id); err != nil {
			return err
		}
		areaIDs[lower] = id
		return nil
	}, "SELECT name_lower, id FROM areas WHERE household_id = ?", hhID); err != nil {
		return res, err
	}
	existingAreas := len(areaIDs)
	for _, a := range f.Areas {
		name, lower, ok := cleanName(a.Name)
		if !ok {
			return res, ErrInvalid
		}
		if _, found := areaIDs[lower]; found {
			res.AreasMerged++
			continue
		}
		if existingAreas+res.AreasCreated >= maxAreas {
			return res, &ImportLimitError{Reason: transfer.ReasonLimitArea}
		}
		id := ids.New()
		if _, err := t.exec(ctx, "INSERT INTO areas (id, household_id, name, name_lower, created_at) VALUES (?, ?, ?, ?, ?)",
			id, hhID, name, lower, now); err != nil {
			return res, conflict(err)
		}
		areaIDs[lower] = id
		res.AreasCreated++
	}

	var existingChores int
	if err := t.queryRow(ctx, "SELECT count(*) FROM chores WHERE household_id = ?", hhID).Scan(&existingChores); err != nil {
		return res, err
	}
	if existingChores+len(f.Chores) > maxChores {
		return res, &ImportLimitError{Reason: transfer.ReasonLimitChore}
	}

	// Members by username, for assignees and completers.
	members := map[string]importMember{}
	if err := t.each(ctx, func(r *sql.Rows) error {
		var name string
		var m importMember
		if err := r.Scan(&name, &m.id, &m.role); err != nil {
			return err
		}
		members[name] = m
		return nil
	}, `SELECT u.username_lower, u.id, m.role FROM memberships m JOIN users u ON u.id = m.user_id
		WHERE m.household_id = ?`, hhID); err != nil {
		return res, err
	}

	for i := range f.Chores {
		c := &f.Chores[i]
		if len(c.Reminders) > maxRem {
			return res, &ImportLimitError{Reason: transfer.ReasonLimitRem}
		}
		if len(c.Completions) > maxComp {
			return res, &ImportLimitError{Reason: transfer.ReasonLimitComp}
		}
		n, unassigned, err := t.importChore(ctx, hhID, actorID, attribute, c, areaIDs, members, loc, now)
		if err != nil {
			return res, err
		}
		res.ChoresCreated++
		res.CompletionsImported += n
		if unassigned {
			res.Unassigned++
		}
	}
	return res, nil
}

// importedOccurrence picks the chore's current occurrence: the file's due
// date (recomputed from the schedule when the file has none) at the
// household's local midnight. The file's nextDue instant is kept as-is only
// in the one case it carries information the date does not: a recurring
// series that has ended (null); otherwise it is recomputed in the target
// zone, which equals the file's value when the zones match.
func importedOccurrence(c *transfer.Chore, loc *time.Location) (due string, next time.Time, err error) {
	first, err := schedule.ParseDate(c.Schedule.FirstDue)
	if err != nil {
		return "", time.Time{}, ErrInvalid
	}
	d := first
	switch {
	case c.DueDate != "":
		if d, err = schedule.ParseDate(c.DueDate); err != nil {
			return "", time.Time{}, ErrInvalid
		}
	case c.Kind == KindRecurring:
		rule, err := RuleOf(Chore{Kind: c.Kind, ScheduleType: c.Schedule.Type, IntervalN: intervalOf(c), IntervalUnit: unitOf(c),
			RRule: c.Schedule.RRule, FirstDue: c.Schedule.FirstDue})
		if err != nil {
			return "", time.Time{}, ErrInvalid
		}
		var ok bool
		if d, ok = rule.Next(first, first.AddDays(-1)); !ok {
			return "", time.Time{}, ErrInvalid
		}
	}
	if c.NextDue == nil && c.Kind == KindRecurring && c.DueDate != "" {
		return d.String(), time.Time{}, nil // ended series
	}
	return d.String(), OccurrenceInstant(d, loc), nil
}

func intervalOf(c *transfer.Chore) int {
	if c.Schedule.Interval == nil {
		return 0
	}
	return c.Schedule.Interval.Every
}

func unitOf(c *transfer.Chore) string {
	if c.Schedule.Interval == nil {
		return ""
	}
	return c.Schedule.Interval.Unit
}

func (t *tx) importChore(ctx context.Context, hhID, actorID string, attribute bool, c *transfer.Chore, areaIDs map[string]string,
	members map[string]importMember, loc *time.Location, now int64) (completions int, unassigned bool, err error) {
	due, next, err := importedOccurrence(c, loc)
	if err != nil {
		return 0, false, err
	}
	in := ChoreInput{Name: c.Name, Notes: c.Notes, Link: c.Link, Kind: c.Kind, ScheduleType: c.Schedule.Type,
		IntervalN: intervalOf(c), IntervalUnit: unitOf(c), RRule: c.Schedule.RRule, Anchor: c.Schedule.Anchor,
		FirstDue: c.Schedule.FirstDue, DueDate: due, NextDue: next, PausedIndefinitely: c.PausedIndefinitely,
		ReminderPreset: c.ReminderPreset}
	if c.PausedUntil != nil {
		in.PausedUntil = *c.PausedUntil
	}
	if c.Area != "" {
		in.AreaID = areaIDs[strings.ToLower(c.Area)]
		if in.AreaID == "" {
			return 0, false, ErrInvalid
		}
	}
	if !in.validate() {
		return 0, false, ErrInvalid
	}
	id := ids.New()
	if err := t.insertChore(ctx, id, hhID, actorID, in, now); err != nil {
		return 0, false, err
	}
	if c.ArchivedAt != nil {
		if _, err := t.exec(ctx, "UPDATE chores SET archived_at = ? WHERE id = ?", c.ArchivedAt.UnixMilli(), id); err != nil {
			return 0, false, err
		}
	}
	// Assignees: matched by username among non-guest members; others dropped.
	seen := map[string]bool{}
	for _, p := range c.Assignees {
		m, found := members[userMatch(p.Username)]
		if !found || !m.role.AtLeast(RoleMember) || seen[m.id] {
			continue
		}
		seen[m.id] = true
		if _, err := t.exec(ctx, "INSERT INTO chore_assignees (chore_id, user_id) VALUES (?, ?)", id, m.id); err != nil {
			return 0, false, err
		}
	}
	unassigned = len(c.Assignees) > 0 && len(seen) == 0
	for _, r := range c.Reminders {
		days := r.Days
		if r.Kind == "on" {
			days = 0
		}
		if _, err := t.exec(ctx, "INSERT INTO chore_reminders (id, chore_id, kind, days_before, time_of_day) VALUES (?, ?, ?, ?, ?)",
			ids.New(), id, r.Kind, days, r.Minute); err != nil {
			return 0, false, err
		}
	}
	for _, h := range c.Completions {
		// Attribution: only an owner's import may name who did it, and the
		// username must match a member of the target household; otherwise
		// the completion is anonymous (NULL user). A member's file could
		// otherwise forge history in another member's name.
		var by any
		if attribute && h.By != nil {
			if m, found := members[userMatch(h.By.Username)]; found {
				by = m.id
			}
		}
		at := h.CompletedAt.UTC().Truncate(time.Millisecond).UnixMilli()
		created := min(at, now) // never sorts after a real completion made now
		var reverted any
		if h.Reverted {
			reverted = now
		}
		// Undo state: the chore as imported, so reverting an imported
		// completion never leaves the chore without a due date.
		if _, err := t.exec(ctx, `INSERT INTO completions (id, chore_id, user_id, completed_at, source, note, reverted_at,
			prev_next_due, prev_due_date, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			ids.New(), id, by, at, SourceImport, h.Note, reverted, ms(next), due, created); err != nil {
			return 0, false, err
		}
	}
	return len(c.Completions), unassigned, nil
}
