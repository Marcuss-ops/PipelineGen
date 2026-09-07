# ── Build work list ────────────────────────────────────────────────────────
# Each entry: "mode:topic_or_id"  mode = "generate" | "render"
WORK_LIST=()

for cid in "${CLIP_IDS[@]}"; do
    WORK_LIST+=("render:${cid}")
done

if [[ -n "$TOPIC" ]]; then
    for ((i=1; i<=CLIPS; i++)); do
        WORK_LIST+=("generate:${TOPIC} #$i")
    done
fi

echo "[bench] Work items: ${#WORK_LIST[@]}"
echo ""

# ── Stage 1: Submit all jobs ──────────────────────────────────────────────
echo "[bench] ═══ STAGE 1: SUBMIT ═══"

# Local clocks are used only for operational polling, never for benchmark metrics.

# Per-job identity/status only. Timing is never measured locally: all report
# metrics come from the fetched job RunReport/result SSOT.
declare -a J_JOB_IDS=()
declare -a J_LABELS=()
declare -a J_MODES=()
declare -a J_STATUS=()

# When the benchmark is explicitly bounded to one slot, submit the next job
# only after the previous one reaches a terminal state.  The server's worker
# pool is shared with other queues, so a report-only label cannot guarantee
# isolation; serial submission makes the advertised 1-slot baseline real.
wait_for_benchmark_job() {
    local index="$1" jid="$2" polled=0 status
    while true; do
        status=$(api GET "/api/jobs/$jid" 2>/dev/null || echo '{}')
        status=$(echo "$status" | json_field "status" "unknown" | tr '[:upper:]' '[:lower:]')
        case "$status" in
            completed|succeeded|failed|cancelled)
                J_STATUS[$index]="$status"
                echo "[bench] Job $jid → $status"
                return 0
                ;;
        esac
        if (( polled >= POLL_MAX )); then
            J_STATUS[$index]="timeout"
            echo "[bench] ⚠️ Job $jid timed out after ${POLL_MAX}s" >&2
            return 1
        fi
        sleep "$POLL_INTERVAL"
        polled=$((polled + POLL_INTERVAL))
    done
}

# Sliding-window controller. This releases one window slot as soon as any job
# terminates, so the submitter can replace
# it immediately without an artificial barrier at every group boundary.
declare -a WINDOW_INDEXES=()
declare -a WINDOW_JOB_IDS=()
wait_for_any_benchmark_window() {
    local polled=0 status i j idx
    while true; do
        for ((i=0; i<${#WINDOW_JOB_IDS[@]}; i++)); do
            j="${WINDOW_JOB_IDS[$i]}"
            status=$(api GET "/api/jobs/$j" 2>/dev/null || echo '{}')
            status=$(echo "$status" | json_field "status" "unknown" | tr '[:upper:]' '[:lower:]')
            case "$status" in
                completed|succeeded|failed|cancelled)
                    idx="${WINDOW_INDEXES[$i]}"
                    J_STATUS[$idx]="$status"
                    echo "[bench] Window slot released: $j → $status"
                    WINDOW_INDEXES=("${WINDOW_INDEXES[@]:0:i}" "${WINDOW_INDEXES[@]:i+1}")
                    WINDOW_JOB_IDS=("${WINDOW_JOB_IDS[@]:0:i}" "${WINDOW_JOB_IDS[@]:i+1}")
                    return 0
                    ;;
            esac
        done
        if (( polled >= POLL_MAX )); then
            for ((i=0; i<${#WINDOW_INDEXES[@]}; i++)); do
                idx="${WINDOW_INDEXES[$i]}"
                [[ "${J_STATUS[$idx]:-}" == "submitted" ]] && J_STATUS[$idx]="timeout"
            done
            echo "[bench] ⚠️ sliding window timed out after ${POLL_MAX}s" >&2
            return 1
        fi
        sleep "$POLL_INTERVAL"
        polled=$((polled + POLL_INTERVAL))
    done
}

SUCCESS_COUNT=0

for entry in "${WORK_LIST[@]}"; do
    MODE="${entry%%:*}"
    PAYLOAD_ARG="${entry#*:}"
    SUBMIT_T0=""

    if [[ "$MODE" == "generate" ]]; then
        # ── Generate: POST /api/script/generate ──────────────────────────
        if [[ "$VOICEOVER" == "1" ]]; then
            [[ -n "$DOCS_FOLDER_ID" ]] || { echo "[bench] ERROR: --docs-folder or BENCH_DOCS_FOLDER_ID is required with --voiceover" >&2; exit 2; }
            PAYLOAD=$(python3 - "$PAYLOAD_ARG" "$DOCS_FOLDER_ID" <<'PY'
import json, sys
from time import time_ns

topic, folder = sys.argv[1:]
print(json.dumps({
  "version": 2,
  "preset": "custom",
  "force_refresh": True,
  "items": [{
    "id": f"bench-matt-damon-{time_ns()}",
    "title": f"Benchmark: {topic}",
    "project": "matt-damon-5-clips-tts-benchmark",
    "language": "en",
    "source": {"type": "text", "topic": topic},
    "script_params": {"target_words": 180, "segment_words": 36},
    "output": {"generate_timeline": True, "voiceover_enabled": True,
               "render": {"enabled": True,
                          "watermark": {"enabled": True, "text": "MATT DAMON", "position": "top_right", "opacity": 1},
                          "subtitles": {"enabled": True, "mode": "burn"}}},
    "audio": {"mode": "COMBINED_TIMELINE", "timing": {"mode": "required", "boundary": "word", "formats": ["json", "srt", "vtt"]}},
    "docs": {"enabled": True, "languages": ["en"], "folder_id": folder}
  }]
}))
PY
)
        else
            PAYLOAD=$(python3 - "$PAYLOAD_ARG" <<'PY'
import json, sys
print(json.dumps({"version": 2, "preset": "custom", "items": [{
  "id": "bench-" + sys.argv[1], "title": "Benchmark: " + sys.argv[1], "language": "en",
  "source": {"type": "text", "topic": sys.argv[1]}, "script_params": {"target_words": 500},
  "output": {"generate_scene_images": False}
}]}))
PY
)
        fi
        RESP=$(api POST "/api/script/generate" -d "$PAYLOAD" 2>/dev/null || echo '{"error":"submit_failed"}')
        JOB_ID=$(echo "$RESP" | json_field "job_id" "")

        if [[ -z "$JOB_ID" ]]; then
            echo "[bench] ❌ Submit failed for '$PAYLOAD_ARG'"
            J_JOB_IDS+=("")
            J_LABELS+=("$PAYLOAD_ARG")
            J_MODES+=("generate")
            : # submission timing is transport-only and never enters the report
            J_STATUS+=("submit_failed")
            continue
        fi

        echo "[bench] ✅ [generate] $JOB_ID ← '$PAYLOAD_ARG'"
        J_JOB_IDS+=("$JOB_ID")
        J_LABELS+=("$PAYLOAD_ARG")
        J_MODES+=("generate")
        : # submission timing is transport-only and never enters the report
        J_STATUS+=("submitted")

        if [[ "$WORKER_SLOTS" == "1" ]]; then
            wait_for_benchmark_job "$(( ${#J_JOB_IDS[@]} - 1 ))" "$JOB_ID" || true
        elif [[ "$WORKER_SLOTS" =~ ^[2-9][0-9]*$ ]]; then
            WINDOW_INDEXES+=("$(( ${#J_JOB_IDS[@]} - 1 ))")
            WINDOW_JOB_IDS+=("$JOB_ID")
            if (( ${#WINDOW_JOB_IDS[@]} >= WORKER_SLOTS )); then
                wait_for_any_benchmark_window || true
            fi
        fi

    elif [[ "$MODE" == "render" ]]; then
        # ── Render: POST /api/clips/render ───────────────────────────────
        ASSET_ID="$PAYLOAD_ARG"
        WM_BLOCK="{}"
        if [[ -n "$WATERMARK_ASSET_ID" ]]; then
            WM_BLOCK="{\"enabled\":true,\"asset_id\":\"${WATERMARK_ASSET_ID}\",\"position\":\"top_right\",\"opacity\":0.25}"
        elif [[ -n "$WATERMARK_TEXT" ]]; then
            WM_BLOCK="{\"enabled\":true,\"text\":\"${WATERMARK_TEXT}\",\"position\":\"top_right\",\"opacity\":0.25}"
        fi
        DEST_BLOCK="{}"
        if [[ -n "$DRIVE_FOLDER_ID" ]]; then
            DEST_BLOCK="{\"drive_folder_id\":\"${DRIVE_FOLDER_ID}\"}"
        fi

        PAYLOAD=$(cat <<EOJSON
{
  "source_asset_id": "${ASSET_ID}",
  "background": {"mode": "none"},
  "watermark": ${WM_BLOCK},
  "transcript": {"mode": "reuse_or_generate", "language": "en"},
  "subtitles": {"enabled": true, "mode": "burn"},
  "output": {"contract": "VELOX_ASSEMBLY_READY_V1", "width": 1920, "height": 1080, "fps_num": 24, "fps_den": 1},
  "audio": {"mode": "copy_if_compatible"},
  "destination": ${DEST_BLOCK},
  "execution": {"require_gpu": false}
}
EOJSON
)
        RESP=$(api POST "/api/clips/render" -d "$PAYLOAD" 2>/dev/null || echo '{"error":"submit_failed"}')
        JOB_ID=$(echo "$RESP" | json_field "job_id" "")

        if [[ -z "$JOB_ID" ]]; then
            echo "[bench] ❌ Submit failed for clip $ASSET_ID"
            J_JOB_IDS+=("")
            J_LABELS+=("$ASSET_ID")
            J_MODES+=("render")
            : # submission timing is transport-only and never enters the report
            J_STATUS+=("submit_failed")
            continue
        fi

        echo "[bench] ✅ Render $JOB_ID ← clip $ASSET_ID"
        J_JOB_IDS+=("$JOB_ID")
        J_LABELS+=("$ASSET_ID")
        J_MODES+=("render")
        : # submission timing is transport-only and never enters the report
        J_STATUS+=("submitted")

        if [[ "$WORKER_SLOTS" == "1" ]]; then
            wait_for_benchmark_job "$(( ${#J_JOB_IDS[@]} - 1 ))" "$JOB_ID" || true
        elif [[ "$WORKER_SLOTS" =~ ^[2-9][0-9]*$ ]]; then
            WINDOW_INDEXES+=("$(( ${#J_JOB_IDS[@]} - 1 ))")
            WINDOW_JOB_IDS+=("$JOB_ID")
            if (( ${#WINDOW_JOB_IDS[@]} >= WORKER_SLOTS )); then
                wait_for_any_benchmark_window || true
            fi
        fi
    fi
done

if [[ "$WORKER_SLOTS" =~ ^[2-9][0-9]*$ ]]; then
    while (( ${#WINDOW_JOB_IDS[@]} > 0 )); do
        wait_for_any_benchmark_window || break
    done
fi

echo ""
echo "[bench] All jobs submitted"

# ── Stage 2: Poll for completion ──────────────────────────────────────────
echo ""
echo "[bench] ═══ STAGE 2: POLL ═══"

POLLED=0
while true; do
    ALL_DONE=1
    for i in "${!J_JOB_IDS[@]}"; do
        JID="${J_JOB_IDS[$i]}"
        [[ -n "$JID" ]] || continue
        CUR="${J_STATUS[$i]}"
        [[ "$CUR" == "completed" || "$CUR" == "succeeded" || "$CUR" == "failed" || "$CUR" == "cancelled" || "$CUR" == "submit_failed" || "$CUR" == "timeout" ]] && continue

        RESP=$(api GET "/api/jobs/$JID" 2>/dev/null || echo '{}')
        STATUS=$(echo "$RESP" | json_field "status" "unknown" | tr '[:upper:]' '[:lower:]')
        ELAPSED=""

        log_v "  [$JID] status=$STATUS elapsed=${ELAPSED}ms"

        case "$STATUS" in
            completed|succeeded|failed|cancelled)
                J_STATUS[$i]="$STATUS"
                echo "[bench] Job $JID → $STATUS"
                ;;
            *)
                ALL_DONE=0
                ;;
        esac
    done

    [[ "$ALL_DONE" == "1" ]] && break

    POLLED=$(( POLLED + POLL_INTERVAL ))
    if (( POLLED >= POLL_MAX )); then
        echo "[bench] ⚠️  Poll timeout (${POLL_MAX}s). Marking remaining as timeout." >&2
        for i in "${!J_JOB_IDS[@]}"; do
            JID="${J_JOB_IDS[$i]}"
            [[ -n "$JID" ]] || continue
            CUR="${J_STATUS[$i]}"
            [[ "$CUR" == "completed" || "$CUR" == "succeeded" || "$CUR" == "failed" || "$CUR" == "cancelled" || "$CUR" == "submit_failed" || "$CUR" == "timeout" ]] && continue
            J_STATUS[$i]="timeout"
        done
        break
    fi

    sleep "$POLL_INTERVAL"
done

# ── Stage 3: Fetch /api/jobs/{id}/full (result + RunReport timing) ────────
# Real facts only: the /full response carries the sealed job result
# (transcript, timings, render.backend, gpu_copy_bytes, metrics_v2) plus the
# RunReport timing summary (stages, operations, critical_path, fanout). The
# report emitter parses these files — no synthetic 60/40 splits.
echo ""
echo "[bench] ═══ STAGE 3: FETCH /full (result + timing) ═══"


for i in "${!J_JOB_IDS[@]}"; do
    JID="${J_JOB_IDS[$i]}"
    if [[ -n "$JID" ]] && [[ "${J_STATUS[$i]}" != "submit_failed" ]]; then
        api GET "/api/jobs/$JID/full" > "$JOBS_DIR/job-$i.json" 2>/dev/null || echo '{}' > "$JOBS_DIR/job-$i.json"
        log_v "  [$JID] /full detail → job-$i.json ($(wc -c < "$JOBS_DIR/job-$i.json") bytes)"
    else
        echo '{}' > "$JOBS_DIR/job-$i.json"
    fi
    if [[ "${J_STATUS[$i]}" == "completed" || "${J_STATUS[$i]}" == "succeeded" ]]; then
        SUCCESS_COUNT=$(( SUCCESS_COUNT + 1 ))
    fi
done

TOTAL_ELAPSED=0 # legacy CLI argument; excluded from all metrics

# ── Stage 4: Emit wall / work / critical-path report ──────────────────────
echo ""
echo "[bench] ═══ STAGE 4: REPORT (wall / work / critical path) ═══"

# The report emitter was split out of this script's former Python heredoc
# into scripts/bench/report/ (an entry that execs its sequential parts in
# one shared namespace). Arguments are identical to the heredoc invocation.
BENCH_RESOURCE_DB="$(canonical_primary_db_path "$ROOT_DIR")" \
python3 "$ROOT_DIR/scripts/bench/report/generate_video_report.py" "$OUTPUT_FILE" "$FINGERPRINT_FILE" \
    "$GIT_SHA" "$GIT_BRANCH" "$CONFIG_SHA" "$DB_SHA" "$WORKER_IDS" "$BASE_URL" \
    "0" "0" "0" \
    "$SUCCESS_COUNT" "${#J_JOB_IDS[@]}" "$JOBS_DIR" \
    "${J_JOB_IDS[@]}" "${J_LABELS[@]}" "${J_MODES[@]}" "${J_STATUS[@]}" \
    "$WORKER_SLOTS"

echo ""
echo "[bench] ✅ Benchmark complete."


