# Changelog

All notable changes are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.5.3] - 2026-11-25

### Added

- Adding a notification channel is guided: pick Discord, Slack, Google Chat, Telegram, Pushover, Pushbullet or ntfy (public or your own server) and Gunkly shows short setup steps and asks only for what that app gives you (a webhook link, a bot token and chat, a user key and API token, a topic), then builds the Shoutrrr service URL itself. Every service links to its full setup guide; "Enter a service URL instead" and the other services keep the URL field, which now also turns a pasted Discord, Slack or Google Chat webhook link into the right format. Services show by name ("Google Chat", not `googlechat`).

- Chores carry `reminderTime` ("HH:MM" in the household's time zone, or `null`): when the due-day reminder goes off, from the chore's own reminders or its preset (the household default when the chore has none). Readable with `chores:read`, so the Home Assistant integration can show chores at their time instead of all day. MCP chore results include it as `remindAt`.

### Changed

- Settings, Integrations, Home Assistant: three steps instead of four. "Add to Home Assistant" is offered where it is needed, next to the new key and the server address, instead of as a separate last step.

## [0.5.2] - 2026-11-25

### Changed

- Settings is now one screen: your own groups (Profile & email, Sign-in & security, Notifications, AI, Integrations, Your data) and, below them, the current household's (General, People, Notifications, Connections, Data). Switching household in the header switches the household part. Administration stays a separate screen. The header's "Account" link and the household "Settings" tab became a single "Settings" (in the tab bar; in the header only without a household). Old addresses (`/account/...`, `/households/<id>/...`) redirect to `/settings/...`, so bookmarks and emailed links keep working.
- Access tokens are listed under Settings, Integrations as "Access tokens"; connecting an AI assistant moved to the new AI group.

### Added

- Settings, AI: explains in plain words what connecting an AI assistant (Claude, ChatGPT, Gemini, other MCP apps) does, with example requests and the setup steps, followed by the guided setup and the connected apps. If an admin has switched MCP off, it says so (and admins get a link to turn it on).
- Settings, Integrations, Home Assistant: a guided setup with "Open in Home Assistant" (installs the integration through HACS via My Home Assistant) and "Add to Home Assistant" (starts adding the Gunkly integration), and a key prefilled for Home Assistant (all your households; see, complete and add chores) that shows this server's address next to it.
- Administration, Sign-up & sign-in has a "Single sign-on (SSO)" section. Single sign-on is still configured only through `GUNKLY_OIDC_*` environment variables (it needs the client secret); the section now says so. When it is off, it explains the setup step by step with this server's redirect URI; when it is on, it shows the provider, client ID, scopes and redirect URI (never the secret). `GET /admin/settings` adds `ssoAvailable`, `ssoIssuer`, `ssoClientId`, `ssoScopes` and `ssoCallback`.

## [0.5.1] - 2026-11-24

### Added

- Per-user time zone (Account, Profile, "Time zone"; default "Same as the household", so nothing changes unless you set it): your personal quiet hours are read in it instead of each household's zone, for people whose households are in different zones. The browser's zone is offered in one click. Household quiet hours, due dates and reminder times stay in the household's zone. `PATCH /me` takes `timeZone` (an IANA name; `null` or empty clears it; session only); the account export includes it.
- MCP can edit and archive chores: new `update_chore` (partial update of name, notes, link, area, assignees, schedule incl. interval or RRULE, reminder preset and time, take-turns settings; same validation as the REST update, unknown or invalid fields are errors), `archive_chore` and `unarchive_chore`, all under `chores:write` with the REST role rules. Deleting stays REST-only (ADR 0015, `docs/ai-assistants.md`).
- API tokens can manage areas: `chores:read` lists them, `chores:write` creates, renames and deletes them, with the same role rules as sessions (a token never exceeds its owner's role). New MCP tools `list_areas` (read), `create_area` and `rename_area` (write; they refuse while the household has areas turned off; deleting areas is REST-only). OpenAPI and ADR 0014 updated.
- Household export/import carry webhooks (ADR 0017, `docs/webhooks.md`): an owner's export adds each webhook's name, events, URL and enabled flag, never its signing secret, and the file is documented as sensitive because URLs can embed credentials. Import re-creates them disabled with new secrets for the owner to review; it skips (and reports as `webhooksSkipped`) webhooks when the instance switch is off, when the importer is not an owner, on invalid URLs or events, or over the household limit. Export format stays version 1 (additive optional `webhooks` field).

### Fixed

- Single sign-on confirmation (step-up, also on the OAuth consent page) now requires the provider's `auth_time` (OIDC requires it when `max_age` is sent; Keycloak and authentik send it), and failed confirmations are recorded in the account's activity log. An account without a password pressing Allow on the consent page no longer counts as a wrong password.
- OAuth consent page (ChatGPT and Claude connectors): an account whose only second factor is a passkey no longer has to type a recovery code, and an account without a password (created by single sign-on) can now approve an app at all. Accounts with a passkey see **Confirm with a passkey** (user verification required; Gunkly verifies the assertion itself); linked accounts see **Confirm with {provider}**, which re-verifies through the identity provider (`prompt=login`, `max_age=0`) and returns to the same consent request. Without script the page is the same plain form as before (password, authenticator, emailed or recovery code).

### Changed

- Single sign-on button name (ADR 0018): an administrator can set it under Administration, Sign-up & sign-in ("Name on the single sign-on button"); it overrides `GUNKLY_OIDC_NAME`. Without a name the buttons now say "Sign in with SSO" and "Confirm with SSO" instead of "single sign-on". Admin settings API: `ssoName` (at most 50 characters), `ssoNameDefault` (read-only, the environment value).
- Sign-in page: the passkey and single sign-on buttons sit under an "or" divider, full width (they were cramped under the Sign in button).
- The consent page loads one small same-origin script, only for accounts that have a passkey (`GET /oauth-consent.js`; no inline script, `script-src 'self'` as before). Offering single sign-on adds the provider's origin to that page's `form-action`. New `POST /oauth/authorize/passkey` (options for the passkey ceremony, bound to the session and the consent page). ADR 0016 and 0018, `docs/sign-in.md`, `docs/sso.md` and the threat model are updated; two new screenshots (sign-in page, Account, Sign-in & security with a passkey).
- The "Create chores" permission is now labelled "Create and edit chores, manage areas" in the access and connect dialogs; the scope itself (`chores:write`) is unchanged.

## [0.5.0] - 2026-11-24

### Added

- Chores that take turns now carry `turnName` (the display name of the person whose turn it is) in the REST API and the MCP chore output, next to `turnUserId`. Additive; API tokens still cannot list members.
- Personal quiet hours (Account, Notifications): your own do-not-disturb window for reminders to your devices, email and messaging apps, in the household's time zone. Reminders wait until it ends, one per chore, never dropped; household channels and the household's own quiet hours are unaffected. `PATCH /me` takes `quietHours` (session only); the account export includes it.
- Passkeys (ADR 0018, `docs/sign-in.md`): add them under Account, Sign-in & security. A passkey signs you in without a password ("Sign in with a passkey" on the sign-in page; your device verifies you with a PIN, fingerprint or face, so it counts as password plus second factor), works as a second factor after a password next to authenticator codes and emailed codes, and can confirm sensitive actions. They need `GUNKLY_BASE_URL` to be an https address (or localhost). New dependency: `go-webauthn/webauthn`. `gunkly admin reset-2fa` removes passkeys too; the data export lists their names and dates.
- Single sign-on with OpenID Connect (ADR 0018, `docs/sso.md`), self-hosted only: set `GUNKLY_OIDC_ISSUER`, `GUNKLY_OIDC_CLIENT_ID`, `GUNKLY_OIDC_CLIENT_SECRET` (and optionally `GUNKLY_OIDC_NAME`, `GUNKLY_OIDC_SCOPES`) and a "Sign in with ..." button appears. Works with Authelia, Authentik, Keycloak, Kanidm, Zitadel, Pocket ID and Google (authorization code with PKCE; ES256 supported). Users link it under Account, Sign-in & security; an administrator can let unknown users get an account on their first sign-in (off by default; Administration, Sign-up & sign-in). Accounts are identified by the provider's issuer and subject, never by email. A single sign-on counts as fully authenticated (the provider handles multi-factor). A provider on your LAN is reachable (only its own host bypasses the private-address block). New dependency: `coreos/go-oidc`.

### Security

- Dependency updates: `gorilla/websocket` (indirect, via Shoutrrr; GO-2026-6278, weak random mask key) and `go-jose` 4.1.6 (GHSA-mgcc-cww5-fp2q, oversized RSA keys in a provider's key set).
- Single sign-on is tested in CI against real Keycloak and authentik on every SSO change, nightly and before each release; a release is not published if it fails.

### Fixed

- Home on a narrow phone: a chore that takes turns no longer squeezes its name to one letter per line; the "<name>'s turn" badge now sits under the name and due date.

### Changed

- Docs name the new settings groups; fresh README screenshots (current header, take turns, grouped household settings), now generated by `npm run screenshots`.

## [0.4.1] - 2026-11-23

### Changed

- Account, household settings and Administration are split into a few groups (Account: Profile & email, Sign-in & security, Notifications, Integrations, Your data; household: General, People, Notifications, Connections, Data; Administration: Users, Sign-up & sign-in, Connections, Data & retention, Limits, Updates). Each group has its own address (for example `/account/security`); phones show the list of groups and open one at a time, wider screens keep the list at the side. Old links (`/account`, `/admin`, `/households/{id}`) still work.
- Chore page: the actions are calmer. **Done** stays a labelled button; snooze, skip, pause/resume, mute, edit and archive are now a row of quiet icon buttons (each named for screen readers and with a tooltip), and Delete is a small bin icon at the end of the row. On touch screens (no tooltips) each icon has a short caption, in two rows of four on narrow phones.

## [0.4.0] - 2026-11-23

### Security

- OAuth client registration now also refuses redirect URIs containing `;` or `,` (found by the nightly fuzzer; not exploitable, since redirect URIs match exactly and the consent CSP names only the origin, but it keeps the validator and its stated rules in step).
- Every response now carries `Cross-Origin-Embedder-Policy: require-corp` (next to the existing same-origin Opener and Resource policies). Gunkly only serves and loads same-origin resources, so the app, service worker, push, icons, QR codes and mark-done pages are unaffected.

### Changed

- Transactional email (verification, password reset, sign-in codes, notices) goes through a persisted outbox with retries instead of a fire-and-forget send (ADR 0008, migration 0017): a failed send is retried after 1 minute, 5 minutes, 30 minutes, 2 hours and 6 hours, then given up with an audit event `mail_gave_up` (no address or text). Queued mail survives restarts, is sealed at rest and deleted once sent or given up, never outlives the code or link it carries, and the email caps are counted when a message is queued, not per retry.

### Added

- **Take turns** (rotating assignees, PRD "Chores & scheduling"): a recurring chore with 2+ assignees can rotate in the assignee order (editor: "Take turns" with up/down controls). The chore has a current-turn person: Home shows a "Sam's turn" badge and "Mine" means my turn (or unassigned), reminders go to the person whose turn it is only (household channels are unchanged), and completing passes the turn to the next person (option "move on only when the person whose turn it is does the chore"). Undo gives the turn back; skip and snooze keep it; a member who leaves, deletes their account or becomes a guest leaves the rotation. The chore page shows the turn and lets you change it ("It's Sam's turn now").
- API (additive): chores carry `rotation`, `advanceOn` (`any`|`assignee`) and `turnUserId`; `PUT` accepts `rotation` and `advanceOn`; new `PATCH /households/{hh}/chores/{chore}` with `turnUserId` (scope `chores:complete`). MCP `list_chores`/`get_chore` show `takesTurns`, `turnUserId` and `myTurn`, and `mine` follows the turn. Household export and import carry the rotation (`rotation`, `rotationAdvance`, `turn`; assignee order is kept). Migration 0016.
- Outgoing webhooks (ADR 0017, [docs/webhooks.md](docs/webhooks.md)). Optional and off by default: the new instance setting `webhooksEnabled` (`GET/PATCH /api/v1/admin/settings`, Administration, Instance settings) turns it on. While it is off every webhook route answers 404, `GET /api/v1/instance` omits `webhooksEnabled`, nothing is queued or sent and pending deliveries are dropped.
- Household owners manage webhooks (Household settings, "Webhooks (advanced)"; `GET/POST /api/v1/households/{hh}/webhooks`, `GET/PATCH/DELETE .../{id}`, `POST .../{id}/rotate-secret`, `POST .../{id}/test`, `GET .../{id}/deliveries`; owners only, never API tokens; creating, changing and rotating the secret need a step-up): an https address (http too when the admin switched the egress guard off), the events (`chore.completed`, `chore.completion_reverted`, `chore.overdue`, `chore.created`, `chore.updated`, `chore.deleted`, `chore.snoozed`, `chore.skipped`), an active flag and a description. New limit `webhooks_per_household` (default 10); migration 0015.
- Each delivery is stable, versioned JSON (`id`, `type`, `version` 1, `createdAt`, `householdId`, `data.chore`, `data.completion`, `data.actor`; no secrets or email addresses) with the headers `Gunkly-Event`, `Gunkly-Delivery`, `User-Agent: Gunkly-Webhook/<version>` and `Gunkly-Signature: t=<unix>,v1=<HMAC-SHA256 of "<t>.<body>">`. The per-webhook secret is shown once at creation and on rotation, stored encrypted and re-encrypted by `gunkly rotate-key`. Verification in shell, Python and Node and a 5-minute replay window are documented.
- Deliveries are queued with the event, sent by the scheduler through the egress guard (no redirects, response ignored), retried with exponential backoff (6 attempts), de-duplicated per event, logged (last 50 per webhook: status, HTTP code, duration, redacted error) and switched off after 20 consecutive failures (audit event `webhook_auto_disable`, notice in the settings). A test button sends a signed `ping`.

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
