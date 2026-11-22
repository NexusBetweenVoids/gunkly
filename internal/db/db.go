// Package db opens SQLite or Postgres connections, rebinds placeholders and
// applies embedded migrations.
//
// SQLite temporary files: SQLite writes temp files (large sorts, VACUUM INTO
// staging) to $SQLITE_TMPDIR. Under the read-only root filesystem, set
// SQLITE_TMPDIR to a directory inside the data volume (e.g. /data/tmp) or a
// small tmpfs mount.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib" // registers "pgx"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Dialect identifies the SQL database in use.
type Dialect int

const (
	SQLite Dialect = iota
	Postgres
)

func (d Dialect) String() string {
	if d == Postgres {
		return "postgres"
	}
	return "sqlite"
}

// DB is an open database handle plus its dialect.
type DB struct {
	*sql.DB
	Dialect Dialect
	path    string // SQLite file path; empty for Postgres
}

// sqlitePragmas run on every new connection. auto_vacuum sorts before
// journal_mode in the driver's apply order, so on a fresh file it takes effect
// before WAL or the first table locks the page layout in.
var sqlitePragmas = []string{
	"busy_timeout(5000)",
	"auto_vacuum(INCREMENTAL)",
	"journal_mode(WAL)",
	"foreign_keys(ON)",
	"synchronous(NORMAL)",
}

// Open connects to url: sqlite://<path> (sqlite:///data/gunkly.db is the
// absolute path /data/gunkly.db) or postgres://... / postgresql://...
func Open(ctx context.Context, rawURL string) (*DB, error) {
	var d *DB
	switch {
	case strings.HasPrefix(rawURL, "sqlite://"):
		path := strings.TrimPrefix(rawURL, "sqlite://")
		if path == "" || strings.ContainsAny(path, "?#") || path == ":memory:" {
			return nil, errors.New("db: sqlite URL must be sqlite://<file path> with no query")
		}
		q := url.Values{"_pragma": sqlitePragmas, "_txlock": {"immediate"}}
		sdb, err := sql.Open("sqlite", path+"?"+q.Encode())
		if err != nil {
			return nil, err
		}
		d = &DB{DB: sdb, Dialect: SQLite, path: path}
	case strings.HasPrefix(rawURL, "postgres://"), strings.HasPrefix(rawURL, "postgresql://"):
		pdb, err := sql.Open("pgx", rawURL)
		if err != nil {
			return nil, errors.New("db: invalid postgres URL") // pgx errors may echo the password
		}
		pdb.SetMaxOpenConns(20)
		pdb.SetConnMaxIdleTime(5 * time.Minute)
		d = &DB{DB: pdb, Dialect: Postgres}
	default:
		return nil, errors.New("db: unsupported database URL scheme")
	}
	if err := d.PingContext(ctx); err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("db: connect %s: %w", d.Dialect, redact(err))
	}
	if err := d.secureFiles(); err != nil {
		_ = d.Close()
		return nil, err
	}
	return d, nil
}

// redact drops connection-string details pgx may include in errors.
func redact(err error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return fmt.Errorf("%s (%s)", pe.Message, pe.Code)
	}
	if strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "://") {
		return errors.New("connection failed")
	}
	return err
}

// Rebind converts ? placeholders to $1, $2, ... for Postgres. It does not
// parse SQL: queries must not contain a literal '?' (in strings, comments or
// operators such as jsonb ?). Pass such values as parameters instead.
func (d Dialect) Rebind(query string) string {
	if d != Postgres || !strings.Contains(query, "?") {
		return query
	}
	var b strings.Builder
	b.Grow(len(query) + 8)
	n := 0
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			n++
			b.WriteByte('$')
			fmt.Fprint(&b, n)
			continue
		}
		b.WriteByte(query[i])
	}
	return b.String()
}

// IsUniqueViolation reports whether err is a unique/primary-key constraint failure.
func IsUniqueViolation(err error) bool {
	var se *sqlite.Error
	if errors.As(err, &se) {
		return se.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE || se.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY
	}
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

// IsForeignKeyViolation reports whether err is a foreign-key constraint failure.
func IsForeignKeyViolation(err error) bool {
	var se *sqlite.Error
	if errors.As(err, &se) {
		return se.Code() == sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY
	}
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23503"
}
