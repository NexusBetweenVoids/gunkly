# Gunkly Product Requirements

Source of truth for scope and behaviour. Check every task against this file; update it in the same PR when behaviour changes. Decisions and rationale live in `docs/adr/`.

## Purpose

Gunkly tracks recurring household chores and home maintenance (gutters, washing-machine filter, descaling the kettle) and reminds the right people at the right time. AGPL-3.0, self-hostable with Docker, also run by the maintainer as a free, non-commercial hosted service. Security is designed for the hosted multi-tenant case from day one.

## Scope

**Is:** a recurring-chore tracker for households with flexible schedules, optional reminders on the user's own messaging services, and a calm, Apple-like UI on phone and desktop.

**Is not:** a general to-do app, project manager, home inventory, shopping list or gamified allowance app. New features are checked against this scope; if in doubt, stop and ask.

| Mode | Who runs it | Notes |
|-|-|-|
| Self-hosted | Anyone, with Docker | Same features as hosted; nothing locked away |
| Hosted | The maintainer, non-commercially | Public code unmodified; only configuration and infrastructure differ |

### Principles

1. Secure by default, designed for the hosted multi-tenant case.
2. Minimal dependencies; each one justified.
3. One app container and one data volume (plus Postgres if chosen).
4. API-first: the web UI uses the same versioned API (`/api/v1`) a future mobile app will use.
5. Calm UI: few screens, clear status, no clutter.
6. No telemetry. The only self-initiated phone-home is the optional update check. Notifications, Web Push and SMTP connect only to services configured by the user or admin.

## Requirements

### Accounts, households and roles

- A **household** owns and isolates all chores, areas and history. A user can belong to several households and switch between them.
- Roles: **owner** (members, settings, deletion), **member** (manages and completes chores), **guest** (view only; no reminders, cannot complete chores, including via notification links). No child accounts.
- Joining is via **invite links** that expire and can be revoked. Open sign-up (`POST /api/v1/signup`, with an ALTCHA-compatible proof of work, `require_pow`) is an instance setting: off by default self-hosted, on for hosted. Self-hosted accounts are username and password (TOTP optional) and never require an email address or verification, with or without SMTP: sign-up takes an optional email (used for password reset by email and email-OTP once verified), creates the account and signs the user in at once. Hosted mode requires an email, verified by link: unverified users can sign in and see a banner but cannot create or join households, import data or reach any household data (invite registration included: the gate, verified email plus 2FA, applies to every household route), and sign-up answers the same 202 ("Check your email to finish signing up") whether or not the address already has an account and signs nobody in. A valid invite still lets an invitee register when open sign-up is off, unless an admin restricts registration to admin-created accounts.
- Sign-in: email (or username when email is disabled) plus password, optional **TOTP** with single-use recovery codes. Users with a verified email may choose email one-time codes instead of TOTP. Hosted: 2FA mandatory (TOTP or email one-time code; verifying the address enrols email codes automatically); users are warned that hosted has no manual admin reset if authenticator and recovery codes are both lost. Self-hosted admins can run `gunkly admin reset-2fa <user>`. **Passkeys** (WebAuthn; ADR 0018, `docs/sign-in.md`): register them under Account, Sign-in & security (name, created, last used; step-up to add or remove; at most 20 per user). A passkey signs in without a password (user verification required, so it counts as password plus second factor, also for the hosted 2FA rule), works as a second factor after a password next to TOTP and email codes, and can re-verify a session for step-up. They need `GUNKLY_BASE_URL` to be https (or `http://localhost`); attestation is `none`; a sign count that does not increase is refused and logged. **SSO** (OpenID Connect; ADR 0018, `docs/sso.md`) is self-hosted only (404 in hosted mode): configured by `GUNKLY_OIDC_*`, authorization code with PKCE, `(issuer, sub)` identifies the user (never the email, never auto-linked); users link or unlink it under Account (step-up; unlink only while a password or passkey remains); an admin setting (Administration, Sign-up & sign-in; default off) lets unknown SSO users get an account; the identity provider's own MFA counts as the second factor. Password sign-in stays available.
- The **instance admin** manages users, limits and instance settings but cannot read household content (chores, notes, history, notification settings) through the app.
- **Leaving a household / deleting an account:** chores assigned only to that person become unassigned ("anyone"); they are removed from other chores' assignees; past completions stay but are anonymised (`user_id` NULL, shown as "Deleted user"). When a household's last owner leaves, ownership transfers only to non-guest members; guests are never promoted. A household with no non-guest members left is deleted, and the app warns and asks confirmation first.
  - `GET /api/v1/account/deletion-preview` lists the households that would be deleted and those where the caller is the last owner (with the non-guest candidates, longest-standing first). `DELETE /api/v1/account` (step-up) needs the body `{"confirmUsername": <own username>, "newOwners": {<household>: <user>}}`; a missing successor answers `409 owner_required` with the households (nothing is changed). Leaving a household is `DELETE /households/{hh}/members/{self}` with the optional body `{"newOwner", "confirmDelete"}` and the same `409 owner_required`; deleting the household by leaving as its only (non-guest) member needs `confirmDelete` (`409 household_would_be_deleted`).
  - Deletion is one transaction: ownership hand-over, deletion of households without a non-guest member, removal of sessions, tokens, recovery codes, personal channels, push subscriptions, feeds, mark-done links and the user's deliveries; audit rows lose actor, IP and metadata (also where another row's metadata names the user). Only user-initiated requests need an explicit choice; system processes (the inactive-account job) hand ownership to the longest-standing non-guest member. The only instance admin cannot be deleted (`409 last_admin`).
- **First-run setup:** one-time screen to create the instance admin, gated by a setup code printed in the container logs; disappears once used.
- **Admin CLI recovery:** `gunkly admin reset-password <user>` and `gunkly admin reset-2fa <user>` (TOTP, email-OTP and passkeys), run via `docker exec`.
- **Sessions:** users list signed-in devices, sign out one or all. Sliding expiry default 30 days inactivity, 90-day absolute max (admin-configurable: 1-365 idle, 1-730 absolute days). Sensitive actions (password/email/2FA change, API token creation, account deletion) need step-up verification within 15 minutes.
- Each household has an IANA **time zone**, defaulting to the creator's browser zone. Timestamps are stored in UTC; reminder times use the household zone.

### Chores and scheduling

- **Recurring or one-off.** Editor defaults to recurring; one-off is under "Does not repeat" and is auto-archived on completion.
- Recurrence: **interval** (every N days/weeks/months) or **calendar rule** (e.g. 1st Saturday of the month, every April and October) stored as a documented RRULE subset (ADR 0005).
- Next due date is computed **from completion** (default for interval) or **from scheduled date** (default for calendar); changeable per chore.
- Edge cases: completing a multi-cycle-overdue "from scheduled" chore jumps to the next upcoming occurrence (no backlog); completing early cancels pending reminders for the current cycle; monthly on 29/30/31 falls on the month's last day; a nonexistent DST local time is sent at the next valid time.
- **Assignees:** zero ("anyone"), one or many household members. One completion by anyone completes the chore for everyone.
- **Take turns (rotating assignees):** a recurring chore with 2+ assignees can rotate. The assignee order is the rotation order (editor: up/down buttons) and the chore has a current-turn person. Home "Mine" shows a rotating chore only to the person whose turn it is (otherwise assigned to me or to anyone), and a Home row shows a "Sam's turn" badge. Reminders go to the current-turn person only (household channels are unchanged). On completion the turn advances to the person after whoever completed it, in order and wrapping (completed by someone outside the rotation or through a household channel: after the person whose turn it was); the option "advance only when the person whose turn it is completes it" (off by default) leaves the turn unchanged when anyone else completes. Reverting the latest completion gives the previous turn back (unless that person has left the rotation). Skip and snooze keep the same person (the occurrence moves, nobody's turn was used). "It's <name>'s turn now" (chore page, Change; `PATCH` with `turnUserId`) sets the turn by hand. A member who leaves, deletes their account or is demoted to guest is removed from the rotation and the turn moves on to the next person; a rotation with fewer than 2 people left is switched off and the remaining person stays the plain assignee. Editing the assignees keeps the turn while that person is still assigned, otherwise the first assignee takes it. One-off chores cannot rotate. API additions (additive): `rotation`, `advanceOn` (`any`|`assignee`), `turnUserId`, `turnName` (display name of the person whose turn it is, null otherwise; API tokens cannot list members, so integrations read the name here); MCP `list_chores`/`get_chore` return `takesTurns`, `turnUserId`, `turnName`, `myTurn` and `mine` follows the turn; export and import carry `rotation`, `rotationAdvance` and `turn` (assignees keep their order; import keeps the rotation only when 2+ assignees match members).
- **Snooze** (move this occurrence), **skip once**, **pause** (until a date or indefinitely) on every chore.
- Fields: name, optional area, notes, optional link (`http`/`https` only, enforced on server and client).
- **Simple editor:** name, area, assignees, first due date, repeat, reminder preset, reminder time ("Remind me at": the preset's reminders at the chosen time, stored as the chore's own reminders; API `reminderTime`, MCP `remind_at`). **Advanced editor:** every option.
- **Completion record:** who, when, source (`app`, `mark_done_link`, `api_token`, `oauth` for a connected app, `import`; token ID in audit log), optional note. History kept until the chore is deleted.
- **Undo:** shown right after completing (app or mark-done page); owners and members can revert any completion later from history. Revert marks the entry reverted (kept for audit), restores the previous due date and reschedules reminders. On the unauthenticated mark-done page, `POST /mark-done` consumes the single-use token and returns a 5-minute single-use undo token scoped to that completion, so Undo needs no session.
- **Archive** instead of delete: keeps history, stops reminders. Delete remains possible.
- **Templates:** starter chore templates and starter areas.

### Reminders and notifications

- Optional. Per chore: **before due**, **on due date**, **repeating while overdue** (at most once per 24 h per chore), each at a time of day.
- Household **presets** ("Gentle", "Nag me"; a new household defaults to Gentle, existing households keep their setting) and household-level **quiet hours**; members can mute a chore. Reminders in quiet hours are deferred to the end and coalesced to one per chore, never dropped. **Personal quiet hours** (Account, Notifications, "Quiet hours"; `PATCH /me` `quietHours` `{start,end}` or `null`, session only; also in the account data export): the same window semantics as the household's (minutes after local midnight, wraps past midnight), read in the user's own time zone when set (Account, Profile, "Time zone"; `PATCH /me` `timeZone`, an IANA name validated server-side, `null`/empty = follow the household; session only; also in the account export), otherwise in each household's time zone (so someone whose households are in different zones should set one). The zone is also what "today" means in the web app for that user (chore status overdue/today/this week, the Calendar's today; sessions only, never API tokens or MCP, so integrations such as Home Assistant stay consistent). Nothing else uses it: due dates (and the dates the app offers when picking one), recurrence and reminder times stay in the household's zone. They apply to the user's personal routes only (Web Push, personal email, personal messaging channels), never to household channels; household quiet hours still apply on top. Deferred and coalesced exactly like household quiet hours.
- Channels are **per user** and **per household** via Shoutrrr service URLs.
- Chores without assignees remind every non-muting member; a chore that takes turns reminds only the person whose turn it is. Guests never get reminders or mark-done links.
- Notifications carry a **mark-done link** (confirmation page; only the POST completes; see THREAT_MODEL).
- **Delivery log** per notification; failed sends retried with backoff.
- **Web Push** built in (RFC 8030/8291/8292 in the standard library; provider-allowlisted, egress-guarded, VAPID keys encrypted and rotated with `rotate-key`, `404`/`410` subscriptions pruned). A device subscribes from Account, Notifications ("This device", a clear on/off; needs a secure context and a browser with the Push API; on iPhone and iPad the page explains the Add to Home Screen steps) or in one tap from the Home card "Get reminders on this device?" (shown only to non-guests whose browser supports push, with no push subscription on this device and no other personal route; "Not now" is remembered per device in the browser and never nags). A subscription is a reminder target like a channel: same recipients, mutes, quiet hours, quotas and delivery log (kind `webpush`). Metadata goes through the browser vendor's push service; the message is encrypted to the browser (privacy note in the UI).
- **Email reminders** (instances with email enabled): a per-user switch "Email me reminders" (Settings, Notifications; needs a verified address, switched off when the address changes) sends each reminder to the user's own verified address, same text and mark-done link as other channels, with the same recipients, mutes, quiet hours, quotas and delivery log (kind `email`), and the instance-wide hourly email cap (`emails_global_hour`). Messaging-app channels sit under "Messaging apps (advanced)", collapsed by default; a household channel (for example the family Telegram or Discord group) reaches everyone in the household with no setup on their side.
- **Missed reminders:** after downtime, at most one catch-up reminder per chore.
- Every reminder has a unique delivery key (no duplicates across crash, restart or takeover).

### Views and calendar

- **Home:** chores grouped Overdue / Today / This week / Later; "Mine / Everyone" toggle; views by area.
- **Calendar screen:** month and week views by due date.
- **iCal feeds:** per user per household; read-only; revocable and regenerable. Calendar-rule chores show the next 12 months; completion-based chores show only the next due date.
- One optional **area** per chore; households manage their own areas. Areas are an optional household feature, **off by default** (`areasEnabled`, owner setting "Group chores by area"): when off, Home has no area chips or labels, the chore editor and detail have no area field, templates' suggested area is ignored, and iCal feeds omit `CATEGORIES`; existing areas and chore links stay untouched and the API still accepts `areaId`. Turning it on offers "Add starter areas" when the household has none. Importing a file with areas into a new household turns the setting on; merging into a household with areas off keeps the setting.

### Interface and languages

- Responsive web app, installable PWA (no offline mode). Apple-inspired design, light/dark following system with manual override.
- English only at launch. i18n built in from day one: externalised message files, Unicode/CJK-safe typography, CSS logical properties and RTL support, so reviewed languages can be added later.
- Dates, times and first weekday follow the user's language, with personal override.
- Accessibility: WCAG 2.2 AA.
- **Export:** every user can export their data (JSON). Owners and members can export a whole household as JSON (chores, areas, settings, history); encrypted notification URLs and secrets are never included. An owner's file also carries the household's outgoing webhooks (name, events, URL, enabled flag; additive optional `webhooks` field, format version stays 1; never the signing secret; the URLs can hold credentials, so the file is sensitive).
- **Import:** create a new household or merge into one where the user is owner/member. Webhooks in the file are re-created only for an owner and only while the instance has webhooks on: always **disabled** with fresh signing secrets (the owner reviews, rotates the secret to see it, and enables); invalid URLs/events (checked with today's scheme and egress rules) and anything over the household's webhook limit are skipped and reported in the result (`webhooksSkipped`), never fatal. Merge combines same-name areas, adds chores as new, turns non-member assignees into "anyone". Upload size cap; limits validated and the whole import runs in one transaction (all or nothing).
  - Formats are versioned (`gunkly-user-export` / `gunkly-household-export`, version 1); unknown formats, versions and fields are rejected. Household files carry assignee and completer display names and usernames as matching hints (usernames are visible to every household member in the file; no emails, notification URLs, tokens or feeds; only an owner's file has webhook URLs).
  - Upload cap 5 MiB (import routes only). Every field is validated like the normal create paths (names, http/https links, RRULE, intervals, time zone, reminders); chores, areas, reminders per chore and completions per chore are checked against the target's limits.
  - Merge details: areas match by case-insensitive name; chores always get new IDs; assignees match by username among non-guest members, else the chore stays "anyone"; imported history gets the source `import` (shown as "Imported"); a completer is attributed only when the importer is an owner of the target household and the username matches a member, otherwise the history entry is anonymous (at most 20,000 history entries per file); the due date comes from the file (recomputed from the schedule if absent, and the instant always in the target's time zone); the target's own settings are never changed. Creating a new household applies the file's settings and makes the importer the only member (owner).

### Instance operations

- **Email optional:** `EMAIL_ENABLED=false` is the self-hosted default (username sign-in; admin or CLI password reset). With email configured, self-hosted users may still add an address (never required): verification, password reset by email, opt-in email-OTP 2FA and inactivity emails via any SMTP server (through the egress guard: a hosted relay must be public). Hosted requires email.
- **Databases:** SQLite (default) or Postgres by configuration; same schema and migrations. `gunkly migrate-db --from sqlite:///data/gunkly.db --to postgres://...` (target must be empty; consistent copy; row-count and checksum verification; SQLite file untouched).
- **Backups:** `gunkly backup` writes a consistent SQLite snapshot while running (stdout or inside `/data`). Postgres uses `pg_dump`. Never copy the volume while running.
- **Scheduler:** one active sender. On Postgres an advisory lock with standby takeover; `GUNKLY_DISABLE_SCHEDULER=true` for web-only. SQLite is single instance. On SIGTERM: stop accepting, let in-flight requests and sends finish (default 30 s), release the lock, exit.
- **Updates:** admin Updates section (running version, newer release from the GitHub Releases API, its changelog with basic Markdown formatting (headings, lists, bold, italic, code, http(s) links; rendered as text nodes, never as HTML; no images); `GET /api/v1/admin/updates`). A background check runs at most every 12 hours through the egress guard with the User-Agent `gunkly-update-check` and no instance data; the result is cached in the settings table. Opening the page refreshes a missing, hour-old or older-than-running cache in the background (non-blocking, one request in flight, at most one per 5 minutes), and "Check now" (`POST /api/v1/admin/updates/check`) does the same on demand. The admin can switch it off (`updateCheck`, default on); off means no request at all. Migrations auto-apply on start by default (Postgres advisory lock; SQLite pre-migration snapshot). If disabled and migrations are pending, the server refuses to start until `gunkly migrate`. Never rolled back automatically. Gunkly never replaces its own image.
- **Reverse proxy:** `Forwarded` (RFC 7239, preferred) and `X-Forwarded-For` honoured only from configured trusted proxies, walked right to left. Admins are warned (`GET /api/v1/admin/health`, in-memory, last 5 peers within 24 h, never logged) when a direct peer that is not trusted sends forwarding headers. Root-path hosting only in MVP. `/healthz`, `/readyz`, and `/metrics` (Prometheus text format, hand-written; enabled only when `GUNKLY_METRICS_TOKEN` is set, `Authorization: Bearer <token>`, 30 requests/minute per IP). Series: build info, process start time, goroutines, heap, HTTP requests by route pattern and status class, a request duration histogram, deliveries sent/failed by channel kind (including `webpush`), scheduler last-tick age, delivery queue depth and open database connections. No user or household labels.
- **Inactive accounts (instance settings `inactiveEnabled` default off, `inactiveWarnAfterDays` default 365, `inactiveDeleteAfterDays` default 548; warn at least 30 days, delete at least 30 days after the warning):** a daily job in the scheduler (leader only) warns accounts idle longer than the warn period by email (when enabled and verified) and through all of the user's personal notification channels and Web Push subscriptions, recording `inactive_warned_at`; it repeats a final warning once the delete period is within 30 days; and deletes accounts past the delete period only after the final warning is at least 30 days old, with the same routine as account deletion and automatic ownership transfer (longest-standing non-guest; guests never promoted; households without a non-guest member are deleted). Signing in (or any activity) clears the warnings. Instance admins are never warned or deleted (logged and skipped). A warning that reached nobody (every send failed) is retried the next day; an account with no way to be reached is recorded as warned and so still ends up deleted after the periods. At most 500 warnings per run. Idempotent across restarts and instances. Text comes from `internal/i18n`.
- **Hosted-only-by-default controls (usable by self-hosters):** egress guard, admin-selected notification service allowlist, ALTCHA sign-up proof-of-work, inactive-account lifecycle (off by default; warn then delete after configurable periods), GDPR items (privacy policy, ToS 16+, EU-only own infrastructure, breach procedure).

### Hosted service

Legal and compliance documents for the maintainer's hosted service are kept outside this repository (not legal advice; reviewed by the maintainer before use; not for self-hosters) and cover: privacy policy (data collected, purposes, legal bases, retention matching the defaults here, processors, EU hosting, third-party notification and push services outside the EU at the user's instruction, rights and how to exercise them in the app: export, deletion, age 16+), terms of use (non-commercial, acceptable use, age 16+, no warranty, inactive-account policy), the Art. 30 processing register, the breach procedure (72-hour notification to the supervisory authority where required, informing users, breach log) and incident response. Changing retention defaults or the inactive-account behaviour means updating those texts too.

### Limits (all configurable via one limits service; per-household override supported)

| Limit | Default |
|-|-|
| Households per user | 20 |
| Members per household | 50 |
| Chores per household | 2,000 |
| Reminders per chore | 10 |
| Notification channels per user / per household | 10 / 10 |
| Notifications sent per household | 100/hour, 250/day |
| Notifications sent per user (all households) | 500/day |
| Overdue reminder repeat cadence | at most once per 24 h per chore |
| Outbound SMTP emails | 10/hour per account for authenticated actions; 5/hour per IP for requests without a session (these never count against the account); 20/hour per account for sign-in and step-up codes; 500/hour global |
| Completions per import file | 20,000 in all |
| Areas per household | 100 |
| Completions kept per chore (oldest non-reverted pruned beyond this) | 1,000 |
| Authenticated write requests | 120/minute per user (burst 60); 300/minute per household (burst 100); fixed, not admin-tunable |
| API token requests | 120/minute per token (burst 60); 300/minute per client IP (burst 100); fixed, not admin-tunable |
| Active invite links per household | 20 (each link: 1-100 uses, default 1) |
| Calendar feeds per user | 10 |
| Outgoing webhooks per household (only while the admin enables webhooks) | 10 |
| API tokens per user (active) | 20 |
| Passkeys per user | 20 |
| Push notification devices (Web Push subscriptions) per user | 10 |
| Sign-ups per IP per hour (open sign-up on) | 5 |
| Login attempts per IP (IPv6 by /64) and per IP + account | 5/minute (admin-tunable, applied live), then exponential backoff; never hard-lock |
| Failed password / second-factor attempts per account, across all IPs | 5 free, then doubling delay capped at 15 minutes; success resets |

### Data retention (daily cleanup; audit and delivery periods configurable by the admin)

| Data | Default |
|-|-|
| Audit log | 90 days |
| Notification delivery log | 30 days |
| Expired sessions, tokens, API tokens, invite links | Deleted once expired (API tokens also once revoked) |
| Accounts with an unverified email address and no household (hosted mode only) | Deleted after 7 days |

SQLite space is reclaimed with bounded `PRAGMA incremental_vacuum` batches, never a full `VACUUM`.

### API rules

Every feature goes through `/api/v1` first; no business logic only in the front end; breaking `/api/v1` changes are forbidden. Auth supports browser sessions and household-scoped, least-privilege API tokens (ADR 0014):

- **Token:** `gk_` + 43 base64url characters, sent as `Authorization: Bearer gk_...`, stored only as a SHA-256 hash and shown once. Created under Settings, "Integrations" (step-up required): a name, the households it is bound to, scopes and an expiry (30 days to 10 years, or never). Listed with scopes, households, created, last used (written at most once a minute) and expiry; revoked at once with a confirmation. At most 20 active per user.
- **Scopes** (no implication between them): `chores:read` (households, chores, completion history, calendar, areas, templates, reminders and presets), `chores:complete` (complete, snooze, skip, pause, resume, revert a completion), `chores:write` (create, update, archive, unarchive, delete chores, set reminders, mute, create/rename/delete areas with the same role rules as a session). Never available to a token: account, sessions, 2FA, password, email, members, invites, household settings or deletion, notification channels, push, calendar feeds, export and import, admin and token management.
- **Effective permission** is the scope AND the owner's current role in that household (a guest owner can only read). A household the token is not bound to, or that its owner has left, answers `404` like an outsider.
- **Bearer requests** ignore cookies, set none and need no CSRF header; a request that carries both a cookie and a token is authenticated by the token alone. Missing, malformed, unknown, expired or revoked tokens answer `401` with `WWW-Authenticate: Bearer` (no distinction). `GET /api/v1/token` describes the calling token and its households.
- **Limits:** 120 requests/minute per token (burst 60) and 300/minute per client IP over all bearer requests (burst 100); not admin-tunable.
- **Audit:** `token_create`, `token_revoke`, and `token_complete` for each completion made with a token (token ID in the metadata, never the secret). Such completions have source `api_token`.
- Intended clients: the Home Assistant integration and AI assistants through the built-in MCP server (`/mcp`, Streamable HTTP, stateless; ADR 0015). Its tools map onto the three scopes: read (`list_households`, `list_chores`, `get_chore`, `upcoming`, `list_areas`), complete (`complete_chore`, `undo_completion`, `snooze_chore`, `skip_chore`), write (`create_chore`, `update_chore`, `archive_chore`, `unarchive_chore`, `create_area`, `rename_area`; deleting chores or areas is REST-only). The admin setting `mcpEnabled` (default on) switches it off.

### Non-functional

- Security baseline and hosted requirements: see `docs/THREAT_MODEL.md`.
- Container: distroless, non-root, read-only rootfs, no shell, one port, health check.
- Supply chain, size/dependency budgets and fitness tests: see ADR 0012 and `AGENTS.md`.

## Delivery phases

| Phase | Contents |
|-|-|
| **0. Foundations** | Repo files (`LICENSE`, `SECURITY.md`, `AGENTS.md`); `make check`; CI (dual-DB `-race`, lint, deadcode, govulncheck, gitleaks, CodeQL, license check, fitness tests, size budgets, Playwright + axe-core, OpenAPI diff, fuzzing, DAST); Dockerfile and both compose files; scoped data-access layer with isolation tests; limits service; translation setup; egress guard with CI tests; ADRs |
| **1A. Self-Hosted Alpha** | Accounts, roles, invites, TOTP; multiple households; recurring and one-off chores with interval and RRULE rules; assignees; snooze, skip, pause, undo, archive; simple/advanced editors; templates and starter areas; reminders, presets, quiet hours; Shoutrrr + egress guard + service allowlist; mark-done + 5-minute undo links; delivery log; home and calendar screens, iCal feeds; PWA; English with i18n/RTL wiring; basic admin panel and CLI (`backup`, `rotate-key`, `reset-password`, `reset-2fa`); first-run setup; session overview; health endpoints; trusted-proxy setting; scheduler lock and graceful shutdown; retention cleanup; full security baseline |
| **1B. Public Hosted Launch** | Web Push; transactional JSON export/import; `migrate-db`; ALTCHA; email-OTP 2FA; inactive-account lifecycle; Updates page with optional auto-migrations; Prometheus `/metrics`; production canary rollout; external security review; GDPR documentation |
| **0.2** | API tokens, built-in MCP server with guided setup, Home Assistant integration (separate repo) |
| **0.3** | OAuth 2.1 "sign in with Gunkly" for MCP, so ChatGPT custom connectors and one-click connector setups (e.g. Claude Desktop connectors) work without copying tokens. Implemented (ADR 0016): optional, off by default (instance setting, needs a public https base URL), public clients with dynamic registration, PKCE S256, short-lived access tokens for `/mcp` only, rotating refresh tokens, a "Connected apps" list |
| **0.4** | Outgoing webhooks (ADR 0017, `docs/webhooks.md`): optional, off by default (instance setting `webhooks_enabled`); owner-managed per household; signed (HMAC-SHA256, per-webhook secret) JSON for chore events; https only unless the egress guard is off, always through the egress guard, no redirects; outbox with retries, auto-disable after 20 consecutive failures, delivery log of the last 50. Rotating assignees ("Take turns"), email retry queue, COEP header |
| **0.5** | Passkeys and SSO (ADR 0018); personal quiet hours; chores that take turns show whose turn it is by name for API tokens (additive `turnName` on chores; tokens cannot list members), and the Home Assistant integration shows it on the to-do item and as a sensor attribute |
| **0.5.2** | One Settings screen (your groups, then the current household's; Administration stays separate; old `/account` and `/households/{id}` addresses redirect); a dedicated AI section explaining assistant access; a guided Home Assistant setup (My Home Assistant links, prefilled token); the admin page explains and shows the single sign-on setup from `GUNKLY_OIDC_*` |
| **Later** | Sub-path hosting, native mobile apps, more languages (CJK, RTL) via reviewed translations; AI help to create chores from plain language (typed or spoken), which would need an AI provider configured on the instance (to be decided) |

**Definition of done (1A / 1B):** a chore can be completed in under 3 taps from the home screen on a phone, in either theme, in every enabled language, passing WCAG 2.2 AA checks, with every item in the applicable security sections in place.
