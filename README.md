# Gunkly

A self-hostable chore tracker for recurring household chores and home maintenance (gutters, washing-machine filter, descaling the kettle). Flexible schedules, optional reminders on your own messaging services, and a calm interface on phone and desktop. One Go binary, one container, SQLite or Postgres. No telemetry.

**Status: pre-alpha, under construction.** Nothing here is ready for use yet.

## Quick start

Placeholders until the first release:

```sh
# SQLite (one container)
docker compose -f compose.sqlite.yml up -d

# Postgres (Gunkly plus Postgres)
docker compose -f compose.postgres.yml up -d
```

On first start, find the setup code in the container logs and open the app to create the admin account.

## SQLite or Postgres

| Choose | When |
|-|-|
| **SQLite** (default) | One household, family and friends, or a small community instance; one container and simple one-command backups |
| **Postgres** | A larger or public instance; you already run and back up Postgres; you want the database on its own server, or more than one app container later |

You can switch later with `gunkly migrate-db --from sqlite:///data/gunkly.db --to postgres://...`.

## Backups

Never copy the data volume while Gunkly is running; SQLite's write-ahead log can make the copy corrupt. Use:

```sh
docker exec gunkly /gunkly backup > backup.db
```

For Postgres use `pg_dump`.

## Releases and verification

Pushing a `vX.Y.Z` tag builds a multi-arch (linux/amd64, linux/arm64) image at `ghcr.io/nexusbetweenvoids/gunkly` (tags `X.Y.Z`, `X.Y`, `latest`), signs it with keyless cosign and attests an SPDX SBOM. The GitHub release carries the SBOM (`sbom.spdx.json`), plain static binaries and `SHA256SUMS`.

Verify the image signature and the SBOM attestation:

```sh
cosign verify ghcr.io/nexusbetweenvoids/gunkly:<tag> \
  --certificate-identity-regexp 'https://github.com/NexusBetweenVoids/gunkly/.github/workflows/release.yml@refs/tags/v.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

cosign verify-attestation ghcr.io/nexusbetweenvoids/gunkly:<tag> --type spdxjson \
  --certificate-identity-regexp 'https://github.com/NexusBetweenVoids/gunkly/.github/workflows/release.yml@refs/tags/v.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

Verify a binary: `sha256sum -c --ignore-missing SHA256SUMS`.

## Documentation

`docs/PRD.md` (scope and requirements), `docs/adr/` (decisions), `docs/THREAT_MODEL.md`, `SECURITY.md`, `CONTRIBUTING.md`, `AGENTS.md`.

## License

AGPL-3.0. See `LICENSE`.
