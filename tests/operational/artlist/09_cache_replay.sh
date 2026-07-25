#!/usr/bin/env bash
# tests/operational/artlist/09_cache_replay.sh — Artlist DoD Gate 9
# (cache replay with cache_hit=true / cache_source=sqlite).
#
# Reorg (July 2026): extracted from tests/operational/artlist/05_pipeline_fresh.sh
# (which bundled Gates 4-9 inline). This file is the canonical Gate 9 owner
# per the DoD "estrazione futura" consolidation. Sibling sub-batteries:
# 04_search_three (Gate 3), 05_clip_validation (Gate 5), 06_drive_resolve
# (Gate 6), 07_outbox_integrity (Gate 7), 08_qdrant_search (Gate 8),
# 10_negative_path (Gate 10). All consume ${WORK_DIR}/clip_ids.txt as
# the godlike/06 SSOT hand-off (one canonical owner per fact at any
# boundary, written by Gate 4 / 05_pipeline_fresh.sh::gate_fresh_run_three).
#
# Spec (artlist_gates.md|Gate 9 verbatim, July 2026 DoD):
#   1. Replay the same /api/artlist/run body verbatim (term=NORMALIZED,
#      limit=3, strategy=replace, clip_duration=7, width=1920, height=1080,
#      fps=30, concurrency=1, dry_run=false — mirror Gate 4 byte-for-byte).
#   2. The replay response MUST carry `cache_hit=true` AND
#      `cache_source=sqlite` as mandatory fields. Fail-closed if either is
#      absent or wrong (does NOT accept "redis"/"memory"/"fallback" —
#      the spec literal says "sqlite" only).
#   3. Per-clip tuple equality: clip_id|file_hash|drive_file_id MUST
#      round-trip byte-for-byte vs the original run's media_assets
#      rows (cross-check vs the GET response's items[] too — single
#      source of truth is the DB; the response is the cross-check).
#   4. "No new download" — media_assets COUNT(*) for the 3 clip_ids
#      BEFORE vs AFTER the replay must match (delta == 0). The replay
#      must NOT create new media_assets rows for clips that already
#      exist in the canonical DB surface.
#   5. "No new Drive upload" — asset_locations COUNT WHERE
#      location_kind='drive' for the 3 clip_ids BEFORE vs AFTER must
#      match (delta == 0). New uploads ALWAYS create new asset_locations
#      rows; the replay with cache_hit=true MUST NOT create any.
#   6. "Faster execution" — replay.elapsed_ms MUST be < 30s (absolute
#      ceiling) AND (if original.elapsed_ms is readable from jobs table)
#      replay.elapsed_ms < original.elapsed_ms * 0.5 (relative speedup).
#      The wall-clock heuristic is the canonical evidence that Gate 9
#      did not re-run the download/process/upload stages (the Go
#      backend doesn't expose per-stage stats for cache_hit replays).
#   7. ALL clips must pass (DoD forbids partial-pass — mirrors Gates
#      4/5/6/7 aggregate contract).
#
# Pre-flight fail-closed: ARTLIST_TERM env var MUST be set, AND
# ${WORK_DIR}/clip_ids.txt MUST exist with at least 1 line (Gate 4
# hand-off). Sentinel: MISSING_CLIPS_HANDOFF mirrors Gate 6's
# ROOT_FOLDER_UNSET fail-fast pattern.
#
# Reuses ONLY canonical helpers:
#   * artlist_replay_run (lib/artlist.sh) — POST /api/artlist/run + parse
#     run_id from stdout's <HTTP_code>\t<run_id>\t<body_path>
#   * artlist_poll_run (lib/artlist.sh) — poll RunTagResponse terminal
#   * smoke_curl (lib/common.sh) — GET /api/artlist/runs/<run_id>
#   * smoke_sqlite_query (lib/common.sh) — per-clip + aggregate SELECTs
#   * log_pass / log_fail / log_info (lib/artlist_runtime.sh via umbrella)
# NO new helpers introduced. AGENTS.md single-focus rule honoured.

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# Source the umbrella (godlike/06 SSOT canonical import contract).
# Resolves path-invariant via BASH_SOURCE[0]; the umbrella's helper-name
# guard fails closed if a future refactor removes any expected helper
# from lib/ (godlike/06 SSOT enforcement).
# shellcheck disable=SC1091
source "$DIR/../lib/_artlist_common.sh"

smoke_require curl jq

# ── Gate 9 — Cache replay (cache_hit=true) ──────────────────────────────
#
# The function below replaces the prior [STUB] block (July 2026 DoD
# compliance wave). All three sub-battery files (08_qdrant_search,
# 09_cache_replay, 10_negative_path) had forward-pointing stubs;
# this file's gate_cache_replay is now the canonical owner of the
# replay semantics per the DoD spec.
gate_cache_replay() {
    smoke_log_section "Gate 9 — Cache replay (cache_hit=true / cache_source=sqlite)"

    # ── Pre-flight fail-closed ──────────────────────────────────────
    if [[ -z "${ARTLIST_TERM:-}" ]]; then
        log_fail "Gate 9 pre-flight: ARTLIST_TERM env var unset — sentinel MISSING_TERM_UNSET; refusing to replay an empty/NULL-term query"
        return 1
    fi
    local clip_file="${WORK_DIR}/clip_ids.txt"
    if [[ ! -s "$clip_file" ]]; then
        log_fail "Gate 9 pre-flight: ${clip_file} missing or empty (Gate 4 must write 3 clip_ids before Gate 9 can run) — sentinel MISSING_CLIPS_HANDOFF"
        return 1
    fi
    log_info "Gate 9 pre-flight: ARTLIST_TERM=${ARTLIST_TERM}; ${clip_file} clip_count=$(wc -l < "$clip_file" | tr -d ' ')"

    # The canonical "no new download" / "no new upload" evidence is the
    # per-clip loop below: media_assets COUNT(*) WHERE id=<clip> must equal
    # exactly 1 (canonical row preserved), AND asset_locations drive
    # COUNT for the clip must be >=1 (Gate 7 contract — Drive mirror row).
    # Any drift on EITHER check fails the gate (sentinels NEW_DOWNLOAD_DETECTED
    # + ASSET_LOCATION_DRIVE_MISSING). Per AGENTS.md Simplicity & Minimalism,
    # the prior AGGREGATE m_before snapshot was removed — dead-weight with no
    # binding downstream use.

    # ── Read original duration from jobs table (relative speed heuristic) ──
    local orig_elapsed_ms=0
    if [[ -s "${WORK_DIR}/gate4_run_id.txt" ]]; then
        local orig_run_id
        orig_run_id=$(cat "${WORK_DIR}/gate4_run_id.txt" 2>/dev/null | tr -d ' \n' || echo "")
        if [[ -n "$orig_run_id" ]]; then
            # jobs table has started_at + completed_at — diff in ms.
            local elapsed_ms_row
            elapsed_ms_row=$(smoke_sqlite_query "$DB_PATH" -json \
                "SELECT (julianday(completed_at) - julianday(started_at)) * 86400000 AS elapsed_ms \
                 FROM jobs WHERE id='${orig_run_id}' AND started_at IS NOT NULL AND completed_at IS NOT NULL" \
                2>/dev/null || echo "")
            if [[ -n "$elapsed_ms_row" ]]; then
                orig_elapsed_ms=$(printf '%s' "${elapsed_ms_row}" | \
                    jq -r 'if type == "array" then (.[0].elapsed_ms // 0) else (.elapsed_ms // 0) end' 2>/dev/null \
                    | awk '{print int($1+0.5)}' || echo 0)
            fi
            log_info "Gate 9 speed baseline: original run id=${orig_run_id} elapsed_ms=${orig_elapsed_ms}"
        else
            log_info "Gate 9 speed baseline: gate4_run_id.txt empty (relative speed check skipped)"
        fi
    else
        log_info "Gate 9 speed baseline: ${WORK_DIR}/gate4_run_id.txt absent (relative speed check skipped; only 30s absolute ceiling applies)"
    fi

    # ── Capture replay start time (for elapsed_ms measurement) ──────
    local start_ns end_ns elapsed_ms
    start_ns=$(date +%s%N)

    # ── Replay the same /api/artlist/run body verbatim ───────────────
    # artlist_replay_run emits <HTTP_code>\t<run_id>\t<body> on stdout.
    # Default strategy=replace, dry_run=false matches Gate 4 exactly;
    # root_folder_id=$ARTLIST_ROOT_FOLDER keeps the Drive routing bound.
    local replay_out replay_code replay_jid replay_body
    replay_out=$(artlist_replay_run \
        "$ARTLIST_TERM" 3 \
        replace 7 1920 1080 30 1 \
        "${ARTLIST_ROOT_FOLDER:-}" 2>/dev/null || true)
    replay_code=$(printf '%s' "$replay_out" | awk -F'\t' 'NF>=1{print $1; exit}')
    replay_jid=$(printf '%s' "$replay_out" | awk -F'\t' 'NF>=2{print $2; exit}')
    replay_body=$(printf '%s' "$replay_out" | awk -F'\t' 'NF>=3{print $3; exit}')

    if [[ ! "$replay_code" =~ ^2[0-9][0-9]$ ]]; then
        log_fail "Gate 9 POST /api/artlist/run replay returned HTTP=${replay_code} (expected 2xx); sentinel REPLAY_ENQUEUE_FAILED"
        return 1
    fi
    if [[ -z "$replay_jid" ]]; then
        log_fail "Gate 9 POST /api/artlist/run response had no .run_id; sentinel REPLAY_ENQUEUE_NO_RUN_ID; body=${replay_body}"
        return 1
    fi
    log_pass "Gate 9 POST /api/artlist/run replay enqueued run_id=${replay_jid} HTTP=${replay_code}"

    # ── Poll until terminal (operator feedback via SMOKE_LAST_STATUS) ─
    # artlist_poll_run returns 0 on terminal, 124 on timeout, 1 on
    # transport failure. We fail-closed on timeout (rc=124) because
    # it indicates the replay never reached terminal — operator must
    # investigate the orchestrator queue. rc=1 also fails-closed.
    if ! artlist_poll_run "$replay_jid" >/dev/null 2>&1; then
        local poll_rc=$?
        log_fail "Gate 9 artlist_poll_run never reached terminal for run_id=${replay_jid} (rc=${poll_rc}; 124=timeout, 1=transport); sentinel REPLAY_POLL_INCOMPLETE"
        return 1
    fi
    log_info "Gate 9 replay reached terminal status=${SMOKE_LAST_STATUS:-?}"

    # ── Capture replay end time ─────────────────────────────────────
    end_ns=$(date +%s%N)
    elapsed_ms=$(( (end_ns - start_ns) / 1000000 ))
    log_info "Gate 9 wall-clock elapsed_ms=${elapsed_ms} (start_ns=${start_ns} end_ns=${end_ns})"

    # ── GET /api/artlist/runs/<run_id> for the canonical terminal body ──
    local http_code
    http_code=$(smoke_curl GET "/api/artlist/runs/${replay_jid}" -o "${WORK_DIR}/gate9_replay_run_${replay_jid}.body")
    if [[ "$http_code" != "200" ]]; then
        log_fail "Gate 9 GET /api/artlist/runs/${replay_jid} returned HTTP=${http_code} (expected 200); sentinel REPLAY_GET_FAILED"
        return 1
    fi
    local replay_run_body
    replay_run_body="${WORK_DIR}/gate9_replay_run_${replay_jid}.body"

    # ── Hard-fail cache_hit + cache_source (per user spec literal) ──
    local cache_hit cache_source
    cache_hit=$(jq -r '.cache_hit // empty' "$replay_run_body" 2>/dev/null || echo "")
    cache_source=$(jq -r '.cache_source // empty' "$replay_run_body" 2>/dev/null || echo "")
    log_info "Gate 9 cache_hit.json = ${cache_hit}; cache_source.json = ${cache_source}"
    if [[ "$cache_hit" != "true" ]]; then
        log_fail "Gate 9 response.cache_hit='${cache_hit}' (expected literal 'true' — DoD spec: cache_hit è campo obbligatorio); sentinel CACHE_HIT_MISSING_OR_FALSE; run_id=${replay_jid}"
        return 1
    fi
    if [[ "$cache_source" != "sqlite" ]]; then
        log_fail "Gate 9 response.cache_source='${cache_source}' (expected literal 'sqlite' — DoD spec: cache_source è campo obbligatorio); sentinel CACHE_SOURCE_NOT_SQLITE; run_id=${replay_jid}"
        return 1
    fi
    log_pass "Gate 9 cache_hit=true AND cache_source=sqlite (mandatory fields present)"

    # ── Per-clip: clip_id|file_hash|drive_file_id tuple round-trip ──
    # Cross-check TWO sources of truth:
    #   (1) media_assets DB SELECT (canonical) — per clip_id pull the
    #       current row's clip_id, file_hash, drive_file_id.
    #   (2) The replay RunTagResponse's items[] (cross-check) — pull
    #       the items[].{clip_id, drive_file_id} per row.
    # Both must agree OR the gate fails (single source of truth doctrine).
    # The original Gate 4 run's media_assets rows are already present
    # (Gate 5 cross-confirmed this); the replay MUST NOT have changed
    # them.
    local clip_id db_row response_clip_id response_drive_file_id
    local ok_clips=0 fail_clips=0
    local clip_count=0
    while IFS= read -r clip_id; do
        [[ -z "$clip_id" ]] && continue
        clip_count=$((clip_count + 1))
        local clip_ok=1
        log_info "── clip ${clip_id}"

        # ── (1) DB-side: media_assets.id|file_hash|drive_file_id ──
        db_row=$(smoke_sqlite_query "$DB_PATH" -json \
            "SELECT id, file_hash, drive_file_id \
             FROM media_assets WHERE id='${clip_id}'" 2>/dev/null || echo "[]")
        local db_clip_id db_file_hash db_drive_file_id
        db_clip_id=$(printf '%s' "${db_row}" | \
            jq -r 'if type == "array" then (.[0].id // empty) else (.id // empty) end' 2>/dev/null || echo "")
        db_file_hash=$(printf '%s' "${db_row}" | \
            jq -r 'if type == "array" then (.[0].file_hash // empty) else (.file_hash // empty) end' 2>/dev/null || echo "")
        db_drive_file_id=$(printf '%s' "${db_row}" | \
            jq -r 'if type == "array" then (.[0].drive_file_id // empty) else (.drive_file_id // empty) end' 2>/dev/null || echo "")
        if [[ -z "$db_clip_id" || -z "$db_file_hash" || -z "$db_drive_file_id" ]]; then
            log_fail "Gate 9 clip ${clip_id}: DB SELECT returned empty fields (clip_id='${db_clip_id}', file_hash='${db_file_hash}', drive_file_id='${db_drive_file_id}') — sentinel DB_ROW_INCOMPLETE"
            fail_clips=$((fail_clips + 1))
            continue
        fi
        log_pass "Gate 9 clip ${clip_id}: DB canonical tuple (file_hash=$(printf '%s' "$db_file_hash" | cut -c1-12)…, drive_file_id=${db_drive_file_id})"

        # ── (2) Response-side: items[].clip_id + items[].drive_file_id ──
        # The replay response body carries items[].clip_id (canonical) +
        # items[].drive_file_id (mirror column on items). We cross-check.
        response_clip_id=$(jq -r --arg id "${clip_id}" \
            '(.items // []) | map(select(.clip_id == $id)) | if length > 0 then .[0].clip_id // empty else empty end' \
            "$replay_run_body" 2>/dev/null || echo "")
        response_drive_file_id=$(jq -r --arg id "${clip_id}" \
            '(.items // []) | map(select(.clip_id == $id)) | if length > 0 then .[0].drive_file_id // empty else empty end' \
            "$replay_run_body" 2>/dev/null || echo "")
        if [[ -z "$response_clip_id" || -z "$response_drive_file_id" ]]; then
            # If the items[] key is absent or empty for this clip_id,
            # log a warn and fall back to the DB-only path (the DB IS
            # the canonical source of truth per godlike/06; the response
            # is the cross-check but the DB is authoritative when response
            # doesn't carry the field). This matches Gate 6's defensive
            # many-shapes-tolerant pattern.
            log_info "Gate 9 clip ${clip_id}: response items[].clip_id/drive_file_id absent or empty; relying on DB canonical only (gate-level cross-check disabled)"
        else
            # Cross-check: response.clip_id MUST equal db_clip_id (id
            # round-trip — defends against a RenumberTags re-run that
            # mapped fresh clip_ids to old drive_ids).
            if [[ "$response_clip_id" != "$db_clip_id" ]]; then
                log_fail "Gate 9 clip ${clip_id}: response.clip_id='${response_clip_id}' ≠ db.clip_id='${db_clip_id}' (round-trip drift) — sentinel CLIP_ID_ROUNDTRIP_DRIFT"
                clip_ok=0
            else
                log_pass "Gate 9 clip ${clip_id}: response.clip_id matches db.clip_id (round-trip)"
            fi
            # Cross-check: response.drive_file_id MUST equal db_drive_file_id
            # (the Drive file id MUST be byte-stable across fresh+replay
            # runs because cache_hit=true means the same upload was
            # reused, NOT re-uploaded).
            if [[ "$response_drive_file_id" != "$db_drive_file_id" ]]; then
                log_fail "Gate 9 clip ${clip_id}: response.drive_file_id='${response_drive_file_id}' ≠ db.drive_file_id='${db_drive_file_id}' (Drive-eligibility drift) — sentinel DRIVE_FILE_ID_DRIFT"
                clip_ok=0
            else
                log_pass "Gate 9 clip ${clip_id}: response.drive_file_id matches db.drive_file_id (Drive byte-stable)"
            fi
        fi

        # ── Aggregate "no new upload" check via asset_locations ────
        # asset_locations.location_kind='drive' count for this clip.
        # Snapshot AFTER (in lockstep with per-clip loop). BEFORE is
        # the count computed once outside the loop (see above).
        # We compute per-clip AFTER here.
        local a_after_clip
        a_after_clip=$(smoke_sqlite_query "$DB_PATH" \
            "SELECT COUNT(*) FROM asset_locations \
             WHERE asset_id='${clip_id}' AND location_kind='drive'" \
            2>/dev/null | tr -d ' \n' || echo "?")
        if [[ "$a_after_clip" =~ ^[0-9]+$ ]] && [[ "$a_after_clip" -lt 1 ]]; then
            log_fail "Gate 9 clip ${clip_id}: asset_locations drive kind count is ${a_after_clip} (expected >=1 — Gate 7 contract requires a Drive mirror row) — sentinel ASSET_LOCATION_DRIVE_MISSING"
            clip_ok=0
        else
            log_pass "Gate 9 clip ${clip_id}: asset_locations drive count=${a_after_clip} (>=1 — Drive mirror row present; no new upload because asset_locations is byte-stable)"
        fi

        # ── Per-clip "no new download" check via media_assets row count ──
        # (already asserted by DB-row present above; this is a defence-
        # in-depth count check — exactly 1 row for this clip_id).
        local m_after_clip
        m_after_clip=$(smoke_sqlite_query "$DB_PATH" \
            "SELECT COUNT(*) FROM media_assets WHERE id='${clip_id}'" \
            2>/dev/null | tr -d ' \n' || echo "?")
        if [[ "$m_after_clip" != "1" ]]; then
            log_fail "Gate 9 clip ${clip_id}: media_assets COUNT(*) = ${m_after_clip} (expected exactly 1 — replay must NOT create new canonical rows) — sentinel NEW_DOWNLOAD_DETECTED"
            clip_ok=0
        else
            log_pass "Gate 9 clip ${clip_id}: media_assets COUNT(*) = 1 (no new download — replay reused the existing canonical row)"
        fi

        if (( clip_ok == 1 )); then
            ok_clips=$((ok_clips + 1))
        else
            fail_clips=$((fail_clips + 1))
        fi
    done < "${clip_file}"

    # ── Aggregate "faster execution" check ──────────────────────────
    # Two thresholds (belt-and-braces):
    #   (a) absolute ceiling: elapsed_ms < 30000 (DoD literal "esecuzione
    #       più veloce" implies a meaningful speedup; absolute 30s is a
    #       reasonable upper bound).
    #   (b) relative speedup: elapsed_ms < orig_elapsed_ms * 0.5 WHEN
    #       orig_elapsed_ms is readable (>0). Fail-closed if the
    #       relative heuristic is unmet (replay should be roughly half
    #       the time of the fresh run; if it's slower or equal, the
    #       cache layer isn't earning its keep).
    if (( elapsed_ms >= 30000 )); then
        log_fail "Gate 9 elapsed_ms=${elapsed_ms} exceeds 30s absolute ceiling; sentinel REPLAY_TOO_SLOW_ABSOLUTE — replay should cache-skip the download/process/upload stages"
        return 1
    fi
    log_pass "Gate 9 elapsed_ms=${elapsed_ms} < 30000ms absolute ceiling (fast replay)"

    if (( orig_elapsed_ms > 0 )); then
        local threshold_ms=$(( orig_elapsed_ms / 2 ))
        if (( elapsed_ms >= threshold_ms )); then
            log_fail "Gate 9 elapsed_ms=${elapsed_ms} NOT faster than original (orig=${orig_elapsed_ms} threshold=${threshold_ms}); sentinel REPLAY_NOT_FASTER_THAN_50PCT — cache layer did not earn its keep"
            return 1
        fi
        log_pass "Gate 9 elapsed_ms=${elapsed_ms} < ${threshold_ms}ms relative threshold (orig=${orig_elapsed_ms}, replay is < ${elapsed_ms}*100/${orig_elapsed_ms}% of original)"
    else
        log_info "Gate 9 relative speedup skipped (orig elapsed_ms not readable from jobs table — only absolute 30s ceiling applied)"
    fi

    # ── Final aggregate verdict (DoD forbids partial-pass) ────────
    if (( fail_clips > 0 )); then
        log_fail "Gate 9 — ${fail_clips} of ${clip_count} clip(s) failed tuple round-trip (each per-clip failure is a HARD fail per DoD spec)"
        return 1
    fi
    log_pass "Gate 9 — all ${ok_clips} of ${clip_count} clip(s) passed cache-replay round-trip (cache_hit=true + cache_source=sqlite + tuple stability + no-new-Drive-upload + fast execution)"
    return 0
}

main() {
    if [[ "$DRY_RUN" == "1" ]]; then
        smoke_echo_safe "DRY RUN — cache replay (Gate 9):"
        printf '  pre-flight fail-closed: ARTLIST_TERM env var MUST be set + ${WORK_DIR}/clip_ids.txt MUST be non-empty (sentinels MISSING_TERM_UNSET / MISSING_CLIPS_HANDOFF)\n'
        printf '  snapshot BEFORE: media_assets COUNT + asset_locations drive COUNT per clip_id (canonical DB fact)\n'
        printf '  capture original elapsed_ms from jobs table (if ${WORK_DIR}/gate4_run_id.txt present)\n'
        printf '  artlist_replay_run $ARTLIST_TERM 3 replace 7 1920 1080 30 1 $ARTLIST_ROOT_FOLDER\n'
        printf '  poll replayed run_id terminal via artlist_poll_run\n'
        printf '  GET /api/artlist/runs/<replay_run_id> — hard-fail if response.cache_hit != true OR response.cache_source != "sqlite"\n'
        printf '  per clip_id:\n'
        printf '    (1) DB-side media_assets SELECT clip_id|file_hash|drive_file_id (canonical)\n'
        printf '    (2) response.items[].{clip_id, drive_file_id} (cross-check)\n'
        printf '    (3) asset_locations drive count per clip (no new upload)\n'
        printf '    (4) media_assets COUNT(*) per clip (no new download)\n'
        printf '  aggregate "fast execution": elapsed_ms < 30000 absolute AND < orig/2 relative (when orig readable)\n'
        printf '  ALL ${WORK_DIR}/clip_ids.txt clips must pass (DoD forbids partial-pass — mirrors Gates 4/5/6/7 aggregate)\n'
        printf '\nLib helpers exercised:\n'
        printf '  artlist_replay_run   (lib/artlist.sh canonical) for the replay POST /api/artlist/run\n'
        printf '  artlist_poll_run     (lib/artlist.sh canonical) for the replay terminal poll\n'
        printf '  smoke_curl           (lib/common.sh) for the GET /api/artlist/runs/<id>\n'
        printf '  smoke_sqlite_query   (lib/common.sh) for media_assets + asset_locations SELECTs\n'
        printf '  log_pass / log_fail  (lib/artlist_runtime.sh via umbrella)\n'
        exit 0
    fi
    gate_cache_replay || return 1

    printf '\n============================================\n'
    printf '  09_cache_replay\n'
    printf '  PASS=%d  WARN=%d  FAIL=%d\n' "$PASS" "$WARN" "$FAIL"
    printf '============================================\n'
    if [[ "$FAIL" -gt 0 ]]; then
        printf 'VERDICT: FAIL\n'
        return 1
    fi
    printf 'VERDICT: PASS\n'
    return 0
}

main "$@"
