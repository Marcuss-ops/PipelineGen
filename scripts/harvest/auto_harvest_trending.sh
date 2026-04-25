#!/usr/bin/env bash
# Monitoraggio canali e download automatico video trending (ultime 48h)
# FLUSSO ENTERPRISE: Lock, Download, Atomic Indexing.
set -euo pipefail

DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
PROJECT_ROOT="$(dirname "$DIR")"
CONFIG_FILE="$PROJECT_ROOT/config/channel_monitor_config.json"
DOWNLOAD_DIR="$PROJECT_ROOT/data/downloads"
INDEX_FILE="$PROJECT_ROOT/data/clip_index.json"
WRAPPER="$DIR/yt_dlp_wrapper.sh"
INDEXER="$PROJECT_ROOT/bin/indexer"
LOCK_FILE="/tmp/velox_harvester.lock"

# 1. Lock Mechanism
exec 200>"$LOCK_FILE"
if ! flock -n 200; then
    echo "⚠️  Un'altra istanza dell'harvester è già in esecuzione. Esco."
    exit 0
fi

log() {
    echo "[$(date +'%Y-%m-%d %H:%M:%S')] $1"
}

log "🚀 Avvio Harvester Trending (Ultime 48 ore)..."

# Assicuriamoci che le directory esistano
mkdir -p "$DOWNLOAD_DIR"
mkdir -p "$(dirname "$INDEX_FILE")"

# Funzione per processare un canale
process_channel() {
    local channel_url=$1
    local category=$2
    local min_views=$3
    
    log "🔍 Analizzando canale: $channel_url [$category]"
    
    # Cerchiamo video caricati nelle ultime 48 ore
    TWO_DAYS_AGO=$(date -d "2 days ago" +%Y%m%d)
    
    # Usiamo --print-json per estrarre dati in modo affidabile
    $WRAPPER \
        --flat-playlist \
        --print-json \
        --dateafter "$TWO_DAYS_AGO" \
        --match-filter "view_count >= $min_views" \
        --playlist-items 10 \
        "$channel_url/videos" 2>/dev/null | while read -r line; do
            
        VIDEO_ID=$(echo "$line" | jq -r '.id')
        VIDEO_TITLE=$(echo "$line" | jq -r '.title')
        VIEWS=$(echo "$line" | jq -r '.view_count')
        
        # Pulizia titolo per filesystem
        SAFE_TITLE=$(echo "$VIDEO_TITLE" | tr -dc '[:alnum:]\n\r ' | tr ' ' '_')
        TARGET_DIR="$DOWNLOAD_DIR/$category"
        mkdir -p "$TARGET_DIR"
        
        FINAL_FILE="$TARGET_DIR/${SAFE_TITLE}.mp4"
        
        if [[ -f "$FINAL_FILE" ]]; then
            log "   ⏭️  Video già presente: $SAFE_TITLE"
            continue
        fi

        log "   🔥 Trending: $VIDEO_TITLE ($VIEWS views)"
        log "   📥 Download in corso..."
        
        # Download in file temporaneo per atomicità
        TMP_FILE="${FINAL_FILE}.downloading"
        if $WRAPPER \
            -f "bestvideo[height<=720][ext=mp4]+bestaudio[ext=m4a]/best[height<=720][ext=mp4]" \
            --merge-output-format mp4 \
            -o "$TMP_FILE" \
            "https://www.youtube.com/watch?v=$VIDEO_ID" > /dev/null 2>&1; then
            
            mv "$TMP_FILE" "$FINAL_FILE"
            log "   ✅ Completato: $SAFE_TITLE"
        else
            rm -f "$TMP_FILE"
            log "   ❌ Fallito download per: $VIDEO_ID"
        fi
    done
}

# Controllo dipendenze
if ! command -v jq >/dev/null 2>&1; then
    log "❌ Errore: 'jq' è richiesto."
    exit 1
fi

if [[ ! -x "$INDEXER" ]]; then
    log "🔄 Compilazione indexer..."
    (cd "$PROJECT_ROOT/src/go-master" && go build -o "$INDEXER" ./cmd/indexer/main.go)
fi

# Iterazione canali
channels_count=$(jq '.channels | length' "$CONFIG_FILE")
for ((i=0; i<$channels_count; i++)); do
    url=$(jq -r ".channels[$i].url" "$CONFIG_FILE")
    cat=$(jq -r ".channels[$i].category" "$CONFIG_FILE")
    views=$(jq -r ".channels[$i].min_views" "$CONFIG_FILE")
    
    process_channel "$url" "$cat" "$views"
done

# 3. Atomic Indexing
log "🔄 Aggiornamento DB Clip Index (Enterprise Indexer)..."
"$INDEXER" -dir "$DOWNLOAD_DIR" -out "$INDEX_FILE"

log "✨ Flusso completato. Il backend leggerà i nuovi dati alla prossima richiesta."
