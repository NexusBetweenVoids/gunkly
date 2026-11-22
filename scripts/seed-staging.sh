#!/usr/bin/env bash
# Seeds a FRESH staging or test instance with obviously fake data through the
# public API: first-run admin, a few demo users, households, areas, chores and
# a webhook channel pointing at the mock receiver. Idempotent for the admin
# and users; households and chores are only created for new users.
#
# Usage: SEED_PASSWORD=... scripts/seed-staging.sh BASE_URL
#   SEED_PASSWORD           required, shared by every synthetic account (12+ chars). Staging only.
#   SETUP_CODE              first-run setup code; if unset, read from `docker compose logs`
#                           (needs SEED_COMPOSE_FILE, and SEED_COMPOSE_ENV_FILE if one is used).
#   CANARY_LOGIN / CANARY_PASSWORD   if both set, also creates that account for scripts/canary.sh.
#   SEED_WEBHOOK_URL        default generic+http://webhook:8080/hook (Shoutrrr generic URL); set to empty to skip the channel.
#   SEED_DISABLE_EGRESS_GUARD  default true: turns the egress guard off so the private
#                           Mailpit/webhook containers are reachable. NEVER do this in production.
set -euo pipefail

base="${1:-}"
[ -n "$base" ] || { echo "usage: $0 BASE_URL" >&2; exit 2; }
base="${base%/}"
: "${SEED_PASSWORD:?set SEED_PASSWORD (staging only)}"
webhook_url="${SEED_WEBHOOK_URL-generic+http://webhook:8080/hook}"
admin=staging-admin
for tool in curl jq; do
  command -v "$tool" >/dev/null || { echo "seed: $tool is required" >&2; exit 2; }
done

tmp=$(mktemp -d)
chmod 700 "$tmp"
trap 'rm -rf "$tmp"' EXIT
status="" resp=""

fail() { echo "seed: FAIL: $*" >&2; exit 1; }

# api USER METHOD PATH [JSON]: one cookie jar per synthetic user; sets $status.
api() {
  local method="$2" path="$3" data="${4:-}" jar="$tmp/jar-$1" csrf
  csrf=$(awk '$6 == "gunkly_csrf" { print $7 }' "$jar" 2>/dev/null | tail -n 1 || true)
  local args=(-sS --max-time 20 -b "$jar" -c "$jar" -X "$method" -o "$tmp/body" -w '%{http_code}'
    -H "X-CSRF-Token: $csrf" -H 'Accept: application/json')
  if [ -n "$data" ]; then
    status=$(printf '%s' "$data" | curl "${args[@]}" -H 'Content-Type: application/json' --data-binary @- "$base$path") || fail "$method $path: request error"
  else
    status=$(curl "${args[@]}" "$base$path") || fail "$method $path: request error"
  fi
  resp=$(cat "$tmp/body")
}

expect() { # expect WANT_CODE WHAT
  [ "$status" = "$1" ] || fail "$2 returned $status (wanted $1)"
}

signin() { # signin USER LOGIN PASSWORD
  api "$1" GET /api/v1/session
  expect 200 "GET /session"
  local body
  body=$(jq -n --arg l "$2" --arg p "$3" '{login: $l, password: $p}')
  api "$1" POST /api/v1/session "$body"
  expect 200 "sign-in as $2"
}

echo "seed: waiting for $base/readyz"
for _ in $(seq 1 60); do
  [ "$(curl -s -o /dev/null -w '%{http_code}' "$base/readyz" || true)" = 200 ] && break
  sleep 2
done
[ "$(curl -s -o /dev/null -w '%{http_code}' "$base/readyz" || true)" = 200 ] || fail "not ready"

# --- first-run setup -------------------------------------------------------
api setup GET /api/v1/setup
needed=$(jq -r ".needed" <<<"$resp")
if [ "$needed" = true ]; then
  code="${SETUP_CODE:-}"
  if [ -z "$code" ] && [ -n "${SEED_COMPOSE_FILE:-}" ]; then
    cargs=(-f "$SEED_COMPOSE_FILE")
    [ -z "${SEED_COMPOSE_ENV_FILE:-}" ] || cargs+=(--env-file "$SEED_COMPOSE_ENV_FILE")
    code=$(docker compose "${cargs[@]}" logs gunkly 2>/dev/null | grep -o 'setup_code[^,} ]*' | tail -n 1 | sed -E 's/^setup_code"?[=:] *"?//; s/"$//' || true)
  fi
  [ -n "$code" ] || fail "setup is pending: set SETUP_CODE (from the gunkly log) or SEED_COMPOSE_FILE"
  api setup GET /api/v1/session
  body=$(jq -n --arg p "$SEED_PASSWORD" --arg c "$code" \
    '{username: "staging-admin", displayName: "Staging Admin (fake)", password: $p, code: $c}')
  api setup POST /api/v1/setup "$body"
  expect 201 "first-run setup"
  echo "seed: created admin $admin"
fi

# --- admin: instance settings and users ------------------------------------
signin admin "$admin" "$SEED_PASSWORD"
step_up=$(jq -n --arg p "$SEED_PASSWORD" '{password: $p}')
api admin POST /api/v1/session/step-up "$step_up"
expect 204 "step-up"

if [ "${SEED_DISABLE_EGRESS_GUARD:-true}" = true ]; then
  # Enable every notification kind (so the generic webhook is allowed) and drop the guard.
  api admin GET /api/v1/admin/settings
  kinds=$(jq -c '.availableNotificationKinds // []' <<<"$resp")
  patch=$(jq -n --argjson k "$kinds" '{egressGuard: false, enabledNotificationKinds: $k}')
  api admin PATCH /api/v1/admin/settings "$patch"
  expect 200 "admin settings"
  echo "seed: egress guard OFF (staging only)"
fi

fresh=""
users=(alice-demo bob-demo)
if [ -n "${CANARY_LOGIN:-}" ] && [ -n "${CANARY_PASSWORD:-}" ]; then users+=("$CANARY_LOGIN"); fi
api admin GET /api/v1/admin/users
existing=$(jq -r '.[].username' <<<"$resp")
for u in "${users[@]}"; do
  if grep -qxF "$u" <<<"$existing"; then continue; fi
  pw="$SEED_PASSWORD"
  [ "$u" != "${CANARY_LOGIN:-}" ] || pw="$CANARY_PASSWORD"
  body=$(jq -n --arg u "$u" --arg p "$pw" '{username: $u, displayName: ($u + " (fake)"), password: $p}')
  api admin POST /api/v1/admin/users "$body"
  expect 201 "create user $u"
  echo "seed: created user $u"
  case "$u" in alice-demo | bob-demo) fresh="$fresh $u" ;; esac
done


# --- demo households -------------------------------------------------------
today=$(date -u +%F)
chore() { # chore USER HH NAME EVERY UNIT
  local body
  body=$(jq -n --arg n "$3" --arg d "$today" --argjson e "$4" --arg u "$5" \
    '{name: $n, schedule: {type: "interval", interval: {every: $e, unit: $u}, firstDue: $d}}')
  api "$1" POST "/api/v1/households/$2/chores" "$body"
  expect 201 "create chore $3"
}

for u in $fresh; do
  signin "$u" "$u" "$SEED_PASSWORD"
  body=$(jq -n --arg n "Demo household of $u (fake)" '{name: $n, timezone: "UTC", starterAreas: true}')
  api "$u" POST /api/v1/households "$body"
  hh=$(jq -r ".id" <<<"$resp")
  expect 201 "create household"
  chore "$u" "$hh" "Wipe the fake kitchen counters" 1 week
  chore "$u" "$hh" "Water the pretend ferns" 3 day
  chore "$u" "$hh" "Descale the imaginary kettle" 1 month
  if [ "$u" = alice-demo ]; then
    chore "$u" "$hh" "Test the fictional smoke alarm" 1 month
    chore "$u" "$hh" "Rotate the make-believe mattress" 3 month
    if [ -n "$webhook_url" ]; then
      ch=$(jq -n --arg u "$webhook_url" '{url: $u, label: "Mock webhook receiver"}')
      api "$u" POST "/api/v1/households/$hh/channels" "$ch"
      expect 201 "create webhook channel"
    fi
  fi
  echo "seed: household for $u"
done
echo "seed: done"
