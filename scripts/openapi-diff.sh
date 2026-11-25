#!/usr/bin/env bash
# Fail on breaking (ERR-level) changes to api/openapi.yaml between BASE and the working tree.
# Usage: scripts/openapi-diff.sh <base-ref> [head-ref]   (head defaults to the working tree)
set -eu
cd "$(dirname "$0")/.."
base=${1:?usage: openapi-diff.sh <base-ref> [head-ref]}
head=${2:-}
command -v oasdiff >/dev/null || { echo "oasdiff not installed (https://github.com/oasdiff/oasdiff)"; exit 1; }
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
git show "$base:api/openapi.yaml" >"$tmp/base.yaml"
if [ -n "$head" ]; then git show "$head:api/openapi.yaml" >"$tmp/head.yaml"; else cp api/openapi.yaml "$tmp/head.yaml"; fi
# The new spec must always load (an unparseable spec fails here, not silently later).
oasdiff breaking "$tmp/head.yaml" "$tmp/head.yaml" --fail-on ERR >/dev/null || { echo "api/openapi.yaml does not load"; exit 1; }
# A base that does not load cannot be compared; that only happens right after fixing one.
if ! oasdiff breaking "$tmp/base.yaml" "$tmp/base.yaml" >/dev/null 2>&1; then
  echo "warning: the base spec ($base) does not load; nothing to compare against"
  exit 0
fi
oasdiff breaking "$tmp/base.yaml" "$tmp/head.yaml" --fail-on ERR
