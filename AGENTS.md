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
| `internal/auth` | Passwords (Argon2id), TOTP, token generation/hashing, passkey relying-party derivation |
| `internal/api` | HTTP handlers under `/api/v1`. Never imports `database/sql`. |
| `internal/ids` | UUIDv7 public IDs, random tokens |
| `internal/limits` | All usage limits |
| `internal/scheduler` | In-process reminder worker: due reminders, delivery with retries, daily retention |
| `internal/notify` | Notification channel URL validation (Shoutrrr), sealing, rendering, sending |
| `internal/sso` | OpenID Connect single sign-on (discovery, PKCE authorization URL, code exchange, ID token verification); self-hosted only (ADR 0018) |
| `internal/webhook` | Outgoing webhooks: URL validation, sealed signing secret, HMAC signature, guarded sender (ADR 0017) |
| `internal/webpush` | Web Push sending (VAPID, RFC 8291 encryption) |
| `internal/mail` | Optional SMTP transactional email |
| `internal/transfer` | Portable export/import file formats |
| `internal/ical` | Read-only iCalendar feeds |
| `internal/i18n` | Server-sent text (notifications, email, feeds) |
| `internal/metrics` | Hand-written Prometheus text exporter |
| `internal/updates` | Optional release check |
| `web/` | Front end; talks only to `/api/v1` |

## Commands

- `make check` — everything CI runs locally: fmt, vet, lint, deadcode, dep-budget, fuzz-check, tests on SQLite **and** Postgres with `-race`, fitness tests, frontend checks/build/budget, e2e. CI additionally runs govulncheck, CodeQL and the Docker/upgrade jobs. Must pass before a PR.
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
- README badges must stay accurate: when a change affects what a static badge states (license, framework version, supported databases, container), update the badge in the same PR. `make fitness` checks the Svelte badge.
- Prefer the standard library. Small, boring code over abstractions. Delete dead code.

## Releases

Only after the user approves. Before tagging:

1. Move `[Unreleased]` in `CHANGELOG.md` to the new version and date.
2. If the UI changed, run `cd web && npm run screenshots` (rewrites `docs/assets/screens/*.png` with demo data), look at every image and commit them.
3. Check the docs (`README.md`, `docs/*.md`) still name screens and settings the way the app does.

After the release is published, update the website (`../website`, local only): `npm run sync -- vX.Y.Z` pins its docs to the new tag and copies the screenshots, then `npm run build && npm run check`, look at the pages and commit there. Never deploy the website without the user.
