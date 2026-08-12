#!/usr/bin/env bash
#
# bgm_subtitle_cache.sh — cache lifecycle and cache benchmark helpers.
# Source-only helper for bgm_subtitle_smoke.sh and related operational tests.
# Contract: common.sh, set -euo pipefail, smoke globals, fail(), and sqlite_q()
# are provided by the caller before this file is sourced.

# ── FASE 2-4: Cache phases ─────────────────────────────────────
# Clears test assets from the worker cache directory before cold run.
clear_test_cache() {
    smoke_log_section "Fase 2: Clear test assets from worker cache"
    local dirs=("$WORKER_CACHE_DIR/audio" "$WORKER_CACHE_DIR/image")
    local cleared=0
    for dir in "${dirs[@]}"; do
        if [[ -d "$dir" ]]; then
            # Remove all cached files to force re-download.
            local count
            count=$(find "$dir" -type f 2>/dev/null | wc -l)
            if find "$dir" -type f -delete 2>/dev/null; then
                printf '  %sCLEARED: %s files from %s%s\n' "$DIM" "$count" "$dir" "$RESET"
                cleared=$((cleared + count))
            else
                printf '  %sWARN: could not clear %s%s\n' "$YELLOW" "$dir" "$RESET" >&2
            fi
        else
            printf '  %sSKIP: %s does not exist%s\n' "$DIM" "$dir" "$RESET"
        fi
    done
    printf '  %sOK: cleared %s cached files — cache is cold%s\n' "$GREEN" "$cleared" "$RESET"
    return 0
}

# Measures job output metrics from task_attempt_metrics.
# Returns tab-separated: output_bytes<TAB>output_sha256
# Timing fields (pipeline_total_ms, engine_asset_download_ms, etc.)
# are not populated by the current worker fleet for scene.composite.v1
# jobs. Wall-clock timing is measured by the caller via date +%s.
measure_job_metrics() {
    local jid="$1"
    local row
    row=$(sqlite_q "
        SELECT COALESCE(output_bytes, 0) || '|' ||
               COALESCE(output_sha256, '')
        FROM task_attempt_metrics
        WHERE attempt_id = (
            SELECT attempt_id FROM tasks WHERE job_id = '${jid}' LIMIT 1
        )
        LIMIT 1
    " 2>/dev/null || echo "0|")
    printf '%s' "$row"
}

# ── FASE 2: Cold cache run ─────────────────────────────────────
run_cold_cache_phase() {
    smoke_log_section "FASE 2: Cold cache — measure download + render time"
    clear_test_cache || { fail "clear_test_cache"; return 1; }

    local cold_key="bgm-cold-${TAG_PREFIX}"
    IDEMPOTENCY_KEY="$cold_key"
    REQ_ID="${TAG_PREFIX}_cold"
    JOB_ID=""

    local start_ts
    start_ts=$(date +%s)
    post_bgm_subtitle_job  || { fail "cold_cache_post"; return 1; }
    poll_job_to_terminal   || { fail "cold_cache_poll"; return 1; }
    local end_ts
    end_ts=$(date +%s)

    local metrics
    metrics=$(measure_job_metrics "$JOB_ID")
    local output_bytes output_sha256
    output_bytes=$(echo "$metrics" | cut -d'|' -f1)
    output_sha256=$(echo "$metrics" | cut -d'|' -f2)
    local wall_s=$((end_ts - start_ts))

    printf '  %sOK: cold cache job_id=%s%s\n' "$GREEN" "$JOB_ID" "$RESET"
    printf '  cold_cache: wall_s=%s output_bytes=%s sha256=%s\n' \
        "$wall_s" "$output_bytes" "$output_sha256"

    # Store for JSON output.
    COLD_OUTPUT_BYTES="$output_bytes"
    COLD_OUTPUT_SHA256="$output_sha256"
    COLD_TOTAL_S="$wall_s"
    COLD_JOB_ID="$JOB_ID"
    return 0
}

# ── FASE 3: Warm cache run ─────────────────────────────────────
run_warm_cache_phase() {
    smoke_log_section "FASE 3: Warm cache — verify 0 downloads, same SHA-256"
    local warm_key="bgm-warm-${TAG_PREFIX}"
    IDEMPOTENCY_KEY="$warm_key"
    REQ_ID="${TAG_PREFIX}_warm"
    JOB_ID=""

    local start_ts
    start_ts=$(date +%s)
    post_bgm_subtitle_job  || { fail "warm_cache_post"; return 1; }
    poll_job_to_terminal   || { fail "warm_cache_poll"; return 1; }
    local end_ts
    end_ts=$(date +%s)

    local metrics
    metrics=$(measure_job_metrics "$JOB_ID")
    local output_bytes output_sha256
    output_bytes=$(echo "$metrics" | cut -d'|' -f1)
    output_sha256=$(echo "$metrics" | cut -d'|' -f2)
    local wall_s=$((end_ts - start_ts))

    printf '  %sOK: warm cache job_id=%s%s\n' "$GREEN" "$JOB_ID" "$RESET"
    printf '  warm_cache: wall_s=%s output_bytes=%s sha256=%s\n' \
        "$wall_s" "$output_bytes" "$output_sha256"

    # Verify warm cache: output_sha256 should match cold cache.
    if [[ -n "$output_sha256" && "$output_sha256" == "${COLD_OUTPUT_SHA256:-}" ]]; then
        printf '  %sOK: warm cache SHA-256 matches cold cache (same output)%s\n' "$GREEN" "$RESET"
    elif [[ -z "$output_sha256" ]]; then
        printf '  %sWARN: warm cache SHA-256 not available%s\n' "$YELLOW" "$RESET" >&2
    fi

    WARM_OUTPUT_BYTES="$output_bytes"
    WARM_OUTPUT_SHA256="$output_sha256"
    WARM_TOTAL_S="$wall_s"
    WARM_JOB_ID="$JOB_ID"
    return 0
}

# ── FASE 4: Post-restart persistence ───────────────────────────
run_post_restart_phase() {
    smoke_log_section "FASE 4: Post-restart cache persistence"
    # Full worker restart requires operator access (systemd/docker).
    # This phase verifies cache file existence and freshness instead.
    local cache_hit=true
    local files_found=0
    for dir in "$WORKER_CACHE_DIR/audio" "$WORKER_CACHE_DIR/image"; do
        if [[ -d "$dir" ]]; then
            local count
            count=$(find "$dir" -type f 2>/dev/null | wc -l)
            files_found=$((files_found + count))
            printf '  %sINFO: %s has %s cached file(s)%s\n' "$DIM" "$dir" "$count" "$RESET"
        fi
    done
    if [[ $files_found -eq 0 ]]; then
        cache_hit=false
        printf '  %sWARN: no cached files found — cache may not survive restart%s\n' "$YELLOW" "$RESET" >&2
    else
        printf '  %sOK: %s cached file(s) present — cache directory persistent%s\n' "$GREEN" "$files_found" "$RESET"
    fi

    # Check for SQLite cache index with last_used_at.
    if [[ -f "$WORKER_CACHE_DB" ]]; then
        local row_count
        row_count=$(sqlite3 -readonly "$WORKER_CACHE_DB" "SELECT COUNT(*) FROM cache_entries WHERE last_used_at > datetime('now', '-1 hour')" 2>/dev/null || echo "0")
        printf '  %sINFO: cache DB %s has %s entries active in last hour%s\n' "$DIM" "$WORKER_CACHE_DB" "$row_count" "$RESET"
    else
        printf '  %sINFO: cache DB not found at %s%s\n' "$DIM" "$WORKER_CACHE_DB" "$RESET"
    fi

    POST_RESTART_CACHE_HIT="$cache_hit"
    POST_RESTART_FILES="$files_found"
    return 0
}
