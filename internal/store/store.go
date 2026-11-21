// Package store is the only data-access layer. Household-scoped methods take
// an Actor and a household ID and enforce membership and minimum role inside
// the same transaction as the data access, so the check cannot race the work.
//
// Error contract:
//   - ErrNotFound: the row does not exist OR the actor is not a member of the
//     household (no existence oracle across tenants).
//   - ErrForbidden: the actor is a member but their role is too low.
package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/db"
)

var (
	ErrNotFound  = errors.New("store: not found")
	ErrForbidden = errors.New("store: forbidden")
	ErrConflict  = errors.New("store: conflict")
	ErrInvalid   = errors.New("store: invalid input")
)

// Actor is the authenticated user on whose behalf a call is made.
type Actor struct {
	UserID string
}

// Role is a household membership role. Each role includes the rights of the
// roles below it: guest (read) < member (write, complete) < owner (manage).
type Role string

const (
	RoleGuest  Role = "guest"
	RoleMember Role = "member"
	RoleOwner  Role = "owner"
)

func (r Role) rank() int {
	switch r {
	case RoleGuest:
		return 1
	case RoleMember:
		return 2
	case RoleOwner:
		return 3
	}
	return 0
}

// Valid reports whether r is a known role.
func (r Role) Valid() bool { return r.rank() > 0 }

// AtLeast reports whether r grants at least min.
func (r Role) AtLeast(min Role) bool { return r.rank() >= min.rank() }

// Store wraps the database handle. Safe for concurrent use.
type Store struct {
	db  *sql.DB
	d   db.Dialect
	now func() time.Time
}

// New returns a Store over an open, migrated database.
func New(d *db.DB) *Store {
	return &Store{db: d.DB, d: d.Dialect, now: time.Now}
}

// tx is a transaction that rebinds placeholders for the dialect.
type tx struct {
	*sql.Tx
	d db.Dialect
}

func (t *tx) exec(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return t.ExecContext(ctx, t.d.Rebind(q), args...)
}

func (t *tx) query(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return t.QueryContext(ctx, t.d.Rebind(q), args...)
}

func (t *tx) queryRow(ctx context.Context, q string, args ...any) *sql.Row {
	return t.QueryRowContext(ctx, t.d.Rebind(q), args...)
}

// execOne runs a statement that must affect exactly one row, else ErrNotFound.
func (t *tx) execOne(ctx context.Context, q string, args ...any) error {
	res, err := t.exec(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return errors.Join(ErrNotFound, err)
	}
	return nil
}

// inTx runs fn in a transaction. Read transactions are snapshot-consistent
// (SQLite deferred tx in WAL; Postgres REPEATABLE READ READ ONLY). Write
// transactions take the write lock up front on SQLite (BEGIN IMMEDIATE).
func (s *Store) inTx(ctx context.Context, write bool, fn func(*tx) error) error {
	opts := &sql.TxOptions{ReadOnly: !write}
	if !write && s.d == db.Postgres {
		opts.Isolation = sql.LevelRepeatableRead
	}
	t, err := s.db.BeginTx(ctx, opts)
	if err != nil {
		return err
	}
	defer func() { _ = t.Rollback() }() // no-op after Commit
	if err := fn(&tx{Tx: t, d: s.d}); err != nil {
		return err
	}
	return t.Commit()
}

// authorize returns the actor's role in the household within t, or
// ErrNotFound if not a member, or ErrForbidden if below min. In write
// transactions on Postgres the membership row is locked (FOR SHARE) so a
// concurrent removal or downgrade waits until this transaction finishes.
func (t *tx) authorize(ctx context.Context, a Actor, householdID string, min Role, write bool) (Role, error) {
	q := "SELECT role FROM memberships WHERE household_id = ? AND user_id = ?"
	if write && t.d == db.Postgres {
		q += " FOR SHARE"
	}
	var r Role
	if err := t.queryRow(ctx, q, householdID, a.UserID).Scan(&r); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	if !r.AtLeast(min) {
		return r, ErrForbidden
	}
	return r, nil
}

// read runs fn in a snapshot transaction after checking the actor can read
// the household (any role).
func (s *Store) read(ctx context.Context, a Actor, householdID string, fn func(*tx, Role) error) error {
	return s.inTx(ctx, false, func(t *tx) error {
		r, err := t.authorize(ctx, a, householdID, RoleGuest, false)
		if err != nil {
			return err
		}
		return fn(t, r)
	})
}

// write runs fn in a write transaction after checking the actor's role is at
// least min.
func (s *Store) write(ctx context.Context, a Actor, householdID string, min Role, fn func(*tx, Role) error) error {
	return s.writeTx(ctx, a, householdID, min, false, fn)
}

// manage is write for membership changes. On Postgres it first locks the
// household row so concurrent membership changes in one household serialise
// (e.g. two owners demoting each other cannot leave it ownerless). SQLite
// write transactions are already serialised by BEGIN IMMEDIATE.
func (s *Store) manage(ctx context.Context, a Actor, householdID string, min Role, fn func(*tx, Role) error) error {
	return s.writeTx(ctx, a, householdID, min, true, fn)
}

func (s *Store) writeTx(ctx context.Context, a Actor, householdID string, min Role, lockHousehold bool, fn func(*tx, Role) error) error {
	return s.inTx(ctx, true, func(t *tx) error {
		if lockHousehold && t.d == db.Postgres {
			var id string
			if err := t.queryRow(ctx, "SELECT id FROM households WHERE id = ? FOR UPDATE", householdID).Scan(&id); err != nil {
				return notFound(err)
			}
		}
		r, err := t.authorize(ctx, a, householdID, min, true)
		if err != nil {
			return err
		}
		return fn(t, r)
	})
}

func (s *Store) nowMS() int64 { return s.now().UnixMilli() }

// conflict maps unique violations to ErrConflict.
func conflict(err error) error {
	if db.IsUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// ms converts a time to nullable Unix milliseconds (zero time -> NULL).
func ms(t time.Time) sql.NullInt64 {
	if t.IsZero() {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: t.UnixMilli(), Valid: true}
}

// fromMS converts nullable Unix milliseconds to UTC time (NULL -> zero).
func fromMS(v sql.NullInt64) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	return time.UnixMilli(v.Int64).UTC()
}

func nullStr(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

// notFoundFK maps foreign-key violations (a referenced row is missing) to ErrNotFound.
func notFoundFK(err error) error {
	if db.IsForeignKeyViolation(err) {
		return ErrNotFound
	}
	return err
}
