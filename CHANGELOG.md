# Changelog

All notable changes are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Changed

- Transactional email (verification, password reset, sign-in codes, notices) goes through a persisted outbox with retries instead of a fire-and-forget send (ADR 0008, migration 0017): a failed send is retried after 1 minute, 5 minutes, 30 minutes, 2 hours and 6 hours, then given up with an audit event `mail_gave_up` (no address or text). Queued mail survives restarts, is sealed at rest and deleted once sent or given up, never outlives the code or link it carries, and the email caps are counted when a message is queued, not per retry.

### Added

- **Take turns** (rotating assignees, PRD "Chores & scheduling"): a recurring chore with 2+ assignees can rotate in the assignee order (editor: "Take turns" with up/down controls). The chore has a current-turn person: Home shows a "Sam's turn" badge and "Mine" means my turn (or unassigned), reminders go to the person whose turn it is only (household channels are unchanged), and completing passes the turn to the next person (option "move on only when the person whose turn it is does the chore"). Undo gives the turn back; skip and snooze keep it; a member who leaves, deletes their account or becomes a guest leaves the rotation. The chore page shows the turn and lets you change it ("It's Sam's turn now").
- API (additive): chores carry `rotation`, `advanceOn` (`any`|`assignee`) and `turnUserId`; `PUT` accepts `rotation` and `advanceOn`; new `PATCH /households/{hh}/chores/{chore}` with `turnUserId` (scope `chores:complete`). MCP `list_chores`/`get_chore` show `takesTurns`, `turnUserId` and `myTurn`, and `mine` follows the turn. Household export and import carry the rotation (`rotation`, `rotationAdvance`, `turn`; assignee order is kept). Migration 0016.

## [0.3.0] - 2026-11-23

### Added

- OAuth 2.1 sign-in for MCP clients (ADR 0016), so ChatGPT custom connectors and Claude's "Add custom connector" connect by address and "sign in with Gunkly". Optional and off by default: the new instance setting `oauthEnabled` (`GET/PATCH /api/v1/admin/settings`, Administration, Instance settings) turns it on and needs `GUNKLY_BASE_URL` to be https (or http://localhost for development), otherwise `400 oauth_requires_https`. While it is off, every OAuth endpoint and metadata document answers 404, nothing is advertised and `/mcp` keeps its plain `Bearer` challenge.
- When on: RFC 9728 and RFC 8414 metadata (`/.well-known/oauth-protected-resource`, `/.well-known/oauth-authorization-server`), dynamic client registration for public clients (`POST /oauth/register`; https or loopback redirect URIs, exact match, capped and rate limited, clients never approved purged after one hour, capped per address), a server-rendered consent page (`/oauth/authorize`, no scripts, PKCE S256 required, RFC 8707 `resource`, apps labelled unverified) where the user picks households and actions, `POST /oauth/token` (authorization code and rotating refresh tokens; reuse revokes the grant) and `POST /oauth/revoke` (RFC 7009). Access tokens (`gko_`, one hour) work on `/mcp` only, with the scopes, household binding, live roles and rate limits of API tokens; completions are recorded with the new source `oauth` and the grant id (chore history names the connected app; `GET .../completions` returns `via`; the export and import format accept `oauth`).
- Account, Integrations lists **Connected apps** with Disconnect (`GET /api/v1/account/oauth-grants`, `DELETE /api/v1/account/oauth-grants/{id}`); the assistant guide gains ChatGPT and Claude (connector). Password changes and second-factor removal disconnect all apps; audit events `oauth_grant`, `oauth_token_reuse`, `oauth_revoke`.
- New limits `oauth_clients` (instance, default 1000) and `oauth_grants_per_user` (default 20); migrations 0013 and 0014 (the latter widens the completions source check on both databases and stores the registering address key for the per-address cap).

### Changed

- The total JavaScript budget (lazy screens included) is now 200 KB gzipped; the first-load budget stays 45 KB and CSS 15 KB.
- "Remind me at" sits next to Reminders and no longer requires More options. It now also shows when a chore's custom reminders are at different times (setting a time moves them all).

### Security

- OAuth consent now requires a step-up: unless the session re-verified in the last 15 minutes, the consent page asks for the password (plus the authenticator code, an emailed code via an "Email me a code" button, or a recovery code), with the same verifier, backoff and rate limits as the step-up endpoint; failures are audited. A stolen session alone can no longer approve an app. "Deny" never asks.
- OAuth client registration is harder to exhaust: unused clients are purged after one hour (was 24), one address (IPv6 /64) can hold 20 never-approved clients (new limit `oauth_unused_clients_per_ip`), and at the instance cap the oldest unused client is evicted instead of refusing everyone. A client is kept only when a user approves it or a token is issued, no longer when its consent page is merely opened.
- The consent page shows the return host as its largest line, warns when this user never approved an app returning to that host, and registration refuses client names containing "gunkly" (Unicode-folded) so an app cannot impersonate Gunkly.
- OAuth completions are attributable: they carry the source `oauth` and the grant id instead of `api_token`.
- Documented that browser-based (CORS) MCP clients are not supported (ADR 0016, `docs/ai-assistants.md`).

## [0.2.3] - 2026-11-22

### Added

- Set the reminder time for each chore: the chore editor has a "Remind me at" time field next to the first due date. Every reminder of the chosen preset (day before, on the day, overdue repeats) then fires at that time in the household's time zone; leave it alone to keep the preset's usual times. Reopening the chore shows the chosen time and the preset; chores with custom reminders at different times show "Different times: see More options" and keep them. No database change: the time is stored as the chore's own reminders.
- API: `POST` and `PUT /api/v1/households/{id}/chores` accept an optional `reminderTime` (`HH:MM`, household time zone; `00:00` to `23:59`). The preset's reminders (or the chore's custom ones) are stored with that time and `reminderPreset` reads `custom`; nothing changes when it is left out or the chore has no reminders. The MCP `create_chore` tool takes the same as `remind_at`.

## [0.2.2] - 2026-11-22

### Fixed

- Turning on reminders failed with "Some of the information isn't valid" on iPhone (Home Screen app) and in every other browser: the browser's subscription includes `expirationTime`, which the strict API decoder rejected as an unknown field. `POST /api/v1/account/push-subscriptions` now accepts and ignores it, the app sends only the endpoint and keys, and asks for notification permission first thing in the tap (iOS needs the tap's user activation). An unsupported push service or unusable keys now get their own messages.

## [0.2.1] - 2026-11-22

### Added

- Two-factor setup shows a QR code (with the Gunkly mark in the middle) to scan with an authenticator app; the secret, now grouped in fours, and the otpauth link stay available to copy. The encoder is small and dependency-free.
- Administration warns when requests arrive with `X-Forwarded-For`, `Forwarded` or `X-Real-IP` from a direct peer that is not in `GUNKLY_TRUSTED_PROXIES` (the usual sign of a reverse proxy that was never listed): a banner explains the effect on client IPs and rate limits and offers the exact `GUNKLY_TRUSTED_PROXIES=...` line to copy. New admin-only `GET /api/v1/admin/health` returns `untrustedProxies` (up to 5 peers seen in the last 24 hours, kept in memory, never logged, never trusted automatically).
- README section "Behind a reverse proxy" with Caddy, nginx, Traefik and Nginx Proxy Manager examples.
- Updates page: "Check now" button (`POST /api/v1/admin/updates/check`) and a "Checking for updates…" state. `GET /api/v1/admin/updates` gains `checking` and `ahead`.

- Email as a reminder channel: where the server sends email, Account, Notifications has an "Email me reminders" switch (needs a confirmed address; turned off when the address changes). `PATCH /api/v1/me` accepts `emailReminders`; `GET /api/v1/me` returns it. Reminders by email carry the same text and mark-done link, and follow the same recipients, mutes, quiet hours, quotas and delivery log as other channels.
- Home offers reminders on this device in one tap ("Get reminders on this device?", with "Not now" remembered per device) to people with no other personal reminder route; guests and browsers without push never see it. On iPhone and iPad, Account explains the Add to Home Screen steps.
- Household settings explain what a household channel (for example a family Telegram or Discord group) does.

### Changed

- New households default to the "Gentle" reminder preset (it was none). Existing households are unchanged.
- Account, Notifications is reordered for non-technical members: "This device" first with a clear on/off, then "Email me reminders", then "Messaging apps (advanced)", collapsed by default.
- The client address is also read from the standard `Forwarded` header (RFC 7239), only from trusted proxies and with the same right-to-left walk as `X-Forwarded-For`; `Forwarded` wins when both are present, and an obfuscated, `unknown` or malformed hop ends the walk at the last trusted hop.
- Creating another household moved out of the header and menu into Settings (Households section); the header shows the household name as plain text when you belong to one household and a switcher only with two or more. With no household, Home shows a prominent "Create your first household" button.

### Fixed

- After an upgrade the Updates page could say "You are up to date. Latest release: v0.1.1" because the cached check predated the release. The page now refreshes the check in the background when the cache is missing, over an hour old or older than the running version (at most one GitHub request per 5 minutes), and never claims "up to date" while the cached release is older than the running version.

## [0.2.0] - 2026-11-22

### Added

- Guided "Connect an AI assistant" setup under Account, Integrations: pick Claude Code, Claude Desktop, Gemini CLI or another MCP app and get the exact setup with your address and key filled in. Plain-language guide in `docs/ai-assistants.md`.
- Home Assistant integration (separate repository `gunkly-homeassistant`): to-do lists, sensors, calendar and actions, using API tokens.

- API tokens for integrations such as Home Assistant and AI assistants (MCP): create them under Account, Integrations. Each token is bound to the households you pick and to the permissions you choose (`chores:read`, `chores:complete`, `chores:write`), never exceeds your own role, is shown once, can expire and can be revoked at once. Send it as `Authorization: Bearer gk_...`; `GET /api/v1/token` describes it. Completions made with a token are recorded as such. See ADR 0014 and the `bearerAuth` operations in the OpenAPI document.
- New limit `api_tokens_per_user` (default 20).
- Built-in MCP server for AI assistants (Claude Code, Claude Desktop and other MCP clients) at `/mcp` (Streamable HTTP, stateless), authenticated by an API token. Tools: `list_households`, `list_chores`, `get_chore`, `upcoming` (`chores:read`), `complete_chore`, `undo_completion`, `snooze_chore`, `skip_chore` (`chores:complete`) and `create_chore` (`chores:write`), each limited to the token's households, scopes and your role. New admin setting `mcpEnabled` (default on; off answers 404) in Administration and `/api/v1/admin/settings`. Adds the official Go SDK `github.com/modelcontextprotocol/go-sdk` (binary about 2 MB larger). See ADR 0015 and the README.

### Security

- API tokens are now revoked when the account's password is changed or reset (account, administrator, `gunkly admin reset-password` or email) and when a second factor is removed (including `gunkly admin reset-2fa`); the audit log notes how many.
- `/mcp` refuses JSON-RPC batches, which could run many tool calls for one rate-limit charge.
- A token's household binding is deleted when its owner leaves or is removed from the household, so rejoining no longer revives old tokens.
- The MCP instructions and tool descriptions now call all names and text fields (not only chore names and notes) untrusted data.

### Changed

- Opening Gunkly at an address other than `GUNKLY_BASE_URL` (or over http when it is https) now says so in the app and logs a warning naming both addresses, instead of a generic "session expired".

## [0.1.1] - 2026-11-22

### Changed

- **Breaking:** the default port is now 4865 (was 8080). If you used the 0.1.0 compose example, change the port mapping to `"4865:4865"` (or keep your host port: `"8080:4865"`) and update `GUNKLY_BASE_URL`. To keep the old port inside the container, set `GUNKLY_LISTEN_ADDR=:8080`.
- Images are also published on Docker Hub as `gunkly/gunkly`.

### Security

- Outgoing mail is sanitised at the point of sending: plain ASCII addresses only, control characters and Unicode direction overrides removed from subject and body, line breaks in subjects refused.

## [0.1.0] - 2026-11-22

The first public release.

### Added

**Chores and scheduling**
- Chore API under `/api/v1/households/{hh}/...`: areas (with opt-in starter areas), chores (interval and RRULE-subset schedules, assignees, notes, link, archive, delete), completions with undo and revert, paginated history, snooze, skip once, pause and resume, per-user mutes, starter templates, and calendar occurrences (up to 62 days).
- Home buckets chores as overdue, today, this week and later in the household time zone and the user's week start.
- Limits through `internal/limits` with the PRD defaults and per-instance and per-household overrides, including `areas_per_household` (100) and `completions_per_chore` (1000, oldest non-reverted pruned).

**Reminders and notifications**
- Reminder scheduler: per-chore reminder windows in the household zone, presets, quiet-hours deferral and coalescing, mutes, guest exclusion, idempotent delivery keys, retries with backoff, one catch-up per chore after downtime, quotas, daily retention cleanup, Postgres advisory-lock leadership with standby takeover. `GUNKLY_DISABLE_SCHEDULER` runs a web-only instance.
- Notification channels per user and per household through Shoutrrr (egress-guarded), with a service allowlist, sealed URLs (covered by `rotate-key`), plain-text messages from `internal/i18n` and a delivery log.
- Web Push (ADR 0006): RFC 8291/8292 in the standard library, VAPID key generated on first use and sealed, subscription endpoints, limit `push_subscriptions_per_user` (10), stale subscriptions pruned; "Enable notifications on this device" in Account; the service worker shows the notification and opens its same-origin URL.
- Mark-done links: hashed single-use tokens with a 7-day maximum, side-effect-free preview page and a 5-minute single-use undo.

**Calendar**
- Month and week calendar screen; per-user, per-household revocable iCal feeds with names, areas and dates only.

**Accounts, households and invites**
- First-run setup code, sessions with CSRF protection and step-up, TOTP with recovery codes, households, roles (owner, member, guest), expiring invites with `maxUses` (1-100), audit log, admin users and settings, session lifetimes and data retention settings (ADR 0013).
- Email (ADR 0008): SMTP sender with caps, address verification, password reset, email-code 2FA; `gunkly admin reset-2fa` clears email codes too.
- Open sign-up with an ALTCHA-compatible proof of work (`GUNKLY_SIGNUP_POW_MAX`), public `GET /instance`, admin settings `openSignup` and `requirePow`.
- Account deletion with ownership hand-over (`GET /account/deletion-preview`, `DELETE /account`); optional inactive-account lifecycle (off by default) with warnings and deletion.

**Export, import and migration**
- Versioned user and household export (`gunkly-user-export`, `gunkly-household-export`), import as a new household or merge, strict validation, all-or-nothing, 5 MiB cap on import routes only, 20,000 completions per file.
- `gunkly migrate-db --from sqlite:///... --to postgres://...`: copies SQLite into an empty Postgres database with per-table row-count and checksum verification; the SQLite file is untouched.

**Admin and operations**
- Admin Updates: running version and latest release with changelog from the GitHub release API (at most every 12 h, egress-guarded, generic user agent, no instance data; can be switched off).
- Prometheus `/metrics` behind `GUNKLY_METRICS_TOKEN` and a bearer token, no user or household labels.
- CLI: `serve`, `healthcheck`, `migrate`, `migrate-db`, `backup`, `admin reset-password`, `admin reset-2fa`, `rotate-key`, `version`. `/healthz` and `/readyz`.
- Synthetic canary (`scripts/canary.sh`) and an upgrade test (`scripts/upgrade-test.sh`).

**Web app**
- Svelte SPA embedded in the binary: home, calendar, chore editor, household settings, account, admin, sign-up, password reset and email verification; installable PWA; light and dark themes; English with i18n and RTL wiring; WCAG 2.2 AA checked in e2e. JS budget 94 KB gzipped.

**Project**
- `api/openapi.yaml` documents every `/api/v1` operation and a test keeps it in step with the routes; a test keeps `.env.example` in step with the config keys.
- CI and release automation: dual-database tests, Playwright e2e, OpenAPI breaking-change check, size and dependency budgets, fuzzing, nightly ZAP baseline, tag-driven release with multi-arch image, cosign signature, SPDX SBOM, binaries and `SHA256SUMS`.
- Documentation: README, getting-started guide, PRD, ADRs, threat model, security policy, and contributing guide.

### Security
- Hosted mode: every household-scoped route requires a verified email and a second factor, enforced once in the router (`403 email_not_verified` / `two_factor_setup_required`); creating or joining a household needs both; 2FA cannot be turned off. Import goes through the same gate as creating a household.
- Mail quotas: sign-in and step-up codes have their own per-account limit (20/h); unauthenticated reset, verification and sign-up mail count only against per-IP and global caps, so nobody can lock another account out of its codes. Password reset keeps up to 3 unexpired single-use tokens.
- Sign-up answers `202` whether or not the address has an account; reset requests do their work in the background (no timing oracle). Sign-up checks the per-IP cap first and marks the proof of work used before hashing; the proof of work is a nuisance filter, the per-IP and mail caps are the real controls (ADR 0008).
- Rate limiting: IPv6 clients keyed by /64, bounded table that refuses new keys when full, per-account failed password and 2FA backoff across IPs, per-user and per-household write throttling. Sign-in does not grant step-up; enabling or disabling TOTP revokes other sessions.
- Atomic enforcement of `households_per_user`, invite use and the chore limit during merge import (row lock on Postgres); two mark-done links for one occurrence cannot both complete a chore.
- Admin password reset is disabled in hosted mode, refused for admin targets, revokes sessions and is recorded in the target's audit log (ADR 0009). Password changes are one transaction.
- Imported completions use the source `import` and are attributed to a named member only when the importer owns the target household.
- Account deletion strips actor, IP and metadata from audit rows, including other rows that mention the user. Unverified, household-less accounts are deleted after 7 days when email is enabled.
- SQLite database, WAL/SHM and backup files are created `0600`; redirects (`safeNext`, service worker) are same-origin paths only; channel hints never contain secret characters.
- Secrets (tokens, mark-done links, recovery codes) are stored only as hashes; sealed values (TOTP, channel URLs, push keys) use a versioned keyring and `rotate-key`.

### Changed
- Home shows the "Everyone / Mine" switch only when a chore is assigned to someone else; otherwise every chore is shown.
- New chores start with the assignees chosen on your previous new chore in that household (remembered on the device).
- README, quick start, upgrade and backup steps rewritten to match the code; `docs/PRD.md` names `GUNKLY_DISABLE_SCHEDULER` (there is no `--disable-scheduler` flag).
- Areas are now an optional per-household setting, off by default (`areasEnabled` on the household; Settings, "Group chores by area"). When off, the UI hides areas and calendar feeds omit categories; areas and chore links are kept. The API stays compatible (additive field; `areaId` is kept on update when omitted).
- Compose files recommend pinning the image version.
- Registration answers `registration_failed` for a duplicate username or email.
- Self-hosted instances never require an email address or verification, with or without SMTP: sign-up takes an optional email, signs the user in at once and sends a verification mail only if an address is given (it matters for password reset by email and opt-in email-code 2FA). The verified-email gate, the "check your email" sign-up, mandatory 2FA, the unverified-email banner and the 7-day purge of unverified accounts apply to hosted mode only.
