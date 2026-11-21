# Contributing to Gunkly

Contributions are welcome. Read `AGENTS.md` (rules for humans and AI agents), `docs/PRD.md` (scope) and `docs/adr/` (decisions) first.

- **DCO sign-off:** sign every commit with `git commit -s`. There is no CLA; the project stays AGPL-3.0.
- **Small PRs:** one task each. If the work looks like new scope, open an issue first.
- **`make check` must pass** locally and in CI (formatting, lint, deadcode, tests on SQLite and Postgres with `-race`, fitness tests, frontend checks).
- **Tests:** every feature needs tests on both databases; every new endpoint needs a cross-household isolation test.
- **Dependencies:** none are added without approval. Justify any change to `go.mod` or `package.json` in one line in the PR (why stdlib is not enough, size impact, AGPL-compatible license).
- **Docs:** update `docs/PRD.md` or the relevant ADR in the same PR when behaviour or architecture changes.
- **Hygiene:** no secrets, real data, database files or `.env` files; fixtures are synthetic. User-facing text goes through translation files.
- **Security issues:** report privately, see `SECURITY.md`.
