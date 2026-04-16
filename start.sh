#!/bin/bash
# VeloxEditing Backend - Script di avvio
# Sistema semplificato: solo Go Master

set -e

echo "🚀 Avvio VeloxEditing Backend..."
echo ""

# Verifica binary Rust
if [ ! -f "./bin/video-stock-creator.bundle" ]; then
    echo "❌ Errore: bin/video-stock-creator.bundle non trovato!"
    echo "   Il binary Rust deve essere nella directory bin/"
    exit 1
fi

echo "✅ Binary Rust trovato"

# Verifica Go
if ! command -v go &> /dev/null; then
    echo "❌ Errore: Go non installato!"
    exit 1
fi

echo "✅ Go installato: $(go version)"
echo ""

# Avvia Go Master
echo "🎯 Avvio Go Master (porta 8080)..."
cd src/go-master

# Usa binario precompilato se esiste, altrimenti compila
if [ -f "../../bin/server" ]; then
    echo "   Usando binario precompilato..."
    ../../bin/server &
else
    echo "   Compilazione..."
    go build -o ../../bin/server ./cmd/server
    ../../bin/server &
fi

MASTER_PID=$!
cd ../..

echo ""
echo "✅ Go Master avviato (PID: $MASTER_PID)"
echo ""
echo "📡 Endpoint disponibili:"
echo "   • http://localhost:8080/health"
echo "   • http://localhost:8080/api/video/create-master"
echo "   • http://localhost:8080/api/jobs/*"
echo "   • http://localhost:8080/api/stock/*"
echo "   • http://localhost:8080/api/clip/*"
echo ""
echo "📚 Documentazione:"
echo "   • README.md"
echo "   • ENDPOINT_ATTIVI.md"
echo ""
echo "🛑 Per fermare: kill $MASTER_PID"
echo ""

# Attendi interrupt
trap "echo ''; echo '🛑 Arresto...'; kill $MASTER_PID 2>/dev/null || true; exit 0" INT TERM
wait $MASTER_PID
