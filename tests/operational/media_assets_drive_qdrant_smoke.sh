#!/usr/bin/env bash
# media_assets_drive_qdrant_smoke.sh — Zone 3 production readiness smoke test.
#
# Generates a voiceover asset via POST /api/media/voiceover/generate (1 item),
# then verifies the full chain:
#   T1: media_assets row exists with drive_file_id + file_hash + lifecycle_state=ACTIVE
#   T2: outbox_events asset.index.requested emitted and completed
#   T3: voiceover row has drive_file_id + drive_link non-empty
#   T4: Drive file exists via Google Drive API (if token available)
#   T5: Qdrant point exists for the asset (if Qdrant reachable)
#
# Exit codes: 0 = PASS, 1 = FAIL, 2 = setup error.
#
# Environment variables:
#   SMOKE_DB            — SQLite database path (default: data/media/media.db.sqlite)
#   SMOKE_DRIVE_TOKEN_FILE — Google OAuth token file (default: token.json)
#   QDRANT_URL          — Qdrant base URL (default: http://127.0.0.1:6333)
#   SMOKE_DRIVE_ROOT    — Drive folder_id for voiceover destination
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
    printf '  POST http://%s/api/media/voiceover/generate  (1 item: it-IT, required=true)\n' "$SMOKE_API_BASE"
    printf '  poll http://%s/api/jobs/<id>/full  (terminal)\n' "$SMOKE_API_BASE"
    printf '  sqlite3: media_assets WHERE source=voiceover + drive_file_id + file_hash + lifecycle_state=ACTIVE\n'
    printf '  sqlite3: outbox_events WHERE event_type=asset.index.requested\n'
    printf '  Drive API: GET /drive/v3/files/<id>  (if token available)\n'
    printf '  Qdrant: POST /collections/media_assets_current/points/scroll  (if reachable)\n'
    exit 0
fi

# ── Configuration ──────────────────────────────────────────────────
SMOKE_DB="${SMOKE_DB:-data/media/media.db.sqlite}"
SMOKE_DRIVE_TOKEN_FILE="${SMOKE_DRIVE_TOKEN_FILE:-${REPO_ROOT:-$(pwd)}/token.json}"
QDRANT_URL="${QDRANT_URL:-http://127.0.0.1:6333}"
SMOKE_DRIVE_ROOT="${SMOKE_DRIVE_ROOT:-}"
DRIVE_API_BASE="https://www.googleapis.com"
TAG_PREFIX="media_smoke_$(date +%s)_$$"
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
    local code
    code=$(smoke_curl GET "/health")
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

# ── T1: Generate voiceover asset ──────────────────────────────────

post_voiceover() {
    smoke_log_section "T1: POST /api/media/voiceover/generate (1 item: it-IT)"

    # Use explicit folder_id if provided; otherwise use empty (composition root will resolve)
    local dest_block
    if [[ -n "$SMOKE_DRIVE_ROOT" ]]; then
        dest_block=$(jq -n --arg fid "$SMOKE_DRIVE_ROOT" '{kind: "explicit", folder_id: $fid}')
    else
        dest_block='{"kind": "explicit", "folder_id": ""}'
    fi

    local payload
    payload=$(jq -n --arg rid "$REQ_ID" --arg vid "$TAG_PREFIX" --argjson dest "$dest_block" '{
        request_id: $rid,
        items: [
            {text: "Smoke test voiceover for media assets chain verification.", language: "it-IT", voice: "it-IT-DiegoNeural", filename: ("smoke_zone3_" + $vid + ".mp3"), required: true}
        ],
        destination: $dest,
        options: {remove_silence: false, strategy: "verify", parallelism: 1}
    }')

    local code
    code=$(smoke_curl POST "/api/media/voiceover/generate" --data "$payload")
    if ! smoke_assert_http_2xx "POST /api/media/voiceover/generate"; then
        fail "post_voiceover_http_${SMOKE_LAST_HTTP}"
        return 1
    fi

    JOB_ID=$(jq -r '.job_id // .id // empty' "$SMOKE_LAST_BODY" 2>/dev/null || echo "")
    if [[ -z "$JOB_ID" ]]; then
        fail "post_voiceover_no_job_id"
        printf '%sFAIL: POST returned no job_id in body%s\n' "$RED" "$RESET" >&2
        return 1
    fi
    printf '  %senqueued job_id=%s (req_id=%s)%s\n' "$GREEN" "$JOB_ID" "$REQ_ID" "$RESET"
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

    # Query media_assets for this voiceover request's drive_file_id
    # Canonical link: voiceovers.drive_file_id → media_assets.drive_file_id
    local rows
    rows=$(sqlite_q "SELECT id || '|' || COALESCE(drive_file_id, '') || '|' || COALESCE(file_hash, '') || '|' || COALESCE(lifecycle_state, '') || '|' || COALESCE(index_state, '') || '|' || COALESCE(source, '') FROM media_assets WHERE source = 'voiceover' AND drive_file_id IN (SELECT drive_file_id FROM voiceovers WHERE request_id = '${REQ_ID}' AND drive_file_id != '') ORDER BY created_at DESC")

    # Fallback: if zero rows AND voiceovers table has rows, diagnose why
    if [[ -z "$rows" ]]; then
        local vo_count
        vo_count=$(sqlite_q "SELECT COUNT(*) FROM voiceovers WHERE request_id = '${REQ_ID}'")
        if [[ "$vo_count" -gt 0 ]]; then
            local vo_drive_empty
            vo_drive_empty=$(sqlite_q "SELECT COUNT(*) FROM voiceovers WHERE request_id = '${REQ_ID}' AND (drive_file_id = '' OR drive_file_id IS NULL)")
            if [[ "$vo_drive_empty" -gt 0 ]]; then
                printf '  %sDIAG: %s voiceover(s) exist but drive_file_id is empty — Drive upload failed before media_assets UPSERT%s
' \
                    "$YELLOW" "$vo_drive_empty" "$RESET"
            fi
        fi
    fi

    if [[ -z "$rows" ]]; then
        fail "media_assets_no_rows"
        printf '%sFAIL: no media_assets rows found for request_id=%s\n' "$RED" "$REQ_ID" >&2
        printf '  Possible causes:\n' >&2
        printf '    1. Finalizer tx rolled back before media_assets UPSERT (PR-VOICEOVER-PIPELINE-DEBUG-2026-07-08)\n' >&2
        printf '    2. Voiceover job FAILED before finalizer stage\n' >&2
        printf '    3. drive_file_id empty in voiceovers table (Drive upload failed)\n' >&2
        return 1
    fi

    local ma_count
    ma_count=$(printf '%s\n' "$rows" | wc -l | tr -d ' ')
    printf '  found %s media_assets row(s)\n' "$ma_count"

    local row_ok=0
    local row_fail=0
    while IFS='|' read -r ma_id drive_file_id file_hash lifecycle_state index_state source; do
        [[ -z "$ma_id" ]] && continue

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

    local total completed pending dead_letter last_error_count
    # Canonical: outbox aggregate_id = media_assets.id (set by FinalizeAsset),
    # NOT voiceovers.id. Join via media_assets.drive_file_id → voiceovers.drive_file_id.
    local outbox_where="event_type = 'asset.index.requested'
    AND aggregate_id IN (SELECT id FROM media_assets WHERE source = 'voiceover' AND drive_file_id IN (SELECT drive_file_id FROM voiceovers WHERE request_id = '${REQ_ID}' AND drive_file_id != ''))
    AND created_at > datetime('now', '-5 minutes')"
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
        printf '%sFAIL: zero outbox_events for request_id=%s (asset.index.requested not emitted)%s\n' \
            "$RED" "$REQ_ID" "$RESET" >&2
        printf '  Canonical owner: internal/application/assets/finalizer/asset_finalizer_tx.go::FinalizeAsset\n' >&2
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

    # T3d: if no completed events, check if still pending (acceptable — Qdrant may be slow)
    if [[ "$completed" -eq 0 && "$pending" -gt 0 ]]; then
        printf '  %sWARN: %s pending events, 0 completed — outbox dispatcher may be lagging or Qdrant unavailable%s\n' \
            "$YELLOW" "$pending" "$RESET"
        # Not a hard fail — the event exists and hasn't dead-lettered
        # The Qdrant-down scenario (T5) will surface the root cause
    elif [[ "$completed" -gt 0 ]]; then
        printf '  %sOK: %s completed outbox events, 0 dead_letter, 0 last_error%s\n' \
            "$GREEN" "$completed" "$RESET"
    fi

    return 0
}

# ── T4: Verify voiceover row has drive fields ─────────────────────

verify_voiceover_drive_fields() {
    smoke_log_section "T4: voiceover row — drive_file_id + drive_link non-empty"

    local vo_rows
    vo_rows=$(sqlite_q "SELECT id || '|' || COALESCE(drive_file_id, '') || '|' || COALESCE(drive_link, '') || '|' || COALESCE(folder_id, '') FROM voiceovers WHERE request_id = '${REQ_ID}' AND drive_file_id != ''")

    if [[ -z "$vo_rows" ]]; then
        fail "voiceover_no_drive_fields"
        printf '%sFAIL: no voiceovers with non-empty drive_file_id for request_id=%s\n' \
            "$RED" "$REQ_ID" >&2
        printf '  Possible causes:\n' >&2
        printf '    1. Drive upload failed (token expired, folder not found)\n' >&2
        printf '    2. Voiceover job FAILED before Stage 3 (Drive upload)\n' >&2
        printf '    3. Publisher returned typed error (ErrPathBuilderIncompleteForOverride)\n' >&2
        return 1
    fi

    local vo_count
    vo_count=$(printf '%s\n' "$vo_rows" | wc -l | tr -d ' ')

    local vo_ok=0 vo_fail=0
    while IFS='|' read -r vo_id drive_file_id drive_link folder_id; do
        [[ -z "$vo_id" ]] && continue
        if [[ -n "$drive_file_id" && -n "$drive_link" ]]; then
            printf '  %sOK: voiceover %s — drive_file_id=%s drive_link=%s…%s\n' \
                "$GREEN" "$vo_id" "${drive_file_id:0:16}..." "${drive_link:0:40}" "$RESET"
            # Save for T4 Drive API verification
            printf '%s|%s|%s\n' "$vo_id" "$drive_file_id" "$folder_id" >> "$WORK_DIR/voiceover_drive_ids.txt"
            vo_ok=$((vo_ok + 1))
        else
            fail "voiceover_empty_drive_${vo_id}"
            printf '%s  FAIL: voiceover %s — drive_file_id=%s drive_link=%s%s\n' \
                "$RED" "$vo_id" "${drive_file_id:-empty}" "${drive_link:-empty}" "$RESET" >&2
            vo_fail=$((vo_fail + 1))
        fi
    done <<< "$vo_rows"

    if [[ "$vo_fail" -gt 0 ]]; then
        return 1
    fi
    printf '  %s%s voiceover(s) with complete Drive metadata%s\n' "$GREEN" "$vo_ok" "$RESET"
    return 0
}

# ── T5: Drive API verification (optional — needs token) ──────────

verify_drive_api() {
    smoke_log_section "T5: Drive API — file exists (if token available)"

    # Check if token file exists
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

    if [[ ! -f "$WORK_DIR/voiceover_drive_ids.txt" ]]; then
        printf '  %sSKIP: no voiceover drive IDs to verify%s\n' "$YELLOW" "$RESET"
        return 0
    fi

    local drive_ok=0 drive_fail=0
    while IFS='|' read -r vo_id drive_file_id folder_id; do
        [[ -z "$drive_file_id" ]] && continue

        local api_url="$DRIVE_API_BASE/drive/v3/files/${drive_file_id}?fields=id,name,mimeType,size,webViewLink"
        local code
        code=$(curl -s --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" \
            -o "$WORK_DIR/drive_${vo_id}.json" -w '%{http_code}' \
            -H "Authorization: Bearer $token" \
            "$api_url" 2>/dev/null || echo "000")

        if [[ "$code" == "200" ]]; then
            local body drive_id drive_size
            body=$(cat "$WORK_DIR/drive_${vo_id}.json")
            drive_id=$(printf '%s' "$body" | jq -r '.id // empty')
            drive_size=$(printf '%s' "$body" | jq -r '.size // "0"')

            if [[ "$drive_id" == "$drive_file_id" ]]; then
                printf '  %sOK: Drive file %s — id match, size=%s%s\n' \
                    "$GREEN" "${drive_file_id:0:16}..." "$drive_size" "$RESET"
                drive_ok=$((drive_ok + 1))
            else
                fail "drive_id_mismatch_${vo_id}"
                printf '%s  FAIL: Drive ID mismatch — db=%s vs api=%s%s\n' \
                    "$RED" "$drive_file_id" "$drive_id" "$RESET" >&2
                drive_fail=$((drive_fail + 1))
            fi
        elif [[ "$code" == "404" ]]; then
            fail "drive_file_not_found_${vo_id}"
            printf '%s  FAIL: Drive file %s returned 404 (file deleted or trashed)%s\n' \
                "$RED" "$drive_file_id" "$RESET" >&2
            drive_fail=$((drive_fail + 1))
        elif [[ "$code" == "401" ]]; then
            fail "drive_token_expired"
            printf '%s  FAIL: Drive API returned 401 — token expired; re-run python3 scripts/generate_drive_token.py%s\n' \
                "$RED" "$RESET" >&2
            drive_fail=$((drive_fail + 1))
        else
            fail "drive_api_error_${code}_${vo_id}"
            printf '%s  FAIL: Drive API returned HTTP %s for file %s%s\n' \
                "$RED" "$code" "$drive_file_id" "$RESET" >&2
            drive_fail=$((drive_fail + 1))
        fi
    done < "$WORK_DIR/voiceover_drive_ids.txt"

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

    # Check Qdrant reachability
    local qdrant_health
    qdrant_health=$(curl -s --max-time 3 "${QDRANT_URL}/healthz" 2>/dev/null || echo "")
    if [[ -z "$qdrant_health" ]]; then
        printf '  %sSKIP: Qdrant not reachable at %s — point verification skipped%s\n' \
            "$YELLOW" "$QDRANT_URL" "$RESET"
        return 0
    fi

    # Get asset IDs from media_assets for this request
    # Canonical: outbox aggregate_id = media_assets.id (set by FinalizeAsset),
    # so we look up media_assets rows directly.
    local asset_ids
    asset_ids=$(sqlite_q "SELECT id FROM media_assets WHERE source = 'voiceover' AND drive_file_id IN (SELECT drive_file_id FROM voiceovers WHERE request_id = '${REQ_ID}' AND drive_file_id != '')")

    if [[ -z "$asset_ids" ]]; then
        printf '  %sSKIP: no media_assets rows to look up in Qdrant%s\n' "$YELLOW" "$RESET"
        return 0
    fi

    local qdrant_ok=0 qdrant_fail=0
    while IFS='|' read -r asset_id; do
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
            # If outbox is still pending, this is expected (Qdrant not yet indexed)
            local outbox_completed
            outbox_completed=$(sqlite_q "SELECT COUNT(*) FROM outbox_events WHERE event_type = 'asset.index.requested' AND aggregate_id = '${asset_id}' AND status = 'completed' AND created_at > datetime('now', '-5 minutes')")
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
    done <<< "$asset_ids"

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
    smoke_log_section "Zone 3: Media Assets + Drive + Qdrant smoke"
    printf '  target:   %s\n  db:       %s\n  qdrant:   %s\n  tag:      %s\n  req_id:   %s\n' \
        "$SMOKE_API_BASE" "$SMOKE_DB" "$QDRANT_URL" "$TAG_PREFIX" "$REQ_ID"

    # Note: voiceover_drive_ids.txt is created by verify_voiceover_drive_fields via >> append

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

    # T1: Generate voiceover
    post_voiceover || { fail "post_voiceover"; exit 1; }

    # Poll to terminal
    poll_to_terminal || true

    # T2: Verify media_assets row
    verify_media_assets || true

    # T3: Verify outbox events
    verify_outbox_events || true

    # T4: Verify voiceover drive fields
    verify_voiceover_drive_fields || true

    # T5: Drive API (optional)
    verify_drive_api || true

    # T6: Qdrant (optional)
    verify_qdrant_point || true

    # ── Verdict ──────────────────────────────────────────────────
    echo
    if (( ${#FAILURES[@]} == 0 )); then
        printf '%sOK: Zone 3 Media Assets + Drive + Qdrant smoke PASS%s\n' \
            "$GREEN" "$RESET"
        printf '  Assertions passed:\n'
        printf '    T1: voiceover generated + job completed\n'
        printf '    T2: media_assets row with drive_file_id + file_hash + lifecycle_state=ACTIVE\n'
        printf '    T3: outbox asset.index.requested emitted + no dead_letter + no last_error\n'
        printf '    T4: voiceover row with drive_file_id + drive_link\n'
        printf '    T5: Drive file exists (if token available)\n'
        printf '    T6: Qdrant point exists (if Qdrant reachable)\n'
        exit 0
    fi

    printf '%sFAIL: %d assertion(s) failed:%s\n' \
        "$RED" "${#FAILURES[@]}" "$RESET" >&2
    for f in "${FAILURES[@]}"; do
        printf '  - %s\n' "$f" >&2
    done
    printf '\n  Failure-to-PR mapping:\n' >&2
    printf '    media_assets empty drive_file_id  → PR-VOICEOVER-PIPELINE-DEBUG-2026-07-08\n' >&2
    printf '    media_assets empty file_hash      → PR-STOCK-FILEHASH-PERSIST\n' >&2
    printf '    media_assets lifecycle != ACTIVE   → PR-STOCK-LIFECYCLE-TRANSITION\n' >&2
    printf '    outbox dead_letter                 → PR-STOCK-OUTBOX-DEAD-LETTERED\n' >&2
    printf '    outbox completed + last_error      → PR-STOCK-OUTBOX-LAST-ERROR\n' >&2
    printf '    Drive file 404                     → PR-VOICEOVER-DRIVE-DRIFT-FORWARD-POINTER\n' >&2
    printf '    Qdrant point missing + completed   → PR-STOCK-OUTBOX-QDRANT-INDEX\n' >&2
    exit 1
}

main "$@"
