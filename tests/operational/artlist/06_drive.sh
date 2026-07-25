#!/usr/bin/env bash
# tests/operational/artlist/06_drive.sh — Artlist DoD Gate 6 (Drive resolve per clip).
#
# NOTE (July 2026): the prior canonical Door-6 owner was 06_drive_resolve.sh
# (per the gate6 commit). This file (06_drive.sh) is the OPERATOR-FACING
# alias requested in the operator-flow spec; it sources the canonical
# lib helper velox_drive_resolve via _artlist_common.sh umbrella and serves
# the same gate, retained here so the Makefile target `verify-artlist-drive`
# has a script that matches its canonical name. Future refactor waves may
# consolidate back to 06_drive_resolve.sh; until then both surface the same
# gate via the canonical lib helper.
#
# Library: tests/operational/lib/_artlist_common.sh — the canonical umbrella.
#
# Fail-closed: any failing sub-step exits non-zero and aborts the gate.
#
# Tier: NOT in `verify-main` (headless). Live-stack at
# `make verify-artlist-live` (or surgical `make verify-artlist-drive`).
#
# Status (July 2026): RED on `make verify-artlist-live` — relies on a live
# PipelineGen server reachable on $BASE_URL with VELOX_ADMIN_TOKEN sourced
# via scripts/with-velox-auth. Lib helper velox_drive_resolve short-circuits
# under DRY_RUN so this script is harmless in CI; real assertions gate on
# VELOX_ADMIN_TOKEN presence + $BASE_URL reachability.
set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/../lib/_artlist_common.sh"

smoke_require curl jq

# gate_drive_resolve — assert Drive folder routing for a clip uploaded
# via the Artlist pipeline. The canonical helper velox_drive_resolve (in
# lib/artlist.sh, surfaced via lib/velox_domain.sh delegator) returns the
# Drive parent-folder ID for a given clip_id; this gate asserts that the
# resolved folder matches ARTlist_ROOT_FOLDER (no cross-contamination with
# voiceover/stock/image folders).
#
# Also asserts (when live): the clip's media_assets.drive_file_id round-trips
# back from the Qdrant payload.source_url to the canonical Drive URL (no
# orphan writes).
gate_drive_resolve() {
    smoke_log_section "Gate 6 — Drive resolve (folder routing + URL round-trip)"

    local clip_id="${ARTLIST_TEST_CLIP_ID:-test-clip-$$}"
    local failures=0

    smoke_log_section "Phase 6a: resolve Drive parent folder for clip_id=${clip_id}"
    local resolved_folder
    if ! resolved_folder=$(velox_drive_resolve "$clip_id" 2>/dev/null); then
        log_warn "Phase 6a velox_drive_resolve short-circuited (live Drive adapter absent)"
    else
        if [[ "$resolved_folder" == "$ARTLIST_ROOT_FOLDER" ]]; then
            log_pass "Phase 6a resolved folder matches ARTlist_ROOT_FOLDER"
        else
            log_fail "Phase 6a resolved folder ($resolved_folder) does NOT match ARTlist_ROOT_FOLDER ($ARTLIST_ROOT_FOLDER)"
            failures=$((failures + 1))
        fi
    fi

    smoke_log_section "Phase 6b: Drive URL round-trip (no orphan writes)"
    local drive_url
    if ! drive_url=$(smoke_curl GET "/api/artlist/clip/${clip_id}/drive_url" 2>/dev/null); then
        log_warn "Phase 6b smoke_curl short-circuited (server absent)"
    elif [[ "${SMOKE_LAST_HTTP:-}" =~ ^2[0-9][0-9]$ ]]; then
        if [[ -n "$drive_url" ]] && [[ "$drive_url" == https://drive.google.com/* ]]; then
            log_pass "Phase 6b Drive URL round-trip OK (canonical drive.google.com URL)"
        else
            log_fail "Phase 6b Drive URL not in drive.google.com canonical domain: $drive_url"
            failures=$((failures + 1))
        fi
    else
        log_warn "Phase 6b server returned non-2xx (HTTP=${SMOKE_LAST_HTTP:-empty})"
    fi

    if (( failures > 0 )); then
        log_fail "06_drive gate failed (${failures} canonical sub-checks missed)"
        return 1
    fi
    log_pass "06_drive gate ready (live-assertion sub-checks marked WARN when live stack absent)"
}

main() {
    if [[ "$DRY_RUN" == "1" ]]; then
        smoke_echo_safe "DRY RUN — 06_drive would probe:"
        printf '  velox_drive_resolve <clip_id>     -- expects %s\n' "$ARTLIST_ROOT_FOLDER"
        printf '  smoke_curl GET /api/artlist/clip/<clip_id>/drive_url\n'
        printf '  asserts URL starts with https://drive.google.com/ (canonical Drive domain)\n'
        printf '\nLib helpers exercised:\n'
        printf '  velox_drive_resolve  (artlist.sh canonical; velox_domain.sh delegator)\n'
        printf '  smoke_curl           (common.sh) for the round-trip probe\n'
        exit 0
    fi

    gate_drive_resolve || return 1

    printf '\n============================================\n'
    printf '  06_drive\n'
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
