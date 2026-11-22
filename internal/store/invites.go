package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/db"
	"github.com/NexusBetweenVoids/gunkly/internal/ids"
)

// ErrLimit means a usage limit (internal/limits) would be exceeded.
var ErrLimit = errors.New("store: limit reached")

// Invite is a household invite link. The token is only ever returned once,
// at creation, by the caller; the store keeps its hash.
type Invite struct {
	ID          string
	HouseholdID string
	Role        Role
	CreatedBy   string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	MaxUses     int // how many people may join with it
	Uses        int // how many have
}

// MaxInviteUses bounds NewInvite.MaxUses.
const MaxInviteUses = 100

// NewInvite is the input to CreateInvite. MaxActive comes from internal/limits.
type NewInvite struct {
	Role      Role
	TokenHash string
	ExpiresAt time.Time
	MaxActive int
	MaxUses   int // 0 means 1; at most MaxInviteUses
}

// CreateInvite creates an invite link. Owner only. ErrLimit when the
// household already has MaxActive unexpired, unrevoked invites.
func (s *Store) CreateInvite(ctx context.Context, a Actor, householdID string, in NewInvite) (Invite, error) {
	if (in.Role != RoleMember && in.Role != RoleGuest) || in.TokenHash == "" {
		return Invite{}, ErrInvalid
	}
	now := s.nowMS()
	if in.MaxUses == 0 {
		in.MaxUses = 1
	}
	if in.ExpiresAt.UnixMilli() <= now || in.MaxUses < 1 || in.MaxUses > MaxInviteUses {
		return Invite{}, ErrInvalid
	}
	inv := Invite{ID: ids.New(), HouseholdID: householdID, Role: in.Role, CreatedBy: a.UserID,
		CreatedAt: time.UnixMilli(now).UTC(), ExpiresAt: in.ExpiresAt.UTC(), MaxUses: in.MaxUses}
	err := s.manage(ctx, a, householdID, RoleOwner, func(t *tx, _ Role) error {
		var n int
		if err := t.queryRow(ctx, "SELECT count(*) FROM invites WHERE household_id = ? AND revoked_at IS NULL AND expires_at > ? AND uses < max_uses",
			householdID, now).Scan(&n); err != nil {
			return err
		}
		if n >= in.MaxActive {
			return ErrLimit
		}
		_, err := t.exec(ctx, `INSERT INTO invites (id, household_id, token_hash, role, created_by, created_at, expires_at, max_uses)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, inv.ID, householdID, in.TokenHash, in.Role, a.UserID, now, in.ExpiresAt.UnixMilli(), in.MaxUses)
		return conflict(err)
	})
	if err != nil {
		return Invite{}, err
	}
	return inv, nil
}

// ListInvites lists the household's active invites. Owner only.
func (s *Store) ListInvites(ctx context.Context, a Actor, householdID string) ([]Invite, error) {
	var out []Invite
	err := s.inTx(ctx, false, func(t *tx) error {
		if _, err := t.authorize(ctx, a, householdID, RoleOwner, false); err != nil {
			return err
		}
		rows, err := t.query(ctx, `SELECT id, household_id, role, COALESCE(created_by, ''), created_at, expires_at, max_uses, uses FROM invites
			WHERE household_id = ? AND revoked_at IS NULL AND expires_at > ? AND uses < max_uses ORDER BY created_at, id`, householdID, s.nowMS())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var inv Invite
			var created, exp int64
			if err := rows.Scan(&inv.ID, &inv.HouseholdID, &inv.Role, &inv.CreatedBy, &created, &exp, &inv.MaxUses, &inv.Uses); err != nil {
				return err
			}
			inv.CreatedAt, inv.ExpiresAt = time.UnixMilli(created).UTC(), time.UnixMilli(exp).UTC()
			out = append(out, inv)
		}
		return rows.Err()
	})
	return out, err
}

// RevokeInvite revokes an active invite. Owner only.
func (s *Store) RevokeInvite(ctx context.Context, a Actor, householdID, inviteID string) error {
	return s.manage(ctx, a, householdID, RoleOwner, func(t *tx, _ Role) error {
		return t.execOne(ctx, "UPDATE invites SET revoked_at = ? WHERE id = ? AND household_id = ? AND revoked_at IS NULL",
			s.nowMS(), inviteID, householdID)
	})
}

// InviteHousehold returns the household of a valid (unexpired, unrevoked)
// invite, so the caller can look up its limits. ErrNotFound otherwise.
func (s *Store) InviteHousehold(ctx context.Context, tokenHash string) (string, error) {
	var hh string
	err := s.db.QueryRowContext(ctx, s.d.Rebind("SELECT household_id FROM invites WHERE token_hash = ? AND revoked_at IS NULL AND expires_at > ? AND uses < max_uses"),
		tokenHash, s.nowMS()).Scan(&hh)
	return hh, notFound(err)
}

// JoinLimits are the caps enforced when someone joins by invite.
type JoinLimits struct {
	MaxMembers    int // members in the household
	MaxHouseholds int // households the joining user belongs to
}

// Joined is the result of accepting an invite.
type Joined struct {
	HouseholdID string
	Role        Role
}

// AcceptInvite adds the user to the invite's household in one transaction:
// validates the hashed token, expiry and revocation, checks the limits and
// inserts the membership. ErrNotFound for an invalid invite, ErrConflict if
// already a member, ErrLimit if a limit is reached.
func (s *Store) AcceptInvite(ctx context.Context, userID, tokenHash string, lim JoinLimits) (Joined, error) {
	var j Joined
	err := s.inTx(ctx, true, func(t *tx) error {
		var err error
		j, err = t.acceptInvite(ctx, userID, tokenHash, lim, s.nowMS())
		return err
	})
	return j, err
}

// RegisterWithInvite creates an account and joins the invite's household
// atomically: if the invite is invalid or full, no account is created.
func (s *Store) RegisterWithInvite(ctx context.Context, in NewUser, tokenHash string, lim JoinLimits) (User, Joined, error) {
	var u User
	var j Joined
	in.IsAdmin = false
	err := s.inTx(ctx, true, func(t *tx) error {
		now := s.nowMS()
		var err error
		if u, err = t.insertUser(ctx, in, now); err != nil {
			return err
		}
		j, err = t.acceptInvite(ctx, u.ID, tokenHash, lim, now)
		return err
	})
	if err != nil {
		return User{}, Joined{}, err
	}
	return u, j, nil
}

func (t *tx) acceptInvite(ctx context.Context, userID, tokenHash string, lim JoinLimits, now int64) (Joined, error) {
	var j Joined
	var exp int64
	var uses, maxUses int
	var revoked sql.NullInt64
	if err := t.queryRow(ctx, "SELECT household_id, role, expires_at, revoked_at, uses, max_uses FROM invites WHERE token_hash = ?",
		tokenHash).Scan(&j.HouseholdID, &j.Role, &exp, &revoked, &uses, &maxUses); err != nil {
		return Joined{}, notFound(err)
	}
	if revoked.Valid || exp <= now || uses >= maxUses {
		return Joined{}, ErrNotFound
	}
	if t.d == db.Postgres {
		// Serialise with other membership changes in this household (see manage).
		var id string
		if err := t.queryRow(ctx, "SELECT id FROM households WHERE id = ? FOR UPDATE", j.HouseholdID).Scan(&id); err != nil {
			return Joined{}, notFound(err)
		}
	}
	if err := t.lockUser(ctx, userID); err != nil {
		return Joined{}, err
	}
	var already, members, households int
	if err := t.queryRow(ctx, `SELECT
		(SELECT count(*) FROM memberships WHERE household_id = ? AND user_id = ?),
		(SELECT count(*) FROM memberships WHERE household_id = ?),
		(SELECT count(*) FROM memberships WHERE user_id = ?)`,
		j.HouseholdID, userID, j.HouseholdID, userID).Scan(&already, &members, &households); err != nil {
		return Joined{}, err
	}
	switch {
	case already > 0:
		return Joined{}, ErrConflict
	case members >= lim.MaxMembers, households >= lim.MaxHouseholds:
		return Joined{}, ErrLimit
	}
	if _, err := t.exec(ctx, "INSERT INTO memberships (household_id, user_id, role, joined_at) VALUES (?, ?, ?, ?)",
		j.HouseholdID, userID, j.Role, now); err != nil {
		return Joined{}, notFoundFK(conflict(err))
	}
	// Consume one use atomically: the conditional UPDATE locks the invite row,
	// so concurrent joins cannot exceed max_uses. The enclosing transaction
	// rolls the membership back if the invite was used up meanwhile.
	res, err := t.exec(ctx, `UPDATE invites SET uses = uses + 1
		WHERE token_hash = ? AND revoked_at IS NULL AND expires_at > ? AND uses < max_uses`, tokenHash, now)
	if err != nil {
		return Joined{}, err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return Joined{}, errors.Join(ErrNotFound, err)
	}
	return j, nil
}
