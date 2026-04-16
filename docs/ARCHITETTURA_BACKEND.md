# 🏗️ Architettura Backend VeloxEditing

> **Documento aggiornato:** 12 Marzo 2026  
> **Stato:** Sistema ibrido Go + Rust + Python legacy

---

## 📊 Overview Architettura

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           VELOX BACKEND                                     │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │                         LAYER 1: GO MASTER                          │   │
│  │                    (API HTTP + Orchestrazione)                      │   │
│  ├─────────────────────────────────────────────────────────────────────┤   │
│  │                                                                     │   │
│  │   ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌───────────┐ │   │
│  │   │   API HTTP  │  │   Job Mgmt  │  │   Worker    │  │  Upload   │ │   │
│  │   │   (Gin)     │  │   Service   │  │   Registry  │  │  Service  │ │   │
│  │   └──────┬──────┘  └──────┬──────┘  └──────┬──────┘  └─────┬─────┘ │   │
│  │          │                │                │               │       │   │
│  │          └────────────────┴────────────────┴───────────────┘       │   │
│  │                              │                                      │   │
│  │                              ▼                                      │   │
│  │   ┌─────────────────────────────────────────────────────────────┐  │   │
│  │   │              Integration Layer (Go)                         │  │   │
│  │   │  • Ollama Client (script generation)                        │  │   │
│  │   │  • EdgeTTS Client (voiceover)                               │  │   │
│  │   │  • Google Drive API                                         │  │   │
│  │   │  • YouTube Data API                                         │  │   │
│  │   └──────────────────────────┬──────────────────────────────────┘  │   │
│  │                              │                                      │   │
│  └──────────────────────────────┼──────────────────────────────────────┘   │
│                                 │                                          │
│                                 ▼                                          │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │                      LAYER 2: RUST BINARY                           │   │
│  │              (video-stock-creator - Processing Core)                │   │
│  ├─────────────────────────────────────────────────────────────────────┤   │
│  │                                                                     │   │
│  │   ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌───────────┐ │   │
│  │   │   FFmpeg    │  │ Transitions │  │   Effects   │  │   Audio   │ │   │
│  │   │   Wrapper   │  │   Engine    │  │   Engine    │  │   Mixer   │ │   │
│  │   └─────────────┘  └─────────────┘  └─────────────┘  └───────────┘ │   │
│  │                                                                     │   │
│  │   Input: JSON config │ Output: Video file MP4                      │   │
│  │                                                                     │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                 ▲                                          │
│                                 │                                          │
│  ┌──────────────────────────────┼──────────────────────────────────────┐   │
│  │                 LAYER 3: GO WORKER (optional)                      │   │
│  │         (Job execution + Rust binary orchestration)                │   │
│  │                                                                    │   │
│  │   ┌─────────────┐  ┌─────────────┐  ┌─────────────┐               │   │
│  │   │   Polling   │  │   Job Exec  │  │   Heartbeat │               │   │
│  │   │   Loop      │  │   Wrapper   │  │   Service   │               │   │
│  │   └─────────────┘  └──────┬──────┘  └─────────────┘               │   │
│  │                           │                                       │   │
│  │                           └──────► Chiama Rust binary             │   │
│  │                                                                    │   │
│  └────────────────────────────────────────────────────────────────────┘   │
│                                                                             │
│  ╔═════════════════════════════════════════════════════════════════════╗   │
│  ║                    LAYER 4: PYTHON LEGACY                           ║   │
│  ║         (Non integrato - codice storico da rimuovere)               ║   │
│  ╠═════════════════════════════════════════════════════════════════════╣   │
│  ║                                                                     ║   │
│  ║   📁 modules/video/          → Sostituito da Rust binary           ║   │
│  ║   📁 modules/youtube_manager/ → Non integrato in Go               ║   │
│  ║   📁 modules/generation/     → Sostituito da Rust + Go            ║   │
│  ║                                                                     ║   │
│  ║   ⚠️  NOTA: Questi file non vengono chiamati dal backend Go       ║   │
│  ║                                                                     ║   │
│  ╚═════════════════════════════════════════════════════════════════════╝   │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 🎯 Divisione Responsabilità per Layer

### **LAYER 1: GO MASTER** (`src/go-master/`)

**Responsabilità:** API HTTP, orchestrazione, state management

| Componente | File | Funzione |
|------------|------|----------|
| HTTP Server | `cmd/server/main.go` | Entry point, bootstrap |
| API Routes | `internal/api/routes.go` | Definizione tutti gli endpoint |
| Job Service | `internal/core/job/service.go` | Gestione job queue |
| Worker Service | `internal/core/worker/service.go` | Registrazione worker, heartbeat |
| Stock Handler | `internal/api/handlers/stock.go` | 8+ endpoint stock processing |
| Clip Handler | `internal/api/handlers/clip.go` | 10+ endpoint clip management |
| Video Handler | `internal/api/handlers/video.go` | Orchestrazione video creation |
| Upload Handler | `internal/api/handlers/drive.go` | Google Drive integration |
| YouTube Handler | `internal/api/handlers/youtube.go` | YouTube API integration |
| Script Handler | `internal/api/handlers/script.go` | Ollama integration |
| Voiceover Handler | `internal/api/handlers/voiceover.go` | EdgeTTS integration |

**Endpoint principali:**
```
POST /api/video/create-master     # Entry point creazione video
POST /api/jobs/create             # Creazione job
GET  /api/workers                 # Lista worker
POST /api/stock/create            # Creazione stock clip
POST /api/clip/suggest            # Suggerimenti clip
POST /api/script/generate         # Generazione script (Ollama)
POST /api/voiceover/generate      # Generazione voiceover (EdgeTTS)
```

---

### **LAYER 2: RUST BINARY** (`video-stock-creator.bundle`)

**Responsabilità:** Processing video effettivo (CPU-intensive)

| Funzione | Implementazione | Input | Output |
|----------|-----------------|-------|--------|
| Video Generation | FFmpeg pipeline | JSON config | File MP4 |
| Transitions | Custom engine | Clip list | Video con transizioni |
| Effects | FFmpeg filters | Video + effects config | Video processato |
| Audio Mix | FFmpeg amix | Background + voiceover | Audio mixato |
| Stock Download | yt-dlp wrapper | YouTube URL | File video locale |

**Come viene chiamato:**
```go
// Go chiama Rust come processo esterno
cmd := exec.Command("video-stock-creator", configPath)
output, err := cmd.CombinedOutput()
```

**Perché Rust:**
- Performance superiori per processing video
- Memory safety
- FFmpeg integration più efficiente

---

### **LAYER 3: GO WORKER** (`go-worker/`)

**Responsabilità:** Esecuzione job distribuita

| Componente | File | Funzione |
|------------|------|----------|
| Worker Main | `cmd/worker/main.go` | Entry point worker |
| Polling | `internal/worker/poller.go` | Polling job dal Master |
| Executor | `internal/worker/executor.go` | Esecuzione job |
| Heartbeat | `internal/worker/heartbeat.go` | Invio status al Master |
| Rust Processor | `internal/worker/rust_processor.go` | Wrapper chiamata Rust |

**Flusso:**
```
1. Worker polla Master → GET /api/workers/jobs
2. Riceve job → Esegue Rust binary
3. Invia heartbeat → POST /api/workers/heartbeat
4. Completa job → POST /api/jobs/{id}/complete
```

---

### **LAYER 4: PYTHON LEGACY** (`modules/`, `scripts/`)

**⚠️ IMPORTANTE:** Questi file **NON sono più integrati** nel backend Go.

| Cartella | Stato | Contenuto |
|----------|-------|-----------|
| `modules/video/` | 🔴 Legacy | Generazione video Python (sostituito da Rust) |
| `modules/generation/` | 🔴 Legacy | Orchestrazione Python (sostituito da Go) |
| `modules/youtube_manager/` | 🟡 Non integrato | Analytics/competitors (standalone) |
| `modules/core/` | 🔴 Legacy | Job/State management (sostituito da Go) |
| `scripts/` | 🟡 Utility | Script di supporto vari |

**Perché esistono ancora:**
- Codice storico per riferimento
- Alcuni script utility ancora utili
- Possibile rimozione futura

---

## 🔄 Flusso Completo: Creazione Video

### **Esempio: POST /api/video/create-master**

```
┌─────────┐     ┌─────────────┐     ┌─────────────────┐     ┌─────────────┐
│ Client  │────▶│ Go Master   │────▶│  Ollama/EdgeTTS │────▶│  Go Worker  │
└─────────┘     │   API       │     │  (Go clients)   │     │  (polling)  │
                └─────────────┘     └─────────────────┘     └──────┬──────┘
                      │                                            │
                      │                                            ▼
                      │                                     ┌─────────────┐
                      │                                     │ Rust Binary │
                      │                                     │ (processing)│
                      │                                     └──────┬──────┘
                      │                                            │
                      ▼                                            ▼
                ┌─────────────┐                              ┌─────────────┐
                │ Google Drive│◄─────────────────────────────│  Output MP4 │
                │  (upload)   │                              │             │
                └─────────────┘                              └─────────────┘
```

### **Step-by-step:**

1. **Client** → `POST /api/video/create-master`
   ```json
   {
     "video_name": "test",
     "youtube_url": "https://youtube.com/watch?v=xxx",
     "duration": 60
   }
   ```

2. **Go Master** → Genera script via Ollama
   - Chiama `internal/ml/ollama/client.go`
   - Genera testo dello script

3. **Go Master** → Genera voiceover via EdgeTTS
   - Chiama `internal/audio/tts/edge.go`
   - Genera file audio MP3

4. **Go Master** → Crea job nella queue
   - Salva in `data/queue.json`
   - Stato: `queued`

5. **Go Worker** → Polla e riceve job
   - `GET /api/workers/jobs`
   - Stato cambia: `running`

6. **Go Worker** → Chiama Rust binary
   - `exec.Command("video-stock-creator", config.json)`
   - Passa configurazione JSON

7. **Rust Binary** → Processa video
   - Download stock clips (yt-dlp)
   - Applica transizioni
   - Mix audio (voiceover + background)
   - Genera output MP4

8. **Go Worker** → Upload su Drive
   - Chiama `internal/upload/drive/client.go`
   - Upload file finale

9. **Go Worker** → Completa job
   - `POST /api/jobs/{id}/complete`
   - Stato: `completed`

---

## 📁 Struttura File per Componente

### **Go Master** (`src/go-master/`)
```
cmd/server/           # Entry point
internal/
  api/handlers/       # HTTP handlers (12 file)
  api/middleware/     # CORS, auth, rate limiting
  core/job/           # Job management
  core/worker/        # Worker registry
  storage/jsondb/     # Persistenza JSON
  storage/sqlite/     # SQLite (futuro)
  video/              # Wrapper Rust
  audio/tts/          # EdgeTTS integration
  ml/ollama/          # Ollama integration
  upload/drive/       # Google Drive API
  upload/youtube/     # YouTube API
  stock/              # Stock video management
  clip/               # Clip management
  youtube/            # YouTube download
pkg/
  models/             # Modelli condivisi
  config/             # Configurazione
  logger/             # Logging
```

### **Go Worker** (`go-worker/`)
```
cmd/worker/           # Entry point
internal/worker/      # Worker logic (polling, heartbeat, executor)
pkg/models/           # Modelli condivisi
```

### **Rust Binary** (`video-stock-creator.bundle`)
```
Binary standalone che include:
- FFmpeg
- yt-dlp
- Logic di processing
```

### **Python Legacy** (`modules/`)
```
modules/video/              # NON USATO - sostituito da Rust
modules/generation/         # NON USATO - sostituito da Go
modules/youtube_manager/    # NON INTEGRATO - standalone
modules/core/               # NON USATO - sostituito da Go
```

---

## 🚀 Come avviare il sistema

### **1. Avvia Go Master**
```bash
cd src/go-master
go run cmd/server/main.go
# oppure
./server
```

Server HTTP su `:8080` (default)

### **2. Avvia Go Worker (opzionale)**
```bash
cd go-worker
go run cmd/worker/main.go
```

Se non ci sono worker, il Master esegue i job in modo sincrono.

### **3. Verifica Rust binary**
```bash
./video-stock-creator.bundle --help
```

Deve essere nel PATH o specificare path assoluto.

---

## 📝 Note Importanti

### **Python vs Go vs Rust**

| Aspetto | Python (Legacy) | Go (Attivo) | Rust (Attivo) |
|---------|-----------------|-------------|---------------|
| API HTTP | ❌ Rimosso | ✅ Gin | N/A |
| Job Management | ❌ Rimosso | ✅ Implementato | N/A |
| Video Processing | ❌ Rimosso | ✅ Wrapper | ✅ Core |
| Script Generation | ❌ Rimosso | ✅ Ollama client | N/A |
| Voiceover | ❌ Rimosso | ✅ EdgeTTS | N/A |
| Upload Drive | ❌ Rimosso | ✅ Implementato | N/A |

### **Cosa è stato migrato:**
- ✅ 100% API endpoints
- ✅ 100% Job/Worker management
- ✅ 100% Upload/integration
- ✅ 100% Video processing (via Rust)

### **Cosa rimane in Python (non usato):**
- 🔴 `modules/video/` - Codice FFmpeg Python (sostituito)
- 🔴 `modules/generation/` - Orchestrazione Python (sostituita)
- 🟡 `modules/youtube_manager/` - Analytics (non integrato)

---

## 🧪 Testing

### **Test Go Master:**
```bash
cd src/go-master
go test ./... -v
```

### **Test E2E:**
```bash
cd src/go-master
go test ./tests/e2e/... -v
```

### **Verifica integrazione Rust:**
```bash
# Test manuale
curl -X POST http://localhost:8080/api/video/create-master \
  -H "Content-Type: application/json" \
  -d '{"video_name":"test","duration":30}'
```

---

## 📞 Supporto

Per domande sull'architettura:
- Documentazione Go: `src/go-master/README.md`
- Documentazione Worker: `go-worker/README.md`
- Documentazione API: `http://localhost:8080/api/docs/index.html` (Swagger)

---

*Documento aggiornato: 12 Marzo 2026*
