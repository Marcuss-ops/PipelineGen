#!/usr/bin/env bash
# artlist_scale_e2e.sh — quota-expensive Artlist/VidRush scale and dedup battery.
#
# Runs a configurable keyword matrix (default: 20 keywords x 10 clips), verifies:
#   - API/scraper availability during the entire run
#   - bounded clip parallelism and job completion
#   - SQLite publication state, Drive persistence and stable source identity
#   - optional VLM visual-summary generation + Qdrant projection
#   - replay idempotency: same assets keep the same Drive IDs/hashes and create
#     no new successful download-audit rows
#
# This is intentionally NOT part of verify-live: the default run may consume
# up to 200 authorized Artlist downloads and can trigger a full VLM/Qdrant pass.
# Replay starts with a one-clip canary; if dedup is broken the full replay is
# aborted, limiting accidental duplicate quota consumption.

set -Eeuo pipefail

HOST="${VELOX_HOST:-127.0.0.1}"
PORT="${PIPELINE_PORT:-${VELOX_PORT:-8000}}"
BASE_URL="${BASE_URL:-http://${HOST}:${PORT}}"
SCRAPER_URL="${VELOX_ARTLIST_SCRAPER_SERVER_URL:-http://127.0.0.1:9123}"
QDRANT_URL="${QDRANT_URL:-http://127.0.0.1:6333}"
QDRANT_API_KEY="${QDRANT_API_KEY:-${VELOX_QDRANT_API_KEY:-}}"
COLLECTION="${QDRANT_COLLECTION:-media_assets_current}"
DB_PATH="${VELOX_DATA_DIR:-./data}/media/media.db.sqlite"
TOKEN="${VELOX_ADMIN_TOKEN:-}"
ROOT_FOLDER_ID="${ROOT_FOLDER_ID:-${VELOX_DRIVE_ARTLIST_ROOT:-}}"

CLIPS_PER_KEYWORD="${ARTLIST_SCALE_CLIPS_PER_KEYWORD:-10}"
CLIP_CONCURRENCY="${ARTLIST_SCALE_CLIP_CONCURRENCY:-10}"
POLL_INTERVAL="${ARTLIST_SCALE_POLL_INTERVAL:-10}"
POLL_MAX="${ARTLIST_SCALE_POLL_MAX:-360}"
HTTP_TIMEOUT="${ARTLIST_SCALE_HTTP_TIMEOUT:-300}"
HEALTH_INTERVAL="${ARTLIST_SCALE_HEALTH_INTERVAL:-15}"
RUN_REPLAY="${ARTLIST_SCALE_RUN_REPLAY:-1}"
RUN_VLM="${ARTLIST_SCALE_RUN_VLM:-1}"
RUN_QDRANT_REINDEX="${ARTLIST_SCALE_RUN_QDRANT_REINDEX:-1}"
REQUIRE_M3U8_PERSISTENCE="${ARTLIST_SCALE_REQUIRE_M3U8_PERSISTENCE:-1}"
REQUIRE_NO_REDOWNLOAD="${ARTLIST_SCALE_REQUIRE_NO_REDOWNLOAD:-1}"
MIN_ASSETS_PER_MINUTE="${ARTLIST_SCALE_MIN_ASSETS_PER_MINUTE:-0}"
VLM_INTERVAL="${ARTLIST_SCALE_VLM_INTERVAL:-7}"
VLM_TIMEOUT="${ARTLIST_SCALE_VLM_TIMEOUT:-120}"

STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
REPORT_DIR="${ARTLIST_SCALE_REPORT_DIR:-/tmp/artlist_scale_${STAMP}}"
mkdir -p "$REPORT_DIR"/{first,replay,replay_canary,drive,qdrant}
SUMMARY_JSON="$REPORT_DIR/summary.json"
FAILURES_FILE="$REPORT_DIR/failures.txt"
: > "$FAILURES_FILE"
printf '{}\n' > "$REPORT_DIR/vlm_validation.json"
printf '{}\n' > "$REPORT_DIR/qdrant_validation.json"

log() { printf '[%s] %s\n' "$(date '+%H:%M:%S')" "$*"; }
fail() {
    log "FAIL: $*"
    printf '%s\n' "$*" >> "$FAILURES_FILE"
}
require_tool() {
    command -v "$1" >/dev/null 2>&1 || {
        printf 'Required tool missing: %s\n' "$1" >&2
        exit 2
    }
}
is_positive_int() { [[ "$1" =~ ^[1-9][0-9]*$ ]]; }
auth_args=(-H "X-Velox-Admin-Token: ${TOKEN}")

now_ms() {
    python3 - <<'PY'
import time
print(int(time.time() * 1000))
PY
}

for tool in curl jq sqlite3 python3 go; do
    require_tool "$tool"
done
if [[ -z "$TOKEN" ]]; then
    echo "VELOX_ADMIN_TOKEN is required (load it with scripts/with-velox-auth)." >&2
    exit 2
fi
if [[ -z "$ROOT_FOLDER_ID" ]]; then
    echo "VELOX_DRIVE_ARTLIST_ROOT or ROOT_FOLDER_ID is required." >&2
    exit 2
fi
for value_name in CLIPS_PER_KEYWORD CLIP_CONCURRENCY POLL_INTERVAL POLL_MAX HTTP_TIMEOUT HEALTH_INTERVAL; do
    value="${!value_name}"
    if ! is_positive_int "$value"; then
        echo "$value_name must be a positive integer (got '$value')." >&2
        exit 2
    fi
done
if (( CLIP_CONCURRENCY > 10 )); then
    echo "CLIP_CONCURRENCY cannot exceed the API maximum of 10." >&2
    exit 2
fi
if [[ ! -f "$DB_PATH" ]]; then
    echo "SQLite database not found: $DB_PATH" >&2
    exit 2
fi

DEFAULT_KEYWORDS=(
    "business team modern office"
    "city skyline aerial drone"
    "factory automation robots"
    "doctor hospital technology"
    "solar panels renewable energy"
    "boxing training gym"
    "basketball practice court"
    "family cooking kitchen"
    "cybersecurity server room"
    "financial trading screens"
    "construction workers building"
    "electric car charging"
    "scientist laboratory research"
    "airport travelers terminal"
    "farmer tractor field"
    "ocean waves coastline"
    "students classroom learning"
    "warehouse logistics packages"
    "coffee shop barista"
    "mountain hiking adventure"
)

KEYWORDS=()
if [[ -n "${ARTLIST_SCALE_KEYWORDS_FILE:-}" ]]; then
    if [[ ! -f "$ARTLIST_SCALE_KEYWORDS_FILE" ]]; then
        echo "ARTLIST_SCALE_KEYWORDS_FILE not found: $ARTLIST_SCALE_KEYWORDS_FILE" >&2
        exit 2
    fi
    mapfile -t KEYWORDS < <(grep -vE '^[[:space:]]*(#|$)' "$ARTLIST_SCALE_KEYWORDS_FILE" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
elif [[ -n "${ARTLIST_SCALE_KEYWORDS:-}" ]]; then
    IFS=',' read -r -a KEYWORDS <<<"$ARTLIST_SCALE_KEYWORDS"
else
    KEYWORDS=("${DEFAULT_KEYWORDS[@]}")
fi
if (( ${#KEYWORDS[@]} == 0 )); then
    echo "No keywords configured." >&2
    exit 2
fi

MONITOR_PID=""
cleanup() {
    if [[ -n "$MONITOR_PID" ]] && kill -0 "$MONITOR_PID" 2>/dev/null; then
        kill "$MONITOR_PID" 2>/dev/null || true
        wait "$MONITOR_PID" 2>/dev/null || true
    fi
}
trap cleanup EXIT INT TERM

health_monitor() {
    local out="$REPORT_DIR/api_health.tsv"
    printf 'timestamp\tpipeline_ready\tscraper_healthy\tartlist_diagnostics\n' > "$out"
    while true; do
        local ts ready scraper diagnostics
        ts="$(date -u +%FT%TZ)"
        ready=0
        scraper=0
        diagnostics=0
        curl -fsS --max-time 10 "$BASE_URL/ready" | jq -e '.status == "ready"' >/dev/null 2>&1 && ready=1 || true
        curl -fsS --max-time 10 "$SCRAPER_URL/health" | jq -e '.healthy == true or .ok == true' >/dev/null 2>&1 && scraper=1 || true
        curl -fsS --max-time 15 "${auth_args[@]}" "$BASE_URL/api/artlist/diagnostics" | jq -e '.ok == true' >/dev/null 2>&1 && diagnostics=1 || true
        printf '%s\t%s\t%s\t%s\n' "$ts" "$ready" "$scraper" "$diagnostics" >> "$out"
        sleep "$HEALTH_INTERVAL"
    done
}

preflight() {
    log "Preflight: PipelineGen, scraper, Artlist diagnostics, Drive config"
    curl -fsS --max-time 15 "$BASE_URL/health" > "$REPORT_DIR/health.json"
    curl -fsS --max-time 15 "$BASE_URL/ready" | tee "$REPORT_DIR/ready.json" | jq -e '.status == "ready"' >/dev/null
    curl -fsS --max-time 15 "$SCRAPER_URL/health" | tee "$REPORT_DIR/scraper_health.json" | jq -e '.healthy == true or .ok == true' >/dev/null
    curl -fsS --max-time 30 "${auth_args[@]}" "$BASE_URL/api/artlist/job-consumer" > "$REPORT_DIR/job_consumer.json"
    curl -fsS --max-time 30 "${auth_args[@]}" "$BASE_URL/api/artlist/diagnostics" | tee "$REPORT_DIR/artlist_diagnostics.json" | jq -e '.ok == true' >/dev/null
}

warmup() {
    local term="${KEYWORDS[0]}"
    local start end code
    start="$(now_ms)"
    code="$(curl -sS --max-time "$HTTP_TIMEOUT" -o "$REPORT_DIR/warmup.json" -w '%{http_code}' \
        -X POST -H 'Content-Type: application/json' \
        -d "$(jq -nc --arg term "$term" '{term:$term,limit:1}')" \
        "$SCRAPER_URL/search")"
    end="$(now_ms)"
    jq -n --arg term "$term" --argjson http_code "$code" --argjson elapsed_ms "$((end-start))" \
        '{term:$term,http_code:$http_code,elapsed_ms:$elapsed_ms}' > "$REPORT_DIR/warmup_metrics.json"
    if [[ ! "$code" =~ ^2[0-9][0-9]$ ]] || ! jq -e '.ok == true and ((.clips // []) | length) > 0' "$REPORT_DIR/warmup.json" >/dev/null 2>&1; then
        echo "Artlist scraper warmup failed; see $REPORT_DIR/warmup.json" >&2
        exit 2
    fi
    log "Warmup complete in $((end-start)) ms"
}

audit_count() {
    sqlite3 -readonly "$DB_PATH" "SELECT COUNT(*) FROM artlist_download_audit WHERE status='succeeded';" 2>/dev/null || echo 0
}

submit_run() {
    local phase="$1" idx="$2" term="$3" limit="${4:-$CLIPS_PER_KEYWORD}"
    local out="$REPORT_DIR/$phase/submit_$(printf '%02d' "$idx").json"
    local payload code jid
    payload="$(jq -nc \
        --arg term "$term" \
        --arg root "$ROOT_FOLDER_ID" \
        --argjson limit "$limit" \
        --argjson concurrency "$CLIP_CONCURRENCY" \
        '{
            term:$term,
            limit:$limit,
            strategy:"verify",
            dry_run:false,
            clip_duration:7,
            width:1920,
            height:1080,
            fps:30,
            concurrency:$concurrency,
            root_folder_id:$root
        }')"
    code="$(curl -sS --max-time "$HTTP_TIMEOUT" -o "$out" -w '%{http_code}' \
        -X POST "${auth_args[@]}" -H 'Content-Type: application/json' \
        -d "$payload" "$BASE_URL/api/artlist/run")"
    jid="$(jq -r '.run_id // empty' "$out")"
    if [[ ! "$code" =~ ^2[0-9][0-9]$ || -z "$jid" ]]; then
        fail "$phase submit failed for keyword[$idx] '$term' (HTTP $code)"
        return 1
    fi
    printf '%s\t%s\t%s\n' "$idx" "$term" "$jid"
}

poll_one() {
    local phase="$1" idx="$2" term="$3" jid="$4"
    local out="$REPORT_DIR/$phase/job_$(printf '%02d' "$idx").json"
    local status_file="$REPORT_DIR/$phase/status_$(printf '%02d' "$idx").json"
    local tmp="$out.tmp"
    local start end status code attempt
    start="$(now_ms)"
    status="UNKNOWN"
    for ((attempt=1; attempt<=POLL_MAX; attempt++)); do
        code="$(curl -sS --max-time "$HTTP_TIMEOUT" -o "$tmp" -w '%{http_code}' \
            "${auth_args[@]}" "$BASE_URL/api/jobs/$jid/full" || true)"
        if [[ "$code" =~ ^2[0-9][0-9]$ ]]; then
            mv "$tmp" "$out"
            status="$(jq -r '.status // "UNKNOWN"' "$out")"
            case "$status" in
                SUCCEEDED|completed|FAILED|failed|CANCELLED|cancelled)
                    break
                    ;;
            esac
        fi
        sleep "$POLL_INTERVAL"
    done
    end="$(now_ms)"
    if [[ ! -f "$out" ]]; then
        printf '{"status":"UNREACHABLE"}\n' > "$out"
    fi
    if (( attempt > POLL_MAX )); then
        status="TIMEOUT"
    fi
    jq -n \
        --arg phase "$phase" --arg term "$term" --arg run_id "$jid" --arg status "$status" \
        --argjson keyword_index "$idx" --argjson elapsed_ms "$((end-start))" \
        '{phase:$phase,keyword_index:$keyword_index,term:$term,run_id:$run_id,status:$status,elapsed_ms:$elapsed_ms}' \
        > "$status_file"
}

run_phase() {
    local phase="$1"
    local runs="$REPORT_DIR/$phase/runs.tsv"
    : > "$runs"
    log "$phase: submitting ${#KEYWORDS[@]} jobs (${CLIPS_PER_KEYWORD} clips each, clip concurrency=${CLIP_CONCURRENCY})"
    local idx=0 term line
    for term in "${KEYWORDS[@]}"; do
        idx=$((idx+1))
        if line="$(submit_run "$phase" "$idx" "$term")"; then
            printf '%s\n' "$line" >> "$runs"
        fi
    done

    log "$phase: polling all jobs concurrently"
    if [[ ! -s "$runs" ]]; then
        printf '[]\n' > "$REPORT_DIR/$phase/statuses.json"
        fail "$phase has no submitted jobs to poll"
        return
    fi
    local pids=() pid
    while IFS=$'\t' read -r idx term jid; do
        poll_one "$phase" "$idx" "$term" "$jid" &
        pids+=("$!")
    done < "$runs"
    for pid in "${pids[@]}"; do
        wait "$pid" || fail "$phase poll worker pid=$pid failed"
    done

    jq -s '.' "$REPORT_DIR/$phase"/status_*.json > "$REPORT_DIR/$phase/statuses.json"
    local succeeded submitted
    submitted="$(wc -l < "$runs" | tr -d ' ')"
    succeeded="$(jq '[.[] | select(.status == "SUCCEEDED" or .status == "completed")] | length' "$REPORT_DIR/$phase/statuses.json")"
    if (( submitted != ${#KEYWORDS[@]} )); then
        fail "$phase submitted $submitted/${#KEYWORDS[@]} jobs"
    fi
    if (( succeeded != submitted )); then
        fail "$phase completed $succeeded/$submitted jobs successfully"
    fi
}

extract_items() {
    local phase="$1"
    local items="$REPORT_DIR/$phase/items.tsv"
    printf 'term\trun_id\tclip_id\tstatus\tdrive_file_id\tdrive_link\tdownload_link\tlocal_path\tfile_hash\n' > "$items"
    local idx term jid job count
    while IFS=$'\t' read -r idx term jid; do
        job="$REPORT_DIR/$phase/job_$(printf '%02d' "$idx").json"
        count="$(jq '(.result.items // []) | length' "$job")"
        if (( count != CLIPS_PER_KEYWORD )); then
            fail "$phase keyword '$term' returned $count/$CLIPS_PER_KEYWORD items"
        fi
        jq -r --arg term "$term" --arg run_id "$jid" '
            (.result.items // [])[] |
            [
                $term,
                $run_id,
                (.clip_id // ""),
                (.status // ""),
                (.drive_file_id // ""),
                (.drive_link // ""),
                (.download_link // ""),
                (.local_path // ""),
                (.file_hash // "")
            ] | @tsv
        ' "$job" >> "$items"
    done < "$REPORT_DIR/$phase/runs.tsv"
}

build_target_report() {
    local items="$REPORT_DIR/first/items.tsv"
    awk -F'\t' 'NR > 1 && $3 != "" {print $3}' "$items" | sort -u > "$REPORT_DIR/target_ids.txt"
    python3 - "$DB_PATH" "$REPORT_DIR/target_ids.txt" "$REPORT_DIR/db_assets.json" <<'PY'
import json, sqlite3, sys
db_path, ids_path, out_path = sys.argv[1:]
ids = [line.strip() for line in open(ids_path, encoding="utf-8") if line.strip()]
conn = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
conn.row_factory = sqlite3.Row
rows = []
if ids:
    marks = ",".join("?" for _ in ids)
    sql = f"""
      SELECT id, source, media_type, lifecycle_state, index_state,
             COALESCE(drive_file_id,'') AS drive_file_id,
             COALESCE(drive_link,'') AS drive_link,
             COALESCE(download_link,'') AS download_link,
             COALESCE(local_path,'') AS local_path,
             COALESCE(file_hash,'') AS file_hash,
             COALESCE(source_version,'') AS source_version,
             COALESCE(source_url,'') AS source_url,
             COALESCE(metadata_json,'{{}}') AS metadata_json
      FROM media_assets WHERE id IN ({marks})
    """
    rows = [dict(r) for r in conn.execute(sql, ids)]
invalid = []
m3u8_ids = []
for row in rows:
    required = (
        row["source"] == "artlist",
        row["media_type"] == "video",
        row["lifecycle_state"] == "PUBLISHED",
        row["index_state"] == "INDEXED",
        bool(row["drive_file_id"]),
        bool(row["drive_link"]),
        bool(row["file_hash"]),
        bool(row["source_version"]),
        bool(row["source_url"] or row["download_link"]),
    )
    if not all(required):
        invalid.append(row["id"])
    haystack = " ".join([
        row.get("source_url", ""),
        row.get("download_link", ""),
        row.get("metadata_json", ""),
    ]).lower()
    if ".m3u8" in haystack:
        m3u8_ids.append(row["id"])
report = {
    "requested_ids": len(ids),
    "found_rows": len(rows),
    "invalid_rows": invalid,
    "m3u8_persisted_ids": m3u8_ids,
    "m3u8_persisted_count": len(m3u8_ids),
    "assets": rows,
}
json.dump(report, open(out_path, "w", encoding="utf-8"), indent=2)
PY

    local requested found invalid m3u8
    requested="$(jq '.requested_ids' "$REPORT_DIR/db_assets.json")"
    found="$(jq '.found_rows' "$REPORT_DIR/db_assets.json")"
    invalid="$(jq '.invalid_rows | length' "$REPORT_DIR/db_assets.json")"
    m3u8="$(jq '.m3u8_persisted_count' "$REPORT_DIR/db_assets.json")"
    if (( found != requested )); then
        fail "SQLite contains $found/$requested target assets"
    fi
    if (( invalid > 0 )); then
        fail "$invalid target assets failed publication/index/Drive/hash checks"
    fi
    if (( REQUIRE_M3U8_PERSISTENCE == 1 && m3u8 == 0 )); then
        fail "no target asset persists an m3u8/stream URL in source_url, download_link or metadata_json"
    fi
}

verify_drive_batch() {
    jq -r '.assets[].drive_file_id | select(length > 0)' "$REPORT_DIR/db_assets.json" | sort -u > "$REPORT_DIR/drive_ids.txt"
    split -l 50 -d -a 3 "$REPORT_DIR/drive_ids.txt" "$REPORT_DIR/drive/chunk_"
    local chunk payload out code expected resolved
    for chunk in "$REPORT_DIR"/drive/chunk_*; do
        [[ -f "$chunk" ]] || continue
        payload="$(jq -Rn '[inputs | select(length > 0)] | {ids:.}' < "$chunk")"
        out="$chunk.json"
        code="$(curl -sS --max-time "$HTTP_TIMEOUT" -o "$out" -w '%{http_code}' \
            -X POST "${auth_args[@]}" -H 'Content-Type: application/json' \
            -d "$payload" "$BASE_URL/api/drive/resolve-by-id")"
        expected="$(wc -l < "$chunk" | tr -d ' ')"
        resolved="$(jq '.resolved_count // 0' "$out")"
        if [[ ! "$code" =~ ^2[0-9][0-9]$ ]] || (( resolved != expected )); then
            fail "Drive batch $(basename "$chunk") resolved $resolved/$expected (HTTP $code)"
        fi
        if ! jq -e 'all((.resolved // [])[]; (.trashed == false) and ((.size // 0) > 0))' "$out" >/dev/null 2>&1; then
            fail "Drive batch $(basename "$chunk") contains missing, trashed or empty files"
        fi
    done
}

run_admin() {
    if [[ -n "${ARTLIST_SCALE_ADMIN_BIN:-}" ]]; then
        "$ARTLIST_SCALE_ADMIN_BIN" "$@"
    else
        go run ./cmd/admin "$@"
    fi
}

run_vlm_and_qdrant() {
    if (( RUN_VLM != 1 )); then
        log "VLM pass skipped by ARTLIST_SCALE_RUN_VLM=0"
        return
    fi
    log "VLM: generating canonical visual summaries for Artlist assets"
    run_admin reindex-visual-summary --apply --source=artlist --interval="$VLM_INTERVAL" \
        --vlm-timeout="$VLM_TIMEOUT" --json > "$REPORT_DIR/vlm_reindex.json"

    python3 - "$DB_PATH" "$REPORT_DIR/target_ids.txt" "$REPORT_DIR/vlm_validation.json" <<'PY'
import json, sqlite3, sys
db_path, ids_path, out_path = sys.argv[1:]
ids = [line.strip() for line in open(ids_path, encoding="utf-8") if line.strip()]
conn = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
conn.row_factory = sqlite3.Row
rows = []
if ids:
    marks = ",".join("?" for _ in ids)
    rows = [dict(r) for r in conn.execute(
        f"""SELECT asset_id, visual_summary_text, visible_actions_json,
                   visible_entities_json, frame_count, interval_seconds,
                   preprocessing_version, model_name, model_version,
                   source_hash, sampled_at
            FROM asset_visual_summaries WHERE asset_id IN ({marks})""", ids)]
valid = [
    r for r in rows
    if r["frame_count"] > 0 and r["interval_seconds"] > 0
    and r["preprocessing_version"] and r["model_name"]
    and r["model_version"] and r["source_hash"] and r["sampled_at"]
]
json.dump({
    "requested_ids": len(ids),
    "rows": len(rows),
    "valid_rows": len(valid),
    "invalid_ids": sorted(set(ids) - {r["asset_id"] for r in valid}),
}, open(out_path, "w", encoding="utf-8"), indent=2)
PY
    local requested valid
    requested="$(jq '.requested_ids' "$REPORT_DIR/vlm_validation.json")"
    valid="$(jq '.valid_rows' "$REPORT_DIR/vlm_validation.json")"
    if (( valid != requested )); then
        fail "VLM produced valid visual summaries for $valid/$requested target assets"
    fi

    if (( RUN_QDRANT_REINDEX == 1 )); then
        log "Qdrant: blue-green reindex after VLM pass"
        run_admin reindex-qdrant --apply --json > "$REPORT_DIR/qdrant_reindex.json"
    fi

    log "Qdrant: validating target payloads in alias $COLLECTION"
    split -l 50 -d -a 3 "$REPORT_DIR/target_ids.txt" "$REPORT_DIR/qdrant/ids_"
    : > "$REPORT_DIR/qdrant/payloads.ndjson"
    local chunk payload out headers=() code
    [[ -n "$QDRANT_API_KEY" ]] && headers=(-H "api-key: $QDRANT_API_KEY")
    for chunk in "$REPORT_DIR"/qdrant/ids_*; do
        [[ -f "$chunk" ]] || continue
        payload="$(jq -Rn '[inputs | select(length > 0)] as $ids | {
            filter:{must:[{key:"asset_id",match:{any:$ids}}]},
            limit:100,
            with_payload:true,
            with_vector:false
        }' < "$chunk")"
        out="$chunk.json"
        code="$(curl -sS --max-time "$HTTP_TIMEOUT" -o "$out" -w '%{http_code}' \
            -X POST "${headers[@]}" -H 'Content-Type: application/json' \
            -d "$payload" "$QDRANT_URL/collections/$COLLECTION/points/scroll")"
        if [[ ! "$code" =~ ^2[0-9][0-9]$ ]]; then
            fail "Qdrant scroll failed for $(basename "$chunk") (HTTP $code)"
            continue
        fi
        jq -c '.result.points[]?.payload' "$out" >> "$REPORT_DIR/qdrant/payloads.ndjson"
    done

    python3 - "$REPORT_DIR/target_ids.txt" "$REPORT_DIR/qdrant/payloads.ndjson" "$REPORT_DIR/qdrant_validation.json" <<'PY'
import json, sys
ids_path, payload_path, out_path = sys.argv[1:]
wanted = {line.strip() for line in open(ids_path, encoding="utf-8") if line.strip()}
payloads = []
for line in open(payload_path, encoding="utf-8"):
    line = line.strip()
    if line:
        payloads.append(json.loads(line))
valid = {}
for p in payloads:
    asset_id = p.get("asset_id", "")
    if asset_id in wanted:
        has_visual_version = bool(
            p.get("visual_preprocessing_version")
            and p.get("visual_model_name")
            and p.get("visual_model_version")
        )
        if p.get("source") == "artlist" and p.get("lifecycle_state") == "PUBLISHED" and has_visual_version:
            valid[asset_id] = True
missing = sorted(wanted - set(valid))
json.dump({
    "requested_ids": len(wanted),
    "valid_payloads": len(valid),
    "missing_or_invalid_ids": missing,
}, open(out_path, "w", encoding="utf-8"), indent=2)
PY
    requested="$(jq '.requested_ids' "$REPORT_DIR/qdrant_validation.json")"
    valid="$(jq '.valid_payloads' "$REPORT_DIR/qdrant_validation.json")"
    if (( valid != requested )); then
        fail "Qdrant contains valid VLM payloads for $valid/$requested target assets"
    fi
}

snapshot_identity() {
    local out="$1"
    jq '[.assets[] | {id,drive_file_id,drive_link,file_hash,source_url,download_link}] | sort_by(.id)' \
        "$REPORT_DIR/db_assets.json" > "$out"
}

run_replay_canary() {
    local term="${KEYWORDS[0]}"
    local line idx jid before after delta
    before="$(audit_count)"
    if ! line="$(submit_run replay_canary 1 "$term" 1)"; then
        fail "replay canary submission failed"
        return 1
    fi
    printf '%s\n' "$line" > "$REPORT_DIR/replay_canary/runs.tsv"
    IFS=$'\t' read -r idx _ jid <<<"$line"
    poll_one replay_canary "$idx" "$term" "$jid"
    after="$(audit_count)"
    delta=$((after-before))
    jq -n --arg term "$term" --arg run_id "$jid" --argjson audit_delta "$delta" \
        '{term:$term,run_id:$run_id,successful_download_audit_delta:$audit_delta}' \
        > "$REPORT_DIR/replay_canary/result.json"
    if (( delta != 0 )); then
        fail "replay canary created $delta successful download-audit rows; full replay aborted to protect quota"
        return 1
    fi
    return 0
}

validate_replay() {
    local audit_before="$1" audit_after="$2"
    extract_items replay

    python3 - "$DB_PATH" "$REPORT_DIR/target_ids.txt" "$REPORT_DIR/identity_before.json" "$REPORT_DIR/identity_after.json" <<'PY'
import json, sqlite3, sys
db_path, ids_path, before_path, out_path = sys.argv[1:]
ids = [line.strip() for line in open(ids_path, encoding="utf-8") if line.strip()]
before = {r["id"]: r for r in json.load(open(before_path, encoding="utf-8"))}
conn = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
conn.row_factory = sqlite3.Row
rows = []
if ids:
    marks = ",".join("?" for _ in ids)
    rows = [dict(r) for r in conn.execute(
        f"""SELECT id, COALESCE(drive_file_id,'') drive_file_id,
                   COALESCE(drive_link,'') drive_link,
                   COALESCE(file_hash,'') file_hash,
                   COALESCE(source_url,'') source_url,
                   COALESCE(download_link,'') download_link
            FROM media_assets WHERE id IN ({marks})""", ids)]
changed = []
for row in rows:
    old = before.get(row["id"])
    if old is None or any(row.get(k, "") != old.get(k, "") for k in ("drive_file_id","drive_link","file_hash")):
        changed.append(row["id"])
json.dump({"assets": rows, "changed_identity_ids": changed}, open(out_path, "w", encoding="utf-8"), indent=2)
PY

    local changed delta
    changed="$(jq '.changed_identity_ids | length' "$REPORT_DIR/identity_after.json")"
    delta=$((audit_after-audit_before))
    if (( changed > 0 )); then
        fail "replay changed Drive/hash identity for $changed assets"
    fi
    if (( REQUIRE_NO_REDOWNLOAD == 1 && delta != 0 )); then
        fail "replay created $delta new successful download-audit rows; expected zero"
    fi
}

write_summary() {
    local start_ms="$1" end_ms="$2" audit_first_before="$3" audit_first_after="$4" audit_replay_before="$5" audit_replay_after="$6"
    local item_total unique_count failure_count elapsed_ms assets_per_minute health_failures
    item_total="$(tail -n +2 "$REPORT_DIR/first/items.tsv" 2>/dev/null | wc -l | tr -d ' ')"
    unique_count="$(wc -l < "$REPORT_DIR/target_ids.txt" 2>/dev/null | tr -d ' ')"
    failure_count="$(wc -l < "$FAILURES_FILE" | tr -d ' ')"
    elapsed_ms=$((end_ms-start_ms))
    assets_per_minute="$(python3 - "$item_total" "$elapsed_ms" <<'PY'
import sys
items, elapsed_ms = map(float, sys.argv[1:])
print(round(items / (elapsed_ms / 60000.0), 3) if elapsed_ms > 0 else 0)
PY
)"
    health_failures="$(awk -F'\t' 'NR>1 && ($2 != 1 || $3 != 1 || $4 != 1) {n++} END {print n+0}' "$REPORT_DIR/api_health.tsv")"
    if (( health_failures > 0 )); then
        fail "API health monitor observed $health_failures unhealthy samples"
        failure_count="$(wc -l < "$FAILURES_FILE" | tr -d ' ')"
    fi
    if python3 - "$assets_per_minute" "$MIN_ASSETS_PER_MINUTE" <<'PY'
import sys
raise SystemExit(0 if float(sys.argv[1]) >= float(sys.argv[2]) else 1)
PY
    then
        :
    else
        fail "throughput $assets_per_minute assets/min is below minimum $MIN_ASSETS_PER_MINUTE"
        failure_count="$(wc -l < "$FAILURES_FILE" | tr -d ' ')"
    fi

    jq -n \
        --arg report_dir "$REPORT_DIR" \
        --argjson keywords "${#KEYWORDS[@]}" \
        --argjson clips_per_keyword "$CLIPS_PER_KEYWORD" \
        --argjson requested_items "$(( ${#KEYWORDS[@]} * CLIPS_PER_KEYWORD ))" \
        --argjson returned_items "$item_total" \
        --argjson unique_assets "$unique_count" \
        --argjson clip_concurrency "$CLIP_CONCURRENCY" \
        --argjson elapsed_ms "$elapsed_ms" \
        --argjson assets_per_minute "$assets_per_minute" \
        --argjson api_health_failures "$health_failures" \
        --argjson first_download_audit_delta "$((audit_first_after-audit_first_before))" \
        --argjson replay_download_audit_delta "$((audit_replay_after-audit_replay_before))" \
        --argjson failure_count "$failure_count" \
        --slurpfile first_statuses "$REPORT_DIR/first/statuses.json" \
        --slurpfile db_assets "$REPORT_DIR/db_assets.json" \
        --slurpfile vlm "$REPORT_DIR/vlm_validation.json" \
        --slurpfile qdrant "$REPORT_DIR/qdrant_validation.json" \
        '{
            ok:($failure_count == 0),
            report_dir:$report_dir,
            matrix:{keywords:$keywords,clips_per_keyword:$clips_per_keyword,requested_items:$requested_items},
            results:{returned_items:$returned_items,unique_assets:$unique_assets},
            performance:{clip_concurrency:$clip_concurrency,elapsed_ms:$elapsed_ms,assets_per_minute:$assets_per_minute},
            availability:{unhealthy_samples:$api_health_failures},
            dedup:{first_download_audit_delta:$first_download_audit_delta,replay_download_audit_delta:$replay_download_audit_delta},
            jobs:($first_statuses[0] // []),
            sqlite:($db_assets[0] // {}),
            vlm:($vlm[0] // {}),
            qdrant:($qdrant[0] // {}),
            failure_count:$failure_count
        }' > "$SUMMARY_JSON"
}

main() {
    preflight
    warmup
    health_monitor &
    MONITOR_PID="$!"

    local start_ms end_ms audit_first_before audit_first_after audit_replay_before audit_replay_after
    start_ms="$(now_ms)"
    audit_first_before="$(audit_count)"

    run_phase first
    extract_items first
    build_target_report
    verify_drive_batch
    audit_first_after="$(audit_count)"
    snapshot_identity "$REPORT_DIR/identity_before.json"
    run_vlm_and_qdrant

    audit_replay_before="$audit_first_after"
    audit_replay_after="$audit_replay_before"
    if (( RUN_REPLAY == 1 )); then
        if run_replay_canary; then
            run_phase replay
            audit_replay_after="$(audit_count)"
            validate_replay "$audit_replay_before" "$audit_replay_after"
        else
            audit_replay_after="$(audit_count)"
        fi
    fi

    end_ms="$(now_ms)"
    cleanup
    MONITOR_PID=""
    write_summary "$start_ms" "$end_ms" "$audit_first_before" "$audit_first_after" "$audit_replay_before" "$audit_replay_after"

    log "Report: $SUMMARY_JSON"
    jq . "$SUMMARY_JSON"
    if [[ -s "$FAILURES_FILE" ]]; then
        log "FAILURES:"
        sed 's/^/  - /' "$FAILURES_FILE"
        exit 1
    fi
    log "PASS: Artlist scale, Drive, VLM/Qdrant and replay dedup checks succeeded"
}

main "$@"
