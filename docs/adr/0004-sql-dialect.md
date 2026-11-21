# 0004. SQL dialect strategy

Status: Accepted

## Context
One codebase must behave identically on SQLite and Postgres without a heavy toolchain.

## Decision
- **Timestamps:** `BIGINT` Unix milliseconds UTC on both databases (not `timestamptz`), giving one schema and identical comparison semantics. Conversion to and from `time.Time` happens only in Go.
- **Query layer:** hand-written parameterised SQL in `internal/store` using `?` placeholders; `internal/db` rebinds them to `$n` for Postgres. No sqlc or other codegen, keeping the toolchain lean. Never string-build SQL from input.
- **Case-insensitive uniqueness/search** (email, username, area name): normalised lowercase columns, not collations.
- **Drivers:** `modernc.org/sqlite` (pure Go, keeps `distroless/static` and easy cross-compilation, no CGO) and `pgx/v5` via its `database/sql` stdlib adapter.
- **SQLite settings:** `auto_vacuum=INCREMENTAL` set at creation; on every connection `journal_mode=WAL`, `busy_timeout`, `foreign_keys=ON`, `synchronous=NORMAL`. `SQLITE_TMPDIR` under `/data/tmp` for the read-only root filesystem.
- **Migrations:** embedded in the binary; one set, with per-dialect files only where SQL differs.

## Consequences
- No dialect-specific time types; date arithmetic stays in Go (`internal/schedule`).
- Hand-written SQL means more review burden but no generator to maintain; dual-DB CI catches drift.
- Space reclamation uses bounded `incremental_vacuum`, never full `VACUUM`.
