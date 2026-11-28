# Changelog

All notable changes are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Administration, Users: **Delete** removes another person's account, as their own Delete account would. The dialog lists the households that go with it and who becomes owner where they were the last owner (the longest-standing member). The admin needs a passkey or two-factor authentication on their own account, confirms it, and types the account's username. Admin accounts and your own account can't be deleted there. The deletion is written to the admin's activity log.

### Fixed

- Home: ticking Archived no longer hides the Everyone/Mine switch and resets it to Everyone; the switch stays in place and Mine still applies when you go back to the active list.

## [0.13.1] - 2026-11-28

### Security

- Behind a reverse proxy, the client address now comes from `X-Forwarded-For` whenever the proxy sends one; a `Forwarded` header is only used when there is no `X-Forwarded-For`. Before, a client could send its own `Forwarded` header through proxies that only maintain `X-Forwarded-For` (Caddy, nginx, Traefik, Cloudflare) and pick its address for rate limits, the activity log and sign-in alerts. A proxy that sets only `Forwarded` must remove any `X-Forwarded-For` from the client.
- The per-account backoff after failed sign-ins keeps counting real accounts even when many made-up usernames are tried at once.
- Opening the email-confirmation link no longer signs in to an account that already has a second factor (authenticator app, passkey or email codes); the address is still confirmed and the person signs in as usual.
- Notification channels for services whose address part is a key or phone number (IFTTT, Notifiarr, WeCom, Signalgrid, Twilio, Join, Teams) show only the service name; hints saved by earlier versions are reset on upgrade.
- Failed reminder, inactivity and test emails are logged by failure kind only, without the SMTP server's text (which can include the recipient's address); a failed update webhook call is logged without the webhook URL.
- The admin-endpoint guard (AI and update webhook) also blocks cloud metadata and link-local addresses written in IPv4-embedding IPv6 forms (NAT64, 6to4, IPv4-compatible).
- `gunkly rotate-key` also re-encrypts queued emails, so removing the old key afterwards no longer drops mail waiting for delivery.
- A household's write rate limit is now only used up by people who may write in that household; requests for households you are not in no longer count against it, and malformed household IDs are refused before they reach the rate limiter.
- No single kind of rate limit can fill the rate limiter's whole table, so a flood of one kind of request cannot crowd out the others.
- Per-household and per-account limits (chores, notification channels, webhooks, calendar feeds, API tokens, connected apps, push subscriptions, passkeys) now also hold when many requests arrive at once on Postgres.

### Fixed

- Postgres: completing the same chore several times at once now handles the completions one after the other, so the next due date and undo always start from the latest state.
- Postgres: two instance admins deleting their accounts at the same moment can no longer leave the instance without an admin.
- Hosted instances: switching off invite registration no longer refuses invited people while open sign-up (on by default when hosted) still lets anyone register.

## [0.13.0] - 2026-11-28

A design pass across every screen: calmer spacing, line icons, denser lists on phones, and settings pages that say plainly where you stand.

### Changed

- Everywhere: spacing inside cards is even (headings and paragraphs no longer add their own margins on top of the gap); card titles stand out more from field labels and card descriptions use one quiet size; a form's main button is full width on phones and sits at the start on wider screens.
- Navigation uses line icons (house, calendar, plus, gear) instead of text characters, in both the sidebar and the phone tab bar; the desktop sidebar now runs the full height of long pages.
- With more than one household, the household name in the header shows a chevron so it reads as a switcher (still a native select for keyboard and screen readers).
- On phones, a Settings or Administration page shows a small back link and the page's own name as its title, instead of "Settings" twice.
- Home: on phones each chore is two compact lines (name; area, status, turn and people) with a round check button, so about twice as many fit on screen; on wider screens the Done button is filled only for overdue and today's chores and quiet for later ones; only the status is red on an overdue chore; an empty group is one short line; area chips fade at the edge when there are more to scroll to.
- Calendar: past days with chores still open show a red count; one toolbar (‹ month › with a Month/Week switch, Today and Subscribe); month rows have the same height on phones; the selected day lists chores as full rows (area, people, status, Done with Undo) like Home.
- Sign-in and sign-up screens: an invite link now says which household it joins and who sent it ("Join Maple Street 12 · Alex invited you"), and a revoked, expired or used link says so at once instead of showing a form; new-password fields show the admin's minimum length; the empty gap above "Create account" is gone; "Choose a new password" has a way back to sign-in, and an expired email-confirmation link a button onwards; main buttons span these narrow cards at every width and titles are a size smaller. Welcome steps never break a name mid-word on phones (only the current step is named there).
- Settings, Sign-in & security: a one-line verdict at the top ("Only a password protects this account", "Protected by a password and a passkey"), then every way to sign in (password, passkeys, single sign-on, authenticator app, email codes, recovery codes) in one list with its state and one action that opens the same forms and checks as before. "Email me about unusual sign-ins" shows as inactive, with the reason, until an email address is confirmed.
- Settings, Notifications: a status line answers "will I get reminders?" (a warning when nothing is on), then this device, email and messaging apps as one list with state and one action; quiet hours shows only its switch until turned on. Settings, AI no longer says Gunkly has no AI built in.
- Household settings: areas are plain rows with a pencil (rename in place) and a bin; the new-area field and Add share one line. Member rows all have the same shape (name, joined date, role, Remove). Invites read "Member · 1 use left · expires 30 Nov, 14:55". Quiet hours explain themselves and show their times only when on. The import file picker matches the other buttons.
- Administration: recent security events are a one-line banner ("3 security events in the last 7 days · all by you", Review) with the full list on Users; events you caused are marked "by you", and when all of them are, the banner shows on Users only. The admin health check (`GET /api/v1/admin/health`) adds `byYou` to each security event.
- Administration: settings set in the server's environment (email, SSO and others) are a read-only list with one lock note; SSO account creation and the button name moved into the SSO card; notification services are an aligned grid with their real names, with a warning when the private-network guard is off; limits are grouped by topic in compact rows, showing the default and Reset only where a value was changed; "Create user", AI fields, inactive-account days and the update check's status show only when relevant.
- Setup guide: an http://localhost address no longer claims to use https, and the progress bar sits under "Step N of 7".

### Added

- API: `GET /api/v1/invites/{token}` (public) returns the household name and inviter of a valid invite link; `GET /api/v1/instance` includes `minPasswordLength`.

### Fixed

- Longer languages (Dutch, German): buttons and pills no longer split a word mid-way ("Verwijde/ren", "Entferne/n"); a state pill in "Ways to sign in" and "Ways to get reminders" no longer hides under its button on phones ("Nicht verfügb…"); the phone tab bar keeps "Einstellungen" on one line; the settings list on wider screens fits "Benachrichtigungen"; headings, buttons and navigation hyphenate long words in the page language; on a chore, the labels line up with their values and take only the room they need. A few Dutch and German texts read more naturally (Dutch security banner says "beveiligingsgebeurtenissen" like the list it links to; German "Anmeldemethoden" and the single sign-on hints; shorter chore-name examples).
- Opening Gunkly with a session that had ended (signed out or revoked elsewhere, expired) no longer deletes the page's CSRF cookie along with it, so the first sign-in there no longer fails with "Your session expired" (it used to need the client's retry or a reload).

## [0.12.2] - 2026-11-27

### Fixed

- Changing a chore's repeat rule now moves its due date to the first date the new rule produces (on or after the current due date, so an overdue chore stays overdue); before, the old date stayed even when the new rule could never produce it. Applies to the app, the API and MCP.
- A repeat rule that never produces a date (such as 30 February) is now refused when a chore is edited or a household is imported, as it already was on create.
- Repeat rules accept spaces around parts and a trailing `;` (`FREQ=WEEKLY; BYDAY=MO;`).

## [0.12.1] - 2026-11-27

### Fixed

- The language chosen under Settings, Profile now changes the app's language (it was saved, but the app only followed the browser language). It is remembered per browser after signing in; "Browser default" follows the browser again.

## [0.12.0] - 2026-11-27

### Added

- Languages: Dutch, French, German and Spanish, picked from the browser language or set under Settings, Profile. Made with AI assistance; corrections are welcome via pull request (README, Translations).
- Sub-path hosting: serve Gunkly under a path such as `https://example.com/gunkly` by putting the path in `GUNKLY_BASE_URL`. Everything (app, API, MCP, OAuth metadata, feeds, email and invite links, PWA, service worker, cookies) follows the prefix; the reverse proxy must not strip it (README has Caddy, nginx and Traefik examples). One image serves any prefix.
- Unusual sign-in emails: a user is emailed when a sign-in comes from a country they have not signed in from in the last 90 days, or from a hosting, cloud or Tor network when their earlier sign-ins were not (at most once an hour, never on a first sign-in; can be turned off under Settings, Sign-in & security). Location comes from a trusted proxy header (`CF-IPCountry` by default, `GUNKLY_GEOIP_COUNTRY_HEADER`, `GUNKLY_GEOIP_ASN_HEADER`) or optional GeoIP database files (`GUNKLY_GEOIP_COUNTRY_DB`, `GUNKLY_GEOIP_ASN_DB`, e.g. DB-IP Lite); without a source the feature is off. Only the country code and a hosting flag are stored with the sign-in (migration 0022 adds the user setting).
- Settings, Profile: "Go through the setup again" reruns the welcome flow. With a household already, it offers that household (or a new one), leaves out starter chores the household already has, and shows the invite step only to owners.

### Changed

- The first-load JavaScript budget is now 50 KB gzipped (was 45); translations load on demand per language and are budgeted per language.
- Release workflow: anchore/sbom-action 0.24.4.
- The single sign-on redirect refuses protocol-relative paths (`//host`) by itself, not only through the `next` check (CodeQL go/bad-redirect-check).

## [0.11.0] - 2026-11-27

### Changed

- Confirming the email address of a brand-new account (created in the last 24 hours) on a device that is signed out, typically the phone's mail app, offers "Confirm and continue as <name>": confirming then also signs in there, once, so setting up carries on without signing in and entering an emailed code again. Not for email changes, older accounts or used links; the activity log records it as a sign-in via email confirmation. In the browser that is already signed in, the page continues to the welcome flow.
- Sign-in with a code: "Verify and sign in" now sits directly under the code field, above "Email me a new code" and "Use a recovery code instead".
- Welcome flow: "Group chores by area" is off until ticked.
- Invite links: at most 5 open links per household (was 20), and at most 10 created per household per 24 hours, revoked ones included, so creating and revoking links over and over is capped (both in Administration, Limits; the error is `limit_reached` with `reason` `active_invites` or `invites_per_day`, and the app says which).
- Security alerts (on by default, Administration, Sign-up & sign-in): when a household hits the daily invite limit, the admins with a confirmed email address get an email (at most one per household per day), and the Administration page lists such events of the last 7 days. The audit log records each hit (`invite_limit_reached`).
- More security alerts, same mechanism and setting (audit log, Administration notice, email at most once per period):
  - sign-in attacks: 20 failed sign-ins against one account in an hour, or one address failing against 10 different accounts in an hour (credential stuffing);
  - a sign-up wave: 20 new accounts in an hour;
  - changes to admin power: a new admin account, open sign-up switched, proof of work or the outbound-connection guard switched off, and security alerts switched off (that one is always emailed);
  - a household hitting its notification limit, or adding 5 notification channels within an hour (new audit event `channel_create`);
  - an API or app token rejected as too fast 20 times in an hour, or an account creating 5 tokens or app connections in an hour.
- Email delivery failing (3 failed sends in an hour, none succeeding): a warning on the Administration page with the kind of error, an `email_failing` event, and a new public probe `GET /healthz/email` (503 while failing) for uptime monitors. `/readyz` is unaffected.
- Invite links: the app offers 1, 3 or 7 days (default 3; was 1, 7 or 30 with 7). Without `expiresInHours` the API also defaults to 3 days; API clients may still ask for up to 30 days.
- Welcome flow: a last, optional "Security" step to add a passkey or an authenticator app (the same sections as in Settings, Sign-in & security), recommended but skippable.

## [0.10.0] - 2026-11-27

### Added

- A welcome flow for people without a household (`/welcome`, where sign-up now leads): confirm the email address (hosted service), name the household (a name is suggested; time zone detected; areas on), pick starter chores (common ones ticked; first dates spread over the coming week) and create an invite link to copy or share. Every step after the household can be skipped.

### Changed

- Sign-in page: when sign-up is open, a clear "New to Gunkly?" section with a "Create an account" button instead of a small link.

## [0.9.0] - 2026-11-26

### Added

- Settings, Sign-in & security, **Recovery codes**: shows how many unused codes are left (with a "Running low" hint) and creates a new set on request (after confirming it's you; the old codes stop working). Until now codes were only shown once, with the first second factor (on the hosted service when the email address is confirmed), which was easy to miss. API: `GET` / `POST /api/v1/account/recovery-codes`.

- Calendar rules may use `BYSETPOS` (for example `FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=-1` for the last weekday of the month), as AI assistants tend to write them.

### Changed

- When a chore is refused, the API names the field and the reason (`error.path`, `error.reason`, for example `schedule.rrule`: `unsupported part "BYHOUR"`), and the MCP tools say it in their terms (`invalid chore: repeat: ...`) instead of listing every field.
- The chore editor keeps calendar rules it cannot show (set through the API or an AI assistant, for example with `BYSETPOS` or several days of the month) exactly as they are, shown as "Custom rule", instead of simplifying them on save; chore pages show such rules as they are instead of a wrong summary.
- Signing in with a password on an account that has a passkey and email codes no longer emails a code straight away: the passkey is offered first, with "Email me a code instead" and "Use a recovery code instead" next to it. The code is only sent when asked for (API: `sendEmailCode` on `POST /api/v1/session`, `emailSent` in the answer). Adding a passkey when recovery codes already exist now says that they still work.

## [0.8.0] - 2026-11-26

### Added

- `/.well-known/security.txt` (RFC 9116) when `GUNKLY_SECURITY_CONTACT` is set (`mailto:` or `https:`), with an optional `GUNKLY_SECURITY_POLICY` link; Expires is always 180 days ahead.
- Hosted service only: `GUNKLY_PRIVACY_URL` and `GUNKLY_TERMS_URL` show links to its privacy policy and terms at sign-up, when joining by invite and at sign-in. Gunkly refuses to start with them set outside hosted mode, so self-hosted instances never show them.

### Fixed

- The "Confirm your email address" banner disappears in other open tabs as soon as the address is confirmed (and when you return to a tab), instead of staying until a full reload.
- The email with a code to confirm an action while signed in (for example an admin change) is now a "confirmation code", no longer a "sign-in code".
- Paths such as `/.git/config` or `/.env` answer 404 instead of the app page.
- Signing in (or any change) no longer fails with "Your session expired" after signing out in another tab: when the CSRF cookie has vanished, the app fetches a fresh one and retries once.

### Changed

- Docs and compose files use the `latest` image by default; pin a version (`IMAGE_TAG`, or `:0.7` in your compose file) to control when you upgrade.
- Settings, Integrations, Home Assistant: the install steps mention the Marketplace that replaces HACS in Home Assistant 2026.11 (the button works with both).

## [0.7.1] - 2026-11-26

### Fixed

- Things created within the same millisecond (for example webhook deliveries or passkeys) now keep their creation order in lists: new IDs count up within a millisecond instead of using random bits there (they stay unguessable).

## [0.7.0] - 2026-11-26

### Added

- Chore editor: "On" in the Repeat sheet pins a chore to a day. Weekly: pick weekdays (for example Saturday, or Tuesday and Friday). Monthly: a date of the month, or a weekday such as the first or last Saturday. The chore then always lands on that day, also when done early or late (saved as a calendar rule; such rules reopen in this simple form). The first due date moves to the first matching day.
- Update button: choose the updater (Watchtower, Portainer, Dockhand or Other). For Dockhand, Gunkly calls its stack deploy API with `{"pull": true}` and a `dh_` API token, so it pulls the newest image and recreates the stack (`GUNKLY_UPDATE_WEBHOOK_KIND`). The docs have Portainer and Dockhand walkthroughs.

### Changed

- Chore editor: "Describe with AI" is now the stars button inside the name field.

## [0.6.1] - 2026-11-26

### Changed

- The chore editor is one short screen: a large name field with template chips ("More…" lists them all) and "Describe with AI" next to them, people picked as tap-friendly pills, then one row each for area, repeat, reminders and notes. Area, Repeat and Reminders open a sheet (a bottom sheet on phones, a dialog on wider screens). Repeat has Today / Tomorrow, Once / Daily / Weekly / Monthly with an "Every N" stepper, "Custom rule…" for calendar rules, the two ways the next date is counted explained side by side, and a preview of the next dates. Reminders has the preset choices, Custom and the time. "More options" is gone: everything is one tap away. No API change.
- AI chore suggestions send the household's area and member names to the model as a separate JSON message instead of inside the instructions (clearer separation of data and instructions; flagged by CodeQL).

## [0.6.0] - 2026-11-25

### Added

- AI chore suggestions (optional, off by default): on "Add chore", describe chores in your own words and Gunkly proposes chores to check, edit and add; nothing is created until you add it. An admin connects any OpenAI-compatible API under Administration, AI (or `GUNKLY_AI_*`): a paid service, or a model on your own network such as Ollama, LM Studio or llama.cpp, with "Test connection". Only the typed text, area names, members' names and today's date are sent. Each person can ask 50 times a day (new limit `ai_requests_per_user_day`). See [docs/ai-chore-suggestions.md](docs/ai-chore-suggestions.md).
- `GET`/`PATCH /api/v1/admin/config`: settings an admin can change in the app instead of in the environment; a variable set in the environment always wins and locks the field. Secret values are stored encrypted, never returned, and re-encrypted by `gunkly rotate-key`.
- Email, single sign-on, the metrics token and the sign-up difficulty can be set in Administration (new Email page; Sign-up & sign-in; Connections) instead of in the environment, with "Send test email" and "Test the provider". Changes apply at once, no restart. A variable set in the environment still wins and shows locked; email counts as set there only with `GUNKLY_EMAIL_ENABLED=true`. Secrets are stored encrypted and never shown again. See [ADR 0019](docs/adr/0019-configuration-in-the-app.md).
- Optional "Update now" button (Administration, Updates): set an update webhook (or `GUNKLY_UPDATE_WEBHOOK_URL`/`_TOKEN`) and Gunkly asks an updater you run, such as Watchtower, to pull the new version and restart it; the page reloads when the new version answers. Gunkly itself never gets access to Docker. See [docs/updating.md](docs/updating.md).
- A guided setup after the first admin account: it checks the server address and offers email, single sign-on, AI suggestions and the update button one optional step at a time, using the same cards as Administration (which links back to it).

### Changed

- `.env.example` no longer sets placeholder SMTP values, the metrics token or the sign-up difficulty; they are commented out (set them there or in Administration).
- Your own time zone (Settings, Profile) now also decides what counts as today for you in the app: "due today", "overdue", "this week" and the Calendar's today follow it, so the app stays right when you travel or live in a different zone from your household. Due dates, the dates offered when you pick one, reminder times and calendar feeds stay in the household's zone, and API tokens (Home Assistant, MCP apps) keep using the household's zone.

## [0.5.5] - 2026-11-25

### Changed

- Administration, Updates shows the release notes formatted (headings, bullet and numbered lists, bold, italic, code, links) instead of as raw Markdown. The notes are still treated as untrusted text: they are never inserted as HTML, so markup in them shows literally; only http(s) links become clickable (new tab, no referrer); images are not shown.
- SQLite driver `modernc.org/sqlite` 1.63.0 (from 1.62.0).

### Fixed

- `api/openapi.yaml` loads again in strict YAML tools (four texts had an unquoted `: `, one an unquoted `?`), and 18 descriptions that a comma cut short are complete again (error-code lists such as the 403's `forbidden, csrf_failed, step_up_required, token_not_allowed, insufficient_scope`). The API itself is unchanged. CI now checks the spec on every push, not only on pull requests.

## [0.5.4] - 2026-11-25

### Changed

- Gunkly has a new home: <https://github.com/NexusBetweenVoids/gunkly>. Container images are now published as `ghcr.io/nexusbetweenvoids/gunkly` (Docker Hub is unchanged). If you pull from ghcr.io, change the image name in your compose file once; the update check in Administration, Updates already looks at the new address. No other changes.

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
