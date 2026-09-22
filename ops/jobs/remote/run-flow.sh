#!/usr/bin/env bash
# run-flow.sh — drive the remote worker through the prepare/finalize flow.
#
#   pre-job.json  → POST /api/v1/jobs/pre              (phase PREPARE)
#   finalize.json → POST /api/v1/jobs/{job_id}/finalize (phase FINALIZE)
#   poll          → GET  /api/v1/jobs/{job_id}          (M2M read scope)
#
# Nothing is submitted unless --yes is passed: without it the script runs
# preflight, prints the exact requests it would send and exits. A real send
# creates a job on the target master, so keep it explicit.
#
# Usage:
#   ops/jobs/remote/run-flow.sh                      # preflight + print the plan
#   ops/jobs/remote/run-flow.sh --yes --pre          # submit only the pre-job
#   ops/jobs/remote/run-flow.sh --yes --finalize ID  # finalize an existing job
#   ops/jobs/remote/run-flow.sh --yes --all          # pre → poll → finalize → poll
#   ops/jobs/remote/run-flow.sh --yes --all \
#     --pre-payload ops/jobs/remote/dolly5-pre.creator-77.json
#   ops/jobs/remote/run-flow.sh --phases   # the phase sequence this kit drives
#
# Options:
#   --phases                     print the canonical phase sequence this kit
#                                drives (PREPARE/FINALIZE mapping) and exit
#   --surface auto|pre|enqueue   submit surface (auto = /pre when mounted,
#                                otherwise enqueue on POST /api/v1/jobs)
#   --pre-payload FILE           PREPARE payload (default: pre-job.creator-77.json).
#                                The file MUST carry copy_only=true.
#   --finalize-payload FILE      FINALIZE payload (default: finalize-job.creator-77.json)
#   --run-id ID                  idempotency-key prefix (default: UTC timestamp)
#   --timeout SEC                poll budget per phase (default 1800)
#   --interval SEC               poll interval (default 10)
#
# Exit codes: 0 ok · 1 usage · 2 transport/unready · 3 auth · 4 surface absent
#             · 5 job failed/terminal-error · 6 poll timeout
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PRE_PAYLOAD="$SCRIPT_DIR/pre-job.creator-77.json"
FINALIZE_PAYLOAD="$SCRIPT_DIR/finalize-job.creator-77.json"

DO_PRE="0"; DO_FINALIZE="0"; FINALIZE_ID=""; ASSUME_YES="0"
SURFACE="auto"; RUN_ID="$(date -u +%Y%m%d-%H%M%S)"; TIMEOUT="1800"; INTERVAL="10"
SHOW_PHASES="0"

# Canonical phases this kit drives, with the payload field that carries each
# one. The phase NAMES come from the canonical vocabulary owned by
# internal/kernel/observability/registry.go (execution phases) and
# internal/kernel/job/stage_progress.go (workflow stages) — never invented here.
#
# The cover/thumbnail lane is NOT part of this flow: it is produced by the
# owner of the cover lane and must not appear as a phase here (pinning the
# absence in the daemon's own phase contract: see the registry test
# TestRegistry_HasNoCoverPhase).
kit_phases() {
  cat <<'EOF'
  phase          surface   carried by
  -------------  --------  ----------------------------------------------------
  script         PREPARE   pre-job.script_text + scenes[].text
  clips          PREPARE   scenes[].clip{asset_id,drive_file_id,sha256,size_bytes}
  stock          PREPARE   scenes[].stock{asset_id,drive_file_id,sha256,duration_ms}
  overlay        FINALIZE  overlays[]{start_frame,end_frame,frame_count,mode,z_index}
  audio_compile  FINALIZE  runtime_assets[]{kind,role,url,sha256} (music/SFX)
  render         FINALIZE  the worker's certified artifact (sha-addressed, polled to terminal)
  publish        FINALIZE  delivery_plan[].destination_id (drive-production)

  not a phase here: cover/thumbnail (owned outside this pipeline).
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --phases) SHOW_PHASES="1"; shift ;;
    --yes|-y) ASSUME_YES="1"; shift ;;
    --pre) DO_PRE="1"; shift ;;
    --all) DO_PRE="1"; DO_FINALIZE="1"; shift ;;
    --finalize) DO_FINALIZE="1"; FINALIZE_ID="${2:?--finalize needs a job_id}"; shift 2 ;;
    --pre-payload) PRE_PAYLOAD="${2:?--pre-payload needs a file}"; shift 2 ;;
    --finalize-payload) FINALIZE_PAYLOAD="${2:?--finalize-payload needs a file}"; shift 2 ;;
    --surface) SURFACE="${2:?--surface needs a value}"; shift 2 ;;
    --run-id) RUN_ID="${2:?--run-id needs a value}"; shift 2 ;;
    --timeout) TIMEOUT="${2:?--timeout needs a value}"; shift 2 ;;
    --interval) INTERVAL="${2:?--interval needs a value}"; shift 2 ;;
    -h|--help) sed -n '2,39p' "${BASH_SOURCE[0]}"; exit 0 ;;
    *) echo "run-flow: unknown argument $1" >&2; exit 1 ;;
  esac
done

if [[ "$SHOW_PHASES" == "1" ]]; then
  echo "run-flow: canonical phases driven by this kit (target ${VELOX_MASTER_URL:-<unset>})"
  kit_phases
  exit 0
fi

if [[ "$DO_PRE" == "0" && "$DO_FINALIZE" == "0" ]]; then
  "$SCRIPT_DIR/preflight.sh" || true
  cat <<EOF

run-flow: nothing submitted (dry plan).

run-flow: canonical phases (run-flow.sh --phases for the full table):
EOF
  kit_phases
  cat <<EOF

  plan for --yes --all:
    1. POST {master}/api/v1/jobs/pre                     body: ${PRE_PAYLOAD}
       → 202 PREPARE, dispatch_status=waiting_runtime_assets
    2. GET  {master}/api/v1/jobs/{job_id}                confirm the job is visible
       (a pre-only job stays PENDING and is never claimed: do NOT wait here)
    3. POST {master}/api/v1/jobs/{job_id}/finalize       body: ${FINALIZE_PAYLOAD}
    4. GET  {master}/api/v1/jobs/{job_id}                poll to terminal
EOF
  exit 0
fi

resolve_creds() {
  local candidate
  for candidate in "${VELOX_M2M_ENV:-}" "$HOME/creator-77-master.env" "/home/pierone/creator-77-master.env"; do
    [[ -n "$candidate" && -f "$candidate" ]] && { printf '%s' "$candidate"; return 0; }
  done
  printf '%s' "${VELOX_M2M_ENV:-$HOME/creator-77-master.env}"
}
CREDS_FILE="$(resolve_creds)"
if [[ ! -f "$CREDS_FILE" ]]; then
  echo "run-flow: FAIL — credentials file $CREDS_FILE not found" >&2; exit 1
fi
# shellcheck disable=SC1090
set -a; . "$CREDS_FILE"; set +a
TARGET="${VELOX_MASTER_URL:-}"
M2M_SECRET="${VELOX_M2M_SECRET:-}"
if [[ -z "$TARGET" || -z "$M2M_SECRET" ]]; then
  echo "run-flow: FAIL — VELOX_MASTER_URL/VELOX_M2M_SECRET missing in $CREDS_FILE" >&2; exit 1
fi
TARGET="${TARGET%/}"
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

api() { # method path body-file-or-empty out-file
  local method="$1" path="$2" body="${3:-}" out="$4"
  local args=(-sS -o "$out" -w '%{http_code}' -m 30 -X "$method" "$TARGET$path"
              -H "Authorization: Bearer $M2M_SECRET" -H 'Content-Type: application/json'
              -H "X-Request-ID: ${RUN_ID}" )
  if [[ -n "$body" ]]; then args+=(--data-binary "@$body"); fi
  curl "${args[@]}" 2>/dev/null || echo 000
}

# Pre-flight gate: the target must be ready and the key accepted before we
# think about submitting. Exit 4 (prepare/finalize surface absent) is tolerated
# here: the enqueue surface may still be usable, and submitting is what tells
# us which one the target actually answers.
set +e
"$SCRIPT_DIR/preflight.sh" >"$TMP/preflight.txt" 2>&1
PF_STATUS=$?
set -e
cat "$TMP/preflight.txt"
if [[ "$PF_STATUS" != "0" && "$PF_STATUS" != "4" ]]; then exit "$PF_STATUS"; fi
echo

pre_payload="$TMP/pre.json"
[[ -r "$PRE_PAYLOAD" ]] || { echo "run-flow: FAIL — PRE payload not readable: $PRE_PAYLOAD" >&2; exit 1; }
jq --arg k "$RUN_ID-pre" '.idempotency_key = $k' "$PRE_PAYLOAD" >"$pre_payload"
fin_payload="$TMP/finalize.json"
[[ -r "$FINALIZE_PAYLOAD" ]] || { echo "run-flow: FAIL — FINALIZE payload not readable: $FINALIZE_PAYLOAD" >&2; exit 1; }
jq --arg k "$RUN_ID-finalize" '.idempotency_key = $k' "$FINALIZE_PAYLOAD" >"$fin_payload"
if [[ "$(jq -r '.copy_only // false' "$pre_payload")" != "true" ]]; then
  echo "run-flow: FAIL — PRE payload must carry copy_only=true" >&2
  exit 2
fi

poll_job() { # job_id label
  local job="$1" label="$2" start now status body code
  start="$(date +%s)"
  while :; do
    code="$(api GET "/api/v1/jobs/$job" "" "$TMP/poll.json")"
    if [[ "$code" == "404" ]]; then
      echo "run-flow: [$label] job $job not visible on $TARGET (HTTP 404)" >&2; return 4
    fi
    status="$(jq -r '.status // .job.status // empty' "$TMP/poll.json" 2>/dev/null || true)"
    now=$(( $(date +%s) - start ))
    printf 'run-flow: [%s] t=%ss status=%s\n' "$label" "$now" "${status:-<none>}"
    case "${status^^}" in
      SUCCEEDED|SUCCEEDED_WITH_WARNINGS|COMPLETED|INDEX_PENDING|INDEXED) return 0 ;;
      FAILED|ERROR|CANCELLED|DEAD_LETTER)
        jq -c '{status, error, dispatch_status, artifact}' "$TMP/poll.json" 2>/dev/null || cat "$TMP/poll.json"
        return 5 ;;
      *) : ;; # QUEUED / RUNNING / PENDING / unknown → keep polling
    esac
    [[ "$now" -lt "$TIMEOUT" ]] || { echo "run-flow: [$label] poll timeout after ${TIMEOUT}s" >&2; return 6; }
    sleep "$INTERVAL"
  done
}

# confirm_pre_visible job_id
#
# PREPARE is a two-stage submit: /pre answers 202 with
# dispatch_status=waiting_runtime_assets and the job stays PENDING (no worker
# claim, started_at null) until the FINALIZE call for the same job_id arrives.
# Verified live 2026-09-20: a pre-only job was still PENDING after 56 s.
# So the pre phase MUST NOT poll for a terminal status — it only confirms the
# job is visible on the master before we finalize it.
confirm_pre_visible() { # job_id
  local job="$1" code status
  code="$(api GET "/api/v1/jobs/$job" "" "$TMP/poll.json")"
  if [[ "$code" == "404" ]]; then
    echo "run-flow: [pre] job $job not visible on $TARGET (HTTP 404)" >&2; return 4
  fi
  status="$(jq -r '.status // .job.status // empty' "$TMP/poll.json" 2>/dev/null || true)"
  printf 'run-flow: [pre] status=%s (ready for finalize)\n' "${status:-<none>}"
  case "${status^^}" in
    FAILED|ERROR|CANCELLED|DEAD_LETTER)
      jq -c '{status, error, dispatch_status}' "$TMP/poll.json" 2>/dev/null || cat "$TMP/poll.json"
      return 5 ;;
  esac
  return 0
}

submit_pre() { # → prints job_id, returns via stdout
  local code body_job
  if [[ "$SURFACE" == "enqueue" ]]; then
    local enq="$TMP/enqueue.json"
    jq -n --slurpfile p "$pre_payload" --arg k "$RUN_ID-pre" \
      '{type: $p[0].job_type, video_name: $p[0].video_name, idempotency_key: $k,
        payload: {script_text: $p[0].script_text, scenes: $p[0].scenes,
                  output: $p[0].output, copy_only: $p[0].copy_only, delivery_plan: $p[0].delivery_plan}}' >"$enq"
    echo "run-flow: POST /api/v1/jobs (legacy enqueue surface)" >&2
    code="$(api POST /api/v1/jobs "$enq" "$TMP/submit.json")"
  else
    echo "run-flow: POST /api/v1/jobs/pre" >&2
    code="$(api POST /api/v1/jobs/pre "$pre_payload" "$TMP/submit.json")"
    if [[ "$code" == "404" && "$SURFACE" == "auto" ]]; then
      echo "run-flow: /api/v1/jobs/pre is not mounted on $TARGET (404)." >&2
      echo "run-flow: retry with --surface=enqueue, or deploy the prepare/finalize build there." >&2
      return 4
    fi
  fi
  echo "run-flow: submit HTTP $code $(jq -c '{job_id,id,status,phase,dispatch_status,error,message}' "$TMP/submit.json" 2>/dev/null || cat "$TMP/submit.json")" >&2
  case "$code" in
    200|201|202) ;;
    401|403) return 3 ;;
    000) return 2 ;;
    404) return 4 ;;
    *) return 1 ;;
  esac
  body_job="$(jq -r '.job_id // .id // empty' "$TMP/submit.json" 2>/dev/null || true)"
  [[ -n "$body_job" ]] || { echo "run-flow: no job_id in response" >&2; return 1; }
  printf '%s' "$body_job"
}

if [[ "$ASSUME_YES" != "1" ]]; then
  echo "run-flow: refusing to submit without --yes (target $TARGET, run-id $RUN_ID)"
  echo "run-flow: requests that WOULD be sent:"
  echo "  POST $TARGET/api/v1/jobs/pre"; jq -c . "$pre_payload" | sed 's/^/    /'
  if [[ "$DO_FINALIZE" == "1" ]]; then
    echo "  POST $TARGET/api/v1/jobs/{job_id}/finalize"; jq -c . "$fin_payload" | sed 's/^/    /'
  fi
  exit 0
fi

JOB_ID="${FINALIZE_ID:-}"
if [[ "$DO_PRE" == "1" ]]; then
  JOB_ID="$(submit_pre)" || exit $?
  echo "run-flow: job_id=$JOB_ID"
  if [[ "$SURFACE" == "enqueue" ]]; then
    # The legacy enqueue surface is single-stage: the job really runs now.
    poll_job "$JOB_ID" enqueue || exit $?
  else
    confirm_pre_visible "$JOB_ID" || exit $?
  fi
fi

if [[ "$DO_FINALIZE" == "1" && "$SURFACE" == "enqueue" && -z "$FINALIZE_ID" ]]; then
  echo "run-flow: --surface=enqueue is single-stage; nothing to finalize"
  DO_FINALIZE="0"
fi

if [[ "$DO_FINALIZE" == "1" ]]; then
  [[ -n "$JOB_ID" ]] || { echo "run-flow: --finalize needs a job_id" >&2; exit 1; }
  echo "run-flow: POST /api/v1/jobs/$JOB_ID/finalize"
  code="$(api POST "/api/v1/jobs/$JOB_ID/finalize" "$fin_payload" "$TMP/finalize_resp.json")"
  echo "run-flow: finalize HTTP $code $(jq -c '{phase,dispatch_status,future_asset_plan,error,message}' "$TMP/finalize_resp.json" 2>/dev/null || cat "$TMP/finalize_resp.json")"
  case "$code" in
    200|201|202) ;;
    401|403) exit 3 ;;
    404) echo "run-flow: finalize surface absent for job $JOB_ID (404)" >&2; exit 4 ;;
    *) exit 1 ;;
  esac
  poll_job "$JOB_ID" finalize || exit $?
fi

echo "run-flow: done (run-id $RUN_ID)"
