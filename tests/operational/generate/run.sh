#!/usr/bin/env bash
# Common data-driven runner for /api/script/generate operational smoke tests.
# Usage: run.sh [scenario.json] [--dry]
set -euo pipefail

RUN_DIR=$(cd "$(dirname "$0")" && pwd)
GENERATE_REPO_ROOT=$(cd "$RUN_DIR/../../.." && pwd)
export GENERATE_REPO_ROOT
# Preserve the 900-second asynchronous budget used by both legacy runners.
SMOKE_TIMEOUT_SECONDS="${SMOKE_TIMEOUT_SECONDS:-900}"
SMOKE_POLL_TIMEOUT_SECONDS="${SMOKE_POLL_TIMEOUT_SECONDS:-900}"
export SMOKE_TIMEOUT_SECONDS SMOKE_POLL_TIMEOUT_SECONDS
SCENARIO_ARG="${1:-basic.json}"
if [[ "$SCENARIO_ARG" == "--dry" ]]; then SCENARIO_ARG="basic.json"; DRY_ARG=1; else DRY_ARG=0; fi
shift || true
if [[ "${1:-}" == "--dry" ]]; then DRY_ARG=1; shift; fi
if [[ "$SCENARIO_ARG" != /* ]]; then SCENARIO_FILE="$RUN_DIR/scenarios/$SCENARIO_ARG"; else SCENARIO_FILE="$SCENARIO_ARG"; fi
[[ -f "$SCENARIO_FILE" ]] || { echo "setup error: scenario not found: $SCENARIO_FILE" >&2; exit 2; }
if (( DRY_ARG )); then export SMOKE_DRY_RUN=1; fi
# common.sh parses smoke flags; hide runner-only positional arguments while sourcing it.
RUNNER_ARGS=("$@"); set --
# shellcheck disable=SC1091
source "$RUN_DIR/../lib/common.sh"
set -- "${RUNNER_ARGS[@]}"
# shellcheck disable=SC1091
source "$RUN_DIR/lib/dispatch.sh"
# shellcheck disable=SC1091
source "$RUN_DIR/lib/result.sh"
# shellcheck disable=SC1091
source "$RUN_DIR/lib/assert_script.sh"
# shellcheck disable=SC1091
source "$RUN_DIR/lib/assert_idempotency.sh"
# shellcheck disable=SC1091
source "$RUN_DIR/lib/assert_vidrush.sh"
# shellcheck disable=SC1091
source "$RUN_DIR/lib/assert_drive.sh"

SCENARIO=$(cat "$SCENARIO_FILE")
NAME=$(jq -r '.name' <<<"$SCENARIO")
CASE_PREFIX="$(jq -r '.case_prefix // "generate"' <<<"$SCENARIO")-$(smoke_gen_uuid)"
IDEMPOTENCY_KEY="$CASE_PREFIX-key"
PAYLOAD=$(jq --arg marker "$CASE_PREFIX" '.payload | tojson | gsub("__CASE_MARKER__"; $marker) | fromjson' <<<"$SCENARIO")

if [[ "$DRY_RUN" == "1" ]]; then
    printf 'DRY RUN — scenario: %s\n' "$NAME"
    printf 'POST http://%s/api/script/generate\n' "$SMOKE_API_BASE"
    printf 'Idempotency-Key: %s\nPayload:\n' "$IDEMPOTENCY_KEY"
    jq . <<<"$PAYLOAD"
    exit 0
fi

ASSERTIONS=$(jq -c '.assertions // {}' <<<"$SCENARIO")
if jq -e '.drive == true' <<<"$ASSERTIONS" >/dev/null; then
    smoke_require sqlite3
fi
smoke_log_section "Dispatch: $NAME"
generate_dispatch "$PAYLOAD" "$IDEMPOTENCY_KEY"
printf 'job_id: %s\n' "$GENERATE_JOB_ID"

smoke_log_section "Poll /api/jobs/$GENERATE_JOB_ID/full"
generate_poll_and_fetch
printf 'final status: %s\n' "$SMOKE_LAST_STATUS"

generate_extract_result
generate_assert_script "$ASSERTIONS"
if jq -e '.vidrush == true' <<<"$ASSERTIONS" >/dev/null; then generate_assert_vidrush "$GENERATE_RESULT"; fi
if jq -e '.drive == true' <<<"$ASSERTIONS" >/dev/null; then generate_assert_drive "$GENERATE_RESULT"; fi
if jq -e '.idempotency == true' <<<"$ASSERTIONS" >/dev/null; then generate_assert_idempotency "$PAYLOAD" "$IDEMPOTENCY_KEY" "$ASSERTIONS"; fi
printf '%sOK: scenario %s passed%s\n' "$GREEN" "$NAME" "$RESET"
