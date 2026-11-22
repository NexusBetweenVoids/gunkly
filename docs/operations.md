# Operations: staging, production and upgrades

This describes the generic, provider-agnostic tooling in this repository. Hosting infrastructure, domains, reverse proxies, production configuration and secrets are **not** kept here; they live with the operator (see ADR 0012).

## Pipeline at a glance

| Stage | Trigger | What runs |
|-|-|-|
| CI | every PR and push to `main` | tests, Playwright, container build and boot, and the `upgrade` job (below) |
| Staging | CI succeeded on `main` (`staging.yml`) | build `ghcr.io/nexusbetweenvoids/gunkly:main-<sha>`, ship `deploy/staging/`, `scripts/deploy.sh`, canary |
| Production | `vX.Y.Z` tag, after the Release workflow succeeded (`deploy.yml`) | approval, cosign verify, `scripts/deploy.sh`, canary, automatic rollback |

Both deploy workflows do nothing (with a notice in the run log) until their environment secrets exist.

## Tooling

- `deploy/staging/compose.yml`: Gunkly on Postgres, Mailpit (SMTP capture, UI on `127.0.0.1:8025`) and a mock webhook receiver (`deploy/staging/webhook`, a standalone ~40-line Go program; `GET /_requests` lists what it captured). Copy `staging.env.example` to `staging.env` on the host and fill in staging-only secrets.
- `scripts/seed-staging.sh BASE_URL`: creates the first admin, demo users, households, chores and a webhook channel through the public API (fake data, `SEED_PASSWORD` required). It also turns the **egress guard off** so the private Mailpit and webhook containers are reachable. This is for staging only; production keeps the guard on.
- `scripts/canary.sh BASE_URL`: the synthetic canary (below).
- `scripts/deploy.sh`: the rollout (below).
- `scripts/upgrade-test.sh PREV_TAG NEW_IMAGE`: the upgrade test (below).

Local tools needed: `bash`, `curl`, `jq` (all scripts), `docker` (upgrade test, deploy target), `python3` (TOTP canary only).

## Canary

`scripts/canary.sh BASE_URL` checks `/healthz` and `/readyz`, signs in as a dedicated canary account (session cookie plus `X-CSRF-Token`), creates a chore, completes it, undoes the completion, deletes the chore and signs out. It exits non-zero on the first failure and removes a half-created chore. Credentials come from `CANARY_LOGIN`, `CANARY_PASSWORD` and, if the account has TOTP, `CANARY_TOTP_SECRET` (base32; the code is computed with `python3`). Nothing secret is printed.

Create the canary account once per environment (Admin, Users) as an ordinary non-admin user without data of value. Without 2FA it needs no extra secret; with 2FA keep the TOTP secret only in the environment secrets. The canary creates a household named "Canary" on first use if the account has none. Mind the sign-in rate limit: do not run the canary in a tight loop.

## Rollout (`scripts/deploy.sh`)

Configured through the environment: `IMAGE_TAG`, `COMPOSE_FILE`, `BASE_URL`, the canary variables, and optionally `DEPLOY_HOST` (SSH; unset runs locally), `DEPLOY_SSH_OPTS`, `DEPLOY_DIR`, `COMPOSE_ENV_FILE`, `DB_KIND` (`sqlite` or `postgres`). The compose file must take its image tag from `${IMAGE_TAG}` and set `stop_grace_period` of at least 35 s on the Gunkly service (the shutdown timeout is 30 s); the script refuses otherwise. The stock `compose.sqlite.yml` and `compose.postgres.yml` meet both requirements (`${IMAGE_TAG:-latest}`, 40 s grace period).

1. **Snapshot** the running version: SQLite uses `gunkly backup` (a timestamped file in `/data/backups` of the data volume); Postgres uses `pg_dump -Fc` through the postgres container into `SNAPSHOT_DIR` on the host. A failed snapshot aborts before anything changes. The first deploy has nothing to snapshot.
2. **Pull** the new tag (a failed pull changes nothing).
3. **`docker compose up -d`**: the old container receives SIGTERM, drains for up to 30 s, and the new one starts and auto-migrates.
4. **Wait for `/readyz`** (default 120 s).
5. **Canary.**
6. On any failure after step 3 the image tag is **rolled back** to the previously running tag, readiness is awaited again, and the script exits non-zero.

### Restore (manual, never automatic)

Migrations are forward-only, so the script never restores data by itself. A rollback of the image is safe only when the new schema is compatible with the old release, which is what expand-and-contract guarantees (below). If the old release misbehaves on the migrated database, restore the snapshot yourself:

1. `docker compose -f <file> stop gunkly`
2. SQLite: copy the snapshot over `gunkly.db` in the data volume and delete `gunkly.db-wal` and `gunkly.db-shm`. Postgres: `pg_restore --clean --if-exists -U gunkly -d gunkly <dump>`.
3. `IMAGE_TAG=<previous> docker compose -f <file> up -d`

Everything written after the snapshot is lost. Prefer fixing forward with a patch release when possible.

## Expand-and-contract rule

A breaking schema change spans two releases:

1. **Expand** (release N): add the new column/table/index and write to both; the old release still works on the new schema. Do not drop, rename or tighten anything yet.
2. **Contract** (release N+1 or later): once release N is live everywhere and no rollback to N-1 is expected, remove the old structures.

This is what makes the image-only rollback safe and what the upgrade test exercises.

## Upgrade test

`scripts/upgrade-test.sh PREV_TAG NEW_IMAGE` starts the previous release on a fresh SQLite volume, seeds it through the API (no egress changes, no webhook), stops it with SIGTERM (it must exit 0), starts `NEW_IMAGE` on the same volume (auto-migrate), checks `/healthz`, compares the seeded chores before and after, and runs the canary. The CI job `upgrade` runs it on pull requests against the latest non-prerelease release and is skipped with a notice while no release exists. Run it locally with `scripts/upgrade-test.sh 1.2.3 gunkly:dev`. Staging's own upgrade path (staging runs the previous `main` build with its seeded Postgres data, so every deploy there is an upgrade) is exercised by the same rollout script.

## Maintainer setup (one time)

Settings, Environments:

- **`staging`**: no reviewers needed (optional restriction to `main`).
- **`production`**: required reviewers (the maintainer), and a deployment branch/tag rule limiting it to release tags.

Secrets, set per environment (staging and production values must differ):

| Secret | Meaning |
|-|-|
| `DEPLOY_SSH_KEY` | private key of a deploy-only user that can run `docker compose` on the host |
| `DEPLOY_HOST` | `user@host` (add `-p` ports via an ssh config on the host side if needed) |
| `DEPLOY_KNOWN_HOSTS` | the host's `known_hosts` line(s), from `ssh-keyscan` verified out of band |
| `DEPLOY_DIR` | directory on the host holding the compose and env files (staging: where `deploy/staging/` is copied) |
| `DEPLOY_BASE_URL` | public https URL for `/readyz` and the canary |
| `CANARY_LOGIN`, `CANARY_PASSWORD` | the canary account |
| `CANARY_TOTP_SECRET` | optional, only when the canary account uses TOTP |

Optional production variables: `COMPOSE_FILE` (default `compose.yml`), `COMPOSE_ENV_FILE`, `DB_KIND`. On the staging host create `staging.env` from `deploy/staging/staging.env.example`, then seed once: `SEED_PASSWORD=... SEED_COMPOSE_FILE=compose.yml SEED_COMPOSE_ENV_FILE=staging.env CANARY_LOGIN=... CANARY_PASSWORD=... scripts/seed-staging.sh https://staging.example.org` (run where the Docker host is reachable, or pass `SETUP_CODE`).

The release image must be public or the host logged in to ghcr.io. The production job verifies the image's cosign signature against the Release workflow identity before deploying. Manual redeploy or rollback to an older release: run the "Deploy production" workflow with the tag (`workflow_dispatch`); it goes through the same approval, snapshot and canary.
