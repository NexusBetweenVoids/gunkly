#!/usr/bin/env bash
# Upgrade test: run the previous release on a fresh SQLite volume, seed it
# through the API, stop it gracefully, start the new image on the SAME volume
# (auto-migrate), then verify the data survived and run the canary.
# Usage: scripts/upgrade-test.sh PREV_TAG NEW_IMAGE
#   PREV_TAG   release tag of the previous version (e.g. 1.2.3; "v" prefix is stripped)
#   NEW_IMAGE  image reference of the candidate (e.g. gunkly:ci)
# Env: PREV_IMAGE_REPO (default ghcr.io/nexusbetweenvoids/gunkly), UPGRADE_PORT (default 18091).
# Needs docker, curl, jq, openssl. Uses only synthetic data and throwaway secrets.
set -euo pipefail

prev_tag="${1:-}"
new_image="${2:-}"
[ -n "$prev_tag" ] && [ -n "$new_image" ] || { echo "usage: $0 PREV_TAG NEW_IMAGE" >&2; exit 2; }
prev_image="${PREV_IMAGE_REPO:-ghcr.io/nexusbetweenvoids/gunkly}:${prev_tag#v}"
port="${UPGRADE_PORT:-18091}"
base="http://127.0.0.1:$port"
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
name="gunkly-upgrade-$$"
vol="$name-data"
key="1:$(openssl rand -base64 32)"
SEED_PASSWORD="upgrade-$(openssl rand -hex 12)"
CANARY_LOGIN=canary-bot
CANARY_PASSWORD="canary-$(openssl rand -hex 12)"
export SEED_PASSWORD CANARY_LOGIN CANARY_PASSWORD
tmp=$(mktemp -d)
chmod 700 "$tmp"

log() { echo "upgrade-test: $*"; }
fail() { echo "upgrade-test: FAIL: $*" >&2; exit 1; }

cleanup() {
  rc=$?
  if [ "$rc" -ne 0 ]; then
    echo "--- container logs ---" >&2
    docker logs "$name" 2>&1 | tail -n 60 >&2 || true
  fi
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker volume rm -f "$vol" >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT

start() { # start IMAGE
  docker run -d --name "$name" --read-only --cap-drop ALL --security-opt no-new-privileges \
    -p "127.0.0.1:$port:8080" -v "$vol:/data" \
    -e GUNKLY_SECRET_KEYS="$key" -e GUNKLY_BASE_URL="$base" "$1" >/dev/null
  for _ in $(seq 1 60); do
    [ "$(curl -s -o /dev/null -w '%{http_code}' "$base/readyz" || true)" = 200 ] && return 0
    sleep 1
  done
  fail "$1 did not become ready"
}

# chore_names: sorted chore names of the seeded demo user, via the public API.
chore_names() {
  local jar="$tmp/jar-names" csrf hh
  rm -f "$jar"
  curl -sS -c "$jar" -o /dev/null "$base/api/v1/session"
  csrf=$(awk '$6 == "gunkly_csrf" { print $7 }' "$jar" | tail -n 1)
  jq -n --arg p "$SEED_PASSWORD" '{login: "alice-demo", password: $p}' |
    curl -sSf -b "$jar" -c "$jar" -H "X-CSRF-Token: $csrf" -H 'Content-Type: application/json' --data-binary @- -o /dev/null "$base/api/v1/session"
  csrf=$(awk '$6 == "gunkly_csrf" { print $7 }' "$jar" | tail -n 1)
  hh=$(curl -sSf -b "$jar" "$base/api/v1/households" | jq -r '.[0].id')
  curl -sSf -b "$jar" "$base/api/v1/households/$hh/chores" | jq -r '.[].name' | sort
}

log "previous release: $prev_image"
docker pull "$prev_image" >/dev/null || fail "cannot pull $prev_image"
docker volume create "$vol" >/dev/null
start "$prev_image"

log "seeding $prev_image through the API"
code=$(docker logs "$name" 2>&1 | grep -o 'setup_code[^,} ]*' | tail -n 1 | sed -E 's/^setup_code"?[=:] *"?//; s/"$//' || true)
[ -n "$code" ] || fail "no setup code in the log"
SETUP_CODE="$code" SEED_DISABLE_EGRESS_GUARD=false SEED_WEBHOOK_URL="" "$here/seed-staging.sh" "$base"
chore_names >"$tmp/before"
[ -s "$tmp/before" ] || fail "no seeded chores found before the upgrade"

log "stopping $prev_image (SIGTERM, 40s grace)"
docker stop -t 40 "$name" >/dev/null
[ "$(docker inspect --format '{{.State.ExitCode}}' "$name")" = 0 ] || fail "previous release did not exit cleanly on SIGTERM"
docker rm "$name" >/dev/null

log "starting $new_image on the same volume (auto-migrate)"
start "$new_image"

log "smoke checks"
[ "$(curl -s -o /dev/null -w '%{http_code}' "$base/healthz")" = 200 ] || fail "/healthz"
chore_names >"$tmp/after"
diff -u "$tmp/before" "$tmp/after" || fail "chores changed across the upgrade"

log "canary"
"$here/canary.sh" "$base"
log "OK: $prev_image -> $new_image"
