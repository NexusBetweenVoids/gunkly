#!/usr/bin/env bash
# Frontend architecture fitness checks (ADR 0012). Run from anywhere.
#  1. Only web/src/lib/api.ts may talk to the network.
#  2. No inline style= attributes in .svelte files (CSP, theming).
#  3. No physical CSS properties in .svelte/.css (RTL: use logical properties).
set -u
cd "$(dirname "$0")/.." || exit 1
src=web/src
fail=0

report() { # title, grep output
  if [ -n "$2" ]; then
    echo "FAIL: $1"
    echo "$2" | sed 's/^/  /'
    fail=1
  fi
}

net=$(grep -rnE --include='*.ts' --include='*.js' --include='*.svelte' \
  '(^|[^.[:alnum:]_$])fetch[[:space:]]*\(|XMLHttpRequest|new[[:space:]]+WebSocket|EventSource' "$src" |
  grep -v "^$src/lib/api.ts:")
report "network access outside web/src/lib/api.ts (fetch/XMLHttpRequest/WebSocket/EventSource)" "$net"

style=$(grep -rnE --include='*.svelte' '[[:space:]]style[[:space:]]*=' "$src")
report "inline style= attribute in .svelte" "$style"

physical=$(grep -rnE --include='*.svelte' --include='*.css' \
  -e '(margin|padding)-(left|right)[[:space:]]*:' \
  -e '(^|[;{[:space:]])(left|right)[[:space:]]*:[[:space:]]*[^[:space:]]' \
  -e 'text-align[[:space:]]*:[[:space:]]*(left|right)' \
  -e 'float[[:space:]]*:[[:space:]]*(left|right)' \
  -e 'border-(left|right)(-[a-z]+)?[[:space:]]*:' "$src")
report "physical CSS properties (use logical: inline-start/end, margin-inline, ...)" "$physical"

# README badges with hard-coded versions must match the code.
root=.
svelte_major=$(sed -n 's/.*"svelte": "\([0-9]*\)\..*/\1/p' "$root/web/package.json")
if ! grep -q "badge/Svelte-${svelte_major}-" "$root/README.md"; then
  echo "README Svelte badge does not match web/package.json (major ${svelte_major})"
  fail=1
fi

[ "$fail" -eq 0 ] && echo "fitness-web: ok"
exit "$fail"
