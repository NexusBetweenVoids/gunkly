package db

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var errNotSQLite = errors.New("db: operation is only supported on SQLite")

// BackupFile writes a consistent snapshot of the SQLite database to dst using
// VACUUM INTO while the app keeps running. dst must not exist.
func (db *DB) BackupFile(ctx context.Context, dst string) error {
	if db.Dialect != SQLite {
		return errNotSQLite
	}
	if strings.ContainsRune(dst, 0) {
		return errors.New("db: invalid backup path")
	}
	// VACUUM INTO takes a string literal; dst is operator-supplied (CLI), and
	// quoting doubles any single quote.
	_, err := db.ExecContext(ctx, "VACUUM INTO '"+strings.ReplaceAll(dst, "'", "''")+"'")
	if err != nil {
		return fmt.Errorf("db: backup: %w", err)
	}
	// Backups hold every household's data: owner-only, whatever the umask.
	return os.Chmod(dst, 0o600)
}

// secureFiles restricts the SQLite database and its -wal/-shm companions to
// the owner (0600). Missing companions are skipped; SQLite creates them with
// the main file's mode, and this runs again after migrations.
func (db *DB) secureFiles() error {
	if db.Dialect != SQLite {
		return nil
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Chmod(db.path+suffix, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("db: restrict file mode: %w", err)
		}
	}
	return nil
}

// Backup streams a consistent snapshot of the SQLite database to w (e.g.
// stdout for `docker exec gunkly /gunkly backup > backup.db`). The snapshot is
// staged in a temporary file next to the database (inside the data volume, so
// it works on a read-only root filesystem) and removed afterwards.
func (db *DB) Backup(ctx context.Context, w io.Writer) error {
	if db.Dialect != SQLite {
		return errNotSQLite
	}
	dir, err := os.MkdirTemp(filepath.Dir(db.path), ".gunkly-backup-")
	if err != nil {
		return fmt.Errorf("db: backup staging: %w", err)
	}
	defer os.RemoveAll(dir)
	tmp := filepath.Join(dir, "backup.db")
	if err := db.BackupFile(ctx, tmp); err != nil {
		return err
	}
	f, err := os.Open(tmp) //nolint:gosec // G304: path built by this function inside its own temp dir
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

// IncrementalVacuum returns up to pages free pages to the filesystem without
// blocking like a full VACUUM. A no-op on Postgres (autovacuum handles it).
func (db *DB) IncrementalVacuum(ctx context.Context, pages int) error {
	if db.Dialect != SQLite || pages <= 0 {
		return nil
	}
	// PRAGMA does not accept bound parameters; pages is an int.
	rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA incremental_vacuum(%d)", pages))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
	}
	return rows.Err()
}
