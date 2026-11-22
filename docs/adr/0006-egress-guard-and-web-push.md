# 0006. Egress guard and Web Push

Status: Accepted

## Context
Users supply webhook/notification URLs and Web Push endpoints; the server would call them, enabling SSRF against internal networks and cloud metadata.

## Decision
- All outbound notification and Web Push traffic uses Gunkly's own HTTP client and dial function (`internal/egress`). A `net.Dialer` `Control` hook checks the actual connected IP after DNS resolution and blocks private, loopback, link-local, CGNAT and cloud-metadata ranges (IPv4 and IPv6).
- Strict timeouts and response-size caps; at most 3 redirects, HTTPS only, each hop re-checked.
- Always on for hosted; switchable for self-hosters.
- Shoutrrr accepts a custom HTTP client and dial context for every service (verified Nov 2026); the Shoutrrr version is pinned and CI sends a test notification for every service and Web Push to blocked addresses, failing the build on any bypass.
- Web Push endpoints must also match a provider allowlist (`fcm.googleapis.com`, `web.push.apple.com`, `updates.push.services.mozilla.com`, `*.notify.windows.com`). VAPID private keys are encrypted at rest and covered by key rotation. `404`/`410` subscriptions are pruned.

## Web Push implementation
Built with the standard library and `golang.org/x/crypto` only (no new module): `internal/webpush`.
- **Encryption:** RFC 8291 `aes128gcm` (RFC 8188 coding): ephemeral ECDH P-256 key per message, HKDF-SHA256, one record of 4096 bytes (plaintext at most 3993 bytes). The tests reproduce the RFC 8291 appendix A vector byte for byte.
- **Authentication:** RFC 8292 VAPID, ES256 JWT (`aud` = the endpoint's origin, `exp` 12 h, `sub` = the instance's https `GUNKLY_BASE_URL`, else the project URL), `Authorization: vapid t=..., k=...`. The key pair is generated on first use and stored in the `settings` table as `vapid_private_key`, sealed by `auth.Keyring` with associated data `vapid:1` (the key-pair generation; the keyring version is already in the ciphertext prefix). `gunkly rotate-key` re-encrypts it with the subscription keys. Concurrent first use on several instances converges on the first stored key.
- **Subscriptions:** `push_subscriptions` (migration 0001); the endpoint is validated with `egress.WebPushAllowed` at registration; `p256dh` and `auth` are sealed with associated data `push:<id>`; at most `push_subscriptions_per_user` (10) per user. An endpoint identifies a browser: registering one that exists replaces the old row, so reminders follow whoever is signed in there. The endpoint is never returned (a device finds its own entry by the SHA-256 of its endpoint).
- **Delivery:** the scheduler treats each subscription as one more target of a reminder, next to channels. The delivery key uses `push:<subscription id>` where channels use their ID. Recipients, mute, assignee, guest exclusion, quiet hours (deferral and coalescing) and quotas are the same code path as for channels. Requests go through the egress-guarded client (the guard follows the instance setting at send time, forced on when hosted) with `TTL: 86400` and `Urgency: high`. The payload is `{title, body, url}` (localised text, `url` = `/chores/<id>`, a same-origin path the service worker re-validates); it carries no mark-done link. `404`/`410` delete the subscription and fail the delivery without retry; other errors retry with the usual backoff.
- **Allowlist vs guard:** the allowlist is checked when a subscription is registered; the dial-time guard is the SSRF control at send time. Tests send to a loopback fake push service only with the guard disabled and assert that with the guard on no request arrives.
- **Privacy:** the push service of the browser vendor learns that a message was sent to the device and when, not what it says (the payload is end-to-end encrypted to the browser). The Account screen says so.

## Consequences
- Pinned Shoutrrr upgrades need CI egress tests to pass.
- Self-hosters on private networks can disable the guard knowingly.
- New push providers require an allowlist change.
