#!/usr/bin/env bash
# scripts/certify_clip_lane.sh — live single-clip certification through the
# canonical POST /api/clips/render endpoint.
#
# Why a script and not a Go test: the certificate must exercise the WIRE the
# product exposes (payload → 202 → submit job → settle continuation child →
# derived asset), so the checked-in evidence is produced by the same endpoint
# an operator calls. The Go suites certify the in-process boundaries; this
# certifies the deployed one.
#
# Usage:
#   VELOX_ADMIN_TOKEN=... scripts/certify_clip_lane.sh plain
#   VELOX_ADMIN_TOKEN=... scripts/certify_clip_lane.sh v2
#
# Modes:
#   plain : background none, no subtitles, no watermark — the pure GPU-native
#           path, used as the per-run speed baseline.
#   v2    : editorial background asset + burned subtitles + watermark — the
#           "Clip v2 subs & background" production case.
#
# Optional environment:
#   VELOX_BASE_URL            default http://127.0.0.1:8000
#   CERT_CLIP_ASSET_ID        default yt_iHaK0M-207o_60_70_v1 (10 s, 10 READY tracks)
#   CERT_CLIP_LANGUAGE        default en
#   CERT_BACKGROUND_ASSET_ID  REQUIRED for v2 — no default (see below)
#   CERT_QUEUE_URL            default http://127.0.0.1:8081 (the RenderingGen
#                             queue; used by the worker preflight below)
#   CERT_SKIP_WORKER_PREFLIGHT set to 1 to bypass the preflight (see below)
#
# v2 prerequisite: the background asset must have a row in the PostgreSQL media
# SSOT, which clip.render resolves against, and its id must be a CANONICAL
# editorial plate. There is deliberately NO DEFAULT any more.
#
# `CERT_BACKGROUND_ASSET_ID` used to default to `classic1`, which is a LEGACY
# alias deliberately outside the curated editorial catalog: the bridge that
# writes plate rows (internal/app/wiring/media/editorial_assets_bootstrap.go,
# driven by `go run ./cmd/admin register-editorial-assets`) does not register it,
# so on a clean database the default v2 payload died in prepare with
#   asset "classic1" not found in postgres media SSOT
# and on the development host it worked only because a hand-written row happened
# to exist there. A default that cannot work on a clean deployment is not a
# default, it is a trap: the id is now REQUIRED, and an unset one fails before a
# job is submitted.
#
# The canonical plates are `drive-background-NN`, registered first with that
# same command; it verifies the bytes it read against the certified plate hash
# and fails closed when the plate's Drive file is gone (observed 2026-09-17:
# Drive 404 for every curated plate id, so no curated plate is registrable on a
# clean database until those files are restored).
#   CERT_DRIVE_FOLDER_ID      default 1ST6FxPuRaxwBOIz39MAN8Jj4gDv509-K
#   CERT_WATERMARK_TEXT       default VeloxEditing; v2 renders burn this text. It
#                             is the documented way to request a FRESH v2 render:
#                             the render identity deliberately has no cache-bypass
#                             flag (cliprender/fingerprint.go), so "different
#                             bytes" means "different content" — and the watermark
#                             text is content.
#   CERT_RENDER_TIMEOUT_SEC   default 300
#   CERT_EVIDENCE_DIR         default ops/benchmarks
set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd -- "$SCRIPT_DIR/.." && pwd)"

MODE="${1:-}"
case "$MODE" in
  plain|v2) ;;
  *) echo "usage: $0 {plain|v2}" >&2; exit 2 ;;
esac

BASE_URL="${VELOX_BASE_URL:-http://127.0.0.1:8000}"
QUEUE_URL="${CERT_QUEUE_URL:-http://127.0.0.1:8081}"
CLIP_ASSET_ID="${CERT_CLIP_ASSET_ID:-yt_iHaK0M-207o_60_70_v1}"
LANGUAGE="${CERT_CLIP_LANGUAGE:-en}"
BACKGROUND_ASSET_ID="${CERT_BACKGROUND_ASSET_ID:-}"
SKIP_WORKER_PREFLIGHT="${CERT_SKIP_WORKER_PREFLIGHT:-0}"
DRIVE_FOLDER_ID="${CERT_DRIVE_FOLDER_ID:-1ST6FxPuRaxwBOIz39MAN8Jj4gDv509-K}"
WATERMARK_TEXT="${CERT_WATERMARK_TEXT:-VeloxEditing}"
TIMEOUT_SEC="${CERT_RENDER_TIMEOUT_SEC:-300}"
EVIDENCE_DIR="${CERT_EVIDENCE_DIR:-$ROOT_DIR/ops/benchmarks}"

TOKEN="${VELOX_ADMIN_TOKEN:-}"
if [ -z "$TOKEN" ]; then
  echo "VELOX_ADMIN_TOKEN is not set. Export it (see AGENTS.md) before running." >&2
  exit 1
fi

# v2 needs an explicit, canonical plate: see the header for why there is no
# default. Checking it here means the operator learns this before a job is
# created, instead of from a `prepare` failure two minutes later.
if [ "$MODE" = "v2" ] && [ -z "$BACKGROUND_ASSET_ID" ]; then
  echo "CERT_BACKGROUND_ASSET_ID is required for v2: pass a canonical editorial plate" >&2
  echo "  (drive-background-NN registered via 'go run ./cmd/admin register-editorial-assets')," >&2
  echo "  not the legacy alias 'classic1', which no clean database can resolve." >&2
  exit 2
fi

# ── Preflight: a certificate is only meaningful if a worker can claim the job ─
#
# A worker row is not a worker. Live observation (2026-09-17): the queue listed
# seventeen rows all reporting `status: ready`, of which exactly one had a live
# heartbeat — the rest ranged from six hours to fourteen days stale, and the
# deployed worker was frozen (all threads stopped) for hours while still reading
# as ready. A run started in that state does not fail; it hangs in the queue and
# gets attributed to the renderer.
#
# The queue derives `liveness` from the heartbeat age (ready/degraded/stale/dead)
# precisely so this check can be a fact instead of a guess: it requires at least
# one worker whose liveness is `ready` RIGHT NOW.
if [ "$SKIP_WORKER_PREFLIGHT" != "1" ]; then
  WORKERS_JSON=$(curl -sS -m 10 "$QUEUE_URL/workers" 2>/dev/null || true)
  if [ -z "$WORKERS_JSON" ]; then
    echo "preflight: cannot read $QUEUE_URL/workers — refusing to certify against an unknown fleet" >&2
    echo "  (set CERT_QUEUE_URL, or CERT_SKIP_WORKER_PREFLIGHT=1 to override deliberately)" >&2
    exit 3
  fi
  READY_IDS=$(printf '%s' "$WORKERS_JSON" | jq -r '[.[] | select(.liveness == "ready") | .id] | join(",")')
  if [ -z "$READY_IDS" ]; then
    echo "preflight FAILED: no worker in $QUEUE_URL reports liveness=ready" >&2
    printf '%s' "$WORKERS_JSON" | jq -r '.[] | "  \(.id)\tstatus=\(.status)\tliveness=\(.liveness)\tlast_heartbeat=\(.last_heartbeat_at)"' >&2
    echo "  A job submitted now would sit unclaimed. Restart the worker (systemctl restart renderinggen-worker) and retry." >&2
    exit 3
  fi
  echo "preflight ok: ready workers = $READY_IDS"
fi

if [ "$MODE" = "plain" ]; then
  PAYLOAD=$(cat <<JSON
{
  "source_asset_id": "${CLIP_ASSET_ID}",
  "background": {"mode": "none"},
  "subtitles": {"enabled": false},
  "watermark": {"enabled": false},
  "output": {"contract": "VELOX_ASSEMBLY_READY_V1", "width": 1920, "height": 1080, "fps_num": 24, "fps_den": 1},
  "audio": {"mode": "copy_if_compatible"},
  "execution": {"require_gpu": true},
  "destination": {"drive_folder_id": "${DRIVE_FOLDER_ID}", "subfolder_name": "clip-lane-certification"}
}
JSON
)
else
  PAYLOAD=$(cat <<JSON
{
  "source_asset_id": "${CLIP_ASSET_ID}",
  "background": {"mode": "asset", "asset_id": "${BACKGROUND_ASSET_ID}", "kind": "video"},
  "transcript": {"mode": "reuse", "language": "${LANGUAGE}"},
  "subtitles": {"enabled": true, "mode": "burn"},
  "watermark": {"enabled": true, "text": "${WATERMARK_TEXT}", "position": "top_right"},
  "output": {"contract": "VELOX_ASSEMBLY_READY_V1", "width": 1920, "height": 1080, "fps_num": 24, "fps_den": 1},
  "audio": {"mode": "copy_if_compatible"},
  "execution": {"require_gpu": true},
  "destination": {"drive_folder_id": "${DRIVE_FOLDER_ID}", "subfolder_name": "clip-lane-certification"}
}
JSON
)
fi

STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$EVIDENCE_DIR"
OUT="$EVIDENCE_DIR/clip-lane-${MODE}-${STAMP}.json"

T_START=$(date +%s%3N)
RESP=$(curl -sS -X POST "$BASE_URL/api/clips/render" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d "$PAYLOAD")
SUBMIT_MS=$(( $(date +%s%3N) - T_START ))

JOB_ID=$(printf '%s' "$RESP" | jq -r '.job_id // empty')
if [ -z "$JOB_ID" ]; then
  # A render-cache hit is a RESULT, not a failed submission: the request
  # fingerprint is the render identity (cliprender/fingerprint.go: "the same
  # visual contract is ONE render however many times it is requested"), so an
  # identical certification re-run answers CACHED with the already certified
  # artifact instead of a new job. Reporting that as "submit failed" made a
  # clean re-run of this script look like a broken lane.
  CACHED_STATUS=$(printf '%s' "$RESP" | jq -r '.status // empty')
  if [ "$CACHED_STATUS" = "CACHED" ]; then
    jq -n \
      --arg mode "$MODE" \
      --arg clip "$CLIP_ASSET_ID" \
      --arg language "$LANGUAGE" \
      --arg background "$BACKGROUND_ASSET_ID" \
      --argjson submit_wall_ms "$SUBMIT_MS" \
      --argjson payload "$PAYLOAD" \
      --argjson response "$RESP" \
      '{mode: $mode, clip_asset_id: $clip, language: $language, background_asset_id: $background,
        status: "CACHED", submit_wall_ms: $submit_wall_ms, response: $response, payload: $payload}' > "$OUT"
    echo "status=CACHED (render-cache hit: the fingerprint was already certified) evidence=$OUT"
    jq '{status, asset_id: .response.asset_id, fingerprint: .response.fingerprint, artifact: .response.artifact}' "$OUT"
    exit 0
  fi
  echo "submit failed: $RESP" >&2
  exit 1
fi
echo "mode=$MODE clip=$CLIP_ASSET_ID submit_job=$JOB_ID submit_wall_ms=$SUBMIT_MS"

# Resolve the settle continuation child: the SUBMIT job's contract is satisfied
# the moment it handed off, so the artifact lives on the CHILD.
CHILD_ID=""
for _ in $(seq 1 600); do
  JOB_DATA=$(curl -sS -H "Authorization: Bearer $TOKEN" "$BASE_URL/api/jobs/$JOB_ID")
  CHILD_ID=$(printf '%s' "$JOB_DATA" | jq -r '.job.result.child_job_id // empty')
  STATUS=$(printf '%s' "$JOB_DATA" | jq -r '.job.status // empty')
  [ -n "$CHILD_ID" ] && break
  if [ "$STATUS" = "FAILED" ] || [ "$STATUS" = "CANCELLED" ]; then
    echo "submit job $JOB_ID terminal without child: $STATUS" >&2
    printf '%s' "$JOB_DATA" | jq '.job.error' >&2
    exit 1
  fi
  sleep 0.2
done
if [ -z "$CHILD_ID" ]; then
  echo "no settle child appeared for $JOB_ID" >&2
  exit 1
fi
echo "settle_child=$CHILD_ID"

DEADLINE=$(( $(date +%s) + TIMEOUT_SEC ))
TIMED_OUT=0
while :; do
  CHILD_DATA=$(curl -sS -H "Authorization: Bearer $TOKEN" "$BASE_URL/api/jobs/$CHILD_ID")
  C_STATUS=$(printf '%s' "$CHILD_DATA" | jq -r '.job.status // empty')
  case "$C_STATUS" in
    SUCCEEDED|PARTIALLY_SUCCEEDED|FAILED|CANCELLED) break ;;
  esac
  if [ "$(date +%s)" -ge "$DEADLINE" ]; then
    # A certificate that only records SUCCESS cannot certify anything: a
    # timeout is itself a result (and today the interesting failures are
    # rejections, not hangs). Keep the run in the evidence and exit non-zero.
    TIMED_OUT=1
    C_STATUS="TIMEOUT_${C_STATUS:-UNKNOWN}"
    break
  fi
  sleep 0.5
done

WALL_MS=$(( $(date +%s%3N) - T_START ))
jq -n \
  --arg mode "$MODE" \
  --arg clip "$CLIP_ASSET_ID" \
  --arg language "$LANGUAGE" \
  --arg background "$BACKGROUND_ASSET_ID" \
  --arg submit_job "$JOB_ID" \
  --arg settle_child "$CHILD_ID" \
  --arg status "$C_STATUS" \
  --argjson submit_wall_ms "$SUBMIT_MS" \
  --argjson total_wall_ms "$WALL_MS" \
  --argjson payload "$PAYLOAD" \
  --argjson job "$CHILD_DATA" \
  '{mode: $mode, clip_asset_id: $clip, language: $language, background_asset_id: $background,
    submit_job_id: $submit_job, settle_child_job_id: $settle_child, status: $status,
    submit_wall_ms: $submit_wall_ms, total_wall_ms: $total_wall_ms,
    payload: $payload, job: $job}' > "$OUT"

echo "status=$C_STATUS total_wall_ms=$WALL_MS evidence=$OUT"
# Projection of the fields the endpoint actually returns. It used to read
# .job.result.asset.sha256/width/height/facts, which the master does not publish
# (the certified stream facts live on the RenderingGen queue artifact record:
# curl $RENDERINGGEN_QUEUE_URL/jobs/<render job id> -> .artifact.output_facts),
# so the summary printed nulls for every certified fact.
jq '{
  status: .status,
  contract: .job.result.contract,
  render_wall_ms: .job.result.render.render_wall_ms,
  fps: {render: .job.result.render.metrics_v2.render_fps, total: .job.result.render.metrics_v2.total_fps, realtime_factor: .job.result.render.metrics_v2.realtime_factor},
  gpu: {
    native_encode_frames: .job.result.render.metrics_v2.nv12_to_rgba_frames,
    rgba_to_nv12_frames: .job.result.render.metrics_v2.rgba_to_nv12_frames,
    subtitle_raster_cpu: .job.result.render.metrics_v2.subtitle_raster_cpu
  },
  audio: {copy_eligible: .job.result.render.audio_copy_eligible, encode_passes: .job.result.render.audio_encode_passes},
  timings: .job.result.timings,
  asset: .job.result.asset,
  phases: [.job.result.timings.phases[]? | {phase, wall_ms, work_ms}]
}' "$OUT"

if [ "$TIMED_OUT" -eq 1 ]; then
  echo "timed out after ${TIMEOUT_SEC}s waiting for $CHILD_ID; see $OUT" >&2
  exit 1
fi
if [ "$C_STATUS" = "FAILED" ] || [ "$C_STATUS" = "CANCELLED" ]; then
  jq '.job.error' "$OUT" >&2
  exit 1
fi
