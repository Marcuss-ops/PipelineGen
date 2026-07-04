#!/usr/bin/env bash
#
# stock_register_batch_boxing_smoke.sh — PipelineGen black-box smoke test
# for POST /api/media/register-batch with Pacquiao vs Broner boxing clips.
#
# Usage:
#   VELOX_ADMIN_TOKEN=<token> ./stock_register_batch_boxing_smoke.sh
#   VELOX_ADMIN_TOKEN=<token> ./stock_register_batch_boxing_smoke.sh --dry
#   VELOX_ADMIN_TOKEN=<token> API_BASE=127.0.0.1:8000 ./stock_register_batch_boxing_smoke.sh
#
#   Env overrides:
#     API_BASE                  host:port (default 127.0.0.1:8000)
#     SMOKE_DRIVE_FOLDER_ID     Google Drive folder (default: boxing match folder)
#     SMOKE_POLL_TIMEOUT_SECONDS poll ceiling (default 600 — yt-dlp + cut + upload is slow)
#     SMOKE_FORCE_REDOWNLOAD    1 = inject `force: true` on every clip in
#                               build_batch_payload (re-process even when the
#                               clip-hash already exists in media_assets).
#                               Default: unset/0 = force field absent/omitted
#                               → preserve idempotency on first run; subsequent
#                               runs dedupe by clip hash. Set to 1 when the
#                               server side has changed and the existing rows
#                               need to be overwritten.
#
# Tests:
#   Test 1 — POST /api/media/register-batch with 8 boxing rounds
#            → HTTP 200, total=8, enqueue_succeeded=8
#            → each result has a JobID (async enqueue), not a ClipID
#   Test 2 — Poll GET /api/jobs/{JobID}/full for each clip
#            → every job reaches terminal status=completed within timeout
#            → each result has clip_id populated after completion
#   Test 3 — Assert media_assets rows created in SQLite
#            → run AFTER all async jobs complete
#
# NOTE: PR-BATCH-REGISTER-ASYNC (July 2026) — register-batch is now ASYNCHRONOUS.
# Each clip becomes an independent media.clip job enqueued via the
# ClipJobEnqueuer port. The handler returns immediately with job_ids;
# yt-dlp download + cut + Drive upload + DB write happen off the request
# thread. Callers MUST poll GET /api/jobs/{id}/full to track completion.
#
# Exit codes:
#   0  all assertions passed
#   1  one or more assertions failed
#   2  setup error (missing token, missing binary, DB not found)
#   124 timeout exceeded

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

smoke_require sqlite3

# ── Constants ──────────────────────────────────────────────────────────
VIDEO_URL="https://www.youtube.com/watch?v=RRJvrDKunyA"
DRIVE_FOLDER_ID="${SMOKE_DRIVE_FOLDER_ID:-1DeDTQK0CvrteF2MO5XhiXyp64amXvRqf}"
SMOKE_DB="${SMOKE_DB:-data/media/media.db.sqlite}"

# PR-BATCH-REGISTER-ASYNC (July 2026): default target is port 8000 per
# convention established by stock_run_boxing_smoke.sh. Must override
# SMOKE_API_BASE (not API_BASE) because common.sh already consumed API_BASE
# at source-time to set SMOKE_API_BASE=127.0.0.1:8080. Overriding
# SMOKE_API_BASE here honours the env-var API_BASE if set by the operator,
# otherwise defaults to port 8000.
SMOKE_API_BASE="${API_BASE:-127.0.0.1:8000}"
SMOKE_POLL_TIMEOUT_SECONDS="${SMOKE_POLL_TIMEOUT_SECONDS:-600}"

# SMOKE_FORCE_REDOWNLOAD — preserves idempotency by default. SMOKE_FORCE_REDOWNLOAD=1
# sets the per-clip `force` wire field to true so register-batch re-processes every
# clip even when its derived clip-hash already exists in media_assets. Mapped here
# to a JSON boolean (force_field) consumed by build_batch_payload (post-process).
SMOKE_FORCE_REDOWNLOAD="${SMOKE_FORCE_REDOWNLOAD:-0}"
if [[ "$SMOKE_FORCE_REDOWNLOAD" == "1" ]]; then
    force_field="true"
else
    force_field="false"
fi

# ── Help text ──────────────────────────────────────────────────────────
if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    sed -n '2,37p' "$0"
    exit 0
fi

# ── Dry-run mode ─────────────────────────────────────────────────────
if [[ "$DRY_RUN" == "1" ]]; then
    smoke_echo_safe "DRY RUN — would probe:"
    printf '  POST http://%s/api/media/register-batch  (8 clips from %s)\n' \
        "$SMOKE_API_BASE" "$VIDEO_URL"
    printf '  jq .results[N].JobID  …  (async enqueue result)\n'
    printf '  GET  /api/jobs/{id}/full  (poll x8, timeout %ss)\n' \
        "$SMOKE_POLL_TIMEOUT_SECONDS"
    printf '  sqlite3 %s  …  (assertion probes)\n' "$SMOKE_DB"
    exit 0
fi

# ── Setup guard ─────────────────────────────────────────────────────
if [[ ! -f "$SMOKE_DB" ]]; then
    printf '%ssetup error: SMOKE_DB=%s does not exist (server must be running first)%s\\n' \
        "$RED" "$SMOKE_DB" "$RESET" >&2
    exit 2
fi

declare -a FAILURES=()
fail() { FAILURES+=("$1"); }

sqlite_q() {
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

# ── Build the batch payload ──────────────────────────────────────────
# 8 rounds from the Pacquiao vs Broner highlights video.
# Timestamps converted to float seconds.
#
# SMOKE_FORCE_REDOWNLOAD knob: the per-clip `force` field is injected via
# post-processing (jq ... --argjson force "$force_field" '.clips |=
# map(. + {force: $force})') so the 8 hardcoded clip literals stay untouched.
# - force_field=true  → server re-processes every clip (drop, re-Drive-upload,
#                       re-register from the source URL)
# - force_field=false → server preserves idempotency via clip-hash dedup
build_batch_payload() {
    local raw
    raw=$(jq -n --arg url "$VIDEO_URL" --arg fid "$DRIVE_FOLDER_ID" '{
        folder_id: $fid,
        clips: [
            {
                url: $url,
                name: "Round 1 \u2014 Fase di studio e velocit\u00e0 di Pacquiao",
                description: "Inizio del match. Pacquiao mette subito in mostra la sua mobilit\u00e0 e rapidit\u00e0 di gambe, lavorando molto con il jab da mancino per prendere le misure. Broner mantiene una guardia molto larga e fatica a prendergli il tempo.",
                tags: ["boxing","pacquiao","broner","round-1","welterweight","WBA"],
                source: "youtube",
                category: "boxing",
                group: "pacquiao-vs-broner",
                start: 32.0,
                end: 231.0
            },
            {
                url: $url,
                name: "Round 2 \u2014 Posizionamento e primi scambi",
                description: "Entrambi i pugili cercano di guadagnare la posizione con il piede avanzato. Pacquiao accelera il ritmo con combinazioni veloci, mentre Broner risponde principalmente di rimessa spingendo via l\u0027avversario.",
                tags: ["boxing","pacquiao","broner","round-2","welterweight","WBA"],
                source: "youtube",
                category: "boxing",
                group: "pacquiao-vs-broner",
                start: 247.0,
                end: 345.0
            },
            {
                url: $url,
                name: "Round 5 \u2014 Il miglior momento di Broner",
                description: "Broner riesce a trovare maggiore continuit\u00e0 con il diretto destro, colpendo il mento di Pacquiao in un paio di occasioni. Pacquiao risponde con un potente gancio sinistro al corpo prima di riprendere il controllo del ritmo a fine round.",
                tags: ["boxing","pacquiao","broner","round-5","welterweight","WBA"],
                source: "youtube",
                category: "boxing",
                group: "pacquiao-vs-broner",
                start: 628.0,
                end: 767.0
            },
            {
                url: $url,
                name: "Round 7 \u2014 Il momento chiave: Broner barcolla",
                description: "Il round pi\u00f9 spettacolare del match. Pacquiao mette a segno una serie di colpi durissimi, tra cui un potente montante e un sinistro che scuotono visibilmente Broner. Broner \u00e8 costretto a legare ed \u00e8 quasi sul punto di andare KO mentre Pacquiao lo tempesta di colpi all\u0027angolo.",
                tags: ["boxing","pacquiao","broner","round-7","knockdown","welterweight","WBA"],
                source: "youtube",
                category: "boxing",
                group: "pacquiao-vs-broner",
                start: 993.0,
                end: 1048.0
            },
            {
                url: $url,
                name: "Round 9 \u2014 Pacquiao ancora all\u0027attacco",
                description: "Un altro ottimo round per il filippino. Pacquiao intercetta Broner con un potente gancio sinistro d\u0027incontro che lo fa arretrare vistosamente sui tacchi, costringendolo nuovamente a subire una raffica di colpi all\u0027angolo.",
                tags: ["boxing","pacquiao","broner","round-9","welterweight","WBA"],
                source: "youtube",
                category: "boxing",
                group: "pacquiao-vs-broner",
                start: 1276.0,
                end: 1330.0
            },
            {
                url: $url,
                name: "Round 10-11 \u2014 Controllo di Pacquiao e mancanza di iniziativa di Broner",
                description: "Viene evidenziato il divario nei colpi portati: Pacquiao domina per volume, mentre Broner lancia pochissimi destri, facendo sospettare un infortunio alla mano. Al Round 11 le statistiche mostrano 109 colpi a segno per Pacquiao contro i soli 49 di Broner.",
                tags: ["boxing","pacquiao","broner","round-10","round-11","welterweight","WBA"],
                source: "youtube",
                category: "boxing",
                group: "pacquiao-vs-broner",
                start: 1382.0,
                end: 1626.0
            },
            {
                url: $url,
                name: "Round 12 \u2014 Il finale del match",
                description: "Negli ultimi 30 secondi Broner non mostra l\u0027urgenza di dover recuperare lo svantaggio e Pacquiao controlla agevolmente fino al suono della campana finale.",
                tags: ["boxing","pacquiao","broner","round-12","welterweight","WBA"],
                source: "youtube",
                category: "boxing",
                group: "pacquiao-vs-broner",
                start: 1657.0,
                end: 1698.0
            },
            {
                url: $url,
                name: "Annuncio del verdetto ufficiale",
                description: "I giudici assegnano una netta decisione unanime a favore di Manny Pacquiao (117-111, 116-112, 116-112), che conserva il titolo mondiale WBA dei pesi welter.",
                tags: ["boxing","pacquiao","broner","verdict","welterweight","WBA"],
                source: "youtube",
                category: "boxing",
                group: "pacquiao-vs-broner",
                start: 1727.0,
                end: 1769.0
            }
        ]
    }')
    # Inject per-clip `force` from SMOKE_FORCE_REDOWNLOAD knob in one pass.
    printf '%s' "$raw" | jq --argjson force "$force_field" \
        '.clips |= map(. + {force: $force})'
}

# ── Test 1: POST /api/media/register-batch ───────────────────────────
# PR-BATCH-REGISTER-ASYNC (July 2026): register-batch is now asynchronous.
# The response has total=8 (count of clips submitted) and each result has a
# JobID (async enqueue) instead of a ClipID (synchronous registration).
# `enqueue_succeeded` counts jobs that were successfully enqueued (not clips
# that finished processing). The test extracts job IDs for Test 2 polling.
test_1_batch_register() {
    smoke_log_section "Test 1: POST /api/media/register-batch (8 boxing clips, async)"

    local payload
    payload=$(build_batch_payload)

    # Save payload for diagnostics
    printf '%s' "$payload" > "$WORK_DIR/batch_payload.json"

    local code
    code=$(smoke_curl POST "/api/media/register-batch" --data "$payload")
    # Defensive: smoke_curl exports SMOKE_LAST_BODY, but under set -u guard against edge cases
    local last_body="${SMOKE_LAST_BODY:-/dev/null}"

    if [[ "$code" != "200" ]]; then
        fail "test1_http_${code}"
        printf '%sFAIL: HTTP %s (expected 200)%s\n' "$RED" "$code" "$RESET" >&2
        if [[ -s "$last_body" ]]; then
            smoke_echo_safe "  body: $(head -c 500 "$last_body" 2>/dev/null || true)" >&2
        fi
        return 1
    fi

    printf '%s  HTTP 200 OK%s\n' "$GREEN" "$RESET"

    # Parse response — PR-BATCH-REGISTER-ASYNC: BatchRegisterResponse has
    # total, succeeded, failed fields (lowercase json tags). `succeeded` now
    # counts successfully ENQUEUED jobs, not successfully registered clips.
    local total succeeded failed
    total=$(jq -r '.total // 0' "$last_body")
    succeeded=$(jq -r '.succeeded // 0' "$last_body")
    failed=$(jq -r '.failed // 0' "$last_body")

    printf '  total=%s  enqueue_succeeded=%s  enqueue_failed=%s\\n' "$total" "$succeeded" "$failed"

    if [[ "$total" != "8" ]]; then
        fail "test1_total_${total}_expected_8"
    fi

    if (( succeeded == 0 )); then
        fail "test1_zero_enqueued"
        printf '%sFAIL: zero clips enqueued — check server logs%s\\n' "$RED" "$RESET" >&2
    elif (( succeeded != total )); then
        fail "test1_partial_enqueue_${succeeded}_of_${total}"
        printf '%sWARN: %s/%s jobs enqueued (expected all %s)%s\\n' \\
            "$YELLOW" "$succeeded" "$total" "$total" "$RESET" >&2
    else
        printf '%s  %s/%s jobs enqueued successfully%s\\n' "$GREEN" "$succeeded" "$total" "$RESET"
    fi

    # PR-BATCH-REGISTER-ASYNC: BatchClipResult fields are PascalCase
    # (ClipID, Name, OK, Error, Duplicate, JobID). JobID is the async
    # job identifier; ClipID is empty in the enqueue response (the clip
    # hasn't been registered yet). OK=true means "job enqueued", not
    # "clip registered". Duplicate is always false in async mode.
    cp "$last_body" "$WORK_DIR/batch_response.json"

    # Extract JobID for every clip — these are used in Test 2 for polling.
    printf '\n  %s--- per-clip enqueue results (JobID for async tracking) ---%s\n' "$DIM" "$RESET"
    jq -r '.results[] | "    \(.Name // "?")  ok=\(.OK)  err=\(.Error // "none")  job_id=\(.JobID // "-")"' \
        "$WORK_DIR/batch_response.json" 2>/dev/null || true
}

# ── Test 2: Poll each async job to completion ────────────────────────
# PR-BATCH-REGISTER-ASYNC (July 2026): each clip is now an independent
# media.clip job processed off the request thread. This test polls
# GET /api/jobs/{JobID}/full for each clip and asserts:
#   1. Every job reaches terminal status=completed within timeout
#   2. Each result has clip_id populated (proof of Drive upload + DB write)
#   3. Every job's result.ok=true (the ytSvc.Register call succeeded)
#
# Missing or empty JobID from Test 1 (enqueue failure) is treated as
# a hard fail for that clip.
test_2_poll_jobs() {
    smoke_log_section "Test 2: Poll async media.clip jobs (timeout ${SMOKE_POLL_TIMEOUT_SECONDS}s)"

    if [[ ! -f "$WORK_DIR/batch_response.json" ]]; then
        printf '%sskipped:%s no response from Test 1\\n' "$YELLOW" "$RESET" >&2
        fail "test2_skipped_no_response"
        return 1
    fi

    # PR-BATCH-REGISTER-ASYNC: extract JobID from each result.
    # BatchClipResult fields are PascalCase: JobID (string), Name (string).
    # JobID is empty/null when the enqueue failed for a specific clip.
    # Test 1 already verified total=8 and succeeded>0; here we count
    # non-empty JobID entries and fail if any clip has a missing JobID.
    local missing_jobid_count
    missing_jobid_count=$(jq '[.results[] | select(.JobID == null or .JobID == "")] | length' "$WORK_DIR/batch_response.json")
    if (( missing_jobid_count > 0 )); then
        fail "test2_missing_jobid_${missing_jobid_count}_of_8"
        printf '%sFAIL: %s clip(s) have empty JobID (enqueue failed for these clips)%s\n' \
            "$RED" "$missing_jobid_count" "$RESET" >&2
        jq -r '.results[] | select(.JobID == null or .JobID == "") | "    \(.Name // "?")  error=\(.Error // "?")"' \
            "$WORK_DIR/batch_response.json" 2>/dev/null || true
    fi

    local job_ids
    job_ids=$(jq -r '.results[] | select(.JobID != null and .JobID != "") | .JobID' "$WORK_DIR/batch_response.json")

    if [[ -z "$job_ids" ]]; then
        fail "test2_zero_jobids"
        printf '%sFAIL: no JobID fields in response — enqueue may have failed for all clips%s\\n' \
            "$RED" "$RESET" >&2
        printf '  hint: check server logs for media.clip handler registration errors\\n' >&2
        return 1
    fi

    local total_jobs=0
    local completed_jobs=0
    local failed_jobs=0
    local first_poll=1

    # Poll each job individually. smoke_poll_terminal returns 0 when the
    # job reaches a terminal status (completed/failed/cancelled/dead_letter)
    # and sets SMOKE_LAST_STATUS + SMOKE_LAST_BODY for the caller to inspect.
    local job_file="$WORK_DIR/poll_job_result.json"
    for job_id in $job_ids; do
        total_jobs=$((total_jobs + 1))
        printf '\n  %s[%d/8] Polling job %s ...%s\n' "$CYAN" "$total_jobs" "$job_id" "$RESET"

        if ! smoke_poll_terminal "$job_id"; then
            # Return 124 = timeout, non-zero non-124 = HTTP error
            fail "test2_job_${job_id}_timeout_or_http_error"
            printf '%sFAIL: job %s did not reach terminal status (last status: %s)%s\\n' \
                "$RED" "$job_id" "${SMOKE_LAST_STATUS:-?}" "$RESET" >&2
            failed_jobs=$((failed_jobs + 1))
            continue
        fi

        printf '  status=%s  ' "$SMOKE_LAST_STATUS"

        # On first successful poll, dump .result shape so the operator
        # can verify result_map nesting (media.clip handler returns a bare
        # map[string]any; the wire shape may be .result directly or
        # .result.result_map depending on the codec).
        if (( first_poll )); then
            first_poll=0
            local result_shape
            result_shape=$(jq -c '.result | keys' "$SMOKE_LAST_BODY" 2>/dev/null || true)
            printf 'result.keys=%s  ' "${result_shape:-?}"
        fi

        if [[ "$SMOKE_LAST_STATUS" != "completed" ]]; then
            fail "test2_job_${job_id}_${SMOKE_LAST_STATUS}"
            printf '%sFAIL: expected completed, got %s%s\\n' \
                "$RED" "$SMOKE_LAST_STATUS" "$RESET" >&2
            failed_jobs=$((failed_jobs + 1))
            continue
        fi

        # Extract per-job result fields. The media.clip handler returns:
        # {ok, clip_id, duplicate, name, drive_link, delivery_status, message}
        # These live under result.result_map in /api/jobs/{id}/full.
        local result_ok result_clip_id result_name
        result_ok=$(jq -r '.result.result_map.ok // false' "$SMOKE_LAST_BODY")
        result_clip_id=$(jq -r '.result.result_map.clip_id // ""' "$SMOKE_LAST_BODY")
        result_name=$(jq -r '.result.result_map.name // "?"' "$SMOKE_LAST_BODY")

        if [[ "$result_ok" != "true" ]]; then
            # Check for duplicate (still valid — the clip already existed)
            local result_dup
            result_dup=$(jq -r '.result.result_map.duplicate // false' "$SMOKE_LAST_BODY")
            if [[ "$result_dup" == "true" ]]; then
                printf '%sduplicate (already registered)%s' "$DIM" "$RESET"
                completed_jobs=$((completed_jobs + 1))
            else
                fail "test2_job_${job_id}_not_ok"
                printf '%sFAIL: ok=false, clip not registered%s\\n' "$RED" "$RESET" >&2
                local result_msg
                result_msg=$(jq -r '.result.result_map.message // "?"' "$SMOKE_LAST_BODY")
                printf '    message=%s\\n' "$result_msg"
                failed_jobs=$((failed_jobs + 1))
            fi
        elif [[ -z "$result_clip_id" || "$result_clip_id" == "null" ]]; then
            fail "test2_job_${job_id}_empty_clip_id"
            printf '%sFAIL: job completed but clip_id is empty (DB/Drive insert failed silently)%s\\n' \
                "$RED" "$RESET" >&2
            failed_jobs=$((failed_jobs + 1))
        else
            printf '%sclip_id=%s  name=%s%s' "$GREEN" "$result_clip_id" "$result_name" "$RESET"
            # Save per-job result for Test 3 cross-reference
            printf '%s\n' "name=$result_name clip_id=$result_clip_id job_id=$job_id" \
                >> "$WORK_DIR/completed_jobs.txt"
            completed_jobs=$((completed_jobs + 1))
        fi

        printf '\n'
    done

    printf '\n  %s--- async job summary ---%s\n' "$DIM" "$RESET"
    printf '  total=%s  completed=%s  failed=%s\\n' "$total_jobs" "$completed_jobs" "$failed_jobs"

    if (( completed_jobs == 0 )); then
        fail "test2_zero_completed"
        printf '%sFAIL: zero jobs reached completed status%s\\n' "$RED" "$RESET" >&2
    else
        printf '%s  %s/%s jobs completed successfully%s\\n' "$GREEN" "$completed_jobs" "$total_jobs" "$RESET"
    fi
}

# ── Test 3: Assert media_assets rows exist ────────────────────────────
# PR-BATCH-REGISTER-ASYNC (July 2026): this test runs AFTER all async jobs
# have completed. media_assets rows are written by the media.clip handler's
# ytSvc.Register call inside the per-job tx.
test_3_media_assets() {
    smoke_log_section "Test 3: Verify media_assets rows in SQLite (post async jobs)"

    # Count media_assets created in the last 30 minutes that match our group.
    # Extended window from 15→30 min because async jobs may take >15 min
    # with yt-dlp download + Drive upload.
    local asset_count
    asset_count=$(sqlite_q "SELECT COUNT(*) FROM media_assets WHERE source='youtube' AND category='boxing' AND \"group\"='pacquiao-vs-broner' AND created_at > datetime('now','-30 minutes')")

    printf '  media_assets rows (last 30 min): %s\\n' "$asset_count"

    if (( asset_count == 0 )); then
        fail "test3_zero_media_assets"
        printf '%sFAIL: no media_assets rows found for pacquiao-vs-broner group%s\\n' \
            "$RED" "$RESET" >&2
        printf '  hint: check if the worker is running, media.clip handler is registered,\\n' >&2
        printf '        and yt-dlp is available on the worker host\\n' >&2
    elif (( asset_count >= 1 )); then
        printf '%s  At least 1 media_asset row created%s\\n' "$GREEN" "$RESET"

        # Show the assets for diagnostics
        printf '\n  %s--- media_assets detail ---%s\n' "$DIM" "$RESET"
        sqlite_q "SELECT id, name, drive_file_id, indexing_status, lifecycle_state FROM media_assets WHERE source='youtube' AND category='boxing' AND \"group\"='pacquiao-vs-broner' AND created_at > datetime('now','-30 minutes')" \
            | while IFS='|' read -r id name drive_id idx_status lifecycle; do
            printf '    id=%-50s name=%-45s drive=%-45s idx=%-12s life=%-12s\\n' \
                "${id:0:50}" "${name:0:45}" "${drive_id:0:45}" "$idx_status" "$lifecycle"
        done
    fi

    # Also check drive_file_id presence (clips should be uploaded to Drive)
    local drive_count
    drive_count=$(sqlite_q "SELECT COUNT(*) FROM media_assets WHERE source='youtube' AND category='boxing' AND \"group\"='pacquiao-vs-broner' AND drive_file_id != '' AND created_at > datetime('now','-30 minutes')")
    printf '\n  with drive_file_id: %s\\n' "$drive_count"

    if (( drive_count > 0 )); then
        printf '%s  Clips uploaded to Google Drive%s\\n' "$GREEN" "$RESET"
    fi
}

# ── Main ───────────────────────────────────────────────────────────────
main() {
    smoke_log_section "Stock Register-Batch Boxing Smoke Test (async)"
    printf '  target:  %s\\n  video:   %s\\n  folder:  %s\\n  db:      %s\\n' \
        "$SMOKE_API_BASE" "$VIDEO_URL" "$DRIVE_FOLDER_ID" "$SMOKE_DB"
    printf '  poll timeout: %ss (media.clip async jobs)\\n\\n' \
        "$SMOKE_POLL_TIMEOUT_SECONDS"

    test_1_batch_register
    test_2_poll_jobs
    test_3_media_assets

    echo
    if (( ${#FAILURES[@]} == 0 )); then
        printf '%sOK: Stock register-batch boxing smoke checks all green%s\\n' \
            "$GREEN" "$RESET"
        exit 0
    fi
    printf '%sFAIL: %d assertion(s) failed:%s\\n' \
        "$RED" "${#FAILURES[@]}" "$RESET" >&2
    for f in "${FAILURES[@]}"; do
        printf '  - %s\\n' "$f" >&2
    done
    exit 1
}

main "$@"
