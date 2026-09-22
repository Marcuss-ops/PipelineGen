#!/usr/bin/env bash
# verify-remote.sh — prove, from the REMOTE computer, that it can
# (1) SEE the elements in the master's database, (2) SEND it commands, and
# (3) POLL every stage of the resulting job to a correct terminal status.
#
# It is the HTTP equivalent of the operator console, exercised end-to-end with
# nothing but the scoped M2M key — no psql, no SSH, no admin token:
#
#   A. SEE      GET  /api/v1/media/facets, /api/v1/media/assets   (media.read)
#   B. COMMAND  POST /api/v1/jobs                                 (jobs.submit)
#   C. POLL     GET  /api/v1/jobs/{id} → status + current_stage + timeline
#
# Step C is the per-stage surface the remote needs: `GET /api/v1/jobs/{id}`
# answers the SAME enriched shape as the admin `/api/jobs/{id}/full`
# (buildJobResponse), so each pipeline stage — queued → prepare → voiceover →
# document/publish → terminal — is observable with `current_stage` (derived
# from the most recent timeline event) plus the full `timeline` array and the
# job `result`. One job, one poller, every stage.
#
# READ-ONLY BY DEFAULT. Nothing is enqueued unless --yes is passed: the auth
# probe sends deliberately invalid JSON (rejected with 400 AFTER authentication,
# so a 400 proves the key is valid without creating a row).
#
# Usage:
#   ops/jobs/remote/verify-remote.sh                       # see + command matrix
#   ops/jobs/remote/verify-remote.sh --job job_abc123       # + poll that job
#   ops/jobs/remote/verify-remote.sh --yes --payload job.json   # + submit, then poll
#   ops/jobs/remote/verify-remote.sh --yes --type script.generate   # probe a type
#   VELOX_M2M_ENV=/path/client.env ops/jobs/remote/verify-remote.sh
#   VELOX_MASTER_URL=http://127.0.0.1:8000 ops/jobs/remote/verify-remote.sh
#
# Options:
#   --payload FILE     enqueue body (JSON; must carry `type`). Submitted only with --yes.
#   --type TYPE        minimal enqueue body {type,idempotency_key} — proves the command
#                      path for a specific job type (e.g. script.generate)
#   --job ID           poll an existing job instead of submitting one
#   --yes              actually submit --payload/--type (creates a real job)
#   --run-id ID        idempotency-key prefix (default: UTC timestamp)
#   --timeout SEC      poll budget (default 1800)
#   --interval SEC     poll interval (default 10)
#
# Exit codes: 0 ok · 1 usage · 2 transport/unready · 3 auth rejected
#             · 4 catalog read surface not mounted on the running build
#             · 5 job failed/terminal-error · 6 poll timeout
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

PAYLOAD_FILE=""; JOB_TYPE=""; JOB_ID=""; ASSUME_YES="0"
RUN_ID="$(date -u +%Y%m%d-%H%M%S)"; TIMEOUT="1800"; INTERVAL="10"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --payload)  PAYLOAD_FILE="${2:?--payload needs a file}"; shift 2 ;;
    --type)     JOB_TYPE="${2:?--type needs a value}"; shift 2 ;;
    --job)      JOB_ID="${2:?--job needs a job id}"; shift 2 ;;
    --yes|-y)   ASSUME_YES="1"; shift ;;
    --run-id)   RUN_ID="${2:?--run-id needs a value}"; shift 2 ;;
    --timeout)  TIMEOUT="${2:?--timeout needs a value}"; shift 2 ;;
    --interval) INTERVAL="${2:?--interval needs a value}"; shift 2 ;;
    -h|--help)  sed -n '2,47p' "${BASH_SOURCE[0]}"; exit 0 ;;
    *) echo "verify-remote: unknown argument $1" >&2; exit 1 ;;
  esac
done

command -v jq >/dev/null 2>&1 || { echo "verify-remote: FAIL — jq is required" >&2; exit 1; }

resolve_creds() {
  local candidate
  for candidate in "${VELOX_M2M_ENV:-}" "$HOME/creator-77-master.env" "/home/pierone/creator-77-master.env"; do
    [[ -n "$candidate" && -f "$candidate" ]] && { printf '%s' "$candidate"; return 0; }
  done
  printf '%s' "${VELOX_M2M_ENV:-$HOME/creator-77-master.env}"
}
CREDS_FILE="$(resolve_creds)"
TARGET="${VELOX_MASTER_URL:-}"
CLIENT_ID=""; M2M_SECRET=""
if [[ -f "$CREDS_FILE" ]]; then
  # shellcheck disable=SC1090
  set -a; . "$CREDS_FILE"; set +a
  TARGET="${VELOX_MASTER_URL:-$TARGET}"
  CLIENT_ID="${VELOX_CLIENT_ID:-}"
  M2M_SECRET="${VELOX_M2M_SECRET:-}"
fi
TARGET="${TARGET%/}"
if [[ -z "$TARGET" ]]; then
  echo "verify-remote: FAIL — no master URL (set VELOX_MASTER_URL or use a client env file)" >&2
  exit 1
fi
if [[ -z "$M2M_SECRET" ]]; then
  echo "verify-remote: FAIL — no VELOX_M2M_SECRET in $CREDS_FILE" >&2
  echo "                  mint one on the master: ops/jobs/remote/provision-key.sh --client-id <id>" >&2
  exit 1
fi

TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

# probe METHOD PATH OUTFILE [auth|noauth|badtoken] [body-literal]
probe() {
  local method="$1" path="$2" out="$3" auth="${4:-no}" body="${5:-}"
  local args=(-sS -o "$out" -w '%{http_code}' -m "${VELOX_TIMEOUT:-15}" -X "$method" "$TARGET$path")
  case "$auth" in
    auth)     args+=(-H "Authorization: Bearer $M2M_SECRET") ;;
    badtoken) args+=(-H "Authorization: Bearer velox_m2m_not_a_real_secret") ;;
  esac
  if [[ -n "$body" ]]; then
    args+=(-H 'Content-Type: application/json' --data-binary "$body")
  fi
  curl "${args[@]}" 2>/dev/null || echo 000
}

echo "verify-remote: target=$TARGET client=${CLIENT_ID:-<unset>}"
echo

# ── 0. liveness ───────────────────────────────────────────────────────────
# /health is the liveness gate. /health/ready is NOT: a master can serve the
# M2M surface without exposing the readiness endpoint (the local dev build
# answers 404 there), so it is reported and the M2M probes below decide.
code="$(probe GET /health "$TMP/health.json")"
if [[ "$code" != "200" ]]; then
  echo "  /health        FAIL (HTTP $code)"
  echo "verify-remote: FAIL — master unreachable at $TARGET" >&2
  exit 2
fi
echo "  /health        OK $(jq -c '{status,commit,version}' "$TMP/health.json" 2>/dev/null || echo '{}')"

code="$(probe GET /health/ready "$TMP/ready.json")"
case "$code" in
  200) echo "  /health/ready  OK $(jq -c '{status,commit}' "$TMP/ready.json" 2>/dev/null || echo '{}')" ;;
  404) echo "  /health/ready  404 (readiness endpoint not exposed on this build — not a failure)" ;;
  *)   echo "  /health/ready  $code (not a failure; the M2M probes below decide)" ;;
  esac
echo

# ── A. SEE — what is in the master's database ─────────────────────────────
echo "A. SEE the elements (scope media.read)"
MEDIA_OK="0"; MEDIA_FORBIDDEN="0"
FACETS_CODE="$(probe GET "/api/v1/media/facets" "$TMP/facets.json" auth)"
case "$FACETS_CODE" in
  200)
    MEDIA_OK="1"
    echo "  GET /api/v1/media/facets     200"
    jq -r 'to_entries[] |
           "    \(.key): " + (if (.value|type)=="object"
                             then (.value|to_entries|map("\(.key)=\(.value)")|join(", "))
                             else (.value|tostring) end)' "$TMP/facets.json" 2>/dev/null \
      | cut -c1-400 || true
    ;;
  403) echo "  GET /api/v1/media/facets     403 — key exists but lacks media.read"; MEDIA_FORBIDDEN="1" ;;
  401) echo "  GET /api/v1/media/facets     401 — key rejected"; echo "verify-remote: FAIL — auth" >&2; exit 3 ;;
  404) echo "  GET /api/v1/media/facets     404 — media read surface NOT mounted on this build"; ;;
  *)   echo "  GET /api/v1/media/facets     $FACETS_CODE"; ;;
esac

ASSETS_CODE="$(probe GET "/api/v1/media/assets?limit=5" "$TMP/assets.json" auth)"
case "$ASSETS_CODE" in
  200)
    MEDIA_OK="1"
    total="$(jq -r '.total // (.items|length)' "$TMP/assets.json" 2>/dev/null || echo '?')"
    echo "  GET /api/v1/media/assets     200  (total=$total)"
    jq -r '.items[]? | "    \(.id)\t\(.source // "-")\t\(.media_type // "-")\t\(.duration_ms // "-")ms\t\(.drive_file_id // .has_drive_file // "-")"' \
      "$TMP/assets.json" 2>/dev/null || true
    ;;
  403) echo "  GET /api/v1/media/assets     403 — key exists but lacks media.read"; MEDIA_FORBIDDEN="1" ;;
  401) echo "  GET /api/v1/media/assets     401 — key rejected"; echo "verify-remote: FAIL — auth" >&2; exit 3 ;;
  404) echo "  GET /api/v1/media/assets     404 — media read surface NOT mounted on this build"; ;;
  *)   echo "  GET /api/v1/media/assets     $ASSETS_CODE"; ;;
esac
echo

# ── B. COMMAND — can it send jobs? ────────────────────────────────────────
# Deliberately invalid JSON: the middleware authenticates BEFORE the body is
# bound, so 401 (no/bad key) vs 400 (key accepted) is a complete verdict on the
# credential without creating a job row.
echo "B. COMMAND the master (scope jobs.submit)"
NOAUTH="$(probe POST /api/v1/jobs "$TMP/b_noauth.json" noauth '{')"
BADTOK="$(probe POST /api/v1/jobs "$TMP/b_bad.json" badtoken '{')"
GOOD="$(probe POST /api/v1/jobs "$TMP/b_good.json" auth '{')"
printf '  POST /api/v1/jobs   no-token=%s bad-token=%s key=%s\n' "$NOAUTH" "$BADTOK" "$GOOD"
case "$NOAUTH" in
  401|403) echo "    no-token  → rejected (auth enforced)" ;;
  400|422) echo "    no-token  → NOT rejected — security.enable_m2m is off (pass-through admin). Set it true before exposing this host." ;;
  *)       echo "    no-token  → unexpected ($NOAUTH)" ;;
esac
case "$GOOD" in
  400|422) echo "    key       → accepted (validation answered — no job created)" ;;
  401)     echo "    key       → REJECTED"; echo "verify-remote: FAIL — key rejected by $TARGET" >&2; exit 3 ;;
  403)     echo "    key       → rejected/disabled or scope missing"; echo "verify-remote: FAIL — no jobs.submit" >&2; exit 3 ;;
  000)     echo "verify-remote: FAIL — transport error" >&2; exit 2 ;;
  404)     echo "    key       → route not mounted (404)" ;;
  *)       echo "    key       → HTTP $GOOD $(jq -c '{error,message}' "$TMP/b_good.json" 2>/dev/null || true)" ;;
esac
echo

# ── C. SUBMIT (opt-in) ────────────────────────────────────────────────────
SUBMITTED_JOB=""
if [[ -n "$PAYLOAD_FILE" || -n "$JOB_TYPE" ]]; then
  BODY="$TMP/enqueue.json"
  if [[ -n "$PAYLOAD_FILE" ]]; then
    [[ -f "$PAYLOAD_FILE" ]] || { echo "verify-remote: FAIL — payload $PAYLOAD_FILE not found" >&2; exit 1; }
    jq -e '.type and (.type|length>0)' "$PAYLOAD_FILE" >/dev/null \
      || { echo "verify-remote: FAIL — payload must carry a non-empty .type" >&2; exit 1; }
    jq --arg k "$RUN_ID" 'if .idempotency_key then . else .idempotency_key=$k end' \
      "$PAYLOAD_FILE" >"$BODY"
  else
    jq -n --arg t "$JOB_TYPE" --arg k "$RUN_ID" '{type:$t, idempotency_key:$k}' >"$BODY"
  fi

  if [[ "$ASSUME_YES" != "1" ]]; then
    echo "C. SUBMIT — refused without --yes (a real send creates a job on $TARGET)"
    echo "   body that WOULD be posted to POST /api/v1/jobs:"
    jq -c . "$BODY" | cut -c1-400 | sed 's/^/     /'
    echo "   re-run with --yes to submit and poll every stage."
    exit 0
  fi

  echo "C. SUBMIT — POST /api/v1/jobs"
  code="$(probe POST /api/v1/jobs "$TMP/submit.json" auth "$(cat "$BODY")")"
  body="$(jq -c '{job_id,id,status,error,message}' "$TMP/submit.json" 2>/dev/null || cat "$TMP/submit.json")"
  echo "  HTTP $code  $body"
  case "$code" in
    200|201|202) ;;
    401|403) echo "verify-remote: FAIL — not authorized to submit" >&2; exit 3 ;;
    000)     echo "verify-remote: FAIL — transport error" >&2; exit 2 ;;
    *)       echo "verify-remote: FAIL — submit rejected (HTTP $code)" >&2; exit 1 ;;
  esac
  SUBMITTED_JOB="$(jq -r '.job_id // .id // empty' "$TMP/submit.json" 2>/dev/null || true)"
  [[ -n "$SUBMITTED_JOB" ]] || { echo "verify-remote: FAIL — no job_id in the response" >&2; exit 1; }
  JOB_ID="$SUBMITTED_JOB"
  echo "  job_id=$JOB_ID"
  echo
fi

if [[ -z "$JOB_ID" ]]; then
  if [[ "$MEDIA_OK" == "1" ]]; then
    echo "verify-remote: OK — master reachable, key accepted, catalog READABLE, commands authorized."
    echo "                pass --job <id> to poll an existing job, or --yes --payload <file> to submit one."
    exit 0
  fi
  if [[ "$MEDIA_FORBIDDEN" == "1" ]]; then
    echo "verify-remote: FAIL — the key is valid but cannot SEE the catalog: it lacks the media.read scope." >&2
    echo "                re-mint it on the master with --scopes jobs.submit,jobs.read,media.read" >&2
    exit 3
  fi
  echo "verify-remote: PARTIAL — the key is accepted and commands are authorized, but the catalog" >&2
  echo "                read surface (/api/v1/media) is NOT mounted on the running build." >&2
  echo "                Rebuild + restart the master from this tree, then re-run this script." >&2
  exit 4
fi

# ── D. POLL — every stage, to a correct terminal status ───────────────────
echo "D. POLL /api/v1/jobs/$JOB_ID every ${INTERVAL}s (budget ${TIMEOUT}s)"
start="$(date +%s)"; last=""; final_status=""
while :; do
  now="$(date +%s)"
  code="$(probe GET "/api/v1/jobs/$JOB_ID" "$TMP/job.json" auth)"
  if [[ "$code" == "200" ]]; then
    line="$(jq -r '[(.status//"-"),(.current_stage//"-"),((.progress//0)|tostring)]|@tsv' "$TMP/job.json" 2>/dev/null || echo '-')"
    if [[ "$line" != "$last" ]]; then
      printf '  +%3ss  status=%-10s stage=%-22s progress=%s\n' "$((now-start))" \
        "$(jq -r '.status // "-"' "$TMP/job.json")" \
        "$(jq -r '.current_stage // "-"' "$TMP/job.json")" \
        "$(jq -r '.progress // 0' "$TMP/job.json")"
      last="$line"
    fi
    st="$(jq -r '.status // ""' "$TMP/job.json" | tr '[:lower:]' '[:upper:]')"
    case "$st" in
      SUCCEEDED|COMPLETED|SUCCESS|FAILED|ERROR|CANCELLED|CANCELED|DEAD_LETTER)
        final_status="$st"; break ;;
    esac
  elif [[ "$code" == "401" || "$code" == "403" ]]; then
    echo "  GET /api/v1/jobs/$JOB_ID → $code (key lacks jobs.read?)"
    echo "verify-remote: FAIL — cannot poll" >&2; exit 3
  elif [[ "$code" == "000" ]]; then
    echo "  transport error while polling (retrying)"
  else
    echo "  GET /api/v1/jobs/$JOB_ID → HTTP $code"
  fi
  if (( now - start >= TIMEOUT )); then
    echo "  poll budget exhausted after ${TIMEOUT}s (job may still be running)"
    echo "verify-remote: TIMEOUT — last status $line" >&2
    exit 6
  fi
  sleep "$INTERVAL"
done

echo
echo "  final: $(jq -c '{status,current_stage,progress,error,started_at,completed_at}' "$TMP/job.json")"
echo "  stages observed (timeline):"
jq -r '(.timeline // [])[]? | "    \(.type)\t\(.created_at // "-")\t\((.message // "")|.[0:90])"' \
  "$TMP/job.json" 2>/dev/null || echo "    (none)"
echo "  result keys: $(jq -c '(.result // {}) | if type=="object" then keys else type end' "$TMP/job.json" 2>/dev/null || echo '?')"
echo

case "$final_status" in
  SUCCEEDED|COMPLETED|SUCCESS)
    echo "verify-remote: OK — job $JOB_ID reached $final_status; every stage was pollable."
    exit 0 ;;
  *)
    echo "verify-remote: FAIL — job $JOB_ID ended $final_status (see error above)" >&2
    exit 5 ;;
esac
