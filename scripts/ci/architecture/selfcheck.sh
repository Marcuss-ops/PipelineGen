#!/usr/bin/env bash
# selfcheck.sh — Regex-pattern validator for the ci/architecture suite.
#
# Runs every check's ripgrep regex against its corresponding fixture
# in tests/fixtures/zero_legacy/ and verifies the regex catches the
# forbidden pattern. Exits 0 only if every check's pattern still
# matches its fixture; exits 1 if any fixture is missing or any
# pattern is broken.
#
# Self-check is a UNIT TEST FOR THE REGEXES — it does NOT scan the
# production tree. The standard production gate (in checks.sh, no
# --self-check flag) is the one that enforces architectural invariants.
#
# The fixtures + check_defs are mirrored 1:1 from the legacy
# scripts/ci-architectural-checks.sh so this file is a behavioural
# drop-in replacement for the --self-check regime.
set -euo pipefail

REPO_ROOT="${REPO_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)}"
FIXTURE_DIR="${REPO_ROOT}/tests/fixtures/zero_legacy"
if [ ! -d "${FIXTURE_DIR}" ]; then
    echo "FAIL: fixture dir ${FIXTURE_DIR} does not exist (run from repo root)" >&2
    exit 1
fi

# Format: name|pattern|fixture_file. Pattern uses ripgrep regex syntax.
# The fixture MUST contain the forbidden pattern; rg must match.
# Mirrors the legacy "Check 8 / 9 / 10 / 11 / 12 / 13 / 14 / 15"
# self-check registrations.
check_defs=(
    "Check 8 (SetOutboxHandler/SetMediasearchHandler after construction)|\\.SetOutboxHandler\\(|check_08_setter.go"
    "Check 8 (SetOutboxHandler/SetMediasearchHandler after construction)|\\.SetMediasearchHandler\\(|check_08_setter.go"
    "Check 9 (nil-dispatcher silent fallback)|dispatcher\\s*==\\s*nil\\s*\\{[^}]*return\\s+nil\\b|check_09_nil_dispatcher.go"
    "Check 10 (asset-repo Upsert outside allowlist)|\\.Upsert\\(ctx,|check_10_upsert.go"
    "Check 11 (event_key constructed with random UUID, inline)|eventKey[^\\n]*uuid\\.NewString|check_11_uuid_event_key.go"
    "Check 11 (event_key constructed with random UUID, multiline reverse)|eventID[^\\n]*=\\s*uuid\\.NewString[^\\n]*\\n(?:[^\\n]*\\n){0,3}[^\\n]*eventKey[^\\n]*=[^\\n]*\\beventID\\b|check_11_uuid_event_key.go"
    "Check 11 (event_key constructed with random UUID, multiline forward)|eventKey[^\\n]*\\n(?:[^\\n]*\\n){0,3}[^\\n]*eventID[^\\n]*=\\s*uuid\\.NewString|check_11_uuid_event_key.go"
    "Check 12 (payload_mapper legacy lifecycle_state fallback)|\"lifecycle_state\":\\s*\\w+\\.Status|check_12_payload_mapper_status.go"
    "Check 13 (ListAssetsForReconcile placeholder)|wired as build-time placeholder|check_13_listassets_placeholder.go"
    "Check 14 (BuildPayload legacy status key)|\"status\":\\s*\\w+\\.|check_14_buildpayload_status_key.go"
    "Check 15 (qdrant.NewClient construction)|qdrant\\.NewClient\\(&qdrant\\.Config\\{|check_15_qdrant_config_apikey.go"
)

failed=0
seen_names=""
for def in "${check_defs[@]}"; do
    IFS='|' read -r name pattern fixture <<< "${def}"
    fixture_path="${FIXTURE_DIR}/${fixture}"
    if [ ! -f "${fixture_path}" ]; then
        echo "FAIL: ${name} — fixture ${fixture} missing" >&2
        failed=1
        continue
    fi
    if rg -qU -- "${pattern}" "${fixture_path}" 2>/dev/null; then
        # De-duplicate per check (Check 8 has 2 patterns for 2 methods).
        if [[ "${seen_names}" != *"${name}"* ]]; then
            echo "PASS: ${name} — caught fixture ${fixture}"
            seen_names="${seen_names}|${name}|"
        fi
    else
        echo "FAIL: ${name} — pattern did NOT catch fixture ${fixture} (regex is broken)" >&2
        failed=1
    fi
done

if [ "${failed}" -gt 0 ]; then
    echo "" >&2
    echo "Self-check FAILED: at least one regex does not catch its fixture." >&2
    echo "Fix the regex OR update the fixture so the forbidden pattern is present." >&2
    exit 1
fi

unique_names=()
for def in "${check_defs[@]}"; do
    name="${def%%|*}"
    if [[ " ${unique_names[*]} " != *" ${name} "* ]]; then
        unique_names+=("${name}")
    fi
done
unique_count=${#unique_names[@]}
pattern_count=${#check_defs[@]}
# Count fixture files dynamically (ls instead of hardcoded "7") so
# the summary stays accurate when new fixtures are added.
fixture_count=$(ls -1 "${FIXTURE_DIR}"/*.go 2>/dev/null | wc -l)
echo "All self-checks passed (${pattern_count} patterns / ${unique_count} unique checks / ${fixture_count} fixtures)."
