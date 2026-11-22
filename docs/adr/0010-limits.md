# 0010. Limits

Status: Accepted

## Context
Hosted needs abuse caps and SMTP/IP reputation protection; self-hosters want control.

## Decision
All usage limits go through `internal/limits`. Defaults are set in the admin panel (table in `docs/PRD.md`), and the data model allows a per-household override ("plan" record) without code changes. Every new countable resource must use the service. Self-hosters may change any value; abuse limits stay on by default. Rate limits apply per IP and per IP + account with exponential backoff; accounts are never hard-locked.

Additional rules: `AreasPerHousehold` is enforced under the household lock inside `CreateArea`, `HouseholdsPerUser` inside the `CreateHousehold` transaction, and `CompletionsPerChore` is retention (the oldest non-reverted completions beyond N are pruned in the completing transaction). API tokens are limited by `api_tokens_per_user` (counted in the creating transaction, active tokens only). Authenticated unsafe requests are also throttled by a per-user and a per-household token bucket (constants in `internal/limits`). The login-rate limiter reads `login_attempts_per_minute` from settings (refreshed every few seconds), keys IPv6 clients by /64, never evicts keys in backoff and refuses new keys when its table is full of active ones.

## Consequences
- No ad-hoc constants for caps in handlers.
- A future higher hosted tier needs only data, not code.
