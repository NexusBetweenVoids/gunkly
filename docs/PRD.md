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
- Joining is via **invite links** that expire and can be revoked. Open sign-up is an instance setting: off by default self-hosted, on for hosted (after email verification). A valid invite still lets an invitee register when open sign-up is off, unless an admin restricts registration to admin-created accounts.
- Sign-in: email (or username when email is disabled) plus password, optional **TOTP** with single-use recovery codes. Hosted: 2FA mandatory (TOTP or email one-time code); users are warned that hosted has no manual admin reset if authenticator and recovery codes are both lost. Self-hosted admins can run `gunkly admin reset-2fa <user>`. Passkeys and SSO are Later.
- The **instance admin** manages users, limits and instance settings but cannot read household content (chores, notes, history, notification settings) through the app.
- **Leaving a household / deleting an account:** chores assigned only to that person become unassigned ("anyone"); they are removed from other chores' assignees; past completions stay but are anonymised ("Deleted user"). When a household's last owner leaves, ownership transfers only to non-guest members; guests are never promoted. A household with no non-guest members left is deleted, and the app warns and asks confirmation first.
- **First-run setup:** one-time screen to create the instance admin, gated by a setup code printed in the container logs; disappears once used.
- **Admin CLI recovery:** `gunkly admin reset-password <user>` and `gunkly admin reset-2fa <user>` (TOTP and email-OTP), run via `docker exec`.
- **Sessions:** users list signed-in devices, sign out one or all. Sliding expiry default 30 days inactivity, 90-day absolute max (admin-configurable: 1-365 idle, 1-730 absolute days). Sensitive actions (password/email/2FA change, API token creation, account deletion) need step-up verification within 15 minutes.
- Each household has an IANA **time zone**, defaulting to the creator's browser zone. Timestamps are stored in UTC; reminder times use the household zone.

### Chores and scheduling

- **Recurring or one-off.** Editor defaults to recurring; one-off is under "Does not repeat" and is auto-archived on completion.
- Recurrence: **interval** (every N days/weeks/months) or **calendar rule** (e.g. 1st Saturday of the month, every April and October) stored as a documented RRULE subset (ADR 0005).
- Next due date is computed **from completion** (default for interval) or **from scheduled date** (default for calendar); changeable per chore.
- Edge cases: completing a multi-cycle-overdue "from scheduled" chore jumps to the next upcoming occurrence (no backlog); completing early cancels pending reminders for the current cycle; monthly on 29/30/31 falls on the month's last day; a nonexistent DST local time is sent at the next valid time.
- **Assignees:** zero ("anyone"), one or many household members. One completion by anyone completes the chore for everyone.
- **Snooze** (move this occurrence), **skip once**, **pause** (until a date or indefinitely) on every chore.
- Fields: name, optional area, notes, optional link (`http`/`https` only, enforced on server and client).
- **Simple editor:** name, area, assignees, first due date, repeat, reminder preset. **Advanced editor:** every option.
- **Completion record:** who, when, source (`app`, `mark_done_link`, `api_token`; token ID in audit log), optional note. History kept until the chore is deleted.
- **Undo:** shown right after completing (app or mark-done page); owners and members can revert any completion later from history. Revert marks the entry reverted (kept for audit), restores the previous due date and reschedules reminders. On the unauthenticated mark-done page, `POST /mark-done` consumes the single-use token and returns a 5-minute single-use undo token scoped to that completion, so Undo needs no session.
- **Archive** instead of delete: keeps history, stops reminders. Delete remains possible.
- **Templates:** starter chore templates and starter areas.

### Reminders and notifications

- Optional. Per chore: **before due**, **on due date**, **repeating while overdue** (at most once per 24 h per chore), each at a time of day.
- Household **presets** ("Gentle", "Nag me") and household-level **quiet hours**; members can mute a chore. Reminders in quiet hours are deferred to the end and coalesced to one per chore, never dropped. Personal quiet hours are Later.
- Channels are **per user** and **per household** via Shoutrrr service URLs.
- Chores without assignees remind every non-muting member. Guests never get reminders or mark-done links.
- Notifications carry a **mark-done link** (confirmation page; only the POST completes; see THREAT_MODEL).
- **Delivery log** per notification; failed sends retried with backoff.
- **Web Push** built in (provider-allowlisted, egress-guarded, VAPID keys encrypted, `404`/`410` subscriptions pruned).
- **Missed reminders:** after downtime, at most one catch-up reminder per chore.
- Every reminder has a unique delivery key (no duplicates across crash, restart or takeover).

### Views and calendar

- **Home:** chores grouped Overdue / Today / This week / Later; "Mine / Everyone" toggle; views by area.
- **Calendar screen:** month and week views by due date.
- **iCal feeds:** per user per household; read-only; revocable and regenerable. Calendar-rule chores show the next 12 months; completion-based chores show only the next due date.
- One optional **area** per chore; households manage their own areas.

### Interface and languages

- Responsive web app, installable PWA (no offline mode). Apple-inspired design, light/dark following system with manual override.
- English only at launch. i18n built in from day one: externalised message files, Unicode/CJK-safe typography, CSS logical properties and RTL support, so reviewed languages can be added later.
- Dates, times and first weekday follow the user's language, with personal override.
- Accessibility: WCAG 2.2 AA.
- **Export:** every user can export their data (JSON). Owners and members can export a whole household as JSON (chores, areas, settings, history); encrypted notification URLs and secrets are never included.
- **Import:** create a new household or merge into one where the user is owner/member. Merge combines same-name areas, adds chores as new, turns non-member assignees into "anyone". Upload size cap; limits validated and the whole import runs in one transaction (all or nothing).
  - Formats are versioned (`gunkly-user-export` / `gunkly-household-export`, version 1); unknown formats, versions and fields are rejected. Household files carry assignee and completer display names and usernames as matching hints (usernames are visible to every household member in the file; no emails, URLs, tokens or feeds).
  - Upload cap 5 MiB (import routes only). Every field is validated like the normal create paths (names, http/https links, RRULE, intervals, time zone, reminders); chores, areas, reminders per chore and completions per chore are checked against the target's limits.
  - Merge details: areas match by case-insensitive name; chores always get new IDs; assignees match by username among non-guest members, else the chore stays "anyone"; a completer is attributed only when the username matches a member, otherwise the history entry is anonymous; the due date comes from the file (recomputed from the schedule if absent, and the instant always in the target's time zone); the target's own settings are never changed. Creating a new household applies the file's settings and makes the importer the only member (owner).

### Instance operations

- **Email optional:** `EMAIL_ENABLED=false` is the self-hosted default (username sign-in; admin or CLI password reset). With email: verification, reset, email-OTP 2FA, inactivity emails via any SMTP server. Hosted requires email.
- **Databases:** SQLite (default) or Postgres by configuration; same schema and migrations. `gunkly migrate-db --from sqlite:///data/gunkly.db --to postgres://...` (target must be empty; consistent copy; row-count and checksum verification; SQLite file untouched).
- **Backups:** `gunkly backup` writes a consistent SQLite snapshot while running (stdout or inside `/data`). Postgres uses `pg_dump`. Never copy the volume while running.
- **Scheduler:** one active sender. On Postgres an advisory lock with standby takeover; `--disable-scheduler` for web-only. SQLite is single instance. On SIGTERM: stop accepting, let in-flight requests and sends finish (default 30 s), release the lock, exit.
- **Updates:** admin Updates page (running version, newer release from GitHub Releases, changelog; switchable off, sends no instance data). Migrations auto-apply on start by default (Postgres advisory lock; SQLite pre-migration snapshot). If disabled and migrations are pending, the server refuses to start until `gunkly migrate`. Never rolled back automatically. Gunkly never replaces its own image.
- **Reverse proxy:** `X-Forwarded-For` honoured only from configured trusted proxies. Root-path hosting only in MVP. `/healthz`, `/readyz`, optional token-protected `/metrics`.
- **Hosted-only-by-default controls (usable by self-hosters):** egress guard, admin-selected notification service allowlist, ALTCHA sign-up proof-of-work, inactive-account lifecycle (off by default; warn then delete after configurable periods), GDPR items (privacy policy, ToS 16+, EU-only own infrastructure, breach procedure).

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
| Outbound SMTP emails | 10/hour per account (5/hour per IP unauthenticated); 500/hour global |
| Areas per household | 100 |
| Completions kept per chore (oldest non-reverted pruned beyond this) | 1,000 |
| Authenticated write requests | 120/minute per user (burst 60); 300/minute per household (burst 100); fixed, not admin-tunable |
| Active invite links per household | 20 (each link: 1-100 uses, default 1) |
| Calendar feeds per user | 10 |
| Sign-ups per IP per hour (open sign-up on) | 5 |
| Login attempts per IP (IPv6 by /64) and per IP + account | 5/minute (admin-tunable, applied live), then exponential backoff; never hard-lock |
| Failed password / second-factor attempts per account, across all IPs | 5 free, then doubling delay capped at 15 minutes; success resets |

### Data retention (daily cleanup; audit and delivery periods configurable by the admin)

| Data | Default |
|-|-|
| Audit log | 90 days |
| Notification delivery log | 30 days |
| Expired sessions, tokens, invite links | Deleted once expired |

SQLite space is reclaimed with bounded `PRAGMA incremental_vacuum` batches, never a full `VACUUM`.

### API rules

Every feature goes through `/api/v1` first; no business logic only in the front end; breaking `/api/v1` changes are forbidden. Auth supports browser sessions and household-scoped, least-privilege API tokens (e.g. `chores:read`, `chores:complete`, `chores:write`; no member, channel or deletion scopes by default).

### Non-functional

- Security baseline and hosted requirements: see `docs/THREAT_MODEL.md`.
- Container: distroless, non-root, read-only rootfs, no shell, one port, health check.
- Supply chain, size/dependency budgets and fitness tests: see ADR 0012 and `AGENTS.md`.

## Delivery phases

| Phase | Contents |
|-|-|
| **0. Foundations** | Repo files (`LICENSE`, `SECURITY.md`, `AGENTS.md`); `make check`; CI (dual-DB `-race`, lint, deadcode, govulncheck, gitleaks, CodeQL, license check, fitness tests, size budgets, Playwright + axe-core, OpenAPI diff, fuzzing, DAST); Dockerfile and both compose files; staging pipeline; scoped data-access layer with isolation tests; limits service; translation setup; egress guard with CI tests; ADRs |
| **1A. Self-Hosted Alpha** | Accounts, roles, invites, TOTP; multiple households; recurring and one-off chores with interval and RRULE rules; assignees; snooze, skip, pause, undo, archive; simple/advanced editors; templates and starter areas; reminders, presets, quiet hours; Shoutrrr + egress guard + service allowlist; mark-done + 5-minute undo links; delivery log; home and calendar screens, iCal feeds; PWA; English with i18n/RTL wiring; basic admin panel and CLI (`backup`, `rotate-key`, `reset-password`, `reset-2fa`); first-run setup; session overview; health endpoints; trusted-proxy setting; scheduler lock and graceful shutdown; retention cleanup; full security baseline |
| **1B. Public Hosted Launch** | Web Push; transactional JSON export/import; `migrate-db`; ALTCHA; email-OTP 2FA; inactive-account lifecycle; Updates page with optional auto-migrations; Prometheus `/metrics`; production canary rollout; external security review; GDPR documentation |
| **Later** | Passkeys and SSO, rotating assignees, personal quiet hours, sub-path hosting, Home Assistant integration, outgoing webhooks, native mobile apps, more languages (CJK, RTL) via reviewed translations, MCP server (token-based first; OAuth 2.1 when remote hosted MCP is enabled) |

**Definition of done (1A / 1B):** a chore can be completed in under 3 taps from the home screen on a phone, in either theme, in every enabled language, passing WCAG 2.2 AA checks, with every item in the applicable security sections in place.
