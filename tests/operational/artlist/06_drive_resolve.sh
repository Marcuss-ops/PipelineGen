#!/usr/bin/env bash
# tests/operational/artlist/06_drive_resolve.sh — Artlist DoD Gate 6
# (Drive resolve-by-id hard gate).
#
# Reorg (July 2026): the existing tests/operational/artlist/06_drive.sh
# was a STUB and is now superseded; this file is the canonical
# Gate 6 owner (filename widened from `06_drive` to `06_drive_resolve`
# per the user's explicit "nuovo file" directive).
#
# Hand-off contract (binding): per-clip loop reads clip IDs from
# ${WORK_DIR}/clip_ids.txt written by Gate 4 (05_pipeline_fresh.sh).
# Same $WORK_DIR inside run_all.sh so all 5 gates (Gate 4 + 5/6/7/8)
# iterate over the same finalised clip set.
#
# Per-clip sub-invariants (DoD spec verbatim, July 2026):
#   step-1  smoke_sqlite_query → drive_file_id MUST be non-empty
#   step-2  artlist_drive_resolve (lib/artlist.sh, the canonical SSOT)
#           → rc=0 only on .ok AND .resolved_count>=1
#                                AND .resolved[0].trashed==false
#                                AND .resolved[0].size>0
#   step-3  INLINE jq -e `.resolved[0].parents[] | any(. == $root)`
#           → file MUST be in ARTLIST_ROOT_FOLDER
#   step-4  INLINE curl -I probe on webViewLink
#           → rc=0 only on 2xx OR 3xx
#           (Drive public-share often 302 → accounts.google.com)
#
# Pre-flight (gate-level fail-closed): ARTLIST_ROOT_FOLDER non-empty
# else emit typed sentinel ROOT_FOLDER_UNSET and abort before the
# per-clip loop (AGENTS.md "Never represent an unavailable backend as
# a successful no-op").
#
# Failure surface (gate-level only — no helpers are mutated): each
# sub-invariant triggers log_fail via the canonical log_* family from
# artlist_runtime.sh. ALL clips MUST pass (DoD forbids partial-pass —
# mirrors Gates 4/5/7).

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# Source the umbrella per the canonical import contract; resolves
# path-invariant via BASH_SOURCE[0]. The umbrella's helper-name guard
# fails closed if a future refactor removes any expected helper from
# lib/, surfacing the regression at import time instead of at first
# call site (godlike/06 SSOT enforcement).
# shellcheck disable=SC1091
source "$DIR/../lib/_artlist_common.sh"

smoke_require curl jq sqlite3

# ── Gate 6 — Drive resolve-by-id hard gate ──────────────────────────────
gate_drive_resolve() {
    smoke_log_section "Gate 6 — Drive resolve-by-id hard gate"

    # ── pre-flight: ARTLIST_ROOT_FOLDER must be non-empty ────────────
    # Fail-closed with a typed sentinel so the operator can grep the
    # diagnostic; we do NOT silently no-op on missing config (AGENTS.md
    # fail-closed invariant). ARTLIST_ROOT_FOLDER mirrors the canonical
    # Drive folder id used by the Artlist scrape seeds.
    if [[ -z "${ARTLIST_ROOT_FOLDER:-}" ]]; then
        log_fail "Gate 6 pre-flight: ARTLIST_ROOT_FOLDER env var unset — sentinel ROOT_FOLDER_UNSET; refusing to verify Drive folder membership"
        log_fail "Set VELOX_DRIVE_ARTLIST_ROOT (Drive root folder id) before running Gate 6"
        return 1
    fi
    log_info "Gate 6 pre-flight: ARTLIST_ROOT_FOLDER=${ARTLIST_ROOT_FOLDER}"

    local clip_file="${WORK_DIR}/clip_ids.txt"
    if [[ ! -s "$clip_file" ]]; then
        log_fail "Gate 6 hand-off ${clip_file} not found or empty (Gate 4 must run first)"
        return 1
    fi
    log_info "Gate 6 hand-off ${clip_file} (clip count = $(wc -l < "$clip_file" | tr -d ' '))"

    local clip_id drive_file_id body
    local ok_clips=0 fail_clips=0
    while IFS= read -r clip_id; do
        [[ -z "$clip_id" ]] && continue
        log_info "── clip ${clip_id}"
        local clip_ok=1

        # ── step-1: lookup drive_file_id from media_assets ───────────
        # Gate 5 cross-check guarantees drive_file_id is already populated
        # for clips that survived the pipeline; this lookup is a
        # defence-in-depth assertion (Gate 5 also asserts drive_file_id
        # non-empty in its 18-invariant composite).
        local id_row
        id_row=$(smoke_sqlite_query "$DB_PATH" \
            "SELECT drive_file_id FROM media_assets WHERE id='${clip_id}'" \
            2>/dev/null) || id_row=""
        drive_file_id="${id_row//[$'\r\n ']/}"
        if [[ -z "$drive_file_id" ]]; then
            log_fail "step-1 ${clip_id}: drive_file_id empty in media_assets"
            fail_clips=$((fail_clips + 1))
            continue
        fi
        log_pass "step-1 ${clip_id}: drive_file_id=${drive_file_id}"

        # ── step-2: artlist_drive_resolve (DoD canonical envelope) ────
        # The function has been moved from lib/velox_domain.sh to
        # lib/artlist.sh (DoD "estrazione futura" consolidation). The
        # artlist_drive_resolve is the canonical Drive resolver.
        # Helper's RC semantics (matches Gates 1+2 typed-helper contract):
        #   rc=0 → contract pass (HTTP 2xx + jq contract).
        #   rc=1 → HTTP 2xx but jq shape violated (e.g. trashed=true,
        #           size=0, resolved_count<1, .ok=false).
        #   rc=2 → transport/HTTP non-2xx (connect refused, 5xx, empty).
        # Helper writes body to ${WORK_DIR:-/tmp}/velox_drive_${file_id}.json
        # for forensic inspection by step-3 (parent-membership jq reads
        # the same body — no extra HTTP per sub-invariant).
        local drive_rc
        artlist_drive_resolve "$drive_file_id"
        drive_rc=$?
        case "$drive_rc" in
            0)
                log_pass "step-2 ${clip_id}: artlist_drive_resolve contract pass (ok=true resolved_count>=1 trashed=false size>0)"
                ;;
            1)
                log_fail "step-2 ${clip_id}: artlist_drive_resolve contract violated (HTTP 2xx but canonical shape failed); body in ${WORK_DIR:-/tmp}/artlist_drive_${drive_file_id}.json"
                clip_ok=0
                ;;
            2)
                log_fail "step-2 ${clip_id}: artlist_drive_resolve transport/HTTP failure (rc=2; verify PipelineGen + Drive API reachability)"
                clip_ok=0
                ;;
            *)
                log_fail "step-2 ${clip_id}: artlist_drive_resolve unexpected rc=${drive_rc}"
                clip_ok=0
                ;;
        esac

        # Read body for steps 3+4 even on rc=1 — the body still exists
        # (the contract failure is in jq, not curl). On rc=2 (transport)
        # the body may be empty; skip steps 3+4 with a marker log.
        body="${WORK_DIR:-/tmp}/artlist_drive_${drive_file_id}.json"
        if [[ "$drive_rc" != "0" ]]; then
            fail_clips=$((fail_clips + 1))
            continue
        fi
        [[ -s "$body" ]] || {
            log_fail "step-3/4 ${clip_id}: body file $body empty after artlist_drive_resolve rc=0"
            fail_clips=$((fail_clips + 1))
            continue
        }

        # ── step-3: INLINE jq parent-membership check ───────────────
        # DoD-exact contract (the Artlist operational contract Gate-6 verbatim):
        # `.resolved[0].parents // [] | any(. == $ARTLIST_ROOT_FOLDER)`.
        # Empty parents[] fails closed (raw Drive folder ID string-equal
        # match — numeric/string drift triggers rc rather than silent
        # accept).
        if ! jq -e --arg root "${ARTLIST_ROOT_FOLDER}" \
            '(.resolved[0].parents // []) | any(. == $root)' \
            "$body" >/dev/null 2>&1; then
            log_fail "step-3 ${clip_id}: parents[] does NOT contain ARTLIST_ROOT_FOLDER=${ARTLIST_ROOT_FOLDER} (file is in a different Drive folder)"
            clip_ok=0
        else
            log_pass "step-3 ${clip_id}: parents[] contains ARTLIST_ROOT_FOLDER"
        fi

        # ── step-4: INLINE curl link-probe on webViewLink ────────────
        # Drive's public-share view usually 302-redirects to
        # accounts.google.com on signed-in guests; accept 2xx OR 3xx
        # as PASS per the DoD spec literal "2xx OR 3xx = PASS".
        local webview
        webview=$(jq -r '.resolved[0].webViewLink // .resolved[0].webViewLinkUrl // empty' "$body" 2>/dev/null || echo)
        if [[ -z "$webview" ]]; then
            log_fail "step-4 ${clip_id}: webViewLink missing in resolve response"
            clip_ok=0
        else
            local link_code
            link_code=$(curl -sS --max-time 6 -o /dev/null -w '%{http_code}' -I "$webview" 2>/dev/null || echo 000)
            if [[ "$link_code" =~ ^[23][0-9][0-9]$ ]]; then
                log_pass "step-4 ${clip_id}: webViewLink probe HTTP=${link_code} (2xx|3xx — Drive share-view redirects accepted)"
            else
                log_fail "step-4 ${clip_id}: webViewLink probe HTTP=${link_code} (expected 2xx or 3xx)"
                clip_ok=0
            fi
        fi

        if (( clip_ok == 1 )); then
            ok_clips=$((ok_clips + 1))
        else
            fail_clips=$((fail_clips + 1))
        fi
    done < "$clip_file"

    log_info "Gate 6 per-clip tally: ok=${ok_clips} fail=${fail_clips}"

    if (( fail_clips > 0 )); then
        log_fail "Gate 6 — ${fail_clips} clip(s) failed Drive resolve-by-id"
        return 1
    fi
    log_pass "Gate 6 — all ${ok_clips} clip(s) passed Drive resolve-by-id"
    return 0
}

main() {
    if [[ "$DRY_RUN" == "1" ]]; then
        smoke_echo_safe "DRY RUN — Drive resolve-by-id (Gate 6):"
        printf '  pre-flight: ARTLIST_ROOT_FOLDER non-empty (fail-closed sentinel ROOT_FOLDER_UNSET otherwise)\n'
        printf '  consume hand-off %s/clip_ids.txt from Gate 4\n' "$WORK_DIR"
        printf '  per clip_id:\n'
        printf '    step-1  smoke_sqlite_query → drive_file_id (non-empty)\n'
        printf '    step-2  artlist_drive_resolve (ok + resolved_count>=1 + trashed=false + size>0)\n'
        printf '    step-3  INLINE jq -e parents[] ⊇ ARTLIST_ROOT_FOLDER\n'
        printf '    step-4  INLINE curl -I webViewLink probe (HTTP 2xx OR 3xx accepted)\n'
        printf '  ALL clips MUST pass.\n'
        exit 0
    fi
    gate_drive_resolve || return 1

    printf '\n============================================\n'
    printf '  06_drive_resolve\n'
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
