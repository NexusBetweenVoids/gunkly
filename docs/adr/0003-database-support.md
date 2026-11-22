# 0003. Database support

Status: Accepted

## Context
Small self-hosters want one container and trivial backups; larger or public instances want Postgres.

## Decision
Support both, selected by configuration, with one schema and one migration set. SQLite is the default. CI runs the full suite against each with `go test -race`. `gunkly migrate-db` moves SQLite to Postgres: stopped instance, empty target (no tables, or only an empty `schema_migrations`), consistent copy, row-count and checksum verification, SQLite file left untouched. The table list and copy order come from the schema itself (`sqlite_master`, foreign keys), the source is opened read-only and read in one snapshot transaction, the target schema is created at the source's version and the data is written, verified and committed in one transaction (a failure leaves the target empty); a source with pending migrations is refused. `gunkly backup` takes an online SQLite snapshot (online backup API or `VACUUM INTO`) to stdout or inside `/data`; Postgres uses `pg_dump`. Migrations run automatically by default (Postgres advisory lock); pre-migration snapshots are SQLite-only; if auto-migrate is off and migrations are pending the server refuses to start until `gunkly migrate`. Migrations never roll back automatically; breaking changes use expand-and-contract.

## Consequences
- Every feature must work on both databases; tests double.
- Postgres operators own their pre-upgrade backups (no `pg_dump` in distroless).
- Multiple app containers are only supported on Postgres.
