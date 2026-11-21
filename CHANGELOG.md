# Changelog

All notable changes are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added
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
