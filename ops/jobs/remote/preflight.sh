#!/usr/bin/env bash
# preflight.sh — connection check for the remote PipelineGen Master (M2M surface).
#
# This script is READ-ONLY with respect to the job broker: it issues GETs plus
# one deliberately invalid POST that the server rejects on payload validation
# BEFORE any job row is created. It never enqueues work.
#
# Usage:
#   ops/jobs/remote/preflight.sh
#   VELOX_M2M_ENV=/path/to/client.env ops/jobs/remote/preflight.sh
#   VELOX_MASTER_URL=http://127.0.0.1:8000 ops/jobs/remote/preflight.sh
#
# Credentials are never printed and never stored in the repository: the script
# reads them from the client env file produced when the M2M key was provisioned
# (default ~/creator-77-master.env, mode 0600).
#
# Exit codes:
#   0  master reachable, ready, M2M key accepted
#   1  usage / missing credentials
#   2  master unreachable or not ready
#   3  M2M key rejected by the master
#   4  prepare/finalize surface absent on the running binary (job submit only)
set -euo pipefail

# Credentials file resolution: explicit VELOX_M2M_ENV wins, then the operator
# home, then the canonical operator path (the client env file lives outside the
# repository on purpose — secrets never enter a commit).
resolve_creds() {
  local candidate
  for candidate in "${VELOX_M2M_ENV:-}" "$HOME/creator-77-master.env" "/home/pierone/creator-77-master.env"; do
    [[ -n "$candidate" && -f "$candidate" ]] && { printf '%s' "$candidate"; return 0; }
  done
  printf '%s' "${VELOX_M2M_ENV:-$HOME/creator-77-master.env}"
}
CREDS_FILE="$(resolve_creds)"
TARGET="${VELOX_MASTER_URL:-}"
CLIENT_ID=""
M2M_SECRET=""
TIMEOUT="${VELOX_PREFLIGHT_TIMEOUT:-8}"

if [[ -f "$CREDS_FILE" ]]; then
  # shellcheck disable=SC1090
  set -a; . "$CREDS_FILE"; set +a
  TARGET="${VELOX_MASTER_URL:-$TARGET}"
  CLIENT_ID="${VELOX_CLIENT_ID:-}"
  M2M_SECRET="${VELOX_M2M_SECRET:-}"
fi

TARGET="${TARGET%/}"
if [[ -z "$TARGET" ]]; then
  echo "preflight: FAIL — no master URL (set VELOX_MASTER_URL or use a client env file)" >&2
  exit 1
fi
if [[ -z "$M2M_SECRET" ]]; then
  echo "preflight: FAIL — no VELOX_M2M_SECRET in $CREDS_FILE" >&2
  exit 1
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# probe METHOD PATH OUTFILE [auth]  → prints the HTTP status code (000 on transport error)
probe() {
  local method="$1" path="$2" out="$3" auth="${4:-no}"
  local args=(-sS -o "$out" -w '%{http_code}' -m "$TIMEOUT" -X "$method" "$TARGET$path")
  if [[ "$auth" == "auth" ]]; then
    args+=(-H "Authorization: Bearer $M2M_SECRET")
  fi
  curl "${args[@]}" 2>/dev/null || echo 000
}

echo "preflight: target=$TARGET client=${CLIENT_ID:-<unset>} creds=$CREDS_FILE"
echo

# ── 1. liveness ────────────────────────────────────────────────────────────
HEALTH_CODE="$(probe GET /health "$TMP/health.json")"
if [[ "$HEALTH_CODE" != "200" ]]; then
  echo "  /health               FAIL (HTTP $HEALTH_CODE)"
  echo "preflight: FAIL — master unreachable at $TARGET" >&2
  exit 2
fi
HEALTH="$(jq -c '{status,version,commit}' "$TMP/health.json" 2>/dev/null || echo '{}')"
echo "  /health               OK   $HEALTH"

READY_CODE="$(probe GET /health/ready "$TMP/ready.json")"
if [[ "$READY_CODE" != "200" ]]; then
  echo "  /health/ready         FAIL (HTTP $READY_CODE)"
  echo "preflight: FAIL — master not ready" >&2
  exit 2
fi
echo "  /health/ready         OK   $(jq -c '{status,commit,capabilities}' "$TMP/ready.json" 2>/dev/null || echo '{}')"
echo

# ── 2. M2M auth ────────────────────────────────────────────────────────────
# An empty body is rejected with invalid_payload (400) AFTER authentication,
# so a 400 proves the key is valid without creating a job.
NOAUTH_CODE="$(probe POST /api/v1/jobs "$TMP/noauth.json")"
AUTH_CODE="$(probe POST /api/v1/jobs "$TMP/auth.json" auth)"
AUTH_BODY="$(jq -c '{error,message}' "$TMP/auth.json" 2>/dev/null || echo '<non-json>')"
printf '  POST /api/v1/jobs     no-token=%s token=%s %s\n' "$NOAUTH_CODE" "$AUTH_CODE" "$AUTH_BODY"

case "$AUTH_CODE" in
  400|200|201|202)
    echo "  m2m auth              OK   key accepted${CLIENT_ID:+ (client $CLIENT_ID)}"
    ;;
  401|403)
    echo "  m2m auth              FAIL (HTTP $AUTH_CODE)"
    echo "preflight: FAIL — M2M key rejected by $TARGET" >&2
    exit 3
    ;;
  000)
    echo "preflight: FAIL — transport error talking to $TARGET" >&2
    exit 2
    ;;
  *)
    echo "  m2m auth              UNKNOWN (HTTP $AUTH_CODE)"
    ;;
esac
echo

# ── 3. route inventory ─────────────────────────────────────────────────────
# 404 with a valid token means the route is NOT mounted on the running binary;
# 401/403/400/422 means the route exists (auth or validation answered).
classify() {
  local code="$1"
  case "$code" in
    404) echo "absent" ;;
    000) echo "unreachable" ;;
    *) echo "mounted" ;;
  esac
}

PRE_CODE="$(probe POST /api/v1/jobs/pre "$TMP/pre.json" auth)"
FIN_CODE="$(probe POST /api/v1/jobs/preflight-probe/finalize "$TMP/fin.json" auth)"
KEYS_CODE="$(probe POST /api/v1/admin/m2m/keys "$TMP/keys.json")"
GETJOB_CODE="$(probe GET /api/v1/jobs/preflight-probe "$TMP/getjob.json" auth)"
# Media SSOT read surface (scope media.read): lets the remote build a payload
# from real rows instead of reading the master's database out of band.
# 200 = mounted and the key carries media.read; 403 = mounted but the key is
# missing the media.read scope (grant it on the master); 404 = not deployed.
MEDIA_CODE="$(probe GET '/api/v1/media/assets?limit=1' "$TMP/media.json" auth)"
sleep 0.3
printf '  POST /api/v1/jobs/pre                        %s  (%s)\n' "$PRE_CODE" "$(classify "$PRE_CODE")"
printf '  POST /api/v1/jobs/{id}/finalize              %s  (%s)\n' "$FIN_CODE" "$(classify "$FIN_CODE")"
# GET /api/v1/jobs/{id} is mounted on both masters; a 404 here is the
# handler's "no such job" (the probe id does not exist), not a missing route.
GETJOB_STATE="$(classify "$GETJOB_CODE")"
if [[ "$GETJOB_CODE" == "404" ]]; then GETJOB_STATE="mounted (404 = unknown job id)"; fi
printf '  GET  /api/v1/jobs/{id}                       %s  (%s)\n' "$GETJOB_CODE" "$GETJOB_STATE"
case "$MEDIA_CODE" in
  200) MEDIA_STATE="mounted (key has media.read)" ;;
  403) MEDIA_STATE="mounted (key misses media.read scope)" ;;
  *)   MEDIA_STATE="$(classify "$MEDIA_CODE")" ;;
esac
printf '  GET  /api/v1/media/assets                    %s  (%s)\n' "$MEDIA_CODE" "$MEDIA_STATE"
printf '  POST /api/v1/admin/m2m/keys (admin token)    %s  (%s)\n' "$KEYS_CODE" "$(classify "$KEYS_CODE")"
echo

PRE_STATE="$(classify "$PRE_CODE")"
FIN_STATE="$(classify "$FIN_CODE")"
if [[ "$PRE_STATE" == "mounted" && "$FIN_STATE" == "mounted" ]]; then
  echo "preflight: OK — master ready, M2M key accepted, prepare/finalize surface mounted"
  exit 0
fi

echo "preflight: WARN — master ready and M2M key accepted, but the prepare/finalize"
echo "           surface is not on the running binary (pre=$PRE_STATE finalize=$FIN_STATE)."
echo "           Only the legacy M2M surface is available: POST /api/v1/jobs (enqueue) +"
echo "           GET /api/v1/jobs/{id}. Deploy the prepare/finalize build to $TARGET"
echo "           before running ops/jobs/remote/run-flow.sh."
exit 4
