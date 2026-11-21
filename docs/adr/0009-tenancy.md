# 0009. Tenancy model

Status: Accepted

## Context
The hosted service holds strangers' data; one missing membership check leaks a household.

## Decision
- The household is the isolation unit; all chores, areas and history belong to exactly one.
- `internal/store` is the only package that issues SQL against household data. Every household-scoped method takes `(caller user ID, household ID)` and verifies membership and role inside the same query or transaction (no check-then-act gap).
- Handlers never import `database/sql`, enforced by `depguard` and an architectural fitness test; router introspection requires every route to declare its auth and tenant-scoping middleware.
- Public IDs are UUIDv7, never sequential. Every new endpoint ships a parameterised cross-tenant isolation test, including ID guessing.
- The instance admin has no app path to household content.

## Consequences
- Some boilerplate in every store method; accepted for safety.
- Cross-household operations (admin, retention cleanup) live in explicitly named unscoped store functions that are reviewed carefully.
