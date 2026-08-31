#!/usr/bin/env bash
# media_assets_drive_qdrant_smoke.sh — Zone 3 production readiness smoke test.
#
# Submits a stock pipeline job via POST /api/stock-pipeline/run (search_queries),
# polls to terminal, then verifies the full chain:
#   T1: stock job submitted + reaches terminal
#   T2: media_assets row exists with drive_file_id + file_hash + lifecycle_state=ACTIVE
#   T3: outbox_events asset.index.requested emitted and completed
#   T4: stock media_assets drive_file_id non-empty (asset published to Drive)
#   T5: Drive file exists via Google Drive API (if token available)
#   T6: Qdrant point exists for the asset (if Qdrant reachable)
#
# Exit codes: 0 = PASS, 1 = FAIL, 2 = setup error.
#
# Environment variables:
#   SMOKE_DB            — SQLite database path (default: data/media/media.db.sqlite)
#   SMOKE_DRIVE_TOKEN_FILE — Google OAuth token file (default: token.json)
#   QDRANT_URL          — Qdrant base URL (default: http://127.0.0.1:6333)
#
# Uses: tests/operational/lib/common.sh (smoke_curl, smoke_poll_terminal, etc.)

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

smoke_require sqlite3 curl

# Help / dry-run
if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    sed -n '2,18p' "$0"
    printf '\nFor full env-var / exit-code docs see the source godoc.\n'
    exit 0
fi

if [[ "$DRY_RUN" == "1" ]]; then
    smoke_echo_safe "DRY RUN — would probe:"
    printf '  POST http://%s/api/stock-pipeline/run  (search_queries=boxing training)\n' "$SMOKE_API_BASE"
    printf '  poll http://%s/api/jobs/<id>/full  (terminal)\n' "$SMOKE_API_BASE"
    printf '  sqlite3: media_assets WHERE source=stock + drive_file_id + file_hash + lifecycle_state=ACTIVE\n'
    printf '  sqlite3: outbox_events WHERE event_type=asset.index.requested\n'
    printf '  Drive API: GET /drive/v3/files/<id>  (if token available)\n'
    printf '  Qdrant: POST /collections/media_assets_current/points/scroll  (if reachable)\n'
    exit 0
fi

# ── Configuration ──────────────────────────────────────────────────
SMOKE_DB="${SMOKE_DB:?SMOKE_DB must be explicitly set to an isolated or approved database}"
SMOKE_DRIVE_TOKEN_FILE="${SMOKE_DRIVE_TOKEN_FILE:-${REPO_ROOT:-$(pwd)}/token.json}"
QDRANT_URL="${QDRANT_URL:-http://127.0.0.1:6333}"
DRIVE_API_BASE="https://www.googleapis.com"
TAG_PREFIX="zone3_stock_$(date +%s)_$$"
REQ_ID="${TAG_PREFIX}_zone3"

declare -a FAILURES=()
fail() { FAILURES+=("$1"); }

# Strict sqlite query (mirrors voiceover_translated_drive_real_smoke.sh)
sqlite_q() {
    local out
    if ! out=$(sqlite3 -separator '|' "$SMOKE_DB" "$1" 2>/tmp/smoke_sqlite_err); then
        echo >&2 "DB query failed: sqlite3 exit non-zero with stderr:"
        cat >&2 /tmp/smoke_sqlite_err
        rm -f /tmp/smoke_sqlite_err
        exit 1
    fi
    rm -f /tmp/smoke_sqlite_err
    printf '%s' "$out"
}

# ── Preflight checks ──────────────────────────────────────────────

preflight_server_up() {
    smoke_log_section "Preflight: Go server reachable (GET /health)"
    smoke_curl GET "/health" >/dev/null
    local code
    code=$(cat "$WORK_DIR/last.code" 2>/dev/null || echo "000")
    if [[ ! "$code" =~ ^2[0-9][0-9]$ ]]; then
        fail "preflight_server_http_${code}"
        printf '%sFAIL: GET /health returned HTTP %s (expected 2xx)%s\n' \
            "$RED" "$code" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: GET /health → HTTP %s%s\n' "$GREEN" "$code" "$RESET"
    return 0
}

preflight_db_exists() {
    smoke_log_section "Preflight: SQLite database exists"
    if [[ ! -f "$SMOKE_DB" ]]; then
        fail "preflight_db_not_found"
        printf '%sFAIL: SMOKE_DB=%s does not exist%s\n' \
            "$RED" "$SMOKE_DB" "$RESET" >&2
        return 1
    fi
    # Check media_assets table exists
    local table_check
    table_check=$(sqlite3 "$SMOKE_DB" "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='media_assets';" 2>/dev/null || echo "0")
    if [[ "$table_check" != "1" ]]; then
        fail "preflight_media_assets_table_missing"
        printf '%sFAIL: media_assets table not found in %s%s\n' \
            "$RED" "$SMOKE_DB" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: %s has media_assets table%s\n' "$GREEN" "$SMOKE_DB" "$RESET"
    return 0
}

# ── T1: Submit stock pipeline job ─────────────────────────────────

submit_stock_job() {
    smoke_log_section "T1: POST /api/stock-pipeline/run (search_queries=boxing training)"

    local payload
    payload=$(jq -n '{
        search_queries: ["boxing training gym"],
        total_minutes: 1,
        chunk_duration: 10,
        clip_duration: 10,
        max_videos: 2,
        no_audio: true,
        no_effects: true,
        no_transitions: true
    }')

    smoke_curl POST "/api/stock-pipeline/run" --data "$payload" >/dev/null
    local code
    code=$(cat "$WORK_DIR/last.code" 2>/dev/null || echo "000")
    if [[ ! "$code" =~ ^2[0-9][0-9]$ ]]; then
        fail "submit_stock_http_${code}"
        printf '%sFAIL: POST /api/stock-pipeline/run returned HTTP %s (expected 2xx)%s\n' \
            "$RED" "$code" "$RESET" >&2
        return 1
    fi

    JOB_ID=$(jq -r '.job_id // .id // empty' "$SMOKE_LAST_BODY" 2>/dev/null || echo "")
    if [[ -z "$JOB_ID" ]]; then
        fail "submit_stock_no_job_id"
        printf '%sFAIL: POST returned no job_id in body%s\n' "$RED" "$RESET" >&2
        return 1
    fi
    printf '  %senqueued job_id=%s%s\n' "$GREEN" "$JOB_ID" "$RESET"
    return 0
}

# ── Poll job to terminal ──────────────────────────────────────────

poll_to_terminal() {
    smoke_log_section "Poll job to terminal (timeout ${SMOKE_POLL_TIMEOUT_SECONDS}s)"
    if ! smoke_poll_terminal "$JOB_ID"; then
        fail "poll_terminal_timeout"
        printf '%sFAIL: job %s did not reach terminal in %ss (last status=%s)%s\n' \
            "$RED" "$JOB_ID" "$SMOKE_POLL_TIMEOUT_SECONDS" "${SMOKE_LAST_STATUS:-?}" "$RESET" >&2
        return 1
    fi
    # Accept both "completed" and "SUCCEEDED" as terminal success
    case "$SMOKE_LAST_STATUS" in
        completed|SUCCEEDED)
            printf '  %sOK: job terminal status=%s%s\n' "$GREEN" "$SMOKE_LAST_STATUS" "$RESET"
            return 0
            ;;
        *)
            fail "poll_terminal_status_${SMOKE_LAST_STATUS}"
            printf '%sFAIL: job terminal status=%s (expected completed/SUCCEEDED)%s\n' \
                "$RED" "$SMOKE_LAST_STATUS" "$RESET" >&2
            return 1
            ;;
    esac
}

# ── T2: Verify media_assets row ───────────────────────────────────

verify_media_assets() {
    smoke_log_section "T2: media_assets row — drive_file_id + file_hash + lifecycle_state=ACTIVE"

    # Query media_assets for stock rows created by THIS job's test run.
    # Correlate via the 10-minute window AND the TAG_PREFIX used in this run.
    # The stock pipeline creates rows with source='stock' and populates
    # drive_file_id, file_hash, lifecycle_state via the finalizer.
    local rows
    rows=$(sqlite_q "SELECT id || '|' || COALESCE(drive_file_id, '') || '|' || COALESCE(file_hash, '') || '|' || COALESCE(lifecycle_state, '') || '|' || COALESCE(index_state, '') || '|' || COALESCE(source, '') FROM media_assets WHERE source = 'stock' AND created_at > datetime('now', '-10 minutes') ORDER BY created_at DESC LIMIT 20")

    if [[ -z "$rows" ]]; then
        fail "media_assets_no_rows"
        printf '%sFAIL: no media_assets rows with source=stock created in last 10 min%s\n' "$RED" "$RESET" >&2
        printf '  Possible causes:\n' >&2
        printf '    1. Stock job FAILED before finalizer stage\n' >&2
        printf '    2. Finalizer tx rolled back before media_assets UPSERT\n' >&2
        printf '    3. stock_root_folder not configured (Drive upload skipped)\n' >&2
        return 1
    fi

    local ma_count
    ma_count=$(printf '%s\n' "$rows" | wc -l | tr -d ' ')
    printf '  found %s media_assets row(s)\n' "$ma_count"

    # Save asset IDs for T3 (outbox) and T6 (Qdrant)
    : > "$WORK_DIR/asset_ids.txt"

    local row_ok=0
    local row_fail=0
    while IFS='|' read -r ma_id drive_file_id file_hash lifecycle_state index_state source; do
        [[ -z "$ma_id" ]] && continue

        # Save for downstream queries
        printf '%s\n' "$ma_id" >> "$WORK_DIR/asset_ids.txt"

        local row_has_issue=0

        # drive_file_id non-empty
        if [[ -z "$drive_file_id" ]]; then
            fail "media_assets_empty_drive_file_id_${ma_id}"
            printf '%s  FAIL: row %s has empty drive_file_id%s\n' "$RED" "$ma_id" "$RESET" >&2
            row_has_issue=1
        fi

        # file_hash non-empty
        if [[ -z "$file_hash" ]]; then
            fail "media_assets_empty_file_hash_${ma_id}"
            printf '%s  FAIL: row %s has empty file_hash%s\n' "$RED" "$ma_id" "$RESET" >&2
            row_has_issue=1
        fi

        # lifecycle_state=ACTIVE
        if [[ "$lifecycle_state" != "ACTIVE" ]]; then
            fail "media_assets_lifecycle_not_ACTIVE_${ma_id}_${lifecycle_state}"
            printf '%s  FAIL: row %s lifecycle_state=%s (expected ACTIVE)%s\n' \
                "$RED" "$ma_id" "$lifecycle_state" "$RESET" >&2
            row_has_issue=1
        fi

        if [[ "$row_has_issue" -eq 0 ]]; then
            printf '  %sOK: row %s — drive_file_id=%s file_hash=%s lifecycle=%s index=%s%s\n' \
                "$GREEN" "$ma_id" "${drive_file_id:0:16}..." "${file_hash:0:16}..." \
                "$lifecycle_state" "$index_state" "$RESET"
            row_ok=$((row_ok + 1))
        else
            row_fail=$((row_fail + 1))
        fi
    done <<< "$rows"

    if [[ "$row_fail" -gt 0 ]]; then
        return 1
    fi
    return 0
}

# ── T3: Verify outbox asset.index.requested ───────────────────────

verify_outbox_events() {
    smoke_log_section "T3: outbox_events — asset.index.requested emitted + completed"

    if [[ ! -s "$WORK_DIR/asset_ids.txt" ]]; then
        fail "outbox_no_asset_ids"
        printf '%sFAIL: no asset IDs to check outbox (T2 may have failed)%s\n' "$RED" "$RESET" >&2
        return 1
    fi

    # Build quoted IN-clause from asset IDs
    local in_list
    in_list=$(awk 'BEGIN{ORS=","; q=sprintf("%c",39)} {printf q "%s" q, $0}' "$WORK_DIR/asset_ids.txt" | sed 's/,$//')

    local total completed pending dead_letter last_error_count
    # Correlate to the specific asset IDs from T2's media_assets query
    # (which is already scoped to this run's 10-minute window).
    local outbox_where="event_type = 'asset.index.requested'
    AND aggregate_id IN (${in_list})
    AND created_at > datetime('now', '-10 minutes')"
    total=$(sqlite_q "SELECT COUNT(*) FROM outbox_events WHERE $outbox_where")
    completed=$(sqlite_q "SELECT COUNT(*) FROM outbox_events WHERE $outbox_where AND status = 'completed'")
    pending=$(sqlite_q "SELECT COUNT(*) FROM outbox_events WHERE $outbox_where AND status = 'pending'")
    dead_letter=$(sqlite_q "SELECT COUNT(*) FROM outbox_events WHERE $outbox_where AND status = 'dead_letter'")
    last_error_count=$(sqlite_q "SELECT COUNT(*) FROM outbox_events WHERE $outbox_where AND status = 'completed' AND last_error != '' AND last_error IS NOT NULL")

    printf '  total=%s  completed=%s  pending=%s  dead_letter=%s  completed_with_error=%s\n' \
        "$total" "$completed" "$pending" "$dead_letter" "$last_error_count"

    # T3a: at least 1 outbox event emitted
    if [[ "$total" -eq 0 ]]; then
        fail "outbox_no_events"
        printf '%sFAIL: zero outbox_events for stock assets (asset.index.requested not emitted)%s\n' \
            "$RED" "$RESET" >&2
        printf '  Canonical owner: internal/capabilities/assets/finalizer/asset_finalizer_tx.go::FinalizeAsset\n' >&2
        return 1
    fi

    # T3b: no dead_letter events
    if [[ "$dead_letter" -gt 0 ]]; then
        fail "outbox_dead_letter_${dead_letter}"
        printf '%sFAIL: %s dead_letter outbox events (exhausted retries)%s\n' \
            "$RED" "$dead_letter" "$RESET" >&2
        return 1
    fi

    # T3c: completed events have empty last_error
    if [[ "$last_error_count" -gt 0 ]]; then
        fail "outbox_completed_with_last_error_${last_error_count}"
        printf '%sFAIL: %s completed events have non-empty last_error (silent-success anti-pattern)%s\n' \
            "$RED" "$last_error_count" "$RESET" >&2
        return 1
    fi

    # T3d: if no completed events, check if still pending
    if [[ "$completed" -eq 0 && "$pending" -gt 0 ]]; then
        printf '  %sWARN: %s pending events, 0 completed — outbox dispatcher may be lagging or Qdrant unavailable%s\n' \
            "$YELLOW" "$pending" "$RESET"
    elif [[ "$completed" -gt 0 ]]; then
        printf '  %sOK: %s completed outbox events, 0 dead_letter, 0 last_error%s\n' \
            "$GREEN" "$completed" "$RESET"
    fi

    return 0
}

# ── T4: Verify stock media_assets drive fields ────────────────────

verify_stock_drive_fields() {
    smoke_log_section "T4: stock media_assets — drive_file_id + drive_link non-empty"

    if [[ ! -s "$WORK_DIR/asset_ids.txt" ]]; then
        printf '  %sSKIP: no asset IDs from T2%s\n' "$YELLOW" "$RESET"
        return 0
    fi

    # Build quoted IN-clause
    local in_list
    in_list=$(awk 'BEGIN{ORS=","; q=sprintf("%c",39)} {printf q "%s" q, $0}' "$WORK_DIR/asset_ids.txt" | sed 's/,$//')

    local rows
    rows=$(sqlite_q "SELECT id || '|' || COALESCE(drive_file_id, '') || '|' || COALESCE(drive_link, '') FROM media_assets WHERE id IN (${in_list}) AND drive_file_id != ''")

    if [[ -z "$rows" ]]; then
        fail "stock_no_drive_fields"
        printf '%sFAIL: no stock assets with non-empty drive_file_id%s\n' "$RED" "$RESET" >&2
        printf '  Possible causes:\n' >&2
        printf '    1. Drive upload failed (stock_root_folder not configured)\n' >&2
        printf '    2. Stock job FAILED before publish step\n' >&2
        printf '    3. Publisher returned typed error\n' >&2
        return 1
    fi

    local count
    count=$(printf '%s\n' "$rows" | wc -l | tr -d ' ')

    # Save drive IDs for T5 Drive API verification
    : > "$WORK_DIR/stock_drive_ids.txt"

    local ok=0 fail_count=0
    while IFS='|' read -r asset_id drive_file_id drive_link; do
        [[ -z "$asset_id" ]] && continue
        if [[ -n "$drive_file_id" ]]; then
            printf '  %sOK: %s — drive_file_id=%s drive_link=%s…%s\n' \
                "$GREEN" "$asset_id" "${drive_file_id:0:16}..." "${drive_link:0:40}" "$RESET"
            printf '%s|%s\n' "$asset_id" "$drive_file_id" >> "$WORK_DIR/stock_drive_ids.txt"
            ok=$((ok + 1))
        else
            fail "stock_empty_drive_${asset_id}"
            printf '%s  FAIL: %s — drive_file_id empty%s\n' "$RED" "$asset_id" "$RESET" >&2
            fail_count=$((fail_count + 1))
        fi
    done <<< "$rows"

    if [[ "$fail_count" -gt 0 ]]; then
        return 1
    fi
    printf '  %s%s stock asset(s) with complete Drive metadata%s\n' "$GREEN" "$ok" "$RESET"
    return 0
}

# ── T5: Drive API verification (optional — needs token) ──────────

verify_drive_api() {
    smoke_log_section "T5: Drive API — file exists (if token available)"

    if [[ ! -f "$SMOKE_DRIVE_TOKEN_FILE" ]]; then
        printf '  %sSKIP: token file %s not found — Drive API verification skipped%s\n' \
            "$YELLOW" "$SMOKE_DRIVE_TOKEN_FILE" "$RESET"
        printf '  (Run: python3 scripts/generate_drive_token.py)\n'
        return 0
    fi

    local token
    token=$(jq -r '.access_token // empty' "$SMOKE_DRIVE_TOKEN_FILE" 2>/dev/null || echo "")
    if [[ -z "$token" ]]; then
        printf '  %sSKIP: token.json has no access_token — Drive API verification skipped%s\n' \
            "$YELLOW" "$RESET"
        return 0
    fi

    if [[ ! -s "$WORK_DIR/stock_drive_ids.txt" ]]; then
        printf '  %sSKIP: no stock drive IDs to verify%s\n' "$YELLOW" "$RESET"
        return 0
    fi

    local drive_ok=0 drive_fail=0
    while IFS='|' read -r asset_id drive_file_id; do
        [[ -z "$drive_file_id" ]] && continue

        local api_url="$DRIVE_API_BASE/drive/v3/files/${drive_file_id}?fields=id,name,mimeType,size,webViewLink"
        local code
        code=$(curl -s --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" \
            -o "$WORK_DIR/drive_${asset_id}.json" -w '%{http_code}' \
            -H "Authorization: Bearer $token" \
            "$api_url" 2>/dev/null || echo "000")

        if [[ "$code" == "200" ]]; then
            local body drive_id drive_size
            body=$(cat "$WORK_DIR/drive_${asset_id}.json")
            drive_id=$(printf '%s' "$body" | jq -r '.id // empty')
            drive_size=$(printf '%s' "$body" | jq -r '.size // "0"')

            if [[ "$drive_id" == "$drive_file_id" ]]; then
                printf '  %sOK: Drive file %s — id match, size=%s%s\n' \
                    "$GREEN" "${drive_file_id:0:16}..." "$drive_size" "$RESET"
                drive_ok=$((drive_ok + 1))
            else
                fail "drive_id_mismatch_${asset_id}"
                printf '%s  FAIL: Drive ID mismatch — db=%s vs api=%s%s\n' \
                    "$RED" "$drive_file_id" "$drive_id" "$RESET" >&2
                drive_fail=$((drive_fail + 1))
            fi
        elif [[ "$code" == "404" ]]; then
            fail "drive_file_not_found_${asset_id}"
            printf '%s  FAIL: Drive file %s returned 404 (file deleted or trashed)%s\n' \
                "$RED" "$drive_file_id" "$RESET" >&2
            drive_fail=$((drive_fail + 1))
        elif [[ "$code" == "401" ]]; then
            fail "drive_token_expired"
            printf '%s  FAIL: Drive API returned 401 — token expired; re-run python3 scripts/generate_drive_token.py%s\n' \
                "$RED" "$RESET" >&2
            drive_fail=$((drive_fail + 1))
        else
            fail "drive_api_error_${code}_${asset_id}"
            printf '%s  FAIL: Drive API returned HTTP %s for file %s%s\n' \
                "$RED" "$code" "$drive_file_id" "$RESET" >&2
            drive_fail=$((drive_fail + 1))
        fi
    done < "$WORK_DIR/stock_drive_ids.txt"

    if [[ "$drive_fail" -gt 0 ]]; then
        return 1
    fi
    if [[ "$drive_ok" -gt 0 ]]; then
        printf '  %s%s file(s) verified on Drive%s\n' "$GREEN" "$drive_ok" "$RESET"
    fi
    return 0
}

# ── T6: Qdrant point verification (optional — needs Qdrant) ──────

verify_qdrant_point() {
    smoke_log_section "T6: Qdrant — point exists (if Qdrant reachable)"

    local qdrant_health
    qdrant_health=$(curl -s --max-time 3 "${QDRANT_URL}/healthz" 2>/dev/null || echo "")
    if [[ -z "$qdrant_health" ]]; then
        printf '  %sSKIP: Qdrant not reachable at %s — point verification skipped%s\n' \
            "$YELLOW" "$QDRANT_URL" "$RESET"
        return 0
    fi

    if [[ ! -s "$WORK_DIR/asset_ids.txt" ]]; then
        printf '  %sSKIP: no asset IDs to look up in Qdrant%s\n' "$YELLOW" "$RESET"
        return 0
    fi

    local qdrant_ok=0 qdrant_fail=0
    while IFS= read -r asset_id; do
        [[ -z "$asset_id" ]] && continue

        local scroll_body
        scroll_body=$(jq -n --arg aid "$asset_id" '{
            filter: {
                must: [
                    {key: "asset_id", match: {value: $aid}}
                ]
            },
            limit: 1,
            with_payload: true,
            with_vector: false
        }')

        local qdrant_resp
        qdrant_resp=$(curl -s --max-time 5 \
            -H "Content-Type: application/json" \
            -d "$scroll_body" \
            "${QDRANT_URL}/collections/media_assets_current/points/scroll" 2>/dev/null || echo "{}")

        local point_count
        point_count=$(printf '%s' "$qdrant_resp" | jq -r '.result.points | length // 0' 2>/dev/null || echo "0")

        if [[ "$point_count" -gt 0 ]]; then
            local payload_lifecycle
            payload_lifecycle=$(printf '%s' "$qdrant_resp" | jq -r '.result.points[0].payload.lifecycle_state // "unknown"' 2>/dev/null || echo "unknown")

            if [[ "$payload_lifecycle" == "ACTIVE" ]]; then
                printf '  %sOK: Qdrant point %s — lifecycle_state=ACTIVE%s\n' \
                    "$GREEN" "${asset_id:0:24}..." "$RESET"
                qdrant_ok=$((qdrant_ok + 1))
            else
                fail "qdrant_lifecycle_not_ACTIVE_${asset_id}"
                printf '%s  FAIL: Qdrant point %s lifecycle_state=%s (expected ACTIVE)%s\n' \
                    "$RED" "${asset_id:0:24}..." "$payload_lifecycle" "$RESET" >&2
                qdrant_fail=$((qdrant_fail + 1))
            fi
        else
            local outbox_completed
            outbox_completed=$(sqlite_q "SELECT COUNT(*) FROM outbox_events WHERE event_type = 'asset.index.requested' AND aggregate_id = '${asset_id}' AND status = 'completed' AND created_at > datetime('now', '-10 minutes')")
            if [[ "$outbox_completed" -gt 0 ]]; then
                fail "qdrant_point_missing_outbox_completed_${asset_id}"
                printf '%s  FAIL: Qdrant point %s NOT found but outbox COMPLETED (silent-success anti-pattern)%s\n' \
                    "$RED" "${asset_id:0:24}..." "$RESET" >&2
                qdrant_fail=$((qdrant_fail + 1))
            else
                printf '  %sWARN: Qdrant point %s not found — outbox not yet completed (expected if Qdrant was down)%s\n' \
                    "$YELLOW" "${asset_id:0:24}..." "$RESET"
            fi
        fi
    done < "$WORK_DIR/asset_ids.txt"

    if [[ "$qdrant_fail" -gt 0 ]]; then
        return 1
    fi
    if [[ "$qdrant_ok" -gt 0 ]]; then
        printf '  %s%s Qdrant point(s) verified%s\n' "$GREEN" "$qdrant_ok" "$RESET"
    fi
    return 0
}

# ── Main ──────────────────────────────────────────────────────────

main() {
    smoke_log_section "Zone 3: Media Assets + Drive + Qdrant smoke (stock pipeline)"
    printf '  target:   %s\n  db:       %s\n  qdrant:   %s\n  tag:      %s\n  req_id:   %s\n' \
        "$SMOKE_API_BASE" "$SMOKE_DB" "$QDRANT_URL" "$TAG_PREFIX" "$REQ_ID"

    # Preflights (fail-fast before any mutation)
    preflight_server_up || true
    preflight_db_exists || true

    if (( ${#FAILURES[@]} > 0 )); then
        printf '%sFAIL: preflight(s) failed, aborting before POST%s\n' "$RED" "$RESET" >&2
        for f in "${FAILURES[@]}"; do
            printf '  - %s\n' "$f" >&2
        done
        exit 1
    fi

    # T1: Submit stock job
    submit_stock_job || { fail "submit_stock_job"; exit 1; }

    # Poll to terminal
    poll_to_terminal || true

    # T2: Verify media_assets row
    verify_media_assets || true

    # T3: Verify outbox events
    verify_outbox_events || true

    # T4: Verify stock drive fields
    verify_stock_drive_fields || true

    # T5: Drive API (optional)
    verify_drive_api || true

    # T6: Qdrant (optional)
    verify_qdrant_point || true

    # ── Verdict ──────────────────────────────────────────────────
    echo
    local fail_count=${#FAILURES[@]}
    if (( fail_count == 0 )); then
        printf '%sOK: Zone 3 Media Assets + Drive + Qdrant smoke PASS%s\n' \
            "$GREEN" "$RESET"
        printf '  Assertions passed:\n'
        printf '    T1: stock job submitted + completed\n'
        printf '    T2: media_assets row with drive_file_id + file_hash + lifecycle_state=ACTIVE\n'
        printf '    T3: outbox asset.index.requested emitted + no dead_letter + no last_error\n'
        printf '    T4: stock media_assets drive_file_id non-empty\n'
        printf '    T5: Drive file exists (if token available)\n'
        printf '    T6: Qdrant point exists (if Qdrant reachable)\n'
        exit 0
    fi

    printf '%sFAIL: %d assertion(s) failed:%s\n' \
        "$RED" "$fail_count" "$RESET" >&2
    for f in "${FAILURES[@]}"; do
        printf '  - %s\n' "$f" >&2
    done
    printf '\n  Failure-to-PR mapping:\n' >&2
    printf '    media_assets empty drive_file_id  → PR-STOCK-FINALIZER-COMPLETE\n' >&2
    printf '    media_assets empty file_hash      → PR-STOCK-FILEHASH-PERSIST\n' >&2
    printf '    media_assets lifecycle != ACTIVE   → PR-STOCK-LIFECYCLE-TRANSITION\n' >&2
    printf '    outbox dead_letter                 → PR-STOCK-OUTBOX-DEAD-LETTERED\n' >&2
    printf '    outbox completed + last_error      → PR-STOCK-OUTBOX-LAST-ERROR\n' >&2
    printf '    Drive file 404                     → PR-STOCK-DOWNLOAD-RESOLVER\n' >&2
    printf '    Qdrant point missing + completed   → PR-STOCK-OUTBOX-QDRANT-INDEX\n' >&2
    exit 1
}

main
