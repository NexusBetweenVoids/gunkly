# AGENTS.md

Rules for any agent (human or AI) changing Gunkly. Read `docs/PRD.md` for scope and `docs/adr/` for decisions before starting.

## Stack

- Go (stdlib `net/http`), one binary: `cmd/gunkly`.
- SQLite (`modernc.org/sqlite`, pure Go) or Postgres (`pgx/v5` via `database/sql`).
- Svelte 5 + Vite SPA in `web/`, built to `web/dist` and embedded into the binary.
- Distroless static container, non-root, read-only root filesystem.

## Layout

| Path | Purpose |
|-|-|
| `cmd/gunkly` | Entry point, CLI subcommands (`serve`, `migrate`, `backup`, `admin ...`, `rotate-key`) |
| `internal/config` | Environment configuration |
| `internal/db` | Connections, dialect handling, embedded migrations |
| `internal/store` | **The only** data-access layer. Household-scoped methods require a household ID and a caller. |
| `internal/schedule` | Pure scheduling math (intervals, RRULE subset, DST, quiet hours). No I/O. |
| `internal/egress` | SSRF-guarded outbound HTTP client/dialer |
| `internal/auth` | Passwords (Argon2id), TOTP, token generation/hashing |
| `internal/api` | HTTP handlers under `/api/v1`. Never imports `database/sql`. |
| `internal/ids` | UUIDv7 public IDs, random tokens |
| `internal/limits` | All usage limits |
| `web/` | Front end; talks only to `/api/v1` |

## Commands

- `make check` — everything CI runs locally: fmt, vet, lint, deadcode, tests on SQLite **and** Postgres with `-race`, fitness tests, frontend checks/build. Must pass before a PR.
- `make test` — Go tests on SQLite only (fast loop).
- `make test-pg` — Go tests on Postgres; needs `GUNKLY_TEST_POSTGRES_URL`.
- `make web` — build the front end.

## Hard rules

1. Never add a dependency without approval; justify it in the PR and stay within binary/bundle size budgets.
2. Never commit secrets, real data, database files or `.env` files; fixtures are synthetic.
3. All household data access goes through `internal/store`; every new endpoint gets an isolation test and passes the architectural fitness checks.
4. Every feature ships with tests that pass on both SQLite and Postgres (`go test -race`); `make check` and CI must be green before merge.
5. Features go through the versioned API first; the UI never bypasses it; breaking `/api/v1` changes are forbidden.
6. No user-facing text in code: every string goes through the translation files (`web/src/lib/i18n/<locale>/<area>.json`, `core` for shell/shared text; server-sent text through `internal/i18n`).
7. Every new countable resource goes through `internal/limits`.
8. Use expand-and-contract migrations for breaking schema changes.
9. Small PRs, one task each. Update `docs/PRD.md` or ADRs in the same PR when behaviour or architecture changes. If a task looks like new scope, stop and ask.

## Conventions

- Timestamps: UTC, stored as `BIGINT` Unix milliseconds on both databases (ADR 0004).
- SQL: write `?` placeholders; `internal/db` rebinds for Postgres. Parameterised only, never string-built from input.
- Public IDs: UUIDv7 strings from `internal/ids`. Never expose sequential IDs.
- Secrets (tokens, mark-done links, recovery codes): store only SHA-256 hashes; compare in constant time.
- Never log passwords, tokens, notification URLs, VAPID keys or session IDs.
- Errors returned to clients are generic; details go to the log (minus secrets).
- README badges must stay accurate: when a change affects what a static badge states (license, framework version, supported databases, container), update the badge in the same PR. At the first release, add release-version and container-image badges. `make fitness` checks the Svelte badge.
- Prefer the standard library. Small, boring code over abstractions. Delete dead code.
