package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Channel scopes.
const (
	ChannelScopeUser      = "user"
	ChannelScopeHousehold = "household"
)

// Channel is a notification channel's metadata. The sealed URL never leaves
// the store except through ChannelSecrets / the scheduler's claim.
type Channel struct {
	ID          string
	Scope       string
	UserID      string // scope user
	HouseholdID string // scope household
	Service     string // notify kind
	Label       string
	Hint        string // masked URL ("…abcd"); the URL itself is never returned
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// NewChannel is a validated, sealed channel (see notify.Prepare): the ID is
// generated before sealing because it is the ciphertext's associated data.
type NewChannel struct {
	ID         string
	Service    string
	Ciphertext string
	Label      string
	Hint       string // masked form of the URL, for display
	Max        int    // limits.ChannelsPerUser / ChannelsPerHousehold, resolved by the caller
}

// ChannelUpdate changes a channel; nil/empty fields are unchanged. Service
// and Ciphertext are replaced together (a new URL, sealed for the same ID).
type ChannelUpdate struct {
	Label      *string
	Service    string
	Ciphertext string
	Hint       string // set with Ciphertext
}

func (in *NewChannel) validate() bool {
	in.Label = strings.TrimSpace(in.Label)
	return in.ID != "" && in.Service != "" && in.Ciphertext != "" && len(in.Label) <= 100 && in.Max > 0
}

func (u *ChannelUpdate) validate() bool {
	if u.Label != nil {
		l := strings.TrimSpace(*u.Label)
		u.Label = &l
		if len(l) > 100 {
			return false
		}
	}
	return (u.Service == "") == (u.Ciphertext == "")
}

const channelCols = `id, scope, COALESCE(user_id, ''), COALESCE(household_id, ''), service, label, url_hint, created_at, updated_at`

func scanChannel(row interface{ Scan(...any) error }) (Channel, error) {
	var c Channel
	var created, updated int64
	if err := row.Scan(&c.ID, &c.Scope, &c.UserID, &c.HouseholdID, &c.Service, &c.Label, &c.Hint, &created, &updated); err != nil {
		return Channel{}, notFound(err)
	}
	c.CreatedAt, c.UpdatedAt = time.UnixMilli(created).UTC(), time.UnixMilli(updated).UTC()
	return c, nil
}

func (t *tx) channels(ctx context.Context, where string, args ...any) ([]Channel, error) {
	rows, err := t.query(ctx, "SELECT "+channelCols+" FROM notification_channels WHERE "+where+" ORDER BY created_at, id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Channel
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (t *tx) insertChannel(ctx context.Context, scope, owner string, in NewChannel, now int64) (Channel, error) {
	col := "user_id"
	if scope == ChannelScopeHousehold {
		col = "household_id"
	}
	var n int
	if err := t.queryRow(ctx, "SELECT count(*) FROM notification_channels WHERE "+col+" = ?", owner).Scan(&n); err != nil {
		return Channel{}, err
	}
	if n >= in.Max {
		return Channel{}, ErrLimit
	}
	_, err := t.exec(ctx, `INSERT INTO notification_channels (id, scope, `+col+`, service, url_ciphertext, label, url_hint, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, in.ID, scope, owner, in.Service, in.Ciphertext, in.Label, in.Hint, now, now)
	if err != nil {
		return Channel{}, notFoundFK(conflict(err))
	}
	return scanChannel(t.queryRow(ctx, "SELECT "+channelCols+" FROM notification_channels WHERE id = ?", in.ID))
}

// CreateUserChannel adds a personal channel for the actor. ErrLimit at the
// per-user limit.
func (s *Store) CreateUserChannel(ctx context.Context, a Actor, in NewChannel) (Channel, error) {
	if !in.validate() || a.UserID == "" {
		return Channel{}, ErrInvalid
	}
	var c Channel
	err := s.inTx(ctx, true, func(t *tx) error {
		if err := t.lockUser(ctx, a.UserID); err != nil { // the limit holds under concurrency
			return err
		}
		var err error
		c, err = t.insertChannel(ctx, ChannelScopeUser, a.UserID, in, s.nowMS())
		return err
	})
	return c, err
}

// CreateHouseholdChannel adds a household channel. Member or owner: members
// manage chores and their reminders, so they may manage where the household's
// reminders go (guests may not). ErrLimit at the per-household limit.
func (s *Store) CreateHouseholdChannel(ctx context.Context, a Actor, householdID string, in NewChannel) (Channel, error) {
	if !in.validate() {
		return Channel{}, ErrInvalid
	}
	var c Channel
	err := s.manage(ctx, a, householdID, RoleMember, func(t *tx, _ Role) error { // household lock: the limit holds under concurrency
		var err error
		c, err = t.insertChannel(ctx, ChannelScopeHousehold, householdID, in, s.nowMS())
		return err
	})
	return c, err
}

// ListUserChannels lists the actor's personal channels.
func (s *Store) ListUserChannels(ctx context.Context, a Actor) ([]Channel, error) {
	var out []Channel
	err := s.inTx(ctx, false, func(t *tx) error {
		var err error
		out, err = t.channels(ctx, "scope = 'user' AND user_id = ?", a.UserID)
		return err
	})
	return out, err
}

// ListHouseholdChannels lists the household's channels. Member or owner
// (notification settings are not shown to guests).
func (s *Store) ListHouseholdChannels(ctx context.Context, a Actor, householdID string) ([]Channel, error) {
	var out []Channel
	err := s.inTx(ctx, false, func(t *tx) error {
		if _, err := t.authorize(ctx, a, householdID, RoleMember, false); err != nil {
			return err
		}
		var err error
		out, err = t.channels(ctx, "scope = 'household' AND household_id = ?", householdID)
		return err
	})
	return out, err
}

func (t *tx) updateChannel(ctx context.Context, where string, owner, channelID string, u ChannelUpdate, now int64) (Channel, error) {
	set, args := "updated_at = ?", []any{now}
	if u.Label != nil {
		set += ", label = ?"
		args = append(args, *u.Label)
	}
	if u.Ciphertext != "" {
		set += ", service = ?, url_ciphertext = ?, url_hint = ?"
		args = append(args, u.Service, u.Ciphertext, u.Hint)
	}
	args = append(args, channelID, owner)
	if err := t.execOne(ctx, "UPDATE notification_channels SET "+set+" WHERE id = ? AND "+where, args...); err != nil {
		return Channel{}, err
	}
	return scanChannel(t.queryRow(ctx, "SELECT "+channelCols+" FROM notification_channels WHERE id = ?", channelID))
}

// UpdateUserChannel edits one of the actor's personal channels.
func (s *Store) UpdateUserChannel(ctx context.Context, a Actor, channelID string, u ChannelUpdate) (Channel, error) {
	if !u.validate() {
		return Channel{}, ErrInvalid
	}
	var c Channel
	err := s.inTx(ctx, true, func(t *tx) error {
		var err error
		c, err = t.updateChannel(ctx, "scope = 'user' AND user_id = ?", a.UserID, channelID, u, s.nowMS())
		return err
	})
	return c, err
}

// UpdateHouseholdChannel edits a household channel (member or owner) and
// invalidates every mark-done link issued through it.
func (s *Store) UpdateHouseholdChannel(ctx context.Context, a Actor, householdID, channelID string, u ChannelUpdate) (Channel, error) {
	if !u.validate() {
		return Channel{}, ErrInvalid
	}
	var c Channel
	err := s.write(ctx, a, householdID, RoleMember, func(t *tx, _ Role) error {
		var err error
		if c, err = t.updateChannel(ctx, "scope = 'household' AND household_id = ?", householdID, channelID, u, s.nowMS()); err != nil {
			return err
		}
		_, err = t.exec(ctx, "DELETE FROM mark_done_tokens WHERE channel_id = ?", channelID)
		return err
	})
	return c, err
}

// DeleteUserChannel deletes one of the actor's personal channels.
func (s *Store) DeleteUserChannel(ctx context.Context, a Actor, channelID string) error {
	return s.inTx(ctx, true, func(t *tx) error {
		return t.execOne(ctx, "DELETE FROM notification_channels WHERE id = ? AND scope = 'user' AND user_id = ?", channelID, a.UserID)
	})
}

// DeleteHouseholdChannel deletes a household channel (member or owner) and
// its mark-done links.
func (s *Store) DeleteHouseholdChannel(ctx context.Context, a Actor, householdID, channelID string) error {
	return s.write(ctx, a, householdID, RoleMember, func(t *tx, _ Role) error {
		if _, err := t.exec(ctx, "DELETE FROM mark_done_tokens WHERE channel_id = ? AND channel_id IN (SELECT id FROM notification_channels WHERE household_id = ?)",
			channelID, householdID); err != nil {
			return err
		}
		return t.execOne(ctx, "DELETE FROM notification_channels WHERE id = ? AND scope = 'household' AND household_id = ?", channelID, householdID)
	})
}

// SetChoreMuted mutes or unmutes a chore's reminders for the actor. Any
// member may mute (it only affects themselves).
func (s *Store) SetChoreMuted(ctx context.Context, a Actor, householdID, choreID string, muted bool) error {
	return s.write(ctx, a, householdID, RoleGuest, func(t *tx, _ Role) error {
		var n int
		if err := t.queryRow(ctx, "SELECT count(*) FROM chores WHERE id = ? AND household_id = ?", choreID, householdID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		q := "DELETE FROM chore_mutes WHERE user_id = ? AND chore_id = ?"
		if muted {
			q = "INSERT INTO chore_mutes (user_id, chore_id) VALUES (?, ?) ON CONFLICT DO NOTHING"
		}
		_, err := t.exec(ctx, q, a.UserID, choreID)
		return err
	})
}

// MutedChores returns the IDs of the actor's muted chores in the household.
func (s *Store) MutedChores(ctx context.Context, a Actor, householdID string) ([]string, error) {
	var out []string
	err := s.read(ctx, a, householdID, func(t *tx, _ Role) error {
		rows, err := t.query(ctx, `SELECT m.chore_id FROM chore_mutes m JOIN chores c ON c.id = m.chore_id
			WHERE m.user_id = ? AND c.household_id = ? ORDER BY m.chore_id`, a.UserID, householdID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			out = append(out, id)
		}
		return rows.Err()
	})
	return out, err
}

// ChannelSecret is a channel's sealed URL, for key rotation.
type ChannelSecret struct {
	ID         string
	Ciphertext string
}

// ChannelSecrets pages through every channel's ciphertext by ID (system use:
// gunkly rotate-key). Pass the last ID of the previous page as afterID.
func (s *Store) ChannelSecrets(ctx context.Context, afterID string, limit int) ([]ChannelSecret, error) {
	return s.secretsPage(ctx, "notification_channels", "url_ciphertext", afterID, limit)
}

// ReplaceChannelCiphertext swaps a channel's ciphertext if it still equals
// old (system use: rotate-key). It does not touch updated_at or mark-done
// links: the URL is unchanged. ErrConflict if the channel changed meanwhile.
func (s *Store) ReplaceChannelCiphertext(ctx context.Context, channelID, old, replacement string) error {
	return s.replaceSecret(ctx, "notification_channels", "url_ciphertext", channelID, old, replacement)
}

// secretsPage pages through one table's sealed column by ID. table and col are
// package constants, never input.
func (s *Store) secretsPage(ctx context.Context, table, col, afterID string, limit int) ([]ChannelSecret, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	var out []ChannelSecret
	err := s.inTx(ctx, false, func(t *tx) error {
		rows, err := t.query(ctx, "SELECT id, "+col+" FROM "+table+" WHERE id > ? ORDER BY id LIMIT ?", afterID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c ChannelSecret
			if err := rows.Scan(&c.ID, &c.Ciphertext); err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}

// replaceSecret swaps one sealed column if it still equals old: ErrConflict if
// it changed meanwhile, ErrNotFound if the row is gone.
func (s *Store) replaceSecret(ctx context.Context, table, col, id, old, replacement string) error {
	return s.inTx(ctx, true, func(t *tx) error {
		res, err := t.exec(ctx, "UPDATE "+table+" SET "+col+" = ? WHERE id = ? AND "+col+" = ?", replacement, id, old)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			var x string
			if qerr := t.queryRow(ctx, "SELECT id FROM "+table+" WHERE id = ?", id).Scan(&x); errors.Is(qerr, sql.ErrNoRows) {
				return ErrNotFound
			}
			return ErrConflict
		}
		return nil
	})
}

// UserChannelCiphertext returns the sealed URL of one of the actor's personal
// channels, for a test send. ErrNotFound if it is not theirs.
func (s *Store) UserChannelCiphertext(ctx context.Context, a Actor, channelID string) (string, error) {
	var ct string
	err := s.inTx(ctx, false, func(t *tx) error {
		return notFound(t.queryRow(ctx, "SELECT url_ciphertext FROM notification_channels WHERE id = ? AND scope = 'user' AND user_id = ?",
			channelID, a.UserID).Scan(&ct))
	})
	return ct, err
}

// HouseholdChannelCiphertext returns the sealed URL of a household channel
// (member or owner), for a test send.
func (s *Store) HouseholdChannelCiphertext(ctx context.Context, a Actor, householdID, channelID string) (string, error) {
	var ct string
	err := s.inTx(ctx, false, func(t *tx) error {
		if _, err := t.authorize(ctx, a, householdID, RoleMember, false); err != nil {
			return err
		}
		return notFound(t.queryRow(ctx, "SELECT url_ciphertext FROM notification_channels WHERE id = ? AND scope = 'household' AND household_id = ?",
			channelID, householdID).Scan(&ct))
	})
	return ct, err
}
