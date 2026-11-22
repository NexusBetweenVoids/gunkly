package store

import (
	"context"
	"strings"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/ids"
)

// Area groups chores within a household (Kitchen, Garden...).
type Area struct {
	ID          string
	HouseholdID string
	Name        string
	CreatedAt   time.Time
}

func cleanName(s string) (name, lower string, ok bool) {
	name = strings.TrimSpace(s)
	return name, strings.ToLower(name), name != "" && len(name) <= 200
}

// CreateArea adds an area. Member or owner. ErrConflict if the name exists
// (case-insensitive); ErrLimit if the household already has maxAreas (0 = no
// cap). The count is checked under the household lock, so concurrent creates
// cannot overshoot.
func (s *Store) CreateArea(ctx context.Context, a Actor, householdID, name string, maxAreas int) (Area, error) {
	name, lower, ok := cleanName(name)
	if !ok {
		return Area{}, ErrInvalid
	}
	now := s.nowMS()
	ar := Area{ID: ids.New(), HouseholdID: householdID, Name: name, CreatedAt: time.UnixMilli(now).UTC()}
	err := s.manage(ctx, a, householdID, RoleMember, func(t *tx, _ Role) error {
		if maxAreas > 0 {
			var n int
			if err := t.queryRow(ctx, "SELECT count(*) FROM areas WHERE household_id = ?", householdID).Scan(&n); err != nil {
				return err
			}
			if n >= maxAreas {
				return ErrLimit
			}
		}
		_, err := t.exec(ctx, "INSERT INTO areas (id, household_id, name, name_lower, created_at) VALUES (?, ?, ?, ?, ?)",
			ar.ID, householdID, name, lower, now)
		return conflict(err)
	})
	if err != nil {
		return Area{}, err
	}
	return ar, nil
}

// ListAreas lists the household's areas by name.
func (s *Store) ListAreas(ctx context.Context, a Actor, householdID string) ([]Area, error) {
	var out []Area
	err := s.read(ctx, a, householdID, func(t *tx, _ Role) error {
		rows, err := t.query(ctx, "SELECT id, name, created_at FROM areas WHERE household_id = ? ORDER BY name_lower, id", householdID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			ar := Area{HouseholdID: householdID}
			var created int64
			if err := rows.Scan(&ar.ID, &ar.Name, &created); err != nil {
				return err
			}
			ar.CreatedAt = time.UnixMilli(created).UTC()
			out = append(out, ar)
		}
		return rows.Err()
	})
	return out, err
}

// Area returns one area of the household.
func (s *Store) Area(ctx context.Context, a Actor, householdID, areaID string) (Area, error) {
	ar := Area{ID: areaID, HouseholdID: householdID}
	err := s.read(ctx, a, householdID, func(t *tx, _ Role) error {
		var created int64
		err := t.queryRow(ctx, "SELECT name, created_at FROM areas WHERE id = ? AND household_id = ?", areaID, householdID).
			Scan(&ar.Name, &created)
		ar.CreatedAt = time.UnixMilli(created).UTC()
		return notFound(err)
	})
	if err != nil {
		return Area{}, err
	}
	return ar, nil
}

// RenameArea renames an area. Member or owner.
func (s *Store) RenameArea(ctx context.Context, a Actor, householdID, areaID, name string) error {
	name, lower, ok := cleanName(name)
	if !ok {
		return ErrInvalid
	}
	return s.write(ctx, a, householdID, RoleMember, func(t *tx, _ Role) error {
		return conflict(t.execOne(ctx, "UPDATE areas SET name = ?, name_lower = ? WHERE id = ? AND household_id = ?",
			name, lower, areaID, householdID))
	})
}

// DeleteArea deletes an area; its chores become area-less. Member or owner.
func (s *Store) DeleteArea(ctx context.Context, a Actor, householdID, areaID string) error {
	return s.write(ctx, a, householdID, RoleMember, func(t *tx, _ Role) error {
		return t.execOne(ctx, "DELETE FROM areas WHERE id = ? AND household_id = ?", areaID, householdID)
	})
}
