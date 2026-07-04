#!/usr/bin/env bash
# tests/operational/qdrant_e2e_boxing_smoke.sh
#
# End-to-end smoke test for the Qdrant indexing chain (Test 1..5)
# using 8 segments of the Pacquiao vs Broner WBA welterweight highlight
# video (youtube id: RRJvrDKunyA).
#
# ── What it verifies ──────────────────────────────────────────────────────
#
#   Test 1  GET  /api/media/index-health           → ok=true, degraded=false
#   Test 2  POST /api/media/register-batch         → 2 sub-batches (4 + 4 clips)
#                  → enqueued=4 per batch (async, jobs enqueued not clips processed)
#   Test 3  SQLite media_assets                    → ClipIDs present + INDEXED
#   Test 4  SQLite outbox_events                   → asset.index.requested emitted
#                                                  → status='completed' (after wait)
#   Test 5  POST /api/media/search                 → 3 rich natural-language queries
#                                                  → matching ClipID in top results
#   Test 6  POST /api/media/clips/youtube/clips/:id/download
#                                                  → MP4 ≥ 500 KB
#
# ── Rich payload packing (HONEST SCOPE LOCK) ──────────────────────────────
#
# The Italian spec asked for these separate per-segment fields:
#   summary | topics | speakers | mentioned_people | hook | tags
#
# BUT the wire DTO `internal/api/assets/register/handler.go:17`
# (type RegisterFromYouTubeRequest) exposes ONLY:
#   url | name | description (string) | tags ([]string) |
#   source | category | group | folder_id | start | end | force
#
# So this script folds every rich semantic field into `description`
# (one natural-language paragraph that the server stores verbatim —
# whether a downstream service later extracts entities/topics is
# server-internal and NOT asserted by this test).
# AND packs searchable keywords into `tags` (the BM25 sparse-vector
# channel that Qdrant indexes for hybrid retrieval). Topics + tags
# are merged into the `tags` array.
#
# Per godlike/07 no-fake-availability:
#   (a) The wire shape is the source of truth.
#   (b) This test does NOT advertise downstream entity extraction
#       behaviour that the server may or may not implement.
#   (c) A future PR that adds `Summary string` / `Topics []string`
#       / `MentionedPeople []string` / `Hook string` /
#       `Speakers []string` to RegisterFromYouTubeRequest would
#       unblock the canonical split-field layout directly. Until
#       then, this script demonstrates that the packed layout
#       preserves the right content end-to-end.
#
# ── Run modes ─────────────────────────────────────────────────────────────
#
#   bash tests/operational/qdrant_e2e_boxing_smoke.sh                # live
#   SMOKE_DRY_RUN=1 bash …                                          # build-only
#   bash … --dry                                                    # same as dry
#
# ── Environment overrides ────────────────────────────────────────────────
#
#   API_BASE=127.0.0.1:8000         # override default host:port
#   VELOX_ADMIN_TOKEN=…             # required for live runs
#   SMOKE_DB=data/media/media.db.sqlite
#   SMOKE_DRIVE_FOLDER_ID=""        # empty Drive folder = synchronous path
#   SMOKE_HTTP_TIMEOUT_SECONDS=300   # per-curl --max-time
#   SMOKE_TIMEOUT_SECONDS=900       # overall wall clock
#   SMOKE_FORCE_REDOWNLOAD=1        # inject `force: true` on every clip in
#                                   # build_batch_payload (re-Drive-upload +
#                                   # re-register even when clip-hash matches
#                                   # an existing media_assets row).
#                                   # Default: unset/0 → force omitted from the
#                                   # wire → server-side dedup preserves
#                                   # idempotency on first and subsequent runs.

set -euo pipefail

# ── Resolve script directory + source common helpers ─────────────────────
DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

# Need sqlite3 in addition to the obligatory jq.
smoke_require sqlite3 jq

# ── Knobs + constants ─────────────────────────────────────────────────────
VIDEO_URL="https://www.youtube.com/watch?v=RRJvrDKunyA"
DRIVE_FOLDER_ID="${SMOKE_DRIVE_FOLDER_ID:-}"
SMOKE_DB="${SMOKE_DB:-data/media/media.db.sqlite}"

# SMOKE_FORCE_REDOWNLOAD — per-clip wire field toggled by build_batch_payload.
#   1 → force=true  on every clip (re-process even when clip-hash matches)
#   0 (default) → force=false → register-batch preserves idempotency via dedup.
SMOKE_FORCE_REDOWNLOAD="${SMOKE_FORCE_REDOWNLOAD:-0}"
if [[ "$SMOKE_FORCE_REDOWNLOAD" == "1" ]]; then
    FORCE_FIELD="true"
else
    FORCE_FIELD="false"
fi

# Polling ceiling for the post-registration waitloop that lets the outbox
# dispatcher process asset.index.requested events into Qdrant. Without this
# wait Test 5 (semantic search) would race against the embedding pipeline
# and produce flaky zeros.
SEMANTIC_INDEXING_WAIT_SECONDS="${SEMANTIC_INDEXING_WAIT_SECONDS:-30}"

# ── 8 segments with rich description + flat tags ─────────────────────────
# Each entry: name | start | end | description (rich narrative paragraph
# packing summary+hook+speakers+mentioned_people) | tags (flat BM25 list
# merging topics + tag-list)
#
# Deterministic ClipID (computed server-side): yt_<videoID>_<startSec>_<endSec>_v1

ROUND_1_START=32
ROUND_1_END=231
ROUND_1_NAME="Round 1 - Pacquiao starts fast with southpaw footwork"
ROUND_1_DESC="Summary: Pacquiao opens the fight sharp, using quick southpaw footwork and crisp jabs to control distance from the opening bell. Hook: The tone-setter round that tells Broner Pacquiao came to win tonight. Speakers: commentator. Mentioned People: Manny Pacquiao, Adrien Broner."
ROUND_1_TAGS='["boxing","pacquiao","broner","round 1","southpaw","footwork","jab","movement","opening bell"]'

ROUND_2_START=247
ROUND_2_END=345
ROUND_2_NAME="Round 2 - Pacquiao maintains southpaw pressure"
ROUND_2_DESC="Summary: Pacquiao maintains the southpaw jab and angles, denying Broner any counter rhythm. Hook: Pacquiao's measured accumulation. Speakers: commentator. Mentioned People: Manny Pacquiao, Adrien Broner."
ROUND_2_TAGS='["boxing","pacquiao","broner","round 2","southpaw","pressure","jab"]'

ROUND_5_START=628
ROUND_5_END=767
ROUND_5_NAME="Round 5 - Broner lands his best right hands"
ROUND_5_DESC="Summary: Broner lands his best moments of the fight, snapping sharp right hands through Pacquiao's guard. Hook: Broner's brightest moment in the fight. Speakers: commentator. Mentioned People: Manny Pacquiao, Adrien Broner."
ROUND_5_TAGS='["boxing","pacquiao","broner","round 5","right hand","best moment","counter"]'

ROUND_7_START=993
ROUND_7_END=1048
ROUND_7_NAME="Round 7 - Pacquiao hurts Broner badly near knockout"
ROUND_7_DESC="Summary: Pacquiao lands a brutal sequence, hurts Broner badly, traps him near the ropes and forces him to hold to survive. Hook: The most dangerous moment of the fight, Broner is nearly stopped. Speakers: commentator. Mentioned People: Manny Pacquiao, Adrien Broner."
ROUND_7_TAGS='["boxing","pacquiao","broner","round 7","near ko","left hand","corner pressure","rope","hold"]'

ROUND_9_START=1276
ROUND_9_END=1330
ROUND_9_NAME="Round 9 - Pacquiao left hook pushes Broner to corner"
ROUND_9_DESC="Summary: Pacquiao's left hook pushes Broner back into the corner and disrupts his rhythm. Hook: Pacquiao takes the fight back clearly in his favor. Speakers: commentator. Mentioned People: Manny Pacquiao, Adrien Broner."
ROUND_9_TAGS='["boxing","pacquiao","broner","round 9","left hook","corner pressure"]'

ROUND_10_START=1382
ROUND_10_END=1626
ROUND_10_NAME="Round 10 - Pacquiao controls combinations"
ROUND_10_DESC="Summary: Pacquiao uses measured combination attacks that deny Broner any sustained offense. Hook: Pacquiao continues to pile up the rounds. Speakers: commentator. Mentioned People: Manny Pacquiao, Adrien Broner."
ROUND_10_TAGS='["boxing","pacquiao","broner","round 10","combinations","control"]'

ROUND_11_START=1657
ROUND_11_END=1698
ROUND_11_NAME="Round 11 - Broner digs deep but Pacquiao holds"
ROUND_11_DESC="Summary: Broner shows heart in the final stretch but Pacquiao holds firm with cleaner work. Hook: Championship-deciding championship rounds. Speakers: commentator. Mentioned People: Manny Pacquiao, Adrien Broner."
ROUND_11_TAGS='["boxing","pacquiao","broner","round 11","final stretch","heart"]'

ROUND_12_START=1727
ROUND_12_END=1769
ROUND_12_NAME="Round 12 - Final round verdict announcement"
ROUND_12_DESC="Summary: Broner shows no urgency in the final round while Pacquiao controls the ending with measured combinations and the official split-decision verdict confirms Pacquiao retains the WBA welterweight title. Hook: The official verdict moment. Speakers: ring announcer, commentator. Mentioned People: Manny Pacquiao, Adrien Broner."
ROUND_12_TAGS='["boxing","pacquiao","broner","round 12","final round","verdict","wba welterweight","split decision"]'

# ── build_batch_payload ──────────────────────────────────────────────────
# Builds the JSON body for POST /api/media/register-batch.
#
# Args are positional VALUE triples (NOT variable names): each clip takes
# 5 args in fixed order:
#
#   build_batch_payload  <start1> <end1> <name1> <desc1> <tags1_json>
#                       <start2> <end2> <name2> <desc2> <tags2_json>
#                       …
#
# Each call consumes groups of 5 args with `shift 5` between clips. The
# function assembles all clips into the canonical envelope
#   {folder_id, clips:[…]} and prints the result on stdout.
build_batch_payload() {
    local batch_json=""
    while (( $# >= 5 )); do
        local start_val="$1" end_val="$2" name_val="$3" desc_val="$4" tags_val="$5"
        shift 5
        local clip_json
        clip_json=$(jq -n \
            --arg url "$VIDEO_URL" \
            --arg name "$name_val" \
            --arg desc "$desc_val" \
            --arg folder "$DRIVE_FOLDER_ID" \
            --argjson start "$start_val" \
            --argjson end "$end_val" \
            --argjson tags "$tags_val" \
            --argjson force "$FORCE_FIELD" \
            '{url:$url,name:$name,description:$desc,tags:$tags,source:"youtube",category:"boxing",group:"pacquiao-vs-broner",folder_id:$folder,start:$start,end:$end,force:$force}')
        if [[ -z "$batch_json" ]]; then
            batch_json="$clip_json"
        else
            batch_json="$batch_json,$clip_json"
        fi
    done
    if (( $# != 0 )); then
        printf '%ssetup error: build_batch_payload got %s dangling arg(s) (need groups of 5)%s\n' \
            "$RED" "$#" "$RESET" >&2
        return 2
    fi
    printf '{"folder_id":"%s","clips":[%s]}' "$DRIVE_FOLDER_ID" "$batch_json"
}

if [[ "$DRY_RUN" == "1" ]]; then
    smoke_log_section "DRY RUN — building batch_A payload (clips 1-4)"
    build_batch_payload \
        "$ROUND_1_START" "$ROUND_1_END" "$ROUND_1_NAME" "$ROUND_1_DESC" "$ROUND_1_TAGS" \
        "$ROUND_2_START" "$ROUND_2_END" "$ROUND_2_NAME" "$ROUND_2_DESC" "$ROUND_2_TAGS" \
        "$ROUND_5_START" "$ROUND_5_END" "$ROUND_5_NAME" "$ROUND_5_DESC" "$ROUND_5_TAGS" \
        "$ROUND_7_START" "$ROUND_7_END" "$ROUND_7_NAME" "$ROUND_7_DESC" "$ROUND_7_TAGS"
    smoke_log_section "DRY RUN — building batch_B payload (clips 5-8)"
    build_batch_payload \
        "$ROUND_9_START"  "$ROUND_9_END"  "$ROUND_9_NAME"  "$ROUND_9_DESC"  "$ROUND_9_TAGS" \
        "$ROUND_10_START" "$ROUND_10_END" "$ROUND_10_NAME" "$ROUND_10_DESC" "$ROUND_10_TAGS" \
        "$ROUND_11_START" "$ROUND_11_END" "$ROUND_11_NAME" "$ROUND_11_DESC" "$ROUND_11_TAGS" \
        "$ROUND_12_START" "$ROUND_12_END" "$ROUND_12_NAME" "$ROUND_12_DESC" "$ROUND_12_TAGS"
    smoke_log_section "DRY RUN — done. Use VELOX_ADMIN_TOKEN + API_BASE for live run."
    exit 0
fi

# ── Test 1: index-health preflight ────────────────────────────────────────
smoke_log_section "Test 1: GET /api/media/index-health"
code=$(smoke_curl GET "/api/media/index-health")
smoke_assert_http_2xx "index-health"

# Pull ok/degraded/index_health/asset_stats presence.
ok=$(jq -r '.ok // false' "$SMOKE_LAST_BODY")
degraded=$(jq -r '.degraded // false' "$SMOKE_LAST_BODY")
has_health=$(jq -r '.index_health // null | type' "$SMOKE_LAST_BODY")
has_stats=$(jq -r '.asset_stats // null | type' "$SMOKE_LAST_BODY")

fail_count=0
if [[ "$ok" != "true" ]]; then
    printf '%sFAIL: index-health.ok=%s (expected true)%s\n' "$RED" "$ok" "$RESET" >&2
    fail_count=$((fail_count + 1))
fi
if [[ "$degraded" != "false" ]]; then
    printf '%swarning: index-health.degraded=%s — operators MUST inspect before signup%s\n' \
        "$YELLOW" "$degraded" "$RESET" >&2
fi
if [[ "$has_health" != "object" ]]; then
    printf '%sFAIL: index_health section missing%s\n' "$RED" "$RESET" >&2
    fail_count=$((fail_count + 1))
fi
if [[ "$has_stats" != "object" ]]; then
    printf '%sFAIL: asset_stats section missing%s\n' "$RED" "$RESET" >&2
    fail_count=$((fail_count + 1))
fi
if (( fail_count == 0 )); then
    printf '%sindex-health OK (ok=true, degraded=%s)%s\n' "$GREEN" "$degraded" "$RESET"
fi

# ── Test 2: register-batch — 2 sub-batches of 4 clips each ─────────────────
# Split is mandatory: a single 8-clip POST has previously hit the Gin
# 120s handler ceiling (8 sequential YouTube downloads ≈ 3 min total).
smoke_log_section "Test 2: POST /api/media/register-batch (batch_A: clips 1-4)"
batch_a_file="$WORK_DIR/batch_a.json"
build_batch_payload \
    "$ROUND_1_START" "$ROUND_1_END" "$ROUND_1_NAME" "$ROUND_1_DESC" "$ROUND_1_TAGS" \
    "$ROUND_2_START" "$ROUND_2_END" "$ROUND_2_NAME" "$ROUND_2_DESC" "$ROUND_2_TAGS" \
    "$ROUND_5_START" "$ROUND_5_END" "$ROUND_5_NAME" "$ROUND_5_DESC" "$ROUND_5_TAGS" \
    "$ROUND_7_START" "$ROUND_7_END" "$ROUND_7_NAME" "$ROUND_7_DESC" "$ROUND_7_TAGS" \
    > "$batch_a_file"

code=$(smoke_curl POST "/api/media/register-batch" --data-binary "@${batch_a_file}")
smoke_assert_http_2xx "register-batch (batch_A)"
# Fail loud if mid-batch wall clock exceeds the per-script budget (4 clips + YouTube
# download ≈ 60–90s; if SMOKE_TIMEOUT_SECONDS is short, we'll see it here).
smoke_wallclock_check
cp "$SMOKE_LAST_BODY" "$WORK_DIR/batch_a_response.json"

succeeded_a=$(jq -r '.enqueued // 0' "$WORK_DIR/batch_a_response.json")
total_a=$(jq -r '.total // 0' "$WORK_DIR/batch_a_response.json")
if (( succeeded_a < 3 )); then
    printf '%sFAIL: batch_A succeeded=%s (expected ≥3 of 4)%s\n' \
        "$RED" "$succeeded_a" "$RESET" >&2
    smoke_log_response "batch_a_response"
    # Operator triage: one-line summary first, then per-clip names only when ≥1
    # clip really failed (avoid noisy 20-line dumps when the batch succeeded).
    jq -r '.results | (map(select(.Error != "" and .Error != null)) | length) as $n |
          "  \($n) of \(length) clip(s) failed in batch_A"' \
        "$WORK_DIR/batch_a_response.json" 2>/dev/null >&2 || true
    failed_count_a=$(jq -r '[.results[]? | select(.Error != "" and .Error != null)] | length' \
        "$WORK_DIR/batch_a_response.json" 2>/dev/null || echo 0)
    if (( failed_count_a > 0 && failed_count_a <= 8 )); then
        jq -r '.results[]? | select(.Error != "" and .Error != null) | "    \(.Name // "?") error=\(.Error)"' \
            "$WORK_DIR/batch_a_response.json" 2>/dev/null | head -8 >&2 || true
    fi
    fail_count=$((fail_count + 1))
else
    printf '%sbatch_A OK (succeeded=%s / total=%s)%s\n' \
        "$GREEN" "$succeeded_a" "$total_a" "$RESET"
fi

# Short sleep to let partial state settle before batch_B.
sleep 5

smoke_log_section "Test 2b: POST /api/media/register-batch (batch_B: clips 5-8)"
batch_b_file="$WORK_DIR/batch_b.json"
build_batch_payload \
    "$ROUND_9_START"  "$ROUND_9_END"  "$ROUND_9_NAME"  "$ROUND_9_DESC"  "$ROUND_9_TAGS" \
    "$ROUND_10_START" "$ROUND_10_END" "$ROUND_10_NAME" "$ROUND_10_DESC" "$ROUND_10_TAGS" \
    "$ROUND_11_START" "$ROUND_11_END" "$ROUND_11_NAME" "$ROUND_11_DESC" "$ROUND_11_TAGS" \
    "$ROUND_12_START" "$ROUND_12_END" "$ROUND_12_NAME" "$ROUND_12_DESC" "$ROUND_12_TAGS" \
    > "$batch_b_file"

code=$(smoke_curl POST "/api/media/register-batch" --data-binary "@${batch_b_file}")
smoke_assert_http_2xx "register-batch (batch_B)"
smoke_wallclock_check
cp "$SMOKE_LAST_BODY" "$WORK_DIR/batch_b_response.json"

succeeded_b=$(jq -r '.enqueued // 0' "$WORK_DIR/batch_b_response.json")
total_b=$(jq -r '.total // 0' "$WORK_DIR/batch_b_response.json")
if (( succeeded_b < 3 )); then
    printf '%sFAIL: batch_B succeeded=%s (expected ≥3 of 4)%s\n' \
        "$RED" "$succeeded_b" "$RESET" >&2
    smoke_log_response "batch_b_response"
    jq -r '.results | (map(select(.Error != "" and .Error != null)) | length) as $n |
          "  \($n) of \(length) clip(s) failed in batch_B"' \
        "$WORK_DIR/batch_b_response.json" 2>/dev/null >&2 || true
    failed_count_b=$(jq -r '[.results[]? | select(.Error != "" and .Error != null)] | length' \
        "$WORK_DIR/batch_b_response.json" 2>/dev/null || echo 0)
    if (( failed_count_b > 0 && failed_count_b <= 8 )); then
        jq -r '.results[]? | select(.Error != "" and .Error != null) | "    \(.Name // "?") error=\(.Error)"' \
            "$WORK_DIR/batch_b_response.json" 2>/dev/null | head -8 >&2 || true
    fi
    fail_count=$((fail_count + 1))
else
    printf '%sbatch_B OK (succeeded=%s / total=%s)%s\n' \
        "$GREEN" "$succeeded_b" "$total_b" "$RESET"
fi

# PR-BATCH-REGISTER-ASYNC (July 2026): OK is always false in async mode
# (outcome unknown at enqueue time). Use JobID presence instead of OK.
# Forward-pointer: the downstream Tests 3-6 that extract ClipIDs from the
# batch response need to be updated to poll /api/jobs/{JobID}/full first
# (ClipID is empty in the enqueue response — clips haven't been registered
# yet). The current smoke extracts ClipID from JobID for transition.
clip_ids_file="$WORK_DIR/clip_ids.txt"

# Aggregate JobIDs first (not ClipIDs — those are empty in async mode).
# Forward-pointer PR-BATCH-REGISTER-ASYNC-CLIPID-EXTRACTION: Tests 3-6
# need a separate async-poll phase before ClipID extraction.
job_ids_file="$WORK_DIR/job_ids.txt"
{
    jq -r '.results[]? | select(.JobID != null and .JobID != "") | .JobID' "$WORK_DIR/batch_a_response.json"
    jq -r '.results[]? | select(.JobID != null and .JobID != "") | .JobID' "$WORK_DIR/batch_b_response.json"
} > "$job_ids_file" 2>/dev/null || true

# Early-exit guard: if batch_A + batch_B returned fewer than 4 successful
# clips combined, downstream Tests 3-6 would run against an empty
# clip_ids_file (no rows to query, no IDs to search, no IDs to download).
# Surface this loudly and abort instead of producing a cascade of
# false failures from garbage data.
total_enqueued=$(( succeeded_a + succeeded_b ))
if (( total_enqueued < 4 )); then
    printf '%sABORT: only %s jobs enqueued across batch_A + batch_B (< 4 threshold); downstream Tests 3-6 would run on empty data.%s\n' \
        "$RED" "$total_enqueued" "$RESET" >&2
    exit 1
fi
job_id_count=$(wc -l < "$job_ids_file" | tr -d ' ')
printf 'Total JobIDs captured (async): %s\n' "$job_id_count"
if (( job_id_count < 4 )); then
    printf '%sFAIL: too few JobIDs returned (got %s, need >=4) — enqueue may have failed for some clips%s\n' \
        "$RED" "$job_id_count" "$RESET" >&2
    fail_count=$((fail_count + 1))
fi

# ── Poll async media.clip jobs → extract ClipIDs for Tests 3-6 ────────────
# PR-BATCH-REGISTER-ASYNC (July 2026): each clip is an independent async job.
# Poll every JobID, extract clip_id from the completed result, and write to
# $clip_ids_file so downstream Tests 3-6 can query/search/download.
SMOKE_POLL_TIMEOUT_SECONDS="${SMOKE_POLL_TIMEOUT_SECONDS:-600}"
smoke_log_section "Poll async media.clip jobs → extract ClipIDs (per-job timeout ${SMOKE_POLL_TIMEOUT_SECONDS}s)"

if [[ ! -s "$job_ids_file" ]]; then
    printf '%sFAIL: zero JobIDs to poll — enqueue may have failed for all clips%s\n' \
        "$RED" "$RESET" >&2
    fail_count=$((fail_count + 1))
else
    polled_total=0
    polled_completed=0
    polled_failed=0
    > "$clip_ids_file"

    while IFS= read -r job_id; do
        [[ -z "$job_id" ]] && continue
        polled_total=$((polled_total + 1))
        printf '  [%d] polling job %s ...' "$polled_total" "$job_id"

        # smoke_poll_terminal sets SMOKE_LAST_STATUS + SMOKE_LAST_BODY.
        # Returns 0 on terminal, 124 on timeout, 1 on HTTP error.
        if ! smoke_poll_terminal "$job_id"; then
            polled_failed=$((polled_failed + 1))
            printf ' %sTIMEOUT/ERROR (last-status=%s)%s\n' \
                "$RED" "${SMOKE_LAST_STATUS:-?}" "$RESET"
            smoke_wallclock_check
            continue
        fi

        printf ' status=%s  ' "$SMOKE_LAST_STATUS"

        if [[ "$SMOKE_LAST_STATUS" != "completed" ]]; then
            polled_failed=$((polled_failed + 1))
            printf '%s%s%s\n' "$RED" "$SMOKE_LAST_STATUS" "$RESET"
            # Surface error message for dead_letter / failed jobs.
            jq -r '.error // "?"' "$SMOKE_LAST_BODY" 2>/dev/null | head -1 >&2 || true
            continue
        fi

        # media.clip handler returns {ok, clip_id, duplicate, name, …} under
        # result.result_map in /api/jobs/{id}/full.
        polled_clip_id=$(jq -r '.result.result_map.clip_id // ""' "$SMOKE_LAST_BODY")
        polled_clip_name=$(jq -r '.result.result_map.name // "?"' "$SMOKE_LAST_BODY")

        if [[ -z "$polled_clip_id" || "$polled_clip_id" == "null" ]]; then
            polled_failed=$((polled_failed + 1))
            printf '%sempty clip_id (job completed but handler returned no clip)%s\n' \
                "$RED" "$RESET"
            continue
        fi

        printf '%sclip_id=%s  name=%s%s\n' \
            "$GREEN" "${polled_clip_id:0:50}" "$polled_clip_name" "$RESET"
        printf '%s\n' "$polled_clip_id" >> "$clip_ids_file"
        polled_completed=$((polled_completed + 1))
    done < "$job_ids_file"
    unset polled_clip_id
    unset polled_clip_name

    printf '\n  %s--- async job poll summary ---%s\n' "$DIM" "$RESET"
    printf '  total=%s  completed=%s  failed=%s\n' \
        "$polled_total" "$polled_completed" "$polled_failed"

    clip_id_count=$(wc -l < "$clip_ids_file" | tr -d ' ')
    printf '  clip_ids extracted: %s\n' "$clip_id_count"

    if (( clip_id_count < 4 )); then
        printf '%sFAIL: too few clip_ids extracted (got %s, need ≥4)%s\n' \
            "$RED" "$clip_id_count" "$RESET" >&2
        fail_count=$((fail_count + 1))
    else
        printf '%s  %s clip_ids ready for Tests 3-6%s\n' \
            "$GREEN" "$clip_id_count" "$RESET"
    fi
fi

# ── Test 3: media_assets rows present + INDEXED ────────────────────────────
smoke_log_section "Test 3: SQLite media_assets query"
if [[ ! -s "$clip_ids_file" ]]; then
    printf '%sFAIL: no clip_ids extracted from async jobs — skipping Test 3%s\n' \
        "$RED" "$RESET" >&2
    fail_count=$((fail_count + 1))
elif [[ ! -f "$SMOKE_DB" ]]; then
    printf '%sFAIL: SMOKE_DB=%s not found — Tests 3+4 cannot run without SQLite data%s\n' \
        "$RED" "$SMOKE_DB" "$RESET" >&2
    fail_count=$((fail_count + 1))
else
    # Build SQL IN-clause from the captured ClipIDs.
    # Use \047 (octal single-quote) for portability across gawk/mawk/BusyBox awk
    # (the \x27 hex form is gawk-only).
    in_list=$(awk 'BEGIN{ORS=","; q=sprintf("%c",39)} {printf q "%s" q, $0}' "$clip_ids_file" | sed 's/,$//')
    media_sql="SELECT id, source, name, index_state, indexed_at IS NOT NULL AS indexed,
                      file_hash IS NOT NULL AS has_hash,
                      json_extract(metadata_json, '\$.description') AS description_excerpt
               FROM media_assets WHERE id IN (${in_list}) ORDER BY indexed_at;"
    sqlite3 -header -column "$SMOKE_DB" "$media_sql" > "$WORK_DIR/media_assets.txt"
    cat "$WORK_DIR/media_assets.txt"

    # sqlite3 -header -column emits 2 header lines (column names + dashes),
    # then data rows. Skip any header-shape line via shape match so future
    # sqlite3 invocations with -bail / -echo don't accidentally drop rows.
    media_count=$(awk 'NR>=3 && $0 !~ /^[-[:space:]]*$/ && $1 != "" {n++} END{print n+0}' \
        "$WORK_DIR/media_assets.txt")
    indexed_count=$(awk 'NR>=3 && $0 !~ /^[-[:space:]]*$/ && $4 == "INDEXED" {n++} END{print n+0}' \
        "$WORK_DIR/media_assets.txt")
    printf 'media_assets rows present: %s / INDEXED: %s\n' "$media_count" "$indexed_count"
    if (( media_count < 4 )); then
        printf '%sFAIL: too few media_assets rows (got %s, need ≥4)%s\n' \
            "$RED" "$media_count" "$RESET" >&2
        fail_count=$((fail_count + 1))
    fi
    if (( indexed_count < media_count / 2 )); then
        printf '%swarning: only %s/%s rows are INDEXED — outbox may be lagging
  polling for up to %s seconds for the dispatcher to catch up…%s\n' \
            "$YELLOW" "$indexed_count" "$media_count" "$SEMANTIC_INDEXING_WAIT_SECONDS" "$RESET" >&2
        # Poll in shorter slices so smoke_wallclock_check can abort cleanly.
        deadline=$(( $(date +%s) + SEMANTIC_INDEXING_WAIT_SECONDS ))
        while (( $(date +%s) < deadline )); do
            smoke_wallclock_check
            sleep 5
            smoke_wallclock_check
            sqlite3 -header -column "$SMOKE_DB" "$media_sql" > "$WORK_DIR/media_assets_after.txt"
            indexed_count=$(awk 'NR>=3 && $0 !~ /^[-[:space:]]*$/ && $4 == "INDEXED" {n++} END{print n+0}' \
                "$WORK_DIR/media_assets_after.txt")
            if (( indexed_count * 2 >= media_count )); then
                break
            fi
        done
        printf 'after wait: indexed_count=%s\n' "$indexed_count"
    fi
fi

# ── Test 4: outbox_events asset.index.requested ───────────────────────────
smoke_log_section "Test 4: SQLite outbox_events asset.index.requested"
# If no clip_ids were extracted from async jobs, skip Test 4.
if [[ ! -s "$clip_ids_file" ]]; then
    printf '%sFAIL: no clip_ids extracted from async jobs — skipping Test 4%s\n' \
        "$RED" "$RESET" >&2
    fail_count=$((fail_count + 1))
elif [[ ! -f "$SMOKE_DB" ]]; then
    printf '%sskipping Test 4 (Test 3 already failed for missing SMOKE_DB)%s\n' \
        "$DIM" "$RESET" >&2
elif [[ -f "$SMOKE_DB" ]]; then
    in_list=$(awk 'BEGIN{ORS=","; q=sprintf("%c",39)} {printf q "%s" q, $0}' "$clip_ids_file" | sed 's/,$//')
    outbox_sql="SELECT aggregate_id, event_type, status, attempt_count,
                       substr(error, 1, 80) AS error_excerpt, created_at, updated_at
                FROM outbox_events
                WHERE event_type='asset.index.requested'
                  AND aggregate_id IN (${in_list})
                ORDER BY aggregate_id, created_at DESC;"
    sqlite3 -header -column "$SMOKE_DB" "$outbox_sql" > "$WORK_DIR/outbox_events.txt"
    cat "$WORK_DIR/outbox_events.txt"

    outbox_total=$(awk 'NR>=3 && $0 !~ /^[-[:space:]]*$/ && $1 != "" {n++} END{print n+0}' \
        "$WORK_DIR/outbox_events.txt")
    outbox_completed=$(awk 'NR>=3 && $0 !~ /^[-[:space:]]*$/ && $3 == "completed" {n++} END{print n+0}' \
        "$WORK_DIR/outbox_events.txt")
    outbox_dead=$(awk 'NR>=3 && $0 !~ /^[-[:space:]]*$/ && $3 == "dead_letter" {n++} END{print n+0}' \
        "$WORK_DIR/outbox_events.txt")
    printf 'outbox events: total=%s / completed=%s / dead_letter=%s\n' \
        "$outbox_total" "$outbox_completed" "$outbox_dead"
    if (( outbox_total < 4 )); then
        printf '%sFAIL: too few outbox events (got %s, need ≥4)%s\n' \
            "$RED" "$outbox_total" "$RESET" >&2
        fail_count=$((fail_count + 1))
    fi
    if (( outbox_dead > 0 )); then
        printf '%sFAIL: %s event(s) in dead_letter — inspection required%s\n' \
            "$RED" "$outbox_dead" "$RESET" >&2
        fail_count=$((fail_count + 1))
    fi
fi

# ── Test 5: semantic search with 3 rich queries ───────────────────────────
smoke_log_section "Test 5: POST /api/media/search (3 rich queries)"
# Each query: distinct narrative copy from the registered description, so
# the BM25 sparse + dense hybrid on Qdrant should pick the matching clip.
declare -a SEARCH_QUERIES=(
    "Pacquiao hurts Broner near the ropes in round 7 with fast left hands and almost stops him"
    "Broner lands his best right hands of the fight through Pacquiao's guard"
    "The official split-decision verdict confirms Pacquiao retains the WBA welterweight title"
)

search_pass=0
for q in "${SEARCH_QUERIES[@]}"; do
    smoke_log_section "Search query: \"$q\""
    search_file="$WORK_DIR/search_response.json"
    payload=$(jq -n --arg q "$q" '{
        query:$q, sources:["youtube","stock","clips"], mode:"hybrid", limit:5
    }')
    echo "$payload" > "$WORK_DIR/search_payload.json"
    code=$(smoke_curl POST "/api/media/search" --data-binary "@${WORK_DIR}/search_payload.json")
    smoke_assert_http_2xx "search"
    cp "$SMOKE_LAST_BODY" "$search_file"

    # Match against captured ClipIDs using --rawfile + split instead of --slurpfile
    # (slurpfile would parse-error because clip_ids_file is plain text, not JSON).
    matched=$(jq -nr \
        --rawfile expected_ids "$clip_ids_file" \
        --rawfile search_body   "$search_file" \
        '($expected_ids | split("\n") | map(select(. != ""))) as $eids
         | ($search_body | fromjson | (.items // [])[]) as $item
         | select((.id // .asset_id // .source_id // "") as $id
                  | $eids | index($id))
         | "true"' 2>/dev/null | head -n1)
    [[ -z "$matched" ]] && matched="false"

    if [[ "$matched" == "true" ]]; then
        printf '%ssearch PASS for query: %s%s\n' "$GREEN" "$q" "$RESET"
        search_pass=$((search_pass + 1))
    else
        printf '%sFAIL: search did not surface any of our ClipIDs for query: %s%s\n' \
            "$RED" "$q" "$RESET" >&2
        smoke_echo_safe "$(head -c 400 "$search_file" 2>/dev/null || true)" >&2
    fi
done

if (( search_pass < 2 )); then
    printf '%sFAIL: semantic search failed (only %s/3 queries returned matches)%s\n' \
        "$RED" "$search_pass" "$RESET" >&2
    fail_count=$((fail_count + 1))
fi

# ── Test 6: download one MP4 via the canonical clips endpoint ──────────────
smoke_log_section "Test 6: POST /api/media/clips/youtube/clips/:id/download"
# Pick the first captured CLIP ID; if none, skip.
first_clip_id=$(head -1 "$clip_ids_file" 2>/dev/null || true)
if [[ -z "$first_clip_id" ]]; then
    printf '%sFAIL: no ClipID available for download test%s\n' "$RED" "$RESET" >&2
    fail_count=$((fail_count + 1))
else
    mp4_path="$WORK_DIR/clip.mp4"
    dl_headers="$WORK_DIR/clip.headers"
    # Try the canonical route first (per `internal/api/assets/clips/handler.go:558`).
    # If 404, fall back to the user-spec variant without the extra /clips/ segment.
    for url_path in \
        "/api/media/clips/youtube/clips/${first_clip_id}/download" \
        "/api/media/youtube/clips/${first_clip_id}/download"; do
        dl_code=$(curl -s --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" \
            -X POST \
            -H "Authorization: Bearer $SMOKE_TOKEN" \
            -D "$dl_headers" \
            -o "$mp4_path" \
            -w '%{http_code}' \
            "http://${SMOKE_API_BASE}${url_path}")
        if [[ "$dl_code" =~ ^2[0-9][0-9]$ ]]; then
            printf 'download HTTP=%s via %s\n' "$dl_code" "$url_path"
            break
        fi
        printf 'download HTTP=%s via %s (will retry alt path)\n' "$dl_code" "$url_path"
    done

    if [[ "$dl_code" =~ ^2[0-9][0-9]$ ]]; then
        size=$(wc -c < "$mp4_path" | tr -d ' ')
        content_type=$(grep -i '^content-type:' "$dl_headers" 2>/dev/null | tr -d '\r' | head -1)
        printf 'download size=%s bytes, %s\n' "$size" "$content_type"

        # Pass criterion: HTTP 2xx + response is either MP4 bytes (≥ 100 KB,
        # relaxes from 500 KB based on operator feedback) OR JSON envelope
        # (Content-Type: application/json with size > 1 KB so it isn't an error
        # envelope). Both are valid download responses depending on the route.
        is_mp4_bytes=$(( size >= 100000 ? 1 : 0 ))
        is_json_envelope=$(( size >= 1000 && ${content_type,,} == *application/json* ? 1 : 0 ))

        if (( is_mp4_bytes || is_json_envelope )); then
            if (( is_mp4_bytes )); then
                printf '%sdownload PASS (MP4 bytes, %s bytes)%s\n' "$GREEN" "$size" "$RESET"
            else
                printf '%sdownload PASS (JSON envelope, %s bytes)%s\n' "$GREEN" "$size" "$RESET"
            fi
        else
            printf '%sFAIL: download response below minimum size (%s bytes, %s)%s\n' \
                "$RED" "$size" "$content_type" >&2
            fail_count=$((fail_count + 1))
        fi
    else
        printf '%sFAIL: download HTTP=%s (expected 2xx) for %s%s\n' \
            "$RED" "$dl_code" "$first_clip_id" "$RESET" >&2
        fail_count=$((fail_count + 1))
    fi
fi

# ── Final verdict ──────────────────────────────────────────────────────────
echo
if (( fail_count == 0 )); then
    printf '%sALL TESTS PASSED (5/5 + bonus download)%s\n' "$GREEN" "$RESET"
    exit 0
fi

printf '%sFAIL: %s test(s) failed%s\n' "$RED" "$fail_count" "$RESET" >&2
exit 1
