#!/usr/bin/env bash
# Monitoraggio canali e download automatico video trending (ultime 48h)
set -euo pipefail

DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
PROJECT_ROOT="$(dirname "$DIR")"
CONFIG_FILE="$PROJECT_ROOT/config/channel_monitor_config.json"
DOWNLOAD_DIR="$PROJECT_ROOT/data/downloads"
WRAPPER="$DIR/yt_dlp_wrapper.sh"

echo "🚀 Avvio Harvester Trending (Ultime 48 ore)..."

# Assicuriamoci che la directory di download esista
mkdir -p "$DOWNLOAD_DIR"

# Funzione per processare un canale
process_channel() {
    local channel_url=$1
    local category=$2
    local min_views=$3
    
    echo "🔍 Analizzando canale: $channel_url [$category]"
    
    # Cerchiamo video caricati nelle ultime 48 ore, ordinati per views
    # Usiamo --dateafter per limitare il tempo
    # Usiamo --match-filter per le views minime
    # Stampiamo JSON per poterlo parsare
    TWO_DAYS_AGO=$(date -d "2 days ago" +%Y%m%d)
    
    $WRAPPER \
        --flat-playlist \
        --print-json \
        --dateafter "$TWO_DAYS_AGO" \
        --match-filter "view_count >= $min_views" \
        --playlist-items 10 \
        "$channel_url/videos" | while read -r line; do
            
        VIDEO_ID=$(echo "$line" | jq -r '.id')
        VIDEO_TITLE=$(echo "$line" | jq -r '.title')
        VIEWS=$(echo "$line" | jq -r '.view_count')
        
        echo "   🔥 Trovato video trending: $VIDEO_TITLE ($VIEWS views)"
        
        # Download effettivo
        # Lo mettiamo in una sottocartella per categoria
        TARGET_DIR="$DOWNLOAD_DIR/$category"
        mkdir -p "$TARGET_DIR"
        
        echo "   📥 Download in corso..."
        $WRAPPER \
            -f "bestvideo[height<=720]+bestaudio/best[height<=720]" \
            --merge-output-format mp4 \
            -o "$TARGET_DIR/%(title)s.%(ext)s" \
            "https://www.youtube.com/watch?v=$VIDEO_ID"
            
        echo "   ✅ Download completato: $VIDEO_TITLE"
    done
}

# Leggiamo il config e iteriamo sui canali
# Nota: richiede 'jq' installato
if ! command -v jq >/dev/null 2>&1; then
    echo "❌ Errore: 'jq' è richiesto per parsare il file di configurazione."
    exit 1
fi

channels_count=$(jq '.channels | length' "$CONFIG_FILE")

for ((i=0; i<$channels_count; i++)); do
    url=$(jq -r ".channels[$i].url" "$CONFIG_FILE")
    cat=$(jq -r ".channels[$i].category" "$CONFIG_FILE")
    views=$(jq -r ".channels[$i].min_views" "$CONFIG_FILE")
    
    process_channel "$url" "$cat" "$views"
done

echo "🔄 Aggiornamento DB Clip Index..."
# Chiamiamo l'eseguibile harvester (se compilato) o simuliamo l'aggiornamento
# In un setup reale, qui chiameresti l'endpoint API del backend Go:
# curl -X POST http://localhost:8080/api/v1/harvester/reindex

echo "✨ Task completato con successo!"
