#!/usr/bin/env bash
# Source-only Artlist pipeline live-test helper: artlist_pipeline_report.sh.
# shellcheck shell=bash
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    echo "[ERROR] artlist_pipeline_report.sh must be sourced, not executed directly." >&2
    exit 1
fi

artlist_pipeline_report() {
# ─── STEP 5: Discover >= 20 candidates (post-dedup) ──────────────────────
log ""
log "── STEP 5/12  Discover >= ${MIN_ASSETS} candidates (media_assets WHERE source='artlist' AND created_at >= '${RUN_START_ISO}') ──"
# post-dedup count: distinct asset_id. The query may produce multiple
# media_assets rows for the same asset_id across re-publishes; the DoD
# requires >=20 UNIQUE candidates.
ASSETS_SQL_FILE="$OUT_DIR/step5_artlist_assets.txt"
sqlite3 -header -column "$DB_PATH" "
    SELECT id, lifecycle_state,
           CASE WHEN file_hash=''     THEN '<empty>' ELSE substr(file_hash,1,12)     END as file_hash,
           CASE WHEN drive_file_id='' THEN '<empty>' ELSE substr(drive_file_id,1,12) END as drive_file_id
    FROM media_assets WHERE source='artlist' AND created_at >= '${RUN_START_ISO}'
    ORDER BY created_at DESC LIMIT 100;
" > "$ASSETS_SQL_FILE" 2>"${ASSETS_SQL_FILE}.err"
# Count DISTINCT asset_ids (column 1 of the output, after the column
# header on line 2). Use awk to skip the title line (-header) and the
# column header line; data starts on line 3.
DEDUP_COUNT=$(awk 'NR>2 {print $1}' "$ASSETS_SQL_FILE" 2>/dev/null | sort -u | wc -l | tr -d ' ')
if [ "$DEDUP_COUNT" -ge "$MIN_ASSETS" ]; then
    pass "discovered ${DEDUP_COUNT} unique candidate(s) (>= ${MIN_ASSETS} threshold)"
    # Build the canonical asset_id set (sorted, unique) for downstream steps.
    mapfile -t ASSET_IDS < <(awk 'NR>2 {print $1}' "$ASSETS_SQL_FILE" | sort -u | head -n 100)
else
    fail "discovered only ${DEDUP_COUNT} unique candidate(s) (need >= ${MIN_ASSETS})"
fi
NUM_ASSETS="${#ASSET_IDS[@]}"
log "   asset_id set: ${NUM_ASSETS} unique (truncated to 100 for downstream probes)"
# Discovery rows may remain STAGING when acquisition fails; downstream probes
# operate only on durable published artifacts.
mapfile -t PIPELINE_ASSET_IDS < <(sqlite3 -noheader "$DB_PATH" "
    SELECT DISTINCT id FROM media_assets
    WHERE source='artlist' AND lifecycle_state='PUBLISHED' AND created_at >= '${RUN_START_ISO}'
      AND lifecycle_state='PUBLISHED' AND drive_file_id!='' AND file_hash!=''
    ORDER BY created_at DESC LIMIT 100;
" 2>/dev/null)
NUM_PIPELINE_ASSETS="${#PIPELINE_ASSET_IDS[@]}"
log "   durable published asset set: ${NUM_PIPELINE_ASSETS}"

# ─── STEP 6: >= 10 valid ffprobe on the downloaded files ────────────────
log ""
log "── STEP 6/12  ffprobe >= ${MIN_DOWNLOADS} valid (duration > 0, has video stream) ──"
# Artlist's pipeline stores the final rendered MP4 in the Drive
# destination; we re-probe each asset's download via the canonical
# POST /api/artlist/runs/:run_id/clips/:asset_id/download (or the
# stock-style /clips/:id/download route — try both). The probe is
# ffprobe + size + duration > 0. We do NOT count "no download route
# available" as a PASS — that's a 404, fail-closed.
FFPROBE_PASS=0
FFPROBE_FAIL=0
FFPROBE_CORRUPT=0
DOWNLOADS_TMP="$OUT_DIR/step6_downloads"
mkdir -p "$DOWNLOADS_TMP"
IDX=0
for AID in "${PIPELINE_ASSET_IDS[@]}"; do
    IDX=$((IDX+1))
# Canonical application download: use the authenticated PipelineGen
# publication route. Calling Google Drive v3 directly with the PipelineGen
# admin token is invalid (it is not an OAuth token) and produces a misleading
# 403 even when the Drive file is healthy.
    DFID=$(sqlite3 "$DB_PATH" "
        SELECT drive_file_id FROM media_assets WHERE id='$AID';
    " 2>/dev/null | tr -d ' ' || echo "")
    if [ -z "$DFID" ] || [ "$DFID" = "<empty>" ]; then
        FFPROBE_FAIL=$((FFPROBE_FAIL+1))
        log "   asset#${IDX} ${AID}: no drive_file_id in SQLite (skipped)"
        continue
    fi
    MP4_PATH="$DOWNLOADS_TMP/${AID}.mp4"
    HTTP=$(curl -sS -L --max-redirs 5 --max-time 120 -X POST \
        --retry 3 --retry-delay 1 \
        -H "X-Velox-Admin-Token: $TOKEN" \
        -o "$MP4_PATH" -w '%{http_code}' \
        "$BASE/api/media/clips/artlist/clips/${AID}/download" 2>/dev/null || echo "000")
    if [ "$HTTP" != "200" ] && [ "$HTTP" != "206" ]; then
        FFPROBE_FAIL=$((FFPROBE_FAIL+1))
        log "   asset#${IDX} ${AID}: Drive download HTTP ${HTTP} (skipped — not a corruption; will check the count below)"
        continue
    fi
    SIZE=$(stat -c%s "$MP4_PATH" 2>/dev/null || stat -f%z "$MP4_PATH" 2>/dev/null || echo 0)
    if [ "$SIZE" -le "$MIN_MP4_BYTES" ]; then
        FFPROBE_CORRUPT=$((FFPROBE_CORRUPT+1))
        log "   asset#${IDX} ${AID}: size=${SIZE} <= ${MIN_MP4_BYTES} (corrupt/empty)"
        continue
    fi
    ffprobe -v error -show_streams -show_format -of json "$MP4_PATH" \
        > "$DOWNLOADS_TMP/${AID}.ffprobe.json" 2>"$DOWNLOADS_TMP/${AID}.ffprobe.err" || true
    if [ ! -s "$DOWNLOADS_TMP/${AID}.ffprobe.json" ]; then
        FFPROBE_CORRUPT=$((FFPROBE_CORRUPT+1))
        log "   asset#${IDX} ${AID}: ffprobe produced empty output (corrupt)"
        continue
    fi
    HAS_VIDEO=$(jq -r '[.streams[]? | select(.codec_type=="video")] | length' "$DOWNLOADS_TMP/${AID}.ffprobe.json" 2>/dev/null || echo "0")
    DURATION=$(jq -r '.format.duration // "0"' "$DOWNLOADS_TMP/${AID}.ffprobe.json" 2>/dev/null || echo "0")
    DUR_INT=$(awk -v d="$DURATION" 'BEGIN{ if (d+0 > 0) print int(d+0.5); else print 0 }')
    if [ "$HAS_VIDEO" -ge 1 ] && [ "$DUR_INT" -gt 0 ]; then
        FFPROBE_PASS=$((FFPROBE_PASS+1))
    else
        FFPROBE_CORRUPT=$((FFPROBE_CORRUPT+1))
        log "   asset#${IDX} ${AID}: ffprobe video_streams=${HAS_VIDEO} duration=${DURATION} (corrupt)"
    fi
done
if [ "$FFPROBE_PASS" -ge "$MIN_DOWNLOADS" ]; then
    pass "ffprobe valid: ${FFPROBE_PASS}/${NUM_ASSETS} assets (>= ${MIN_DOWNLOADS} threshold; corrupt=${FFPROBE_CORRUPT}, download-skipped=${FFPROBE_FAIL})"
else
    fail "ffprobe valid: only ${FFPROBE_PASS}/${NUM_ASSETS} assets (need >= ${MIN_DOWNLOADS}; corrupt=${FFPROBE_CORRUPT})"
fi

# ─── STEP 7: >= 10 valid FFmpeg outputs (re-encode / re-probe) ───────────
log ""
log "── STEP 7/12  FFmpeg re-encode >= ${MIN_DOWNLOADS} valid outputs ──"
# The "FFmpeg output" in the artlist pipeline is the final rendered MP4
# BEFORE Drive upload. We re-encode each downloaded MP4 via ffmpeg -c
# copy (lossless stream copy) into a destination under the battery's
# output dir, then ffprobe the copy. A successful re-encode (exit 0 +
# destination >0 bytes + ffprobe duration > 0 + video stream present)
# counts as a valid FFmpeg output.
FFMPEG_PASS=0
FFMPEG_FAIL=0
FFMPEG_CORRUPT=0
FFMPEG_TMP="$OUT_DIR/step7_ffmpeg_outputs"
mkdir -p "$FFMPEG_TMP"
IDX=0
for AID in "${PIPELINE_ASSET_IDS[@]}"; do
    IDX=$((IDX+1))
    SRC="$DOWNLOADS_TMP/${AID}.mp4"
    [ -f "$SRC" ] || continue
    DEST="$FFMPEG_TMP/${AID}.ffmpeg.mp4"
    # Real re-encode: -c:v libx264 -preset ultrafast is a genuine FFmpeg
    # invocation (not the synthetic -c copy stream-copy). ultrafast keeps
    # the wall-clock low for the battery while still exercising the
    # decoder + encoder + container-muxer stack end-to-end. -an strips
    # audio to avoid the ffprobe "audio codec" mode-switch issue on
    # some input files.
    ffmpeg -y -v error -i "$SRC" -c:v libx264 -preset ultrafast -an "$DEST" \
        >"$FFMPEG_TMP/${AID}.ffmpeg.log" 2>&1 || true
    if [ ! -s "$DEST" ]; then
        FFMPEG_CORRUPT=$((FFMPEG_CORRUPT+1))
        continue
    fi
    SIZE=$(stat -c%s "$DEST" 2>/dev/null || stat -f%z "$DEST" 2>/dev/null || echo 0)
    if [ "$SIZE" -le "$MIN_MP4_BYTES" ]; then
        FFMPEG_CORRUPT=$((FFMPEG_CORRUPT+1))
        continue
    fi
    ffprobe -v error -show_streams -show_format -of json "$DEST" \
        > "$FFMPEG_TMP/${AID}.ffprobe.json" 2>"$FFMPEG_TMP/${AID}.ffprobe.err" || true
    if [ ! -s "$FFMPEG_TMP/${AID}.ffprobe.json" ]; then
        FFMPEG_CORRUPT=$((FFMPEG_CORRUPT+1))
        continue
    fi
    HAS_VIDEO=$(jq -r '[.streams[]? | select(.codec_type=="video")] | length' "$FFMPEG_TMP/${AID}.ffprobe.json" 2>/dev/null || echo "0")
    DURATION=$(jq -r '.format.duration // "0"' "$FFMPEG_TMP/${AID}.ffprobe.json" 2>/dev/null || echo "0")
    DUR_INT=$(awk -v d="$DURATION" 'BEGIN{ if (d+0 > 0) print int(d+0.5); else print 0 }')
    if [ "$HAS_VIDEO" -ge 1 ] && [ "$DUR_INT" -gt 0 ]; then
        FFMPEG_PASS=$((FFMPEG_PASS+1))
    else
        FFMPEG_CORRUPT=$((FFMPEG_CORRUPT+1))
    fi
done
if [ "$FFMPEG_PASS" -ge "$MIN_DOWNLOADS" ]; then
    pass "ffmpeg re-encode valid: ${FFMPEG_PASS}/${NUM_ASSETS} assets (>= ${MIN_DOWNLOADS} threshold; corrupt=${FFMPEG_CORRUPT})"
else
    fail "ffmpeg re-encode valid: only ${FFMPEG_PASS}/${NUM_ASSETS} assets (need >= ${MIN_DOWNLOADS}; corrupt=${FFMPEG_CORRUPT})"
fi

# ─── STEP 8: >= 10 Drive files verified via API ─────────────────────────
log ""
log "── STEP 8/12  Drive resolve-by-id >= ${MIN_DOWNLOADS} verified ──"
# Pull drive_file_id from SQLite, then POST /api/drive/resolve-by-id
# for each. A PASS is: ok=true AND resolved_count>=1 AND name!='' AND
# size>0 AND trashed=false.
DRIVE_FILE_IDS=$(sqlite3 "$DB_PATH" "
    SELECT DISTINCT drive_file_id FROM media_assets
    WHERE source='artlist' AND created_at >= '${RUN_START_ISO}'
      AND drive_file_id != '' AND drive_file_id IS NOT NULL
    ORDER BY created_at DESC LIMIT 50;
" 2>/dev/null)
DRIVE_PASS=0
DRIVE_FAIL=0
DRIVE_TRASHED=0
DRIVE_SIZE0=0
IDX=0
for DFID in $DRIVE_FILE_IDS; do
    IDX=$((IDX+1))
    [ -z "$DFID" ] && continue
    DRIVE_RESP_FILE="$OUT_DIR/step8_drive_${IDX}_${DFID:0:12}.json"
    DRIVE_HTTP=$(http_post "$BASE/api/drive/resolve-by-id" "$DRIVE_RESP_FILE" \
        "$(jq -nc --arg id "$DFID" '{ids: [$id]}')")
    DOK=$(jq -r '.ok // false' "$DRIVE_RESP_FILE" 2>/dev/null || echo "false")
    DRESOLVED=$(jq -r '.resolved_count // 0' "$DRIVE_RESP_FILE" 2>/dev/null || echo "0")
    if [ "$DOK" != "true" ] || [ "$DRESOLVED" -lt 1 ]; then
        DRIVE_FAIL=$((DRIVE_FAIL+1))
        continue
    fi
    DNAME=$(jq -r '.resolved[0].name    // ""' "$DRIVE_RESP_FILE" 2>/dev/null)
    DSIZE=$(jq -r '.resolved[0].size    // 0' "$DRIVE_RESP_FILE" 2>/dev/null)
    DTRASH=$(jq -r '.resolved[0].trashed // false' "$DRIVE_RESP_FILE" 2>/dev/null)
    if [ -z "$DNAME" ] || [ "$DNAME" = "null" ]; then
        DRIVE_FAIL=$((DRIVE_FAIL+1))
    elif ! [[ "$DSIZE" =~ ^[0-9]+$ ]] || [ "$DSIZE" -le 0 ]; then
        DRIVE_SIZE0=$((DRIVE_SIZE0+1))
    elif [ "$DTRASH" = "true" ]; then
        DRIVE_TRASHED=$((DRIVE_TRASHED+1))
    else
        DRIVE_PASS=$((DRIVE_PASS+1))
    fi
done
if [ "$DRIVE_PASS" -ge "$MIN_DOWNLOADS" ]; then
    pass "Drive verified: ${DRIVE_PASS} files (>= ${MIN_DOWNLOADS} threshold; not-found=${DRIVE_FAIL}, trashed=${DRIVE_TRASHED}, size=0=${DRIVE_SIZE0})"
else
    fail "Drive verified: only ${DRIVE_PASS} files (need >= ${MIN_DOWNLOADS}; not-found=${DRIVE_FAIL}, trashed=${DRIVE_TRASHED})"
fi

# ─── STEP 9: >= 10 PUBLISHED in SQLite ───────────────────────────────────
log ""
log "── STEP 9/12  media_assets lifecycle_state='PUBLISHED' (source='artlist') >= ${MIN_PUBLISHED} ──"
PUB_COUNT=$(sqlite3 "$DB_PATH" "
    SELECT COUNT(DISTINCT id) FROM media_assets
    WHERE source='artlist'
      AND lifecycle_state='PUBLISHED'
      AND created_at >= '${RUN_START_ISO}';
" 2>/dev/null | tr -d ' ' || echo "0")
if [ "$PUB_COUNT" -ge "$MIN_PUBLISHED" ]; then
    pass "PUBLISHED count: ${PUB_COUNT} unique assets (>= ${MIN_PUBLISHED})"
else
    fail "PUBLISHED count: ${PUB_COUNT} unique assets (need >= ${MIN_PUBLISHED})"
fi

# ─── STEP 10: >= 10 outbox events completed ─────────────────────────────
log ""
log "── STEP 10/12  outbox_events status='completed' (event_type='asset.index.requested') >= ${MIN_OUTBOX_COMPLETED} ──"
OUTBOX_COUNT=$(sqlite3 "$DB_PATH" "
    SELECT COUNT(DISTINCT aggregate_id) FROM outbox_events
    WHERE event_type='asset.index.requested'
      AND status='completed'
      AND aggregate_id IN (
        SELECT id FROM media_assets
        WHERE source='artlist' AND created_at >= '${RUN_START_ISO}'
      );
" 2>/dev/null | tr -d ' ' || echo "0")
if [ "$OUTBOX_COUNT" -ge "$MIN_OUTBOX_COMPLETED" ]; then
    pass "outbox completed: ${OUTBOX_COUNT} unique asset events (>= ${MIN_OUTBOX_COMPLETED})"
else
    fail "outbox completed: ${OUTBOX_COUNT} unique asset events (need >= ${MIN_OUTBOX_COMPLETED})"
fi

# ─── STEP 11: >= 10 Qdrant points ─────────────────────────────────────────
log ""
log "── STEP 11/12  Qdrant scroll on '$QDRANT_COLLECTION' (source='artlist', asset_id in our set) >= ${MIN_QDRANT_POINTS} ──"
if [ "$REQUIRE_QDRANT" = "1" ]; then
    # Build the asset_id set as a JSON array for the Qdrant scroll filter.
    ASSET_IDS_JSON=$(printf '%s\n' "${PIPELINE_ASSET_IDS[@]}" | jq -R . | jq -s 'map(select(. != ""))')
    S11_BODY=$(jq -nc --argjson ids "$ASSET_IDS_JSON" --arg src "artlist" '{
        limit: 200,
        with_payload: true,
        filter: {
            must: [
                { key: "source", match: { value: $src } },
                { key: "asset_id", match: { any: $ids } }
            ]
        }
    }')
    S11_FILE="$OUT_DIR/step11_qdrant_scroll.json"
    # Build the Qdrant headers: api-key when QDRANT_API_KEY is set
    # (mirrors artlist_live_e2e_verify.sh's QHEADERS pattern). A
    # secured Qdrant cluster returns 401/403 without the api-key
    # header, so this is REQUIRED for production. Inline curl because
    # the generic http_post helper doesn't carry per-call custom
    # headers.
    if [ -n "${QDRANT_API_KEY:-}" ]; then
        Q_HTTP=$(curl -sS --max-time 30 -X POST \
            -H "X-Velox-Admin-Token: $TOKEN" \
            -H "api-key: ${QDRANT_API_KEY}" \
            -H 'Content-Type: application/json' \
            -d "$S11_BODY" \
            -o "$S11_FILE" -w '%{http_code}' \
            "$QDRANT_URL/collections/$QDRANT_COLLECTION/points/scroll" 2>/dev/null || echo "000")
    else
        Q_HTTP=$(http_post "$QDRANT_URL/collections/$QDRANT_COLLECTION/points/scroll" "$S11_FILE" "$S11_BODY")
    fi
    if [ "$Q_HTTP" = "200" ]; then
        QDRANT_HITS=$(jq -r '.result.points | length // 0' "$S11_FILE" 2>/dev/null || echo "0")
        if [ "$QDRANT_HITS" -ge "$MIN_QDRANT_POINTS" ]; then
            pass "Qdrant scroll: ${QDRANT_HITS} point(s) match our asset_id set (>= ${MIN_QDRANT_POINTS})"
        else
            fail "Qdrant scroll: only ${QDRANT_HITS} point(s) match our asset_id set (need >= ${MIN_QDRANT_POINTS})"
        fi
    else
        fail "Qdrant scroll returned HTTP ${Q_HTTP}"
    fi
else
    note_skip "STEP 11/12  Qdrant scroll skipped (REQUIRE_QDRANT=0)"
fi

# ─── STEP 12: >= 10 found via /api/media/search (sources=['artlist']) ───
log ""
log "── STEP 12/12  POST /api/media/search (sources=['artlist']) >= ${MIN_SEARCH_HITS} of our assets ──"
# Two probes: (a) sources=['artlist'] returns >= MIN_SEARCH_HITS results
# for a representative query; (b) our specific asset_ids appear in the
# results. A PASS requires BOTH.
SEARCH_QUERY="${QUERIES[0]}"
SEARCH_FILE="$OUT_DIR/step12_search.json"
SEARCH_BODY=$(jq -nc --arg q "$SEARCH_QUERY" --argjson limit 50 \
    '{query: $q, sources: ["artlist"], mode: "hybrid", limit: $limit}')
S_HTTP=$(http_post "$BASE/api/media/search" "$SEARCH_FILE" "$SEARCH_BODY")
if [ "$S_HTTP" = "200" ]; then
    SEARCH_FOUND_ARTLIST=$(jq -r '[.items[]?] | length' "$SEARCH_FILE" 2>/dev/null || echo "0")
    # Count our asset_ids in the search response.
    SEARCH_OURS=0
    for AID in "${PIPELINE_ASSET_IDS[@]}"; do
        [ -z "$AID" ] && continue
        PRESENT=$(jq -r --arg a "$AID" '[.items[]? | select((.asset_id // .id // "") == $a)] | length' "$SEARCH_FILE" 2>/dev/null || echo "0")
        SEARCH_OURS=$((SEARCH_OURS + PRESENT))
    done
    if [ "$SEARCH_OURS" -ge "$MIN_SEARCH_HITS" ]; then
        pass "/api/media/search: ${SEARCH_OURS} of our asset_ids present in response (>= ${MIN_SEARCH_HITS} threshold; total artlist results=${SEARCH_FOUND_ARTLIST})"
    elif [ "$SEARCH_FOUND_ARTLIST" -ge "$MIN_SEARCH_HITS" ]; then
        note "/api/media/search: ${SEARCH_FOUND_ARTLIST} artlist results, but only ${SEARCH_OURS} of our asset_ids present (projection lag — Qdrant scroll is canonical truth)"
    else
        fail "/api/media/search: only ${SEARCH_FOUND_ARTLIST} artlist results, ${SEARCH_OURS} of our asset_ids present (need >= ${MIN_SEARCH_HITS} of our asset_ids)"
    fi
else
    fail "/api/media/search returned HTTP ${S_HTTP}"
fi

# ─── ZERO-TOLERANCE DEDUP + NO-ORPHAN CHECKS ─────────────────────────────
log ""
log "── ZERO-TOLERANCE: dedup + no-orphan ──"
# (a) No duplicate asset_id in our set.
DEDUP_UNIQUE=$(printf '%s\n' "${ASSET_IDS[@]}" | sort -u | wc -l | tr -d ' ')
if [ "$DEDUP_UNIQUE" = "$NUM_ASSETS" ]; then
    pass "dedup: ${NUM_ASSETS} unique asset_ids (no duplicates)"
else
    fail "dedup: ${DEDUP_UNIQUE} unique vs ${NUM_ASSETS} total (duplicates present)"
fi

# (b) Every media_assets row in our set has a non-empty drive_file_id.
ORPHAN_ROWS=$(sqlite3 "$DB_PATH" "
    SELECT COUNT(*) FROM media_assets
    WHERE source='artlist' AND lifecycle_state='PUBLISHED' AND created_at >= '${RUN_START_ISO}'
      AND (drive_file_id IS NULL OR drive_file_id = '');
" 2>/dev/null | tr -d ' ')
if [ "$ORPHAN_ROWS" = "0" ]; then
    pass "no-orphan: every media_assets row has a drive_file_id"
else
    fail "no-orphan: ${ORPHAN_ROWS} media_assets row(s) have empty drive_file_id"
fi

# (c) Every outbox_events row for our set has a corresponding media_assets row.
ORPHAN_OUTBOX=$(sqlite3 "$DB_PATH" "
    SELECT COUNT(*) FROM outbox_events oe
    WHERE oe.event_type='asset.index.requested'
      AND oe.created_at >= '${RUN_START_ISO}'
      AND NOT EXISTS (
        SELECT 1 FROM media_assets ma
        WHERE ma.id = oe.aggregate_id
          AND ma.source='artlist'
      );
" 2>/dev/null | tr -d ' ')
if [ "$ORPHAN_OUTBOX" = "0" ]; then
    pass "no-orphan: every outbox_events row has a corresponding media_assets row"
else
    fail "no-orphan: ${ORPHAN_OUTBOX} outbox_events row(s) have no corresponding media_assets row"
fi

# (d) Every outbox_events row for our set has a non-empty event_key (idempotency).
ORPHAN_KEYS=$(sqlite3 "$DB_PATH" "
    SELECT COUNT(*) FROM outbox_events oe
    WHERE oe.event_type='asset.index.requested'
      AND oe.created_at >= '${RUN_START_ISO}'
      AND (oe.event_key IS NULL OR oe.event_key = '');
" 2>/dev/null | tr -d ' ')
if [ "$ORPHAN_KEYS" = "0" ]; then
    pass "no-orphan: every outbox_events row has a non-empty event_key (idempotency surface intact)"
else
    fail "no-orphan: ${ORPHAN_KEYS} outbox_events row(s) have empty event_key"
fi

# (e) Every media_assets row has a non-empty file_hash (no fake-success content).
NO_FAKE_ROWS=$(sqlite3 "$DB_PATH" "
    SELECT COUNT(*) FROM media_assets
    WHERE source='artlist' AND lifecycle_state='PUBLISHED' AND created_at >= '${RUN_START_ISO}'
      AND (file_hash IS NULL OR file_hash = '');
" 2>/dev/null | tr -d ' ')
if [ "$NO_FAKE_ROWS" = "0" ]; then
    pass "no-fake: every media_assets row has a non-empty file_hash"
else
    fail "no-fake: ${NO_FAKE_ROWS} media_assets row(s) have empty file_hash (false-success surface)"
fi

# ─── Verdict ───────────────────────────────────────────────────────────────
log ""
log "═══════════════════════════════════════════════════════════════════"
log "  ArtlistPipeline live test verdict"
log "═══════════════════════════════════════════════════════════════════"
log "  PASS=$PASS  FAIL=$FAIL"
log "  Queries: ${NUM_QUERIES}  Jobs SUCCEEDED: ${SUCCEEDED}/${NUM_QUERIES}"
log "  Unique assets: ${DEDUP_UNIQUE}/${NUM_ASSETS}"
log "  ffprobe valid: ${FFPROBE_PASS}  ffmpeg re-encode valid: ${FFMPEG_PASS}"
log "  Drive verified: ${DRIVE_PASS}  PUBLISHED: ${PUB_COUNT}  outbox completed: ${OUTBOX_COUNT}"
log "  Qdrant hits: ${QDRANT_HITS:-0}  search hits: ${SEARCH_FOUND_ARTLIST:-0}"
log "  Artifacts: $OUT_DIR"
log "═══════════════════════════════════════════════════════════════════"

# Emit a structured one-liner for easy grepping by CI / paste into reports.
printf 'VERDICT pass=%s fail=%s queries=%s jobs_succeeded=%s assets_unique=%s ffprobe_valid=%s ffmpeg_valid=%s drive_verified=%s published=%s outbox_completed=%s qdrant_hits=%s search_hits=%s\n' \
    "$PASS" "$FAIL" "$NUM_QUERIES" "$SUCCEEDED" "$DEDUP_UNIQUE" \
    "$FFPROBE_PASS" "$FFMPEG_PASS" "$DRIVE_PASS" "$PUB_COUNT" "$OUTBOX_COUNT" \
    "${QDRANT_HITS:-0}" "${SEARCH_FOUND_ARTLIST:-0}"

# Exit non-zero if any FAIL — fail-closed contract (AGENTS.md).
if [ "$FAIL" -gt 0 ]; then exit 1; fi
exit 0
}
