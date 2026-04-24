# PipelineGen - Struttura Progetto

Panoramica della struttura del progetto.

## 📁 Struttura Principale

```
PipelineGen/
├── README.md                     # Documentazione principale
├── requirements.txt               # Dipendenze Python (Ollama + requests)
│
├── src/                           # 📦 Source Code
│   ├── go-master/                 # 🟢 Go API Server (PRIMARY)
│   │   ├── cmd/server/           # Entry point
│   │   ├── internal/             # Core logic (50+ packages)
│   │   ├── pkg/                  # Shared models, config, logger
│   │   ├── data/                 # JSON database (runtime)
│   │   ├── tests/                # Unit & integration tests
│   │   └── Makefile              # Build & test commands
│   ├── python/                    # 🐍 Ollama text generation
│   │   ├── llm_client.py         # Client Ollama
│   │   ├── script_generation.py  # Generazione script da testo/YouTube
│   │   ├── youtube_transcript.py # Estrazione transcript YouTube
│   │   └── yt_dlp_utils.py       # Utility yt-dlp
│   ├── rust/                      # 🦀 Rust video processing
│   └── node-scraper/             # 🌐 Node.js Artlist scraper
│
├── scripts/                      # 🛠️ Utility scripts
│   └── generate_script.py         # Thin wrapper for the Go script pipeline
├── docs/                          # 📚 Documentazione
├── data/                          # 💾 JSON database + output artifacts
├── assets/                        # 🎨 Audio, fonts, transizioni
├── effects/                       # ✨ Overlay video effects
├── config/                        # ⚙️ Configuration Files
└── tests/                         # 🧪 Test Files
```

## 📦 Moduli Principali

### `src/go-master/` (Go — PRIMARY)
Il backend Go è il componente principale:
- API HTTP (60+ endpoints, Gin framework)
- Job / Worker Management
- Script Generation (Ollama integration)
- Entity Extraction (NLP + Ollama)
- Clip Indexing & Stock Orchestrator
- Channel Monitor (cron, AI folder classification)
- Google Drive / YouTube Upload
- GPU / NVIDIA AI Integration

Vedi [src/go-master/README.md](../src/go-master/README.md)

### `src/python/` (Python — Ollama text generation)
Modulo Python minimale per generazione testi:
- `llm_client.py` — Client Ollama (chiama `/api/generate`)
- `script_generation.py` — Generazione script da testo o YouTube
- `youtube_transcript.py` — Estrazione transcript VTT da YouTube
- `yt_dlp_utils.py` — Utility per costruire comandi yt-dlp

Vedi [src/python/README.md](../src/python/README.md)

## 🚀 Utilizzo

### Import Python (Ollama diretto)

```python
import sys; sys.path.insert(0, 'src')
from python.script_generation import generate_script_from_text, generate_script_from_youtube
```

### API Go (primaria)

Tutte le operazioni passano dall'API Go sulla porta 8080:
```bash
curl http://localhost:8080/health
curl -X POST http://localhost:8080/api/script/generate -d '{...}'
```

## 📝 Convenzioni

- **Go è il backend primario** — tutta la logica business è in Go
- **Python è solo per Ollama** — generazione testi, nessun video/audio processing
- **Config in JSON** — file JSON in `config/` e `data/`
- **Docs in Markdown** — ogni cartella importante ha un README.md

## 🔍 Navigazione

1. **Struttura generale?** → `README_STRUCTURE.md` (questo file)
2. **API endpoints?** → `docs/API_ENDPOINTS.md`
3. **Go backend?** → `src/go-master/README.md`
4. **Python Ollama?** → `src/python/README.md`
5. **Configurazione?** → `config/README.md`
