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
oasdiff breaking "$tmp/base.yaml" "$tmp/head.yaml" --fail-on ERR
