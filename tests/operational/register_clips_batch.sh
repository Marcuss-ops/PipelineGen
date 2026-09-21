#!/usr/bin/env bash
# tests/operational/register_clips_batch.sh — live driver for the CLIPS
# timestamp path: POST /api/media/register-batch.
#
# WHY THIS ENDPOINT (and not /api/stock-pipeline/run):
#   /api/stock-pipeline/run is the STOCK pipeline. It runs a deterministic
#   planner, composes the result, and — for `clips[]` — routes every plan
#   through the explicit planner, which AUTO-SPLITS any clip whose duration
#   is >= 60s into 5-second children
#   (stockpipeline/step_plan_clips.go::expandExplicitClipSpecs, pinned by
#   TestStockPlanStep_ExplicitClips_LongClip_DefaultsToFiveSecondSegments).
#   That is the wrong tool for operator-supplied highlight windows.
#
#   POST /api/media/register-batch is the CLIPS capability's YouTube
#   registrar: each entry of `clips[]` carries its OWN `url` (so several
#   source videos can share one request), explicit `start`/`end` in SECONDS,
#   and NO segmentation at all when `seconds_per_segment` is omitted — one
#   clip window => one clip_id => one Drive upload. That is the "download
#   these timestamps and upload them correctly" flow.
#
# REQUEST CONTRACT (internal/capabilities/assets/register/requests.go):
#   { "folder_id": "<drive folder id>",
#     "clips": [ { "url": "<youtube url>", "name": "...", "summary": "...",
#                  "description": "...", "topics": [...], "speakers": [...],
#                  "mentioned_people": [...], "hook": "...", "tags": [...],
#                  "source": "youtube", "category": "...", "group": "...",
#                  "start": 23, "end": 84, "force": false } ] }
#   - `clips` and each clip `url` are binding:"required".
#   - `start`/`end` are float64 SECONDS (not "mm:ss").
#   - `hook_clips` is NOT part of any server contract: it is an analysis
#     artefact. Hook windows are submitted as ordinary clips[] entries.
#   - `seconds_per_segment` > 0 fans one clip out into N children, each with
#     its own clip_id + Drive upload ("<name> (part N)").
#
# RESPONSE (async enqueue — PR-BATCH-REGISTER-ASYNC):
#   { ok, total, enqueued_count, enqueue_failed, results:[{ClipID,Name,OK,
#     Error,Duplicate,JobID}] } — enqueued_count != "finished": poll
#   GET /api/jobs/{JobID} (or pass --wait) for the real per-clip outcome.
#
# WITH --wait the driver also prints a per-clip summary read from the job
# results (GET /api/jobs/{id}/full): name, requested window (end-start),
# clip_id, delivery status and drive_link. clip_id/drive_link/delivery come
# from result{}; the duration is NOT part of result{} — it is the requested
# window carried in the job payload (the catalog's own `duration` for the
# same clip equals end-start, e.g. 759→799 = 40s).
#
# USAGE
#   API_BASE=127.0.0.1:8000 VELOX_ADMIN_TOKEN=… \
#     bash tests/operational/register_clips_batch.sh --dry          # validate only
#   API_BASE=127.0.0.1:8000 VELOX_ADMIN_TOKEN=… \
#     bash tests/operational/register_clips_batch.sh                # all payloads
#   … register_clips_batch.sh --wait payloads/weeknd-goat-talk.register-batch.json
#
# The POST is never auto-retried: the endpoint is not idempotency-gated, so a
# re-post would enqueue the batch twice (observed: a duplicate set makes the
# second copy fail on an already-terminal outbox row while the first copy is
# already published). A 429 is reported instead.
#
# EXIT
#   0 every payload accepted (and, with --wait, every clip job terminal ok)
#   1 at least one payload/job failed
#   2 setup error (missing payload, bad JSON, missing token)
#   124 wall-clock/poll timeout

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)

# ── Driver flags MUST be consumed before sourcing lib/common.sh.
# `source` with no arguments leaves the CALLER's positional parameters in
# place, and that library parses "$@" at source time — exiting 2 on any flag
# it does not know, which would include our own --wait and every payload
# path. So: pull our flags out, keep everything the library accepts, and
# re-set "$@" to exactly that before sourcing.
DRIVER_DRY=0
DRIVER_WAIT=0
DRIVER_HELP=0
ALLOW_UNPUBLISHED=0
PAYLOADS=()
LIB_ARGS=()
for arg in "$@"; do
    case "$arg" in
        --dry) DRIVER_DRY=1; LIB_ARGS+=(--dry) ;;
        --wait) DRIVER_WAIT=1 ;;
        --allow-unpublished) ALLOW_UNPUBLISHED=1 ;;
        -h|--help) DRIVER_HELP=1; LIB_ARGS+=(-h) ;;
        -*) LIB_ARGS+=("$arg") ;; # unknown flag: let common.sh reject it
        *) PAYLOADS+=("$arg") ;;
    esac
done
set -- ${LIB_ARGS[@]+"${LIB_ARGS[@]}"}

# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

# common.sh flips DRY_RUN for --dry / SMOKE_DRY_RUN=1; keep the two views aligned.
if [[ "${DRY_RUN:-0}" == "1" ]]; then
    DRIVER_DRY=1
fi

PAYLOAD_DIR="$DIR/payloads"

usage() {
    printf 'usage: %s [--dry] [--wait] [--allow-unpublished] [payload.json ...]\n\n' "$(basename "$0")"
    printf '  --dry                 validate + summarize the payloads, send nothing\n'
    printf '  --wait                poll GET /api/jobs/{id} for every enqueued clip job\n'
    printf '  --allow-unpublished   do not fail the run when a clip has no drive_link\n'
    printf '  default               payloads/*.register-batch.json\n'
}

if (( DRIVER_HELP == 1 )); then
    usage
    exit 0
fi

if (( ${#PAYLOADS[@]} == 0 )); then
    shopt -s nullglob
    for f in "$PAYLOAD_DIR"/*.register-batch.json; do
        PAYLOADS+=("$f")
    done
    shopt -u nullglob
fi
if (( ${#PAYLOADS[@]} == 0 )); then
    printf '%ssetup error: no payloads found (pass a path or populate %s)%s\n' \
        "$RED" "$PAYLOAD_DIR" "$RESET" >&2
    exit 2
fi

smoke_require curl jq

# Per-clip summary rows (TSV) collected while --wait polls the jobs:
#   name  window_sec  clip_id  drive_link  delivery_status  duplicate  job_status
SUMMARY_FILE="$WORK_DIR/clip-summary.tsv"

# Clips requested but never published to Drive (no drive_link). Counted at the
# end so a LOCAL_ONLY row cannot pass unnoticed.
UNPUBLISHED_COUNT=0

# ── Per-payload local validation: the payload must be structurally sound
# BEFORE it burns an authenticated request. Mirrors the server-side binding
# guards (clips required, url required, end > start).
validate_payload() {
    local file="$1" bad
    if ! jq -e . "$file" >/dev/null 2>&1; then
        printf '%sFAIL: %s is not valid JSON%s\n' "$RED" "$file" "$RESET" >&2
        return 1
    fi
    if ! jq -e '(.clips | type) == "array" and (.clips | length) > 0' "$file" >/dev/null 2>&1; then
        printf '%sFAIL: %s has no clips[]%s\n' "$RED" "$file" "$RESET" >&2
        return 1
    fi
    bad=$(jq -r '[ .clips[]
                   | select((.url // "") == "" or (.name // "") == "")
                   | (.name // "<unnamed>") ] | join(", ")' "$file")
    if [ -n "$bad" ]; then
        printf '%sFAIL: %s has clips without url/name: %s%s\n' "$RED" "$file" "$bad" "$RESET" >&2
        return 1
    fi
    bad=$(jq -r '[ .clips[] | select((.end // 0) <= (.start // 0))
                   | "\(.name) [\(.start)-\(.end)]" ] | join(", ")' "$file")
    if [ -n "$bad" ]; then
        printf '%sFAIL: %s has non-positive windows: %s%s\n' "$RED" "$file" "$bad" "$RESET" >&2
        return 1
    fi
    return 0
}

summarize_payload() {
    jq -r --arg f "$(basename "$1")" '
        "  file: \($f)"
        + "\n  clips: \(.clips | length)"
        + "   sources: \((.clips | map(.url) | unique) | join(", "))"
        + "\n  window: \(.clips | map(.start) | min)s → \(.clips | map(.end) | max)s"
        + "   total: \(.clips | map(.end - .start) | add)s"
        + " (min \((.clips | map(.end - .start) | min))s / max \((.clips | map(.end - .start) | max))s)"
        + "\n  hooks: \(.clips | map(select((.hook // "") != "")) | length)"
        + "   segmented: \(.clips | map(select((.seconds_per_segment // 0) > 0)) | length)"
        + "\n  folder_id: \(.folder_id // "<none>")"' "$1"
}

overall_rc=0

# print_clip_summary renders the collected rows as a fixed-width table plus
# counters. A row whose drive_link is "-" (delivery LOCAL_ONLY / unpublished)
# is still printed — the honest state, never an invented link.
print_clip_summary() {
    printf '%s== per-clip summary (from job results) ==%s\n' "$CYAN" "$RESET"
    printf '%-46s %7s  %-32s %-11s %s\n' "CLIP" "SEC" "CLIP_ID" "DELIVERY" "DRIVE_LINK"
    awk -F'\t' '{
        name = $1
        if (length(name) > 44) name = substr(name, 1, 41) "..."
        printf "%-46s %7.1f  %-32s %-11s %s\n", name, $2 + 0, $3, $5, $4
    }' "$SUMMARY_FILE"
    awk -F'\t' '
        { total++
          if ($4 != "-") linked++
          if ($6 == "true") dup++
        }
        END {
            printf "%d clip: %d con drive_link, %d duplicati, %d senza link\n",
                total, linked + 0, dup + 0, total - linked
        }' "$SUMMARY_FILE"

    # A requested window that produced no Drive file is an incomplete result,
    # not a silent detail: LOCAL_ONLY means the local file exists but was never
    # uploaded, FAILED means the job itself died. Both are listed explicitly so
    # neither can hide behind the global counters.
    local unpub
    unpub=$(awk -F'\t' '$4 == "-"' "$SUMMARY_FILE")
    if [ -n "$unpub" ]; then
        UNPUBLISHED_COUNT=$(printf '%s\n' "$unpub" | grep -c .)
        printf '\n%s== UNPUBLISHED CLIPS (no drive_link) ==%s\n' "$YELLOW" "$RESET"
        printf '%-44s %-32s %-12s %s\n' "CLIP" "CLIP_ID" "DELIVERY" "JOB"
        printf '%s\n' "$unpub" | awk -F'\t' '{
            name = $1
            if (length(name) > 42) name = substr(name, 1, 39) "..."
            printf "%-44s %-32s %-12s %s\n", name, $3, ($5 == "" ? "-" : $5), $7
        }'
        printf '%s  -> LOCAL_ONLY: POST /api/media/clips/{source}/clips/<clip_id>/reupload (only while the local file still exists)%s\n' "$DIM" "$RESET"
        printf '%s  -> otherwise re-send that window with "force": true (skips the external-ref dedup; the index commit may still be refused by the completed outbox row)%s\n' "$DIM" "$RESET"
    else
        UNPUBLISHED_COUNT=0
    fi
    printf '\n'
}

for payload in "${PAYLOADS[@]}"; do
    printf '%s== %s%s\n' "$CYAN" "$payload" "$RESET"
    if ! validate_payload "$payload"; then
        overall_rc=1
        continue
    fi
    summarize_payload "$payload"

    if (( DRIVER_DRY == 1 )); then
        printf '%sdry-run: no request sent%s\n\n' "$DIM" "$RESET"
        continue
    fi

    # NO AUTO-RETRY on this POST. /api/media/register-batch is deliberately
    # NOT idempotency-gated (batch semantics: many distinct registrations per
    # call, each deduplicated later inside the registrar), so re-posting it
    # both enqueues a second set of jobs for the same windows and makes the
    # loser fail on already-terminal outbox rows. A 429 from the limiter is
    # answered before the batch executes, so the operator can simply re-run.
    smoke_curl POST "/api/media/register-batch" --data-binary "@${payload}" >/dev/null
    if [[ "$SMOKE_LAST_HTTP" == "429" ]]; then
        printf '%sFAIL: HTTP 429 (rate limited). NOT retried on purpose: this endpoint is not idempotency-gated, so a retry could enqueue the same batch twice. Wait %ss and re-run.%s\n\n' \
            "$RED" "${SMOKE_LAST_RETRY_AFTER:-60}" "$RESET"
        overall_rc=1
        continue
    fi

    if [[ "$SMOKE_LAST_HTTP" != "200" && "$SMOKE_LAST_HTTP" != "202" ]]; then
        printf '%sFAIL: HTTP %s — %s%s\n\n' "$RED" "$SMOKE_LAST_HTTP" "$(cat "$SMOKE_LAST_BODY")" "$RESET"
        overall_rc=1
        continue
    fi

    jq -r '
        "  enqueued: \(.enqueued_count)/\(.total)   enqueue_failed: \(.enqueue_failed)"
        + "\n" + ([.results[]?
                   | "    - \(.Name // .name // "<unnamed>"): job=\(.JobID // .job_id // "-")"
                     + (if (.Error // "") != "" then "  error=\(.Error)" else "" end)]
                  | join("\n"))' "$SMOKE_LAST_BODY"

    failed=$(jq -r '[.results[]? | select((.JobID // .job_id // "") == "" and (.Error // "") != "")] | length' "$SMOKE_LAST_BODY")
    if (( failed > 0 )); then
        overall_rc=1
    fi

    if (( DRIVER_WAIT == 1 )); then
        # enqueued != finished: the real outcome is the broker job state.
        for job in $(jq -r '[.results[]? | (.JobID // .job_id // empty)] | unique | .[]' "$SMOKE_LAST_BODY"); do
            printf '%s  waiting on job %s%s\n' "$DIM" "$job" "$RESET"
            if ! smoke_poll_terminal "$job"; then
                printf '%sFAIL: job %s did not reach a terminal state%s\n' "$RED" "$job" "$RESET"
                overall_rc=1
                continue
            fi
            status="${SMOKE_LAST_STATUS:-$(jq -r '.status // .state // "unknown"' "$SMOKE_LAST_BODY")}"
            printf '    %s -> %s\n' "$job" "$status"
            # The broker reports the terminal state upper-cased (SUCCEEDED);
            # normalize before matching so a successful job is not scored as
            # a failure.
            case "$(printf '%s' "$status" | tr '[:upper:]' '[:lower:]')" in
                completed|succeeded) ;;
                *) overall_rc=1 ;;
            esac

            # Collect the canonical per-clip result for the final summary.
            # /full is the same envelope read here (result.clip_id,
            # result.drive_link, result.delivery_status, result.duplicate);
            # the duration is the requested window from the job payload
            # because result{} carries no duration field.
            smoke_curl GET "/api/jobs/${job}/full" >/dev/null
            if smoke_rate_limit_backoff; then
                smoke_curl GET "/api/jobs/${job}/full" >/dev/null
            fi
            if [[ "$SMOKE_LAST_HTTP" == "200" ]]; then
                jq -r --arg job "$job" '[
                    (.job.payload.Name // .result.name // "<unnamed>"),
                    ((.job.payload.EndSec // 0) - (.job.payload.StartSec // 0)),
                    (.result.clip_id // "-"),
                    (.result.drive_link // "-"),
                    (.result.delivery_status // "-"),
                    ((.result.duplicate // false) | tostring),
                    (.job.status // "?")
                ] | @tsv' "$SMOKE_LAST_BODY" >> "$SUMMARY_FILE"
            else
                printf '%s    (could not read /full for %s: HTTP %s)%s\n' \
                    "$DIM" "$job" "$SMOKE_LAST_HTTP" "$RESET"
            fi
        done
    fi
    printf '\n'
done

if (( DRIVER_WAIT == 1 )) && [[ -s "$SUMMARY_FILE" ]]; then
    print_clip_summary
fi

if (( UNPUBLISHED_COUNT > 0 )) && (( ALLOW_UNPUBLISHED == 0 )); then
    printf '%sFAIL: %d clip(s) richieste senza drive_link (non pubblicate). Passa --allow-unpublished per accettarle.%s\n' \
        "$RED" "$UNPUBLISHED_COUNT" "$RESET"
    overall_rc=1
fi

if (( overall_rc != 0 )); then
    printf '%sFAIL: at least one payload or clip job failed%s\n' "$RED" "$RESET"
    exit 1
fi
if (( DRIVER_DRY == 1 )); then
    printf '%sOK: payloads validated (dry-run, nothing sent)%s\n' "$GREEN" "$RESET"
    exit 0
fi
printf '%sOK: every payload enqueued%s\n' "$GREEN" "$RESET"
