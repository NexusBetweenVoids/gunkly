# 0002. Stack

Status: Accepted

## Context
One-container self-hosting, low dependency count, and API-first design for future mobile apps and integrations.

## Decision
- Go with the standard library HTTP server; one binary serving the SPA, the `/api/v1` JSON API, iCal feeds and the in-process scheduler.
- Svelte 5 front end compiled to static files and embedded (see 0012).
- SQLite or Postgres (0003, 0004).
- Notifications via Shoutrrr (`nicholas-fedor` fork), pinned (0006, 0007).
- Distroless static image, amd64 and arm64, configured by environment variables; runtime-changeable settings live in the admin panel.
- Public IDs are UUIDv7; passwords use Argon2id.

## Consequences
- One artefact to build, sign and deploy.
- Prefer stdlib; each new dependency needs a PR justification and approval.
- The UI can use only the public API, so third-party clients are first-class.
