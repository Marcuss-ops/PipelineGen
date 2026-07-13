#!/usr/bin/env bash
#
# script_with_images_smoke.sh — refactored canary smoke for the
# scene-images script-generation path. Replaces scripts/tools/test_script_with_images.py
# in spirit (same payload + same assertion contract) while matching the
# pacquiao_broner .sh/.json precedent (smoke.sh wrapper + test.json payload).
#
# godlike/06 SSOT: this wrapper reads tests/operational/script_with_images_test.json
# for the canonical payload and applies env-driven correlation_id + style
# overrides via lib/common.sh.
# godlike/07 fail-closed: capability-gate refuses green when script is NOT_MOUNTED.
# scripts/tools/test_script_with_images.py is retained per "mantieni" and remains
# the legacy Python reference implementation; this .sh is the canonical canary.
#
# Usage:
#   ./script_with_images_smoke.sh
#
# Env (resolved by lib/common.sh::smoke_resolve_token):
#   VELOX_ADMIN_TOKEN    admin bearer token
#   SCRIPT_IMAGE_STYLE   style override (default: "cinematic")

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

PAYLOAD_FILE="${PAYLOAD_FILE:-$DIR/script_with_images_test.json}"
SCRIPT_IMAGE_STYLE="${SCRIPT_IMAGE_STYLE:-cinematic}"

if [[ ! -f "$PAYLOAD_FILE" ]]; then
    printf '%ssetup error: payload not found: %s%s\n' "$RED" "$PAYLOAD_FILE" "$RESET" >&2
    exit 2
fi

smoke_require jq curl

# godlike/07 — capability gate FIRST.
CAPS=$(curl -s --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" \
        "${SMOKE_API_BASE%/}/api/capabilities")
SCRIPT_CAP=$(echo "$CAPS" | jq -r '.capabilities.script // "UNKNOWN"')

smoke_log_section "Capability gate"
printf '  script: %s\n' "$SCRIPT_CAP"

if [[ "$SCRIPT_CAP" != "MOUNTED" ]]; then
    printf '%sFAIL: script capability is %s (required MOUNTED)%s\n' "$RED" "$SCRIPT_CAP" "$RESET" >&2
    exit 1
fi

# Build the request body: copy the canonical JSON, override dynamic fields.
WORK_BODY="$WORK_DIR/script_with_images_body.json"
mkdir -p "$WORK_DIR"
CORRELATION_ID="${SCRIPT_IMAGE_STYLE}-$(smoke_gen_uuid)"
jq \
    --arg style "$SCRIPT_IMAGE_STYLE" \
    --arg cid "$CORRELATION_ID" \
    '.items[0].style = $style | .correlation_id = $cid' \
    "$PAYLOAD_FILE" > "$WORK_BODY"

smoke_log_section "POST /api/script/generate"
smoke_curl POST "/api/script/generate" \
    -H "Idempotency-Key: $(smoke_gen_uuid)" \
    --data @"$WORK_BODY" >/dev/null
HTTP_DISPATCH="$SMOKE_LAST_HTTP"
if [[ "$HTTP_DISPATCH" != "202" && "$HTTP_DISPATCH" != "200" ]]; then
    printf '%sFAIL: dispatch HTTP %s (accepted: 200, 202)%s\n' "$RED" "$HTTP_DISPATCH" "$RESET" >&2
    smoke_echo_safe "$(head -c 600 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi
printf '  dispatch HTTP: %s\n' "$HTTP_DISPATCH"

JOB_ID=$(jq -r '.job_id // ""' "$SMOKE_LAST_BODY")
if [[ -z "$JOB_ID" || "$JOB_ID" == "null" ]]; then
    printf '%sFAIL: dispatch returned no job_id%s\n' "$RED" "$RESET" >&2
    smoke_echo_safe "$(head -c 600 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi
printf '  job_id: %s\n' "$JOB_ID"

smoke_log_section "Poll /api/jobs/${JOB_ID}/full"
if ! smoke_poll_terminal "$JOB_ID"; then
    rc=$?
    printf '%sFAIL: polling did not reach terminal (rc=%d, status=%s)%s\n' "$RED" "$rc" "${SMOKE_LAST_STATUS:-?}" "$RESET" >&2
    exit 1
fi
FINAL_STATUS="$SMOKE_LAST_STATUS"
if [[ "$FINAL_STATUS" != "completed" && "$FINAL_STATUS" != "SUCCEEDED" ]]; then
    printf '%sFAIL: terminal status=%s (expected completed|SUCCEEDED)%s\n' "$RED" "$FINAL_STATUS" "$RESET" >&2
    exit 1
fi
printf '  final status: %s\n' "$FINAL_STATUS"

# Canonical image-binding assertion: every scene must have a non-empty
# bindings.image.url. Mirrors the .py assertion.
smoke_log_section "Scene image bindings"
SCENE_TOTAL=$(jq -r '
    [
        (
          .result.data.data.output.specscene.scenes
          // .result.data.output.specscene.scenes
          // .result.output.specscene.scenes
          // .result.items[0].result.output.specscene.scenes
          // .result.items[0].output.specscene.scenes
          // []
        )
    ] | length
' "$SMOKE_LAST_BODY")
if [[ "$SCENE_TOTAL" -le 0 ]]; then
    printf '%sFAIL: specscene.scenes is empty (LLM emitted no scenes)%s\n' "$RED" "$RESET" >&2
    smoke_echo_safe "$(head -c 800 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi

MISSING_IMAGES=$(jq -r '
    [
        (
          .result.data.data.output.specscene.scenes
          // .result.data.output.specscene.scenes
          // .result.output.specscene.scenes
          // .result.items[0].result.output.specscene.scenes
          // .result.items[0].output.specscene.scenes
          // []
        )[]
        | select((.bindings.image.url // "") == "")
    ] | length
' "$SMOKE_LAST_BODY")
if [[ "$MISSING_IMAGES" -ne 0 ]]; then
    printf '%sFAIL: %s/%s scenes are missing bindings.image.url%s\n' "$RED" "$MISSING_IMAGES" "$SCENE_TOTAL" "$RESET" >&2
    exit 1
fi
printf '  scenes with image url: %s/%s\n' "$SCENE_TOTAL" "$SCENE_TOTAL"

printf '\n%sOK: script-with-images canary smoke PASSED on %s (correlation=%s)%s\n' \
    "$GREEN" "$(date -Iseconds)" "$CORRELATION_ID" "$RESET"
exit 0
