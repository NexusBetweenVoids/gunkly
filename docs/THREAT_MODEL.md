# Gunkly Threat Model

Covers self-hosted and the hosted service. Hosting infrastructure (servers, networking) is out of scope here.

## Assets

| Asset | Why it matters |
|-|-|
| Household content (chores, notes, history) | Private home information; tenant isolation is the core guarantee |
| Credentials: password hashes, TOTP secrets, recovery codes, sessions, API tokens | Account takeover |
| Notification URLs (bot tokens, webhook secrets) and VAPID private keys | Allow impersonation and spam via the user's channels |
| Mark-done tokens and calendar feed tokens | Unauthenticated capabilities |
| Instance secret key | Decrypts stored secrets |
| Email addresses and display names | Personal data (GDPR) |
| Instance IP and SMTP reputation | Abuse would break delivery for everyone |
| Backups | Contain all of the above |

## Actors

| Actor | Capabilities / intent |
|-|-|
| Anonymous internet | Probes endpoints, sign-up, login, tokens; credential stuffing; spam sign-ups |
| Malicious user / tenant | Valid account; tries ID guessing, cross-household access, SSRF through notification URLs, abuse as spam relay |
| Curious instance admin | Admin panel and DB/host access; should not read household content through the app |
| Compromised notification provider | Sees reminder text and mark-done links sent to it |
| Link-preview bots and mail scanners | Fetch every link automatically |
| Malicious self-hosted service URL | User-supplied endpoint (webhook, ntfy, Gotify) that redirects or returns data to reach internal addresses |

## Trust boundaries

1. Internet to Gunkly (reverse proxy, then app). Client IP trusted only from configured trusted proxies.
2. Browser/API client to server: sessions with CSRF protection, or scoped API tokens.
3. Handlers to data: only through `internal/store`, which enforces household membership and role (ADR 0009).
4. Gunkly to outbound services (Shoutrrr targets, Web Push endpoints, SMTP, update check): crosses to untrusted networks, passes the egress guard (ADR 0006).
5. Third-party relays: Discord, Telegram, Slack, Apple, Google, Mozilla and Microsoft push services receive reminder text or push metadata outside Gunkly's control and, for hosted, outside the EU. Treated as untrusted; mark-done links are single-use and short-lived because they traverse these channels.
6. Email: email-OTP 2FA shares a trust boundary with email-based password reset and verification links. An attacker controlling the mailbox can do all of them (a reset still leaves the second factor in place, but with email-OTP the mailbox is that factor), so email-OTP defends against password reuse and credential stuffing but not mailbox takeover. TOTP is the stronger second factor. Outbound SMTP crosses boundary 4: it dials through the egress guard, so a hosted instance needs a public relay.
7. Operator vs tenant: instance admin is trusted with the instance, not with household content in-app; DB and host access can read data, so secrets are encrypted at rest and operators are in the trust base.

## Threats and mitigations

| Threat | Mitigation (plan section) |
|-|-|
| Cross-household read/write, ID guessing | Single scoped data-access layer, membership checked in-query, UUIDv7 public IDs, parameterised isolation tests per endpoint, depguard + fitness tests (Tenant isolation) |
| Password guessing, credential stuffing | Argon2id, per-IP (IPv6 by /64) and per-IP+account rate limits with exponential backoff, per-account failure backoff across IPs, fail-closed limiter table, no hard lockout, no implicit step-up at sign-in, mandatory 2FA on hosted (Authentication) |
| Session theft / CSRF | Secure httpOnly SameSite cookies, CSRF protection, revocable sessions, 15-minute step-up for sensitive actions, strict CSP (Authentication, Web hardening) |
| Stolen API token | Hashed at rest, revocable, household-bound, least-privilege scopes, token ID in audit log (Authentication) |
| Email-OTP brute force/replay | Single-use, 10-minute expiry, hashed with the challenge ID, 5 attempts per code then invalidated, per-account failure backoff, resends capped by the email limits |
| Account enumeration and mail bombing via reset/verify/email change | Reset request always answers 202 and sends in the background; email-change answers alike for taken addresses; per-IP, per-login and per-account hourly email caps; tokens hashed, single use, expiring (1 h reset, 24 h verification) |
| SSRF via notification URL or push endpoint | Custom dialer with post-DNS IP check, private/loopback/link-local/CGNAT/metadata blocked, 3-redirect HTTPS-only cap, timeouts and body caps; CI tests every Shoutrrr service and Web Push against blocked ranges (Abuse resistance) |
| Malicious self-hosted service URL / redirect | Same guard on every hop; admin-controlled service allowlist; provider-host overrides rejected |
| Push endpoint abuse | Provider domain allowlist, expired subscription pruning |
| Link previews completing chores | GET shows confirmation only; POST with single-use hashed token completes; `Referrer-Policy: no-referrer`; tokens kept out of logs (Links without sign-in) |
| Mark-done token leak via provider | Single-use, max 7 days, scoped to occurrence and recipient/channel, invalidated on state-affecting changes, role downgrade, leave or password change; guests never get one; 5-minute single-use undo token |
| Calendar feed leak | Long random per-user-per-household token, names/areas/dates only, revocable and regenerable |
| Curious admin | Admin panel exposes no household content; admin password reset is disabled on hosted, refused for admin targets, revokes sessions and is visible in the target's own audit log (on self-hosted an admin can still take over a non-admin account, which is an accepted, now non-silent, trust-in-operator trade-off, ADR 0009); secrets encrypted at rest with versioned key and `gunkly rotate-key`; logs scrubbed of secrets |
| Authenticated abuse / data growth | Per-user and per-household write token buckets, areas-per-household cap, completion history retention, atomic households-per-user and invite-use limits |
| Spam relay / sign-up abuse | Limits service caps, SMTP caps, email verification (required to create or join households in hosted mode), ALTCHA proof-of-work (HMAC-signed, expiring, single use; checked before the password is hashed), per-IP sign-up limit |
| XSS / injection | Strict CSP without inline scripts, server-side validation, output encoding, parameterised SQL only, link schemes limited to http/https |
| Clickjacking / MIME sniffing | Frame protection, `X-Content-Type-Options`, HSTS |
| Account lockout by third party | No hard lockouts; CLI recovery for admins; hosted has no manual 2FA reset (warned at setup) |
| Supply chain | Pinned dependencies, justification per dependency, govulncheck, gitleaks, CodeQL, license check, SBOM, cosign-signed images, Renovate/Dependabot |
| Data at rest on disk | SQLite database, -wal/-shm and backup/snapshot files are mode 0600; channel hints show only kind/host, never secret characters |
| Container compromise | Distroless, non-root, read-only rootfs, no shell, one port |
| Data loss / bad upgrade | Expand-and-contract migrations, pre-migration snapshots (SQLite), pre-deploy snapshot, canary with auto-rollback, tested restores |
| Duplicate or storm notifications | Unique delivery keys, scheduler lock, per-household and per-user caps, 24 h overdue cadence |
| Privacy/regulatory | Minimal data, no telemetry, export and delete, EU-only own infrastructure, breach procedure (72 h), processor agreements |
| Inactive account data retention | Optional inactivity warnings and deletion; retention cleanup of audit (90 d) and delivery logs (30 d) |

## Out of scope / accepted risks

- Compromise of the host or database by the operator.
- Mailbox takeover defeating email-OTP and email reset (documented above).
- Content sent to third-party providers once delivered.
- Hosting infrastructure hardening, covered separately in operations documents.
- An external security review is required before the hosted service opens to the public.
