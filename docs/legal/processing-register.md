# Record of processing activities (GDPR Art. 30) (template)

> **Template for the maintainer to review. This is not legal advice.** Replace the `[placeholders]`, check it against the real setup, keep it current, and have it reviewed. Whether the operator is formally obliged to keep this record depends on the size and nature of the processing (Art. 30(5)); keeping it is good practice either way. Hosted service only.

Last reviewed: [date]. Next review: [date, at least yearly and on every change to processors or purposes].

## 1. Controller

| Item | Value |
|-|-|
| Controller | [Operator name], [Operator address] |
| Contact | [Contact email] |
| Data protection officer | None appointed [Operator to confirm whether one is required] |
| Joint controllers / representative | None |

## 2. Processing activities

| # | Activity | Purpose | Data subjects | Categories of personal data | Legal basis | Retention | Recipients |
|-|-|-|-|-|-|-|-|
| 1 | Account management | Create and secure accounts, sign-in, two-step verification, account emails | Users (16+) | Username, display name, email, password hash, TOTP secret (encrypted), recovery and email code hashes, language, week start | Art. 6(1)(b) | Until deletion; inactive accounts deleted after 18 months (warning at 12 months) | Hosting provider, email provider |
| 2 | Household chore tracking | The core service | Users, people named in chores | Household names, chores, notes, assignments, completion history, memberships and roles | Art. 6(1)(b) | Until deletion by the household; completion history per chore capped at 1,000 | Hosting provider |
| 3 | Reminders and notifications | Send reminders where the user asks | Users | Notification addresses (encrypted), Web Push endpoints and keys (keys encrypted), reminder text, mute choices, mark-done link tokens (hashed) | Art. 6(1)(b), user's instruction | Until removed by the user; mark-done links expire | User-chosen notification services and browser push services, which may be outside the EU |
| 4 | Delivery log | Retry failed sends, show history | Users | Recipient, channel, status, error text | Art. 6(1)(f) | 30 days | Hosting provider |
| 5 | Sessions | Keep users signed in, show devices | Users | Device label, creation and last-used time | Art. 6(1)(b), (f) | 30 days idle, 90 days absolute | Hosting provider |
| 6 | Security audit log and rate limiting | Detect and limit abuse, show account history | Users, visitors | Event type, time, IP address, account | Art. 6(1)(f) | Audit 90 days; rate-limit counters minutes to hours | Hosting provider |
| 7 | Inactive-account lifecycle | Delete unused accounts | Users | Last-activity time, warning timestamps | Art. 6(1)(f) | Warning timestamps cleared on sign-in; deleted with the account | Email provider, notification services (warnings) |
| 8 | Web server and proxy logs | Operations and security | Visitors | [Operator to complete: IP address, time, request, status] | Art. 6(1)(f) | [Operator to complete] | Hosting provider |
| 9 | Backups | Recovery | As above | Full copy of the database (secrets stay encrypted) | Art. 6(1)(f) | [backup retention] | Backup storage provider |
| 10 | Data subject requests, breach log | Compliance | Users | Contact details, request content | Art. 6(1)(c) | [e.g. 3 years after closing] | None |

Special categories of data (Art. 9) are not intended to be processed. Users are told not to enter them. There is no profiling, no automated decision-making and no direct marketing.

## 3. Processors

| Processor | Service | Location | Agreement (Art. 28) | Sub-processors reviewed |
|-|-|-|-|-|
| [Hosting provider] | Servers, storage | EU ([country]) | [reference, date] | [date] |
| [Email provider] | SMTP relay | [location] | [reference, date] | [date] |
| [Backup provider] | Backup storage | EU ([country]) | [reference, date] | [date] |
| [Other] | [purpose] | [location] | [reference, date] | [date] |

## 4. Transfers to third countries

The application and database run in the EU. Transfers outside the EU happen only when a user adds a notification service or browser push subscription and Gunkly delivers a reminder there at the user's instruction (Art. 49(1)(b) and the user's own choice; the services are independent of the operator). Providers include, depending on the user's choice, Telegram, Discord, Slack, ntfy, Gotify, Pushover, and the Apple, Google, Mozilla and Microsoft push services. [Operator to document the transfer mechanism for any processor outside the EU.]

## 5. Technical and organisational measures (Art. 32)

Summary; details are in `docs/THREAT_MODEL.md`.

- Tenant isolation enforced in one data-access layer with isolation tests; the instance admin cannot read household content through the app.
- Passwords hashed with Argon2id; mandatory two-step verification on the hosted service; rate limiting and backoff; CSRF protection; secure, HTTP-only, same-site cookies.
- Secrets (notification addresses, TOTP secrets, push keys) encrypted at rest with a versioned key and a rotation command; tokens stored only as hashes.
- Outbound requests go through an egress guard against requests to internal addresses.
- TLS for all connections; [Operator to complete: disk encryption, access control to servers, key storage, backup encryption].
- Retention cleanup runs daily; account deletion is one transaction.
- Logs do not contain passwords, tokens, notification addresses, push keys or session identifiers.
- Breach procedure: see [breach-procedure.md](breach-procedure.md). Incident handling: [incident-response.md](incident-response.md).
- External security review before public launch.
