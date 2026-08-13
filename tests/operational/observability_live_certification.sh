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
#   9. PASS/FAIL summary
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

    # ── Aggregate verdict ────────────────────────────────────────
    echo
    if (( rc == 0 && ${#FAILURES[@]} == 0 )); then
        printf '%sOBSERVABILITY CERTIFICATION: PASS (metrics reachable, families present, counter/histogram deltas +1, durable stage_progress 1/1, Drive fields present)%s\n' \
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
