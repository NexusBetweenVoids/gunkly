package db

import "context"

// MigrateBefore applies the migrations that sort before version, for tests of
// a migration's effect on existing data.
func (db *DB) MigrateBefore(ctx context.Context, version string) error {
	ms, err := db.migrations()
	if err != nil {
		return err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at BIGINT NOT NULL)"); err != nil {
		return err
	}
	for _, m := range ms {
		if m.version >= version {
			break
		}
		if err := db.apply(ctx, conn, m); err != nil {
			return err
		}
	}
	return nil
}
