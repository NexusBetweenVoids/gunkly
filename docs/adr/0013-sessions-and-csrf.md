# 0013. Sessions, CSRF and rate limiting

Status: Accepted

## Context
Browser sessions, cross-site request forgery and brute force need one coherent, small design that works for a single self-hosted instance and does not depend on infrastructure the operator may not run.

## Decision
- **Sessions:** a random token in an HttpOnly cookie; only its SHA-256 hash is stored, so a database leak yields no usable sessions. Expiry is sliding (idle, default 30 days) and absolute (from sign-in, default 90 days). Both are instance settings, `sessionIdleDays` (1-365) and `sessionMaxDays` (1-730, at least the idle value), editable by the admin, read on use (cached for a few seconds) and applied to existing sessions. The stored `expires_at` is only a hard ceiling, and the daily retention job also deletes sessions past either limit. Sensitive actions need step-up (password or TOTP re-verification) within 15 minutes.
- **CSRF:** double-submit token (a readable cookie echoed in a header, rotated at sign-in) on every unsafe method, plus an `Origin` / `Sec-Fetch-Site` check that rejects cross-site requests. Mark-done and undo forms are authenticated by an unguessable token in the URL and have no session, so they are exempt from the cookie check and get the cross-site check only.
- **Rate limiting:** in-memory token buckets per instance, keyed by client IP (and IP + account for login), with exponential backoff and no hard lockout. Buckets: login 5/minute, mark-done 30/minute, iCal 60/minute, plus setup, 2FA and password buckets. State is lost on restart and not shared between instances; a shared Postgres-backed limiter is part of 1B (multi-instance).
- **Account throttling:** failed password and TOTP/recovery attempts are also counted per account across all IPs (5 free, then a doubling delay capped at 15 minutes, reset on success; unknown logins are counted by login string). This is in memory and per instance, so behind several app containers each instance counts separately until the shared limiter ships.
- **Step-up:** signing in does not grant step-up; sensitive actions always ask for re-verification. Enabling or disabling TOTP and any password change revoke the user's other sessions.
- **First-run setup code:** the one secret Gunkly writes to its logs. With no users, startup generates a one-time code, logs it and keeps only its hash in memory. It is accepted only while no account exists, so once the admin is created it is worthless. The log is visible only to the operator, which is the person who should create the admin; this avoids default credentials and any prior-access requirement. Passwords, tokens, notification URLs and session IDs are never logged.

## Consequences
- Revoking sessions is a row delete; changing the lifetimes needs no migration.
- Per-instance limits are weaker behind several app containers until the shared limiter ships.
- A missed setup code is recovered by restarting (a new code is generated).
