# 0012. Deployment and testing

Status: Accepted

## Context
AI agents will write most changes, so verification, architectural constraints and release promotion must be automated.

## Decision
- **Front end:** plain Svelte 5 + Vite SPA (no SvelteKit) with a tiny hash-free history router, built to `web/dist` and embedded in the Go binary via `embed.FS`. It talks only to `/api/v1`. Root-path hosting only (sub-path is Later).
- **CI:** GitHub Actions. Trunk-based on `main`, small single-task PRs.
- **Pipeline:** immutable multi-arch cosign-signed images built once and promoted unchanged. (1) every PR: build the distroless image, boot on SQLite and Postgres, run migrations, check `/healthz` and `/readyz`, full tests and Playwright; (2) staging auto-deployed from `main` with synthetic data and capture sinks (Mailpit, mock webhook) after an upgrade test from the previous release DB; (3) production on a signed `vX.Y.Z` tag with maintainer approval, pre-deploy snapshot, graceful SIGTERM rollout, synthetic canary and auto-rollback.
- **Migrations:** expand-and-contract across two releases for breaking changes.
- **Test layers:** unit/property/fuzz (scheduling, tokens, quiet hours, limits, URL validation, parsers); dual-DB integration and isolation with `-race`; security and egress tests (govulncheck, gitleaks, CodeQL, license check, DAST); Playwright + axe-core (mobile/desktop, under-3-tap completion, undo, RTL, themes); OpenAPI breaking-change and upgrade-compat checks.
- **Leanness gates:** deadcode, strict golangci-lint, svelte-check, unused exports and translation keys; binary/image/bundle/dependency budgets; fitness tests (handlers never import `database/sql`, frontend never bypasses `/api/v1`, every route declares auth and tenant middleware).
- **Workflow:** task spec checked against PRD and ADRs; `make check` locally; separate read-only AI review against `AGENTS.md` and the threat model; monthly dependency and pruning audit.
- **Supply chain:** pinned lockfiles, SBOM and cosign signatures on releases, Renovate or Dependabot.

## Consequences
- `make check` is the single local gate matching CI.
- Dependency changes need explicit justification and human approval.
