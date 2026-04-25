#!/usr/bin/env bash
# Wrapper universale per yt-dlp con fallback automatico
DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
source "$DIR/env_ytdlp_defaults.sh"

# Eseguiamo il comando usando la nostra funzione robusta
yt-dlp-velox "$@"
