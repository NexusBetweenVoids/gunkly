# Changelog

All notable changes are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Security
- Rate limiter: IPv6 clients are keyed by /64; eviction only drops idle keys not in backoff and a full table refuses new keys (429); it follows the admin-set `login_attempts_per_minute`.
- Authenticated write throttling: per-user and per-household token buckets; new limits `areas_per_household` (100) and `completions_per_chore` (1000, oldest non-reverted completions pruned).
- Admin password reset: disabled in hosted mode, refused for admin targets, revokes sessions, recorded in the target's own audit log; ADR 0009 and the threat model state the self-hosted trade-off. Password changes are now one transaction (password, sessions, mark-done links).
- `households_per_user` and invite use are enforced atomically; invites have `maxUses` (1-100, default 1, new migration 0005) and registration answers `registration_failed` for duplicate username/email.
- Sign-in no longer grants step-up; enabling/disabling TOTP revokes other sessions; per-account failed password/2FA backoff across IPs.
- Mark-done: two links for one occurrence cannot both complete the chore (chore row lock).
- SQLite database, WAL/SHM and backup files are created 0600; compose files recommend pinning the image version.
- Redirect hardening in `safeNext` and the service worker (same-origin paths only); channel hints no longer contain secret characters.

### Added
- Data export and import: `GET /api/v1/account/export` (personal data, step-up, attachment), `GET /api/v1/households/{hh}/export` (member+), `POST /api/v1/households/import` (new household) and `POST /api/v1/households/{hh}/import` (merge, member+). Versioned formats `gunkly-user-export` and `gunkly-household-export` (version 1); strict validation with the normal create-path rules, reported as `import_invalid` with a `reason` and field `path`; limits checked; one transaction; 5 MiB upload cap on the import routes only (1 MiB elsewhere); audit events; Account and household settings screens; `FuzzImport` in the nightly fuzz matrix.
- `gunkly migrate-db --from sqlite:///... --to postgres://...`: copies SQLite into an empty Postgres database with per-table row-count and checksum verification, refuses a non-empty target or a source with pending migrations, rolls back on any mismatch, leaves the SQLite file untouched. README "Switching later" documents the steps.
- Admin-configurable session lifetimes (`sessionIdleDays` 1-365, `sessionMaxDays` 1-730) and data retention (`auditRetentionDays`, `deliveryRetentionDays`) in `GET/PATCH /api/v1/admin/settings` and the admin settings screen; the retention job reads them on each run (the `scheduler.Config.Retention` field is gone). ADR 0013 (sessions, CSRF, rate limiting, setup code).
- CLI tests for `healthcheck`, `admin reset-password`/`reset-2fa`, `rotate-key`, `backup -` and `migrate`; a test keeps `.env.example` in step with the config keys. README quick start, upgrade and backup steps corrected to match the code.
- CI/release automation: Playwright e2e job; OpenAPI breaking-change check on PRs (`oasdiff`, `make openapi-diff`); image size budget (40 MB); frontend fitness checks (`make fitness`); dependency count budgets (`make dep-budget`); fuzz matrix coverage check; nightly OWASP ZAP baseline DAST (`dast.yml`); tag-driven release workflow with multi-arch image, cosign keyless signature, SPDX SBOM attestation, binaries and `SHA256SUMS`. CI uses Node 26.
- HTTP server and `/api/v1` auth API: first-run setup code, sessions with CSRF protection, step-up, TOTP with recovery codes, rate limiting, households, members, invites, audit log, admin users/settings; `/healthz`, `/readyz`; embedded SPA with strict security headers.
- Chore API (`/api/v1/households/{hh}/...`): areas (list/create/rename/delete; opt-in starter areas via `starterAreas` on household creation), chores (list with due status buckets `overdue`/`today`/`thisWeek`/`later` in the household zone and the user's week start, `mine`/`area`/`archived` filters, create/get/update/archive/unarchive/delete with server-side validation and `ChoresPerHousehold`), completions (complete with undo path, paginated history, revert), snooze, skip once, pause/resume (each invalidating mark-done links), per-chore reminders and presets (`RemindersPerChore`, household default preset), per-user mutes, calendar occurrences (max 62 days) and `GET /api/v1/templates`. Audit events for chore delete, skip and revert.
- `api/openapi.yaml` documents every `/api/v1` operation; a test fails when a registered route is missing from it or the spec lists a route that does not exist.
- CLI: `serve`, `healthcheck`, `migrate`, `backup`, `admin reset-password`, `admin reset-2fa`, `rotate-key`.
- Reminder scheduler (`internal/scheduler`): per-tick reminder windows in the household zone, quiet-hours deferral and coalescing, mutes, guest exclusion, idempotent delivery keys, retries with exponential backoff, one catch-up per chore after downtime, notification quotas through `internal/limits`, daily retention cleanup with bounded SQLite `incremental_vacuum`, Postgres advisory-lock leadership with standby takeover; started by `serve` unless `GUNKLY_DISABLE_SCHEDULER`.
- Notification channels (`internal/notify`, store): service allowlist (fixed public providers on by default, host overrides rejected), URLs sealed per channel ID, `rotate-key` support, plain-text messages from `internal/i18n`, delivery log. Delivery via Shoutrrr v0.25.0 through the egress guard (Go 1.27 required).
- Mark-done links (store): hashed single-use tokens scoped to an occurrence and a recipient or household channel, 7-day maximum, side-effect-free peek, one-transaction completion with a 5-minute single-use undo token.
- iCal feeds (`internal/ical`, store): per-user, per-household revocable/regenerable tokens; names, areas and dates only.
- `internal/i18n` for server-sent text.
- `internal/limits` service with the PRD defaults, instance and per-household plan overrides.
- Project documentation: PRD, architecture decision records, threat model, security policy and contributing guide.
