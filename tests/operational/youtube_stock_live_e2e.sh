#!/usr/bin/env bash
set -Eeuo pipefail

BASE="${BASE:-http://127.0.0.1:8000}"
CAPTIONS_URL="${YOUTUBE_CANARY_CAPTIONS_URL:-}"
NO_CAPTIONS_URL="${YOUTUBE_CANARY_NO_CAPTIONS_URL:-}"
WORK="${YOUTUBE_STOCK_WORKDIR:-/tmp/youtube-stock-canary}"
POLL_SECONDS="${YOUTUBE_STOCK_POLL_SECONDS:-5}"
POLL_MAX="${YOUTUBE_STOCK_POLL_MAX:-120}"
mkdir -p "$WORK"

for tool in curl jq ffprobe stat; do
    command -v "$tool" >/dev/null 2>&1 || { echo "PREREQ FAIL: $tool missing" >&2; exit 2; }
done
[[ -n "$CAPTIONS_URL" && -n "$NO_CAPTIONS_URL" ]] || {
    echo "PREREQ FAIL: set YOUTUBE_CANARY_CAPTIONS_URL and YOUTUBE_CANARY_NO_CAPTIONS_URL" >&2
    exit 2
}

auth=(-H "Authorization: Bearer ${VELOX_ADMIN_TOKEN:?VELOX_ADMIN_TOKEN is required}")
payload=$(jq -n --arg url "$CAPTIONS_URL" '{subject:"youtube-stock-canary",youtube_urls:[$url],query:"important explanation and key moments",clips_per_video:2,clip_duration_ms:7000}')
response="$WORK/submit.json"
http=$(curl -sS -o "$response" -w '%{http_code}' -X POST "$BASE/api/clips/stock" "${auth[@]}" -H 'Content-Type: application/json' --data "$payload" || true)
[[ "$http" == 2* ]] || { echo "FAIL: POST /api/clips/stock HTTP=$http" >&2; sed -n '1,80p' "$response" >&2; exit 1; }
job_id=$(jq -r '.job_id // .id // empty' "$response")
[[ -n "$job_id" ]] || { echo "FAIL: stock submission did not return job_id" >&2; exit 1; }

status="unknown"
job_json='{}'
for _ in $(seq 1 "$POLL_MAX"); do
    sleep "$POLL_SECONDS"
    job_json=$(curl -fsS "${auth[@]}" "$BASE/api/jobs/$job_id/full")
    status=$(jq -r '.status // .job.status // "unknown"' <<<"$job_json")
    echo "job=$job_id status=$status"
    case "$status" in
        SUCCEEDED|completed|COMPLETE) break ;;
        FAILED|failed|CANCELLED|cancelled) echo "$job_json" >&2; exit 1 ;;
    esac
done
case "$status" in SUCCEEDED|completed|COMPLETE) ;; *) echo "FAIL: job timeout" >&2; exit 1 ;; esac

result=$(jq -c '.result.data // .result // .job.result // empty' <<<"$job_json")
jq -e '(.selected_segments|length)==2 and all(.selected_segments[]; .youtube_video_id and .start_ms>=0 and .end_ms>.start_ms and .duration_ms==7000 and .selection_basis=="transcript" and (.visual_verified==false) and (.cache_key|length)>0)' <<<"$result" >/dev/null || {
    echo "FAIL: selection contract missing" >&2
    echo "$result" >&2
    exit 1
}

ids=$(jq -r '.selected_segments[].asset_id // empty' <<<"$result" | sort -u)
[[ -n "$ids" ]] || { echo "FAIL: no asset IDs in stock result" >&2; exit 1; }
for id in $ids; do
    out="$WORK/$id.mp4"
    dl_http=$(curl -sS -o "$out" -w '%{http_code}' -X POST "${auth[@]}" "$BASE/api/media/stock/clips/$id/download" || true)
    [[ "$dl_http" == 200 ]] || { echo "FAIL: download $id HTTP=$dl_http" >&2; exit 1; }
    [[ $(stat -c%s "$out") -gt 100000 ]] || { echo "FAIL: $id is too small" >&2; exit 1; }
    ffprobe_json=$(ffprobe -v error -select_streams v:0 -show_entries stream=codec_name,width,height -show_entries format=duration -of json "$out")
    jq -e '(.streams|length>0) and .streams[0].width>0 and .streams[0].height>0 and (.streams[0].codec_name|length)>0 and (.format.duration|tonumber)>0 and (.format.duration|tonumber)>=6 and (.format.duration|tonumber)<=9' <<<"$ffprobe_json" >/dev/null || {
        echo "FAIL: ffprobe contract failed for $id" >&2
        echo "$ffprobe_json" >&2
        exit 1
    }
done

strict_payload=$(jq -n --arg url "$NO_CAPTIONS_URL" '{subject:"youtube-stock-canary-strict",youtube_urls:[$url],query:"important explanation",clips_per_video:2,clip_duration_ms:7000}')
strict_http=$(curl -sS -o "$WORK/strict.json" -w '%{http_code}' -X POST "$BASE/api/clips/stock" "${auth[@]}" -H 'Content-Type: application/json' --data "$strict_payload" || true)
if [[ "$strict_http" == 2* ]]; then
    strict_job=$(jq -r '.job_id // .id // empty' "$WORK/strict.json")
    [[ -n "$strict_job" ]] || { echo "FAIL: strict request did not return job_id" >&2; exit 1; }
    strict_status="unknown"
    strict_json='{}'
    for _ in $(seq 1 "$POLL_MAX"); do
        sleep "$POLL_SECONDS"
        strict_json=$(curl -fsS "${auth[@]}" "$BASE/api/jobs/$strict_job/full")
        strict_status=$(jq -r '.status // .job.status // "unknown"' <<<"$strict_json")
        case "$strict_status" in
            FAILED|failed|CANCELLED|cancelled) break ;;
            SUCCEEDED|completed|COMPLETE) echo "FAIL: no-caption canary unexpectedly succeeded in strict mode" >&2; exit 1 ;;
        esac
    done
    jq -e '([.. | strings] | map(select(contains("TRANSCRIPT_UNAVAILABLE"))) | length) > 0' <<<"$strict_json" >/dev/null || {
        echo "FAIL: strict no-caption result did not expose TRANSCRIPT_UNAVAILABLE" >&2
        echo "$strict_json" >&2
        exit 1
    }
else
    echo "FAIL: strict no-caption submission HTTP=$strict_http" >&2
    sed -n '1,80p' "$WORK/strict.json" >&2
    exit 1
fi

echo "PASS: metadata/enqueue, transcript, selection, partial clips, persistence, download and strict transcript failure"
