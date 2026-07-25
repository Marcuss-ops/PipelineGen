#!/usr/bin/env bash
# tests/operational/artlist/05_pipeline_fresh.sh — Artlist DoD Gates 4 + 5 (fresh run 3/3 + per-clip validation).
#
# Reorg (July 2026): split out of tests/operational/artlist_e2e.sh (now a thin shim).
# Bundles two gates that exercise the same operational surface (an end-to-end
# /api/artlist/run cycle):
#   Gate 4 — first fresh run (3/3 SUCCEEDED, failed=0, no RETRY_WAIT)
#   Gate 5 — per-clip DB + local file validation
#
# Both gates are currently STUBS in the monolithic; the next PR implements
# them via lib/artlist.sh::artlist_enqueue_run + artlist_poll_run, then walks
# the resulting clip_ids through lib/velox_domain.sh::velox_artlist_pipeline_run
# for Gate 5. This sub-script just declares the surface so make verify-artlist-pipeline
# has a parseable target.

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/../lib/common.sh"
# shellcheck disable=SC1091
source "$DIR/../lib/artlist.sh"
# shellcheck disable=SC1091
source "$DIR/../lib/artlist_runtime.sh"
# Future: source lib/velox_domain.sh once Gate 5 implementation lands.



smoke_require curl jq



# ── Gate 4 — first fresh run 3/3 ────────────────────────────────────────
# Spec (July 2026 DoD, artlist-gates.md row 4):
#   - precondition: artlist_runs has NO prior matching row for the canonical
#     (term=$ARGLIST_TERM-NORMALIZED, root_folder_id=$ARTLIST_ROOT_FOLDER)
#     signature. The persisted term is the NORMALIZED form (post-
#     normalizeSearchTerm: trim + collapse whitespace + cap at 6 words —
#     see types.go::RunTagRequest.NormalizeRunTagRequest); the precondition
#     SQL mirrors that normalization on the bash side so the query matches
#     what the orchestrator's RunDedupKey == idempotency.BuildKey actually
#     persisted (reviewer hardening note 1).
#   - POST /api/artlist/run with the canonical 9-field body verbatim
#     (term, limit=3, strategy=replace, clip_duration=7, width=1920,
#     height=1080, fps=30, concurrency=1, dry_run=false). The body literal
#     is built via `jq -nc --arg term` so JSON is shell-safe even when
#     $ARTLIST_TERM contains '"' or '\' (reviewer hardening note 2).
#   - poll the worker via /api/jobs/<run_id>/full until terminal
#     (smoke_poll_terminal covers QUEUED/RUNNING/RETRY_WAIT → SUCCEEDED/
#     FAILED/CANCELLED/DEAD_LETTER; smoke_poll_terminal NEVER returns
#     0 on RETRY_WAIT, so an infinite-retry run auto-fails Gate 4).
#   - the 8 hard invariants (DoD-verbatim):
#       inv-0 precondition: artlist_runs row count for normalized term == 0
#       inv-1 POST /api/artlist/run returns HTTP 200 or 202
#       inv-2 run_id present in POST response body
#       inv-3 GET /api/artlist/runs/<run_id> status in {SUCCEEDED, completed}
#             (PARTIALLY_SUCCEEDED / RETRY_WAIT / RUNNING / QUEUED / FAILED
#              / CANCELLED / DEAD_LETTER all fail-closed; the default
#              branch also rejects lowercase/unknown variants per the
#              job.Status uppercase SSOT — reviewer hardening note 3)
#       inv-4 .items length exactly 3
#       inv-5 zero items with status startswith "blocked_"
#             (Fase 6 typed-error block surface per types.go::RunTagItem
#              canonical Status enum: blocked_mode / blocked_daily_limit /
#              blocked_unauthorized / blocked_session_expired)
#       inv-6 .processed == 3
#       inv-7 .failed == 0
#       inv-8 jobs ledger has zero RETRY_WAIT rows for the run_id
#             (belt-and-braces for "no infinite backoff" — inv-3 at the
#              RunTagResponse level + inv-8 at the jobs ledger level both
#              guard against RETRY_WAIT persistence in any future corner).
# Reuses only helpers from lib/{common.sh,artlist.sh,artlist_runtime.sh}
# (smoke_curl / smoke_poll_terminal / smoke_sqlite_query / log_*). No
# duplicate decision logic (AGENTS.md single-focus rule).
gate_fresh_run_three() {
    local term term_norm run_body words
    term="${ARTLIST_TERM}"
    # ── Normalize term to mirror artlist.NormalizeRunTagRequest →
    # normalizeSearchTerm (trim + collapse whitespace + cap at 6 words).
    # Reviewer hardening note 1: artlist_runs.term is the NORMALIZED
    # term, not the raw $ARTLIST_TERM. A precondition query by RAW
    # input would silently miss a prior normalized row when the
    # operator supplies extra spaces, leading/trailing whitespace,
    # or >6 words — letting inv-0 wrongly PASS while the orchestrator's
    # RunDedupKey (which IS normalized via normalizeSearchTermLower)
    # dedup-merges the new run with the stale row.
    term_norm="$(printf '%s' "${term}" | tr -s ' \t\n' ' ')"
    term_norm="${term_norm#"${term_norm%%[![:space:]]*}"}"  # trim leading
    term_norm="${term_norm%"${term_norm##*[![:space:]]}"}"  # trim trailing
    read -ra words <<<"${term_norm}"
    if (( ${#words[@]} > 6 )); then
        # Reviewer hard-nit 2: pin join char on IFS to be immune to
        # upstream IFS drift. `${words[*]:0:6}` joins on IFS[0] at slice
        # time, not at read time; if a future caller sources any lib
        # that has mutated IFS globally, the join character would silently
        # change. `local IFS=' '` is local to this command and forces the
        # join to single space regardless of caller scope.
        local IFS=' '
        term_norm="${words[*]:0:6}"  # bash array slice (single-spaced)
    fi

    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        smoke_echo_safe "[DRY] Gate 4 inv-0 precondition: SELECT COUNT(*) FROM artlist_runs WHERE term='${term_norm}'"
        smoke_echo_safe "[DRY] Gate 4 inv-1 POST ${BASE_URL}/api/artlist/run ← canonical 9-field JSON body (term='${term}', normalized='${term_norm}')"
        smoke_echo_safe "[DRY] Gate 4 inv-2 extract .run_id from POST response"
        smoke_echo_safe "[DRY] Gate 4 polling /api/jobs/<run_id>/full until terminal (smoke_poll_terminal)"
        smoke_echo_safe "[DRY] Gate 4 inv-3..7 GET /api/artlist/runs/<run_id> → 5 jq invariants (status/items/blocked/processed/failed)"
        smoke_echo_safe "[DRY] Gate 4 inv-8 SELECT COUNT(*) FROM jobs WHERE id='<run_id>' AND status='RETRY_WAIT'"
        return 0
    fi

    smoke_log_section "Gate 4 — first fresh run 3/3"

    # inv-0: precondition — no prior matching artlist_runs row (term_norm!)
    local prior_count
    prior_count=$(smoke_sqlite_query "$DB_PATH" \
        "SELECT COUNT(*) FROM artlist_runs WHERE term='${term_norm}'") || prior_count="?"
    if [[ "$prior_count" != "0" ]]; then
        log_fail "Gate 4 inv-0 precondition: artlist_runs has ${prior_count} matching row(s) for normalized term='${term_norm}' (raw input='${term}'); DoD forbids stale row on a fresh install — wipe artlist_runs or pick a different term"
        return 1
    fi
    log_pass "Gate 4 inv-0 precondition: artlist_runs clean (0 matching rows for normalized term='${term_norm}')"

    # inv-1: POST /api/artlist/run → HTTP 2xx.
    # Body is built via jq --arg so JSON is shell-safe regardless of
    # operator-supplied $term containing '"' or '\' (reviewer hardening
    # note 2: bash single-quote concatenation breaks JSON on those
    # inputs; jq is the canonical JSON builder per AGENTS.md).
    run_body=$(jq -nc \
        --arg term "${term}" \
        '{term:$term,limit:3,strategy:"replace",clip_duration:7,width:1920,height:1080,fps:30,concurrency:1,dry_run:false}')
    local http_code
    http_code=$(smoke_curl POST "/api/artlist/run" -d "$run_body")
    if [[ ! "$http_code" =~ ^2[0-9][0-9]$ ]]; then
        log_fail "Gate 4 inv-1 POST /api/artlist/run returned HTTP ${http_code} (DoD expects 200 or 202)"
        return 1
    fi
    log_pass "Gate 4 inv-1 POST /api/artlist/run HTTP ${http_code}"

    # inv-2: run_id present in POST response body
    local run_id
    # Reviewer hard-nit 1: `set -e` × `local var=$(jq …)` aborts the
    # script BEFORE any `log_fail` line fires if jq exits non-zero
    # (malformed JSON, missing $SMOKE_LAST_BODY, file unreadable). The
    # `|| echo ""` guarantees the inner command substitution always
    # succeeds so the downstream inv-2 fail-closed check produces the
    # canonical [FAIL] line. AGENTS.md "fail-closed" requires the
    # [FAIL] marker, not a process abort.
    run_id=$(jq -r '.run_id // empty' "$SMOKE_LAST_BODY" || echo "")
    if [[ -z "$run_id" ]]; then
        log_fail "Gate 4 inv-2 .run_id missing in POST response body"
        return 1
    fi
    log_pass "Gate 4 inv-2 .run_id=${run_id} present"

    # Poll the worker for a terminal status (smoke_poll_terminal uses
    # /api/jobs/<id>/full; returns 124 on timeout, non-zero on the last
    # status being non-terminal — both suffice to fail Gate 4).
    log_info "Gate 4 polling /api/jobs/${run_id}/full for terminal status..."
    if ! smoke_poll_terminal "${run_id}"; then
        log_fail "Gate 4 polling never reached terminal (last status=${SMOKE_LAST_STATUS:-?}); RETRY_WAIT or 124 timeout rejected by DoD"
        return 1
    fi
    log_pass "Gate 4 worker reached terminal status=${SMOKE_LAST_STATUS}"

    # Re-fetch the finalized RunTagResponse from /api/artlist/runs/<run_id>
    http_code=$(smoke_curl GET "/api/artlist/runs/${run_id}")
    if [[ "$http_code" != "200" ]]; then
        log_fail "Gate 4 inv-3 GET /api/artlist/runs/${run_id} returned HTTP ${http_code}"
        return 1
    fi
    # ${SMOKE_LAST_BODY} points at ${WORK_DIR}/last.body and is stable
    # across the remaining inv-3..inv-7 reads (no more smoke_curl calls
    # in between, so the body is held verbatim until jq reads it).
    # Reviewer hard-nit 3: drop the redundant `cp` snapshot — it was
    # a no-op since ${SMOKE_LAST_BODY} IS already the canonical handoff
    # file ($WORK_DIR/last.body). Per AGENTS.md "Simplicity &
    # Minimalism" — avoid changes that don't add behaviour.

    # inv-3: terminal status ∈ {SUCCEEDED, completed}
    # Fail-closed on PARTIALLY_SUCCEEDED / RETRY_WAIT / RUNNING / QUEUED /
    # FAILED / CANCELLED / DEAD_LETTER per DoD: PARTIAL_SUCCESS forbidden,
    # RETRY_WAIT forbidden (the jobs-ledger inv-8 is a belt-and-braces
    # companion that surfaces it even if the orchestrator ever returned
    # status=RETRY_WAIT before the ledger catches up).
    local run_status
    # See reviewer hard-nit 1 (set -e × jq guard).
    run_status=$(jq -r '.status // empty' "$SMOKE_LAST_BODY" || echo "")
    case "${run_status}" in
        SUCCEEDED|completed)
            log_pass "Gate 4 inv-3 status=${run_status} (terminal: success)"
            ;;
        PARTIALLY_SUCCEEDED|RETRY_WAIT|RUNNING|QUEUED|FAILED|CANCELLED|DEAD_LETTER)
            log_fail "Gate 4 inv-3 status='${run_status}' explicitly rejected by DoD (PARTIAL/RETRY/RUNNING/FAIL/CANCEL)"
            return 1
            ;;
        *)
            # Fail-closed default per job.Status SSOT (uppercase only).
            # Anything not in {SUCCEEDED, completed} is rejected —
            # including lowercase variants like "succeeded" (canonical
            # SSOT is uppercase per kernel/job/job.go::Status constants),
            # unknown aliases, unrecognised strings, or a future status
            # enum not yet canonicalised. "completed" is the ONE accepted
            # legacy lowercase alias because older artlist_runs rows use
            # it (types.go::RunTagResponse .status field shape; the
            # orchestrator's status writer sets the canonical uppercase
            # string per jobs.Service.IsTerminal contract). Reviewer
            # hardening note 3: explicit SSOT comment per godlike/06.
            log_fail "Gate 4 inv-3 status='${run_status}' not in {SUCCEEDED, completed} (default branch fails closed per job.Status SSOT)"
            return 1
            ;;
    esac

    # inv-4: exactly 3 items in the response
    # See reviewer hard-nit 1 (set -e × jq guard).
    local item_count
    item_count=$(jq -r '(.items // []) | length' "$SMOKE_LAST_BODY" || echo "-1")
    if [[ "${item_count}" != "3" ]]; then
        log_fail "Gate 4 inv-4 expected exactly 3 items; got ${item_count}"
        return 1
    fi
    log_pass "Gate 4 inv-4 items count=3"

    # inv-5: zero items with status startswith "blocked_"
    # Fase 6 typed-error block (types.go::RunTagItem commented enum)
    # covers blocked_mode / blocked_daily_limit / blocked_unauthorized /
    # blocked_session_expired. All four hard-fail Gate 4.
    # See reviewer hard-nit 1 (set -e × jq guard) — AND use the
    # defensive `.items // [] | .[]` pattern so a null .items (eg
    # a bare-bones RunTagResponse without an items[] key) doesn't
    # trigger a `Cannot iterate over null` runtime error that would
    # also abort under set -uo pipefail before the [FAIL] marker.
    local blocked_count
    blocked_count=$(jq -r '[.items // [] | .[] | select((.status // "") | startswith("blocked_"))] | length' "$SMOKE_LAST_BODY" || echo "-1")
    if [[ "${blocked_count}" != "0" ]]; then
        log_fail "Gate 4 inv-5 found ${blocked_count} item(s) with blocked_* status (DoD forbids)"
        return 1
    fi
    log_pass "Gate 4 inv-5 zero blocked_* items"

    # inv-6: .processed == 3
    # See reviewer hard-nit 1 (set -e × jq guard). The `// -1` jq-level
    # default already covers the missing-field case; the outer
    # `|| echo "-1"` is the belt-and-braces for jq itself aborting.
    local processed
    processed=$(jq -r '.processed // -1' "$SMOKE_LAST_BODY" || echo "-1")
    if [[ "${processed}" != "3" ]]; then
        log_fail "Gate 4 inv-6 processed=${processed} expected 3 (DoD: found-3 processed)"
        return 1
    fi
    log_pass "Gate 4 inv-6 processed=3"

    # inv-7: .failed == 0
    # See reviewer hard-nit 1 (set -e × jq guard).
    local failed
    failed=$(jq -r '.failed // -1' "$SMOKE_LAST_BODY" || echo "-1")
    if [[ "${failed}" != "0" ]]; then
        log_fail "Gate 4 inv-7 failed=${failed} expected 0 (PARTIAL-equivalent rejected)"
        return 1
    fi
    log_pass "Gate 4 inv-7 failed=0"

    # inv-8: jobs ledger has zero RETRY_WAIT rows for the run_id
    # Belt-and-braces for "no infinite backoff" — even if Gate 4 inv-3
    # somehow let a RETRY_WAIT slip past, the dedicated ledger check
    # surfaces it as a hard fail. The run_id IS the job id (enqueued
    # via h.jobsService.Enqueue in artlist_handlers.go::enqueueArtlistRun).
    local retry_wait_count
    retry_wait_count=$(smoke_sqlite_query "$DB_PATH" \
        "SELECT COUNT(*) FROM jobs WHERE id='${run_id}' AND status='RETRY_WAIT'") || retry_wait_count="?"
    if [[ "${retry_wait_count}" != "0" ]]; then
        log_fail "Gate 4 inv-8 jobs ledger has ${retry_wait_count} RETRY_WAIT row for run id=${run_id} (DoD forbids infinite backoff)"
        return 1
    fi
    log_pass "Gate 4 inv-8 jobs ledger clean (zero RETRY_WAIT for run id=${run_id})"

    return 0
}

# ── Gate 5 — per-clip DB + file validation ──────────────────────────────
# Spec (July 2026 DoD):
#   - for each clip_id from Gate 4:
#       sqlite3 read on $DB_PATH for SELECT * FROM assets WHERE id = clip_id
#       file exists at the assets.local_path
#       file size > 0
#       local file MIME == video/mp4 (sample first clip, optional)
gate_per_clip_validation() {
    smoke_log_section "Gate 5 — per-clip DB + file validation"
    log_info "[STUB] Gate 5 — implement next"
}

main() {
    if [[ "$DRY_RUN" == "1" ]]; then
        smoke_echo_safe "DRY RUN — pipeline fresh probes (Gates 4 + 5):"
        printf '  POST %s/api/artlist/run term=<ARTLIST_TERM> limit=3 (Gate 4)\n' "$BASE_URL"
        printf '  poll run_id until terminal (Gate 4)\n'
        printf '  SELECT * FROM assets WHERE id IN (Gate 5)\n'
        exit 0
    fi
    gate_fresh_run_three || return 1
    gate_per_clip_validation || return 1

    printf '\n============================================\n'
    printf '  05_pipeline_fresh (Gates 4 + 5)\n'
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
