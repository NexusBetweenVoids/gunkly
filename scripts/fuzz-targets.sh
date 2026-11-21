#!/usr/bin/env bash
# Fail if a Fuzz* target in the repo is missing from the nightly fuzz matrix.
set -eu
cd "$(dirname "$0")/.."
rc=0
for t in $(grep -rhoE --include='*_test.go' '^func Fuzz[A-Za-z0-9_]+' internal cmd | awk '{print $2}'); do
  grep -q "target: $t\$" .github/workflows/fuzz.yml || { echo "missing from fuzz.yml: $t"; rc=1; }
done
exit $rc
