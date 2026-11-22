# 0012. Deployment and testing

Status: Accepted

## Context
AI agents will write most changes, so verification, architectural constraints and release promotion must be automated.

## Decision
- **Front end:** plain Svelte 5 + Vite SPA (no SvelteKit) with a tiny hash-free history router, built to `web/dist` and embedded in the Go binary via `embed.FS`. It talks only to `/api/v1`. Root-path hosting only (sub-path is Later).
- **CI:** GitHub Actions. Trunk-based on `main`, small single-task PRs.
- **Pipeline:** immutable multi-arch cosign-signed images built once and promoted unchanged. (1) every PR: build the distroless image, boot on SQLite and Postgres, run migrations, check `/healthz` and `/readyz`, full tests and Playwright; (2) staging (deploy tooling lives outside the public repo) auto-deployed from `main` with synthetic data and capture sinks (Mailpit, mock webhook) after an upgrade test from the previous release DB; (3) production on a signed `vX.Y.Z` tag with maintainer approval, pre-deploy snapshot, graceful SIGTERM rollout, synthetic canary and auto-rollback.
- **Migrations:** expand-and-contract across two releases for breaking changes.
- **Test layers:** unit/property/fuzz (scheduling, tokens, quiet hours, limits, URL validation, parsers); dual-DB integration and isolation with `-race`; security and egress tests (govulncheck, gitleaks, CodeQL, license check, DAST); Playwright + axe-core (mobile/desktop, under-3-tap completion, undo, RTL, themes); OpenAPI breaking-change and upgrade-compat checks.
- **Leanness gates:** deadcode, strict golangci-lint, svelte-check, unused exports and translation keys; binary/image/bundle/dependency budgets; fitness tests (handlers never import `database/sql`, frontend never bypasses `/api/v1`, every route declares auth and tenant middleware).
- **Workflow:** task spec checked against PRD and ADRs; `make check` locally; separate read-only AI review against `AGENTS.md` and the threat model; monthly dependency and pruning audit.
- **Supply chain:** pinned lockfiles, SBOM and cosign signatures on releases, Renovate or Dependabot.

## Implementation notes
- **Workflows:** `ci.yml` (Go SQLite and Postgres, web, Playwright `e2e`, `openapi`, gitleaks, licenses, container), `codeql.yml`, `fuzz.yml` (nightly, 2 min per target; `make fuzz-check` fails if a `Fuzz*` target is missing from the matrix), `dast.yml` (nightly OWASP ZAP baseline against the read-only container after first-run setup, fails on High alerts), `release.yml`. Third-party actions are pinned by commit SHA; each job declares least-privilege permissions.
- **Budgets:** image at most 40 MB (CI, with binary size reported); at most 6 direct Go requires and 9 web devDependencies (`scripts/dep-budget.sh`).
- **Fitness (frontend):** `scripts/fitness-web.sh` fails on network access outside `web/src/lib/api.ts`, inline `style=` attributes, and physical CSS properties (use logical ones for RTL).
- **API compatibility:** `oasdiff breaking` on `api/openapi.yaml` between PR base and head fails on ERR-level changes (the spec only describes `/api/v1`).
- **Release:** on a `vX.Y.Z` tag, a multi-arch image is pushed to `ghcr.io/nexusbetweenvoids/gunkly`, signed by digest with cosign keyless (GitHub OIDC), the SPDX SBOM is attested and attached to the GitHub release together with static binaries and `SHA256SUMS`. Release notes come from the matching CHANGELOG section. Staging and production deploy tooling (workflows, rollout script, staging stack, hosted-service documents) lives outside the public repository, following the rule that hosting infrastructure lives elsewhere; only the generic `scripts/canary.sh` and `scripts/upgrade-test.sh` stay here.
- **Pending:** the `/healthz` boot probe in the container job stays `continue-on-error` until the maintainer decides.

## Consequences
- `make check` is the single local gate matching CI.
- Dependency changes need explicit justification and human approval.
