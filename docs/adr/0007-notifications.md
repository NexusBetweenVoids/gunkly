# 0007. Notifications

Status: Accepted

## Context
Users want reminders on services they already use, without Gunkly running a messaging stack.

## Decision
- Shoutrrr (maintained `nicholas-fedor` fork) is the delivery library; Web Push is a separate built-in channel (0006).
- Channels exist per user and per household (limit 10 each). URLs are encrypted at rest with a versioned key, never shown in full after saving, and `gunkly rotate-key` re-encrypts.
- **Service allowlist:** admins choose permitted services. Default on: only fixed-public-provider services (Discord, Slack, Telegram, Pushover, Pushbullet, Google Chat, ntfy.sh only); URLs overriding provider hosts are rejected. Off by default: user-chosen-host services (generic webhook, SMTP, MQTT, XMPP, self-hosted ntfy, Gotify, Matrix, Mattermost, Rocket.Chat, Zulip, Home Assistant, Signal, Bark, Teams), paid/incident services (Twilio, PagerDuty, Opsgenie, ...). Shoutrrr's `logger` service is never allowed: it would write reminder text and mark-done links into the server log. Allowlist entries are *kinds* (`internal/notify`): mostly the Shoutrrr scheme, with aliases merged (`hangouts` = `googlechat`, `mqtts` = `mqtt`, `xmpps` = `xmpp`, `generic+https` = `generic`) and ntfy split into `ntfy` (ntfy.sh only) and `ntfy-selfhosted`. Fixed providers accept only presentation query parameters; any host, port, API-URL, TLS or custom-URL (`service+https://`) override is rejected.
- Unique delivery key per reminder: SHA-256 of chore, occurrence date, reminder kind, fire instant, channel and recipient; quiet-hours-deferred reminders are keyed by their release instant without the kind, so they coalesce into one (the most urgent kind wins). Retries back off 1, 2, 4, ... minutes (max 1 h), 6 attempts. Delivery log (30 days). A pending delivery whose occurrence was completed, snoozed or skipped, or whose recipient muted the chore or left, is dropped at send time. Mark-done tokens are minted at send time (never stored in plaintext).
- Household channels are managed by members and owners (not guests), like the chores whose reminders they carry.
- Quotas (per household 100/h and 250/day, per user 500/day, from `internal/limits`) are checked when deliveries are queued; reminders over quota are logged as failed ("quota exceeded"), never retried.
- Built-in reminder presets: `gentle` (on the due date 09:00), `nag` (day before 18:00, due date 09:00, daily overdue 09:00), `none`; `custom` uses the chore's own reminders. A chore's empty preset uses the household default; new households default to `gentle` (existing ones keep their value).
- Mark-done links: opening never changes state; only the confirmation POST does. Tokens are random, hashed, single-use, scoped (personal: one occurrence and one non-guest recipient; household: one occurrence and one channel), expire on state-affecting changes and within 7 days. Guests never receive them.
- Quiet hours defer and coalesce; guests get no reminders.
- Email is a third target kind next to channels and push subscriptions: the user's own verified address, opted in with `users.email_reminders` (cleared when the address changes, ignored while unverified or when the instance has no email). Deliveries carry `via_email`; the delivery key uses the target `email` plus the user ID. The send re-checks the opt-in and verification, and the instance-wide hourly email cap (ADR 0008) applies; the per-account mail caps do not, as they protect user-triggered mail and the notification quotas already bound reminders.
- Channel URLs are write-only over the API: views carry the kind, label and a hint stored at save time: the kind (plus a fixed provider host), or the server host for services where the user chooses one; never a host part that is a key, ID or phone number. The admin allowlist and the egress-guard switch are instance settings read at use (channel validation, test sends, scheduler); hosted mode forces the guard on. Test messages go through the real sender and appear in the delivery log with a redacted error. Calendar feeds may be created by guests (read-only data: names, areas, dates).

## Consequences
- Reminder text is sent to third-party providers at the user's instruction; this is disclosed in the privacy policy and channel-setup UI.
- Provider outages surface in the delivery log, not as silent drops.
- Every Shoutrrr release that can inject both the HTTP client and the dial function (`SenderOptions.DialContext`, v0.20.0+) requires Go 1.27, so Gunkly targets Go 1.27 and pins Shoutrrr v0.25.0. `TestSendersBlocked` proves every enabled and sample admin-enableable service is blocked from loopback and metadata addresses; an upgrade that bypasses the guard fails the build.
