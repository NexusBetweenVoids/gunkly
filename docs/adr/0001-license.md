# 0001. License: AGPL-3.0

Status: Accepted

## Context
Gunkly is self-hostable and also run as a public hosted service. Anyone offering a modified version over a network should share their changes.

## Decision
License under AGPL-3.0. Accept contributions under a DCO sign-off, with no CLA. The hosted service runs the public code unmodified.

## Consequences
- Network-service modifiers must publish source under the same license; the hosted service meets this by running public code.
- No CLA means nobody can relicense away from AGPL.
- Every dependency must be AGPL-3.0 compatible; CI checks licenses.
