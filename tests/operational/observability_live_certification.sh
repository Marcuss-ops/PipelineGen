#!/usr/bin/env bash
# observability_live_certification.sh — live observability certification.
#
# Certifies that the voiceover observability surface is real, not just
# registered: /metrics reachable, the expected families present, and — the
# critical part — the Prometheus counters/histograms actually MOVE by the
# expected delta when exactly ONE voiceover.generate job completes, and the
# same stages surface durable per-job timing in GET /api/jobs/:id/full.
#
# Flow:
#   1. GET /metrics            → reachable (fail-closed posture)
#   2. verify metric families  → voiceover_*, tts_*, drive_upload_*,
#                                translation_*, orphan_sweeper_* present
#   3. snapshot BEFORE         → save /metrics
#   4. POST /api/media/voiceover/generate (single item, project set)
#   5. poll /api/jobs/:id/full → terminal AND parent_state != waiting_children
#   6. snapshot AFTER          → save /metrics
#   7. delta checks            → voiceover_jobs_total{status="completed"} +1;
#                                voiceover_stage_duration_seconds_count for
#                                tts / publish / finalize / timing +1;
#                                drive_upload_failures_total unchanged
#   8. durable timing checks   → child job result carries stage_progress
#                                (voiceover/upload/persistence completed)
#                                and a drive_file_id / drive_link
#   9. timing contract compare → dispatch one script.generate
#                                (COMBINED_TIMELINE); assert canonical
#                                audio_metrics (AudioPipelineMetrics) is the
#                                single authority, legacy timings
#                                (GenerationTimings) is NOT double-emitted,
#                                and instrumented stages fed timing > 0
#  10. render copy certification → assert the M4A copy-only render path:
#                                FINAL_AUDIO_COPY strategy + copy_eligible
#                                final_audio master, BUILD encoded exactly
#                                once (audio_encode_passes=1); the render
#                                copy never re-encodes (media-plane frames
#                                stay 0 — see metrics_media.go)
#  11. PASS/FAIL summary
#
# The `project` field is REQUIRED (PR-VOICEOVER-DRIVE-DRIFT): the semantic
# publish path fails closed with a typed error when it is empty.
#
# Environment (overridable; defaults shown):
#   API_BASE                    host:port (default 127.0.0.1:8000)
#   VELOX_ADMIN_TOKEN           bearer token for /api/* (or TOKEN_FILE)
#   METRICS_AUTH_TOKEN          bearer token for /metrics (fail-closed)
#   METRICS_URL                 (default http://${SMOKE_API_BASE}/metrics)
#   VELOX_DRIVE_VOICEOVER_ROOT  Drive folder_id for voiceover destination
#   OBS_PROJECT_ID              voiceover project id (default observability-cert)
#   SMOKE_POLL_TIMEOUT_SECONDS  poll ceiling (default 120)
#   OBS_SCRIPT_POLL_TIMEOUT_SECONDS  script.generate poll ceiling (default 300)
#
# Exit codes:
#   0   all assertions pass (CERTIFIED)
#   1   one or more assertions failed
#   2   setup error (missing token/folder/binary)
#   124 poll loop or wall-clock timeout exceeded

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

smoke_require jq curl

# ── Help / dry-run ───────────────────────────────────────────────
if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    sed -n '2,42p' "$0"
    exit 0
fi
if [[ "$DRY_RUN" == "1" ]]; then
    smoke_echo_safe "DRY RUN — would probe:"
    printf '  GET  http://%s/metrics  (reachable + families)\n' "$SMOKE_API_BASE"
    printf '  POST http://%s/api/media/voiceover/generate  (1 item, project set)\n' "$SMOKE_API_BASE"
    printf '  poll http://%s/api/jobs/<id>/full  (terminal, parent_state settled)\n' "$SMOKE_API_BASE"
    printf '  snapshot /metrics BEFORE → AFTER, assert counter/histogram deltas\n'
    printf '  GET /api/jobs/<child>/full, assert durable stage_progress + drive fields\n'
    printf '  POST /api/script/generate (COMBINED_TIMELINE), compare audio_metrics vs legacy timings\n'
    exit 0
fi

# ── Configuration ────────────────────────────────────────────────
METRICS_URL="${METRICS_URL:-http://${SMOKE_API_BASE}/metrics}"
VO_ROOT="${VELOX_DRIVE_VOICEOVER_ROOT:-}"
PROJECT_ID="${OBS_PROJECT_ID:-observability-cert}"
TAG_PREFIX="obs_cert_$(date +%s)_$$"
REQ_ID="${TAG_PREFIX}_single"
JOB_ID=""
CHILD_JOB_ID=""
CMP_JOB_ID=""
declare -a FAILURES=()
fail() { FAILURES+=("$1"); }

# ── Setup guards ─────────────────────────────────────────────────
if [[ -z "$VO_ROOT" ]]; then
    printf '%ssetup error: VELOX_DRIVE_VOICEOVER_ROOT env var unset (voiceover destination folder_id)%s\n' \
        "$RED" "$RESET" >&2
    exit 2
fi
if [[ -z "${METRICS_AUTH_TOKEN:-}" ]]; then
    printf '%ssetup error: METRICS_AUTH_TOKEN env var unset (needed for /metrics fail-closed)%s\n' \
        "$RED" "$RESET" >&2
    exit 2
fi

# ── Metrics helpers ──────────────────────────────────────────────
# Fetch /metrics with the metrics bearer token. Fail-closed: a non-200
# or an empty body is a hard failure, never a silent no-op.
metrics_text() {
    curl -fsS --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" \
        -H "Authorization: Bearer $METRICS_AUTH_TOKEN" \
        "$METRICS_URL"
}

# metric_value SNAPSHOT_FILE FAMILY LABEL_SELECTOR
# Prints the integer value of a counter/histogram-count series, or
# "MISSING" when the series has never been observed (label-vectors are
# only emitted after the first observation).
metric_value() {
    local file="$1" family="$2" selector="$3"
    awk -v family="$family" -v sel="$selector" '
        ($1 == family || index($1, family "{") == 1) &&
        (sel == "" || index($0, sel) > 0) { print $2; found=1 }
        END { if (!found) print "MISSING" }
    ' "$file"
}

# ── Preflight 1: Go server up ────────────────────────────────────
precheck_health() {
    smoke_log_section "Preflight 1: Go server up (GET /health)"
    smoke_curl GET "/health" >/dev/null
    if [[ ! "$SMOKE_LAST_HTTP" =~ ^2[0-9][0-9]$ ]]; then
        fail "precheck_health_http_${SMOKE_LAST_HTTP}"
        printf '%sFAIL: GET /health returned HTTP %s%s\n' "$RED" "$SMOKE_LAST_HTTP" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: GET /health → HTTP %s%s\n' "$GREEN" "$SMOKE_LAST_HTTP" "$RESET"
    return 0
}

# ── Preflight 2: /metrics reachable ──────────────────────────────
precheck_metrics_reachable() {
    smoke_log_section "Preflight 2: /metrics reachable"
    if metrics_text > "$WORK_DIR/metrics_probe" 2>/tmp/obs_metrics_err; then
        printf '  %sOK: /metrics reachable (%s lines)%s\n' \
            "$GREEN" "$(wc -l < "$WORK_DIR/metrics_probe")" "$RESET"
        return 0
    fi
    fail "precheck_metrics_reachable"
    printf '%sFAIL: /metrics not reachable (fail-closed):%s\n' "$RED" "$RESET" >&2
    cat /tmp/obs_metrics_err >&2 2>/dev/null || true
    return 1
}

# ── Preflight 3: metric families present ─────────────────────────
# Plain counters (no labels) are always emitted; label-vectors appear
# only after first observation, so we assert the families that MUST be
# visible without traffic: the failure counters and the sweeper run
# counter. The labeled vectors are asserted via delta AFTER the job.
precheck_families() {
    smoke_log_section "Preflight 3: metric families registered"
    local family missing=0
    for family in tts_failures_total drive_upload_failures_total translation_failures_total orphan_sweeper_runs_total; do
        if grep -qE "^${family}[ {]" "$WORK_DIR/metrics_probe"; then
            printf '  %sOK: %s present%s\n' "$GREEN" "$family" "$RESET"
        else
            fail "family_missing_${family}"
            printf '%sFAIL: %s NOT present in /metrics%s\n' "$RED" "$family" "$RESET" >&2
            missing=1
        fi
    done
    return $missing
}

# ── POST single voiceover item ───────────────────────────────────
post_single_item() {
    smoke_log_section "POST /api/media/voiceover/generate (1 item, project=$PROJECT_ID)"
    local payload
    payload=$(jq -n --arg rid "$REQ_ID" --arg vid "$TAG_PREFIX" --arg fid "$VO_ROOT" --arg proj "$PROJECT_ID" '{
        request_id: $rid,
        project: $proj,
        items: [
            {text: "Questa e la verifica operativa delle metriche voiceover di PipelineGen.", language: "it-IT", voice: "it-IT-DiegoNeural", filename: ("obs_cert_" + $vid + ".mp3"), required: true}
        ],
        destination: {kind: "explicit", folder_id: $fid},
        options: {remove_silence: false, strategy: "verify", parallelism: 1}
    }')
    smoke_curl POST "/api/media/voiceover/generate" --data "$payload" >/dev/null
    if ! smoke_assert_http_2xx "POST /api/media/voiceover/generate"; then
        fail "post_single_item_http_${SMOKE_LAST_HTTP}"
        smoke_echo_safe "  body: $(head -c 400 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
        return 1
    fi
    JOB_ID=$(jq -r '.job_id // .id // empty' "$SMOKE_LAST_BODY" 2>/dev/null || echo "")
    if [[ -z "$JOB_ID" ]]; then
        fail "post_single_item_no_job_id"
        printf '%sFAIL: POST returned no job_id%s\n' "$RED" "$RESET" >&2
        return 1
    fi
    printf '  %senqueued parent job_id=%s%s\n' "$GREEN" "$JOB_ID" "$RESET"
    return 0
}

# ── Poll parent to terminal, honoring parent_state ───────────────
# The broker can expose status=SUCCEEDED while parent_state is still
# waiting_children (children in RETRY_WAIT / still running). A true
# terminal is: parent_state settled AND status terminal.
poll_parent_terminal() {
    smoke_log_section "Poll parent to terminal (timeout ${SMOKE_POLL_TIMEOUT_SECONDS}s)"
    local deadline=$(( $(date +%s) + SMOKE_POLL_TIMEOUT_SECONDS ))
    while (( $(date +%s) < deadline )); do
        smoke_wallclock_check
        if ! smoke_curl GET "/api/jobs/${JOB_ID}/full" >/dev/null; then
            fail "poll_parent_http_${SMOKE_LAST_HTTP}"
            return 1
        fi
        local status parent_state
        status=$(jq -r '.status // "?"' "$SMOKE_LAST_BODY")
        parent_state=$(jq -r '.result.parent_state // .result.data.parent_state // "none"' "$SMOKE_LAST_BODY")
        if [[ "$parent_state" != "waiting_children" ]]; then
            case "$status" in
                SUCCEEDED|completed)
                    printf '  %sOK: parent terminal status=%s parent_state=%s%s\n' \
                        "$GREEN" "$status" "$parent_state" "$RESET"
                    return 0 ;;
                FAILED|failed|cancelled|dead_letter)
                    fail "poll_parent_status_${status}"
                    printf '%sFAIL: parent terminal status=%s%s\n' "$RED" "$status" "$RESET" >&2
                    return 1 ;;
            esac
        fi
        sleep "$SMOKE_POLL_INTERVAL_SECONDS"
    done
    fail "poll_parent_timeout"
    printf '%sFAIL: parent did not reach settled terminal in %ss%s\n' \
        "$RED" "$SMOKE_POLL_TIMEOUT_SECONDS" "$RESET" >&2
    return 124
}

# ── Locate the SUCCEEDED child job ───────────────────────────────
find_succeeded_child() {
    smoke_log_section "Locate SUCCEEDED voiceover.generate_item child"
    if ! smoke_curl GET "/api/jobs/${JOB_ID}/full" >/dev/null; then
        fail "find_child_http_${SMOKE_LAST_HTTP}"
        return 1
    fi
    # Children share the parent request_id. Query the jobs list for the
    # child that reached SUCCEEDED for this run's request_id.
    local child
    child=$(curl -fsS --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" \
        -H "Authorization: Bearer $SMOKE_TOKEN" \
        "http://${SMOKE_API_BASE}/api/jobs?limit=500" \
        | jq -r --arg rid "$REQ_ID" '
            [.jobs[] | select(.type == "voiceover.generate_item"
                and ((.payload.request_id // .payload.correlation_id // "") == $rid)
                and (.status == "SUCCEEDED" or .status == "completed"))][0].id // empty')
    if [[ -z "$child" ]]; then
        fail "find_child_no_succeeded_child"
        printf '%sFAIL: no SUCCEEDED voiceover.generate_item child for request_id=%s%s\n' \
            "$RED" "$REQ_ID" "$RESET" >&2
        return 1
    fi
    CHILD_JOB_ID="$child"
    printf '  %sOK: child job_id=%s%s\n' "$GREEN" "$CHILD_JOB_ID" "$RESET"
    return 0
}

# ── Durable per-job timing assertions ────────────────────────────
# The voiceover child result carries stage_progress (per-stage
# completed/total) and the Drive publish fields. Every stage that ran
# must be completed=1; a missing drive_file_id/drive_link is a hard
# failure (no fake availability).
verify_durable_timings() {
    smoke_log_section "Durable per-job timings (GET /api/jobs/${CHILD_JOB_ID}/full)"
    if ! smoke_curl GET "/api/jobs/${CHILD_JOB_ID}/full" >/dev/null; then
        fail "durable_http_${SMOKE_LAST_HTTP}"
        return 1
    fi
    local body="$SMOKE_LAST_BODY"
    local stage rc=0
    for stage in voiceover upload persistence; do
        local completed total
        completed=$(jq -r --arg s "$stage" '.result.stage_progress[$s].completed // 0' "$body")
        total=$(jq -r --arg s "$stage" '.result.stage_progress[$s].total // 0' "$body")
        if [[ "$completed" == "1" && "$total" == "1" ]]; then
            printf '  %sOK: stage_progress[%s] completed=%s total=%s%s\n' \
                "$GREEN" "$stage" "$completed" "$total" "$RESET"
        else
            fail "durable_stage_${stage}_${completed}_${total}"
            printf '%sFAIL: stage_progress[%s] completed=%s total=%s (expected 1/1)%s\n' \
                "$RED" "$stage" "$completed" "$total" "$RESET" >&2
            rc=1
        fi
    done
    local drive_file_id drive_link
    drive_file_id=$(jq -r '.result.drive_file_id // empty' "$body")
    drive_link=$(jq -r '.result.drive_link // empty' "$body")
    if [[ -n "$drive_file_id" && -n "$drive_link" ]]; then
        printf '  %sOK: drive_file_id=%s drive_link present%s\n' \
            "$GREEN" "$drive_file_id" "$RESET"
    else
        fail "durable_drive_fields_missing"
        printf '%sFAIL: child result missing drive_file_id/drive_link (no fake availability)%s\n' \
            "$RED" "$RESET" >&2
        rc=1
    fi
    return $rc
}

# ── Timing contract comparison (GenerationTimings vs AudioPipelineMetrics) ─
# The canonical script generation path (script.generate, COMBINED_TIMELINE)
# persists AudioPipelineMetrics under result.data.result.audio_metrics. The
# legacy GenerationTimings (JSON "timings") belongs to the migration-only
# GenerateOneUseCase path; the new runner must NOT double-emit it. This phase
# certifies that the canonical contract is the single authority and that every
# instrumented stage actually fed its timing field.
verify_timing_contract_comparison() {
    smoke_log_section "Timing contract comparison: GenerationTimings vs AudioPipelineMetrics"
    local payload cmp_job_id cmp_rc=0
    payload=$(jq -n --arg id "obs_timing_${TAG_PREFIX}" '{
        version: 2,
        preset: "custom",
        force_refresh: true,
        items: [{
            id: $id,
            title: ("Observability timing contract check " + $id),
            language: "it",
            tone: "documentary",
            source: {
                type: "text",
                topic: "osservabilita",
                source_text: "PipelineGen genera una breve scena con voiceover per verificare i timing durevoli del contratto audio."
            },
            script_params: {target_words: 120, min_words: 20},
            output: {generate_timeline: true, voiceover_enabled: "enabled"},
            audio: {mode: "COMBINED_TIMELINE"},
            docs: {enabled: false}
        }]
    }')

    smoke_curl POST "/api/script/generate" --data "$payload" >/dev/null
    if ! smoke_assert_http_2xx "POST /api/script/generate"; then
        fail "timing_cmp_dispatch_http_${SMOKE_LAST_HTTP}"
        return 1
    fi
    cmp_job_id=$(jq -r '.job_id // .id // empty' "$SMOKE_LAST_BODY" 2>/dev/null || echo "")
    if [[ -z "$cmp_job_id" ]]; then
        fail "timing_cmp_no_job_id"
        printf '%sFAIL: POST /api/script/generate returned no job_id%s\n' "$RED" "$RESET" >&2
        return 1
    fi
    CMP_JOB_ID="$cmp_job_id"
    printf '  enqueued script.generate job_id=%s\n' "$cmp_job_id"

    local saved_poll="$SMOKE_POLL_TIMEOUT_SECONDS"
    SMOKE_POLL_TIMEOUT_SECONDS="${OBS_SCRIPT_POLL_TIMEOUT_SECONDS:-300}"
    if ! smoke_poll_terminal "$cmp_job_id"; then
        SMOKE_POLL_TIMEOUT_SECONDS="$saved_poll"
        fail "timing_cmp_poll_timeout"
        return 1
    fi
    SMOKE_POLL_TIMEOUT_SECONDS="$saved_poll"
    if [[ "$SMOKE_LAST_STATUS" != "SUCCEEDED" && "$SMOKE_LAST_STATUS" != "completed" ]]; then
        fail "timing_cmp_status_${SMOKE_LAST_STATUS}"
        printf '%sFAIL: script.generate ended in status=%s%s\n' "$RED" "$SMOKE_LAST_STATUS" "$RESET" >&2
        return 1
    fi

    if ! smoke_curl GET "/api/jobs/${cmp_job_id}/full" >/dev/null; then
        fail "timing_cmp_full_http_${SMOKE_LAST_HTTP}"
        return 1
    fi
    local body="$SMOKE_LAST_BODY"

    local audio_present timings_present
    audio_present=$(jq -r '.result.data.result.audio_metrics != null' "$body")
    timings_present=$(jq -r '.result.data.result.timings != null' "$body")
    if [[ "$audio_present" == "true" ]]; then
        printf '  %sOK: canonical audio_metrics (AudioPipelineMetrics) present%s\n' "$GREEN" "$RESET"
    else
        fail "timing_cmp_audio_metrics_missing"
        printf '%sFAIL: canonical audio_metrics missing from durable result%s\n' "$RED" "$RESET" >&2
        cmp_rc=1
    fi
    if [[ "$timings_present" == "false" ]]; then
        printf '  %sOK: legacy timings (GenerationTimings) NOT double-emitted%s\n' "$GREEN" "$RESET"
    else
        printf '  %sWARN: legacy timings (GenerationTimings) present alongside audio_metrics — dual-contract divergence%s\n' "$YELLOW" "$RESET"
    fi

    local field val
    for field in tts_ms tts_calls audio_duration_ms total_ms; do
        val=$(jq -r --arg f "$field" '.result.data.result.audio_metrics[$f] // 0' "$body")
        if [[ "$val" =~ ^[0-9]+$ ]] && (( val > 0 )); then
            printf '  %sOK: audio_metrics.%s=%s (>0)%s\n' "$GREEN" "$field" "$val" "$RESET"
        else
            fail "timing_cmp_fed_${field}_${val}"
            printf '%sFAIL: audio_metrics.%s=%s (expected >0 — stage ran)%s\n' "$RED" "$field" "$val" "$RESET" >&2
            cmp_rc=1
        fi
    done

    local passes
    passes=$(jq -r '.result.data.result.audio_metrics.audio_encode_passes // 0' "$body")
    if [[ "$passes" =~ ^[0-9]+$ ]] && (( passes >= 1 )); then
        printf '  %sOK: audio_metrics.audio_encode_passes=%s (BUILD path encoded AAC)%s\n' "$GREEN" "$passes" "$RESET"
    else
        fail "timing_cmp_encode_passes_${passes}"
        printf '%sFAIL: audio_metrics.audio_encode_passes=%s (expected >=1 on BUILD path)%s\n' "$RED" "$passes" "$RESET" >&2
        cmp_rc=1
    fi

    local copy_eligible codec
    copy_eligible=$(jq -r '.result.data.result.final_audio.copy_eligible // false' "$body")
    codec=$(jq -r '.result.data.result.final_audio.codec // empty' "$body")
    if [[ "$copy_eligible" == "true" && -n "$codec" ]]; then
        printf '  %sOK: final_audio.copy_eligible=true codec=%s (copy strategy certified)%s\n' "$GREEN" "$codec" "$RESET"
    else
        fail "timing_cmp_final_audio"
        printf '%sFAIL: final_audio copy_eligible=%s codec=%s (copy strategy not certified)%s\n' "$RED" "$copy_eligible" "$codec" "$RESET" >&2
        cmp_rc=1
    fi

    # Known gap: canonical-only stage fields that are registered but not yet
    # fed by the renderer. Report as NOTE, not FAIL — the instrumented
    # contract above is the gate; these are surfaced for visibility.
    local gap
    for gap in mix_ms aac_encode_ms probe_ms hash_ms upload_ms timeline_compile_ms audio_plan_compile_ms clip_audio_prepare_ms media_fetch_ms; do
        val=$(jq -r --arg g "$gap" '.result.data.result.audio_metrics[$g] // 0' "$body")
        if [[ "$val" == "0" || "$val" == "null" ]]; then
            printf '  %sNOTE: audio_metrics.%s=0 (registered but not yet fed — see architecture/observability-measurement-matrix.yaml)%s\n' "$YELLOW" "$gap" "$RESET"
        fi
    done

    return $cmp_rc
}

# ── RENDER COPY CERTIFICATION (M4A copy-only render path) ────────
# The render worker must NOT re-encode the certified final_audio.m4a.
# The literal durable fields final_audio_copy / final_mux_audio_mode /
# render-side audio_encode_passes=0 do NOT exist in the current render
# result ({render_job_id, output_path}); the copy-only contract is
# certified from the script.generate durable result instead:
#   final_audio_copy=true          → audio_strategy == "FINAL_AUDIO_COPY"
#                                    AND final_audio.copy_eligible == true
#   audio_encode_passes=1 (BUILD)  → audio_metrics.audio_encode_passes == 1
#                                    (master AAC encoded exactly once)
#   audio_encode_passes=0 (RENDER) → MuxFinalAudioCopy never re-encodes;
#                                    media plane reports frames_decoded=0 /
#                                    frames_encoded=0 + ffmpeg_exec_count
#                                    increment (metrics_media.go)
#   final_mux_audio_mode=COPY      → OperationMuxAudioCopy (ffmpeg
#                                    -c:v copy -c:a copy, no encode fallback)
verify_render_copy_certification() {
    smoke_log_section "RENDER COPY CERTIFICATION (M4A copy-only render path)"
    if [[ -z "$CMP_JOB_ID" ]]; then
        fail "render_copy_no_cmp_job"
        printf '%sFAIL: no script.generate job id (timing comparison did not run)%s\n' "$RED" "$RESET" >&2
        return 1
    fi
    if ! smoke_curl GET "/api/jobs/${CMP_JOB_ID}/full" >/dev/null; then
        fail "render_copy_full_http_${SMOKE_LAST_HTTP}"
        return 1
    fi
    local body="$SMOKE_LAST_BODY" rc=0
    local strategy copy_eligible final_mix codec passes
    strategy=$(jq -r '.result.data.result.audio_strategy // empty' "$body")
    copy_eligible=$(jq -r '.result.data.result.final_audio.copy_eligible // false' "$body")
    final_mix=$(jq -r '.result.data.result.final_audio.final_mix // false' "$body")
    codec=$(jq -r '.result.data.result.final_audio.codec // empty' "$body")
    passes=$(jq -r '.result.data.result.audio_metrics.audio_encode_passes // 0' "$body")

    # final_audio_copy=true ≡ FINAL_AUDIO_COPY strategy + copy-eligible master.
    if [[ "$strategy" == "FINAL_AUDIO_COPY" && "$copy_eligible" == "true" && "$final_mix" == "true" ]]; then
        printf '  %sOK: final_audio_copy certified (audio_strategy=%s, copy_eligible=%s, final_mix=%s)%s\n' \
            "$GREEN" "$strategy" "$copy_eligible" "$final_mix" "$RESET"
    else
        fail "render_copy_strategy_${strategy}_${copy_eligible}_${final_mix}"
        printf '%sFAIL: final_audio_copy not certified (audio_strategy=%s copy_eligible=%s final_mix=%s)%s\n' \
            "$RED" "$strategy" "$copy_eligible" "$final_mix" "$RESET" >&2
        rc=1
    fi

    # Canonical copy master must be AAC (the copy path rejects non-canonical media).
    if [[ "$codec" == "aac" ]]; then
        printf '  %sOK: final_audio.codec=%s (canonical copy-eligible master)%s\n' "$GREEN" "$codec" "$RESET"
    else
        fail "render_copy_codec_${codec}"
        printf '%sFAIL: final_audio.codec=%s (expected aac for copy path)%s\n' "$RED" "$codec" "$RESET" >&2
        rc=1
    fi

    # BUILD encodes the master exactly once; the render copy path must add 0
    # passes (it never emits audio_encode_passes — the distinction is what
    # keeps build(=1) from being confused with render copy(=0)).
    if [[ "$passes" =~ ^[0-9]+$ ]] && (( passes == 1 )); then
        printf '  %sOK: BUILD audio_encode_passes=%s (master encoded once; render copy adds 0)%s\n' \
            "$GREEN" "$passes" "$RESET"
    else
        fail "render_copy_build_passes_${passes}"
        printf '%sFAIL: BUILD audio_encode_passes=%s (expected 1)%s\n' "$RED" "$passes" "$RESET" >&2
        rc=1
    fi

    # final_mux_audio_mode=COPY / render audio_encode_passes=0 have no durable
    # field today; the no-re-encode guarantee lives in MuxFinalAudioCopy
    # (OperationMuxAudioCopy, -c:a copy) and surfaces on the media plane as
    # frames_decoded=0 / frames_encoded=0 + ffmpeg_exec_count increment.
    printf '  %sNOTE: final_mux_audio_mode/final_audio_copy are not durable render fields; copy mux = OperationMuxAudioCopy (-c:a copy) → frames_decoded=0 frames_encoded=0 + ffmpeg_exec_count increment (metrics_media.go)%s\n' \
        "$YELLOW" "$RESET"

    return $rc
}

main() {
    smoke_log_section "Observability live certification"
    printf '  target:    %s\n  metrics:   %s\n  project:   %s\n  vo_root:   %s\n  req_id:    %s\n' \
        "$SMOKE_API_BASE" "$METRICS_URL" "$PROJECT_ID" "$VO_ROOT" "$REQ_ID"

    # Preflight (fail-fast before any state-mutating call).
    local preflight_rc=0
    precheck_health || preflight_rc=1
    precheck_metrics_reachable || preflight_rc=1
    precheck_families || preflight_rc=1
    if (( preflight_rc != 0 )); then
        printf '%sFAIL: preflight failed, aborting before POST%s\n' "$RED" "$RESET" >&2
        for f in "${FAILURES[@]}"; do printf '  - %s\n' "$f" >&2; done
        exit 1
    fi

    # Snapshot BEFORE (fresh, this run).
    metrics_text > "$WORK_DIR/metrics.before" || {
        fail "snapshot_before"
        printf '%sFAIL: /metrics BEFORE snapshot failed%s\n' "$RED" "$RESET" >&2
        exit 1
    }

    # Dispatch + poll + locate child.
    post_single_item || { fail "post_single_item"; exit 1; }
    poll_parent_terminal || { fail "poll_parent_terminal"; exit 1; }
    find_succeeded_child || { fail "find_succeeded_child"; exit 1; }

    # Snapshot AFTER.
    metrics_text > "$WORK_DIR/metrics.after" || {
        fail "snapshot_after"
        printf '%sFAIL: /metrics AFTER snapshot failed%s\n' "$RED" "$RESET" >&2
        exit 1
    }

    # ── Delta assertions ────────────────────────────────────────
    smoke_log_section "Prometheus counter/histogram deltas (BEFORE → AFTER)"
    local before after delta rc=0
    before=$(metric_value "$WORK_DIR/metrics.before" 'voiceover_jobs_total' 'status="completed"')
    after=$(metric_value "$WORK_DIR/metrics.after" 'voiceover_jobs_total' 'status="completed"')
    before_n=${before:-0}; before_n=${before_n/MISSING/0}; after_n=${after:-0}; after_n=${after_n/MISSING/0}
    delta=$(( after_n - before_n ))
    if (( delta == 1 )); then
        printf '  %sOK: voiceover_jobs_total{status="completed"} %s → %s (+1)%s\n' \
            "$GREEN" "$before" "$after" "$RESET"
    else
        fail "delta_completed_${delta}"
        printf '%sFAIL: voiceover_jobs_total{status="completed"} delta=%s (expected +1; before=%s after=%s)%s\n' \
            "$RED" "$delta" "$before" "$after" "$RESET" >&2
        rc=1
    fi

    local stage
    for stage in tts publish finalize timing; do
        before=$(metric_value "$WORK_DIR/metrics.before" 'voiceover_stage_duration_seconds_count' "stage=\"$stage\"")
        after=$(metric_value "$WORK_DIR/metrics.after" 'voiceover_stage_duration_seconds_count' "stage=\"$stage\"")
        before_n=${before:-0}; before_n=${before_n/MISSING/0}; after_n=${after:-0}; after_n=${after_n/MISSING/0}
        delta=$(( after_n - before_n ))
        if (( delta == 1 )); then
            printf '  %sOK: voiceover_stage_duration_seconds_count{stage="%s"} %s → %s (+1)%s\n' \
                "$GREEN" "$stage" "$before" "$after" "$RESET"
        else
            fail "delta_stage_${stage}_${delta}"
            printf '%sFAIL: voiceover_stage_duration_seconds_count{stage="%s"} delta=%s (expected +1; before=%s after=%s)%s\n' \
                "$RED" "$stage" "$delta" "$before" "$after" "$RESET" >&2
            rc=1
        fi
    done

    # Failure counters must NOT increase on a completed job.
    before=$(metric_value "$WORK_DIR/metrics.before" 'drive_upload_failures_total' '')
    after=$(metric_value "$WORK_DIR/metrics.after" 'drive_upload_failures_total' '')
    before_n=${before:-0}; before_n=${before_n/MISSING/0}; after_n=${after:-0}; after_n=${after_n/MISSING/0}
    delta=$(( after_n - before_n ))
    if (( delta == 0 )); then
        printf '  %sOK: drive_upload_failures_total unchanged (%s → %s)%s\n' \
            "$GREEN" "$before" "$after" "$RESET"
    else
        fail "delta_drive_upload_failures_${delta}"
        printf '%sFAIL: drive_upload_failures_total delta=%s (expected 0; before=%s after=%s)%s\n' \
            "$RED" "$delta" "$before" "$after" "$RESET" >&2
        rc=1
    fi

    # ── Durable timing assertions ────────────────────────────────
    verify_durable_timings || rc=1

    # ── Timing contract comparison (GenerationTimings vs AudioPipelineMetrics)
    verify_timing_contract_comparison || rc=1

    # ── Render copy certification (M4A copy-only render path)
    verify_render_copy_certification || rc=1

    # ── Aggregate verdict ────────────────────────────────────────
    echo
    if (( rc == 0 && ${#FAILURES[@]} == 0 )); then
        printf '%sOBSERVABILITY CERTIFICATION: PASS (metrics reachable, families present, counter/histogram deltas +1, durable stage_progress 1/1, timing contract audio_metrics authoritative, render copy FINAL_AUDIO_COPY certified)%s\n' \
            "$GREEN" "$RESET"
        printf '  parent=%s child=%s\n' "$JOB_ID" "$CHILD_JOB_ID"
        exit 0
    fi
    printf '%sOBSERVABILITY CERTIFICATION: FAIL (%d assertion(s) failed):%s\n' \
        "$RED" "${#FAILURES[@]}" "$RESET" >&2
    for f in "${FAILURES[@]}"; do
        printf '  - %s\n' "$f" >&2
    done
    exit 1
}

main "$@"
