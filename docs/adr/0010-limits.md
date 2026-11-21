# 0010. Limits

Status: Accepted

## Context
Hosted needs abuse caps and SMTP/IP reputation protection; self-hosters want control.

## Decision
All usage limits go through `internal/limits`. Defaults are set in the admin panel (table in `docs/PRD.md`), and the data model allows a per-household override ("plan" record) without code changes. Every new countable resource must use the service. Self-hosters may change any value; abuse limits stay on by default. Rate limits apply per IP and per IP + account with exponential backoff; accounts are never hard-locked.

## Consequences
- No ad-hoc constants for caps in handlers.
- A future higher hosted tier needs only data, not code.
