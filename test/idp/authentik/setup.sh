#!/usr/bin/env bash
# TEST ONLY. Waits for authentik, applies the blueprint (gunkly-ci.yaml) and sets the test users'
# password. Every wait is bounded.
set -euo pipefail

BASE=http://localhost:9000
TOKEN=ci-only-api-token-not-a-real-secret
PASSWORD=ci-only-password-1
BLUEPRINT_PATH=custom/gunkly-ci.yaml

api() { curl -fsS -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' "$@"; }

# retry <seconds> <description> <command...>: run the command every 3 seconds until it succeeds.
retry() {
  local end=$((SECONDS + $1)) what=$2
  shift 2
  until "$@" >/dev/null 2>&1; do
    if [ "$SECONDS" -ge "$end" ]; then echo "timed out waiting for: $what" >&2; return 1; fi
    sleep 3
  done
}

ready() { curl -fsS "$BASE/-/health/ready/"; }
flows_ready() { api "$BASE/api/v3/flows/instances/default-provider-authorization-implicit-consent/"; }
key_ready() { api "$BASE/api/v3/crypto/certificatekeypairs/?name=authentik%20Self-signed%20Certificate" | jq -e '.results | length == 1'; }
app_ready() { api "$BASE/api/v3/core/applications/gunkly/" | jq -e '.provider != null'; }
user_ready() { api "$BASE/api/v3/core/users/?username=$1" | jq -e '.results | length == 1'; }

retry 240 "authentik to be ready" ready
# The stock flows and signing key come from authentik's own default blueprints, which the worker
# applies after start-up.
retry 240 "the default flows" flows_ready
retry 240 "the default signing key" key_ready

# Register the mounted file as a blueprint instance (unless the worker already did) and apply it.
pk=$(api "$BASE/api/v3/managed/blueprints/?path=$BLUEPRINT_PATH" | jq -r '.results[0].pk // empty')
if [ -z "$pk" ]; then
  pk=$(api -X POST "$BASE/api/v3/managed/blueprints/" \
    -d "{\"name\":\"gunkly-ci\",\"path\":\"$BLUEPRINT_PATH\",\"enabled\":true}" | jq -r .pk)
fi
echo "blueprint instance: $pk"
api -X POST "$BASE/api/v3/managed/blueprints/$pk/apply/" >/dev/null || true

if ! retry 120 "the blueprint to create the application" app_ready; then
  api "$BASE/api/v3/managed/blueprints/$pk/" | jq . >&2 || true
  exit 1
fi
for u in ci-sso-user ci-link-user; do
  retry 60 "user $u" user_ready "$u"
  upk=$(api "$BASE/api/v3/core/users/?username=$u" | jq -r '.results[0].pk')
  api -X POST "$BASE/api/v3/core/users/$upk/set_password/" -d "{\"password\":\"$PASSWORD\"}" >/dev/null
done

# The issuer must answer, at the address the browser and Gunkly both use.
curl -fsS "$BASE/application/o/gunkly/.well-known/openid-configuration" | jq -r .issuer
