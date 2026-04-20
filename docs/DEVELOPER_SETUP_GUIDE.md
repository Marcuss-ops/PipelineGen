# 🛠️ VeloxEditing: Developer Setup Guide

Benvenuto nel progetto **VeloxEditing**! Questa guida ti aiuterà a configurare l'intero ecosistema di sviluppo, che comprende componenti in **Go**, **Rust**, **Python** e **Node.js**.

---

## 🏗️ Architettura in Breve

Il sistema è composto da quattro strati principali:
1.  **Go Master (`src/go-master`)**: Orchestratore centrale, API HTTP, gestione Job/Worker e integrazioni (Google Drive, YouTube, Ollama).
2.  **Rust Video Engine (`src/rust`)**: Il motore ad alte prestazioni per il montaggio video (basato su FFmpeg e Remotion).
3.  **Python Helpers (`src/python`)**: Utility per script generation e trascrizioni YouTube.
4.  **Node Scraper (`src/node-scraper`)**: Harvester di contenuti stock tramite Playwright/Puppeteer.

---

## 📋 Requisiti di Sistema

Assicurati di avere installato:
- **Go 1.21+**
- **Rust toolchain** (via `rustup`)
- **Python 3.10+**
- **Node.js 18+**
- **PostgreSQL 14+** (opzionale per dev, obbligatorio per production-like)
- **FFmpeg** (con supporto encoder moderni)
- **yt-dlp** (installato globalmente nel PATH)
- **Ollama** (per la generazione AI locale)

---

## 🚀 Step-by-Step Setup

### 1. Clona il Repository e Inizializza
```bash
git clone <repository-url> velox-editing
cd velox-editing
```

### 2. Configura l'Ambiente Go (Master)
Il Go Master gestisce l'orchestrazione.
```bash
cd src/go-master
# Scarica le dipendenze
go mod download
# Crea un file .env partendo dall'esempio (se presente) o usa variabili d'ambiente
```

### 3. Configura il Motore Rust (Video Engine)
Il motore Rust deve essere compilato per essere usato dal Master.
```bash
cd src/rust
# Esegui lo script di installazione per le dipendenze (FFmpeg, Node, etc.)
chmod +x install.sh
./install.sh
# Compila in modalità release
cargo build --release
```
*Nota: Il binario compilato dovrebbe trovarsi in `bin/video-stock-creator.bundle` (o path configurato nel Master).*

### 4. Configura i Python Helpers
```bash
cd src/python
pip install -r requirements.txt
```

### 5. Configura il Node Scraper
```bash
cd src/node-scraper
npm install
cp .env.example .env # Configura le chiavi necessarie
```

### 6. Configura Ollama (AI locale)
Il sistema usa Ollama per generare script. Assicurati che sia in esecuzione e scarica il modello:
```bash
ollama run gemma3:4b
```

---

## ⚙️ Variabili d'Ambiente e Persistenza

Il sistema può funzionare in modalità **JSON** (per sviluppo veloce) o **Postgres** (per produzione/test robusti).

### Modalità Sviluppo (JSON)
Default se non configurato diversamente. I dati vengono salvati in `data/*.json`.

### Modalità Produzione (Postgres)
Imposta le seguenti variabili nel tuo terminale o nel file `.env`:
```bash
export VELOX_DB_DSN="postgres://user:pass@localhost:5432/velox?sslmode=disable"
export VELOX_STORAGE_BACKEND="postgres"
export VELOX_QUEUE_BACKEND="postgres"
```

### Altre Variabili Chiave
| Variabile | Descrizione |
|-----------|-------------|
| `OLLAMA_ADDR` | URL del server Ollama (default: `http://localhost:11434`) |
| `VELOX_PORT` | Porta del Master Go (default: `8080`) |
| `VELOX_DATA_DIR` | Directory per i file temporanei e cache (default: `./data`) |

---

## 🧪 Testare l'Installazione

### Test Go Master
```bash
cd src/go-master
make test
```

### Test Integrazione (E2E)
Puoi avviare il sistema completo e verificare che risponda:
```bash
# Nella root del progetto
./start.sh
# Verifica l'health check
curl http://localhost:8080/health
```

---

## 📚 Documentazione Correlata

- [ARCHITETTURA_BACKEND.md](./ARCHITETTURA_BACKEND.md): Dettagli tecnici profondi.
- [API_DOCUMENTATION.md](./API_DOCUMENTATION.md): Tutti gli endpoint disponibili.
- [YOUTUBE_CLIENT_GPU_GUIDE.md](./YOUTUBE_CLIENT_GPU_GUIDE.md): Info su accelerazione GPU e yt-dlp.
- [GEMINI.md](../GEMINI.md): Mandati core e convenzioni di sviluppo per l'agente AI.

---

*Ultimo aggiornamento: Aprile 2026*
