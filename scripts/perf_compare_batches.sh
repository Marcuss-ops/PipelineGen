#!/usr/bin/env bash
# scripts/perf_compare_batches.sh — run two 5-job batches (baseline + new) and
# compare their aggregate performance reports.
#
# Dispatches its own script.generate jobs (one per item) with
# audio.timing.mode=best_effort so the live Edge TTS backend (which currently
# emits no word boundaries) cannot fail the whole job on required timing; the
# full chain (Gemma → entities → Edge TTS → Rust final_audio → Drive → Google
# Doc) still runs. Only SUCCEEDED jobs enter the report.
#
# Environment (optional, sourced from .env when present):
#   VELOX_ADMIN_TOKEN, VELOX_DRIVE_SCRIPTS_GENERATE, VELOX_PORT
#   CERT_LANGUAGE          target language (default en)
#   PERF_COMPARE_DIR       working dir (default /tmp/perf-compare)
#   ADMIN_BIN              prebuilt admin binary (default /tmp/pipelinegen-admin)

set -euo pipefail
cd "$(dirname "$0")/.."

set -a
# shellcheck disable=SC1091
source .env 2>/dev/null || true
set +a

# Prefer the LIVE server token over a possibly-stale .env value.
LIVE_PID=$(pgrep -f '/bin/pipelinegen' | head -1 || true)
if [[ -n "${LIVE_PID:-}" && -r "/proc/${LIVE_PID}/environ" ]]; then
    LIVE_TOKEN=$(tr '\0' '\n' < "/proc/${LIVE_PID}/environ" 2>/dev/null | grep -E '^VELOX_ADMIN_TOKEN=' | head -1 | cut -d= -f2- || true)
    [[ -n "${LIVE_TOKEN:-}" ]] && export VELOX_ADMIN_TOKEN="$LIVE_TOKEN"
fi

CERT_LANGUAGE="${CERT_LANGUAGE:-en}"
CERT_DOCS_FOLDER_ID="${CERT_DOCS_FOLDER_ID:-${VELOX_DRIVE_SCRIPTS_GENERATE:-}}"
export CERT_LANGUAGE CERT_DOCS_FOLDER_ID

if [[ -z "$CERT_DOCS_FOLDER_ID" ]]; then
    echo "setup error: CERT_DOCS_FOLDER_ID (or VELOX_DRIVE_SCRIPTS_GENERATE) is required" >&2
    exit 2
fi

# common.sh enforces a per-run wall clock; a 10-job sequential run far
# exceeds the 180s default, so raise both ceilings before sourcing.
export SMOKE_TIMEOUT_SECONDS="${SMOKE_TIMEOUT_SECONDS:-3600}"
export SMOKE_POLL_TIMEOUT_SECONDS="${SMOKE_POLL_TIMEOUT_SECONDS:-900}"

# shellcheck source=tests/operational/lib/common.sh
source "$(pwd)/tests/operational/lib/common.sh"
smoke_require jq curl

OUT="${PERF_COMPARE_DIR:-/tmp/perf-compare}"
ADMIN_BIN="${ADMIN_BIN:-/tmp/pipelinegen-admin}"
mkdir -p "$OUT"

STYLE="Write a short English narration. Use only the supplied source text. Preserve the named person, place and main concept. Do not invent additional people, places, organizations, quotes or events."
TONE="clear, factual and conversational"

# item_id|person|place|concept  (same controlled items as the certification).
ITEMS=(
    "perf-01|Jackie Chan|Hong Kong|martial arts"
    "perf-02|Tom Holland|London|acting"
    "perf-03|Adam Sandler|New York|comedy"
    "perf-04|Serena Williams|Miami|tennis"
    "perf-05|Gordon Ramsay|London|cooking"
    "perf-06|Keanu Reeves|Toronto|filmmaking"
    "perf-07|Lewis Hamilton|Monaco|Formula One"
    "perf-08|Adele|London|music"
    "perf-09|Emma Watson|Paris|education"
    "perf-10|Dwayne Johnson|Miami|wrestling"
)

build_payload() {
    local id="$1" person="$2" place="$3" concept="$4"
    local title="${person} and ${concept}"
    local text="${person} is the central person in this short narration. The setting discussed is ${place}. The main concept is ${concept}. Explain these three supplied elements in a concise and natural way without introducing additional named people, places, organizations, dates or events."
    jq -nc --arg id "$id" --arg title "$title" --arg topic "$title" \
        --arg text "$text" --arg lang "$CERT_LANGUAGE" \
        --arg tone "$TONE" --arg style "$STYLE" \
        --arg folder "$CERT_DOCS_FOLDER_ID" --arg project "perf-compare" '{
        version: 2,
        preset: "custom",
        force_refresh: true,
        items: [{
            id: $id,
            title: $title,
            project: $project,
            language: $lang,
            tone: $tone,
            style: $style,
            source: { type: "text", topic: $topic, source_text: $text },
            script_params: { target_words: 100, min_words: 60, segment_words: 60, use_memory: false },
            output: {
                save_to_db: true,
                extract_entities: true,
                generate_metadata: false,
                generate_scene_images: false,
                generate_timeline: true,
                voiceover_enabled: true
            },
            audio: {
                mode: "COMBINED_TIMELINE",
                timing: { mode: "best_effort", boundary: "word", formats: ["json", "srt", "vtt"] }
            },
            docs: { enabled: true, languages: [$lang], folder_id: $folder }
        }]
    }'
}

run_item() {
    local idx="$1"
    IFS='|' read -r id person place concept <<< "${ITEMS[$idx]}"
    local payload job_id
    payload=$(build_payload "$id" "$person" "$place" "$concept")

    export SMOKE_IDEMPOTENCY_KEY="perf-${id}-$(date +%s)"
    smoke_curl POST "/api/script/generate" --data "$payload" >/dev/null
    unset SMOKE_IDEMPOTENCY_KEY
    if [[ "$SMOKE_LAST_HTTP" != "202" && "$SMOKE_LAST_HTTP" != "200" ]]; then
        echo "FAIL dispatch $id: HTTP $SMOKE_LAST_HTTP"
        return 1
    fi
    job_id=$(jq -r '.job_id // .id // empty' "$SMOKE_LAST_BODY")
    if [[ -z "$job_id" || "$job_id" == "null" ]]; then
        echo "FAIL dispatch $id: no job_id"
        return 1
    fi
    echo "dispatch OK $id job_id=$job_id"

    if ! smoke_poll_terminal "$job_id"; then
        echo "FAIL poll $id ($job_id): timeout"
        return 1
    fi
    local status="$SMOKE_LAST_STATUS"
    echo "  $id -> $status"
    if [[ "$status" == "SUCCEEDED" || "$status" == "completed" ]]; then
        echo "$job_id" >> "$OUT/$idx.ids"
    fi
    return 0
}

# ── dispatch all 10 sequentially, one at a time ─────────────────────────
# Resumable: items already recorded in <idx>.ids are skipped, so a killed
# driver can be relaunched without re-dispatching completed items.
for i in $(seq 0 9); do
    if [[ -s "$OUT/$i.ids" ]]; then
        echo "skip $i (already recorded: $(tr '\n' ',' < "$OUT/$i.ids"))"
        continue
    fi
    run_item "$i" || true
done

# baseline = items 0..4, new = items 5..9
# (|| true: brace expansion over missing .ids would trip set -euo pipefail)
BASE_IDS=$(cat "$OUT"/{0,1,2,3,4}.ids 2>/dev/null | paste -sd, - || true)
NEW_IDS=$(cat "$OUT"/{5,6,7,8,9}.ids 2>/dev/null | paste -sd, - || true)

if [[ -z "$BASE_IDS" || -z "$NEW_IDS" ]]; then
    echo "error: not enough SUCCEEDED jobs to compare (baseline='$BASE_IDS' new='$NEW_IDS')" >&2
    exit 1
fi

echo
echo "===== BASELINE ====="
"$ADMIN_BIN" performance-report --job-ids "$BASE_IDS" --format text

echo
echo "===== NEW ====="
"$ADMIN_BIN" performance-report --job-ids "$NEW_IDS" --format text

echo
echo "baseline ids: $BASE_IDS"
echo "new ids:      $NEW_IDS"
