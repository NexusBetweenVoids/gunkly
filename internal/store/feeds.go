package store

import (
	"context"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/ids"
)

// Feed is a calendar feed's metadata (the token is only returned on create
// and regenerate).
type Feed struct {
	ID          string
	HouseholdID string
	CreatedAt   time.Time
}

// CreateFeed creates an iCal feed for the actor in the household (any member,
// guests included: a feed is read-only) and returns its token once. max is
// limits.CalendarFeedsPerUser, resolved by the caller; ErrLimit when the
// actor already has max feeds (across households).
func (s *Store) CreateFeed(ctx context.Context, a Actor, householdID string, max int) (Feed, string, error) {
	if max <= 0 {
		return Feed{}, "", ErrInvalid
	}
	token := ids.Token()
	f := Feed{ID: ids.New(), HouseholdID: householdID}
	err := s.inTx(ctx, true, func(t *tx) error {
		// The per-user count is checked under the user lock, so concurrent
		// creates cannot overshoot max.
		if err := t.lockUser(ctx, a.UserID); err != nil {
			return err
		}
		if _, err := t.authorize(ctx, a, householdID, RoleGuest, true); err != nil {
			return err
		}
		var n int
		if err := t.queryRow(ctx, "SELECT count(*) FROM calendar_feeds WHERE user_id = ? AND revoked_at IS NULL", a.UserID).Scan(&n); err != nil {
			return err
		}
		if n >= max {
			return ErrLimit
		}
		now := s.nowMS()
		f.CreatedAt = time.UnixMilli(now).UTC()
		_, err := t.exec(ctx, "INSERT INTO calendar_feeds (id, token_hash, user_id, household_id, created_at) VALUES (?, ?, ?, ?, ?)",
			f.ID, ids.Hash(token), a.UserID, householdID, now)
		return err
	})
	if err != nil {
		return Feed{}, "", err
	}
	return f, token, nil
}

// ListFeeds lists the actor's active feeds in the household.
func (s *Store) ListFeeds(ctx context.Context, a Actor, householdID string) ([]Feed, error) {
	var out []Feed
	err := s.read(ctx, a, householdID, func(t *tx, _ Role) error {
		rows, err := t.query(ctx, `SELECT id, created_at FROM calendar_feeds
			WHERE user_id = ? AND household_id = ? AND revoked_at IS NULL ORDER BY created_at, id`, a.UserID, householdID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			f := Feed{HouseholdID: householdID}
			var created int64
			if err := rows.Scan(&f.ID, &created); err != nil {
				return err
			}
			f.CreatedAt = time.UnixMilli(created).UTC()
			out = append(out, f)
		}
		return rows.Err()
	})
	return out, err
}

// RevokeFeed deletes one of the actor's feeds; its URL stops working at once.
func (s *Store) RevokeFeed(ctx context.Context, a Actor, householdID, feedID string) error {
	return s.write(ctx, a, householdID, RoleGuest, func(t *tx, _ Role) error {
		return t.execOne(ctx, "DELETE FROM calendar_feeds WHERE id = ? AND user_id = ? AND household_id = ?", feedID, a.UserID, householdID)
	})
}

// RegenerateFeed replaces a feed's token (the old URL stops working) and
// returns the new one once.
func (s *Store) RegenerateFeed(ctx context.Context, a Actor, householdID, feedID string) (string, error) {
	token := ids.Token()
	err := s.write(ctx, a, householdID, RoleGuest, func(t *tx, _ Role) error {
		return t.execOne(ctx, "UPDATE calendar_feeds SET token_hash = ? WHERE id = ? AND user_id = ? AND household_id = ? AND revoked_at IS NULL",
			ids.Hash(token), feedID, a.UserID, householdID)
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// FeedChore is what a calendar feed shows of a chore: name, area and dates
// only (never notes or links).
type FeedChore struct {
	Chore    // Notes and Link are always empty
	AreaName string
}

// FeedData is a resolved calendar feed.
type FeedData struct {
	HouseholdName string
	Location      *time.Location
	Chores        []FeedChore // active (not archived, not paused indefinitely), with a due date
}

// FeedByToken resolves a feed token (unauthenticated GET). The feed's owner
// must still be a member of the household; anything else is ErrNotFound.
func (s *Store) FeedByToken(ctx context.Context, token string) (FeedData, error) {
	var out FeedData
	if token == "" || len(token) > 128 {
		return out, ErrNotFound
	}
	err := s.inTx(ctx, false, func(t *tx) error {
		var hid, tz string
		var areasOn bool
		err := t.queryRow(ctx, `SELECT f.household_id, h.name, h.timezone, h.areas_enabled FROM calendar_feeds f
			JOIN households h ON h.id = f.household_id
			JOIN memberships m ON m.household_id = f.household_id AND m.user_id = f.user_id
			WHERE f.token_hash = ? AND f.revoked_at IS NULL`, ids.Hash(token)).Scan(&hid, &out.HouseholdName, &tz, &areasOn)
		if err != nil {
			return notFound(err)
		}
		if out.Location, err = time.LoadLocation(tz); err != nil {
			return err
		}
		areas := map[string]string{} // stays empty (no CATEGORIES) when areas are off
		if err := func() error {
			if !areasOn {
				return nil
			}
			rows, err := t.query(ctx, "SELECT id, name FROM areas WHERE household_id = ?", hid)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var id, name string
				if err := rows.Scan(&id, &name); err != nil {
					return err
				}
				areas[id] = name
			}
			return rows.Err()
		}(); err != nil {
			return err
		}
		rows, err := t.query(ctx, "SELECT "+choreCols+` FROM chores
			WHERE household_id = ? AND archived_at IS NULL AND paused_indefinitely = FALSE AND next_due IS NOT NULL
			ORDER BY next_due, id`, hid)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanChore(rows)
			if err != nil {
				return err
			}
			c.Notes, c.Link = "", ""
			out.Chores = append(out.Chores, FeedChore{Chore: c, AreaName: areas[c.AreaID]})
		}
		return rows.Err()
	})
	if err != nil {
		return FeedData{}, err
	}
	return out, nil
}
