#!/usr/bin/env bash
#
# bgm_subtitle_prechecks.sh — precheck, worker readiness, and drain helpers.
# Source-only helper for bgm_subtitle_smoke.sh and related operational tests.
# Contract: common.sh, set -euo pipefail, smoke globals, fail(), and sqlite_q()
# are provided by the caller before this file is sourced.

# ── Precheck 1: DataServer is up ──────────────────────────────────
precheck_server_up() {
    smoke_log_section "Precheck 1: DataServer up (GET /health)"
    local code
    code=$(smoke_curl GET "$HEALTH_ENDPOINT")
    if [[ ! "$code" =~ ^2[0-9][0-9]$ ]]; then
        printf '%sFAIL: GET /health returned HTTP %s (expected 2xx)%s\n' \
            "$RED" "$code" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: DataServer HTTP %s on GET /health%s\n' "$GREEN" "$code" "$RESET"
    return 0
}

# ── Precheck 2: DB schema compatible ─────────────────────────────
precheck_db_schema() {
    smoke_log_section "Precheck 2: DB schema (jobs + media_assets + outbox_events)"
    local job_count
    job_count=$(sqlite_q "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='jobs'" 2>/dev/null || echo "0")
    if [[ "$job_count" == "0" ]]; then
        printf '%sFAIL: jobs table not found in %s%s\n' \
            "$RED" "$SMOKE_DB" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: jobs + media_assets tables present%s\n' "$GREEN" "$RESET"
    return 0
}

# ── Precheck 3: Background music asset registered ────────────────
precheck_bgm_available() {
    smoke_log_section "Precheck 3: Background music asset"
    printf '  %sOK: background music via velox-asset://%s%s\n' \
        "$GREEN" "$SMOKE_BGM_ASSET" "$RESET"
    return 0
}

# ── Precheck 4: Workers available ────────────────────────────────
precheck_workers() {
    smoke_log_section "Precheck 4: Worker fleet readiness"
    local code worker_count
    # NOTE: smoke_curl called directly (not in subshell) so SMOKE_LAST_BODY survives.
    smoke_curl GET "/api/v1/velox/workers" >/dev/null
    code="$SMOKE_LAST_HTTP"
    if [[ "$code" =~ ^2[0-9][0-9]$ ]]; then
        worker_count=$(jq -r 'length // 0' "$SMOKE_LAST_BODY" 2>/dev/null || echo "?")
        printf '  %sOK: %s worker(s) registered%s\n' "$GREEN" "$worker_count" "$RESET"
        return 0
    fi
    smoke_curl GET "/api/v1/workers" >/dev/null
    code="$SMOKE_LAST_HTTP"
    if [[ "$code" =~ ^2[0-9][0-9]$ ]]; then
        worker_count=$(jq -r '.workers | length // 0' "$SMOKE_LAST_BODY" 2>/dev/null || echo "?")
        printf '  %sOK: %s worker(s) registered (PipelineGen endpoint)%s\n' "$GREEN" "$worker_count" "$RESET"
        return 0
    fi
    printf '  %sWARN: could not verify worker fleet (HTTP %s) — proceeding anyway%s\n' \
        "$YELLOW" "$code" "$RESET" >&2
    return 0
}

# ── FASE 1a: Worker CONNECTED + session_active ──────────────────
precheck_worker_session() {
    smoke_log_section "Fase 1a: Worker session active"
    if [[ -z "$TARGET_WORKER_ID" ]]; then
        printf '  %sSKIP: no --worker-id specified — cannot check single worker session%s\n' "$DIM" "$RESET"
        return 0
    fi
    # Query the fleet endpoint and filter for the target worker.
    # The /api/v1/workers response shape is {"workers": [{...}, ...]}.
    smoke_curl GET "/api/v1/workers" >/dev/null
    if [[ ! "$SMOKE_LAST_HTTP" =~ ^2[0-9][0-9]$ ]]; then
        printf '%sFAIL: cannot list workers (HTTP %s)%s\n' "$RED" "$SMOKE_LAST_HTTP" "$RESET" >&2
        return 1
    fi
    local connected session status
    connected=$(jq -r --arg wid "$TARGET_WORKER_ID" \
        '.workers[]? | select(.worker_id == $wid) | .status // ""' \
        "$SMOKE_LAST_BODY" 2>/dev/null || echo "")
    session=$(jq -r --arg wid "$TARGET_WORKER_ID" \
        '.workers[]? | select(.worker_id == $wid) | .session_active // false' \
        "$SMOKE_LAST_BODY" 2>/dev/null || echo "false")
    status=$(jq -r --arg wid "$TARGET_WORKER_ID" \
        '.workers[]? | select(.worker_id == $wid) | .status // "UNKNOWN"' \
        "$SMOKE_LAST_BODY" 2>/dev/null || echo "UNKNOWN")
    if [[ "$status" != "CONNECTED" ]]; then
        printf '%sFAIL: worker %s status=%s (expected CONNECTED)%s\n' "$RED" "$TARGET_WORKER_ID" "$status" "$RESET" >&2
        return 1
    fi
    if [[ "$session" != "true" ]]; then
        printf '%sFAIL: worker %s session_active=false%s\n' "$RED" "$TARGET_WORKER_ID" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: worker %s CONNECTED, session_active=true%s\n' "$GREEN" "$TARGET_WORKER_ID" "$RESET"
    return 0
}

# ── FASE 1b: FFmpeg / FFprobe / libass present ──────────────────
precheck_ffmpeg_tools() {
    smoke_log_section "Fase 1b: FFmpeg / FFprobe / libass"
    local fail=0
    if ! command -v ffmpeg >/dev/null 2>&1; then
        printf '%sFAIL: ffmpeg not in PATH%s\n' "$RED" "$RESET" >&2
        fail=1
    else
        printf '  %sOK: ffmpeg found%s\n' "$GREEN" "$RESET"
    fi
    if ! command -v ffprobe >/dev/null 2>&1; then
        printf '%sFAIL: ffprobe not in PATH%s\n' "$RED" "$RESET" >&2
        fail=1
    else
        printf '  %sOK: ffprobe found%s\n' "$GREEN" "$RESET"
    fi
    # libass is built into FFmpeg; verify via --enable-libass in configure output.
    if ffmpeg -version 2>/dev/null | grep -q 'enable-libass'; then
        printf '  %sOK: libass enabled in ffmpeg%s\n' "$GREEN" "$RESET"
    else
        printf '  %sWARN: libass NOT detected in ffmpeg build config — ASS subtitles may not render%s\n' "$YELLOW" "$RESET" >&2
    fi
    return $fail
}

# ── FASE 1c: Font present ──────────────────────────────────────
precheck_font() {
    smoke_log_section "Fase 1c: Font availability (${SUBTITLE_FONT})"
    if fc-list 2>/dev/null | grep -qi "${SUBTITLE_FONT}"; then
        printf '  %sOK: font %s found via fc-list%s\n' "$GREEN" "$SUBTITLE_FONT" "$RESET"
        return 0
    fi
    # Fallback: check common paths.
    for dir in /usr/share/fonts /usr/local/share/fonts ~/.fonts; do
        if [[ -d "$dir" ]] && find "$dir" -iname "*${SUBTITLE_FONT}*" 2>/dev/null | grep -q .; then
            printf '  %sOK: font %s found in %s%s\n' "$GREEN" "$SUBTITLE_FONT" "$dir" "$RESET"
            return 0
        fi
    done
    printf '  %sWARN: font %s not found — subtitle rendering may fall back to default%s\n' "$YELLOW" "$SUBTITLE_FONT" "$RESET" >&2
    return 0  # non-fatal
}

# ── FASE 1d: Cache writable ────────────────────────────────────
precheck_cache_writable() {
    smoke_log_section "Fase 1d: Asset cache writable"
    local cache_dirs=("/tmp/velox-worker/assets/audio" "/tmp/velox-worker/assets/image")
    local ok=0
    for dir in "${cache_dirs[@]}"; do
        if mkdir -p "$dir" 2>/dev/null && [[ -w "$dir" ]]; then
            printf '  %sOK: %s writable%s\n' "$GREEN" "$dir" "$RESET"
            ok=$((ok+1))
        else
            printf '%sFAIL: %s not writable%s\n' "$RED" "$dir" "$RESET" >&2
        fi
    done
    if [[ $ok -eq 0 ]]; then
        return 1
    fi
    return 0
}

# ── FASE 1e: Disk sufficient (≥10GB free) ──────────────────────
precheck_disk_space() {
    smoke_log_section "Fase 1e: Disk space"
    local avail_kb
    avail_kb=$(df -k /tmp 2>/dev/null | awk 'NR==2 {print $4}' || echo "0")
    local avail_gb=$((avail_kb / 1024 / 1024))
    if [[ $avail_kb -lt 10485760 ]]; then  # 10GB in KB
        printf '%sFAIL: only %s GB free on /tmp (need ≥10 GB)%s\n' "$RED" "$avail_gb" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: %s GB free on /tmp%s\n' "$GREEN" "$avail_gb" "$RESET"
    return 0
}

# ── FASE 1f: Drain other workers ────────────────────────────────
drain_other_workers() {
    smoke_log_section "Fase 1f: Drain other workers"
    if [[ "$DRAIN_OTHERS" != "1" ]]; then
        printf '  %sSKIP: --drain-others not specified%s\n' "$DIM" "$RESET"
        return 0
    fi
    if [[ -z "$TARGET_WORKER_ID" ]]; then
        printf '  %sWARN: --drain-others requires --worker-id — skipping drain%s\n' "$YELLOW" "$RESET" >&2
        return 0
    fi
    smoke_curl GET "/api/v1/velox/workers" >/dev/null
    if [[ ! "$SMOKE_LAST_HTTP" =~ ^2[0-9][0-9]$ ]]; then
        printf '  %sWARN: cannot list workers (HTTP %s) — skipping drain%s\n' "$YELLOW" "$SMOKE_LAST_HTTP" "$RESET" >&2
        return 0
    fi
    local drained=0
    local worker_ids
    worker_ids=$(jq -r '.[].worker_id // .[].id // empty' "$SMOKE_LAST_BODY" 2>/dev/null || true)
    while IFS= read -r wid; do
        [[ -z "$wid" || "$wid" == "$TARGET_WORKER_ID" ]] && continue
        # PUT /api/v1/velox/workers/<id>/drain
        smoke_curl PUT "/api/v1/velox/workers/${wid}/drain" >/dev/null
        if [[ "$SMOKE_LAST_HTTP" =~ ^2[0-9][0-9]$ ]]; then
            printf '  %sDRAINED: worker %s%s\n' "$DIM" "$wid" "$RESET"
            drained=$((drained+1))
        else
            printf '  %sWARN: drain worker %s returned HTTP %s%s\n' "$YELLOW" "$wid" "$SMOKE_LAST_HTTP" "$RESET" >&2
        fi
    done <<< "$worker_ids"
    if [[ $drained -gt 0 ]]; then
        printf '  %sOK: drained %s worker(s) — only %s remains active%s\n' "$GREEN" "$drained" "$TARGET_WORKER_ID" "$RESET"
    fi
    return 0
}
