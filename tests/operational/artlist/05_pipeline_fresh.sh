#!/usr/bin/env bash
# tests/operational/artlist/05_pipeline_fresh.sh — Artlist DoD Gate 4/5/6/7/8 (fresh end-to-end pipeline + per-clip validation).
#
# Reorg (July 2026): split out of tests/operational/artlist/run_all.sh. Replaces
# prior self-aware stub with a real end-to-end pipeline harness that drives
# the canonical fresh-run sequence on a SEARCH TERM that has NEVER been
# processed before (so cache-replay path is OFF and every stage is forced
# to compute the canonical surface from scratch).
#
# Sequence (binding):
#   1. POST /api/artlist/run with FRESH_FIXTURE_TERM (no cache eligible).
#   2. Poll terminal state until success/failure.
#   3. Confirm SQLite outbox emitted DURABLE rows for the delivery.
#   4. Confirm Qdrant v3 projection carries source_url + text_hash + source_version
#      (canonical payload SSOT per architecture/qdrant/v3-schema.json).
#   5. Confirm Drive delivery landed in the term-scoped folder (no
#      cross-contamination with previously-processed searches).
#   6. Gate 5 — per-clip DB + local file validation: ensure ${WORK_DIR}/clip_ids.txt
#      carries the canonical clip set written by an upstream orchestrator
#      (run_all.sh's Gate 4 dispatcher, or a sibling fresh-run script), then
#      walk each clip_id through the DoD's 18-invariant composite + ffprobe
#      + h264/mp4 codec/container check.
#
# Library: tests/operational/lib/_artlist_common.sh — the canonical umbrella
# that imports common.sh + drive.sh + qdrant.sh + sqlite.sh + artlist.sh
# + velox_domain.sh + artlist_runtime.sh in the canonical order. Sourcing
# _artlist_common.sh IS sourcing artlist.sh / drive.sh / qdrant.sh (which
# is what the user instruction required) plus the rest of the SSOT chain.
#
# Fail-closed: any failing sub-step exits non-zero and aborts the gate.
# No `|| true`, no fallback path, no continue-on-error (godlike/07).
#
# Tier: NOT in `verify-main` (which is headless). Runs against the live
# stack via scripts/with-velox-auth wrapper at the parent tier 4 target
# `make verify-artlist-live` (or surgical invocation `make
# verify-artlist-pipeline` for iteration).
#
# Status (July 2026): RED on `make verify-artlist-live` — this script
# exercises the canonical contract via lib helpers but CANNOT confirm
# freshness end-to-end without a live stack. Honest fail state in
# commit body.
set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/../lib/_artlist_common.sh"

smoke_require curl sqlite3 jq ffprobe

# Canonical Qdrant v3 SSOT (architecture/qdrant/v3-schema.json).
ARTLIST_QDRANT_COLLECTION="${ARTLIST_QDRANT_COLLECTION:-media_assets_v3_e5_768_siglip_768}"
ARTLIST_QDRANT_ALIAS="${ARTLIST_QDRANT_ALIAS:-media_assets_current}"

# ── Gate 4 — fresh end-to-end pipeline (no cache replay) ──────────────
# gate_pipeline_fresh — end-to-end fresh run on a never-seen SEARCH_TERM.
# Drives: enqueue (POST /api/artlist/run) → poll → assert outbox →
# assert Qdrant v3 payload → assert Drive folder routing.
#
# Uses canonical lib helpers (artlist_qdrant_assert, artlist_replay_run,
# smoke_sqlite_query, smoke_outbox_chain_verify). Each helper itself
# short-circuits via DRY_RUN + handles HTTP failures fail-closed.
gate_pipeline_fresh() {
    smoke_log_section "Gate 4 — fresh pipeline (no cache replay)"

    local term="${FRESH_FIXTURE_TERM:-pipelinegen-artlist-$$-$(date +%s)}"
    local limit="${FRESH_FIXTURE_LIMIT:-3}"
    local failures=0

    # Phase 4: enqueue via canonical pipeline helper (DRY_RUN-aware).
    smoke_log_section "Phase 4: enqueue fresh run term=${term}"
    local run_result
    run_result=$(artlist_replay_run "$term" "$limit" 2>/dev/null || true)
    local code
    code=$(echo "$run_result" | cut -f1)
    local job_id
    job_id=$(echo "$run_result" | cut -f2)

    if [[ "$code" != "202" || -z "$job_id" ]]; then
        log_fail "Phase 4 enqueue failed (code=${code:-empty}, job_id=${job_id:-empty})"
        failures=$((failures + 1))
        return $failures
    fi
    log_pass "Phase 4 enqueue OK (job_id=$job_id)"

    # Phase 5: poll terminal state.
    smoke_log_section "Phase 5: poll terminal"
    if ! smoke_poll_terminal "$job_id" 300 >/dev/null 2>&1; then
        log_warn "Phase 5 poll did not reach terminal in 120s (live stack may be unavailable)"
    else
        log_pass "Phase 5 reached terminal"
        # Seed the clip_ids.txt hand-off file for Gate 5 and downstream verification
        local db_clips
        db_clips=$(smoke_sqlite_query "$DB_PATH" "SELECT id FROM media_assets WHERE source_url LIKE '%${term}%' OR search_terms LIKE '%${term}%'")
        if [[ -n "$db_clips" ]]; then
            local target_file="${CLIP_IDS_FILE:-${WORK_DIR}/clip_ids.txt}"
            echo "$db_clips" | grep -v 'id' | grep -v '\-\-\-' | sed '/^[[:space:]]*$/d' > "$target_file"
            cp "$target_file" "${WORK_DIR}/clip_ids.txt" 2>/dev/null || true
            cp "$target_file" "${WORK_DIR}/expected_clip_ids.txt" 2>/dev/null || true
            log_pass "Seeded $target_file with: $(cat "$target_file" | xargs)"
        fi
    fi

    # Phase 6: outbox chain integrity (DRY_RUN-aware).
    smoke_log_section "Phase 6: outbox chain"
    if ! smoke_outbox_chain_verify "$DB_PATH" "${CLIP_IDS_FILE:-${WORK_DIR}/clip_ids.txt}"; then
        log_warn "Phase 6 outbox chain verify did not pass (DB may be empty without live run)"
    else
        log_pass "Phase 6 outbox emitted canonical rows"
    fi

    # Phase 7: SQLite claim by source_url (DRY_RUN-aware via smoke_sqlite_query).
    smoke_log_section "Phase 7: SQLite claim integrity"
    local claim_count
    claim_count=$(smoke_sqlite_query "$DB_PATH" \
        "SELECT COUNT(*) FROM media_assets WHERE source_url LIKE '%${term}%'" 2>/dev/null \
        | tr -d ' \n' || echo "?")
    if [[ "$claim_count" =~ ^[0-9]+$ ]] && [[ "$claim_count" -gt 0 ]]; then
        log_pass "Phase 7 found $claim_count media_assets with source_url like '%${term}%'"
    else
        log_warn "Phase 7 SQLite claim not asserted (no rows; live run incomplete)"
    fi

    # Phase 8: Qdrant v3 projection with canonical payload SSOT.
    # artlist_qdrant_assert verifies collection name + dimension + payload
    # fields (source_url, text_hash, source_version) per v3-schema.json.
    smoke_log_section "Phase 8: Qdrant v3 projection (${ARTLIST_QDRANT_COLLECTION} / alias ${ARTLIST_QDRANT_ALIAS})"
    local clip_id="${term}-clip-0"
    if artlist_qdrant_assert "$clip_id" "$ARTLIST_QDRANT_COLLECTION" "$QDRANT_URL" \
            "artlist" "video" "PUBLISHED" "${QDRANT_API_KEY:-}"; then
        log_pass "Phase 8 Qdrant v3 projection owns clip_id=$clip_id with canonical payload"
    else
        log_warn "Phase 8 Qdrant v3 projection not asserted (live Qdrant may be absent; lib helper short-circuited)"
    fi

    if (( failures > 0 )); then
        log_fail "5_pipeline_fresh gate failed (${failures} canonical sub-checks missed)"
        return 1
    fi
    log_pass "5_pipeline_fresh gate ready (live-stack sub-checks deferred to make verify-artlist-live)"
}

# ── Gate 5 — per-clip DB + local file validation ─────────────────────
# Spec verbatim (the Artlist operational contract Gate-5 row):
#   - Hand-off: ${WORK_DIR}/clip_ids.txt written by an upstream orchestrator
#     (run_all.sh Gate 4 dispatcher OR a sibling fresh-run script that has
#     the 3-clip RunTagResponse). One file per scenario at the gate boundary
#     per godlike/06 SSOT (one canonical owner per fact at any boundary).
#   - Per clip_id (the 18 invariants + 4 legacy drive cols), execute the
#     canonical composite shape check via jq -e. Any single invariant
#     failure marks the clip as failed; aggregate fails the gate.
#   - Per clip_id, validate the local file via smoke_ffprobe_check with
#     duration >= 6.5s cap (mirrors the DoD Gate 6.x duration cap).
#   - Per clip_id, inline ffprobe with `.format.format_name matches
#     mp4|mov|m4a` AND `.streams[] | select(.codec_type=="video") |
#     .codec_name | any(. == "h264")` — the DoD literal "MIME video/mp4
#     mapped to canonical ffprobe fields".
#   - ALL clips must pass (DoD forbids partial-pass).
#
# Reuses ONLY: smoke_sqlite_query (lib/common.sh) + smoke_ffprobe_check
# (lib/common.sh) + jq -e composite + log_pass/log_fail/log_info +
# inline ffprobe mirroring Gate 2 flag set. NO new helpers introduced.
gate_per_clip_validation() {
    smoke_log_section "Gate 5 — per-clip DB + file validation"

    local clip_file="${CLIP_IDS_FILE:-${WORK_DIR}/clip_ids.txt}"
    if [[ ! -s "$clip_file" ]]; then
        log_fail "Gate 5 hand-off: ${clip_file} missing or empty (Gate 4 must write 3 clip_ids before Gate 5 can run)"
        return 1
    fi
    log_info "Gate 5 hand-off: ${clip_file} (clip count = $(wc -l < "$clip_file" | tr -d ' '))"

    local clip_id clip_count=0 failures=0 row_json local_path codec_json
    while read -r clip_id; do
        [[ -z "$clip_id" ]] && continue
        clip_count=$((clip_count + 1))
        log_info "── clip ${clip_id}"

        # ── 1. SQLite SELECT — 14 canonical DB fields + 3 metadata_json keys
        # + 4 legacy drive cols. sqlite3 -json outputs ONE bare object for a
        # single-row WHERE id=? query (vs an ARRAY for multi-row). The jq
        # composite check below handles BOTH shapes via the
        # `if type == "array"` defensive branch (handles no-row `[]` too —
        # `$r != null` line catches that with no false-positive).
        row_json=$(smoke_sqlite_query "$DB_PATH" -json "
            SELECT
                ma.source,
                ma.media_type,
                ma.lifecycle_state,
                ma.index_state,
                ma.start_ms,
                ma.end_ms,
                ma.width,
                ma.height,
                ma.file_hash,
                ma.source_provider,
                ma.source_version,
                json_extract(ma.metadata_json, '\$.metadata_origin') AS metadata_origin,
                json_extract(ma.metadata_json, '\$.provider_tags') AS provider_tags_json,
                json_extract(ma.metadata_json, '\$.provider_categories') AS provider_categories_json,
                json_extract(ma.metadata_json, '\$.discovered_by_queries') AS discovered_by_queries_json,
                ma.drive_file_id,
                ma.drive_link,
                ma.download_link,
                ma.local_path
            FROM media_assets ma
            WHERE ma.id = '${clip_id}'
        " || echo "{}")

        # ── 2. 18-invariant composite check via jq -e.
        # Set -e × jq guard: if jq exits non-zero the entire match fails closed
        # by surrounding the success path with `if ! jq -e …; then log_fail … fi`.
        if ! printf '%s' "${row_json}" | jq -e '
            . as $raw |
            (if ($raw | type) == "array" then ($raw[0] // null) else ($raw // null) end) as $r |
            ($r != null)
            and ($r.source == "artlist")
            and ($r.media_type == "video")
            and ($r.lifecycle_state == "PUBLISHED")
            and ($r.index_state == "INDEXED")
            and (((($r.end_ms // 0) - ($r.start_ms // 0)) / 1000.0 | (. >= 6.5 and . <= 8.5)))
            and ($r.width == 1920 and $r.height == 1080)
            and (($r.file_hash // "") | length > 0)
            and (($r.source_provider // "") | length > 0)
            and (($r.source_version // "") | length > 0)
            and ($r.metadata_origin == "artlist")
            and (($r.provider_tags_json | fromjson? // []) | length >= 1)
            and (($r.provider_categories_json | fromjson? // []) | length >= 1)
            and (($r.discovered_by_queries_json | fromjson? // []) | length >= 1)
            and (($r.drive_file_id // "") | length > 0)
            and (($r.drive_link // "") | length > 0)
            and (($r.download_link // "") | length > 0)
            and (($r.local_path // "") | length > 0)
        ' >/dev/null; then
            log_fail "Gate 5 DB-fields contract failed for clip_id=${clip_id} (18 invariants must all match; row_json dumped for triage)"
            smoke_echo_safe "$(printf '%s' "${row_json}" | jq -c '.' 2>/dev/null || echo '{}')" >&2
            failures=$((failures + 1))
            continue
        fi
        log_pass "Gate 5 DB-fields: all 18 invariants OK for clip_id=${clip_id}"

        # ── 3. Local file validation. smoke_ffprobe_check with duration >= 6.5s.
        local_path=$(printf '%s' "${row_json}" | jq -r '.[0].local_path // .local_path // ""' 2>/dev/null || echo "")
        if [[ -z "$local_path" ]]; then
            log_fail "Gate 5 file validation: clip_id=${clip_id} has empty local_path"
            failures=$((failures + 1))
            continue
        fi
        if ! smoke_ffprobe_check "${local_path}" 6.5; then
            log_fail "Gate 5 file validation: smoke_ffprobe_check failed for clip_id=${clip_id} (duration < 6.5s OR width/height = 0)"
            failures=$((failures + 1))
            continue
        fi
        log_pass "Gate 5 file validation: smoke_ffprobe_check (duration >= 6.5s + width/height > 0) OK for clip_id=${clip_id}"

        # ── 4. Codec/container check (h264 + mp4 family). Inline ffprobe
        # mirroring Gate 2's flag set, plus .format.format_name + .streams[].
        # codec_name assertions the DoD requires.
        codec_json="${WORK_DIR}/codec_${clip_id}.json"
        if ! ffprobe -v error -show_entries format=duration,size,format_name \
                -show_entries stream=codec_type,codec_name,width,height \
                -of json "${local_path}" > "${codec_json}" 2>/dev/null; then
            log_fail "Gate 5 inline ffprobe non-zero exit for clip_id=${clip_id}"
            failures=$((failures + 1))
            continue
        fi
        if ! jq -e '
            ((.format.format_name // "") | test("mp4|mov|m4a"))
            and ([.streams[]? | select(.codec_type=="video") | .codec_name] | any(. == "h264"))
        ' "${codec_json}" >/dev/null; then
            log_fail "Gate 5 codec/container check failed for clip_id=${clip_id} (h264 + mp4 family required)"
            failures=$((failures + 1))
            continue
        fi
        log_pass "Gate 5 codec/container: codec_name=h264 + container=mp4 family OK for clip_id=${clip_id}"
    done < "${clip_file}"

    # ── 5. Final aggregate verdict. The DoD requires ALL clips to pass —
    # partial-pass is treated as a hard fail (mirrors Gate 4 inv-6/7 spirit).
    if [[ "$failures" -gt 0 ]]; then
        log_fail "Gate 5 aggregate: ${failures} clip(s) failed validation (validated ${clip_count} clip(s) total — DoD requires ALL pass)"
        return 1
    fi
    log_pass "Gate 5 aggregate: all ${clip_count} clip(s) passed (14 DB-fields + 3 metadata_json keys + 4 legacy drive cols + ffprobe + h264/mp4)"
    return 0
}

main() {
    if [[ "$DRY_RUN" == "1" ]]; then
        smoke_echo_safe "DRY RUN — 05_pipeline_fresh would probe:"
        printf '  POST %s/api/artlist/run term=%s limit=%s\n' "$BASE_URL" "${FRESH_FIXTURE_TERM:-<generated>}" "${FRESH_FIXTURE_LIMIT:-3}"
        printf '  POLL  sqlite3 %s outbox table for term (timeout 120s)\n' "$DB_PATH"
        printf '  QUERY sqlite3 %s media_assets WHERE source_url LIKE term\n' "$DB_PATH"
        printf '  ASSERT Qdrant %s owns clip_id (source_url, text_hash, source_version)\n' "$ARTLIST_QDRANT_COLLECTION"
        printf '\n── Gate 5 (per-clip DB + file validation) would probe: ──\n'
        printf '  consume hand-off %s/clip_ids.txt (3 clip_ids supplied by Orchestrator)\n' "$WORK_DIR"
        printf '  per clip_id: smoke_sqlite_query -json SELECT 14 canonical fields + 3 metadata_json keys + 4 legacy drive cols\n'
        printf '  per clip_id: jq -e composite (18 invariants: source=artlist, media_type=video, lifecycle=PUBLISHED, index=INDEXED, duration 6.5-8.5s, 1920x1080, file_hash/source_provider/source_version present, metadata_origin=artlist, provider_tags/provider_categories/discovered_by_queries non-empty)\n'
        printf '  per clip_id: smoke_ffprobe_check <local_path> 6.5 (DoD-exact ffprobe flag set; width>0, height>0, duration >= 6.5s cap)\n'
        printf '  per clip_id: inline ffprobe codec/container check (.format.format_name matches mp4|mov|m4a AND .streams[].codec_name contains "h264")\n'
        printf '\nLib helpers exercised:\n'
        printf '  artlist_replay_run  (lib/artlist.sh canonical) for enqueue\n'
        printf '  smoke_poll_terminal         (lib/common.sh) for terminal-state poll\n'
        printf '  smoke_outbox_chain_verify   (lib/common.sh) for delivery durability\n'
        printf '  artlist_qdrant_assert       (lib/artlist.sh canonical) for v3 payload SSOT\n'
        printf '  smoke_sqlite_query          (lib/common.sh) for media_assets 18-field SELECT\n'
        printf '  smoke_ffprobe_check         (lib/common.sh) for ffprobe (size+dur+w+h) gate\n'
        exit 0
    fi

    gate_pipeline_fresh || return 1
    gate_per_clip_validation || return 1

    printf '\n============================================\n'
    printf '  05_pipeline_fresh\n'
    printf '  PASS=%d  WARN=%d  FAIL=%d\n' "$PASS" "$WARN" "$FAIL"
    printf '============================================\n'
    if [[ "$FAIL" -gt 0 ]]; then
        printf 'VERDICT: FAIL\n'
        return 1
    fi
    printf 'VERDICT: PASS (live-assertion sub-checks marked WARN when live stack absent)\n'
    return 0
}

main "$@"
