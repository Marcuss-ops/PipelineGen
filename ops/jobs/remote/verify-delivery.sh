#!/usr/bin/env bash
# verify-delivery.sh — prove that a finished remote job really reached Drive.
#
# The master only reports an artifact URL; the download leg is served by the
# master itself, so a 200 there proves nothing about the `drive-production`
# delivery plan. This script closes that gap:
#
#   1. GET  {master}/api/v1/jobs/{job_id}        → artifact_url + expected size
#   2. stream the artifact                       → sha256 of the delivered bytes
#   3. refresh a Drive access token              → operator OAuth token file
#   4. Drive files.list  name == "<sha256>.f4v"  → the published file + size
#
# The delivery is content-addressed: the worker uploads the render under
# `<sha256-of-the-file>.f4v` (MIME video/mp4) into a per-job folder, so the
# hash computed in step 2 is exactly the name to look for in step 4. A single
# job folder holds a single file.
#
# The artifact is also gated on the canonical audio identity before Drive is
# consulted. `scene.composite.v1` renders VIDEO ONLY, so a silent MP4 is the
# normal output of this lane and `SUCCEEDED` says nothing about audio; the gate
# is `mux-final-audio.sh --verify` (see its header for the contract). Pass
# --allow-silent only to re-verify a legacy artifact that predates it.
#
# Usage:
#   ops/jobs/remote/verify-delivery.sh job_10af228ce8bbd21d
#   VELOX_DRIVE_TOKEN_FILE=/path/token.json ops/jobs/remote/verify-delivery.sh <job_id>
#   ops/jobs/remote/verify-delivery.sh <job_id> --allow-silent
#
# Exit codes: 0 delivered · 1 usage/config · 2 master/transport · 3 artifact
#             download failed · 4 Drive auth failed · 5 NOT on Drive (or size
#             mismatch) · 6 artifact has no canonical audio (see --allow-silent)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
JOB_ID=""
ALLOW_SILENT="${VELOX_ALLOW_SILENT_ARTIFACT:-0}"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --allow-silent) ALLOW_SILENT="1"; shift ;;
    -h|--help) sed -n '2,38p' "${BASH_SOURCE[0]}"; exit 0 ;;
    -*) echo "verify-delivery: unknown argument $1" >&2; exit 1 ;;
    *) JOB_ID="$1"; shift ;;
  esac
done
if [[ -z "$JOB_ID" ]]; then
  sed -n '2,38p' "${BASH_SOURCE[0]}"
  exit 1
fi

DRIVE_CREDENTIALS="${VELOX_DRIVE_CREDENTIALS_FILE:-/home/pierone/.config/velox/credentials.json}"
DRIVE_TOKEN="${VELOX_DRIVE_TOKEN_FILE:-/home/pierone/.config/velox/token.json}"
DRIVE_API="https://www.googleapis.com/drive/v3/files"
OAUTH_TOKEN_URL="https://oauth2.googleapis.com/token"

resolve_creds() {
  local candidate
  for candidate in "${VELOX_M2M_ENV:-}" "$HOME/creator-77-master.env" "/home/pierone/creator-77-master.env"; do
    [[ -n "$candidate" && -f "$candidate" ]] && { printf '%s' "$candidate"; return 0; }
  done
  printf '%s' "${VELOX_M2M_ENV:-$HOME/creator-77-master.env}"
}
CREDS_FILE="$(resolve_creds)"
if [[ ! -f "$CREDS_FILE" ]]; then
  echo "verify-delivery: FAIL — client env $CREDS_FILE not found" >&2
  exit 1
fi
# shellcheck disable=SC1090
set -a; . "$CREDS_FILE"; set +a
TARGET="${VELOX_MASTER_URL:-}"
M2M_SECRET="${VELOX_M2M_SECRET:-}"
if [[ -z "$TARGET" || -z "$M2M_SECRET" ]]; then
  echo "verify-delivery: FAIL — VELOX_MASTER_URL/VELOX_M2M_SECRET missing in $CREDS_FILE" >&2
  exit 1
fi
TARGET="${TARGET%/}"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# ── 1. job state ───────────────────────────────────────────────────────────
CODE="$(curl -sS -o "$TMP/job.json" -w '%{http_code}' -m 20 \
  "$TARGET/api/v1/jobs/$JOB_ID" -H "Authorization: Bearer $M2M_SECRET" || echo 000)"
if [[ "$CODE" == "000" ]]; then
  echo "verify-delivery: FAIL — master unreachable at $TARGET" >&2
  exit 2
fi
STATUS="$(jq -r '.status // empty' "$TMP/job.json" 2>/dev/null || true)"
ARTIFACT_URL="$(jq -r '.artifact_url // empty' "$TMP/job.json" 2>/dev/null || true)"
EXPECTED_SIZE="$(jq -r '.artifact_size_bytes // empty' "$TMP/job.json" 2>/dev/null || true)"
echo "verify-delivery: job=$JOB_ID status=$STATUS size=${EXPECTED_SIZE:-<none>}"
if [[ "$STATUS" != "SUCCEEDED" ]]; then
  echo "verify-delivery: FAIL — job is not SUCCEEDED (status=$STATUS); nothing to verify" >&2
  exit 2
fi
if [[ -z "$ARTIFACT_URL" ]]; then
  echo "verify-delivery: FAIL — no artifact_url on the job" >&2
  exit 2
fi

# ── 2. artifact bytes → sha256 (streamed; never stored) ────────────────────
SHA="$(curl -sS -m 300 "$ARTIFACT_URL" -H "Authorization: Bearer $M2M_SECRET" \
  | tee "$TMP/artifact.mp4" | sha256sum | cut -d' ' -f1)"
if [[ -z "$SHA" ]]; then
  echo "verify-delivery: FAIL — artifact download/read failed" >&2
  exit 3
fi
echo "verify-delivery: artifact sha256=$SHA"

# ── 2b. canonical audio gate ───────────────────────────────────────────────
# The master publishes whatever the worker produced, and the worker produces no
# audio at all. Without this gate a silent artifact verifies as delivered.
if [[ "$ALLOW_SILENT" != "1" ]]; then
  set +e
  "$SCRIPT_DIR/mux-final-audio.sh" --verify "$TMP/artifact.mp4"
  GATE_RC=$?
  set -e
  if [[ "$GATE_RC" != "0" ]]; then
    echo "verify-delivery: FAIL — the delivered artifact does not satisfy the canonical audio contract (gate rc=$GATE_RC)" >&2
    echo "verify-delivery: the worker renders video only; the audio leg is owned by the PipelineGen host" >&2
    echo "verify-delivery: rebuild it with ops/jobs/remote/mux-final-audio.sh (README §8)" >&2
    exit 6
  fi
fi

# ── 3. Drive access token from the operator OAuth files ────────────────────
if [[ ! -f "$DRIVE_CREDENTIALS" || ! -f "$DRIVE_TOKEN" ]]; then
  echo "verify-delivery: FAIL — Drive credentials/token not found:" >&2
  echo "  $DRIVE_CREDENTIALS" >&2
  echo "  $DRIVE_TOKEN" >&2
  exit 4
fi
CLIENT_ID="$(jq -r '.installed.client_id // .web.client_id // empty' "$DRIVE_CREDENTIALS")"
CLIENT_SECRET="$(jq -r '.installed.client_secret // .web.client_secret // empty' "$DRIVE_CREDENTIALS")"
REFRESH_TOKEN="$(jq -r '.refresh_token // empty' "$DRIVE_TOKEN")"
if [[ -z "$CLIENT_ID" || -z "$CLIENT_SECRET" || -z "$REFRESH_TOKEN" ]]; then
  echo "verify-delivery: FAIL — OAuth files lack client_id/client_secret/refresh_token" >&2
  exit 4
fi
ACCESS_TOKEN="$(curl -sS -m 20 -X POST "$OAUTH_TOKEN_URL" \
  -d "client_id=$CLIENT_ID" -d "client_secret=$CLIENT_SECRET" \
  -d "refresh_token=$REFRESH_TOKEN" -d "grant_type=refresh_token" \
  | jq -r '.access_token // empty')"
if [[ -z "$ACCESS_TOKEN" ]]; then
  echo "verify-delivery: FAIL — Drive OAuth refresh rejected" >&2
  exit 4
fi

# ── 4. the published file, looked up by its content address ────────────────
Q="trashed=false and name='$SHA.f4v'"
DRIVE_JSON="$(curl -sS -m 30 -G "$DRIVE_API" \
  --data-urlencode "q=$Q" \
  --data-urlencode 'fields=files(id,name,size,parents,mimeType,createdTime,webViewLink)' \
  --data-urlencode 'spaces=drive' \
  -H "Authorization: Bearer $ACCESS_TOKEN")"
FILES="$(jq -r '.files // [] | length' <<<"$DRIVE_JSON" 2>/dev/null || echo 0)"
if [[ "${FILES:-0}" == "0" ]]; then
  echo "verify-delivery: NOT DELIVERED — no Drive file named $SHA.f4v" >&2
  jq -c '{error:.error}' <<<"$DRIVE_JSON" >&2 2>/dev/null || true
  exit 5
fi
DRIVE_SIZE="$(jq -r '.files[0].size // empty' <<<"$DRIVE_JSON")"
DRIVE_ID="$(jq -r '.files[0].id' <<<"$DRIVE_JSON")"
DRIVE_PARENT="$(jq -r '.files[0].parents[0] // empty' <<<"$DRIVE_JSON")"
echo "verify-delivery: drive file=$DRIVE_ID parent=$DRIVE_PARENT size=$DRIVE_SIZE"
if [[ -n "$EXPECTED_SIZE" && "$EXPECTED_SIZE" != "$DRIVE_SIZE" ]]; then
  echo "verify-delivery: NOT DELIVERED — size mismatch (master=$EXPECTED_SIZE drive=$DRIVE_SIZE)" >&2
  exit 5
fi
echo "verify-delivery: OK — the render is on Drive under its content address"
