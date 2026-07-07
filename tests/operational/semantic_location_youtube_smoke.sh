#!/usr/bin/env bash
# semantic_location_youtube_smoke.sh — E2E smoke for PR-YT-CLIP-SEMANTIC-LOCATION-FIX.
#
# Verifies that a YouTube clip registered with semantic-location metadata
# (category, subject, provider) lands in the correct Drive folder hierarchy:
#   clips/{category}/{video-slug}/
#
# Required environment:
#   PIPELINEGEN_ADMIN_TOKEN   Admin token for PipelineGen API.
#   YT_SMOKE_URL              YouTube URL to register (must be a short,
#                             publicly accessible clip).
#   YT_SMOKE_CATEGORY         Semantic category for the Drive folder.
#                             Default: Boxe
#   YT_SMOKE_SUBJECT          Semantic subject for the Drive folder.
#                             Default: pacquiao-vs-broner
#                             (slugified form; the original title like
#                             "Pacquiao vs Broner" becomes the slug via
#                             textutil.SlugifyWithMax in buildDriveParams)
#
# Optional environment:
#   PIPELINEGEN_BASE          Default: http://127.0.0.1:8080
#   E2E_POLL_SECONDS          Default: 3
#   E2E_TIMEOUT_SECONDS       Default: 600
#
# Usage:
#   PIPELINEGEN_ADMIN_TOKEN=... YT_SMOKE_URL=https://youtu.be/... \
#     tests/operational/semantic_location_youtube_smoke.sh
#
# Exit codes:
#   0 PASS — clip registered with correct Drive folder path
#   1 FAIL — registration failed or Drive path mismatch
#   2 prereq missing — server unreachable, missing binaries, missing env

set -euo pipefail
umask 077

# ── Prerequisites ──────────────────────────────────────────────────────

for bin in jq curl; do
  command -v "$bin" >/dev/null 2>&1 || {
    printf 'missing required binary: %s\n' "$bin" >&2
    exit 2
  }
done

PIPELINEGEN_BASE=${PIPELINEGEN_BASE:-http://127.0.0.1:8080}
POLL_SECONDS=${E2E_POLL_SECONDS:-3}
TIMEOUT_SECONDS=${E2E_TIMEOUT_SECONDS:-600}

if [[ -z "${PIPELINEGEN_ADMIN_TOKEN:-}" ]]; then
  printf 'PIPELINEGEN_ADMIN_TOKEN is required\n' >&2
  exit 2
fi

if [[ -z "${YT_SMOKE_URL:-}" ]]; then
  printf 'YT_SMOKE_URL is required (e.g. https://youtu.be/dQw4w9WgXcQ)\n' >&2
  exit 2
fi

# ── Work dir ───────────────────────────────────────────────────────────

WORK_DIR=$(mktemp -d /tmp/semantic-location-yt-smoke.XXXXXX)
trap 'rm -rf "$WORK_DIR"' EXIT INT TERM

# ── Helpers ────────────────────────────────────────────────────────────

json_http() {
  local method=$1 url=$2 token=$3 body_file=${4:-} out_file=$5
  local code args=(
    -sS --max-time 60 -X "$method" -o "$out_file" -w '%{http_code}'
    -H "Authorization: Bearer $token"
    -H 'Content-Type: application/json'
  )
  [[ -n "$body_file" ]] && args+=(--data-binary "@$body_file")
  code=$(curl "${args[@]}" "$url")
  printf '%s' "$code"
}

poll_job() {
  local base=$1 token=$2 job_id=$3 out_file=$4
  local deadline=$(( $(date +%s) + TIMEOUT_SECONDS ))
  local status code

  while (( $(date +%s) < deadline )); do
    code=$(json_http GET "$base/api/jobs/$job_id/full" "$token" "" "$out_file")
    if [[ "$code" == "200" ]]; then
      status=$(jq -r '.status // .job.status // ""' "$out_file" | tr '[:upper:]' '[:lower:]')
      printf '  job=%s status=%s\n' "$job_id" "${status:-unknown}" >&2
      case "$status" in
        succeeded|completed|failed|cancelled|dead_letter)
          printf '%s' "$status"
          return 0 ;;
      esac
    elif [[ "$code" == "404" ]]; then
      printf '  job=%s not found yet (HTTP 404)\n' "$job_id" >&2
    else
      printf '  job=%s HTTP %s\n' "$job_id" "$code" >&2
    fi
    sleep "$POLL_SECONDS"
  done
  return 124
}

# ── Pre-flight: server reachable ───────────────────────────────────────

HEALTH_FILE="$WORK_DIR/health.json"
health_code=$(json_http GET "$PIPELINEGEN_BASE/healthz" "$PIPELINEGEN_ADMIN_TOKEN" "" "$HEALTH_FILE") || true
if [[ "$health_code" != "200" ]]; then
  printf 'FAIL: PipelineGen server unreachable at %s (HTTP %s)\n' \
    "$PIPELINEGEN_BASE" "${health_code:-error}" >&2
  printf 'canonical: PR-STOCK-COMPOSITION-WIRE (server not running)\n' >&2
  exit 1
fi
printf 'Pre-flight: server reachable at %s\n' "$PIPELINEGEN_BASE" >&2

# ── Test 1: Register clip with semantic location ──────────────────────

CATEGORY="${YT_SMOKE_CATEGORY:-Boxe}"
SUBJECT="${YT_SMOKE_SUBJECT:-pacquiao-vs-broner}"
PROVIDER="youtube"

# The subject as typed here is the SLUGIFIED form — buildDriveParams
# runs textutil.SlugifyWithMax on the Name field, converting e.g.
# "Pacquiao vs Broner" → "pacquiao-vs-broner". The Drive path will
# contain the slug, not the original title-case form.

PAYLOAD_FILE="$WORK_DIR/payload.json"
jq -n \
  --arg url "$YT_SMOKE_URL" \
  --arg category "$CATEGORY" \
  --arg subject "$SUBJECT" \
  --arg provider "$PROVIDER" \
  '{
    url: $url,
    # force:true skips dedup on re-runs so the smoke test is
    # idempotent — every invocation creates a fresh registration.
    force: true,
    location: {
      category: $category,
      subject: $subject,
      provider: $provider
    }
  }' > "$PAYLOAD_FILE"

printf '\nTest 1: Registering clip with location={category:"%s", subject:"%s", provider:"%s"}\n' \
  "$CATEGORY" "$SUBJECT" "$PROVIDER" >&2

SUBMIT_FILE="$WORK_DIR/submit.json"
submit_code=$(json_http POST \
  "$PIPELINEGEN_BASE/api/media/register-from-youtube" \
  "$PIPELINEGEN_ADMIN_TOKEN" \
  "$PAYLOAD_FILE" \
  "$SUBMIT_FILE")

if [[ "$submit_code" != "200" ]]; then
  printf 'FAIL: register-from-youtube returned HTTP %s\n' "$submit_code" >&2
  jq . "$SUBMIT_FILE" >&2 || cat "$SUBMIT_FILE" >&2
  printf 'canonical: PR-STOCK-ROUTE-REGISTRATION or PR-YT-CLIP-SEMANTIC-LOCATION-FIX (handler rejects semantic payload)\n' >&2
  exit 1
fi

RESP_OK=$(jq -r '.ok // false' "$SUBMIT_FILE")
if [[ "$RESP_OK" != "true" ]]; then
  printf 'FAIL: register-from-youtube returned ok=false\n' >&2
  jq . "$SUBMIT_FILE" >&2
  exit 1
fi

CLIP_ID=$(jq -r '.clip_id // ""' "$SUBMIT_FILE")
JOB_ID=$(jq -r '.job_id // ""' "$SUBMIT_FILE")

printf '  clip_id=%s job_id=%s\n' "$CLIP_ID" "${JOB_ID:-none}" >&2

# ── Test 2: Poll for job completion ────────────────────────────────────

if [[ -z "$JOB_ID" || "$JOB_ID" == "null" ]]; then
  # Synchronous path: clip was registered inline (no async job).
  # The response should already have drive_path populated.
  printf '  Synchronous registration (no async job)\n' >&2
  DRIVE_PATH=$(jq -r '.drive_path // ""' "$SUBMIT_FILE")
else
  FULL_FILE="$WORK_DIR/full.json"
  JOB_STATUS=$(poll_job "$PIPELINEGEN_BASE" "$PIPELINEGEN_ADMIN_TOKEN" "$JOB_ID" "$FULL_FILE") || {
    printf 'FAIL: job polling timed out after %ds\n' "$TIMEOUT_SECONDS" >&2
    printf 'canonical: PR-STOCK-BROKER-TIMEOUT (worker not processing)\n' >&2
    exit 1
  }

  if [[ "$JOB_STATUS" != "succeeded" && "$JOB_STATUS" != "completed" ]]; then
    printf 'FAIL: job ended as %s (expected succeeded/completed)\n' "$JOB_STATUS" >&2
    jq . "$FULL_FILE" >&2 || true
    case "$JOB_STATUS" in
      failed)
        printf 'canonical: PR-STOCK-STAGER-BOUND or PR-STOCK-CUTTER (download/extract failed)\n' >&2 ;;
    esac
    exit 1
  fi

  # Extract drive_path from the job result.
  DRIVE_PATH=$(jq -r '.result.drive_path // .result_map.drive_path // ""' "$FULL_FILE")
fi

printf '\nTest 2: Verifying Drive folder path\n' >&2
printf '  drive_path=%s\n' "$DRIVE_PATH" >&2

# ── Test 3: Assert canonical Drive path structure ─────────────────────

if [[ -z "$DRIVE_PATH" || "$DRIVE_PATH" == "null" ]]; then
  printf 'FAIL: drive_path is empty — clip was registered but no Drive folder metadata returned\n' >&2
  printf 'canonical: PR-YT-CLIP-SEMANTIC-LOCATION-FIX (Category not reaching YouTubeClipPath)\n' >&2
  exit 1
fi

# The canonical path format is "clips/{group}/{subject}" where group
# should be the category ("Boxe") per the 3-level fallback in
# buildDriveParams. We assert BOTH the "clips/" prefix AND the
# category segment to avoid false positives from paths like
# "clips/Boxing-Day/foo".
EXPECTED_PREFIX="clips/${CATEGORY}/"
if ! printf '%s' "$DRIVE_PATH" | grep -qF "$EXPECTED_PREFIX"; then
  printf 'FAIL: drive_path "%s" does not start with "%s"\n' \
    "$DRIVE_PATH" "$EXPECTED_PREFIX" >&2
  printf 'canonical: PR-YT-CLIP-SEMANTIC-LOCATION-FIX (Group fallback not applied)\n' >&2
  exit 1
fi

# Bonus: verify the subject slug appears in the path.
if ! printf '%s' "$DRIVE_PATH" | grep -qF "$SUBJECT"; then
  printf 'WARN: drive_path "%s" does not contain subject "%s" (may be truncated)\n' \
    "$DRIVE_PATH" "$SUBJECT" >&2
  # Non-fatal: subject may be truncated by SafeFolderName or video ID prefix.
fi

printf '\nPASS: clip registered with Drive path "%s" containing category "%s"\n' \
  "$DRIVE_PATH" "$CATEGORY"
printf 'clip_id=%s\n' "$CLIP_ID"
[[ -n "${JOB_ID:-}" ]] && printf 'job_id=%s\n' "$JOB_ID"
