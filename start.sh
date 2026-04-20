#!/bin/bash
export PATH=$PATH:/usr/local/go/bin
# VeloxEditing Backend - Script di avvio
# Sistema semplificato: solo Go Master

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"

echo "🚀 Avvio VeloxEditing Backend..."
echo ""

RUST_BUNDLE="$ROOT_DIR/bin/video-stock-creator.bundle"
RUST_AVAILABLE=false

# Verifica binary Rust
if [ -f "$RUST_BUNDLE" ]; then
    echo "✅ Binary Rust trovato: $RUST_BUNDLE"
    RUST_AVAILABLE=true
else
    echo "⚠️  Binary Rust non trovato: $RUST_BUNDLE"
    echo "   Avvio comunque il Go Master in modalità API-only."
    echo "   Gli endpoint di video processing che dipendono dal bundle Rust"
    echo "   resteranno indisponibili finché il binary non verrà compilato."
fi

# Verifica dipendenze esterne
check_dependencies() {
    local deps=("go" "ffmpeg" "python3" "node" "yt-dlp")
    local missing=()
    
    for dep in "${deps[@]}"; do
        if ! command -v "$dep" &> /dev/null; then
            missing+=("$dep")
        fi
    done
    
    if [ ${#missing[@]} -ne 0 ]; then
        echo "❌ Errore: Dipendenze mancanti: ${missing[*]}"
        echo "   Assicurati di installarle prima di avviare il backend."
        exit 1
    fi
    echo "✅ Tutte le dipendenze esterne (go, ffmpeg, python3, node, yt-dlp) sono presenti."
}

check_dependencies

# Avvia Go Master
PORT="${VELOX_PORT:-8080}"
echo "🎯 Avvio Go Master (porta ${PORT})..."
cd "$ROOT_DIR/src/go-master"

# Usa binario precompilato se esiste, altrimenti compila
if [ -f "$ROOT_DIR/bin/server" ]; then
    echo "   Usando binario precompilato..."
    "$ROOT_DIR/bin/server" &
else
    echo "   Compilazione..."
    (cd "$ROOT_DIR/src/go-master" && (cd "$ROOT_DIR/src/go-master" && go build -o "$ROOT_DIR/bin/server" ./cmd/server))
    "$ROOT_DIR/bin/server" &
fi

MASTER_PID=$!
cd "$ROOT_DIR"

echo ""
echo "✅ Go Master avviato (PID: $MASTER_PID)"
echo ""
echo "📡 Endpoint disponibili:"
echo "   • http://localhost:${PORT}/health"
echo "   • http://localhost:${PORT}/api/health"
echo "   • http://localhost:${PORT}/api/video/create-master"
echo "   • http://localhost:${PORT}/api/jobs/*"
echo "   • http://localhost:${PORT}/api/stock/*"
echo "   • http://localhost:${PORT}/api/clip/*"
echo ""
echo "📚 Documentazione:"
echo "   • README.md"
echo "   • docs/API_ENDPOINTS.md"
echo ""

if [ "$RUST_AVAILABLE" = false ]; then
    echo "⚠️  Modalità API-only attiva:"
    echo "   • Compila il bundle Rust per abilitare gli endpoint video completi"
    echo "   • Percorso atteso: ./bin/video-stock-creator.bundle"
    echo ""
fi

echo "🛑 Per fermare: kill $MASTER_PID"
echo ""

# Attendi interrupt
trap "echo ''; echo '🛑 Arresto...'; kill $MASTER_PID 2>/dev/null || true; exit 0" INT TERM
wait $MASTER_PID
