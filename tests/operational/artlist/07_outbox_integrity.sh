#!/usr/bin/env bash
# tests/operational/artlist/07_outbox_integrity.sh — Artlist DoD Gate 7
# (SQLite + outbox integrity hard gate).
#
# Reorg (July 2026): split out of tests/operational/artlist/07_index.sh
# (which now owns only Gate 8 = Qdrant + media-search). This sub-battery
# enforces the 5 hard invariants from tests/operational/artlist_gates.md
# Gate-7 row verbatim, plus the post-loop forensic probe via the
# canonical `smoke_outbox_chain_verify` (lib/common.sh) DoD-exact helper.
#
# Hand-off contract (binding): the per-clip loop reads clip IDs from
# ${WORK_DIR}/clip_ids.txt written by Gate 4 (05_pipeline_fresh.sh). All
# 5 gates (5/6/7/8 + Replay) share $WORK_DIR inside run_all.sh so each
# per-clip loop iterates over the same finalised clip set.
#
# Failure surface (gate-level only — no helpers are mutated): inv-1..inv-5
# each trigger log_fail via the canonical log_* family from
# artlist_runtime.sh. The post-loop smoke_outbox_chain_verify probe is
# forensic-only (rc=1 on SUPERSEDED, but Gate 7's DoD surface accepts
# SUPERSEDED as terminal per migration 092); the `|| true` prevents the
# forensic helper from failing the gate on SUPERSEDED-rather-than-COMPLETED
# branches where the chain lag is acceptable.

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# Source the umbrella per the canonical import contract; resolves
# path-invariant via BASH_SOURCE[0]. The umbrella's helper-name guard
# fails closed if a future refactor removes any expected helper from
# lib/, surfacing the regression at import time instead of at first
# call site (godlike/06 SSOT enforcement).
# shellcheck disable=SC1091
source "$DIR/../lib/_artlist_common.sh"

smoke_require curl jq sqlite3 sha256sum

# ── Gate 7 — SQLite + outbox integrity ──────────────────────────────────
# Spec (tests/operational/artlist_gates.md Gate-7 row, July 2026 DoD):
#   - consume hand-off ${WORK_DIR}/clip_ids.txt (Gate 4 wrote it).
#   - per clip_id, 5 HARD invariants (signature: set -u + smoke_sqlite_query):
#       inv-1: media_assets COUNT(*) WHERE id='<clip_id>' MUST be exactly 1
#              (no duplicate canonical rows).
#       inv-2: file_hash from media_assets MUST equal sha256sum(local_path)
#              (one-shot SELECT for both columns; default pipe-delimited
#               split per DoD spec).
#       inv-3: asset_locations COUNT WHERE asset_id='<clip_id>'
#              AND location_kind='local' >= 1 (per migration 055 ASSET_LOCATIONS).
#       inv-4: asset_locations COUNT WHERE asset_id='<clip_id>'
#              AND location_kind='drive' >= 1 (Drive mirror row).
#       inv-5: outbox_events SUM(CASE...) WHERE event_type='asset.index.requested'
#              AND aggregate_id='<clip_id>' MUST satisfy
#              (completed+superseded) >= 1 AND dead_letter == 0 AND total >= 1
#              (per-clip inline per AGENTS.md no-duplicate-classification;
#              accepts SUPERSEDED as terminal per migration 092 +
#              outboxevents.MarkSuperseded writer).
#   - post-loop forensic: smoke_outbox_chain_verify $DB_PATH $clip_file || true
#              (helper rc=1 is stricter than Gate-7's DoD surface; the
#              `|| true` keeps it diagnostic-only without false-fail).
#   - ALL clips MUST pass (DoD forbids partial-pass — mirrors Gates 4/5/6).
gate_outbox_integrity() {
    smoke_log_section "Gate 7 — SQLite + outbox integrity"
    local clip_file="${WORK_DIR}/clip_ids.txt"
    if [[ ! -s "$clip_file" ]]; then
        log_fail "Gate 7 hand-off ${clip_file} not found or empty (Gate 4 must run first)"
        return 1
    fi
    log_info "Gate 7 hand-off ${clip_file} (clip count = $(wc -l < "$clip_file" | tr -d ' '))"

    local clip_id file_hash local_path local_count drive_count
    local completed superseded dead_letter total inv_pass
    local ok_clips=0 fail_clips=0
    while IFS= read -r clip_id; do
        [[ -z "$clip_id" ]] && continue
        inv_pass=1
        log_info "── clip ${clip_id}"

        # ── inv-1: exactly one canonical row in media_assets ───────
        local row_count
        row_count=$(smoke_sqlite_query "$DB_PATH" \
            "SELECT COUNT(*) FROM media_assets WHERE id='${clip_id}'" \
            2>/dev/null) || row_count="?"
        if [[ "$row_count" != "1" ]]; then
            log_fail "inv-1 media_assets row count for ${clip_id} = ${row_count} (expected 1)"
            inv_pass=0
        else
            log_pass "inv-1 media_assets row count for ${clip_id} = 1"
        fi

        # ── inv-2: file_hash coherent with local_path on disk ──────
        # One-shot SELECT for file_hash + local_path; default sqlite3
        # pipe-delimited split per DoD spec matching artlist_gates.md Gate-7.
        local hash_row
        hash_row=$(smoke_sqlite_query "$DB_PATH" \
            "SELECT file_hash, local_path FROM media_assets WHERE id='${clip_id}'" \
            2>/dev/null) || hash_row="|"
        file_hash="${hash_row%%|*}"
        local_path="${hash_row#*|}"
        if [[ -z "$local_path" || ! -f "$local_path" ]]; then
            log_fail "inv-2 local_path for ${clip_id} is empty or file missing (got '${local_path}')"
            inv_pass=0
        else
            local actual_hash
            actual_hash="$(sha256sum "$local_path" 2>/dev/null | awk '{print $1}')"
            if [[ "$actual_hash" != "$file_hash" ]]; then
                log_fail "inv-2 ${clip_id}: file_hash drift — db='${file_hash}' disk='${actual_hash}'"
                inv_pass=0
            else
                log_pass "inv-2 ${clip_id}: file_hash matches sha256sum(${local_path})"
            fi
        fi

        # ── inv-3: at least one local asset_locations row ──────────
        local_count=$(smoke_sqlite_query "$DB_PATH" \
            "SELECT COUNT(*) FROM asset_locations WHERE asset_id='${clip_id}' AND location_kind='local'" \
            2>/dev/null) || local_count="?"
        if (( local_count < 1 )); then
            log_fail "inv-3 asset_locations local for ${clip_id} = ${local_count} (expected >= 1)"
            inv_pass=0
        else
            log_pass "inv-3 asset_locations local for ${clip_id} = ${local_count}"
        fi

        # ── inv-4: at least one drive asset_locations row ──────────
        drive_count=$(smoke_sqlite_query "$DB_PATH" \
            "SELECT COUNT(*) FROM asset_locations WHERE asset_id='${clip_id}' AND location_kind='drive'" \
            2>/dev/null) || drive_count="?"
        if (( drive_count < 1 )); then
            log_fail "inv-4 asset_locations drive for ${clip_id} = ${drive_count} (expected >= 1)"
            inv_pass=0
        else
            log_pass "inv-4 asset_locations drive for ${clip_id} = ${drive_count}"
        fi

        # ── inv-5: outbox chain terminal with no dead_letter ──────
        # Inline SUM(CASE...) is the DoD-verbatim inv-5 contract (per
        # artlist_gates.md). smoke_outbox_chain_verify below is forensic
        # only and runs post-loop; this inline query is the per-clip hard gate
        # so per-clip failures surface inline, not only via aggregate chain.
        local outbox_row
        outbox_row=$(smoke_sqlite_query "$DB_PATH" \
            "SELECT
                SUM(CASE WHEN status='completed' THEN 1 ELSE 0 END) AS completed,
                SUM(CASE WHEN status='superseded' THEN 1 ELSE 0 END) AS superseded,
                SUM(CASE WHEN status='dead_letter' THEN 1 ELSE 0 END) AS dead_letter,
                COUNT(*) AS total
             FROM outbox_events
             WHERE event_type='asset.index.requested'
               AND aggregate_id='${clip_id}'" \
            2>/dev/null) || outbox_row="|"
        IFS='|' read -r completed superseded dead_letter total <<<"$outbox_row"
        if (( completed + superseded >= 1 )) \
            && (( dead_letter == 0 )) \
            && (( total >= 1 )); then
            log_pass "inv-5 ${clip_id}: outbox terminal (completed=${completed} superseded=${superseded} dead_letter=${dead_letter} total=${total})"
        else
            log_fail "inv-5 ${clip_id}: outbox terminal contract failed (completed=${completed} superseded=${superseded} dead_letter=${dead_letter} total=${total})"
            inv_pass=0
        fi

        if (( inv_pass == 1 )); then
            ok_clips=$((ok_clips + 1))
        else
            fail_clips=$((fail_clips + 1))
        fi
    done < "$clip_file"

    log_info "Gate 7 per-clip tally: ok=${ok_clips} fail=${fail_clips}"

    # ── post-loop forensic probe ──────────────────────────────────
    # smoke_outbox_chain_verify's rc=1 on SUPERSEDED is stricter than
    # Gate-7's DoD surface; the `|| true` keeps it diagnostic-only without
    # false-failing on a SUPERSEDED-rather-than-COMPLETED branch where the
    # chain lag is acceptable. The classification is logged on stdout
    # for forensic inspection.
    smoke_outbox_chain_verify "$DB_PATH" "$clip_file" || true

    if (( fail_clips > 0 )); then
        log_fail "Gate 7 — ${fail_clips} clip(s) failed outbox integrity"
        return 1
    fi
    log_pass "Gate 7 — all ${ok_clips} clip(s) passed outbox integrity"
    return 0
}

main() {
    if [[ "$DRY_RUN" == "1" ]]; then
        smoke_echo_safe "DRY RUN — outbox integrity (Gate 7):"
        printf '  consume hand-off %s/clip_ids.txt from Gate 4\n' "$WORK_DIR"
        printf '  per clip_id: 5 hard invariants\n'
        printf '    inv-1  media_assets COUNT(*) WHERE id=<clip_id> == 1\n'
        printf '    inv-2  file_hash == sha256sum(local_path)\n'
        printf '    inv-3  asset_locations location_kind=local >= 1\n'
        printf '    inv-4  asset_locations location_kind=drive >= 1\n'
        printf '    inv-5  outbox_events (completed+superseded)>=1 AND dead_letter==0 AND total>=1\n'
        printf '  post-loop forensic: smoke_outbox_chain_verify %s %s/clip_ids.txt || true\n' \
            "$DB_PATH" "$WORK_DIR"
        exit 0
    fi
    gate_outbox_integrity || return 1

    printf '\n============================================\n'
    printf '  07_outbox_integrity\n'
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
