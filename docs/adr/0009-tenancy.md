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

### Admin password reset
Hosted mode disables the admin reset endpoint (`disabled_in_hosted_mode`); hosted users reset by email and the operator has the CLI. In every mode the endpoint refuses admin targets, signs the target out everywhere, and writes a "password reset by an administrator" event to the target's own audit log. Trade-off, stated plainly: on a self-hosted instance an admin can still reset a non-admin user's password and then sign in as them, so the admin is trusted with all accounts on that instance (as the operator who holds the database already is); the protection is that it is no longer silent, not that it is impossible.

### Admin account deletion
An admin can delete a non-admin account other than their own (Administration, Users), with the same effects as the user's own Delete account; where the user is the last owner, the longest-standing non-guest member becomes owner, as in the inactive-account cleanup. Because it cannot be undone, the admin must have a second factor (passkey, authenticator app or email codes), pass a step-up with it, and type the account's username. The deletion is written to the admin's audit log (`admin_user_delete`). Admin accounts are refused (`admin_delete_target`): an admin leaves through their own Delete account.

## Consequences
- Some boilerplate in every store method; accepted for safety.
- Cross-household operations (admin, retention cleanup) live in explicitly named unscoped store functions that are reviewed carefully.
