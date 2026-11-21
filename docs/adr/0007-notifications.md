# 0007. Notifications

Status: Accepted

## Context
Users want reminders on services they already use, without Gunkly running a messaging stack.

## Decision
- Shoutrrr (maintained `nicholas-fedor` fork) is the delivery library; Web Push is a separate built-in channel (0006).
- Channels exist per user and per household (limit 10 each). URLs are encrypted at rest with a versioned key, never shown in full after saving, and `gunkly rotate-key` re-encrypts.
- **Service allowlist:** admins choose permitted services. Default on: only fixed-public-provider services (Discord, Slack, Telegram, Pushover, Pushbullet, Google Chat, ntfy.sh only); URLs overriding provider hosts are rejected. Off by default: user-chosen-host services (generic webhook, SMTP, MQTT, XMPP, self-hosted ntfy, Gotify, Matrix, Mattermost, Rocket.Chat, Zulip, Home Assistant, Signal, Bark, Teams), paid/incident services (Twilio, PagerDuty, Opsgenie, ...) and `logger`.
- Unique delivery key per reminder; retries with backoff; delivery log (30 days).
- Mark-done links: opening never changes state; only the confirmation POST does. Tokens are random, hashed, single-use, scoped (personal: one occurrence and one non-guest recipient; household: one occurrence and one channel), expire on state-affecting changes and within 7 days. Guests never receive them.
- Quiet hours defer and coalesce; guests get no reminders.

## Consequences
- Reminder text is sent to third-party providers at the user's instruction; this is disclosed in the privacy policy and channel-setup UI.
- Provider outages surface in the delivery log, not as silent drops.
