# 0019: Configuration in the app, environment wins

Status: accepted (0.6.0)

## Context

Self-hosters configured email, single sign-on and metrics only through environment variables, so every change meant editing `docker-compose.yml` and restarting the container. Most of these settings are not needed to start the server, and an admin page is easier.

## Decision

- Settings that are not needed before the app can serve a page move into Administration: email (SMTP), single sign-on (OpenID Connect), the AI endpoint, the metrics token and the sign-up proof-of-work difficulty. They live in the `settings` table under `cfg.` keys and are defined once in `internal/api/config.go` (type, environment variable, default, validation).
- **The environment always wins.** A variable that is set locks its field, which the admin page shows read-only with the variable's name. Email and single sign-on are groups: one of their variables locks the group, so values never mix. Email counts as set only with `GUNKLY_EMAIL_ENABLED=true` (the SMTP variables never did anything without it, and many `.env` files carry placeholders).
- **Stay environment-only:** the database URL and secret keys (needed to read anything; secrets in the database must not be encrypted with a key stored next to them), `GUNKLY_BASE_URL` (links, cookies, passkeys and SSO depend on it; when it changes the admin should know to change the deployment), hosted mode, auto-migrate, trusted proxies (a wrong value lets clients fake their IP and dodge rate limits), the listen address and log level.
- **Secrets** (SMTP password, client secret, AI key, metrics token) are encrypted with the keyring, bound to their key name as associated data, never returned by the API or logged, and re-encrypted by `gunkly rotate-key`. Changes need a recent step-up and are audited by key name, never by value.
- **No restart:** email, SSO, metrics and proof of work are rebuilt from the effective configuration after each change and swapped atomically (`internal/api/runtime.go`); a request reads the snapshot once.
- Admin-set endpoints that point to the admin's own network (an AI model, an SSO provider, an SMTP relay) follow the existing rules: the AI endpoint and the SSO issuer may be private (the admin chose them); SMTP goes through the egress guard as before.

## Consequences

- One more place to look: the admin pages say when a value comes from the environment.
- A stolen admin session can now change SMTP or SSO settings. Mitigated by step-up for every change, the audit log, and secrets that cannot be read back. Hosted mode never offers single sign-on.
