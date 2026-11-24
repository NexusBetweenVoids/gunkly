package store

import (
	"context"
	"strings"
	"time"
)

// HouseholdUpdate changes household settings; nil fields are unchanged.
// QuietHours set to a pointer to nil clears quiet hours.
type HouseholdUpdate struct {
	Name       *string
	Timezone   *string
	QuietHours **QuietHours
	// DefaultReminderPreset is the household default ("" = none); the caller
	// validates the name against the scheduler's presets.
	DefaultReminderPreset *string
	// AreasEnabled switches the areas feature on or off. Areas and the
	// chores' area links are kept when it is off.
	AreasEnabled *bool
	// StarterAreas (already translated by the caller) are created when the
	// household has no areas yet; otherwise they are ignored.
	StarterAreas []string
}

// QuietHours are minutes after local midnight; End may be before Start
// (overnight).
type QuietHours struct {
	StartMin int
	EndMin   int
}

func (q *QuietHours) valid() bool {
	return q.StartMin >= 0 && q.StartMin <= 1439 && q.EndMin >= 0 && q.EndMin <= 1439 && q.StartMin != q.EndMin
}

// UpdateHousehold changes household settings. Owner only.
func (s *Store) UpdateHousehold(ctx context.Context, a Actor, householdID string, u HouseholdUpdate) (Household, error) {
	if u.Name != nil && strings.TrimSpace(*u.Name) == "" {
		return Household{}, ErrInvalid
	}
	if u.Timezone != nil {
		if _, err := time.LoadLocation(*u.Timezone); err != nil || *u.Timezone == "" || *u.Timezone == "Local" {
			return Household{}, ErrInvalid
		}
	}
	if u.QuietHours != nil && *u.QuietHours != nil && !(*u.QuietHours).valid() {
		return Household{}, ErrInvalid
	}
	if u.DefaultReminderPreset != nil && len(*u.DefaultReminderPreset) > 32 {
		return Household{}, ErrInvalid
	}
	var h Household
	err := s.manage(ctx, a, householdID, RoleOwner, func(t *tx, _ Role) error {
		if u.Name != nil {
			if err := t.execOne(ctx, "UPDATE households SET name = ? WHERE id = ?", strings.TrimSpace(*u.Name), householdID); err != nil {
				return err
			}
		}
		if u.Timezone != nil {
			if err := t.execOne(ctx, "UPDATE households SET timezone = ? WHERE id = ?", *u.Timezone, householdID); err != nil {
				return err
			}
		}
		if u.DefaultReminderPreset != nil {
			if err := t.execOne(ctx, "UPDATE households SET default_reminder_preset = ? WHERE id = ?", *u.DefaultReminderPreset, householdID); err != nil {
				return err
			}
		}
		if u.AreasEnabled != nil {
			if err := t.execOne(ctx, "UPDATE households SET areas_enabled = ? WHERE id = ?", *u.AreasEnabled, householdID); err != nil {
				return err
			}
		}
		if len(u.StarterAreas) > 0 {
			var n int
			if err := t.queryRow(ctx, "SELECT count(*) FROM areas WHERE household_id = ?", householdID).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				if err := t.insertAreas(ctx, householdID, u.StarterAreas, s.nowMS()); err != nil {
					return err
				}
			}
		}
		if u.QuietHours != nil {
			var start, end any
			if q := *u.QuietHours; q != nil {
				start, end = q.StartMin, q.EndMin
			}
			if err := t.execOne(ctx, "UPDATE households SET quiet_start_min = ?, quiet_end_min = ? WHERE id = ?", start, end, householdID); err != nil {
				return err
			}
		}
		var err error
		h, err = scanHousehold(t.queryRow(ctx, `SELECT `+householdCols+`
			FROM households h JOIN memberships m ON m.household_id = h.id AND m.user_id = ? WHERE h.id = ?`, a.UserID, householdID))
		return err
	})
	return h, err
}
