# 📡 Endpoint Attivi - Mappa Completa

> **Data:** 12 Marzo 2026  
> **Stato:** Endpoint Go (primari) + Endpoint Python (secondari/utility)

---

## 🎯 Overview

Il sistema ha **due strati di endpoint** attivi:

| Strato | Tecnologia | Porta | Scopo |
|--------|------------|-------|-------|
| **Primario** | Go (Gin) | 8080 | API core, job management, video creation, clip indexing |
| **Utility** | Python | Varie | Tracing, YouTube Manager, altri servizi |

---

## 🟢 STRATO 1: GO MASTER (Porta 8080) - PRIMARIO

> **Nota:** La porta corretta è **8080**, non 8000. Se vedi 8000, aggiorna la configurazione.

**Tutti i endpoint principali sono qui.** Questo è il backend principale.

### Video Creation
| Metodo | Endpoint | Descrizione |
|--------|----------|-------------|
| POST | `/api/video/create-master` | Entry point principale creazione video |
| POST | `/api/video/generate` | Genera video via Rust |
| POST | `/api/video/process` | Processa video esistente |
| POST | `/api/video/effects` | Applica effetti |
| POST | `/api/video/audio/mix` | Mix audio tracks |
| POST | `/api/video/audio/voiceover` | Genera voiceover |
| GET | `/api/video/status/:id` | Stato processing |
| GET | `/api/video/binary/info` | Info binary Rust |

### Job Management
| Metodo | Endpoint | Descrizione |
|--------|----------|-------------|
| POST | `/api/jobs/create` | Crea nuovo job |
| GET | `/api/jobs/:id` | Ottieni job |
| GET | `/api/jobs` | Lista jobs |
| POST | `/api/jobs/:id/cancel` | Cancella job |
| POST | `/api/jobs/:id/complete` | Completa job (worker) |

### Worker Management
| Metodo | Endpoint | Descrizione |
|--------|----------|-------------|
| GET | `/api/workers` | Lista workers |
| POST | `/api/workers/register` | Registra worker |
| POST | `/api/workers/unregister` | Deregistra worker |
| POST | `/api/workers/heartbeat` | Heartbeat worker |
| GET | `/api/workers/jobs` | Job disponibili (polling) |

### Clip & Stock
| Metodo | Endpoint | Descrizione |
|--------|----------|-------------|
| POST | `/api/stock/create` | Crea stock clip |
| POST | `/api/stock/batch-create` | Batch creation |
| POST | `/api/stock/create-studio` | Crea studio |
| POST | `/api/stock/find-and-create` | Cerca e crea |
| POST | `/api/stock/process` | Processa stock |
| POST | `/api/stock/process-simple` | Processo semplificato |
| GET | `/api/stock/projects` | Lista progetti |
| POST | `/api/clip/search-folders` | Cerca cartelle |
| POST | `/api/clip/read-folder-clips` | Leggi clip cartella |
| POST | `/api/clip/suggest` | Suggerimenti clip |
| POST | `/api/clip/create-subfolder` | Crea subfolder |
| GET | `/api/clip/subfolders` | Lista subfolders |
| POST | `/api/clip/download` | Download clip |
| POST | `/api/clip/upload` | Upload clip |

### Clip Indexing & Semantic Suggestions ⭐ NUOVO
| Metodo | Endpoint | Descrizione |
|--------|----------|-------------|
| POST | `/api/clip/index/scan` | Scansiona Drive e ricostruisce indice |
| GET | `/api/clip/index/stats` | Statistiche indice |
| GET | `/api/clip/index/status` | Status indexer |
| DELETE | `/api/clip/index/clear` | Cancella indice clip |
| POST | `/api/clip/index/search` | Ricerca clip con filtri |
| GET | `/api/clip/index/clips` | Lista tutte le clip indicizzate |
| GET | `/api/clip/index/clips/:id` | Ottieni clip specifica per ID |
| POST | `/api/clip/index/suggest/sentence` | ⭐ Suggerimenti per frase |
| POST | `/api/clip/index/suggest/script` | ⭐⭐ Suggerimenti per script intero |

### Script & Voiceover
| Metodo | Endpoint | Descrizione |
|--------|----------|-------------|
| POST | `/api/script/generate` | Genera script (Ollama) |
| POST | `/api/script/generate-from-youtube` | Genera da YouTube |
| POST | `/api/script/regenerate` | Rigenera script |
| POST | `/api/voiceover/generate` | Genera voiceover |
| GET | `/api/voiceover/languages` | Lista lingue |

### Upload & Drive
| Metodo | Endpoint | Descrizione |
|--------|----------|-------------|
| POST | `/api/drive/upload` | Upload su Drive |
| POST | `/api/drive/folder` | Crea cartella |
| GET | `/api/drive/folders` | Lista cartelle |
| POST | `/api/youtube/upload` | Upload su YouTube |
| GET | `/api/youtube/status/:id` | Stato upload |

### YouTube Tools
| Metodo | Endpoint | Descrizione |
|--------|----------|-------------|
| POST | `/api/youtube/search` | Ricerca video |
| POST | `/api/youtube/remote/search` | Ricerca remota |
| POST | `/api/youtube/remote/channel-videos` | Video canale |
| POST | `/api/youtube/remote/video-info` | Info video |
| POST | `/api/youtube/remote/thumbnail` | Download thumbnail |
| POST | `/api/youtube/remote/channel-analytics` | Analytics canale |
| POST | `/api/youtube/remote/related-videos` | Video correlati |
| GET | `/api/youtube/subtitles` | Sottotitoli |

### Dashboard & Admin
| Metodo | Endpoint | Descrizione |
|--------|----------|-------------|
| GET | `/api/dashboard` | Dashboard stats |
| GET | `/api/metrics` | Metriche Prometheus |
| GET | `/api/logs` | Logs sistema |
| GET | `/api/stats` | Statistiche |
| GET | `/api/admin/workers` | Gestione workers |
| POST | `/api/admin/workers/:id/command` | Invia comando |
| GET | `/api/health` | Health check |
| GET | `/api/docs/*` | Swagger UI |

---

## 🟡 STRATO 2: PYTHON UTILITY (Secondari)

### 1. Tracing Dashboard (`scripts/tracing_dashboard.py`)
**Porta:** 5555 (default)  
**Tecnologia:** Flask  
**Scopo:** Monitoraggio e tracing API

| Metodo | Endpoint | Descrizione |
|--------|----------|-------------|
| GET | `/` | Dashboard web UI |
| GET | `/api/tracing/logs` | API log completo |
| GET | `/api/tracing/requests` | Richieste raggruppate |
| GET | `/api/tracing/request/<id>` | Dettaglio richiesta |
| GET | `/api/tracing/stats` | Statistiche |
| GET | `/api/tracing/stream` | SSE real-time stream |
| POST | `/api/tracing/clear` | Pulisci log |
| GET | `/api/tracing/errors` | Solo errori |
| GET | `/api/tracing/processing` | Job in elaborazione |
| GET | `/api/tracing/queue` | Stato coda |
| GET | `/api/tracing/health/detailed` | Health check dettagliato |
| GET | `/api/tracing/export` | Esporta log |
| ALL | `/api/worker/forward/<endpoint>` | Proxy a worker:5000 |

**Avvio:**
```bash
python scripts/tracing_dashboard.py
# o
TRACING_PORT=5555 python scripts/tracing_dashboard.py
```

---

### 2. YouTube Manager (`modules/youtube_manager/routes.py`)
**Porta:** Integrato in altro server (non standalone)  
**Tecnologia:** FastAPI  
**Scopo:** Gestione canali YouTube, analytics, ricerca viral

| Metodo | Endpoint | Descrizione |
|--------|----------|-------------|
| GET | `/youtube_manager` | UI Manager |
| GET | `/api/youtube/manager/groups` | Lista gruppi |
| POST | `/api/youtube/manager/groups` | Crea gruppo |
| DELETE | `/api/youtube/manager/groups/<name>` | Elimina gruppo |
| POST | `/api/youtube/manager/groups/<name>/channels` | Aggiungi canale |
| DELETE | `/api/youtube/manager/groups/<name>/channels/<id>` | Rimuovi canale |
| POST | `/api/youtube/manager/tools/scrape` | Scrape metadata video |
| POST | `/api/youtube/manager/tools/viral` | Ricerca video viral |
| POST | `/api/youtube/manager/tools/similar` | Canali simili |
| POST | `/api/youtube/manager/tools/find_channel` | Reverse image search |
| GET | `/api/youtube/manager/download_thumbnail` | Download thumbnail |
| POST | `/api/youtube/manager/groups/<name>/channels/<id>/stats` | Aggiorna stats |

**Nota:** Questi endpoint richiedono integrazione manuale in un server FastAPI. Non sono esposti di default.

---

### 3. Dark Editor Standalone (`scripts/run_standalone_editor.py`)
**Porta:** 8081  
**Tecnologia:** FastAPI + Uvicorn  
**Scopo:** Editor video standalone

| Metodo | Endpoint | Descrizione |
|--------|----------|-------------|
| GET | `/` | Redirect a dark_editor |
| ALL | `/dark_editor/*` | Dark Editor UI |

**Avvio:**
```bash
python scripts/run_standalone_editor.py
```

---

### 4. Job Master Server Python (Legacy) (`archive/python-cleanup-*/job_master_server.py`)
**Porta:** 8000  
**Tecnologia:** FastAPI  
**Stato:** ⚠️ ARCHIVIATO - Sostituito da Go  
**Nota:** Presente in `archive/`, non usato in produzione.

---

## 📊 Mappa Confronto Endpoint

### Endpoint Go (ATTIVI - Produzione)
```
✅ Tutti i endpoint /api/jobs/*
✅ Tutti i endpoint /api/workers/*
✅ Tutti i endpoint /api/stock/*
✅ Tutti i endpoint /api/clip/*
✅ Tutti i endpoint /api/video/*
✅ Tutti i endpoint /api/script/*
✅ Tutti i endpoint /api/voiceover/*
✅ Tutti i endpoint /api/drive/*
✅ Tutti i endpoint /api/youtube/* (search, upload, status)
✅ /api/dashboard
✅ /api/metrics
✅ /api/health
```

### Endpoint Python (ATTIVI - Utility)
```
🟡 /api/tracing/* (porta 5555) - Monitoring
🟡 /api/youtube/manager/* - YouTube Manager (non integrato in Go)
🟡 /dark_editor/* (porta 8081) - Editor standalone
```

### Endpoint Python (DEPRECATI - Non usati)
```
❌ /api/stock/* (Python) - Sostituito da Go
❌ /api/clip/* (Python) - Sostituito da Go
❌ /api/jobs/* (Python) - Sostituito da Go
❌ /api/workers/* (Python) - Sostituito da Go
```

---

## 🔌 Integrazione tra Go e Python

### Go chiama Python?
**NO** - Go non chiama endpoint Python. Sono servizi indipendenti.

### Python chiama Go?
**SÌ** - Alcuni servizi Python chiamano Go:

```python
# scripts/tracing_dashboard.py
# Proxy verso worker Go
@app.route('/api/worker/forward/<path:endpoint>')
def api_worker_forward(endpoint):
    worker_url = f"http://localhost:5000/{endpoint}"  # Go Worker
    # ... forward request ...

# Health check verso Go Worker
resp = requests.get('http://localhost:5000/health', timeout=5)
```

---

## 🚀 Avvio Completo Sistema

### 1. Go Master (Obbligatorio)
```bash
cd go-master
go run cmd/server/main.go
# Porta 8080
```

### 2. Go Worker (Opzionale ma consigliato)
```bash
cd go-worker
go run cmd/worker/main.go
# Porta 5000
```

### 3. Tracing Dashboard (Opzionale)
```bash
python scripts/tracing_dashboard.py
# Porta 5555
```

### 4. YouTube Manager (Opzionale)
```bash
# Richiede integrazione in server FastAPI esistente
# o avvio standalone con router FastAPI
```

### 5. Dark Editor (Opzionale)
```bash
python scripts/run_standalone_editor.py
# Porta 8081
```

---

## ⚠️ Note Importanti

### Quali endpoint Python sono VERAMENTE necessari?

| Servizio | Necessità | Note |
|----------|-----------|------|
| **Tracing Dashboard** | Media | Utile per monitoring, ma opzionale |
| **YouTube Manager** | Bassa | Feature avanzate, non core |
| **Dark Editor** | Media | Per editing manuale video |

### Endpoint Go coprono il 100% del flusso core:
- ✅ Creazione video (`/api/video/create-master`)
- ✅ Job management (`/api/jobs/*`)
- ✅ Worker management (`/api/workers/*`)
- ✅ Stock/Clip (`/api/stock/*`, `/api/clip/*`)
- ✅ Script/Voiceover (`/api/script/*`, `/api/voiceover/*`)
- ✅ Upload (`/api/drive/*`, `/api/youtube/upload`)

### Endpoint Python sono EXTRA:
- 🟡 Monitoring/tracing
- 🟡 YouTube manager (analytics, gruppi)
- 🟡 Dark Editor (UI editing)

---

## 📝 Esempi di Chiamate

### Go Master (Primario)
```bash
# Creazione video
curl -X POST http://localhost:8080/api/video/create-master \
  -H "Content-Type: application/json" \
  -d '{"video_name":"test","duration":30}'

# Lista workers
curl http://localhost:8080/api/workers
```

### Tracing Dashboard (Python)
```bash
# Stats
curl http://localhost:5555/api/tracing/stats

# Log recenti
curl http://localhost:5555/api/tracing/logs?limit=100
```

---

*Documento aggiornato: 12 Marzo 2026*
