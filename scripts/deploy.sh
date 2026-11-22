#!/usr/bin/env bash
# Generic single-host rollout for a docker compose deployment of Gunkly, local
# or over SSH. Provider-agnostic: hosting details come from the environment.
#
# Flow: pre-deploy snapshot -> pull -> `up -d` (graceful SIGTERM stop) -> wait
# for /readyz -> scripts/canary.sh -> on failure roll the image tag back.
# Data is NEVER restored automatically: migrations are forward-only, so a
# rollback only works while the schema stays compatible with the previous
# release (expand-and-contract, see docs/operations.md). The snapshot path and
# restore steps are printed instead.
#
# Required environment
#   IMAGE_TAG      tag to deploy (e.g. 1.4.0 or main-<sha>); the compose file must use ${IMAGE_TAG}
#   COMPOSE_FILE   compose file path (on the target host when DEPLOY_HOST is set)
#   BASE_URL       public URL used for /readyz and the canary, e.g. https://staging.example.org
#   CANARY_LOGIN, CANARY_PASSWORD   (+ CANARY_TOTP_SECRET if the account has TOTP)
# Optional
#   DEPLOY_HOST        [user@]host for SSH; unset = run docker compose locally
#   DEPLOY_SSH_OPTS    extra ssh options (e.g. "-i key -o UserKnownHostsFile=file -o StrictHostKeyChecking=yes")
#   DEPLOY_DIR         directory to cd into on the target before running compose
#   COMPOSE_ENV_FILE   --env-file for compose (on the target)
#   DB_KIND            sqlite (default) or postgres
#   SNAPSHOT_DIR       Postgres dumps are written here on the target (default ./snapshots)
#   PGUSER / PGDATABASE   Postgres snapshot identity (default gunkly / gunkly)
#   SERVICE / POSTGRES_SERVICE   compose service names (default gunkly / postgres)
#   READY_TIMEOUT      seconds to wait for /readyz (default 120)
#   SKIP_CANARY=1      skip the canary (not for production)
set -euo pipefail

: "${IMAGE_TAG:?set IMAGE_TAG}"
: "${COMPOSE_FILE:?set COMPOSE_FILE}"
: "${BASE_URL:?set BASE_URL}"
BASE_URL="${BASE_URL%/}"
DB_KIND="${DB_KIND:-sqlite}"
SERVICE="${SERVICE:-gunkly}"
POSTGRES_SERVICE="${POSTGRES_SERVICE:-postgres}"
SNAPSHOT_DIR="${SNAPSHOT_DIR:-./snapshots}"
PGUSER="${PGUSER:-gunkly}"
PGDATABASE="${PGDATABASE:-gunkly}"
READY_TIMEOUT="${READY_TIMEOUT:-120}"
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

case "$DB_KIND" in sqlite | postgres) ;; *) echo "deploy: DB_KIND must be sqlite or postgres" >&2; exit 2 ;; esac
case "$IMAGE_TAG" in *[!A-Za-z0-9._-]* | "") echo "deploy: IMAGE_TAG has unexpected characters" >&2; exit 2 ;; esac

log() { echo "deploy: $*"; }

# sh_target STRING: run a shell snippet on the target (ssh) or here.
sh_target() {
  local cmd="$1"
  if [ -n "${DEPLOY_HOST:-}" ]; then
    [ -z "${DEPLOY_DIR:-}" ] || cmd="cd $(printf '%q' "$DEPLOY_DIR") && $cmd"
    # shellcheck disable=SC2086  # DEPLOY_SSH_OPTS is intentionally word-split
    ssh -o BatchMode=yes ${DEPLOY_SSH_OPTS:-} -- "$DEPLOY_HOST" "$cmd"
  else
    (
      [ -z "${DEPLOY_DIR:-}" ] || cd "$DEPLOY_DIR"
      bash -c "$cmd"
    )
  fi
}

# dc TAG ARGS...: a `docker compose` command string pinned to the image tag.
dc() {
  local tag="$1"
  shift
  local s
  s="IMAGE_TAG=$(printf '%q' "$tag") docker compose -f $(printf '%q' "$COMPOSE_FILE")"
  [ -z "${COMPOSE_ENV_FILE:-}" ] || s="$s --env-file $(printf '%q' "$COMPOSE_ENV_FILE")"
  printf '%s %s' "$s" "$(printf '%q ' "$@")"
}

wait_ready() {
  local i
  for ((i = 0; i < READY_TIMEOUT; i += 2)); do
    if [ "$(curl -s -o /dev/null --max-time 5 -w '%{http_code}' "$BASE_URL/readyz" || true)" = 200 ]; then
      return 0
    fi
    sleep 2
  done
  return 1
}

# Compose must give the container longer than GUNKLY_SHUTDOWN_TIMEOUT (30s) to drain.
check_grace() {
  local v secs
  v=$(sh_target "$(dc "$IMAGE_TAG" config)" | awk -v s="$SERVICE" '
    $0 ~ "^  " s ":" { on = 1; next }
    on && /^  [^ ]/ { on = 0 }
    on && /stop_grace_period:/ { print $2; exit }')
  secs=$(awk -v v="${v:-}" 'BEGIN {
    t = 0; while (match(v, /^[0-9.]+[hms]/)) { n = substr(v, 1, RLENGTH - 1) + 0; u = substr(v, RLENGTH, 1);
      t += n * (u == "h" ? 3600 : u == "m" ? 60 : 1); v = substr(v, RLENGTH + 1) } print t }')
  if [ "${secs%.*}" -lt 35 ]; then
    echo "deploy: compose service $SERVICE needs stop_grace_period >= 35s (found '${v:-unset}'): Gunkly drains for up to 30s on SIGTERM" >&2
    exit 2
  fi
}

# --- previous state ----------------------------------------------------------
prev_image=$(sh_target "docker inspect --format '{{.Config.Image}}' \"\$($(dc "$IMAGE_TAG" ps -q "$SERVICE"))\" 2>/dev/null" || true)
prev_tag="${prev_image##*:}"
[ "$prev_image" != "$prev_tag" ] || prev_tag=""
[ -z "$prev_tag" ] || log "currently running tag: $prev_tag"
[ -n "$prev_image" ] || log "no running $SERVICE container: first deploy, no rollback target"

check_grace

# --- 1. snapshot -------------------------------------------------------------
ts=$(date -u +%Y%m%dT%H%M%SZ)
snapshot=""
if [ -n "$prev_image" ]; then
  if [ "$DB_KIND" = sqlite ]; then
    log "snapshot: gunkly backup into the data volume (/data/backups, timestamped)"
    snapshot=$(sh_target "$(dc "${prev_tag:-$IMAGE_TAG}" exec -T "$SERVICE" /gunkly backup)" | tail -n 1) || { echo "deploy: snapshot failed, aborting before any change" >&2; exit 1; }
    [ -n "$snapshot" ] || { echo "deploy: snapshot printed no path, aborting before any change" >&2; exit 1; }
    snapshot="sqlite volume file $snapshot (inside the $SERVICE data volume)"
  else
    log "snapshot: pg_dump via the $POSTGRES_SERVICE container"
    f="$SNAPSHOT_DIR/pre-deploy-$ts.dump"
    sh_target "umask 077; mkdir -p $(printf '%q' "$SNAPSHOT_DIR") && $(dc "${prev_tag:-$IMAGE_TAG}" exec -T "$POSTGRES_SERVICE" pg_dump -U "$PGUSER" -d "$PGDATABASE" -Fc) > $(printf '%q' "$f")" || { echo "deploy: snapshot failed, aborting before any change" >&2; exit 1; }
    snapshot="postgres dump $f (on the target host)"
  fi
  log "snapshot written: $snapshot"
else
  log "snapshot: skipped (nothing running yet)"
fi

rollback_help() {
  cat >&2 <<HELP

deploy: DATA IS NOT RESTORED AUTOMATICALLY. Migrations are forward-only; the previous
release only runs correctly if the schema change was backward compatible
(expand-and-contract). If $prev_tag misbehaves on the migrated database, restore:
  snapshot: ${snapshot:-none taken}
  1. stop the service:   docker compose -f $COMPOSE_FILE stop $SERVICE
  2. SQLite: copy the snapshot over gunkly.db in the data volume (remove gunkly.db-wal/-shm).
     Postgres: pg_restore --clean --if-exists -U $PGUSER -d $PGDATABASE <dump>
  3. start the previous tag: IMAGE_TAG=$prev_tag docker compose -f $COMPOSE_FILE up -d
See docs/operations.md. Data written after the snapshot is lost by a restore.
HELP
}

fail_deploy() {
  echo "deploy: FAILED at stage '$1'" >&2
  if [ -z "$prev_tag" ]; then
    echo "deploy: no previous tag known; nothing to roll back to" >&2
    exit 1
  fi
  log "rolling back image tag to $prev_tag"
  if sh_target "$(dc "$prev_tag" up -d --remove-orphans "$SERVICE")" && wait_ready; then
    log "rolled back: $prev_tag is ready again"
  else
    echo "deploy: ROLLBACK ALSO FAILED; $SERVICE is not ready. Inspect: docker compose logs $SERVICE" >&2
  fi
  rollback_help
  exit 1
}

# --- 2. pull and 3. roll out -------------------------------------------------
log "pull $IMAGE_TAG"
sh_target "$(dc "$IMAGE_TAG" pull "$SERVICE")" || { echo "deploy: pull failed; nothing changed" >&2; exit 1; }

log "up -d (SIGTERM, graceful stop)"
sh_target "$(dc "$IMAGE_TAG" up -d --remove-orphans)" || fail_deploy up

# --- 4. readiness and 5. canary ----------------------------------------------
log "waiting for $BASE_URL/readyz (up to ${READY_TIMEOUT}s)"
wait_ready || fail_deploy readyz

if [ "${SKIP_CANARY:-}" = 1 ]; then
  log "canary skipped (SKIP_CANARY=1)"
else
  "$here/canary.sh" "$BASE_URL" || fail_deploy canary
fi

log "OK: $IMAGE_TAG is live${snapshot:+ (snapshot: $snapshot)}"
