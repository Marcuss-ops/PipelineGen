#!/usr/bin/env bash
# tests/operational/artlist/05_pipeline_fresh.sh — Artlist DoD Gates 4 + 5 (fresh run 3/3 + per-clip validation).
#
# Reorg (July 2026): split out of tests/operational/artlist_e2e.sh (now a thin shim).
# Bundles FIVE gates that exercise the same operational surface (an end-to-end
# /api/artlist/run cycle + per-clip side-effect verification):
#   Gate 4 — first fresh run (3/3 SUCCEEDED, failed=0, no RETRY_WAIT)
#             writes $WORK_DIR/clip_ids.txt as the canonical Single-Source-of-
#             Truth hand-off for downstream gates within the same script
#             invocation.
#   Gate 5 — per-clip DB + local file validation (smoke_sqlite_query -json +
#             smoke_ffprobe_check + inline ffprobe for codec/container)
#   Gate 6 — Drive resolve per clip (velox_drive_resolve for canonical shape
#             contract + INLINE jq parent-folder membership + INLINE curl HEAD
#             probe for the "link apribile" requirement)
#   Gate 7 — SQLite + outbox integrity per clip (smoke_sqlite_query for
#             media_assets count + file_hash coherence + asset_locations
#             dual-presence + outbox COMPLETED+SUPERSEDED with no DEAD_LETTER;
#             smoke_outbox_chain_verify diagnostic table at end)
#   Gate 8 — Qdrant + media search hard gate (velox_qdrant_assert for per-
#             clip payload shape + collection existence + smoke_curl POST
#             /api/media_search with sources=[artlist] recouping ≥1 of the 3
#             clip_ids per $ARTLIST_TERM — promoted from warning to HARD gate)
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
# shellcheck disable=SC1091
source "$DIR/../lib/velox_domain.sh"  # velox_qdrant_assert + velox_drive_resolve + velox_artlist_pipeline_run



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

    # ── Hand-off (Gate 4 → Gate 5): write 3 clip_ids to ${WORK_DIR}/clip_ids.txt
    # as the canonical Single Source of Truth at the gate boundary. At this
    # point ${SMOKE_LAST_BODY} holds the finalized ${BASE_URL}/api/artlist/
    # runs/${run_id} response body, which carries the .items[].clip_id[] array.
    # Per AGENTS.md godlike/06 SSOT (one canonical owner per fact at any
    # boundary) — this file is consumed ONLY by gate_per_clip_validation; never
    # re-derived via a second HTTP round-trip.
    #
    # Reviewer hard-nit 1 (set -e × jq guard) reapplied under the '|| :' form:
    # jq exits non-zero under malformed JSON; the '|| :' swallows any exit
    # code so the bash side can fail-closed on clip_ids.txt emptiness via
    # the explicit `[[ -s … ]]` check below (also matches AGENTS.md "fail
    # closed" — never let the script abort silently).
    jq -r '.items[]?.clip_id // empty' "$SMOKE_LAST_BODY" \
        > "$WORK_DIR/clip_ids.txt" 2>/dev/null || :
    local clip_count
    clip_count=$(wc -l < "$WORK_DIR/clip_ids.txt" 2>/dev/null | tr -d ' ' || echo 0)
    if [[ "$clip_count" -lt 1 ]]; then
        # The response was 2xx + items.length=3 + no retry_wait, yet
        # clip_id extraction produced zero rows. Treat that as a contract
        # drift in /api/artlist/runs/<run_id> rather than silently passing
        # through to Gate 5.
        log_fail "Gate 4 hand-off: ${WORK_DIR}/clip_ids.txt empty — /api/artlist/runs/${run_id} returned items.length=3 but clip_id extraction produced 0 rows (response contract drift)"
        return 1
    fi
    log_info "Gate 4 hand-off: ${WORK_DIR}/clip_ids.txt written with ${clip_count} clip_id(s) for Gate 5 consumption"

    # ── Hand-off (Gate 4 → Gate 8): also write the NORMALIZED term used
    # to surface the 3 clips. Gate 8's semantic reconciliation
    # (/api/media/search with sources=[artlist]) MUST query the SAME
    # term that the orchestrator indexed against — otherwise raw
    # $ARTLIST_TERM with extra whitespace or >6 words (which Gate 4
    # normalizes via artlist.NormalizeRunTagRequest →
    # normalizeSearchTerm: trim + collapse whitespace + cap at 6 words)
    # would search for an un-normalized variant and fail recoup for an
    # unrelated reason. godlike/06 SSOT (one canonical hand-off per
    # boundary) — parallels the existing ${WORK_DIR}/clip_ids.txt
    # pattern above for downstream gates within the same script.
    printf '%s' "${term_norm}" > "${WORK_DIR}/gate4_norm_term.txt" 2>/dev/null || :
    log_info "Gate 4 hand-off: ${WORK_DIR}/gate4_norm_term.txt written with normalized term='${term_norm}' for Gate 8 semantic recovery reuse"

    return 0
}

# ── Gate 5 — per-clip DB + file validation ──────────────────────────────
# Spec (July 2026 DoD, artlist-gates.md row 5):
#   - hand-off: Gate 4 wrote ${WORK_DIR}/clip_ids.txt with 3 clip_ids
#     following godlike/06 SSOT (one canonical owner per fact at any
#     boundary). Gate 5 reads that file — no second HTTP round-trip
#     to /api/artlist/runs/<run_id>.
#   - per clip_id, the **14 canonical media_assets fields** must satisfy:
#       source=artlist                            media_type=video
#       lifecycle_state=PUBLISHED                 index_state=INDEXED
#       ((end_ms - start_ms) / 1000.0) ∈ [6.5, 8.5]  width=1920  height=1080
#       file_hash     present    source_provider present    source_version present
#       metadata_origin=artlist
#       provider_tags       non-empty []   provider_categories   non-empty []
#       discovered_by_queries non-empty []
#   - per clip_id, the **4 canonical drive-side columns** must be present:
#       drive_file_id  present
#       drive_link     present
#       download_link  present
#       local_path     present  (file-validation anchor)
#   - per clip_id, the **local file** at media_assets.local_path must:
#       exist + size > 0 + ffprobe-readable with duration > 0, video
#         stream width > 0, height > 0 (smoke_ffprobe_check DoD-exact
#         contract, reused verbatim from Gate 2 — AGENTS.md no duplicate
#         decision logic across handlers)
#       container = mp4 family (.format.format_name matches mp4|mov|m4a)
#       first video stream codec_name = 'h264' (DoD MIME video/mp4 mapped
#         to the canonical ffprobe fields)
#
# Schema SSOT (verify in migrations/sqlite/*.sql):
#   - lifecycle_state : migrations 094/101 (canonical column, NOT lifecycle_status)
#   - index_state      : migration 094 (canonical column)
#   - width/height     : migration 068 (canonical direct columns)
#   - source_provider  : migration 152 (canonical column)
#   - source_version   : canonical Asset.struct field per
#                        tests/e2e/canonical_surfaces_e2e_test.go L478
#   - start_ms/end_ms  : migrations 152 (duration math = end_ms − start_ms)
#   - drive_file_id / drive_link / download_link / local_path :
#                       legacy per migration 055 comment ("remain for
#                       backward compatibility") — still populated today
#   - metadata_json.$.{metadata_origin, provider_tags, provider_categories,
#                       discovered_by_queries} :
#                       INSIDE metadata_json per migration 173 + search_core.go
#                       pre-cutover (the cutover is a future followup PR,
#                       not in Gate 5 scope)
#
# Reuses ONLY helpers from lib/{common.sh,artlist.sh,artlist_runtime.sh,
# velox_domain.sh} (smoke_sqlite_query -json + smoke_ffprobe_check +
# log_pass/log_fail/log_info + jq -e composite check + inline ffprobe
# mirroring Gate 2 flag set). No new helpers introduced. AGENTS.md
# single-focus rule.
gate_per_clip_validation() {
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        smoke_echo_safe "[DRY] Gate 5 per-clip DB + file validation:"
        smoke_echo_safe "[DRY]   - hand-off consumed from ${WORK_DIR}/clip_ids.txt (3 clip_ids)"
        smoke_echo_safe "[DRY]   - per clip_id: smoke_sqlite_query -json SELECT (14 canonical fields + 3 metadata_json keys + 4 legacy drive cols)"
        smoke_echo_safe "[DRY]   - jq -e composite check (Source=artlist MediaType=video Life=PUBLISHED Index=INDEXED Duration 6.5-8.5s 1920x1080 ProviderFieldsPresent MetadataOrigin=artlist ProviderTags+Cats+DQs non-empty DriveColumnsPresent)"
        smoke_echo_safe "[DRY]   - smoke_ffprobe_check on local_path (size+dur+w+h)"
        smoke_echo_safe "[DRY]   - inline ffprobe: codec_name=='h264' AND format.format_name matches mp4|mov|m4a"
        return 0
    fi

    smoke_log_section "Gate 5 — per-clip DB + file validation"

    local clip_file="${WORK_DIR}/clip_ids.txt"
    if [[ ! -s "$clip_file" ]]; then
        log_fail "Gate 5 hand-off: ${clip_file} missing or empty (Gate 4 must write 3 clip_ids before Gate 5 can run)"
        return 1
    fi

    local clip_id clip_count=0 failures=0 row_json local_path codec_json
    while read -r clip_id; do
        [[ -z "$clip_id" ]] && continue
        clip_count=$((clip_count + 1))

        # ── 1. SQLite SELECT — 14 canonical DB fields + 3 metadata_json keys +
        # 4 legacy drive cols. SQLite -json outputs ONE bare object for a
        # single-row WHERE id=? query (vs an ARRAY for multi-row). The jq
        # composite check below handles BOTH shapes via the `if type ==
        # "array"` defensive branch (handle no-row `[]` response too —
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

        # ── 2. Composite shape check (18 invariants in ONE jq expression).
        # Set -e × jq guard (reviewer hard-nit 1): if jq exits non-zero the
        # entire match fails closed by surrounding the success path with
        # `if ! jq -e …; then log_fail … fi`.
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
            # Forensic dump (token-redacted via smoke_echo_safe) for operator triage.
            smoke_echo_safe "$(printf '%s' "${row_json}" | jq -c '.' 2>/dev/null || echo '{}')" >&2
            failures=$((failures + 1))
            continue
        fi
        log_pass "Gate 5 DB-fields: all 18 invariants OK for clip_id=${clip_id}"

        # ── 3. Local file validation. smoke_ffprobe_check covers the DoD-exact
        # contract from Gate 2 verbatim: ffprobe with the canonical flag set
        # (format.duration+size, streams.codec_type+codec_name+width+height),
        # then jq -e that .format.size > 0 + .format.duration >= 0 + first
        # video stream width > 0 AND height > 0. AGENTS.md "do not duplicate
        # the same decision logic across handlers" — existing helper IS
        # reused, NOT duplicated inline.
        local_path=$(printf '%s' "${row_json}" | jq -r '.[0].local_path // .local_path // ""' 2>/dev/null || echo "")
        if [[ -z "$local_path" ]]; then
            log_fail "Gate 5 file validation: clip_id=${clip_id} has empty local_path"
            failures=$((failures + 1))
            continue
        fi
        if ! smoke_ffprobe_check "${local_path}" 0; then
            log_fail "Gate 5 file validation: smoke_ffprobe_check failed for clip_id=${clip_id} (path=${local_path})"
            failures=$((failures + 1))
            continue
        fi
        log_pass "Gate 5 file validation: smoke_ffprobe_check (size+dur+w+h) OK for clip_id=${clip_id}"

        # ── 4. Codec & container check (inline ffprobe mirroring Gate 2's
        # flag set, plus .format.format_name + .streams[].codec_name
        # assertions the DoD requires): "MIME video/mp4" mapped to:
        #   .format.format_name matches mp4|mov|m4a (ffprobe canonical
        #     container family representation)
        #   first video stream .codec_name == "h264" (DoD canonical codec)
        # Inline (no new helper) per AGENTS.md "Simplicity & Minimalism".
        codec_json="${WORK_DIR}/codec_${clip_id}.json"
        if ! ffprobe -v error -show_entries format=duration,size,format_name \
                -show_entries stream=codec_type,codec_name,width,height \
                -of json "${local_path}" > "${codec_json}" 2>/dev/null; then
            log_fail "Gate 5 inline ffprobe non-zero exit for clip_id=${clip_id} (path=${local_path})"
            failures=$((failures + 1))
            continue
        fi
        if ! jq -e '
            ((.format.format_name // "") | test("mp4|mov|m4a"))
            and ([.streams[]? | select(.codec_type=="video") | .codec_name] | any(. == "h264"))
        ' "${codec_json}" >/dev/null; then
            log_fail "Gate 5 codec/container check failed for clip_id=${clip_id} (h264 + mp4 family required by DoD)"
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
    log_pass "Gate 5 aggregate: all ${clip_count} clip(s) passed (14 DB-fields + 4 legacy drive cols + ffprobe + h264/mp4)"
    return 0
}

# ── Gate 6 — Drive resolve per clip ──────────────────────────────────────────────
# Spec (July 2026 DoD, artlist-gates.md row 6):
#   - hand-off consumption: ${WORK_DIR}/clip_ids.txt (Gate 4 wrote it on success)
#     — same-file shared $WORK_DIR inside one 05_pipeline_fresh.sh invocation.
#   - preflight: $ARTLIST_ROOT_FOLDER MUST be set (canonical configured Artlist
#     Drive root folder, sourced via lib/artlist_runtime.sh::ARTLIST_ROOT_FOLDER
#     with $VELOX_DRIVE_ARTLIST_ROOT env-override); fail-closed with the same
#     "artlist root folder not configured" typed sentinel the handler emits at
#     apiutil.BadRequest (matches handler at artlist_handlers.go::RunTagPipeline).
#   - per clip_id in ${WORK_DIR}/clip_ids.txt:
#       (1) read media_assets.drive_file_id via smoke_sqlite_query -json
#           (cross-gate contract from Gate 5 — must be non-empty)
#       (2) velox_drive_resolve "$drive_file_id" (lib/velox_domain.sh)
#           → 0 on the canonical shape contract pass (.ok AND
#             .resolved_count>=1 AND .resolved[0].trashed==false AND
#             .resolved[0].size>0); writes the body to
#             $WORK_DIR/velox_drive_${drive_file_id}.json for downstream
#             INLINE checks; 1 on contract fail; 2 on transport / HTTP non-2xx.
#       (3) INLINE jq -e check on $WORK_DIR/velox_drive_${id}.json:
#             `.resolved[0].parents // [] | any(. == $ARTLIST_ROOT_FOLDER)`
#           → file MUST live in the configured Artlist root folder.
#       (4) INLINE curl HEAD probe on `.resolved[0].webViewLink`
#           → HTTP 2xx OR 3xx (Drive often returns 302 -> accounts.google.com
#             for the public-share view; both are valid PASS per the spec's
#             "link apribile" intent). 4xx / 5xx / timeout → FAIL.
#   - ALL clips must pass (DoD forbids partial-pass — mirrors Gate 4 inv-6/7
#     spirit and Gate 5 aggregate contract).
#
# Schema SSOTs referenced:
#   - GET /api/drive/resolve-by-id : internal/api/system/handler_drive.go:106
#     (canonical response shape mirrors drive.FileMeta from
#      internal/infrastructure/drive/uploader_file.go:98 — id, name, mimeType,
#      size, webViewLink, parents[], trashed)
#   - $ARTLIST_ROOT_FOLDER : $VELOX_DRIVE_ARTLIST_ROOT (env) || ${ROOT_FOLDER_ID}
#     || '' — sourced from lib/artlist_runtime.sh.
#
# Reuses only: smoke_sqlite_query -json (lib/common.sh) + velox_drive_resolve
# (lib/velox_domain.sh) + log_pass/log_fail (lib/artlist_runtime.sh) + jq
# parent-+-link-probe inline. NO new helpers introduced. AGENTS.md single-
# focus rule honoured.
gate_drive_resolve_gate() {
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        smoke_echo_safe "[DRY] Gate 6: drive resolve per clip:"
        smoke_echo_safe "[DRY]   - hand-off consumed from ${WORK_DIR}/clip_ids.txt (3 clip_ids from Gate 4)"
        smoke_echo_safe "[DRY]   - preflight ARTLIST_ROOT_FOLDER non-empty (fail-closed otherwise)"
        smoke_echo_safe "[DRY]   - per clip_id: smoke_sqlite_query read media_assets.drive_file_id (Gate 5 cross-check)"
        smoke_echo_safe "[DRY]   - velox_drive_resolve (lib/velox_domain.sh) covers trashed/size shape contract"
        smoke_echo_safe "[DRY]   - INLINE jq -e .resolved[0].parents|any(. == \$ARTLIST_ROOT_FOLDER) for folder membership"
        smoke_echo_safe "[DRY]   - INLINE curl HEAD probe on .resolved[0].webViewLink (2xx OR 3xx = PASS)"
        return 0
    fi

    smoke_log_section "Gate 6 — Drive resolve per clip"

    # ── preflight: ARTLIST_ROOT_FOLDER must be configured (matches handler
    # typed sentinel at artlist_handlers.go::RunTagPipeline).
    if [[ -z "${ARTLIST_ROOT_FOLDER:-}" ]]; then
        log_fail "Gate 6 preflight failed: ARTLIST_ROOT_FOLDER is unset (set VELOX_DRIVE_ARTLIST_ROOT=<artlist_drive_root_id>)"
        return 1
    fi

    local clip_file="${WORK_DIR}/clip_ids.txt"
    if [[ ! -s "$clip_file" ]]; then
        log_fail "Gate 6 hand-off: ${clip_file} missing or empty (Gate 4 must write 3 clip_ids before Gate 5/6 can run)"
        return 1
    fi

    local clip_id drive_file_id drive_json web_view_link probe_code failures=0 clip_count=0
    while read -r clip_id; do
        [[ -z "$clip_id" ]] && continue
        clip_count=$((clip_count + 1))

        # ── Step 1: read media_assets.drive_file_id (Gate 5 cross-check:
        # non-empty IS the canonical invariant — if it's empty, Gate 5's
        # inv-4 'drive_file_id present' should have failed already).
        drive_file_id=$(smoke_sqlite_query "$DB_PATH" -json "
            SELECT drive_file_id FROM media_assets ma WHERE ma.id='${clip_id}'
        " 2>/dev/null | jq -r '.[0].drive_file_id // .drive_file_id // ""' 2>/dev/null || echo "")
        if [[ -z "$drive_file_id" ]]; then
            log_fail "Gate 6 step-1: clip_id=${clip_id} has NO drive_file_id in media_assets (Gate 5 inv-4 should have failed — cross-gate contract drift)"
            failures=$((failures + 1))
            continue
        fi

        # ── Step 2: velox_drive_resolve (lib/velox_domain.sh) covers the
        # canonical shape contract (ok/resolved_count/trashed/size).
        # Returns 3 distinct values that map to 3 different operator
        # actions — the diagnostic line splits them so an operator sees
        # immediately whether to chase the API contract drift (rc=1)
        # vs the auth/quota/transport layer (rc=2) vs an unexpected
        # intermediate state (rc=1+, default branch).
        if ! velox_drive_resolve "${drive_file_id}"; then
            local velox_rc=$?
            case "${velox_rc}" in
                1)
                    # Shape-jq contract fail: curl wrote the response body
                    # to ${drive_json}, the .ok/.resolved_count/.trashed/
                    # .size check returned false. Inspect ${drive_json}
                    # for the failing field. (velox_drive_resolve writes
                    # ${drive_json} BEFORE the jq check per
                    # lib/velox_domain.sh::velox_drive_resolve impl.)
                    log_fail "Gate 6 step-2: clip_id=${clip_id} (drive=${drive_file_id}) Drive SHAPE contract drift (rc=1) - jq contract returned false on .ok/.resolved_count/.trashed/.size inside ${drive_json}"
                    ;;
                2)
                    # Transport / HTTP non-2xx: curl itself failed or
                    # returned non-2xx; ${drive_json} is empty. Likely
                    # VELOX_ADMIN_TOKEN expired, Drive API quota exceeded,
                    # or curl transport error (DNS, network, SSL).
                    log_fail "Gate 6 step-2: clip_id=${clip_id} (drive=${drive_file_id}) Drive TRANSPORT/HTTP non-2xx (rc=2) - bearer expired, Drive API quota exceeded, or curl transport error - verify VELOX_ADMIN_TOKEN freshness and Drive quotas"
                    ;;
                *)
                    log_fail "Gate 6 step-2: clip_id=${clip_id} (drive=${drive_file_id}) velox_drive_resolve failed (rc=${velox_rc}) - unexpected return code from lib/velox_domain.sh"
                    ;;
            esac
            failures=$((failures + 1))
            continue
        fi

        drive_json="${WORK_DIR}/velox_drive_${drive_file_id}.json"
        if [[ ! -s "$drive_json" ]]; then
            log_fail "Gate 6 step-2: velox_drive_resolve did NOT write expected response file at ${drive_json} (lib/velox_domain.sh contract drift)"
            failures=$((failures + 1))
            continue
        fi

        # ── Step 3: parent-folder membership. ARTLIST_ROOT_FOLDER is the
        # raw Drive folder ID (NOT a URL — string exact match only). parents[]
        # MUST contain this folder ID; empty/missing parents[] fails closed.
        if ! jq -e --arg root "${ARTLIST_ROOT_FOLDER}" '
            .resolved[0].parents // [] | any(. == $root)
        ' "${drive_json}" >/dev/null 2>&1; then
            log_fail "Gate 6 step-3: clip_id=${clip_id} (drive=${drive_file_id}) NOT inside ARTLIST_ROOT_FOLDER=${ARTLIST_ROOT_FOLDER} (parents[] doesn't contain it)"
            failures=$((failures + 1))
            continue
        fi

        # ── Step 4: link-probe (link apribile). HEAD on webViewLink; 2xx OR
        # 3xx (Drive commonly returns 302 → accounts.google.com for the
        # public-share view; both are valid per DoD 'link apribile' intent).
        # 4xx / 5xx / timeout (000) → FAIL.
        web_view_link=$(jq -r '.resolved[0].webViewLink // empty' "${drive_json}" 2>/dev/null || echo "")
        if [[ -z "$web_view_link" ]]; then
            log_fail "Gate 6 step-4: clip_id=${clip_id} (drive=${drive_file_id}) missing webViewLink in Drive response"
            failures=$((failures + 1))
            continue
        fi
        probe_code=$(curl -sS --max-time 8 -o /dev/null -w '%{http_code}' -I "${web_view_link}" 2>/dev/null || echo 000)
        if [[ ! "$probe_code" =~ ^2[0-9][0-9]$ && ! "$probe_code" =~ ^3[0-9][0-9]$ ]]; then
            log_fail "Gate 6 step-4: clip_id=${clip_id} (drive=${drive_file_id}) link probe FAILED (webViewLink HTTP ${probe_code}, expected 2xx or 3xx)"
            failures=$((failures + 1))
            continue
        fi

        log_pass "Gate 6: clip_id=${clip_id} (drive=${drive_file_id}) resolved OK (parents contains ${ARTLIST_ROOT_FOLDER}, webViewLink HTTP ${probe_code})"
    done < "${clip_file}"

    # ── Aggregate verdict. DoD: ALL clips must pass — partial-pass is a
    # hard fail (mirrors Gate 4 inv-6/7 + Gate 5 aggregate contract).
    if [[ "$failures" -gt 0 ]]; then
        log_fail "Gate 6 aggregate: ${failures} clip(s) failed validation (validated ${clip_count} total — DoD requires ALL pass)"
        return 1
    fi
    log_pass "Gate 6 aggregate: all ${clip_count} clip(s) passed Drive resolve (parents in Artlist folder + webViewLink 2xx/3xx probe)"
    return 0
}

# ── Gate 7 — SQLite + outbox integrity per clip ────────────────────────
# Spec (July 2026 DoD, artlist-gates.md row 7):
#   - hand-off: Gate 4 wrote ${WORK_DIR}/clip_ids.txt (3 clip_ids);
#     Gates 5/6 already read it; Gate 7 reads it once more (same-file
#     shared ${WORK_DIR} across all 4 gates in this script invocation).
#   - 5 DoD invariants per clip_id (verdict checklist point 7):
#       inv-1: media_assets row count for clip_id == 1
#              (PROMOTED from warning to HARD gate per DoD refactor —
#               "una sola riga canonica in media_assets, no duplicati")
#       inv-2: media_assets.file_hash == sha256(local_path on disk)
#       inv-3: asset_locations row WHERE asset_id=clip_id AND
#              location_kind='local' >= 1
#       inv-4: asset_locations row WHERE asset_id=clip_id AND
#              location_kind='drive' >= 1
#       inv-5: outbox_events WHERE event_type='asset.index.requested'
#              AND aggregate_id=clip_id:
#                COMPLETED + SUPERSEDED >= 1  (terminal chain)
#                DEAD_LETTER == 0            ("nessun evento bloccato")
#                total >= 1                  (chain EXISTS at all)
#   - post-loop forensic: smoke_outbox_chain_verify called ONCE at end
#     with `|| true` to print the per-clip classification table for
#     operator triage. The helper's internal rc=1 on SUPERSEDED/PENDING
#     is STRICTER than Gate 7's DoD spec (which accepts SUPERSEDED);
#     `|| true` makes the call diagnostic-only so it never false-fails
#     the gate when our PASS surface is broader.
#
# Schema SSOT (canonical, verify in migrations/sqlite/*.sql):
#   - media_assets.id    : PRIMARY KEY (canonical per migration 085)
#                          → COUNT(*)>1 per id is catastrophic corruption.
#                          COUNT(*)==0 means Gate 4 inv-3 should have
#                          failed already; either way fail-closed.
#   - asset_locations    : (asset_id, location_kind, uri, is_primary)
#                          migration 055_ASSET_LOCATIONS.SQL — schema
#                          verified via code-searcher (migrations/sqlite)
#   - outbox_events      : (aggregate_id, event_type, status, ...)
#                          migration 092_create_outbox_events.sql —
#                          status enum: completed|pending|superseded|
#                          dead_letter (verified via code-searcher
#                          matching internal/infrastructure/database/
#                          sqlite/outboxevents/repository_write.go lines
#                          158/191/205/234 — the canonical writers).
#
# Reuses ONLY helpers from lib/common.sh + sha256sum coreutil + jq -e
# composite check + log_pass/log_fail/log_info. NO new helpers, NO
# duplicated per-clip classification logic — AGENTS.md single-focus rule.
gate_outbox_integrity_gate() {
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        smoke_echo_safe "[DRY] Gate 7: SQLite rows + asset_locations + outbox chain integrity per clip:"
        smoke_echo_safe "[DRY]   - hand-off consumed from ${WORK_DIR}/clip_ids.txt (3 clip_ids)"
        smoke_echo_safe "[DRY]   - inv-1: smoke_sqlite_query 'SELECT COUNT(*) FROM media_assets WHERE id=clip_id' -> must == 1 (promoted to hard gate)"
        smoke_echo_safe "[DRY]   - inv-2: media_assets.file_hash == sha256(local_path on disk) (sha256sum + awk '{print \$1}')"
        smoke_echo_safe "[DRY]   - inv-3: smoke_sqlite_query asset_locations COUNT WHERE asset_id=? AND location_kind='local' >= 1"
        smoke_echo_safe "[DRY]   - inv-4: smoke_sqlite_query asset_locations COUNT WHERE asset_id=? AND location_kind='drive' >= 1"
        smoke_echo_safe "[DRY]   - inv-5: per-clip inline outbox SUM aggregation -> completed+superseded>=1 AND dead_letter==0 AND total>=1 (jq -e composite)"
        smoke_echo_safe "[DRY]   - post-loop: smoke_outbox_chain_verify \$DB_PATH \$clip_file || true (forensic table only)"
        return 0
    fi

    smoke_log_section "Gate 7 — SQLite + outbox integrity per clip"

    local clip_file="${WORK_DIR}/clip_ids.txt"
    if [[ ! -s "$clip_file" ]]; then
        log_fail "Gate 7 hand-off: ${clip_file} missing or empty (Gate 4 must write 3 clip_ids before Gate 5/6/7 can run)"
        return 1
    fi

    local clip_id failures=0 clip_count=0 dupe_count row file_hash local_path sha_disk loc_local loc_drive chain_row
    while read -r clip_id; do
        [[ -z "$clip_id" ]] && continue
        clip_count=$((clip_count + 1))

        # ── clip_id integrity guard (SQL-injection defense — re-applies
        # lib/sqlite.sh::sqlite_clip_id_validate regex locally because
        # Gate 7 issues raw smoke_sqlite_query calls, NOT routed through
        # lib/sqlite.sh helpers. clip_id is consumer-supplied via
        # Gate 4's response body and lands in ${WORK_DIR}/clip_ids.txt;
        # without this regex check, a clip_id containing ' OR '1'='1
        # could escape the WHERE clause in the COUNT/MEDIAS query below.
        if ! [[ "$clip_id" =~ ^[a-zA-Z0-9_:.-]+$ ]]; then
            log_fail "Gate 7 inv-pre: clip_id='${clip_id}' fails regex ^[a-zA-Z0-9_:.-]+$ (rejecting for SQL-injection defense before any sqlite query)"
            failures=$((failures + 1))
            continue
        fi

        # ── inv-1: media_assets row count == 1 (PROMOTED to hard gate).
        # Schema: media_assets.id is PRIMARY KEY — >1 row per id is a
        # catastrophic corruption. <1 means the clip row was never
        # committed (Gate 4/5 path fragmented). Either way fail-closed.
        # Reviewer hard-nit 1 (set -e × sqlite guard): the `|| echo "?"`
        # suffix guarantees the outer `local var=$(…)` always completes
        # so the explicit `[[ != "1" ]]` fail-closed branch below fires
        # with the canonical [FAIL] line — never a silent script abort
        # under bash strict mode.
        dupe_count=$(smoke_sqlite_query "$DB_PATH" \
            "SELECT COUNT(*) FROM media_assets WHERE id='${clip_id}'" || echo "?")
        if [[ "$dupe_count" != "1" ]]; then
            log_fail "Gate 7 inv-1: clip_id=${clip_id} media_assets row count=${dupe_count} (must ==1; no-duplicate is now HARD gate per DoD refactor)"
            failures=$((failures + 1))
            continue
        fi
        log_pass "Gate 7 inv-1: clip_id=${clip_id} media_assets row count=1"

        # ── inv-2: file_hash == sha256(local_path). One SELECT for both
        # columns (avoid the second sqlite hit). Use smoke_sqlite_query
        # -json + json_object() so local_path with any character (incl.
        # '|', newlines, embedded quotes) won't be mis-split during awk
        # column extraction (reviewer hard-nit #3 — column-`|` brittleness).
        local row_json
        row_json=$(smoke_sqlite_query "$DB_PATH" -json \
            "SELECT json_object('file_hash', COALESCE(file_hash, ''), 'local_path', COALESCE(local_path, '')) AS r FROM media_assets WHERE id='${clip_id}'" || echo "[]")
        file_hash=$(printf '%s' "${row_json}" | jq -r '.[0].r.file_hash // ""' 2>/dev/null || echo "")
        local_path=$(printf '%s' "${row_json}" | jq -r '.[0].r.local_path // ""' 2>/dev/null || echo "")
        if [[ -z "$file_hash" || "$file_hash" == "null" ]]; then
            log_fail "Gate 7 inv-2: clip_id=${clip_id} file_hash is empty/null in media_assets"
            failures=$((failures + 1))
            continue
        fi
        if [[ -z "$local_path" || "$local_path" == "null" || ! -f "$local_path" ]]; then
            log_fail "Gate 7 inv-2: clip_id=${clip_id} local_path='${local_path:-empty}' missing or file absent on disk (Gate 5 file validation should have failed)"
            failures=$((failures + 1))
            continue
        fi
        sha_disk=$(sha256sum "$local_path" 2>/dev/null | awk '{print $1}' || echo "")
        if [[ -z "$sha_disk" ]]; then
            log_fail "Gate 7 inv-2: clip_id=${clip_id} sha256sum failed on local_path=${local_path} (read error / I/O)"
            failures=$((failures + 1))
            continue
        fi
        if [[ "$sha_disk" != "$file_hash" ]]; then
            log_fail "Gate 7 inv-2: clip_id=${clip_id} hash drift (media_assets.file_hash=${file_hash} != sha256(local_path)=${sha_disk}); re-pipeline needed"
            failures=$((failures + 1))
            continue
        fi
        log_pass "Gate 7 inv-2: clip_id=${clip_id} file_hash matches sha256(local_path)"

        # ── inv-3 + inv-4: asset_locations dual presence. SELECT COUNT(*)
        # per location_kind (sqlite outputs a bare scalar, not JSON).
        loc_local=$(smoke_sqlite_query "$DB_PATH" \
            "SELECT COUNT(*) FROM asset_locations WHERE asset_id='${clip_id}' AND location_kind='local'" || echo 0)
        loc_drive=$(smoke_sqlite_query "$DB_PATH" \
            "SELECT COUNT(*) FROM asset_locations WHERE asset_id='${clip_id}' AND location_kind='drive'" || echo 0)
        # Reviewer hard-nit fix: inv-3 fail NO LONGER short-circuits the
        # remaining invariants — inv-4 + inv-5 still run so the operator
        # gets the FULL diagnostic picture per clip. asset_locations and
        # outbox_events are independent surfaces: a dead_letter outbox
        # event MUST surface even when asset_locations is also malformed.
        # Only inv-4's `continue` skips inv-5 (the location structural
        # anomaly is the gate's most downstream "this row is incomplete"
        # signal — once seen, probing the outbox for the same row just
        # buries the real cause under chained logs).
        if [[ "$loc_local" -lt 1 ]] || [[ -z "$loc_local" ]]; then
            log_fail "Gate 7 inv-3: clip_id=${clip_id} asset_locations has NO row with location_kind='local' (local file anchor missing)"
            failures=$((failures + 1))
            # NO continue — fall through to inv-4 + inv-5 per reviewer hard-nit
        fi
        if [[ "$loc_drive" -lt 1 ]] || [[ -z "$loc_drive" ]]; then
            log_fail "Gate 7 inv-4: clip_id=${clip_id} asset_locations has NO row with location_kind='drive' (Drive mirror missing)"
            failures=$((failures + 1))
            continue
        fi
        log_pass "Gate 7 inv-3+4: clip_id=${clip_id} asset_locations has BOTH location_kind='local' (n=${loc_local}) AND location_kind='drive' (n=${loc_drive})"

        # ── inv-5: outbox chain COMPLETED+SUPERSEDED accepted, DEAD_LETTER
        # rejected, chain must exist. Per-clip inline aggregation (NOT
        # reusing smoke_outbox_chain_verify here because its rc=1 on
        # SUPERSEDED/PENDING is stricter than Gate 7's DoD spec — the
        # helper is correct for the "happy chain" reporting case but
        # not appropriate for the DoD-supersede acceptance surface).
        chain_row=$(smoke_sqlite_query "$DB_PATH" \
            "SELECT
                SUM(CASE WHEN status='completed' THEN 1 ELSE 0 END) AS completed,
                SUM(CASE WHEN status='superseded' THEN 1 ELSE 0 END) AS superseded,
                SUM(CASE WHEN status='dead_letter' THEN 1 ELSE 0 END) AS dead_letter,
                COUNT(*) AS total
            FROM outbox_events
            WHERE event_type='asset.index.requested' AND aggregate_id='${clip_id}'" || echo "")

        # Reviewer hard-nit 1 (set -e × jq guard): the `if ! jq -e …; then`
        # wrapper guarantees the explicit [FAIL] marker fires on jq exit
        # non-zero before any potential set -e abort — never silent.
        if ! printf '%s' "${chain_row}" | jq -e '
            (.completed // 0 | tonumber) + (.superseded // 0 | tonumber) >= 1
            and ((.dead_letter // 0 | tonumber)) == 0
            and ((.total // 0 | tonumber)) >= 1
        ' >/dev/null 2>&1; then
            log_fail "Gate 7 inv-5: clip_id=${clip_id} outbox chain rejected (need COMPLETED+SUPERSEDED>=1 AND DEAD_LETTER==0 AND chain exists; per-clip row dumped for triage)"
            smoke_echo_safe "$(printf '%s' "${chain_row}" | jq -c '.' 2>/dev/null || echo '{}')" >&2
            failures=$((failures + 1))
            continue
        fi
        log_pass "Gate 7 inv-5: clip_id=${clip_id} outbox chain healthy (event_type='asset.index.requested' COMPLETED+SUPERSEDED ok, no DEAD_LETTER)"
    done < "${clip_file}"

    # ── Aggregate verdict. DoD: ALL clips must pass — partial-pass is a
    # hard fail (mirrors Gates 4/5/6 contract).
    if [[ "$failures" -gt 0 ]]; then
        log_fail "Gate 7 aggregate: ${failures} clip(s) failed validation (validated ${clip_count} total — DoD requires ALL pass)"
        return 1
    fi

    # ── Forensic diagnostic: smoke_outbox_chain_verify prints the
    # classification table at end. The helper's internal rc semantics
    # are STRICTER than Gate 7's DoD spec (helper returns rc=1 on
    # SUPERSEDED-only / PENDING / MISSING / DEAD_LETTER — Gate 7 per-clip
    # inv-5 above already accepted SUPERSEDED as terminal per DoD).
    # Reaching this branch implies per-clip inv-5 already verified the
    # chain surface; we capture rc into a local var so a stricter-than-
    # DoD surface degrades to log_warn instead of a silent `|| true`
    # swallow that could mask a real DEAD_LETTER violation per AGENTS.md
    # "Never represent an unavailable backend as a successful no-op.
    # Fail closed." (The previous `|| true` was borderline — it masked
    # nothing in practice because per-clip inv-5 already gates DEAD_LETTER,
    # but it's the wrong tool to reach for; the rc-capture pattern is the
    # canonical way to express a diagnostic-only call.)
    local chain_rc=0
    smoke_outbox_chain_verify "$DB_PATH" "$clip_file" || chain_rc=$?
    if [[ "$chain_rc" -ne 0 ]]; then
        log_warn "Gate 7 forensic: outbox helper returned rc=${chain_rc} — note: helper is stricter than Gate 7 DoD (SUPERSEDED-only chains also return rc=1); per-clip inv-5 already verified the chain surface above; rc=${chain_rc} is informational, not a gate failure"
    fi

    log_pass "Gate 7 aggregate: all ${clip_count} clip(s) passed (1 media_assets row + sha256 coherence + 2 asset_locations rows + outbox COMPLETED+SUPERSEDED+no DEAD_LETTER)"
    return 0
}

# ── Gate 8 — Qdrant + media search hard gate ───────────────────────────
# Spec (July 2026 DoD, artlist-gates.md row 8):
#   - preflight: QDRANT_COLLECTION defaults to "media_assets_current"
#     (operational SSOT across tests/operational/*.sh scripts — verified
#      via code-searcher across qdrant_indexing_smoke.sh + yt_clip_register_*
#      + youtube_dod_live_verify.sh); $QDRANT_URL defaults to
#     "http://127.0.0.1:6333" (matches artlist_runtime.sh exports).
#   - per clip_id (3 from $WORK_DIR/clip_ids.txt hand-off shared with
#     Gates 4–7):
#       inv-1+2+3: velox_qdrant_assert (lib/velox_domain.sh) covers
#                   canonical Qdrant shape contract in one call:
#                   /points/scroll filter asset_id=clip_id returns ≥1 point;
#                   payload.source == "artlist", payload.media_type ==
#                   "video", payload.lifecycle_state == "PUBLISHED";
#                   rc=0 PASS, rc=1 SHAPE drift, rc=2 transport/HTTP
#                   non-2xx (mirrors Gate 6's 3-way case split).
#   - aggregate semantic recovery (promoted from warning to HARD gate per
#     DoD refactor):
#       inv-4:  POST /api/media/search with body
#                 {"query": "$ARTLIST_TERM", "sources": ["artlist"], "limit": 3}
#               (reuses $SMOKE_TOKEN + Idempotency-Key via smoke_curl).
#               Response items[].asset_id must contain AT LEAST ONE of the
#               3 clip_ids — failure to recoup is a HARD fail, not a
#               warning, per DoD operator contract.
#     Implementation: jq extracts returned asset_ids to $WORK_DIR/search_assets.txt,
#     then `grep -Fxcf $clip_file $search_assets_file` counts the
#     intersection (fail-closed: returns 0 sets rc, 1 false).
#   - ALL inv-clips must pass for Qdrant phase; recoupment count >= 1.
#
# Schema SSOTs:
#   - $QDRANT_COLLECTION default "media_assets_current" — matches the
#     operational tests' household default established in
#     tests/operational/{qdrant_indexing_smoke.sh,yt_clip_register_*}.
#     Hard-fail closed if absent AND no override env var.
#   - /api/media/search request/response shape :
#     internal/api/assets/search/handler.go:Search (200 → items[] +
#     next_cursor + partial + provider_errors).
#   - $ARTLIST_TERM source: tests/operational/lib/artlist_runtime.sh
#     ("business team working in modern office" default).
#
# Reuses ONLY helpers from lib/{common.sh,velox_domain.sh,artlist_runtime.sh}:
# velox_qdrant_assert (lib/velox_domain.sh) + smoke_sqlite_query (lib/common.sh)
# + smoke_curl (lib/common.sh) + jq -r asset_id extraction + grep -Fxcf
# intersection. NO new helpers introduced. AGENTS.md single-focus rule honoured.
gate_qdrant_search_gate() {
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        smoke_echo_safe "[DRY] Gate 8: Qdrant per-clip payload shape + Media Search semantic recovery:"
        smoke_echo_safe "[DRY]   - preflight QDRANT_COLLECTION default 'media_assets_current' + QDRANT_URL default 'http://127.0.0.1:6333'"
        smoke_echo_safe "[DRY]   - hand-off consumed from ${WORK_DIR}/clip_ids.txt (3 clip_ids from Gate 4, shared with Gates 5–7)"
        smoke_echo_safe "[DRY]   - per clip_id: velox_qdrant_assert <clip_id> media_assets_current <QDRANT_URL> artlist video PUBLISHED (covers inv-1…3 in one call; 3-way case on rc)"
        smoke_echo_safe "[DRY]   - aggregate semantic recovery: POST /api/media/search {query: \$ARTLIST_TERM, sources:[\"artlist\"], limit:3} → grep -Fxcf recoupment count >= 1 (PROMOTED warning → HARD gate)"
        return 0
    fi

    smoke_log_section "Gate 8 — Qdrant + Media Search hard gate"

    local clip_file="${WORK_DIR}/clip_ids.txt"
    if [[ ! -s "$clip_file" ]]; then
        log_fail "Gate 8 hand-off: ${clip_file} missing or empty (Gate 4 must write 3 clip_ids before Gate 5/6/7/8 can run)"
        return 1
    fi

    # Preflight: assign QDRANT_COLLECTION + QDRANT_URL from overrides
    # matched to the operational suite's household defaults.
    local q_coll="${QDRANT_COLLECTION:-media_assets_current}"
    local q_url="${QDRANT_URL:-http://127.0.0.1:6333}"

    # ── inv-1+2+3: Per-clip Qdrant Assertion (existence + collection +
    # payload shape). velox_qdrant_assert returns 3 distinct rc values
    # that map to 3 different operator actions — 3-way case statement
    # mirrors Gate 6's velox_drive_resolve diagnostic split for parallel
    # operator UX.
    local clip_id qa_rc failures=0 clip_count=0
    while read -r clip_id; do
        [[ -z "$clip_id" ]] && continue
        clip_count=$((clip_count + 1))

        # Set -e × velox_qdrant_assert guard: capture rc via case-statement
        # immediately after the if-condition so the [FAIL] log line fires
        # with the canonical rc-mapped operator action (rather than a
        # silent set -e abort). AGENTS.md fail-closed + reviewer hard-nit
        # pattern from Gates 6/7.
        # Reviewer hard-nit fix: rc-capture happens BEFORE the if-test.
        # In bash, `if ! cmd; then BODY; fi` sets $? to 0 inside BODY (since
        # the negation test was true). To recover cmd's actual rc you must
        # run cmd in its own statement, capture $? immediately, THEN branch
        # on the captured value. The original `qa_rc=$?` inside the then-
        # branch captured 0 unconditionally, masking the SHAPE-vs-TRANSPORT
        # distinction and forcing every failure into the `*` default branch.
        velox_qdrant_assert "${clip_id}" "${q_coll}" "${q_url}" "artlist" "video" "PUBLISHED" "${QDRANT_API_KEY:-}"
        qa_rc=$?
        if [[ "${qa_rc}" -ne 0 ]]; then
            case "${qa_rc}" in
                1)
                    # SHAPE contract drift: HTTP 2xx but payload.source /
                    # media_type / lifecycle_state mismatch — the Qdrant
                    # projection is alive but its contents don't match the
                    # canonical contract.
                    log_fail "Gate 8 inv-1: clip_id=${clip_id} Qdrant SHAPE drift (rc=1) inside ${WORK_DIR}/velox_qdrant_${clip_id}.json — payload.source/media_type/lifecycle_state mismatch on collection=${q_coll}"
                    ;;
                2)
                    # HTTP / transport non-2xx: bearer expired, QDRANT_API_KEY
                    # wrong, Qdrant service down, network/DNS/SSL error.
                    log_fail "Gate 8 inv-1: clip_id=${clip_id} Qdrant TRANSPORT/HTTP non-2xx (rc=2) — verify QDRANT_URL=${q_url} and QDRANT_API_KEY freshness"
                    ;;
                *)
                    log_fail "Gate 8 inv-1: clip_id=${clip_id} velox_qdrant_assert unexpected rc=${qa_rc}"
                    ;;
            esac
            failures=$((failures + 1))
            continue
        fi
        log_pass "Gate 8 inv-1: clip_id=${clip_id} Qdrant point exists in collection=${q_coll} with payload source=artlist media_type=video lifecycle_state=PUBLISHED"
    done < "${clip_file}"

    # Qdrant-phase aggregate: ALL clips must pass (DoD forbids partial-pass
    # — mirrors Gate 4 inv-6/7 + Gates 5/6/7 aggregate contracts).
    if [[ "$failures" -gt 0 ]]; then
        log_fail "Gate 8 Qdrant phase aggregate: ${failures}/${clip_count} clip(s) failed Qdrant validation (DoD requires ALL pass before semantic search)"
        return 1
    fi
    log_pass "Gate 8 Qdrant phase aggregate: all ${clip_count}/${clip_count} clip(s) have Qdrant point + canonical payload shape"

    # ── inv-4: Semantic recovery hard gate (PROMOTED from warning to
    # hard per DoD refactor). POST /api/media/search with the EXACT same
    # $ARTLIST_TERM that surfaced the 3 clips in Gate 4 — semantic
    # recovery uses the canonical Artlist seed term (sourced from
    # lib/artlist_runtime.sh, default 'business team working in modern
    # office'). The handler contract is in
    # internal/api/assets/search/handler.go:Search (200 → items[] +
    # next_cursor + partial + provider_errors).
    local search_body term
    # Reviewer hard-nit fix: use the SAME normalized term that Gate 4
    # persisted to artlist_runs (and stashed into godlike/06
    # ${WORK_DIR}/gate4_norm_term.txt) — NOT the raw $ARTLIST_TERM
    # which may carry trailing whitespace or >6 words (which Gate 4's
    # NormalizeRunTagRequest normalizes away via trim + collapse +
    # cap-at-6). Reading from the hand-off file guarantees the
    # semantic-recovery search queries the canonical indexed variant
    # and recoupment won't fail closed for an unrelated reason.
    #
    # Fail-CLOSED on MISS: godlike/06 SSOT strictly. If
    # ${WORK_DIR}/gate4_norm_term.txt is absent, the Gate 4 hand-off
    # contract is broken — bundle integrity violated. Standalone Gate 8
    # invocation (outside the 4-gate SSOT chain) is unsupported; we fail
    # fast with `log_fail` + `return 1` rather than silently retrying
    # under raw $ARTLIST_TERM with a misleading [FAIL] stamp that lets
    # the bundle continue and produce a confusing "PASS-after-FAIL"
    # outcome in the operator audit log. Mirrors Gate 4's own pattern
    # (`if [[ ! -s "$WORK_DIR/clip_ids.txt" ]]; then log_fail; return 1; fi`).
    if [[ -s "${WORK_DIR}/gate4_norm_term.txt" ]]; then
        term="$(cat "${WORK_DIR}/gate4_norm_term.txt}")"
    else
        log_fail "Gate 8 hand-off MISS: ${WORK_DIR}/gate4_norm_term.txt absent (Gate 4 must write the godlike/06 SSOT hand-off; standalone Gate 8 invocation is unsupported — run via 05_pipeline_fresh.sh::main so all 5 gates execute in order)"
        return 1
    fi
    search_body=$(jq -nc --arg q "${term}" '{query:$q, sources:["artlist"], limit:3}')

    local http_code
    # Reviewer hard-nit pattern: capture $? via smoke_curl stdout to
    # avoid subshell rc drift (smoke_curl writes SMOKE_LAST_HTTP/BODY as
    # side-effects which get lost in $(…) — the printed HTTP code on
    # stdout is the canonical return path).
    http_code=$(smoke_curl POST "/api/media/search" -d "${search_body}")
    if [[ ! "${http_code}" =~ ^2[0-9][0-9]$ ]]; then
        log_fail "Gate 8 inv-4: POST /api/media/search returned HTTP ${http_code} (DoD requires 2xx for semantic recovery)"
        return 1
    fi
    log_pass "Gate 8 inv-4-1: POST /api/media_search HTTP ${http_code} (term='${term}', filter sources=[artlist])"

    # Extract returned asset_ids to a side band and intersect against
    # Gate 4's hand-off clip_ids.txt. `grep -Fxcf <needle-file> <haystack
    # -file>` counts the count of lines in needle-file that appear as
    # full-line matches in haystack-file. -F (fixed string), -x (full
    # line), -c (count), -f (read patterns from file). Returns 0 when ≥1
    # match found, 1 when 0 matches (fail-closed semantics).
    local search_assets_file="${WORK_DIR}/search_assets.txt"
    # Set -e × jq guard: `|| :` swallows non-zero exit so the bash side
    # never aborts the script silently on malformed JSON (matches Gate 4
    # /api/artlist/runs hand-off `|| :` pattern).
    jq -r '.items[]?.asset_id // empty' "${SMOKE_LAST_BODY}" \
        > "${search_assets_file}" 2>/dev/null || :

    # Set -e × grep -Fxcf guard: an empty haystack file would produce rc=1
    # (no matches). Redirect stderr to /dev/null + `|| echo 0` keeps the
    # fail-closed contract even when ${search_assets_file} is empty.
    local found_count
    found_count=$(grep -Fxcf "${clip_file}" "${search_assets_file}" 2>/dev/null || echo 0)

    if [[ "${found_count}" -lt 1 ]]; then
        log_fail "Gate 8 inv-4-2: semantic recovery FAILED HARD (previously warning) — recouped 0/${clip_count} clip_ids for term='${term}' (DoD: GA08 hard gate requires ≥1 recouped clip)"
        # Forensic dump: every item the search returned (source + asset_id),
        # routed through smoke_echo_safe (token redacted) for triage without
        # exposing the bearer token in logs.
        smoke_echo_safe "Search response items (first 10):" >&2
        jq -c '.items // [] | .[0:10] | map({asset_id: (.asset_id // null), source: (.source // null)})' \
            "${SMOKE_LAST_BODY}" 2>/dev/null >&2 || :
        return 1
    fi
    log_pass "Gate 8 inv-4-2: semantic recovery recouped ${found_count}/${clip_count} clip_id(s) for term='${term}' (HARD gate satisfied)"

    log_pass "Gate 8 aggregate: Qdrant per-clip ALL PASS + semantic recovery >= 1/${clip_count}; collection=${q_coll} verified end-to-end"
    return 0
}

main() {
    # Under DRY_RUN, every gate's [DRY] banner fires (gate_* functions
    # handle their own DRY_RUN early-returns inside the if blocks). This
    # is the canonical "describe probe surface" pattern — main() is a
    # thin orchestration shell, the gates own their own probe listing per
    # AGENTS.md single-focus rule (avoids duplicating the per-gate probe
    # enumeration in main()'s printf summary that was the previous
    # design).
    gate_fresh_run_three || return 1
    gate_per_clip_validation || return 1
    gate_drive_resolve_gate || return 1
    gate_outbox_integrity_gate || return 1
    gate_qdrant_search_gate || return 1

    printf '\n============================================\n'
    printf '  05_pipeline_fresh (Gates 4 + 5 + 6 + 7 + 8)\n'
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
