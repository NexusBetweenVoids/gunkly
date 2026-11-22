# 0014. API tokens

Status: Accepted

## Context
Integrations (a Home Assistant integration, an MCP server for AI assistants, scripts) need to read and complete chores without a browser session. A session cookie is the wrong credential: it needs CSRF handling, it can do everything the user can (including deleting the account), and it expires by idle time. The credential must be least-privilege, household-bound and revocable.

## Decision
- **Token:** `gk_` + `ids.Token()` (43 base64url characters), sent as `Authorization: Bearer gk_...`. Only its SHA-256 hash is stored (`api_tokens.token_hash`); the secret is shown once, in the creation response. A user has at most 20 active tokens (`api_tokens_per_user`, `internal/limits`).
- **Scopes**, stored space-separated, with no implication between them:
  - `chores:read`: households list and detail (bound ones), chores list and get, completion history, calendar, areas list, templates, reminders get, reminder presets.
  - `chores:complete`: complete, snooze, skip, pause, resume, revert a completion.
  - `chores:write`: create, update, archive, unarchive and delete chores, set reminders, mute and unmute.
  - Never reachable with a token: account, sessions, 2FA, password, email, members, invites, household settings or deletion, areas writes, notification channels, push, calendar feeds, export and import, admin, token management.
- **Households:** a token is bound to the households chosen at creation (`api_token_households`), each of which the owner must belong to at that time. Effective permission is the scope AND the owner's current role in that household: a guest can only read, a demotion applies to existing tokens at once, and a household that is not bound, or that the owner has left, answers `404` exactly like an outsider. The household list shows only bound households. Leaving a household or being removed from it deletes the token's binding to it, so rejoining later does not revive old tokens (a demotion keeps the binding; the role limits apply).
- **Policy mechanism:** every route that needs a session (session, household and admin routes) must declare its token behaviour in its policy, either `.withToken(scope)` or `.noToken()`. A route that declares neither fails at registration, so a new route cannot silently become token-accessible or silently omit the decision. Admin routes and step-up routes can never declare a scope. `TestEveryRouteRefusesTokensOrRequiresItsScope` walks the whole route table against the contract table, and `TestOpenAPIDocumentsTokenScopes` keeps `api/openapi.yaml` in step.
- **Bearer requests:** a request with `Authorization: Bearer` on a session, household or admin route is authenticated by the token alone. It never reads or sets cookies, is exempt from CSRF (the credential is not ambient: a cross-site page cannot make a browser attach it), and a bad token is a `401` even when a valid session cookie is also present. Other credentials are unaffected: public routes ignore the header and `/metrics` keeps its own token.
- **Failures:** missing, malformed, unknown, expired and revoked tokens all answer `401 unauthenticated` with `WWW-Authenticate: Bearer` and the same body (no oracle). A route that refuses tokens answers `403 token_not_allowed`; a missing scope `403 insufficient_scope`.
- **Introspection:** `GET /api/v1/token` (bearer only, any scope) returns id, name, scopes, bound households with the owner's current role, the owner's id and display name and the expiry, so an integration can configure itself.
- **Management:** `GET/POST /api/v1/account/tokens` and `DELETE /api/v1/account/tokens/{id}` are session-only; creation needs step-up (ADR 0013). Revoking sets `revoked_at`; the daily retention job deletes revoked and expired rows.
- **Credential changes:** a password change or reset (account, admin, CLI or email) and removing a second factor (authenticator app, email codes, `gunkly admin reset-2fa`) revoke all of the account's active tokens in the same transaction, like they sign out its sessions, because a token minted with the old credentials must not outlive them. The audit log records one `token_revoke` with `via` (`password_change` or `2fa_disable`), `count` and the token ids.
- **Rate limits:** a bucket per token (120/minute, burst 60) and one per client IP over all bearer requests (300/minute, burst 100; it also bounds token guessing), on top of the per-user and per-household write buckets.
- **Last use:** `last_used_at` is written at most once a minute (a conditional `UPDATE`), and counts as account activity for the inactive-account job.
- **Attribution and audit:** a completion made with a token has source `api_token` and `token_id`. The audit log records `token_create`, `token_revoke` and, for every completion by token, `token_complete`; any other audited action performed with a token also carries `token` (its id, never the secret) in its metadata.
- **Hosted:** creating and using tokens is subject to the same verified-email and second-factor gate as household access.

## Consequences
- A leaked token exposes only the bound households' chores within the chosen scopes and the owner's role, and is revocable at once; it cannot change who is in a household, reach notification URLs, export data or reach the account.
- Because scopes do not imply one another, a read-only integration cannot complete chores even if the user could.
- Every new route forces an explicit token decision, at the cost of one more word per route.
- Per-token and per-IP buckets are in memory and per instance, like the other limiters (ADR 0013).
- OAuth 2.1 for remote hosted MCP remains future work; the same scopes would apply.
