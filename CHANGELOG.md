# Changelog

All notable changes are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added
- HTTP server and `/api/v1` auth API: first-run setup code, sessions with CSRF protection, step-up, TOTP with recovery codes, rate limiting, households, members, invites, audit log, admin users/settings; `/healthz`, `/readyz`; embedded SPA with strict security headers.
- CLI: `serve`, `healthcheck`, `migrate`, `backup`, `admin reset-password`, `admin reset-2fa`, `rotate-key`.
- `internal/limits` service with the PRD defaults, instance and per-household plan overrides.
- Project documentation: PRD, architecture decision records, threat model, security policy and contributing guide.
