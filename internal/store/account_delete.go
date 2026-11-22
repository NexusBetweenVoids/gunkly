package store

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/db"
)

// Errors specific to removing accounts and last owners.
var (
	// ErrOwnerRequired: a household would be left without an owner and no
	// successor was chosen. The caller learns the households from
	// OwnerRequiredError (errors.As).
	ErrOwnerRequired = errors.New("store: new owner required")
	// ErrLastAdmin: the account is the only instance admin.
	ErrLastAdmin = errors.New("store: last instance admin")
)

// Candidate is a household member who may take over ownership. Guests are
// never candidates.
type Candidate struct {
	UserID      string
	DisplayName string
	Role        Role
}

// OwnerChoice is a household whose last owner is leaving and that has other
// non-guest members: the leaver must pick one of Candidates.
type OwnerChoice struct {
	HouseholdID   string
	HouseholdName string
	Candidates    []Candidate // longest-standing member first
}

// OwnerRequiredError carries the households that still need a successor.
type OwnerRequiredError struct{ Households []OwnerChoice }

func (e *OwnerRequiredError) Error() string { return ErrOwnerRequired.Error() }
func (e *OwnerRequiredError) Unwrap() error { return ErrOwnerRequired }

// HouseholdRef names a household for a preview.
type HouseholdRef struct {
	ID   string
	Name string
}

// DeletionPreview is what deleting the account (or leaving) would do.
type DeletionPreview struct {
	DeleteHouseholds []HouseholdRef // no non-guest member would remain
	NeedOwner        []OwnerChoice  // the user is the last owner and others remain
}

// AccountDeletion tunes DeleteAccount.
type AccountDeletion struct {
	// NewOwners maps household ID to the member who becomes owner there.
	NewOwners map[string]string
	// AutoTransfer lets system processes (inactive-account cleanup) hand
	// ownership to the longest-standing non-guest member when no choice was
	// made. User-initiated requests never set it.
	AutoTransfer bool
	// IdleBefore, if set, makes the deletion conditional on the user not
	// having been active since then (re-checked in the transaction, so a
	// sign-in racing the cleanup wins): ErrConflict otherwise.
	IdleBefore time.Time
	// RefuseAdmin makes deleting any instance admin an ErrLastAdmin.
	RefuseAdmin bool
}

// Transfer records one ownership hand-over.
type Transfer struct{ HouseholdID, NewOwnerID string }

// DeleteResult summarises a deletion or a leave.
type DeleteResult struct {
	DeletedHouseholds []string
	Transfers         []Transfer
}

// memberRow is one membership of the household being examined.
type memberRow struct {
	userID, name string
	role         Role
}

// householdMembers lists the household's members, longest-standing first.
func (t *tx) householdMembers(ctx context.Context, householdID string) ([]memberRow, error) {
	rows, err := t.query(ctx, `SELECT m.user_id, u.display_name, m.role
		FROM memberships m JOIN users u ON u.id = m.user_id WHERE m.household_id = ?
		ORDER BY m.joined_at, m.user_id`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []memberRow
	for rows.Next() {
		var m memberRow
		if err := rows.Scan(&m.userID, &m.name, &m.role); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// leaveKind is what a user's departure means for a household.
type leaveKind int

const (
	leaveSimple   leaveKind = iota // others keep the household as is
	leaveDelete                    // no non-guest member remains: the household goes
	leaveTransfer                  // last owner, others remain: a successor is needed
)

// leaveInfo classifies userID's departure from the household with members ms
// (longest-standing first) and returns the non-guest successors.
func leaveInfo(ms []memberRow, userID string) (leaveKind, []Candidate) {
	var role Role
	otherOwner := false
	var cands []Candidate
	for _, m := range ms {
		if m.userID == userID {
			role = m.role
			continue
		}
		if m.role == RoleOwner {
			otherOwner = true
		}
		if m.role != RoleGuest {
			cands = append(cands, Candidate{UserID: m.userID, DisplayName: m.name, Role: m.role})
		}
	}
	switch {
	case role == "" || role == RoleGuest:
		return leaveSimple, cands
	case len(cands) == 0:
		return leaveDelete, nil
	case role == RoleOwner && !otherOwner:
		return leaveTransfer, cands
	}
	return leaveSimple, cands
}

// userHouseholds lists the households userID belongs to (ID and name).
func (t *tx) userHouseholds(ctx context.Context, userID string) ([]HouseholdRef, error) {
	rows, err := t.query(ctx, `SELECT h.id, h.name FROM households h JOIN memberships m ON m.household_id = h.id
		WHERE m.user_id = ? ORDER BY h.name, h.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HouseholdRef
	for rows.Next() {
		var h HouseholdRef
		if err := rows.Scan(&h.ID, &h.Name); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// AccountDeletionPreview lists the households that would be deleted with the
// account and those where the user is the last owner.
func (s *Store) AccountDeletionPreview(ctx context.Context, userID string) (DeletionPreview, error) {
	var p DeletionPreview
	err := s.inTx(ctx, false, func(t *tx) error {
		hs, err := t.userHouseholds(ctx, userID)
		if err != nil {
			return err
		}
		for _, h := range hs {
			ms, err := t.householdMembers(ctx, h.ID)
			if err != nil {
				return err
			}
			switch kind, cands := leaveInfo(ms, userID); kind {
			case leaveDelete:
				p.DeleteHouseholds = append(p.DeleteHouseholds, h)
			case leaveTransfer:
				p.NeedOwner = append(p.NeedOwner, OwnerChoice{HouseholdID: h.ID, HouseholdName: h.Name, Candidates: cands})
			}
		}
		return nil
	})
	return p, err
}

// pickOwner resolves the successor in c: the explicit choice (must be a
// candidate), else the longest-standing candidate when auto, else "".
func pickOwner(c OwnerChoice, chosen string, auto bool) (string, error) {
	if chosen != "" {
		for _, cand := range c.Candidates {
			if cand.UserID == chosen {
				return chosen, nil
			}
		}
		return "", ErrInvalid
	}
	if auto && len(c.Candidates) > 0 {
		return c.Candidates[0].UserID, nil
	}
	return "", nil
}

// applyLeave performs userID's departure from one household inside t:
// delete it, hand over ownership, or just remove the membership with its
// per-household data. It reports what happened; needs is set (and nothing
// changed) when a successor is missing.
func (t *tx) applyLeave(ctx context.Context, h HouseholdRef, userID, chosen string, auto bool) (deleted bool, newOwner string, needs *OwnerChoice, err error) {
	ms, err := t.householdMembers(ctx, h.ID)
	if err != nil {
		return false, "", nil, err
	}
	kind, cands := leaveInfo(ms, userID)
	switch kind {
	case leaveDelete:
		_, err = t.exec(ctx, "DELETE FROM households WHERE id = ?", h.ID)
		return true, "", nil, err
	case leaveTransfer:
		c := OwnerChoice{HouseholdID: h.ID, HouseholdName: h.Name, Candidates: cands}
		if newOwner, err = pickOwner(c, chosen, auto); err != nil {
			return false, "", nil, err
		}
		if newOwner == "" {
			return false, "", &c, nil
		}
		if err = t.execOne(ctx, "UPDATE memberships SET role = ? WHERE household_id = ? AND user_id = ?", RoleOwner, h.ID, newOwner); err != nil {
			return false, "", nil, err
		}
	}
	if err = t.execOne(ctx, "DELETE FROM memberships WHERE household_id = ? AND user_id = ?", h.ID, userID); err != nil {
		return false, "", nil, err
	}
	for _, q := range []string{
		"DELETE FROM chore_assignees WHERE user_id = ? AND chore_id IN (SELECT id FROM chores WHERE household_id = ?)",
		"DELETE FROM chore_mutes WHERE user_id = ? AND chore_id IN (SELECT id FROM chores WHERE household_id = ?)",
		"DELETE FROM calendar_feeds WHERE user_id = ? AND household_id = ?",
	} {
		if _, err = t.exec(ctx, q, userID, h.ID); err != nil {
			return false, "", nil, err
		}
	}
	if err = t.dropMemberTokens(ctx, h.ID, userID); err != nil {
		return false, "", nil, err
	}
	return false, newOwner, nil, t.dropTokenBindings(ctx, h.ID, userID)
}

// LeaveOptions tunes LeaveHousehold.
type LeaveOptions struct {
	// NewOwner is the member who becomes owner when the leaver is the last
	// owner and others remain.
	NewOwner string
	// ConfirmDelete acknowledges that leaving deletes the household because
	// no non-guest member would remain.
	ConfirmDelete bool
}

// LeaveHousehold removes the actor from the household. As last owner with
// other non-guest members they must name a successor (OwnerRequiredError
// otherwise); with none, the household would be deleted and ConfirmDelete is
// required (ErrConflict otherwise).
func (s *Store) LeaveHousehold(ctx context.Context, a Actor, householdID string, o LeaveOptions) (DeleteResult, error) {
	var res DeleteResult
	err := s.manage(ctx, a, householdID, RoleGuest, func(t *tx, _ Role) error {
		h := HouseholdRef{ID: householdID}
		if err := t.queryRow(ctx, "SELECT name FROM households WHERE id = ?", householdID).Scan(&h.Name); err != nil {
			return notFound(err)
		}
		ms, err := t.householdMembers(ctx, householdID)
		if err != nil {
			return err
		}
		if kind, _ := leaveInfo(ms, a.UserID); kind == leaveDelete && !o.ConfirmDelete {
			return ErrConflict
		}
		deleted, newOwner, needs, err := t.applyLeave(ctx, h, a.UserID, o.NewOwner, false)
		switch {
		case err != nil:
			return err
		case needs != nil:
			return &OwnerRequiredError{Households: []OwnerChoice{*needs}}
		case deleted:
			res.DeletedHouseholds = append(res.DeletedHouseholds, householdID)
		case newOwner != "":
			res.Transfers = append(res.Transfers, Transfer{householdID, newOwner})
		}
		return nil
	})
	if err != nil {
		return DeleteResult{}, err
	}
	return res, nil
}

// userOwnedStatements clear a deleted user's data by user ID. They are listed
// explicitly (not left to ON DELETE CASCADE) so the guarantee reads in one
// place.
var userOwnedStatements = []string{
	"DELETE FROM sessions WHERE user_id = ?",
	"DELETE FROM recovery_codes WHERE user_id = ?",
	"DELETE FROM user_tokens WHERE user_id = ?",
	"DELETE FROM mark_done_tokens WHERE user_id = ?",
	"DELETE FROM calendar_feeds WHERE user_id = ?",
	"DELETE FROM api_tokens WHERE user_id = ?",
	"DELETE FROM push_subscriptions WHERE user_id = ?",
	"DELETE FROM notification_channels WHERE scope = 'user' AND user_id = ?",
	"DELETE FROM deliveries WHERE user_id = ?",
	"DELETE FROM chore_assignees WHERE user_id = ?",
	"DELETE FROM chore_mutes WHERE user_id = ?",
	"UPDATE completions SET user_id = NULL WHERE user_id = ?",
	"UPDATE chores SET created_by = NULL WHERE created_by = ?",
	"UPDATE invites SET created_by = NULL WHERE created_by = ?",
}

// DeleteAccount permanently deletes the user in one transaction: ownership of
// households where they are the last owner passes on (explicitly chosen, or
// the longest-standing non-guest member when o.AutoTransfer), households with
// no non-guest member left are deleted, chores keep their other assignees
// (those assigned only to the user become unassigned), completions are
// anonymised (user_id NULL, shown as "Deleted user"), every credential,
// session, channel, push subscription and feed goes, and audit rows lose
// their actor, IP and metadata. Guests are never promoted.
//
// ErrLastAdmin if the user is the only instance admin (or any admin with
// RefuseAdmin); an OwnerRequiredError (ErrOwnerRequired) lists every
// household still lacking a successor.
func (s *Store) DeleteAccount(ctx context.Context, userID string, o AccountDeletion) (DeleteResult, error) {
	var res DeleteResult
	err := s.inTx(ctx, true, func(t *tx) error {
		res = DeleteResult{}
		if err := t.lockUser(ctx, userID); err != nil {
			return err
		}
		var isAdmin bool
		var active int64
		if err := t.queryRow(ctx, "SELECT is_admin, last_active_at FROM users WHERE id = ?", userID).Scan(&isAdmin, &active); err != nil {
			return notFound(err)
		}
		if !o.IdleBefore.IsZero() && active >= o.IdleBefore.UnixMilli() {
			return ErrConflict
		}
		if isAdmin {
			var others int
			if err := t.queryRow(ctx, "SELECT count(*) FROM users WHERE is_admin = ? AND id <> ?", true, userID).Scan(&others); err != nil {
				return err
			}
			if others == 0 || o.RefuseAdmin {
				return ErrLastAdmin
			}
		}
		hs, err := t.userHouseholds(ctx, userID)
		if err != nil {
			return err
		}
		var missing []OwnerChoice
		for _, h := range hs {
			if t.d == db.Postgres {
				var id string
				if err := t.queryRow(ctx, "SELECT id FROM households WHERE id = ? FOR UPDATE", h.ID).Scan(&id); err != nil {
					return notFound(err)
				}
			}
			deleted, newOwner, needs, err := t.applyLeave(ctx, h, userID, o.NewOwners[h.ID], o.AutoTransfer)
			switch {
			case err != nil:
				return err
			case needs != nil:
				missing = append(missing, *needs)
			case deleted:
				res.DeletedHouseholds = append(res.DeletedHouseholds, h.ID)
			case newOwner != "":
				res.Transfers = append(res.Transfers, Transfer{h.ID, newOwner})
			}
		}
		if len(missing) > 0 {
			return &OwnerRequiredError{Households: missing}
		}
		for _, q := range userOwnedStatements {
			if _, err := t.exec(ctx, q, userID); err != nil {
				return err
			}
		}
		// Audit rows keep no personal data: the user's own rows lose actor,
		// IP and metadata; another actor's row that mentions this user's ID in
		// its metadata loses only its metadata and IP (the actor stays, so the
		// trail of what others did is kept).
		if _, err := t.exec(ctx, "UPDATE audit_log SET actor_user_id = NULL, ip = '', meta = '{}' WHERE actor_user_id = ?", userID); err != nil {
			return err
		}
		if _, err := t.exec(ctx, "UPDATE audit_log SET ip = '', meta = '{}' WHERE meta LIKE ?", "%"+userID+"%"); err != nil {
			return err
		}
		if err := t.execOne(ctx, "DELETE FROM users WHERE id = ?", userID); err != nil {
			return err
		}
		now := s.nowMS()
		for _, tr := range res.Transfers {
			if err := t.insertAudit(ctx, AuditEvent{HouseholdID: tr.HouseholdID, Event: AuditOwnerTransfer,
				Meta: map[string]string{"owner": tr.NewOwnerID}}, now); err != nil {
				return err
			}
		}
		return t.insertAudit(ctx, AuditEvent{Event: AuditAccountDelete}, now)
	})
	if err != nil {
		return DeleteResult{}, err
	}
	sort.Strings(res.DeletedHouseholds)
	return res, nil
}
