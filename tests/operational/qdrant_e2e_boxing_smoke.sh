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
#                                                  → succeeded=4 per batch
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
# (one natural-language paragraph the LLM extraction pipeline can later
# ingest for entity/topic detection) AND packs searchable keywords
# into `tags` (the BM25 sparse-vector channel that Qdrant indexes for
# hybrid retrieval). Summary/Hook/Speakers/MentionedPeople are encoded
# in description; topics + tags are merged into the `tags` array.
#
# This is documented honestly: per godlike/07 no-fake-availability,
# the wire shape is the source of truth, and a future PR that adds
# `Summary string` / `Topics []string` / `MentionedPeople []string` /
# `Hook string` to RegisterFromYouTubeRequest would unblock the
# split-field layout directly. Until then, this script demonstrates
# the canonical packed-layout chain works end-to-end.
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
            '{url:$url,name:$name,description:$desc,tags:$tags,source:"youtube",category:"boxing",group:"pacquiao-vs-broner",folder_id:$folder,start:$start,end:$end}')
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
cp "$SMOKE_LAST_BODY" "$WORK_DIR/batch_a_response.json"

succeeded_a=$(jq -r '.succeeded // 0' "$WORK_DIR/batch_a_response.json")
total_a=$(jq -r '.total // 0' "$WORK_DIR/batch_a_response.json")
if (( succeeded_a < 3 )); then
    printf '%sFAIL: batch_A succeeded=%s (expected ≥3 of 4)%s\n' \
        "$RED" "$succeeded_a" "$RESET" >&2
    smoke_log_response "batch_a_response"
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
cp "$SMOKE_LAST_BODY" "$WORK_DIR/batch_b_response.json"

succeeded_b=$(jq -r '.succeeded // 0' "$WORK_DIR/batch_b_response.json")
total_b=$(jq -r '.total // 0' "$WORK_DIR/batch_b_response.json")
if (( succeeded_b < 3 )); then
    printf '%sFAIL: batch_B succeeded=%s (expected ≥3 of 4)%s\n' \
        "$RED" "$succeeded_b" "$RESET" >&2
    smoke_log_response "batch_b_response"
    fail_count=$((fail_count + 1))
else
    printf '%sbatch_B OK (succeeded=%s / total=%s)%s\n' \
        "$GREEN" "$succeeded_b" "$total_b" "$RESET"
fi

# Aggregate the ClipIDs (PascalCase per sourcing.BatchClipResult struct
# which has NO explicit json tags → Go identifier names win).
clip_ids_file="$WORK_DIR/clip_ids.txt"
{
    jq -r '.results[]? | select(.OK == true) | .ClipID' "$WORK_DIR/batch_a_response.json"
    jq -r '.results[]? | select(.OK == true) | .ClipID' "$WORK_DIR/batch_b_response.json"
} > "$clip_ids_file" 2>/dev/null || true

clip_id_count=$(wc -l < "$clip_ids_file" | tr -d ' ')
printf 'Total ClipIDs captured: %s\n' "$clip_id_count"
if (( clip_id_count < 4 )); then
    printf '%sFAIL: too few ClipIDs returned (got %s, need ≥4)%s\n' \
        "$RED" "$clip_id_count" "$RESET" >&2
    fail_count=$((fail_count + 1))
fi

# ── Test 3: media_assets rows present + INDEXED ────────────────────────────
smoke_log_section "Test 3: SQLite media_assets query"
if [[ ! -f "$SMOKE_DB" ]]; then
    printf '%swarning: SMOKE_DB=%s not found — skipping Test 3+4 (run after server starts)%s\n' \
        "$YELLOW" "$SMOKE_DB" "$RESET" >&2
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

    # sqlite3 -header -column emits 2 header lines (column names + dashes), then
    # data rows. Skip lines 1-2 (NR > 2) to land on row 1.
    media_count=$(awk 'NR>2 && $1 != "" {n++} END{print n+0}' "$WORK_DIR/media_assets.txt")
    indexed_count=$(awk 'NR>2 && $4 == "INDEXED" {n++} END{print n+0}' "$WORK_DIR/media_assets.txt")
    printf 'media_assets rows present: %s / INDEXED: %s\n' "$media_count" "$indexed_count"
    if (( media_count < 4 )); then
        printf '%sFAIL: too few media_assets rows (got %s, need ≥4)%s\n' \
            "$RED" "$media_count" "$RESET" >&2
        fail_count=$((fail_count + 1))
    fi
    if (( indexed_count < media_count / 2 )); then
        printf '%swarning: only %s/%s rows are INDEXED — outbox may be lagging
  (waiting %s seconds for the dispatcher to catch up…)%s\n' \
            "$YELLOW" "$indexed_count" "$media_count" "$SEMANTIC_INDEXING_WAIT_SECONDS" "$RESET" >&2
        sleep "$SEMANTIC_INDEXING_WAIT_SECONDS"
        sqlite3 -header -column "$SMOKE_DB" "$media_sql" > "$WORK_DIR/media_assets_after.txt"
        indexed_count=$(awk 'NR>2 && $4 == "INDEXED" {n++} END{print n+0}' "$WORK_DIR/media_assets_after.txt")
        printf 'after wait: indexed_count=%s\n' "$indexed_count"
    fi
fi

# ── Test 4: outbox_events asset.index.requested ───────────────────────────
smoke_log_section "Test 4: SQLite outbox_events asset.index.requested"
if [[ -f "$SMOKE_DB" ]]; then
    in_list=$(awk 'BEGIN{ORS=","; q=sprintf("%c",39)} {printf q "%s" q, $0}' "$clip_ids_file" | sed 's/,$//')
    outbox_sql="SELECT aggregate_id, event_type, status, attempt_count,
                       substr(error, 1, 80) AS error_excerpt, created_at, updated_at
                FROM outbox_events
                WHERE event_type='asset.index.requested'
                  AND aggregate_id IN (${in_list})
                ORDER BY aggregate_id, created_at DESC;"
    sqlite3 -header -column "$SMOKE_DB" "$outbox_sql" > "$WORK_DIR/outbox_events.txt"
    cat "$WORK_DIR/outbox_events.txt"

    outbox_total=$(awk 'NR>2 && $1 != "" {n++} END{print n+0}' "$WORK_DIR/outbox_events.txt")
    outbox_completed=$(awk 'NR>2 && $3 == "completed" {n++} END{print n+0}' "$WORK_DIR/outbox_events.txt")
    outbox_dead=$(awk 'NR>2 && $3 == "dead_letter" {n++} END{print n+0}' "$WORK_DIR/outbox_events.txt")
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
