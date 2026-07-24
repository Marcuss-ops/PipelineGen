#!/usr/bin/env bash
# tests/operational/lib/artlist_runtime_test.sh — regression net for
# artlist_runtime.sh (commit aa5b78c93 → 5f21bfa15).
#
# Asserts (per the runtime.sh refactor code-reviewer's must-address item):
#   1. The 9 canonical DoD runtime vars default to their documented values
#      when all env overrides are unset.
#   2. PASS / WARN / FAIL counters initialise to 0 on source.
#   3. log_pass bumps PASS to 1 (counter propagation works through the
#      sourced scope, not a shadowed local).
#
# Contract:
#   - exit 0  : every assertion passed
#   - exit 1  : one or more assertions failed (caller may collect PASS_ASSERT
#               for human-readable summary)
#   - exit 2  : source-time setup error (lib file missing, jq not present)
#
# Run from anywhere: bash tests/operational/lib/artlist_runtime_test.sh
# Wired into verify-foundation in a followup commit to make the runtime
# lib contract automatically auditable on every pre-push gate.
#
# Maintenance note: if you add a new env-overridable var to
# artlist_runtime.sh that uses a layered fall-through (e.g.
# `X:-${Y:-default}`), add BOTH names (X and Y) to the unset list
# below; otherwise an operator-supplied Y in the env will mask the
# canonical default the test is asserting.

set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"

# ── Isolated env (no operator-supplied env vars feeding the runtime defaults)
# artlist_runtime.sh exports HOST/BASE_URL/DB_PATH/etc; unset any operator-
# supplied overrides BEFORE sourcing so the assertion can read the canonical
# DoD defaults the lib publishes. PASS/WARN/FAIL / WORK_DIR are also
# cleared so we observe the lib-assigned value, not a pre-existing one.
unset HOST PIPELINE_PORT BASE_URL DB_PATH SCRAPER_URL QDRANT_URL \
      QDRANT_COLLECTION ARTLIST_ROOT_FOLDER ARTLIST_TERM \
      VELOX_HOST VELOX_PORT VELOX_DATA_DIR \
      VELOX_ARTLIST_SCRAPER_SERVER_URL \
      VELOX_DRIVE_ARTLIST_ROOT ROOT_FOLDER_ID \
      PASS WARN FAIL WORK_DIR
# common.sh is intentionally NOT sourced here — this test exercises
# artlist_runtime.sh in isolation (the same way a focused regression net
# should), confirming it can stand on its own. common.sh's smoke_require jq
# is called instead via the test's own smoke_require call below.

# ── Pre-flight: jq presence (mirror of common.sh's pre-flight contract)
command -v jq >/dev/null 2>&1 || {
    printf '%ssetup error: jq missing (artlist_runtime.sh + downstream libs use jq for JSON parsing)%s\n' \
        "" "" >&2
    exit 2
}

# ── Source the lib under test
# shellcheck disable=SC1091
source "$DIR/artlist_runtime.sh"

# ── Per-assertion helper (does not mutate the lib's PASS counter)
PASS_ASSERT=0
fail=0
assert_eq() {
    local label="$1" expected="$2" actual="$3"
    if [[ "$expected" == "$actual" ]]; then
        printf '  ✅  %-30s = %s\n' "$label" "$actual"
        PASS_ASSERT=$((PASS_ASSERT + 1))
    else
        printf '  ❌  %-30s — expected [%s], got [%s]\n' \
            "$label" "$expected" "$actual"
        fail=$((fail + 1))
    fi
}

# ── Assertion block 1: 9 runtime vars default to canonical DoD values
echo
echo '🧪 1. Runtime vars default to canonical DoD values'
assert_eq "HOST"                   "127.0.0.1"                            "${HOST}"
assert_eq "PIPELINE_PORT"          "8000"                                  "${PIPELINE_PORT}"
assert_eq "BASE_URL"               "http://127.0.0.1:8000"                 "${BASE_URL}"
assert_eq "DB_PATH"                "./data/media/media.db.sqlite"           "${DB_PATH}"
assert_eq "SCRAPER_URL"            "http://127.0.0.1:9123"                 "${SCRAPER_URL}"
assert_eq "QDRANT_URL"             "http://127.0.0.1:6333"                 "${QDRANT_URL}"
assert_eq "QDRANT_COLLECTION"      "media_assets_current"                  "${QDRANT_COLLECTION}"
assert_eq "ARTLIST_ROOT_FOLDER"    ""                                      "${ARTLIST_ROOT_FOLDER}"
assert_eq "ARTLIST_TERM"           "business team working in modern office" "${ARTLIST_TERM}"

# ── Assertion block 2: counters init to 0
echo
echo '🧪 2. PASS / WARN / FAIL counters init to 0'
assert_eq "PASS init"  "0" "${PASS}"
assert_eq "WARN init"  "0" "${WARN}"
assert_eq "FAIL init"  "0" "${FAIL}"

# ── Assertion block 3: log_pass bumps PASS to 1 (propagates via sourced scope)
echo
echo '🧪 3. log_pass bumps PASS to 1'
log_pass "synthetic test event"
assert_eq "PASS after log_pass" "1" "${PASS}"
assert_eq "WARN untouched"      "0" "${WARN}"
assert_eq "FAIL untouched"      "0" "${FAIL}"

# ── Footer summary
echo
if (( fail == 0 )); then
    printf '✅ artlist_runtime_test.sh: %d/%d assertions passed\n' \
        "${PASS_ASSERT}" "${PASS_ASSERT}"
    exit 0
else
    printf '❌ artlist_runtime_test.sh: %d/%d assertions failed\n' \
        "$fail" "$((PASS_ASSERT + fail))"
    exit 1
fi
