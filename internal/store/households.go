package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/db"
	"github.com/NexusBetweenVoids/gunkly/internal/ids"
)

// Household as seen by a particular actor (Role is the actor's role).
type Household struct {
	ID                    string
	Name                  string
	Timezone              string
	QuietStartMin         *int // minutes after local midnight; nil = no quiet hours
	QuietEndMin           *int
	DefaultReminderPreset string
	AreasEnabled          bool
	CreatedAt             time.Time
	Role                  Role
}

// NewHousehold is the input to CreateHousehold.
type NewHousehold struct {
	Name     string
	Timezone string // IANA name
	// StarterAreas are created with the household (already translated by the caller).
	StarterAreas []string
	// AreasEnabled turns the areas feature on for the new household.
	AreasEnabled bool
	// MaxHouseholds is the user's HouseholdsPerUser limit (0 = unchecked); it
	// is enforced inside the creating transaction so concurrent requests
	// cannot exceed it. ErrLimit when reached.
	MaxHouseholds int
}

// Member is a household membership with display details.
type Member struct {
	UserID      string
	DisplayName string
	Role        Role
	JoinedAt    time.Time
}

// CreateHousehold creates a household with the actor as owner, atomically.
func (s *Store) CreateHousehold(ctx context.Context, a Actor, in NewHousehold) (Household, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || a.UserID == "" {
		return Household{}, ErrInvalid
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil || in.Timezone == "" || in.Timezone == "Local" {
		return Household{}, ErrInvalid
	}
	now := s.nowMS()
	h := Household{ID: ids.New(), Name: name, Timezone: in.Timezone, AreasEnabled: in.AreasEnabled, CreatedAt: time.UnixMilli(now).UTC(), Role: RoleOwner}
	err := s.inTx(ctx, true, func(t *tx) error {
		if in.MaxHouseholds > 0 {
			if err := t.lockUser(ctx, a.UserID); err != nil {
				return err
			}
			var n int
			if err := t.queryRow(ctx, "SELECT count(*) FROM memberships WHERE user_id = ?", a.UserID).Scan(&n); err != nil {
				return err
			}
			if n >= in.MaxHouseholds {
				return ErrLimit
			}
		}
		if _, err := t.exec(ctx, "INSERT INTO households (id, name, timezone, areas_enabled, created_at) VALUES (?, ?, ?, ?, ?)",
			h.ID, h.Name, h.Timezone, in.AreasEnabled, now); err != nil {
			return err
		}
		if _, err := t.exec(ctx, "INSERT INTO memberships (household_id, user_id, role, joined_at) VALUES (?, ?, ?, ?)",
			h.ID, a.UserID, RoleOwner, now); err != nil {
			return notFoundFK(err)
		}
		return t.insertAreas(ctx, h.ID, in.StarterAreas, now)
	})
	if err != nil {
		return Household{}, err
	}
	return h, nil
}

// insertAreas creates areas with the given (translated) names.
func (t *tx) insertAreas(ctx context.Context, householdID string, names []string, now int64) error {
	for _, name := range names {
		name, lower, ok := cleanName(name)
		if !ok {
			return ErrInvalid
		}
		if _, err := t.exec(ctx, "INSERT INTO areas (id, household_id, name, name_lower, created_at) VALUES (?, ?, ?, ?, ?)",
			ids.New(), householdID, name, lower, now); err != nil {
			return conflict(err)
		}
	}
	return nil
}

const householdCols = `h.id, h.name, h.timezone, h.quiet_start_min, h.quiet_end_min, h.default_reminder_preset, h.areas_enabled, h.created_at, m.role`

func scanHousehold(row interface{ Scan(...any) error }) (Household, error) {
	var h Household
	var qs, qe sql.NullInt64
	var created int64
	if err := row.Scan(&h.ID, &h.Name, &h.Timezone, &qs, &qe, &h.DefaultReminderPreset, &h.AreasEnabled, &created, &h.Role); err != nil {
		return Household{}, notFound(err)
	}
	h.QuietStartMin, h.QuietEndMin = intPtr(qs), intPtr(qe)
	h.CreatedAt = time.UnixMilli(created).UTC()
	return h, nil
}

// ListHouseholds returns the households the actor belongs to.
func (s *Store) ListHouseholds(ctx context.Context, a Actor) ([]Household, error) {
	rows, err := s.db.QueryContext(ctx, s.d.Rebind(`SELECT `+householdCols+`
		FROM households h JOIN memberships m ON m.household_id = h.id AND m.user_id = ?
		ORDER BY h.name, h.id`), a.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Household
	for rows.Next() {
		h, err := scanHousehold(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// Household returns one household the actor belongs to. A single query with
// the membership join: non-members get ErrNotFound.
func (s *Store) Household(ctx context.Context, a Actor, householdID string) (Household, error) {
	return scanHousehold(s.db.QueryRowContext(ctx, s.d.Rebind(`SELECT `+householdCols+`
		FROM households h JOIN memberships m ON m.household_id = h.id AND m.user_id = ?
		WHERE h.id = ?`), a.UserID, householdID))
}

// Role returns the actor's role in the household, or ErrNotFound.
func (s *Store) Role(ctx context.Context, a Actor, householdID string) (Role, error) {
	var r Role
	err := s.db.QueryRowContext(ctx, s.d.Rebind("SELECT role FROM memberships WHERE household_id = ? AND user_id = ?"),
		householdID, a.UserID).Scan(&r)
	return r, notFound(err)
}

// ListMembers lists household members. Any member (including guests) may read.
func (s *Store) ListMembers(ctx context.Context, a Actor, householdID string) ([]Member, error) {
	var out []Member
	err := s.read(ctx, a, householdID, func(t *tx, _ Role) error {
		rows, err := t.query(ctx, `SELECT m.user_id, u.display_name, m.role, m.joined_at
			FROM memberships m JOIN users u ON u.id = m.user_id
			WHERE m.household_id = ? ORDER BY m.joined_at, m.user_id`, householdID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var m Member
			var joined int64
			if err := rows.Scan(&m.UserID, &m.DisplayName, &m.Role, &joined); err != nil {
				return err
			}
			m.JoinedAt = time.UnixMilli(joined).UTC()
			out = append(out, m)
		}
		return rows.Err()
	})
	return out, err
}

// AddMember adds an existing user to the household. Owner only.
// ErrConflict if already a member; ErrNotFound if the user does not exist.
func (s *Store) AddMember(ctx context.Context, a Actor, householdID, userID string, role Role) error {
	if !role.Valid() {
		return ErrInvalid
	}
	return s.manage(ctx, a, householdID, RoleOwner, func(t *tx, _ Role) error {
		_, err := t.exec(ctx, "INSERT INTO memberships (household_id, user_id, role, joined_at) VALUES (?, ?, ?, ?)",
			householdID, userID, role, s.nowMS())
		return notFoundFK(conflict(err))
	})
}

// ChangeRole changes a member's role. Owner only. The last owner cannot be
// demoted (ErrConflict). Downgrading to guest invalidates the member's
// mark-done links for this household.
func (s *Store) ChangeRole(ctx context.Context, a Actor, householdID, userID string, role Role) error {
	if !role.Valid() {
		return ErrInvalid
	}
	return s.manage(ctx, a, householdID, RoleOwner, func(t *tx, _ Role) error {
		var cur Role
		if err := t.queryRow(ctx, "SELECT role FROM memberships WHERE household_id = ? AND user_id = ?",
			householdID, userID).Scan(&cur); err != nil {
			return notFound(err)
		}
		if cur == role {
			return nil
		}
		if cur == RoleOwner {
			if err := t.ensureOtherOwner(ctx, householdID, userID); err != nil {
				return err
			}
		}
		if err := t.execOne(ctx, "UPDATE memberships SET role = ? WHERE household_id = ? AND user_id = ?",
			role, householdID, userID); err != nil {
			return err
		}
		if role == RoleGuest {
			return t.dropMemberTokens(ctx, householdID, userID)
		}
		return nil
	})
}

// RemoveMember removes a member. Owners may remove anyone; any member may
// remove themselves (leave). The last owner cannot leave (ErrConflict;
// ownership transfer is handled by the caller first). Cleans up the user's
// assignments, mutes, mark-done links and calendar feeds in this household.
func (s *Store) RemoveMember(ctx context.Context, a Actor, householdID, userID string) error {
	min := RoleOwner
	if userID == a.UserID {
		min = RoleGuest
	}
	return s.manage(ctx, a, householdID, min, func(t *tx, _ Role) error {
		var cur Role
		if err := t.queryRow(ctx, "SELECT role FROM memberships WHERE household_id = ? AND user_id = ?",
			householdID, userID).Scan(&cur); err != nil {
			return notFound(err)
		}
		if cur == RoleOwner {
			if err := t.ensureOtherOwner(ctx, householdID, userID); err != nil {
				return err
			}
		}
		if err := t.execOne(ctx, "DELETE FROM memberships WHERE household_id = ? AND user_id = ?", householdID, userID); err != nil {
			return err
		}
		for _, q := range []string{
			"DELETE FROM chore_assignees WHERE user_id = ? AND chore_id IN (SELECT id FROM chores WHERE household_id = ?)",
			"DELETE FROM chore_mutes WHERE user_id = ? AND chore_id IN (SELECT id FROM chores WHERE household_id = ?)",
			"DELETE FROM calendar_feeds WHERE user_id = ? AND household_id = ?",
		} {
			if _, err := t.exec(ctx, q, userID, householdID); err != nil {
				return err
			}
		}
		if err := t.dropMemberTokens(ctx, householdID, userID); err != nil {
			return err
		}
		return t.dropTokenBindings(ctx, householdID, userID)
	})
}

func (t *tx) ensureOtherOwner(ctx context.Context, householdID, userID string) error {
	var n int
	if err := t.queryRow(ctx, "SELECT count(*) FROM memberships WHERE household_id = ? AND role = ? AND user_id <> ?",
		householdID, RoleOwner, userID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrConflict
	}
	return nil
}

func (t *tx) dropMemberTokens(ctx context.Context, householdID, userID string) error {
	_, err := t.exec(ctx, "DELETE FROM mark_done_tokens WHERE user_id = ? AND chore_id IN (SELECT id FROM chores WHERE household_id = ?)",
		userID, householdID)
	return err
}

// dropTokenBindings removes the user's API tokens' binding to a household they
// no longer belong to, so rejoining later does not revive old tokens.
func (t *tx) dropTokenBindings(ctx context.Context, householdID, userID string) error {
	_, err := t.exec(ctx, "DELETE FROM api_token_households WHERE household_id = ? AND token_id IN (SELECT id FROM api_tokens WHERE user_id = ?)",
		householdID, userID)
	return err
}

func intPtr(v sql.NullInt64) *int {
	if !v.Valid {
		return nil
	}
	i := int(v.Int64)
	return &i
}

// lockUser serialises concurrent per-user membership counting on Postgres
// (SQLite write transactions are already serialised). ErrNotFound if the
// user does not exist.
func (t *tx) lockUser(ctx context.Context, userID string) error {
	q := "SELECT id FROM users WHERE id = ?"
	if t.d == db.Postgres {
		q += " FOR UPDATE"
	}
	var id string
	return notFound(t.queryRow(ctx, q, userID).Scan(&id))
}
