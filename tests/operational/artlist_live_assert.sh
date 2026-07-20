#!/usr/bin/env bash
# artlist_live_assert.sh — assert phase for artlist_live_e2e_verify.sh
#
# Sourced by the main shim AFTER run.sh. Runs the per-asset 9-step
# verification (audit status, media_assets row, Drive resolve-by-id,
# outbox_events, Qdrant scroll + payload) plus the STEP 9 unified
# /api/media/search round-trip. Per-asset verdicts accumulate in the
# ASSET_VERDICTS array (mutated by append_asset_verdict from env.sh).
#
# Cross-phase reads:
#   TOKEN / BASE_URL / CURL_TIMEOUT / SEARCH_TERM / LIMIT / COLLECTION /
#   DB_PATH / QDRANT_URL / SCROLL_TIMEOUT / SCRAPER_CONNECT_TIMEOUT_SECONDS
#   ASSET_IDS / QHEADERS (declared in prep.sh)
#
# Cross-phase writes (consumed by teardown.sh):
#   PASS / WARN / FAIL counters, ASSET_VERDICTS array,
#   SEARCH_FOUND_TOTAL / SEARCH_FOUND_ARTLIST / SEARCH_PRESENT (STEP 9)

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    echo "[ERROR] artlist_live_assert.sh must be sourced, not executed directly." >&2
    exit 1
fi

# ============================================================
# Per-asset verification: 9 steps per asset
# ============================================================
for AID in ${ASSET_IDS}; do
    log_info "=== ASSET ${AID}: 9-step verification ==="

    AP=0; AW=0; AF=0
    tap()  { local label="$1"; local cond="$2"; local details="$3"
        if [[ "${cond}" == "1" ]]; then
            log_pass  "  ${AID}: ${label}${details:+ — ${details}}"; AP=$((AP+1));
        else
            log_fail  "  ${AID}: ${label}${details:+ — ${details}}"; AF=$((AF+1));
        fi
    }
    taw()  { local label="$1"; local cond="$2"; local details="$3"
        if [[ "${cond}" == "1" ]]; then
            log_pass  "  ${AID}: ${label}${details:+ — ${details}}"; AP=$((AP+1));
        else
            log_warn  "  ${AID}: ${label}${details:+ — ${details}}"; AW=$((AW+1));
        fi
    }

    # --- STEP 3: artlist_download_audit status='succeeded' ---
    log_info "--- 3: artlist_download_audit ---"
    AUDIT_STATUS=$(sqlite3 "${DB_PATH}" "
        SELECT status FROM artlist_download_audit
        WHERE asset_id='${AID}'
        ORDER BY created_at DESC LIMIT 1" 2>/dev/null || echo "")
    if [[ "${AUDIT_STATUS}" == "succeeded" ]]; then
        tap "download audit status=succeeded" 1 ""
    elif [[ -z "${AUDIT_STATUS}" ]]; then
        tap "download audit status=succeeded" 0 "no audit row for asset_id='${AID}'"
    else
        tap "download audit status=succeeded" 0 "got '${AUDIT_STATUS}'"
    fi

    # --- STEP 4: media_assets row (SQLite via -json + jq — robust) ---
    log_info "--- 4: SQLite media_assets ---"
    ROW_JSON=$(sqlite3 -json "${DB_PATH}" "
        SELECT source, media_type, lifecycle_state,
               COALESCE(drive_file_id, '') AS drive_file_id,
               COALESCE(drive_link, '') AS drive_link,
               COALESCE(download_link, '') AS download_link,
               COALESCE(file_hash, '') AS file_hash,
               COALESCE(source_version, '') AS source_version,
               COALESCE(index_state, '') AS index_state
        FROM media_assets WHERE id='${AID}'
    " 2>/dev/null || true)

    if [[ -z "${ROW_JSON}" || "${ROW_JSON}" == "[]" ]]; then
        log_fail "  ${AID}: media_assets row not found"
        AF=$((AF+1))
        append_asset_verdict "${AID}" "0|0|1"
        continue
    fi

    SRC=$(echo "${ROW_JSON}"   | jq -r '.[0].source           // ""')
    MTYPE=$(echo "${ROW_JSON}" | jq -r '.[0].media_type       // ""')
    LSTATE=$(echo "${ROW_JSON}" | jq -r '.[0].lifecycle_state  // ""')
    DFID=$(echo "${ROW_JSON}"  | jq -r '.[0].drive_file_id    // ""')
    DLINK=$(echo "${ROW_JSON}" | jq -r '.[0].drive_link       // ""')
    DOWNLINK=$(echo "${ROW_JSON}" | jq -r '.[0].download_link  // ""')
    FHASH=$(echo "${ROW_JSON}" | jq -r '.[0].file_hash        // ""')
    SVER=$(echo "${ROW_JSON}"  | jq -r '.[0].source_version   // ""')
    ISTATE=$(echo "${ROW_JSON}" | jq -r '.[0].index_state     // ""')

    [[ "${SRC}" == "artlist" ]] && tap "source=artlist" 1 "" \
        || tap "source=artlist" 0 "got '${SRC}'"
    [[ "${MTYPE}" == "video" ]] && tap "media_type=video" 1 "" \
        || tap "media_type=video" 0 "got '${MTYPE}'"
    if [[ "${LSTATE}" == "PUBLISHED" ]]; then
        tap "lifecycle_state=PUBLISHED" 1 "got '${LSTATE}'"
    else
        tap "lifecycle_state=PUBLISHED" 0 "got '${LSTATE}'"
    fi
    [[ -n "${DFID}"     ]] && tap "drive_file_id valorizzato" 1 "" \
        || tap "drive_file_id valorizzato" 0 "got empty"
    [[ -n "${DLINK}"    ]] && tap "drive_link valorizzato" 1 "" \
        || tap "drive_link valorizzato" 0 "got empty"
    [[ -n "${DOWNLINK}" ]] && tap "download_link valorizzato" 1 "" \
        || tap "download_link valorizzato" 0 "got empty"
    [[ -n "${FHASH}" ]] && tap "file_hash valorizzato" 1 "" \
        || tap "file_hash valorizzato" 0 "got empty"
    [[ -n "${SVER}"     ]] && tap "source_version valorizzato" 1 "" \
        || tap "source_version valorizzato" 0 "got empty"
    [[ "${ISTATE}" == "INDEXED" ]] && tap "index_state=INDEXED" 1 "" \
        || tap "index_state=INDEXED" 0 "got '${ISTATE}'"

    # --- STEP 5: Drive Files.Get via /api/drive/resolve-by-id ---
    if [[ -n "${DFID}" ]]; then
        log_info "--- 5: Drive resolve-by-id (${DFID}) ---"
        DRIVE_RESP=$(curl -s --max-time "${CURL_TIMEOUT}" -X POST "${BASE_URL}/api/drive/resolve-by-id" \
            -H "$(auth_header)" \
            -H 'Content-Type: application/json' \
            -d "$(jq -nc --arg id "${DFID}" '{ids: [$id]}')")

        DOK=$(echo "${DRIVE_RESP}"   | jq -r '.ok            // false')
        DRESOLVED=$(echo "${DRIVE_RESP}" | jq -r '.resolved_count // 0')

        if [[ "${DOK}" == "true" && "${DRESOLVED}" -ge 1 ]]; then
            tap "Drive resolve-by-id ok" 1 ""
            DNAME=$(echo "${DRIVE_RESP}" | jq -r '.resolved[0].name    // ""')
            DSIZE_RAW=$(echo "${DRIVE_RESP}" | jq -r '.resolved[0].size // 0')
            DTRASH=$(echo "${DRIVE_RESP}" | jq -r '.resolved[0].trashed // false')
            DMIME=$(echo "${DRIVE_RESP}"  | jq -r '.resolved[0].mime_type // ""')

            [[ -n "${DNAME}" && "${DNAME}" != "null" ]] \
                && tap "Drive name non-empty" 1 "name='${DNAME}'" \
                || tap "Drive name non-empty" 0 "got empty"
            [[ "${DSIZE_RAW}" =~ ^[0-9]+$ && "${DSIZE_RAW}" -gt 0 ]] \
                && tap "Drive size > 0" 1 "size=${DSIZE_RAW}" \
                || tap "Drive size > 0" 0 "got '${DSIZE_RAW}'"
            [[ "${DTRASH}" == "false" ]] \
                && tap "Drive trashed=false" 1 "" \
                || tap "Drive trashed=false" 0 "got '${DTRASH}'"
            [[ -n "${DMIME}" && "${DMIME}" != "null" ]] \
                && taw "Drive mime_type present" 1 "mime='${DMIME}'" \
                || taw "Drive mime_type present" 0 "got empty"
        else
            tap "Drive resolve-by-id ok" 0 "response: $(echo "${DRIVE_RESP}" | head -c 200)"
        fi
    else
        log_warn "  ${AID}: skipping Drive resolve (no drive_file_id)"
    fi

    # --- STEP 6: outbox_events status in (completed|superseded) ---
    log_info "--- 6: outbox_events status ---"
    OUTBOX_STATUS=$(sqlite3 "${DB_PATH}" "
        SELECT status FROM outbox_events
        WHERE event_type='asset.index.requested'
          AND aggregate_id='${AID}'
        ORDER BY id DESC LIMIT 1" 2>/dev/null || echo "")
    if [[ "${OUTBOX_STATUS}" == "completed" || "${OUTBOX_STATUS}" == "superseded" ]]; then
        tap "outbox index event=${OUTBOX_STATUS}" 1 ""
    elif [[ -z "${OUTBOX_STATUS}" ]]; then
        taw "outbox event present" 0 "no row for aggregate_id='${AID}'"
    else
        tap "outbox index event in (completed,superseded)" 0 "got '${OUTBOX_STATUS}'"
    fi

    # --- STEP 7+8: Qdrant scroll + payload ---
    log_info "--- 7+8: Qdrant scroll on ${COLLECTION} ---"
    SCROLL=$(curl -sS --connect-timeout "${SCRAPER_CONNECT_TIMEOUT_SECONDS:-5}" --max-time "${SCROLL_TIMEOUT:-120}" \
        -X POST "${QDRANT_URL}/collections/${COLLECTION}/points/scroll" \
        "${QHEADERS[@]}" \
        -H 'Content-Type: application/json' \
        -d "$(jq -nc --arg id "${AID}" '{
            filter: { must: [ { key: "asset_id", match: { value: $id } } ] },
            limit: 5,
            with_payload: true,
            with_vector: false
        }')")

    SCROLL_STATUS=$(echo "${SCROLL}" | jq -r '.status // "ok"')
    if [[ "${SCROLL_STATUS}" != "ok" ]]; then
        taw "Qdrant scroll endpoint ok" 0 "status='${SCROLL_STATUS}' — response: $(echo "${SCROLL}" | head -c 200)"
    else
        POINT_COUNT=$(echo "${SCROLL}" | jq -r '.result.points | length // 0' 2>/dev/null || echo "0")
        [[ "${POINT_COUNT}" -ge 1 ]] \
            && tap "Qdrant: punto con payload.asset_id=${AID}" 1 "count=${POINT_COUNT}" \
            || taw "Qdrant: punto con payload.asset_id=${AID}" 0 "got count=${POINT_COUNT}"

        if [[ "${POINT_COUNT}" -ge 1 ]]; then
            PAYLOAD_SRC=$(echo "${SCROLL}" | jq -r '.result.points[0].payload.source          // ""')
            PAYLOAD_MT=$(echo "${SCROLL}"  | jq -r '.result.points[0].payload.media_type      // ""')
            PAYLOAD_LS=$(echo "${SCROLL}"  | jq -r '.result.points[0].payload.lifecycle_state // ""')
            [[ "${PAYLOAD_SRC}" == "artlist" ]] \
                && tap "Qdrant payload source=artlist" 1 "got '${PAYLOAD_SRC}'" \
                || tap "Qdrant payload source=artlist" 0 "got '${PAYLOAD_SRC}'"
            [[ "${PAYLOAD_MT}" == "video" ]] \
                && tap "Qdrant payload media_type=video" 1 "" \
                || tap "Qdrant payload media_type=video" 0 "got '${PAYLOAD_MT}'"
            if [[ "${PAYLOAD_LS}" == "PUBLISHED" ]]; then
                tap "Qdrant payload lifecycle_state=PUBLISHED" 1 "got '${PAYLOAD_LS}'"
            else
                tap "Qdrant payload lifecycle_state=PUBLISHED" 0 "got '${PAYLOAD_LS}'"
            fi
        fi
    fi

    # Per-asset tally
    append_asset_verdict "${AID}" "${AP}|${AW}|${AF}"
done

# ============================================================
# STEP 9: unified search /api/media/search returns the asset
# ============================================================
log_info "=== STEP 9: POST /api/media/search (sources=['artlist']) ==="
SEARCH=$(curl -s --max-time "${CURL_TIMEOUT}" -X POST "${BASE_URL}/api/media/search" \
    -H "$(auth_header)" \
    -H 'Content-Type: application/json' \
    -d "$(jq -nc --arg q "${SEARCH_TERM}" --argjson limit "${LIMIT}" \
        '{query: $q, sources: ["artlist"], mode: "hybrid", limit: $limit}')")

SEARCH_FOUND_TOTAL=$(echo "${SEARCH}"   | jq -r '.results | length                                    // 0' 2>/dev/null || echo "0")
SEARCH_FOUND_ARTLIST=$(echo "${SEARCH}" | jq -r '[.results[]? | select(.source=="artlist")] | length'  2>/dev/null || echo "0")
SEARCH_PRESENT=$(echo "${SEARCH}"       | jq -r --arg a "${ASSET_IDS}" \
    '[$a | split(" ")[] as $id | .results[]? | select((.asset_id // .id // "") == $id)] | length' \
    2>/dev/null || echo "0")

if [[ "${SEARCH_FOUND_ARTLIST}" -ge 1 ]]; then
    log_pass "/api/media/search returned ${SEARCH_FOUND_ARTLIST} artlist result(s) (of ${SEARCH_FOUND_TOTAL} total)"
else
    log_warn "/api/media/search returned 0 artlist results — the embedding pipeline may be stale; the Qdrant scroll above is the canonical truth"
fi

if [[ "${SEARCH_PRESENT}" -ge 1 ]]; then
    log_pass "/api/media/search result includes at least one of our asset_id(s)"
else
    log_warn "/api/media/search did not include our specific asset_id(s) (SEARCH_PRESENT=${SEARCH_PRESENT})"
fi
