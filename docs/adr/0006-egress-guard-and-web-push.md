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

## Consequences
- Pinned Shoutrrr upgrades need CI egress tests to pass.
- Self-hosters on private networks can disable the guard knowingly.
- New push providers require an allowlist change.
