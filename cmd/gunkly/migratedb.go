package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/NexusBetweenVoids/gunkly/internal/db"
)

// migrateDB implements `gunkly migrate-db`: copy a SQLite database into an
// empty Postgres one and verify the copy. It needs no secret key (ciphertext
// is copied as stored) and never writes to the source. Connection URLs may
// contain passwords and are never printed.
func migrateDB(ctx context.Context, from, to string, stdout io.Writer) error {
	if !strings.HasPrefix(from, "sqlite://") {
		return errors.New("--from must be a sqlite:// URL")
	}
	if !strings.HasPrefix(to, "postgres://") && !strings.HasPrefix(to, "postgresql://") {
		return errors.New("--to must be a postgres:// URL")
	}
	src, err := db.OpenSQLiteReadOnly(ctx, from)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := db.Open(ctx, to)
	if err != nil {
		return err
	}
	defer dst.Close()
	report, err := db.CopyToPostgres(ctx, src, dst)
	if err != nil {
		return fmt.Errorf("migrate-db: %w (nothing was copied)", err)
	}
	for _, t := range report.Tables {
		fmt.Fprintf(stdout, "%-24s %8d rows  sha256 %.16s\n", t.Name, t.Rows, t.Checksum)
	}
	_, err = fmt.Fprintf(stdout, "Copied and verified %d tables, %d rows. The SQLite file was not modified.\n"+
		"Point DATABASE_URL at the Postgres database and start Gunkly.\n", len(report.Tables), report.Rows())
	return err
}
