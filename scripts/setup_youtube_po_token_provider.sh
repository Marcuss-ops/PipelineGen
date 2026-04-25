#!/usr/bin/env bash
set -euo pipefail

# Installa e avvia bgutil-ytdlp-pot-provider per superare i blocchi YouTube 2026.
# Uso:
#   scripts/setup_youtube_po_token_provider.sh
#
# Prerequisiti:
# - Python venv con yt-dlp
# - Node.js + npm installabili

VENV_PATH="${VENV_PATH:-$HOME/venv}"
BGUTIL_DIR="${BGUTIL_DIR:-$HOME/bgutil-ytdlp-pot-provider}"
BGUTIL_PORT="${BGUTIL_PORT:-4416}"
BGUTIL_REF="${BGUTIL_REF:-1.3.1}"

if [[ ! -x "$VENV_PATH/bin/python3" ]]; then
  echo "Errore: venv non trovato in $VENV_PATH"
  exit 1
fi

echo "[1/6] Aggiorno pip packages nel venv..."
"$VENV_PATH/bin/python3" -m pip install -U yt-dlp yt-dlp-get-pot bgutil-ytdlp-pot-provider

echo "[2/6] Verifico Node.js e npm..."
if ! command -v node >/dev/null 2>&1 || ! command -v npm >/dev/null 2>&1; then
  echo "Node.js/npm non trovati. Installa con:"
  echo "  sudo apt-get update && sudo apt-get install -y nodejs npm"
  exit 1
fi

echo "[3/6] Verifico yarn..."
if ! command -v yarn >/dev/null 2>&1; then
  npm install -g yarn
fi

echo "[4/6] Clono/aggiorno repository bgutil..."
if [[ -d "$BGUTIL_DIR/.git" ]]; then
  git -C "$BGUTIL_DIR" fetch --tags origin
  git -C "$BGUTIL_DIR" checkout "$BGUTIL_REF"
  git -C "$BGUTIL_DIR" pull --ff-only origin "$BGUTIL_REF" || true
else
  git clone --branch "$BGUTIL_REF" https://github.com/Brainicism/bgutil-ytdlp-pot-provider.git "$BGUTIL_DIR"
fi

echo "[5/6] Build server bgutil..."
cd "$BGUTIL_DIR/server"
yarn install --frozen-lockfile
npx tsc

echo "[6/6] Avvio server bgutil su 127.0.0.1:${BGUTIL_PORT}..."
nohup node build/main.js --port "$BGUTIL_PORT" > "$BGUTIL_DIR/server/bgutil.log" 2>&1 &
BGUTIL_PID=$!
sleep 1

if ! kill -0 "$BGUTIL_PID" >/dev/null 2>&1; then
  echo "Errore: server bgutil non partito correttamente. Controlla $BGUTIL_DIR/server/bgutil.log"
  exit 1
fi

cat <<EOF
OK: bgutil avviato (pid=$BGUTIL_PID) su 127.0.0.1:${BGUTIL_PORT}

Configura il servizio Go con:
  export VELOX_YTDLP_COOKIES_FILE=/percorso/cookies.txt
  export VELOX_YTDLP_EXTRACTOR_ARGS='youtube:player_client=mweb'
  export VELOX_YTDLP_JS_RUNTIMES='node'
  export VELOX_YTDLP_REMOTE_COMPONENTS='ejs:github'

Test rapido:
  $VENV_PATH/bin/yt-dlp -v \\
    --cookies "\$VELOX_YTDLP_COOKIES_FILE" \\
    --extractor-args "\$VELOX_YTDLP_EXTRACTOR_ARGS" \\
    --js-runtimes "\$VELOX_YTDLP_JS_RUNTIMES" \\
    --remote-components "\$VELOX_YTDLP_REMOTE_COMPONENTS" \\
    "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
EOF
