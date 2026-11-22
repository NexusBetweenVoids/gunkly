#!/usr/bin/env bash
# Dependency count budgets (ADR 0012): direct Go requires and web devDependencies.
set -eu
cd "$(dirname "$0")/.."
GO_MAX=10
WEB_DEV_MAX=9

go_count=$(awk '
  /^require[[:space:]]*\(/ {inb=1; next}
  inb && /^\)/ {inb=0; next}
  inb && !/\/\/ *indirect/ && NF {n++}
  /^require[[:space:]]+[^(]/ && !/\/\/ *indirect/ {n++}
  END {print n+0}' go.mod)
web_count=$(node -e 'const p=require("./web/package.json");console.log(Object.keys(p.devDependencies||{}).length)')

echo "go direct requires: $go_count (max $GO_MAX)"
echo "web devDependencies: $web_count (max $WEB_DEV_MAX)"
fail=0
[ "$go_count" -le "$GO_MAX" ] || { echo "FAIL: too many direct Go dependencies"; fail=1; }
[ "$web_count" -le "$WEB_DEV_MAX" ] || { echo "FAIL: too many web devDependencies"; fail=1; }
exit "$fail"
