#!/usr/bin/env bash
# Configurazione ROBUSTA per yt-dlp (2026)
set -euo pipefail

# 1. Gestione Cookie (Fondamentale per evitare bot-check)
if [[ -z "${VELOX_YTDLP_COOKIES_FILE:-}" ]]; then
  if [[ -f "./cookies.txt" ]]; then
    export VELOX_YTDLP_COOKIES_FILE="./cookies.txt"
  elif [[ -f "$HOME/cookies.txt" ]]; then
    export VELOX_YTDLP_COOKIES_FILE="$HOME/cookies.txt"
  fi
fi

# 2. Configurazione Client e Anti-Bot
# Proviamo una combinazione di client per massimizzare le probabilità di successo
export VELOX_YTDLP_EXTRACTOR_ARGS="${VELOX_YTDLP_EXTRACTOR_ARGS:-youtube:player_client=mweb,android;pot_client=web}"

# 3. JS Runtime (Indispensabile per n-challenge)
# Se node è installato, lo forziamo come runtime principale
if command -v node >/dev/null 2>&1; then
    export VELOX_YTDLP_JS_RUNTIMES="node"
else
    export VELOX_YTDLP_JS_RUNTIMES="javascript"
fi

# 4. Remote Components (Per aggiornamenti dinamici degli estrattori)
export VELOX_YTDLP_REMOTE_COMPONENTS="ejs:github"

# 5. PO Token Server (Verifica se attivo, altrimenti non passiamo l'argomento per evitare errori)
POT_SERVER_URL="http://127.0.0.1:4416/ping"
if curl -s --max-time 1 "$POT_SERVER_URL" >/dev/null 2>&1; then
    # Il server è attivo, lo usiamo come sorgente per i POT
    export VELOX_YTDLP_EXTRACTOR_ARGS="${VELOX_YTDLP_EXTRACTOR_ARGS};pot_provider=bgutil"
    echo "DEBUG: POT Server detected and active."
else
    echo "DEBUG: POT Server not reachable. Using fallback strategy (standard extraction)."
fi

# Esportiamo le variabili per yt-dlp
# Usiamo alias o funzioni per rendere il comando yt-dlp sempre "sicuro"
function yt-dlp-velox() {
    local args=()
    if [[ -n "${VELOX_YTDLP_COOKIES_FILE:-}" && -f "$VELOX_YTDLP_COOKIES_FILE" ]]; then
        args+=(--cookies "$VELOX_YTDLP_COOKIES_FILE")
    fi
    
    yt-dlp \
        "${args[@]}" \
        --extractor-args "$VELOX_YTDLP_EXTRACTOR_ARGS" \
        --js-runtimes "$VELOX_YTDLP_JS_RUNTIMES" \
        --remote-components "$VELOX_YTDLP_REMOTE_COMPONENTS" \
        --no-check-certificate \
        --prefer-free-formats \
        "$@"
}
export -f yt-dlp-velox
