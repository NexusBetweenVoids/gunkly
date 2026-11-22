package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// OpenSQLiteReadOnly opens an existing SQLite file for reading only (SQLite
// mode=ro, no pragmas that write, no journal-mode change), so a migration
// source is never modified. The URL has the form sqlite://<path>.
func OpenSQLiteReadOnly(ctx context.Context, rawURL string) (*DB, error) {
	path, ok := strings.CutPrefix(rawURL, "sqlite://")
	if !ok || path == "" || strings.ContainsAny(path, "?#") {
		return nil, errors.New("db: source must be sqlite://<file path> with no query")
	}
	if st, err := os.Stat(path); err != nil || st.IsDir() {
		return nil, errors.New("db: source SQLite file does not exist")
	}
	esc := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(path)
	sdb, err := sql.Open("sqlite", "file:"+esc+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	d := &DB{DB: sdb, Dialect: SQLite, path: path}
	if err := d.PingContext(ctx); err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("db: open source: %w", err)
	}
	return d, nil
}

// TableReport is the verified outcome for one table.
type TableReport struct {
	Name     string
	Rows     int
	Checksum string // hex SHA-256 over the rows in primary-key order
}

// CopyReport lists every copied table in copy order.
type CopyReport struct {
	Tables []TableReport
}

// Rows is the total number of rows copied.
func (r CopyReport) Rows() int {
	n := 0
	for _, t := range r.Tables {
		n += t.Rows
	}
	return n
}

type column struct {
	name string
	kind valueKind
	pk   int // 1-based position in the primary key, 0 = not part of it
}

type table struct {
	name string
	cols []column
	deps []string // tables this one references
}

func (t table) colNames() []string {
	out := make([]string, len(t.cols))
	for i, c := range t.cols {
		out[i] = c.name
	}
	return out
}

type valueKind int

const (
	kindText valueKind = iota
	kindInt
	kindBool
)

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// CopyToPostgres copies every table of the SQLite database src into the empty
// Postgres database dst, which it first migrates to the same schema version.
//
// It refuses when src has pending (or unknown) migrations, when dst is not
// empty (no tables, or only an empty schema_migrations), or when the two
// schemas differ in tables or columns. The source is read in one read
// transaction (a consistent snapshot); the target is written in one
// transaction, verified there (per-table row counts and a SHA-256 over the
// rows ordered by primary key, normalised so both databases compare equal)
// and committed only when everything matches. On any failure the data is
// rolled back and the tables created by this call are dropped again, so the
// command can be retried. The tables are derived from the schema
// (sqlite_master, PRAGMA foreign_key_list), not from a list in the code.
func CopyToPostgres(ctx context.Context, src, dst *DB) (_ CopyReport, err error) {
	if src.Dialect != SQLite || dst.Dialect != Postgres {
		return CopyReport{}, errors.New("db: copy goes from SQLite to Postgres")
	}
	if err := src.requireCurrent(ctx); err != nil {
		return CopyReport{}, fmt.Errorf("source: %w", err)
	}
	if err := dst.requireEmpty(ctx); err != nil {
		return CopyReport{}, fmt.Errorf("target: %w", err)
	}
	if err := dst.Migrate(ctx); err != nil {
		return CopyReport{}, fmt.Errorf("target: %w", err)
	}
	created := true
	defer func() {
		if err != nil && created {
			dst.dropAll(context.WithoutCancel(ctx))
		}
	}()

	stx, err := src.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return CopyReport{}, err
	}
	defer func() { _ = stx.Rollback() }()
	dtx, err := dst.BeginTx(ctx, nil)
	if err != nil {
		return CopyReport{}, err
	}
	defer func() { _ = dtx.Rollback() }()

	tables, err := sourceSchema(ctx, stx)
	if err != nil {
		return CopyReport{}, err
	}
	if err := dst.checkTargetSchema(ctx, dtx, tables); err != nil {
		return CopyReport{}, err
	}
	order, err := copyOrder(tables)
	if err != nil {
		return CopyReport{}, err
	}
	// The target's schema_migrations was filled by Migrate; replace it with the
	// source's history so applied_at is preserved too.
	if _, err := dtx.ExecContext(ctx, "DELETE FROM schema_migrations"); err != nil {
		return CopyReport{}, err
	}
	var report CopyReport
	for _, t := range order {
		tr, err := copyTable(ctx, stx, dtx, t)
		if err != nil {
			return CopyReport{}, fmt.Errorf("table %s: %w", t.name, err)
		}
		report.Tables = append(report.Tables, tr)
	}
	for _, tr := range report.Tables {
		got, err := checksumTable(ctx, dtx, tableByName(order, tr.Name), true)
		if err != nil {
			return CopyReport{}, fmt.Errorf("verify %s: %w", tr.Name, err)
		}
		if got.Rows != tr.Rows || got.Checksum != tr.Checksum {
			return CopyReport{}, fmt.Errorf("verify %s: target has %d rows (checksum %.12s), source %d (%.12s)",
				tr.Name, got.Rows, got.Checksum, tr.Rows, tr.Checksum)
		}
	}
	if err := dtx.Commit(); err != nil {
		return CopyReport{}, err
	}
	created = false
	return report, nil
}

func tableByName(ts []table, name string) table {
	for _, t := range ts {
		if t.name == name {
			return t
		}
	}
	return table{}
}

// requireCurrent refuses a database with pending migrations, or with
// migrations this binary does not know (it was written by a newer version).
func (db *DB) requireCurrent(ctx context.Context) error {
	pending, err := db.Pending(ctx)
	if err != nil {
		return err
	}
	if len(pending) > 0 {
		return fmt.Errorf("migrations pending (%s): run `gunkly migrate` first", strings.Join(pending, ", "))
	}
	ms, err := db.migrations()
	if err != nil {
		return err
	}
	applied, err := appliedVersions(ctx, db.DB, db.Dialect)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, m := range ms {
		known[m.version] = true
	}
	for v := range applied {
		if !known[v] {
			return fmt.Errorf("migration %s is unknown to this version: upgrade gunkly first", v)
		}
	}
	return nil
}

// requireEmpty accepts a Postgres schema with no tables, or with only an
// empty schema_migrations table.
func (db *DB) requireEmpty(ctx context.Context) error {
	names, err := db.tableNames(ctx)
	if err != nil {
		return err
	}
	switch {
	case len(names) == 0:
		return nil
	case len(names) == 1 && names[0] == "schema_migrations":
		var n int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations").Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
	}
	return errors.New("database is not empty: migrate-db only fills an empty database")
}

// dropAll removes the tables of the current schema; used only to undo the
// schema this call created in a database it found empty.
func (db *DB) dropAll(ctx context.Context) {
	names, err := db.tableNames(ctx)
	if err != nil {
		return
	}
	for _, n := range names {
		_, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS "+quoteIdent(n)+" CASCADE")
	}
}

// tableNames lists the base tables of the current Postgres schema.
func (db *DB) tableNames(ctx context.Context) ([]string, error) {
	return strings1(ctx, db.DB, `SELECT table_name FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_type = 'BASE TABLE'`)
}

// sourceSchema reads tables, columns, primary keys and foreign keys from SQLite.
func sourceSchema(ctx context.Context, q querier) ([]table, error) {
	names, err := strings1(ctx, q, "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		return nil, err
	}
	var out []table
	for _, n := range names {
		t := table{name: n}
		var err error
		if t.cols, err = sourceColumns(ctx, q, n); err != nil {
			return nil, err
		}
		if t.deps, err = strings1(ctx, q, `SELECT DISTINCT "table" FROM pragma_foreign_key_list(?) ORDER BY 1`, n); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

func sourceColumns(ctx context.Context, q querier, tableName string) ([]column, error) {
	rows, err := q.QueryContext(ctx, "SELECT name, type, pk FROM pragma_table_info(?) ORDER BY cid", tableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []column
	for rows.Next() {
		var c column
		var typ string
		if err := rows.Scan(&c.name, &typ, &c.pk); err != nil {
			return nil, err
		}
		if c.kind, err = kindOfSQLite(typ); err != nil {
			return nil, fmt.Errorf("table %s column %s: %w", tableName, c.name, err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func strings1(ctx context.Context, q querier, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func kindOfSQLite(decl string) (valueKind, error) {
	switch strings.ToUpper(decl) {
	case "TEXT":
		return kindText, nil
	case "INTEGER", "BIGINT":
		return kindInt, nil
	case "BOOLEAN":
		return kindBool, nil
	}
	return 0, fmt.Errorf("unsupported column type %q", decl)
}

// checkTargetSchema verifies the Postgres schema has the same tables and
// columns as the source, with compatible types.
func (db *DB) checkTargetSchema(ctx context.Context, q querier, src []table) error {
	rows, err := q.QueryContext(ctx, `SELECT table_name, column_name, data_type FROM information_schema.columns
		WHERE table_schema = current_schema() ORDER BY table_name, ordinal_position`)
	if err != nil {
		return err
	}
	defer rows.Close()
	got := map[string]map[string]string{}
	for rows.Next() {
		var t, c, typ string
		if err := rows.Scan(&t, &c, &typ); err != nil {
			return err
		}
		if got[t] == nil {
			got[t] = map[string]string{}
		}
		got[t][c] = typ
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(got) != len(src) {
		return fmt.Errorf("schema mismatch: source has %d tables, target %d", len(src), len(got))
	}
	for _, t := range src {
		cols, ok := got[t.name]
		if !ok || len(cols) != len(t.cols) {
			return fmt.Errorf("schema mismatch in table %s", t.name)
		}
		for _, c := range t.cols {
			typ, ok := cols[c.name]
			if !ok || !kindMatches(c.kind, typ) {
				return fmt.Errorf("schema mismatch in table %s column %s", t.name, c.name)
			}
		}
	}
	return nil
}

func kindMatches(k valueKind, pgType string) bool {
	switch k {
	case kindText:
		return pgType == "text" || pgType == "character varying"
	case kindInt:
		return pgType == "bigint" || pgType == "integer" || pgType == "smallint"
	case kindBool:
		return pgType == "boolean"
	}
	return false
}

// copyOrder sorts tables so each comes after the tables it references
// (Kahn's algorithm, alphabetical among equals for a deterministic order).
func copyOrder(ts []table) ([]table, error) {
	byName := map[string]table{}
	for _, t := range ts {
		byName[t.name] = t
	}
	done := map[string]bool{}
	var out []table
	for len(out) < len(ts) {
		var ready []string
		for _, t := range ts {
			if done[t.name] {
				continue
			}
			ok := true
			for _, d := range t.deps {
				if _, known := byName[d]; !known {
					return nil, fmt.Errorf("table %s references unknown table %s", t.name, d)
				}
				if d != t.name && !done[d] {
					ok = false
				}
			}
			if ok {
				ready = append(ready, t.name)
			}
		}
		if len(ready) == 0 {
			return nil, errors.New("foreign keys form a cycle; cannot order the tables")
		}
		sort.Strings(ready)
		for _, n := range ready {
			done[n] = true
			out = append(out, byName[n])
		}
	}
	return out, nil
}

// normalise converts a scanned value to the canonical form shared by both
// databases: nil, bool, int64 or string.
func normalise(k valueKind, v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	switch k {
	case kindBool:
		switch x := v.(type) {
		case bool:
			return x, nil
		case int64:
			if x == 0 || x == 1 {
				return x == 1, nil
			}
		}
	case kindInt:
		switch x := v.(type) {
		case int64:
			return x, nil
		case bool:
			if x {
				return int64(1), nil
			}
			return int64(0), nil
		case string:
			if n, err := strconv.ParseInt(x, 10, 64); err == nil {
				return n, nil
			}
		}
	case kindText:
		switch x := v.(type) {
		case string:
			return x, nil
		case []byte:
			return string(x), nil
		}
	}
	return nil, fmt.Errorf("value of type %T does not fit the column", v)
}

// rowHash accumulates a deterministic digest of normalised rows.
type rowHash struct {
	h hash.Hash
	n int
}

func newRowHash() *rowHash { return &rowHash{h: sha256.New()} }

func (r *rowHash) add(vals []any) {
	r.n++
	var b [8]byte
	for _, v := range vals {
		switch x := v.(type) {
		case nil:
			r.h.Write([]byte{'n'})
		case bool:
			if x {
				r.h.Write([]byte{'b', 1})
			} else {
				r.h.Write([]byte{'b', 0})
			}
		case int64:
			r.h.Write([]byte{'i'})
			binary.BigEndian.PutUint64(b[:], uint64(x)) //nolint:gosec // G115: the bit pattern is what is hashed
			r.h.Write(b[:])
		case string:
			r.h.Write([]byte{'t'})
			binary.BigEndian.PutUint64(b[:], uint64(len(x)))
			r.h.Write(b[:])
			r.h.Write([]byte(x))
		}
	}
	r.h.Write([]byte{'\n'})
}

func (r *rowHash) report(name string) TableReport {
	return TableReport{Name: name, Rows: r.n, Checksum: hex.EncodeToString(r.h.Sum(nil))}
}

// orderBy is the deterministic row order: the primary key, else every column.
// Text is ordered bytewise ("C") on Postgres to match SQLite's BINARY order.
func orderBy(t table, postgres bool) string {
	cols := slices.Clone(t.cols)
	var key []column
	for _, c := range cols {
		if c.pk > 0 {
			key = append(key, c)
		}
	}
	sort.Slice(key, func(i, j int) bool { return key[i].pk < key[j].pk })
	if len(key) == 0 {
		key = cols
	}
	parts := make([]string, len(key))
	for i, c := range key {
		parts[i] = quoteIdent(c.name)
		if postgres && c.kind == kindText {
			parts[i] += ` COLLATE "C"`
		}
	}
	return strings.Join(parts, ", ")
}

func selectAll(t table, postgres bool) string {
	cols := make([]string, len(t.cols))
	for i, c := range t.cols {
		cols[i] = quoteIdent(c.name)
	}
	return "SELECT " + strings.Join(cols, ", ") + " FROM " + quoteIdent(t.name) + " ORDER BY " + orderBy(t, postgres)
}

// scanRow reads one row into normalised values.
func scanRow(rows *sql.Rows, t table) ([]any, error) {
	raw := make([]any, len(t.cols))
	ptrs := make([]any, len(raw))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	for i, c := range t.cols {
		v, err := normalise(c.kind, raw[i])
		if err != nil {
			return nil, fmt.Errorf("column %s: %w", c.name, err)
		}
		raw[i] = v
	}
	return raw, nil
}

// checksumTable counts and hashes a table's rows from q.
func checksumTable(ctx context.Context, q querier, t table, postgres bool) (TableReport, error) {
	rows, err := q.QueryContext(ctx, selectAll(t, postgres))
	if err != nil {
		return TableReport{}, err
	}
	defer rows.Close()
	h := newRowHash()
	for rows.Next() {
		vals, err := scanRow(rows, t)
		if err != nil {
			return TableReport{}, err
		}
		h.add(vals)
	}
	if err := rows.Err(); err != nil {
		return TableReport{}, err
	}
	return h.report(t.name), nil
}

// copyTable streams the table from the source snapshot into the target in
// batches, returning the source-side report. It also cross-checks the
// streamed row count against count(*) on the same snapshot.
func copyTable(ctx context.Context, stx, dtx *sql.Tx, t table) (TableReport, error) {
	var want int
	if err := stx.QueryRowContext(ctx, "SELECT count(*) FROM "+quoteIdent(t.name)).Scan(&want); err != nil {
		return TableReport{}, err
	}
	rows, err := stx.QueryContext(ctx, selectAll(t, false))
	if err != nil {
		return TableReport{}, err
	}
	defer rows.Close()
	batchRows := max(1, min(500, 60000/max(1, len(t.cols))))
	insertPrefix := "INSERT INTO " + quoteIdent(t.name) + " (" + strings.Join(quoteAll(t.colNames()), ", ") + ") VALUES "
	h := newRowHash()
	var batch []any
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		n := len(batch) / len(t.cols)
		var b strings.Builder
		b.WriteString(insertPrefix)
		for r := range n {
			if r > 0 {
				b.WriteByte(',')
			}
			b.WriteByte('(')
			for c := range t.cols {
				if c > 0 {
					b.WriteByte(',')
				}
				b.WriteString("$" + strconv.Itoa(r*len(t.cols)+c+1))
			}
			b.WriteByte(')')
		}
		_, err := dtx.ExecContext(ctx, b.String(), batch...)
		batch = batch[:0]
		return err
	}
	for rows.Next() {
		vals, err := scanRow(rows, t)
		if err != nil {
			return TableReport{}, err
		}
		h.add(vals)
		batch = append(batch, vals...)
		if len(batch) >= batchRows*len(t.cols) {
			if err := flush(); err != nil {
				return TableReport{}, err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return TableReport{}, err
	}
	if err := flush(); err != nil {
		return TableReport{}, err
	}
	if h.n != want {
		return TableReport{}, fmt.Errorf("read %d rows, source count is %d", h.n, want)
	}
	return h.report(t.name), nil
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = quoteIdent(s)
	}
	return out
}
