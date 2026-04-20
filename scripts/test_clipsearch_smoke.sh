#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_DIR="$ROOT_DIR/src/go-master"
OUT_FILE="/tmp/test_clipsearch_smoke.out"
MAX_ATTEMPTS="${MAX_ATTEMPTS:-2}"
KEYWORDS="${KEYWORDS:-travel technology science}"

# Load shared yt-dlp defaults for YouTube.
source "$ROOT_DIR/scripts/env_ytdlp_defaults.sh"

cd "$GO_DIR"

echo "== Run clipsearch smoke (ForceFresh) =="

SUCCESS_KEYWORD=""
for kw in $KEYWORDS; do
  attempt=1
  while (( attempt <= MAX_ATTEMPTS )); do
    echo "-- keyword=$kw attempt $attempt/$MAX_ATTEMPTS"
    if KEYWORD="$kw" go run ./cmd/tmp_clipsearch_fresh/main.go | tee "$OUT_FILE"; then
      if rg -n '^results=1$' "$OUT_FILE" >/dev/null; then
        SUCCESS_KEYWORD="$kw"
        break
      fi
    fi
    attempt=$((attempt + 1))
    sleep 1
  done
  if [[ -n "$SUCCESS_KEYWORD" ]]; then
    break
  fi
done

if [[ -z "$SUCCESS_KEYWORD" ]]; then
  echo "ERROR: smoke did not produce results=1 for keywords: $KEYWORDS"
  exit 1
fi

DRIVE_ID="$(rg -o 'drive_id=[^ ]+' "$OUT_FILE" | tail -n1 | cut -d= -f2)"
DRIVE_URL="$(rg -o 'url=https://drive.google.com/file/d/[^ ]+/view' "$OUT_FILE" | tail -n1 | cut -d= -f2-)"
FILENAME="$(rg -o 'file=[^ ]+' "$OUT_FILE" | tail -n1 | cut -d= -f2)"
UPLOADED_NOW="false"
if rg -n "Dynamic clip uploaded in keyword folder" "$OUT_FILE" >/dev/null; then
  UPLOADED_NOW="true"
fi

if [[ -z "${DRIVE_ID:-}" || -z "${DRIVE_URL:-}" || -z "${FILENAME:-}" ]]; then
  echo "ERROR: failed to parse smoke output"
  exit 1
fi

echo "== Validate StockDB =="
STOCK_DB="$GO_DIR/data/stock.db.json"
if [[ "$UPLOADED_NOW" == "true" ]]; then
  rg -n "\"clip_id\": \"$DRIVE_ID\"" "$STOCK_DB" >/dev/null || { echo "ERROR: clip_id not found in stock.db"; exit 1; }
  rg -n "\"filename\": \"$FILENAME\"" "$STOCK_DB" >/dev/null || { echo "ERROR: filename missing in stock.db"; exit 1; }
else
  echo "dedup path detected: no new upload, stock.db new clip_id check skipped"
fi

echo "== Validate ArtlistDB =="
ARTLIST_DB="$GO_DIR/data/artlist_local.db.json"
rg -n "\"drive_file_id\": \"$DRIVE_ID\"" "$ARTLIST_DB" >/dev/null || { echo "ERROR: drive_file_id not found in artlist db"; exit 1; }
rg -n "\"drive_url\": \"$DRIVE_URL\"" "$ARTLIST_DB" >/dev/null || { echo "ERROR: drive_url mismatch in artlist db"; exit 1; }
rg -n "\"local_path_drive\": \"Stock/Artlist/$SUCCESS_KEYWORD/" "$ARTLIST_DB" >/dev/null || { echo "ERROR: local_path_drive folder mismatch"; exit 1; }

echo "SUCCESS"
echo "keyword=$SUCCESS_KEYWORD"
echo "uploaded_now=$UPLOADED_NOW"
echo "drive_id=$DRIVE_ID"
echo "drive_url=$DRIVE_URL"
echo "filename=$FILENAME"
