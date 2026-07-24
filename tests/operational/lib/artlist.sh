#!/usr/bin/env bash
# tests/operational/lib/artlist.sh — Artlist-battery application-side helpers.
#
# Source-able library. Every arlist/*.sh sub-script does:
#   DIR=$(cd "$(dirname "$0")" && pwd)
#   # shellcheck disable=SC1091
#   source "$DIR/../lib/common.sh"
#   # shellcheck disable=SC1091
#   source "$DIR/../lib/artlist.sh"
#   # shellcheck disable=SC1091
#   source "$DIR/../lib/artlist_runtime.sh"
#
# Contract (July 2026, post-verify-* split):
#   - Exposes artlist_search_live / artlist_detail / artlist_download /
#     artlist_enqueue_run / artlist_poll_run. Each is a THIN STUB as of
#     this commit; full implementations ship in subsequent followups.
#   - Inherits BASE_URL / SCRAPER_URL / SMOKE_TOKEN / smoke_curl /
#     SMOKE_LAST_HTTP / SMOKE_LAST_BODY / DRY_RUN from common.sh
#     (sourced first by every arlist sub-script).
#
# Stub semantics (per AGENTS.md "fail closed" rule):
#   - Each helper validates its required args; missing args -> exit 2
#     with a [FATAL] stderr line.
#   - Under SMOKE_DRY_RUN=1: silently returns success (no [STUB] noise)
#     so dev dry-runs of `make smoke-dry` aren't spammed by stub markers.
#   - Under non-dry-run: emits a [STUB] stderr line + return 1.
#     Callers see rc=1 + an auditable log marker; never a silent no-op.

set -euo pipefail

# ── ANSI color defaults (defensive under `set -u` in dry-source) ─────────
# Sub-scripts source common.sh first which exports $RED / $YELLOW /
# $RESET. Stub libs may also be sourced in isolation (e.g. by a future
# regression net); defaulting here keeps the printf lines below from
# tripping set -u on $YELLOW-/- $RESET with no common.sh ancestor.
: "${YELLOW:=\033[33m}"
: "${RESET:=\033[0m}"
: "${RED:=\033[31m}"

# ── Internal arg-count guard (fail-closed per AGENTS.md) ──────────────────
# Usage: artlist_required_args EXPECTED_COUNT "$@"
# Iterates "$@" (without EXPECTED_COUNT arg) and counts non-empty entries.
# If got < need: emits [FATAL] to stderr + exit 2 (canonical setup-error).
artlist_required_args() {
    local need=$1; shift
    local got=0
    local _arg
    for _arg in "$@"; do
        [[ -n "$_arg" ]] && got=$((got + 1))
    done
    if (( got < need )); then
        printf '%s[FATAL]%s artlist lib: %d required arg(s), got %d\n' \
            "$RED" "$RESET" "$need" "$got" >&2
        exit 2
    fi
}

# ── artlist_search_live — GET /api/artlist/search/live wrapper ──────────
# artlist_search_live TERM [LIMIT] OUT
#   TERM    search term to POST against /api/artlist/search/live
#   LIMIT   (optional) max-clip count (default 5)
#   OUT     absolute path of the response body file (always written)
# Returns HTTP code on stdout (canonical smoke_curl contract).
artlist_search_live() {
    artlist_required_args 2 "$@"
    local term="$1" limit="${2:-5}" out="${3:-${WORK_DIR:-/tmp}/last.body}"
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        : > "$out"
        printf '%s\n' "0"
        return 0
    fi
    printf '%s[STUB]%s artlist_search_live(term=%q, limit=%s, out=%q) — not yet implemented\n' \
        "$YELLOW" "$RESET" "$term" "$limit" "$out" >&2
    : > "$out"
    return 1
}

# ── artlist_detail — POST /detail (or scraper /detail) wrapper ──────────
# artlist_detail CLIP_PAGE_URL [OUT]
#   CLIP_PAGE_URL  artlist.io stock-footage URL
#   OUT            (optional) response body file (default $WORK_DIR/last.body)
# Returns HTTP code on stdout.
artlist_detail() {
    artlist_required_args 1 "$@"
    local url="$1" out="${2:-${WORK_DIR:-/tmp}/last.body}"
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        : > "$out"
        printf '%s\n' "0"
        return 0
    fi
    printf '%s[STUB]%s artlist_detail(url=%q, out=%q) — not yet implemented\n' \
        "$YELLOW" "$RESET" "$url" "$out" >&2
    : > "$out"
    return 1
}

# ── artlist_download — POST /download (or scraper /download) wrapper ────
# artlist_download CLIP_PAGE_URL OUTPUT_DIR OUT
#   CLIP_PAGE_URL  source URL
#   OUTPUT_DIR     directory where the scraper writes the local artifact
#   OUT            response body file (mandatory — caller wants the json)
# Returns HTTP code on stdout. The download itself is an out-of-band
# side-effect performed by $SCRAPER_URL, the response JSON acknowledges.
artlist_download() {
    artlist_required_args 3 "$@"
    local url="$1" out_dir="$2" out="$3"
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        : > "$out"
        printf '%s\n' "0"
        return 0
    fi
    printf '%s[STUB]%s artlist_download(url=%q, out_dir=%q, out=%q) — not yet implemented\n' \
        "$YELLOW" "$RESET" "$url" "$out_dir" "$out" >&2
    : > "$out"
    return 1
}

# ── artlist_enqueue_run — POST /api/artlist/run (returns run_id) ─────────
# artlist_enqueue_run TERM LIMIT [IDEMPOTENCY_KEY]
#   TERM              search term
#   LIMIT             max-clip count (default 3, matches DoD Gate 4)
#   IDEMPOTENCY_KEY   (optional, defaults to smoke_gen_uuid from common.sh)
# Writes response body to $WORK_DIR/last.body. Returns RUN_ID on stdout.
artlist_enqueue_run() {
    artlist_required_args 2 "$@"
    local term="$1" limit="${2:-3}" idem="${3:-$(smoke_gen_uuid 2>/dev/null || echo stub-key)}"
    local out="${WORK_DIR:-/tmp}/last.body"
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        : > "$out"
        printf '%s\n' "stub-run-id"
        return 0
    fi
    printf '%s[STUB]%s artlist_enqueue_run(term=%q, limit=%s, idem=%q) — not yet implemented\n' \
        "$YELLOW" "$RESET" "$term" "$limit" "$idem" >&2
    : > "$out"
    return 1
}

# ── artlist_poll_run — poll RUN_ID until terminal ───────────────────────
# artlist_poll_run RUN_ID
#   RUN_ID  ID returned by artlist_enqueue_run
# Returns 0 on terminal (completed / failed / cancelled / dead_letter),
# 124 on poll timeout per common.sh convention.
artlist_poll_run() {
    artlist_required_args 1 "$@"
    local run_id="$1"
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        return 0
    fi
    printf '%s[STUB]%s artlist_poll_run(run_id=%q) — not yet implemented\n' \
        "$YELLOW" "$RESET" "$run_id" >&2
    return 1
}
