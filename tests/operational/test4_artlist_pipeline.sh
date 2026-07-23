#!/usr/bin/env bash
# test4_artlist_pipeline.sh — Test 4: pipeline Artlist completa per 3 video
#
# Verifica end-to-end:
#   1. Avvio run Artlist con 3 clip
#   2. Job terminal SUCCEEDED senza blocchi (sessione/autorizzazione/limite giornaliero)
#   3. 3 asset prodotti con hash, drive_file_id, download_link
#   4. File MP4 su Drive accessibile e valido
#   5. Durata tagliata a 7 secondi (per policy/config)
#   6. Normalizzazione a 1920x1080
#
# Richiede: VELOX_ADMIN_TOKEN, server e consumer attivi, scraper Artlist attivo,
# Drive abilitato, acquisition_mode=authorized_api, daily limit > 0.

set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Source helpers
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

smoke_require curl jq sqlite3

TOKEN="${VELOX_ADMIN_TOKEN:-}"
[[ -n "$TOKEN" ]] || { echo "VELOX_ADMIN_TOKEN unset"; exit 2; }

BASE="${API_BASE:-127.0.0.1:${VELOX_PORT:-8000}}"
API="http://$BASE"
DB="${VELOX_DATA_DIR:-./data}/media/media.db.sqlite"
SEARCH_TERM="${SEARCH_TERM:-business team working in modern office}"
LIMIT=3
POLL_INTERVAL=15
POLL_MAX=40  # ~10 minuti

LAST_JSON="/tmp/test4_artlist_pipeline_last.json"

log_section() { echo ""; echo "▶ $1"; }

cleanup() {
  rm -f /tmp/test4_*.mp4 /tmp/test4_ffprobe_*.json
}
trap cleanup EXIT

# ─────────────────────────────────────────────────────────────
# 1) Verifica preliminare: server, consumer, scraper
# ─────────────────────────────────────────────────────────────
log_section "Test 4 — Artlist pipeline completa per $LIMIT video"

echo "Verifica servizi preliminari..."

ready=$(curl -sS --max-time 5 "$API/ready" | jq -r '.status // "unknown"')
[[ "$ready" == "ready" ]] || { echo "Server non ready: $ready"; exit 1; }

consumer=$(curl -sS --max-time 5 -H "Authorization: Bearer $TOKEN" "$API/api/artlist/job-consumer" | jq -r '.active // false')
[[ "$consumer" == "true" ]] || { echo "Consumer media.artlist non attivo"; exit 1; }

scraper_healthy=$(curl -sS --max-time 5 http://127.0.0.1:9123/health | jq -r '.healthy // false')
[[ "$scraper_healthy" == "true" ]] || { echo "Scraper Artlist non healthy"; exit 1; }

echo "  server ready ✓  consumer active ✓  scraper healthy ✓"

# ─────────────────────────────────────────────────────────────
# 2) Avvio run Artlist
# ─────────────────────────────────────────────────────────────
log_section "Avvio run: term='$SEARCH_TERM' limit=$LIMIT"

run_body=$(jq -nc --arg term "$SEARCH_TERM" --argjson limit "$LIMIT" '{
  term: $term,
  limit: $limit,
  dry_run: false
}')

run_resp=$(curl -sS --max-time 30 -X POST "$API/api/artlist/run" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d "$run_body")

echo "Run response: $run_resp"
job_id=$(echo "$run_resp" | jq -r '.run_id // ""')
[[ -n "$job_id" ]] || { echo "run_id mancante"; exit 1; }
echo "Job enqueued: $job_id"

# ─────────────────────────────────────────────────────────────
# 3) Polling stato job
# ─────────────────────────────────────────────────────────────
log_section "Attesa completamento job $job_id"

status="?"
job_full="{}"
for i in $(seq 1 $POLL_MAX); do
  sleep "$POLL_INTERVAL"
  job_full=$(curl -sS --max-time 30 "$API/api/jobs/$job_id/full" \
    -H "Authorization: Bearer $TOKEN" || true)
  status=$(echo "$job_full" | jq -r '.status // "?"')
  echo "  poll #$i: status=$status"
  if [[ "$status" == "SUCCEEDED" || "$status" == "FAILED" ]]; then
    break
  fi
done

if [[ "$status" != "SUCCEEDED" ]]; then
    if [[ "$status" == "?" || "$status" == "RUNNING" ]]; then
      echo "FAIL: timeout polling job (stato ancora $status dopo $POLL_MAX tentativi)"
    else
      echo "Job non completato con successo (status=$status)"
    fi
    echo "$job_full" | jq .
    exit 1
  fi

# Controlli sul risultato del run
found=$(echo "$job_full" | jq -r '.result.found // 0')
processed=$(echo "$job_full" | jq -r '.result.processed // 0')
skipped=$(echo "$job_full" | jq -r '.result.skipped // 0')
failed=$(echo "$job_full" | jq -r '.result.failed // 0')
echo "Run summary: found=$found processed=$processed skipped=$skipped failed=$failed"

if [[ "$processed" -lt "$LIMIT" || "$failed" -ne 0 ]]; then
  echo "FAIL: atteso processed>=$LIMIT e failed=0, trovato processed=$processed failed=$failed"
  echo "$job_full" | jq .
  exit 1
fi

blocked_items=$(echo "$job_full" | jq -r '.result.items[]? | select((.status // "") | startswith("blocked_")) | .status' | tr '\n' ' ')
if [[ -n "$blocked_items" ]]; then
  echo "FAIL: elementi bloccati rilevati: $blocked_items"
  exit 1
fi

echo "Job SUCCEEDED (nessun blocco di sessione/autorizzazione/limite giornaliero)"

# ─────────────────────────────────────────────────────────────
# 4) Estrazione asset prodotti
# ─────────────────────────────────────────────────────────────
log_section "Asset prodotti"

asset_ids=$(echo "$job_full" | jq -r '.result.items[]?.clip_id // empty')
[[ -n "$asset_ids" ]] || { echo "Nessun asset prodotto"; exit 1; }

mapfile -t assets <<< "$asset_ids"
if [[ ${#assets[@]} -lt $LIMIT ]]; then
  echo "Attesi $LIMIT asset, trovati ${#assets[@]}"
  exit 1
fi

echo "Asset prodotti: ${assets[*]}"

# Salva JSON per eventuali verifiche manuali
echo "$job_full" > "$LAST_JSON"

# ─────────────────────────────────────────────────────────────
# 5) Verifiche per-asset
# ─────────────────────────────────────────────────────────────
log_section "Verifiche per-asset"

fail=0
for aid in "${assets[@]}"; do
  echo "--- Asset $aid ---"

  # Recupera riga media_assets
  row=$(sqlite3 -json "$DB" "
    SELECT id, file_hash, drive_file_id, drive_link, download_link,
           COALESCE(lifecycle_state, '') AS lifecycle_state,
           COALESCE(index_state, '') AS index_state,
           COALESCE(metadata_json, '') AS metadata_json
    FROM media_assets WHERE id='$aid'
  " | jq '.[0]')

  if [[ -z "$row" || "$row" == "null" ]]; then
    echo "  FAIL: riga media_assets mancante"
    fail=1
    continue
  fi

  file_hash=$(echo "$row" | jq -r '.file_hash // ""')
  drive_file_id=$(echo "$row" | jq -r '.drive_file_id // ""')
  drive_link=$(echo "$row" | jq -r '.drive_link // ""')
  download_link=$(echo "$row" | jq -r '.download_link // ""')
  lstate=$(echo "$row" | jq -r '.lifecycle_state // ""')
  istate=$(echo "$row" | jq -r '.index_state // ""')
  metadata_json=$(echo "$row" | jq -r '.metadata_json // ""')

  echo "  lifecycle_state=$lstate index_state=$istate"
  echo "  drive_file_id=$drive_file_id"
  echo "  download_link=$download_link"

  [[ -n "$file_hash" ]] || { echo "  FAIL: file_hash vuoto"; fail=1; }
  [[ -n "$drive_file_id" ]] || { echo "  FAIL: drive_file_id vuoto"; fail=1; }
  [[ -n "$drive_link" ]] || { echo "  FAIL: drive_link vuoto"; fail=1; }
  [[ -n "$download_link" ]] || { echo "  FAIL: download_link vuoto"; fail=1; }
  [[ "$lstate" == "PUBLISHED" ]] || { echo "  FAIL: lifecycle_state=$lstate (atteso PUBLISHED)"; fail=1; }

  # Verifica file su Drive
  drive_resp=$(curl -sS --max-time 30 -X POST "$API/api/drive/resolve-by-id" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d "$(jq -nc --arg id "$drive_file_id" '{ids: [$id]}')")

  drive_ok=$(echo "$drive_resp" | jq -r '.ok // false')
  drive_size=$(echo "$drive_resp" | jq -r '.resolved[0].size // 0')
  drive_trashed=$(echo "$drive_resp" | jq -r '.resolved[0].trashed // true')

  drive_mime=$(echo "$drive_resp" | jq -r '.resolved[0].mime_type // ""')
  if [[ "$drive_ok" != "true" || "$drive_size" -eq 0 || "$drive_trashed" != "false" ]]; then
    echo "  FAIL: file Drive non valido (ok=$drive_ok size=$drive_size trashed=$drive_trashed)"
    fail=1
  else
    echo "  Drive file OK (size=$drive_size bytes mime=$drive_mime)"
  fi

  if [[ "$drive_mime" != "video/mp4" ]]; then
    echo "  FAIL: mime_type atteso video/mp4, trovato $drive_mime"
    fail=1
  fi

  # Verifica blocco autorizzazione / limite giornaliero
  audit_status=$(sqlite3 "$DB" "
    SELECT status FROM artlist_download_audit
    WHERE asset_id='$aid'
    ORDER BY created_at DESC LIMIT 1
  " 2>/dev/null || echo "")
  echo "  audit_status=$audit_status"
  if [[ "$audit_status" != "succeeded" ]]; then
    echo "  FAIL: audit status non succeeded ($audit_status)"
    fail=1
  fi

  # Verifica MP4 valido, durata e risoluzione via ffprobe sul download_link
  if command -v ffprobe >/dev/null 2>&1; then
    tmpfile="/tmp/test4_$aid.mp4"
    ffout="/tmp/test4_ffprobe_$aid.json"
    # Google Drive direct-download URL from drive_file_id
    drive_direct="https://drive.google.com/uc?export=download&id=${drive_file_id}"
    echo "  Downloading per ffprobe..."
    if curl -sSL --max-time 180 -o "$tmpfile" "$drive_direct" || curl -sSL --max-time 180 -o "$tmpfile" "$download_link"; then
      if ffprobe -v error -select_streams v:0 -show_entries stream=width,height,duration,codec_name -of json "$tmpfile" 2>/dev/null > "$ffout"; then
        width=$(jq -r '.streams[0].width // 0' "$ffout")
        height=$(jq -r '.streams[0].height // 0' "$ffout")
        duration=$(jq -r '.streams[0].duration // 0' "$ffout")
        codec=$(jq -r '.streams[0].codec_name // ""' "$ffout")
        echo "  ffprobe: ${width}x${height} duration=${duration}s codec=${codec}"

        # Atteso codec h264 in MP4
        if [[ "$codec" != "h264" ]]; then
          echo "  FAIL: codec video non h264 ($codec)"
          fail=1
        fi
        # Atteso 1920x1080
        if [[ "$width" -ne 1920 || "$height" -ne 1080 ]]; then
          echo "  FAIL: risoluzione diversa da 1920x1080 (${width}x${height})"
          fail=1
        fi
        # Atteso durata 7s (tolleranza 6-8s)
        awk_exit=0
        awk -v d="$duration" 'BEGIN { if (d < 6.0 || d > 8.0) exit 1; exit 0 }' || awk_exit=$?
        if [[ "$awk_exit" -ne 0 ]]; then
          echo "  FAIL: durazione diversa da 7s (${duration}s)"
          fail=1
        fi
      else
        echo "  FAIL: ffprobe non riesce ad analizzare il file"
        fail=1
      fi
    else
      echo "  FAIL: impossibile scaricare il file per ffprobe"
      fail=1
    fi
  else
    echo "  SKIP: ffprobe non disponibile"
  fi
done

# ─────────────────────────────────────────────────────────────
# 6) Veredict
# ─────────────────────────────────────────────────────────────
log_section "Veredict"
if [[ "$fail" -eq 0 ]]; then
  echo "PASS — Test 4 completato con successo per ${#assets[@]} video"
  exit 0
else
  echo "FAIL — Test 4 fallito (vedi dettagli sopra)"
  exit 1
fi
