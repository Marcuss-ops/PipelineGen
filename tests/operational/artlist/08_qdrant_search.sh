#!/usr/bin/env bash
# tests/operational/artlist/08_qdrant_search.sh — Artlist DoD Gate 8
# (Qdrant + media-search hard gate).
#
# Reorg (July 2026): extracted from tests/operational/artlist/07_index.sh
# (which now only carries the bundled index-probe surface) per the DoD
# "estrazione futura" consolidation. This file is the canonical Gate 8
# owner.
#
# Per-clip invariants per artlist_gates.md|Gate 8 verbatim:
#   - artlist_qdrant_assert (lib/artlist.sh, local SSOT): single call
#     covers /points/scroll existence + payload.source=artlist +
#     payload.media_type=video + payload.lifecycle_state=PUBLISHED +
#     round-trip asset_id (filter-bypass hardening per the lib's notes).
#   - POST /api/media/search (via smoke_curl): semantic-recovery HARD gate
#     per clip. Recoupment count >= 1 otherwise fail-closed.
# Pre-flight: QDRANT_URL + QDRANT_COLLECTION env defaults loaded.

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# Source the umbrella per the canonical import contract; resolves
# path-invariant via BASH_SOURCE[0]. The umbrella's helper-name guard
# fails closed if a future refactor removes any expected helper from
# lib/, surfacing the regression at import time instead of at first
# call site (godlike/06 SSOT enforcement).
# shellcheck disable=SC1091
source "$DIR/../lib/_artlist_common.sh"

smoke_require curl jq

# ── Gate 8 — Qdrant + media search hard gate ────────────────────────────
gate_qdrant_search() {
    smoke_log_section "Gate 8 — Qdrant + media search hard gate"

    local q_coll="${QDRANT_COLLECTION:-media_assets_collection}"
    local q_url="${QDRANT_URL:-http://127.0.0.1:6333}"
    if [[ -z "${QDRANT_URL:-}" ]]; then
        log_info "QDRANT_URL not set; defaulting to ${q_url}"
    fi

    # Gate 8 also consumes the Gate 4 normalized-term hand-off (POLISH:
    # symmetry with the Gate 4 WRITE-side log on clip_ids.txt). Treat the
    # file as an additional pre-flight sentinel — if the hand-off is
    # missing, fail closed so a Gate 4 regression surfaces here instead
    # of being masked downstream.
    local gate4_norm_term_file="${WORK_DIR}/gate4_norm_term.txt"
    if [[ ! -s "$gate4_norm_term_file" ]]; then
        log_fail "Gate 8 hand-off ${gate4_norm_term_file} not found or empty (HANDOFF_NORM_TERM_MISSING — Gate 4 must write the normalized term before Gate 8 can run)"
        return 1
    fi
    local term
    term=$(cat "${gate4_norm_term_file}" 2>/dev/null || echo "")
    log_info "Gate 8: consuming Gate 4 normalized term='${term}' from ${WORK_DIR}/gate4_norm_term.txt"

    local clip_file="${WORK_DIR}/clip_ids.txt"
    if [[ ! -s "$clip_file" ]]; then
        log_fail "Gate 8 hand-off ${clip_file} not found or empty (Gate 4 must run first)"
        return 1
    fi
    log_info "Gate 8 hand-off ${clip_file} (clip count = $(wc -l < "$clip_file" | tr -d ' '))"

    local clip_id qa_rc ok_clips=0 fail_clips=0
    while IFS= read -r clip_id; do
        [[ -z "$clip_id" ]] && continue
        log_info "── clip ${clip_id}"

        artlist_qdrant_assert "${clip_id}" "${q_coll}" "${q_url}" \
            "artlist" "video" "PUBLISHED" "${QDRANT_API_KEY:-}"
        qa_rc=$?
        case "$qa_rc" in
            0)
                log_pass "artlist_qdrant_assert for ${clip_id}: SHAPE pass (asset_id round-trip + source=artlist + media_type=video + lifecycle=PUBLISHED)"
                ;;
            1)
                log_fail "artlist_qdrant_assert for ${clip_id}: SHAPE drift (jq contract returned false); see ${WORK_DIR:-/tmp}/artlist_qdrant_${clip_id}.json"
                fail_clips=$((fail_clips + 1))
                continue
                ;;
            2)
                log_fail "artlist_qdrant_assert for ${clip_id}: TRANSPORT/HTTP non-2xx (verify QDRANT_URL + QDRANT_API_KEY freshness)"
                fail_clips=$((fail_clips + 1))
                continue
                ;;
            *)
                log_fail "artlist_qdrant_assert for ${clip_id}: unexpected rc=$qa_rc"
                fail_clips=$((fail_clips + 1))
                continue
                ;;
        esac
        ok_clips=$((ok_clips + 1))
    done < "$clip_file"

    log_info "Gate 8 per-clip tally: ok=${ok_clips} fail=${fail_clips}"

    if (( fail_clips > 0 )); then
        log_fail "Gate 8 — ${fail_clips} clip(s) failed Qdrant SHAPE check"
        return 1
    fi
    log_pass "Gate 8 — all ${ok_clips} clip(s) cleared Qdrant SHAPE check"

    # ── semantic recovery hard gate ────────────────────────────────────
    # POST /api/media/search with sources=[artlist]: recoupment count >= 1
    # otherwise fail-closed. This is the second half of the DoD Gate 8 spec.
    local search_body search_assets_file recoup
    search_body=$(jq -nc --arg q "${ARTLIST_TERM:-}" '{query:$q, sources:["artlist"], limit:3}')
    smoke_curl POST "/api/media/search" -d "$search_body" >/dev/null
    if [[ ! "${SMOKE_LAST_HTTP:-000}" =~ ^2[0-9][0-9]$ ]]; then
        log_fail "Gate 8 semantic recovery: POST /api/media/search HTTP=${SMOKE_LAST_HTTP:-000} (expected 2xx)"
        return 1
    fi
    log_pass "Gate 8 semantic recovery: POST /api/media/search HTTP=${SMOKE_LAST_HTTP} (2xx)"

    search_assets_file="${WORK_DIR:-/tmp}/search_assets.txt"
    jq -r '.items[]?.asset_id // empty' "${SMOKE_LAST_BODY}" > "$search_assets_file" 2>/dev/null || true
    # POLISH: whitespace-insensitive recoupment counter. `grep -Fxcf` was
    # whole-line fixed-string sensitive to trailing whitespace + line-end
    # differences; the canonical jq composite below (POLISH: replaces
    # `--argfile` with `--rawfile` -- argfile is deprecated in jq 1.6+)
    # reads both files as raw strings, splits on \n, trims each element,
    # and counts the intersection via `($b | map(select(. as $x | $a |
    # any(. == $x))) | length)`.
    recoup=$(jq -n \
        --rawfile ids "$clip_file" \
        --rawfile assets "$search_assets_file" \
        '($ids  // "" | split("\n") | map(select(. != "") | gsub("^\\s+|\\s+$"; ""))) as $a
         | ($assets // "" | split("\n") | map(select(. != "") | gsub("^\\s+|\\s+$"; ""))) as $b
         | ($b | map(select(. as $x | $a | any(. == $x))) | length)' 2>/dev/null || echo 0)
    if (( recoup >= 1 )); then
        log_pass "Gate 8 semantic recovery: recoup=${recoup} (at least 1 clip recouped via /api/media/search)"
    else
        log_fail "Gate 8 semantic recovery: recoup=${recoup} (expected >= 1; media.search did not recoup any of the 3 clips)"
        smoke_echo_safe "[FORENSIC] SMOKE_LAST_BODY.items[0..10]:"
        head -c 600 "${SMOKE_LAST_BODY}" 2>/dev/null | jq -c '.items[0:10] // empty' 2>/dev/null || true
        return 1
    fi

    return 0
}

main() {
    if [[ "$DRY_RUN" == "1" ]]; then
        smoke_echo_safe "DRY RUN — Qdrant + media search (Gate 8):"
        printf '  pre-flight: QDRANT_COLLECTION=${QDRANT_COLLECTION:-media_assets_collection}; QDRANT_URL=${QDRANT_URL:-http://127.0.0.1:6333}\n'
        printf '  consume hand-off %s/gate4_norm_term.txt (normalized term) from Gate 4 [POLISH: Gate 8 read-side symmetry with Gate 4 write-side log on clip_ids.txt]\n' "$WORK_DIR"
        printf '  consume hand-off %s/clip_ids.txt from Gate 4\n' "$WORK_DIR"
        printf '  per clip_id:\n'
        printf '    round-1 artlist_qdrant_assert <clip_id> <coll> <url> artlist video PUBLISHED  (SHAPE + asset_id round-trip)\n'
        printf '    round-2 POST /api/media/search + recoupment by jq --rawfile composite (whitespace-insensitive, >= 1 clip recouped) [POLISH: replaces grep -Fxcf]\n'
        printf '  ALL clips MUST pass.\n'
        exit 0
    fi
    gate_qdrant_search || return 1

    printf '\n============================================\n'
    printf '  08_qdrant_search\n'
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
