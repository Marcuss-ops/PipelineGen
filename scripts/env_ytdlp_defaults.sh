#!/usr/bin/env bash
# Shared yt-dlp defaults for YouTube anti-bot flow (2026).
set -euo pipefail

if [[ -z "${VELOX_YTDLP_COOKIES_FILE:-}" ]]; then
  if [[ -f "/home/pierone/Downloads/coo1kies.txt" ]]; then
    export VELOX_YTDLP_COOKIES_FILE="/home/pierone/Downloads/coo1kies.txt"
  elif [[ -f "/home/pierone/Downloads/cookies.txt" ]]; then
    export VELOX_YTDLP_COOKIES_FILE="/home/pierone/Downloads/cookies.txt"
  fi
fi

export VELOX_YTDLP_EXTRACTOR_ARGS="${VELOX_YTDLP_EXTRACTOR_ARGS:-youtube:player_client=mweb}"
export VELOX_YTDLP_JS_RUNTIMES="${VELOX_YTDLP_JS_RUNTIMES:-node}"
export VELOX_YTDLP_REMOTE_COMPONENTS="${VELOX_YTDLP_REMOTE_COMPONENTS:-ejs:github}"
