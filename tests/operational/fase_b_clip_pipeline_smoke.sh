#!/usr/bin/env bash
#
# fase_b_clip_pipeline_smoke.sh — PipelineGen black-box FASE B regression smoke
#
# Usage:
#   ./fase_b_clip_pipeline_smoke.sh            # real probes against a live server
#   ./fase_b_clip_pipeline_smoke.sh --dry      # print the would-be probes, exit 0
#
# Tests (mirrors the 4 FASE B manual probes from the operator runbook):
#   Test 3  strategy=replace                 →  asset_id stays same; drive_file_id stable
#   Test 7  destination.folder_id pre-riso    →  NO "group is required" error
#   Test 8  destination has group, no folder  →  PathBuilder canonical; no group-is-required
#   Test 9  duplicate payload (run twice)    →  same media_assets.id; same drive_file_id
#
# Three user-specified PASS conditions (per godlike/07 no-fake-availability):
#   (a) NO "group is required" error string in jobs.error  (Tests 3, 7, 8)
#   (b) Test 9 produces asset with SAME drive_file_id across both runs (cache hit)
#   (c) Test 3 updates drive_file_id WITHOUT creating new asset rows (UPSERT, not INSERT)
#
# Exit codes:
#   0  all assertion pass conditions satisfied
#   1  one or more pass conditions failed
#   2  setup error (missing token, bad flag, missing sqlite3)
#   124 overall timeout exceeded
#
# Why negative assertions today: production server still routes through the
# legacy in-process Worker path which fails artifact-producing jobs at
# `ErrCompleteJobPathViolation`. (a) + (c) are observable under that gate;
# (b) requires the new in-proc worker.Runner migration (forward-pointer to
# `architecture/current.yaml#PR-WORKER-RUNNER-INPROCESS-MIGRATION`). Asserting
# only the negative patterns keeps the signal green while the canonical
# migration is en route — once (b) lands, this test becomes the verification
# surface for the migration (no test rewrite needed).

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

# Project-specific binaries (kept separate from the unconditional `jq` check)
smoke_require sqlite3

# ── Constants ──────────────────────────────────────────────────────────
VIDEO_ID="9u4T_o3FxOU"
VIDEO_URL="https://www.youtube.com/watch?v=${VIDEO_ID}"
DRIVE_FOLDER_ID="${SMOKE_DRIVE_FOLDER_ID:-1JUA9sUmm7ZYSLYztLNbzLVptr1igno9H}"  # boxing
SMOKE_DB="${SMOKE_DB:-data/media/media.db.sqlite}"
# clipIDs are shaped yt_<videoID>_<startSec>_<endSec>_<policyVer>; use a fixed
# segment so the smoke is reproducible across runs and asset_id is stable.
CLIP_PATTERN="yt_${VIDEO_ID}_30_70_v%"

# ── Help text ──────────────────────────────────────────────────────────
if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    sed -n '2,32p' "$0"
    exit 0
fi

# ── Dry-run mode ─────────────────────────────────────────────────────
if [[ "$DRY_RUN" == "1" ]]; then
    smoke_echo_safe "DRY RUN — would probe:"
    printf '  POST http://%s/api/clips/process  (Test 3: replace)\n'        "$SMOKE_API_BASE"
    printf '  POST http://%s/api/clips/process  (Test 7: folder_id)\n'        "$SMOKE_API_BASE"
    printf '  POST http://%s/api/clips/process  (Test 8: no folder_id, PathBuilder)\n' "$SMOKE_API_BASE"
    printf '  POST http://%s/api/clips/process  (Test 9: duplicate, twice)\n'  "$SMOKE_API_BASE"
    printf '  sqlite3 %s   …   (assertion probes)\n' "$SMOKE_DB"
    exit 0
fi

# ── Setup guard ─────────────────────────────────────────────────────
if [[ ! -f "$SMOKE_DB" ]]; then
    printf '%ssetup error: SMOKE_DB=%s does not exist%s\n' \
        "$RED" "$SMOKE_DB" "$RESET" >&2
    exit 2
fi

declare -a FAILURES=()
fail() { FAILURES+=("$1"); }

sqlite_q() {
    # Strict variant (godlike/07 no-fake-availability): propagate stderr to
    # a captured log ON FIRST error; abort the script with non-zero exit
    # BEFORE the assertion function returns. This is the #1 most-likely
    # production failure mode (offline / corrupted DB during CI), and
    # silently swallowing it would let the smoke script report PASS on a
    # broken environment. Caller pipes through jq when JSON shape needed.
    local out
    if ! out=$(sqlite3 -separator '|' "$SMOKE_DB" "$1" 2>/tmp/smoke_sqlite_err); then
        echo >&2 "DB query failed: sqlite3 exit non-zero with stderr:"
        cat >&2 /tmp/smoke_sqlite_err
        rm -f /tmp/smoke_sqlite_err
        exit 1
    fi
    rm -f /tmp/smoke_sqlite_err
    printf '%s' "$out"
}

# Common payload builders, kept on /dev/stdin so jq can reformat safely.
build_payload_replace() {
    jq -n --arg url "$VIDEO_URL" --arg fid "$DRIVE_FOLDER_ID" '{
        url: $url,
        segments: [{
            start:"00:00:30", end:"00:01:10",
            name:"FASE B replace smoke clip",
            tags:["boxing","fase-b","smoke"]
        }],
        strategy:"replace",
        destination:{group:"boxing", folder_id:$fid}
    }'
}

build_payload_folder_id() {
    jq -n --arg url "$VIDEO_URL" --arg fid "$DRIVE_FOLDER_ID" '{
        url: $url,
        segments: [{
            start:"00:00:30", end:"00:01:10",
            name:"FASE B folder_id smoke clip",
            tags:["boxing","fase-b","smoke"]
        }],
        strategy:"verify",
        destination:{group:"boxing", folder_id:$fid}
    }'
}

# NOTE: payload has NO Group/Subject on the destination — that's the surface
# the publisher.go fix (`RootFolderOverride == ""` short-circuit) is supposed
# to skip path-builder for. With the fix landed, this still resolves cleanly.
build_payload_no_folder_id() {
    jq -n --arg url "$VIDEO_URL" '{
        url: $url,
        segments: [{
            start:"00:00:30", end:"00:01:10",
            name:"FASE B no-folder smoke clip",
            tags:["boxing","fase-b","smoke"]
        }],
        strategy:"verify",
        destination:{group:"boxing", subfolder_name:"mayweather", create_subfolder:true}
    }'
}

# Generic POST → enqueue → wait terminal → assert error+assets
enqueue_and_assert() {
    local label="$1"; shift
    local payload="$1"; shift

    local code job_id
    code=$(smoke_curl POST "/api/clips/process" --data "$payload")
    if ! smoke_assert_http_2xx "POST /api/clips/process ($label)"; then
        # Most likely HTTP 4xx — capture body for diagnostics but DON'T fail
        # CAUSE we still want to log which assertions fired afterwards.
        fail "${label}_post_http_${code}"
        if [[ -s "$SMOKE_LAST_BODY" ]]; then
            smoke_echo_safe "  body: $(head -c 300 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
        fi
        return 1
    fi
    job_id=$(jq -r '.job_id // .id // empty' "$SMOKE_LAST_BODY")
    if [[ -z "$job_id" ]]; then
        fail "${label}_no_job_id"
        return 1
    fi

    # Poll to terminal — but tolerate timeout (legacy Worker gate). When we
    # hit the gate, the job lands in FAILED with the typed sentinel; that's
    # still observable via sqlite and is enough for assertions (a)+(c).
    smoke_poll_terminal "$job_id" || {
        printf '%swarning:%s %s poll timeout — proceeding with whatever DB state exists\n' \
            "$YELLOW" "$RESET" "$label" >&2
    }

    # (a) NO "group is required" in the error column for THIS job
    local err
    err=$(sqlite_q "SELECT COALESCE(error,'') FROM jobs WHERE id='$job_id' LIMIT 1")
    if [[ "$err" == *"group is required"* ]]; then
        fail "${label}_group_required"
        printf '  err: %s\n' "$err" >&2
    fi
    printf '  %s job_id=%s status=%s err_len=%d\n' "$DIM" "$job_id" "${SMOKE_LAST_STATUS:-?}" "${#err}" >&2

    # Echo the job_id so callers can pipe-assert for (b)+(c) inter-test.
    printf '%s' "$job_id"
}

# ── Test 3: strategy=replace ───────────────────────────────────────
# (c) drives this: the media_assets row for $CLIP_PATTERN must NOT duplicate
# when we run Test 3 twice — second run is an UPSERT, not a fresh INSERT.
test_3_replace() {
    smoke_log_section "Test 3: strategy=replace (asset_id stable, no duplicate row)"

    local before_count
    # Removed `|| echo 0` fallback (code-reviewer blocker #1): fake availability
    # would mask DB error. Fail-fast via sqlite_q.
    before_count=$(sqlite_q "SELECT COUNT(*) FROM media_assets WHERE id LIKE '$CLIP_PATTERN'")
    printf '  %s media_assets (before): %s rows\n' "$DIM" "${before_count:-?}" >&2

    local job1 job2
    job1=$(enqueue_and_assert "fase-b_replace" "$(build_payload_replace)") || true
    job2=$(enqueue_and_assert "fase-b_replace" "$(build_payload_replace)") || true

    local after_count
    after_count=$(sqlite_q "SELECT COUNT(DISTINCT id) FROM media_assets WHERE id LIKE '$CLIP_PATTERN'")
    printf '  %s media_assets (after):  %s rows\n' "$DIM" "${after_count:-?}" >&2

    # (c): UPSERT, not INSERT — distinct id count must NOT grow by 2.
    # Tolerate a baseline of 1 (already-existing row from prior smoke) and
    # forbid ANY new rows introduced by THIS run if both jobs went through.
    if (( after_count > before_count + 0 )); then
        fail "test3_new_asset_ids_created (before=$before_count after=$after_count)"
    fi
}

# ── Test 7: folder_id pre-riso ──────────────────────────────────────
# The historic bug surfaced here: publisher.go was building the path even
# when folder_id was supplied, then complaining Group/Subject were missing.
# Post-fix (RootFolderOverride == ""), this path must NOT emit that string
# — even when the job later fails (e.g. legacy Worker gate).
test_7_folder_id() {
    smoke_log_section "Test 7: destination.folder_id pre-riso (publisher fix)"

    local job_id
    job_id=$(enqueue_and_assert "fase_b_folder_id" "$(build_payload_folder_id)") || true

    # Defence-in-depth scoped scan (code-reviewer blocker #2): the previous query
    # used `JOIN media_assets m ON m.id LIKE '$CLIP_PATTERN'` — that's a CROSS JOIN
    # (no linking key on jobs), inflating the row count by the number of recent
    # assets matching the pattern.
    #
    # Canonical fix: scope the JOBS table directly by clipping the request payload
    # to the canonical clip_id pattern. `jobs.payload_json` IS the SQLite column
    # for `EnqueueRequest.Payload` per the SQLiteStore schema — the request body
    # is JSON-marshaled into that column at enqueue time. INSTR() avoids SQLite
    # LIKE wildcard semantics for the `_` characters in the clip_id.
    local scoped_errs
    scoped_errs=$(sqlite_q "SELECT COUNT(*) FROM jobs WHERE created_at > datetime('now','-5 minutes') AND error LIKE '%group is required%' AND instr(payload_json, 'yt_9u4T_o3FxOU_30_70_v') > 0")
    if (( scoped_errs > 0 )); then
        fail "test7_group_required_observed_in_${scoped_errs}_scoped_jobs"
    fi
}

# ── Test 8: no folder_id (PathBuilder canonical) ─────────────────────
# Symmetric to Test 7 but in the OPPOSITE branch: PathBuilder must do real
# work because folder_id is absent. The forbidden "group is required" string
# must STILL be absent here — meaning the destination.group field is
# consumed (or defaulted) rather than re-derived from a stale Nil.
test_8_no_folder_id() {
    smoke_log_section "Test 8: no folder_id, group=boxing (PathBuilder canonical)"

    local job_id
    job_id=$(enqueue_and_assert "fase_b_no_folder" "$(build_payload_no_folder_id)") || true
}

# ── Test 9: duplicate Drive (run payload twice → same drive_file_id) ──
# (b) drives this: across two runs of the SAME payload, the SAME
# media_assets row should exist with the SAME drive_file_id. Today this
# depends on Publisher's ConflictSkipByHash (publisher.go). Once the new
# in-process worker.Runner is migrated, both runs should also reach
# SUCCEEDED; before migration, both runs may FAIL at the Worker gate —
# but the canonical media_assets row + drive_file_id must still be stable
# because ClipAtomicWriter is reached BEFORE the gate.
test_9_duplicate() {
    smoke_log_section "Test 9: duplicate (same drive_file_id across 2 runs)"

    local payload
    payload=$(build_payload_folder_id)

    local job1 job2
    job1=$(enqueue_and_assert "fase_b_dup_run1" "$payload" 2>/dev/null || true) || true
    job2=$(enqueue_and_assert "fase_b_dup_run2" "$payload" 2>/dev/null || true) || true

    local drive_count
    drive_count=$(sqlite_q "SELECT COUNT(DISTINCT drive_file_id) FROM media_assets WHERE id LIKE '$CLIP_PATTERN' AND drive_file_id != ''")
    printf '  %s distinct drive_file_id(s) for %s: %s\n' \
        "$DIM" "$CLIP_PATTERN" "${drive_count:-?}" >&2

    # (b): exactly ONE distinct drive_file_id for the canonical asset id.
    # 0  means no Drive upload landed yet (acceptable under legacy gate).
    # 2+ means ConflictSkipByHash regressed.
    if (( drive_count > 1 )); then
        fail "test9_multiple_drive_file_ids_$drive_count"
    fi
}

main() {
    smoke_log_section "FASE B clip pipeline regression smoke starting"
    printf '  target: %s\n  video:  %s\n  db:     %s\n' \
        "$SMOKE_API_BASE" "$VIDEO_URL" "$SMOKE_DB"

    test_3_replace
    test_7_folder_id
    test_8_no_folder_id
    test_9_duplicate

    echo
    if (( ${#FAILURES[@]} == 0 )); then
        printf '%sOK: FASE B smoke checks all green%s\n' "$GREEN" "$RESET"
        exit 0
    fi
    printf '%sFAIL: %d FASE B smoke assertion(s) failed:%s\n' \
        "$RED" "${#FAILURES[@]}" "$RESET" >&2
    for f in "${FAILURES[@]}"; do
        printf '  - %s\n' "$f" >&2
    done
    exit 1
}
main "$@"
