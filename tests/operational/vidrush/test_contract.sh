#!/usr/bin/env bash
# tests/operational/vidrush/test_contract.sh — hermetic contract tests.
#
# Covers the source-only VidRush phase helpers without contacting a live
# server:
#   - report_json schema, identity fields, timing and extra-field merging;
#   - runner exit codes for successful dry-runs, setup errors and BLOCKED phases;
#   - temporary-directory cleanup for concurrency strict-mode failure and
#     idempotency's failed assertion path.
#
# Contract:
#   0  all assertions passed
#   1  one or more contract assertions failed
#   2  test setup error

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$DIR/../../.." && pwd)
RUNNER="$DIR/run_scenario.sh"
REPORT_LIB="$DIR/lib/report.sh"
CONCURRENCY_LIB="$DIR/lib/concurrency.sh"
IDEMPOTENCY_LIB="$DIR/lib/idempotency.sh"

for required in bash jq md5sum mktemp find; do
    command -v "$required" >/dev/null 2>&1 || {
        printf 'setup error: missing required binary: %s\n' "$required" >&2
        exit 2
    }
done

[[ -x "$RUNNER" || -f "$RUNNER" ]] || {
    printf 'setup error: runner not found: %s\n' "$RUNNER" >&2
    exit 2
}
[[ -f "$REPORT_LIB" && -f "$CONCURRENCY_LIB" && -f "$IDEMPOTENCY_LIB" ]] || {
    printf 'setup error: VidRush phase library missing\n' >&2
    exit 2
}

TEST_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/vidrush-contract.XXXXXX")
trap 'rm -rf -- "$TEST_ROOT"' EXIT

failures=0
assert_eq() {
    local label="$1" expected="$2" actual="$3"
    if [[ "$expected" == "$actual" ]]; then
        printf '  PASS %-46s %s\n' "$label" "$actual"
    else
        printf '  FAIL %-46s expected=%s actual=%s\n' "$label" "$expected" "$actual" >&2
        failures=$((failures + 1))
    fi
}

assert_json() {
    local label="$1" expression="$2" json="$3"
    if jq -e "$expression" <<<"$json" >/dev/null 2>&1; then
        printf '  PASS %-46s\n' "$label"
    else
        printf '  FAIL %-46s\n' "$label" >&2
        printf '       report=%s\n' "$json" >&2
        failures=$((failures + 1))
    fi
}

capture_rc() {
    local output_var="$1" rc_var="$2"
    shift 2
    local captured_output captured_rc
    if captured_output=$("$@" 2>&1); then
        captured_rc=0
    else
        captured_rc=$?
    fi
    printf -v "$output_var" '%s' "$captured_output"
    printf -v "$rc_var" '%s' "$captured_rc"
}

report_from_output() {
    sed -n '/^{/,$p' <<<"$1"
}

make_contract_manifest() {
    cat >"$TEST_ROOT/contract-manifest.json" <<'EOF'
{
  "scenario_id": "contract-manifest",
  "payload": {
    "items": [
      {
        "source": {
          "source_text": "Hermetic VidRush contract fixture"
        }
      }
    ]
  }
}
EOF
}

printf '\n=== VidRush contract: report_json ===\n'
make_contract_manifest
if (
    SCENARIO_ID=contract-report
    GIT_SHA=contract-sha
    TIMESTAMP_START=$(date +%s%3N 2>/dev/null || date +%s000)
    SCENARIO_FILE="$TEST_ROOT/contract-manifest.json"
    export SCENARIO_ID GIT_SHA TIMESTAMP_START SCENARIO_FILE
    # shellcheck disable=SC1091
    source "$REPORT_LIB"
    report=$(report_json "SUCCEEDED" "job-contract" "warm" \
        '{"counts":{"segments":2,"entities":1,"provider_requests":3,"bindings":2,"unresolved":0},"artifacts":{"sqlite_verified":true,"qdrant_verified":true,"drive_verified":true,"render_verified":false}}')
    jq -e '
        .scenario_id == "contract-report"
        and .git_sha == "contract-sha"
        and .job_id == "job-contract"
        and .status == "SUCCEEDED"
        and .cache_mode == "warm"
        and (.input_hash | test("^[0-9a-f]{32}$"))
        and (.timing_ms.total | type == "number")
        and .counts.segments == 2
        and .counts.entities == 1
        and .counts.provider_requests == 3
        and .artifacts.sqlite_verified == true
        and .artifacts.render_verified == false
    ' <<<"$report" >/dev/null
); then
    printf '  PASS report_json schema and field merge\n'
else
    printf '  FAIL report_json schema and field merge\n' >&2
    failures=$((failures + 1))
fi

printf '\n=== VidRush contract: runner exit codes and reports ===\n'
manifest_count=0
while IFS= read -r manifest; do
    manifest_count=$((manifest_count + 1))
    expected_id=$(jq -r '.scenario_id' "$manifest")
    capture_rc output rc bash "$RUNNER" --dry "$manifest"
    assert_eq "dry-run rc $expected_id" "0" "$rc"
    report=$(report_from_output "$output")
    assert_json "dry-run status $expected_id" \
        '.status == "DRY_RUN"' \
        "$report"
done < <(find "$DIR/scenarios" -maxdepth 1 -type f -name '*.json' -print | sort)
assert_eq "all scenario manifests covered" "15" "$manifest_count"

capture_rc output rc bash "$RUNNER" "$TEST_ROOT/missing-scenario.json"
assert_eq "missing scenario is setup error" "2" "$rc"

# These phases are deliberately blocked before any HTTP call. Generate the
# dummy token at runtime so the test contains no credential literal.
dummy_token=$(printf 'x%.0s' {1..64})
for scenario in 09_provider_failure.json 12_render_handoff.json; do
    capture_rc output rc env SMOKE_TOKEN="$dummy_token" bash "$RUNNER" "$DIR/scenarios/$scenario"
    expected_id=$(jq -r '.scenario_id' "$DIR/scenarios/$scenario")
    assert_eq "blocked phase rc $expected_id" "1" "$rc"
    report=$(report_from_output "$output")
    assert_json "blocked report status $expected_id" \
        '.status == "BLOCKED" and (.required_steps | type == "array")' \
        "$report"
done

printf '\n=== VidRush contract: concurrency cleanup ===\n'
concurrency_tmp="$TEST_ROOT/concurrency-tmp"
mkdir -p "$concurrency_tmp"
if (
    export TMPDIR="$concurrency_tmp"
    SCENARIO_ID=contract-concurrency
    GIT_SHA=contract-sha
    TIMESTAMP_START=$(date +%s%3N 2>/dev/null || date +%s000)
    SCENARIO_FILE="$TEST_ROOT/contract-manifest.json"
    SMOKE_API_BASE=127.0.0.1:1
    SMOKE_TOKEN="$dummy_token"
    SMOKE_HTTP_TIMEOUT_SECONDS=1
    SMOKE_POLL_TIMEOUT_SECONDS=1
    RED= GREEN= YELLOW= CYAN= DIM= RESET=""
    export SCENARIO_ID GIT_SHA TIMESTAMP_START SCENARIO_FILE SMOKE_API_BASE \
        SMOKE_TOKEN SMOKE_HTTP_TIMEOUT_SECONDS SMOKE_POLL_TIMEOUT_SECONDS \
        RED GREEN YELLOW CYAN DIM RESET
    # shellcheck disable=SC1091
    source "$REPORT_LIB"
    # shellcheck disable=SC1091
    source "$CONCURRENCY_LIB"
    unset METRICS_AUTH_TOKEN
    if run_concurrency >"$TEST_ROOT/concurrency.out" 2>"$TEST_ROOT/concurrency.err"; then
        exit 1
    fi
); then
    printf '  PASS concurrency failure exit path\n'
else
    printf '  FAIL concurrency failure exit path\n' >&2
    failures=$((failures + 1))
fi
if [[ -z "$(find "$concurrency_tmp" -mindepth 1 -print -quit)" ]]; then
    printf '  PASS concurrency temp directory cleanup\n'
else
    printf '  FAIL concurrency temp directory cleanup\n' >&2
    failures=$((failures + 1))
fi

printf '\n=== VidRush contract: idempotency cleanup ===\n'
idempotency_tmp="$TEST_ROOT/idempotency-tmp"
mkdir -p "$idempotency_tmp"
if (
    export TMPDIR="$idempotency_tmp"
    SCENARIO_ID=contract-idempotency
    GIT_SHA=contract-sha
    TIMESTAMP_START=$(date +%s%3N 2>/dev/null || date +%s000)
    SCENARIO_FILE="$DIR/scenarios/10_idempotency.json"
    SMOKE_API_BASE=127.0.0.1:1
    SMOKE_TOKEN="$dummy_token"
    SMOKE_HTTP_TIMEOUT_SECONDS=1
    SMOKE_POLL_TIMEOUT_SECONDS=1
    RED= GREEN= YELLOW= CYAN= DIM= RESET=""
    export SCENARIO_ID GIT_SHA TIMESTAMP_START SCENARIO_FILE SMOKE_API_BASE \
        SMOKE_TOKEN SMOKE_HTTP_TIMEOUT_SECONDS SMOKE_POLL_TIMEOUT_SECONDS \
        RED GREEN YELLOW CYAN DIM RESET
    # shellcheck disable=SC1091
    source "$REPORT_LIB"
    # shellcheck disable=SC1091
    source "$IDEMPOTENCY_LIB"
    smoke_gen_uuid() { printf 'contract-id\n'; }
    idempotency_post() {
        local _payload="$1" _key="$2" label="$3" dir="$4"
        printf '500\n' >"$dir/${label}.code"
        printf '{}\n' >"$dir/${label}.body"
        : >"$dir/${label}.headers"
    }
    if run_idempotency >"$TEST_ROOT/idempotency.out" 2>"$TEST_ROOT/idempotency.err"; then
        exit 1
    fi
); then
    printf '  PASS idempotency failed-assertion exit path\n'
else
    printf '  FAIL idempotency failed-assertion exit path\n' >&2
    failures=$((failures + 1))
fi
if [[ -z "$(find "$idempotency_tmp" -mindepth 1 -print -quit)" ]]; then
    printf '  PASS idempotency temp directory cleanup\n'
else
    printf '  FAIL idempotency temp directory cleanup\n' >&2
    failures=$((failures + 1))
fi

printf '\n'
if (( failures == 0 )); then
    printf '✅ VidRush contract tests passed\n'
    exit 0
fi
printf '❌ VidRush contract tests failed: %d assertion(s)\n' "$failures" >&2
exit 1
