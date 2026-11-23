package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/db"
	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/schedule"
)

// Mark-done link lifetimes (THREAT_MODEL "Links that work without signing in").
const (
	MarkDoneMaxTTL = 7 * 24 * time.Hour
	UndoTTL        = 5 * time.Minute
)

// Audit events for unauthenticated links; internal/api records them with the
// client IP after ConsumeMarkDone / UndoWithToken succeed.
const (
	AuditMarkDoneUse  = "mark_done_use"
	AuditMarkDoneUndo = "mark_done_undo"
)

// RuleOf returns the recurrence of a recurring chore (nil, nil for one-off).
func RuleOf(c Chore) (schedule.Rule, error) {
	if c.Kind == KindOneOff {
		return nil, nil
	}
	if c.ScheduleType == ScheduleRRule {
		r, err := schedule.ParseRRule(c.RRule)
		if err != nil {
			return nil, ErrInvalid
		}
		return r, nil
	}
	iv := schedule.Interval{Every: c.IntervalN}
	switch c.IntervalUnit {
	case "day":
		iv.Unit = schedule.Days
	case "week":
		iv.Unit = schedule.Weeks
	case "month":
		iv.Unit = schedule.Months
		if fd, err := schedule.ParseDate(c.FirstDue); err == nil {
			iv.AnchorDay = fd.Day // keep the 31st across short months
		}
	default:
		return nil, ErrInvalid
	}
	return iv, nil
}

// OccurrenceInstant is the convention for Chore.NextDue: local midnight of
// the due date in the household zone (DST-shifted like any local time).
func OccurrenceInstant(d schedule.Date, loc *time.Location) time.Time {
	return schedule.LocalTime(d, 0, loc).UTC()
}

// NextOccurrence computes a recurring chore's occurrence after a completion
// at completedAt: its local due date and NextDue instant. ok is false when
// the series has no further occurrence.
func NextOccurrence(c Chore, loc *time.Location, completedAt time.Time) (due schedule.Date, next time.Time, ok bool, err error) {
	rule, err := RuleOf(c)
	if err != nil || rule == nil {
		return schedule.Date{}, time.Time{}, false, err
	}
	cur, err := schedule.ParseDate(c.DueDate)
	if err != nil {
		return schedule.Date{}, time.Time{}, false, ErrInvalid
	}
	anchor := schedule.FromScheduled
	if c.Anchor == AnchorFromCompletion {
		anchor = schedule.FromCompletion
	}
	due, ok = schedule.NextDue(rule, anchor, cur, schedule.DateOf(completedAt, loc))
	if !ok {
		return schedule.Date{}, time.Time{}, false, nil
	}
	return due, OccurrenceInstant(due, loc), true, nil
}

// MarkDoneLink addresses a mark-done link: one chore occurrence and either one
// non-guest recipient (personal channel) or one household channel.
type MarkDoneLink struct {
	ChoreID       string
	OccurrenceDue time.Time // the chore's NextDue when the reminder was sent
	UserID        string
	ChannelID     string
	IssuedAt      time.Time // the scheduler's clock; zero = now
	ExpiresAt     time.Time // at most MarkDoneMaxTTL after IssuedAt
}

// CreateMarkDone issues a mark-done token (system use: the scheduler) and
// returns its plaintext, which is never stored. It checks the chore is active
// on that occurrence and the recipient may complete it: ErrInvalid otherwise.
func (s *Store) CreateMarkDone(ctx context.Context, l MarkDoneLink) (string, error) {
	now := l.IssuedAt
	if now.IsZero() {
		now = s.now()
	}
	if (l.UserID == "") == (l.ChannelID == "") || !l.ExpiresAt.After(now) || l.ExpiresAt.Sub(now) > MarkDoneMaxTTL {
		return "", ErrInvalid
	}
	token := ids.Token()
	err := s.inTx(ctx, true, func(t *tx) error {
		row := mdRow{choreID: l.ChoreID, occurrence: l.OccurrenceDue.UnixMilli(), userID: l.UserID, channelID: l.ChannelID}
		if _, err := t.checkMarkDone(ctx, row, now.UnixMilli()); err != nil {
			if errors.Is(err, ErrNotFound) {
				return ErrInvalid
			}
			return err
		}
		_, err := t.exec(ctx, `INSERT INTO mark_done_tokens (token_hash, chore_id, occurrence_due, user_id, channel_id, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, ids.Hash(token), l.ChoreID, row.occurrence, nullStr(l.UserID), nullStr(l.ChannelID),
			now.UnixMilli(), l.ExpiresAt.UnixMilli())
		return err
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// RevokeMarkDone deletes a token that was never delivered (e.g. the send failed).
func (s *Store) RevokeMarkDone(ctx context.Context, token string) error {
	return s.inTx(ctx, true, func(t *tx) error {
		_, err := t.exec(ctx, "DELETE FROM mark_done_tokens WHERE token_hash = ?", ids.Hash(token))
		return err
	})
}

type mdRow struct {
	choreID    string
	occurrence int64
	userID     string
	channelID  string
}

// mdChore is what a mark-done link needs to know about its chore.
type mdChore struct {
	Chore
	loc *time.Location
}

// checkMarkDone verifies a link is still valid apart from use and expiry:
// the chore is active on the same occurrence, and the recipient is still a
// non-guest member (personal) or the channel still belongs to the chore's
// household (household). Any failure is ErrNotFound.
func (t *tx) checkMarkDone(ctx context.Context, r mdRow, now int64) (mdChore, error) {
	var hid, tz string
	if err := t.queryRow(ctx, "SELECT c.household_id, h.timezone FROM chores c JOIN households h ON h.id = c.household_id WHERE c.id = ?",
		r.choreID).Scan(&hid, &tz); err != nil {
		return mdChore{}, notFound(err)
	}
	c, err := t.chore(ctx, hid, r.choreID)
	if err != nil {
		return mdChore{}, err
	}
	if !c.ArchivedAt.IsZero() || c.PausedIndefinitely || c.PausedUntil.UnixMilli() > now || c.NextDue.IsZero() || c.NextDue.UnixMilli() != r.occurrence {
		return mdChore{}, ErrNotFound
	}
	if r.userID != "" {
		var role Role
		if err := t.queryRow(ctx, "SELECT role FROM memberships WHERE household_id = ? AND user_id = ?", hid, r.userID).Scan(&role); err != nil {
			return mdChore{}, notFound(err)
		}
		if !role.AtLeast(RoleMember) {
			return mdChore{}, ErrNotFound
		}
	} else {
		var n int
		if err := t.queryRow(ctx, "SELECT count(*) FROM notification_channels WHERE id = ? AND scope = 'household' AND household_id = ?",
			r.channelID, hid).Scan(&n); err != nil {
			return mdChore{}, err
		}
		if n == 0 {
			return mdChore{}, ErrNotFound
		}
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return mdChore{}, err
	}
	return mdChore{Chore: c, loc: loc}, nil
}

// lockChoreOfToken takes the row lock of the chore a token belongs to (a
// no-op on SQLite, whose write transactions are serialised). Unknown tokens
// are ErrNotFound.
func (t *tx) lockChoreOfToken(ctx context.Context, token string) error {
	if token == "" || len(token) > 128 {
		return ErrNotFound
	}
	var id string
	q := "SELECT c.id FROM chores c JOIN mark_done_tokens m ON m.chore_id = c.id WHERE m.token_hash = ?"
	if t.d == db.Postgres {
		q += " FOR UPDATE OF c"
	}
	return notFound(t.queryRow(ctx, q, ids.Hash(token)).Scan(&id))
}

// markDone loads a token by its hash and validates it. With claim, the token
// is atomically marked used first, so concurrent POSTs cannot both succeed.
func (t *tx) markDone(ctx context.Context, token string, now int64, claim bool) (mdRow, mdChore, error) {
	if token == "" || len(token) > 128 {
		return mdRow{}, mdChore{}, ErrNotFound
	}
	h := ids.Hash(token)
	if claim {
		res, err := t.exec(ctx, "UPDATE mark_done_tokens SET used_at = ? WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?", now, h, now)
		if err != nil {
			return mdRow{}, mdChore{}, err
		}
		if n, err := res.RowsAffected(); err != nil || n != 1 {
			return mdRow{}, mdChore{}, errors.Join(ErrNotFound, err)
		}
	}
	var r mdRow
	var user, channel sql.NullString
	var used sql.NullInt64
	var expires int64
	if err := t.queryRow(ctx, "SELECT chore_id, occurrence_due, user_id, channel_id, used_at, expires_at FROM mark_done_tokens WHERE token_hash = ?", h).
		Scan(&r.choreID, &r.occurrence, &user, &channel, &used, &expires); err != nil {
		return mdRow{}, mdChore{}, notFound(err)
	}
	r.userID, r.channelID = user.String, channel.String
	if !claim && (used.Valid || expires <= now) {
		return mdRow{}, mdChore{}, ErrNotFound
	}
	c, err := t.checkMarkDone(ctx, r, now)
	return r, c, err
}

// PeekMarkDone returns the chore name for a valid, unused mark-done token, for
// the GET confirmation page. It changes nothing and reveals nothing else.
// Every invalid, used, expired or stale token is ErrNotFound.
func (s *Store) PeekMarkDone(ctx context.Context, token string) (string, error) {
	var name string
	err := s.inTx(ctx, false, func(t *tx) error {
		_, c, err := t.markDone(ctx, token, s.nowMS(), false)
		name = c.Name
		return err
	})
	if err != nil {
		return "", err
	}
	return name, nil
}

// MarkDoneResult is the outcome of ConsumeMarkDone. UndoToken is plaintext,
// returned once; the IDs are for the audit log, not for the page.
type MarkDoneResult struct {
	ChoreName     string
	HouseholdID   string
	UserID        string // recipient; "" for a household channel
	ChannelID     string // household channel; "" for a personal link
	CompletionID  string
	UndoToken     string
	UndoExpiresAt time.Time
}

// ConsumeMarkDone completes a chore through a mark-done link, in one
// transaction: the token must be unused, unexpired and still current (see
// checkMarkDone). The completion is attributed to the recipient (source
// mark_done_link) or, for a household channel, to no user with the channel ID
// as token_id. Every other link for the chore is invalidated, and a 5-minute
// single-use undo token scoped to this completion is returned.
func (s *Store) ConsumeMarkDone(ctx context.Context, token string) (MarkDoneResult, error) {
	var res MarkDoneResult
	undo := ids.Token()
	err := s.inTx(ctx, true, func(t *tx) error {
		now := s.now()
		// Serialise with every other completion of the same chore (Postgres):
		// two links for one occurrence must not both pass the occurrence
		// check. markDone re-reads the chore after the lock is held.
		if err := t.lockChoreOfToken(ctx, token); err != nil {
			return err
		}
		r, c, err := t.markDone(ctx, token, now.UnixMilli(), true)
		if err != nil {
			return err
		}
		in := CompletionInput{CompletedAt: now, Source: SourceMarkDoneLink, TokenID: r.channelID}
		if c.Kind != KindOneOff {
			due, next, ok, err := NextOccurrence(c.Chore, c.loc, now)
			if err != nil {
				return err
			}
			in.DueDate = c.DueDate
			if ok {
				in.DueDate, in.NextDue = due.String(), next
			}
		}
		comp, err := t.complete(ctx, c.HouseholdID, c.ID, r.userID, in, now.UnixMilli())
		if err != nil {
			return err
		}
		exp := now.Add(UndoTTL)
		if _, err := t.exec(ctx, "INSERT INTO undo_tokens (token_hash, completion_id, created_at, expires_at) VALUES (?, ?, ?, ?)",
			ids.Hash(undo), comp.ID, now.UnixMilli(), exp.UnixMilli()); err != nil {
			return err
		}
		res = MarkDoneResult{ChoreName: c.Name, HouseholdID: c.HouseholdID, UserID: r.userID, ChannelID: r.channelID,
			CompletionID: comp.ID, UndoToken: undo, UndoExpiresAt: exp.UTC()}
		return nil
	})
	if err != nil {
		return MarkDoneResult{}, err
	}
	return res, nil
}

// UndoResult identifies a completion reverted by UndoWithToken (for the audit log).
type UndoResult struct {
	HouseholdID  string
	CompletionID string
}

// UndoWithToken reverts the completion an undo token was issued for. The token
// is single-use and valid for UndoTTL; anything else is ErrNotFound.
// ErrConflict if the completion was already reverted another way.
func (s *Store) UndoWithToken(ctx context.Context, token string) (UndoResult, error) {
	var res UndoResult
	err := s.inTx(ctx, true, func(t *tx) error {
		if token == "" || len(token) > 128 {
			return ErrNotFound
		}
		now, h := s.nowMS(), ids.Hash(token)
		r, err := t.exec(ctx, "UPDATE undo_tokens SET used_at = ? WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?", now, h, now)
		if err != nil {
			return err
		}
		if n, err := r.RowsAffected(); err != nil || n != 1 {
			return errors.Join(ErrNotFound, err)
		}
		if err := t.queryRow(ctx, `SELECT u.completion_id, ch.household_id FROM undo_tokens u
			JOIN completions c ON c.id = u.completion_id JOIN chores ch ON ch.id = c.chore_id
			WHERE u.token_hash = ?`, h).Scan(&res.CompletionID, &res.HouseholdID); err != nil {
			return notFound(err)
		}
		return t.revertCompletion(ctx, res.HouseholdID, res.CompletionID, "", now)
	})
	if err != nil {
		return UndoResult{}, err
	}
	return res, nil
}
