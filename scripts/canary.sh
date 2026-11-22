#!/usr/bin/env bash
# Synthetic canary: signs in as a dedicated canary account over the public API
# and exercises the core loop (create, complete, undo, delete a chore).
# Usage: CANARY_LOGIN=... CANARY_PASSWORD=... [CANARY_TOTP_SECRET=...] scripts/canary.sh BASE_URL
# Exits non-zero on any failure. Never prints credentials, cookies or tokens.
#
# CANARY_TOTP_SECRET (base32) is needed only when the canary account has TOTP;
# the code is computed with python3 (stdlib only). Prefer a canary account
# without 2FA and with no data of value: it is a throwaway identity.
set -euo pipefail

base="${1:-}"
[ -n "$base" ] || { echo "usage: $0 BASE_URL" >&2; exit 2; }
base="${base%/}"
: "${CANARY_LOGIN:?set CANARY_LOGIN}"
: "${CANARY_PASSWORD:?set CANARY_PASSWORD}"
for tool in curl jq; do
  command -v "$tool" >/dev/null || { echo "canary: $tool is required" >&2; exit 2; }
done

tmp=$(mktemp -d)
chmod 700 "$tmp"
jar="$tmp/cookies"
hh="" chore=""
csrf=""

step() { echo "canary: $*"; }
fail() { echo "canary: FAIL: $*" >&2; exit 1; }

# api METHOD PATH [JSON] -> sets $status and $resp (never run in a subshell). Credentials only ever
# travel in the request body via stdin, never on a command line.
status="" resp=""
api() {
  local method="$1" path="$2" data="${3:-}" out
  local args=(-sS --max-time 20 -b "$jar" -c "$jar" -X "$method" -o "$tmp/body" -w '%{http_code}'
    -H "X-CSRF-Token: $csrf" -H 'Accept: application/json')
  if [ -n "$data" ]; then args+=(-H 'Content-Type: application/json' --data-binary @-); fi
  if [ -n "$data" ]; then
    out=$(printf '%s' "$data" | curl "${args[@]}" "$base$path") || fail "$method $path: request error"
  else
    out=$(curl "${args[@]}" "$base$path") || fail "$method $path: request error"
  fi
  status="$out"
  resp=$(cat "$tmp/body")
}

refresh_csrf() {
  csrf=$(awk '$6 == "gunkly_csrf" { print $7 }' "$jar" | tail -n 1)
}

cleanup() {
  rc=$?
  # Best-effort removal of the canary chore if a step failed midway.
  if [ -n "$chore" ] && [ -n "$hh" ] && [ -n "$csrf" ]; then
    curl -sS --max-time 10 -b "$jar" -c "$jar" -X DELETE -o /dev/null \
      -H "X-CSRF-Token: $csrf" "$base/api/v1/households/$hh/chores/$chore" || true
  fi
  rm -rf "$tmp"
  if [ "$rc" -ne 0 ]; then echo "canary: FAILED" >&2; fi
}
trap cleanup EXIT

totp_code() {
  CANARY_TOTP_SECRET="$CANARY_TOTP_SECRET" python3 -I -c '
import base64, hashlib, hmac, os, struct, time
s = os.environ["CANARY_TOTP_SECRET"].replace(" ", "").upper()
key = base64.b32decode(s + "=" * (-len(s) % 8))
mac = hmac.new(key, struct.pack(">Q", int(time.time()) // 30), hashlib.sha1).digest()
o = mac[-1] & 15
print("%06d" % ((struct.unpack(">I", mac[o:o + 4])[0] & 0x7FFFFFFF) % 1000000))'
}

step "probes"
for p in /healthz /readyz; do
  code=$(curl -sS --max-time 10 -o /dev/null -w '%{http_code}' "$base$p") || fail "GET $p: request error"
  [ "$code" = 200 ] || fail "GET $p returned $code"
done

step "session"
api GET /api/v1/session
[ "$status" = 200 ] || fail "GET /session returned $status"
refresh_csrf
[ -n "$csrf" ] || fail "no CSRF cookie issued"

step "sign in"
login=$(jq -n --arg l "$CANARY_LOGIN" --arg p "$CANARY_PASSWORD" '{login: $l, password: $p}')
api POST /api/v1/session "$login"
[ "$status" = 200 ] || fail "sign-in returned $status"
if [ "$(jq -r '.twoFactorRequired // false' <<<"$resp")" = true ]; then
  [ -n "${CANARY_TOTP_SECRET:-}" ] || fail "account needs a second factor but CANARY_TOTP_SECRET is not set"
  [ "$(jq -r '.method' <<<"$resp")" = totp ] || fail "second factor is not TOTP"
  command -v python3 >/dev/null || fail "python3 is required to compute the TOTP code"
  login=$(jq -n --arg l "$CANARY_LOGIN" --arg p "$CANARY_PASSWORD" --arg t "$(totp_code)" '{login: $l, password: $p, totp: $t}')
  api POST /api/v1/session "$login"
  [ "$status" = 200 ] || fail "TOTP sign-in returned $status"
fi
[ "$(jq -r '.authenticated // false' <<<"$resp")" = true ] || fail "not authenticated after sign-in"
unset login CANARY_PASSWORD CANARY_TOTP_SECRET
refresh_csrf

api GET /api/v1/me
[ "$status" = 200 ] || fail "GET /me returned $status"

step "household"
api GET /api/v1/households
[ "$status" = 200 ] || fail "GET /households returned $status"
hh=$(jq -r '.[0].id // empty' <<<"$resp")
if [ -z "$hh" ]; then
  api POST /api/v1/households '{"name":"Canary","timezone":"UTC"}'
  [ "$status" = 201 ] || fail "create household returned $status"
  hh=$(jq -r '.id' <<<"$resp")
fi

step "create chore"
today=$(date -u +%F)
body=$(jq -n --arg n "canary-$(date -u +%Y%m%dT%H%M%SZ)" --arg d "$today" \
  '{name: $n, schedule: {type: "interval", interval: {every: 1, unit: "day"}, firstDue: $d}}')
api POST "/api/v1/households/$hh/chores" "$body"
[ "$status" = 201 ] || fail "create chore returned $status"
chore=$(jq -r '.id' <<<"$resp")
due_before=$(jq -r '.dueDate' <<<"$resp")

step "complete chore"
api POST "/api/v1/households/$hh/chores/$chore/complete"
[ "$status" = 201 ] || fail "complete returned $status"
undo=$(jq -r '.undoPath // empty' <<<"$resp")
[ -n "$undo" ] || fail "complete returned no undoPath"
[ "$(jq -r '.chore.dueDate' <<<"$resp")" != "$due_before" ] || fail "due date did not advance"

step "undo completion"
api POST "$undo"
[ "$status" = 200 ] || fail "undo returned $status"
[ "$(jq -r '.dueDate' <<<"$resp")" = "$due_before" ] || fail "undo did not restore the due date"

step "delete chore"
api DELETE "/api/v1/households/$hh/chores/$chore"
[ "$status" = 204 ] || fail "delete returned $status"
gone="$chore"
chore=""
api GET "/api/v1/households/$hh/chores/$gone"
[ "$status" = 404 ] || fail "deleted chore still readable ($status)"

step "sign out"
api DELETE /api/v1/session
[ "$status" = 204 ] || fail "sign-out returned $status"
csrf=""

step "OK"
