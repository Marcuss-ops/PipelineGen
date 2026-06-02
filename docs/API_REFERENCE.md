# PipelineGen API Reference

This guide contains ready-to-use examples for interacting with the PipelineGen backend from any computer on the network.

## 🛠️ Initial Setup

Tutte le chiamate richiedono l'autenticazione tramite Bearer Token.
**Token attivo:** `velox_master_key_2026`
**Indirizzo Server:** Sostituisci `77.93.152.122` con l'IP effettivo del server se dovesse cambiare.

---

## 🟢 1. Health & System

### 1.1 Controlla se il server è online
```bash
curl -i http://77.93.152.122:8080/api/health \
  -H "Authorization: Bearer velox_master_key_2026"
```

### 1.2 System Doctor (Module status details)
```bash
curl -i http://77.93.152.122:8080/api/system/doctor \
  -H "Authorization: Bearer velox_master_key_2026"
```

### 1.3 Avvia Manutenzione Manuale (Log Pruning & Orphan Cleanup)
```bash
curl -i -X POST "http://77.93.152.122:8080/api/system/cleanup?deep=true" \
  -H "Authorization: Bearer velox_master_key_2026"
```

---

## 🎙️ 2. Voiceover (Sintesi Vocale)

### 2.1 Genera un singolo Voiceover
```bash
curl -i -X POST http://77.93.152.122:8080/api/media/voiceover/generate \
  -H "Authorization: Bearer velox_master_key_2026" \
  -H "Content-Type: application/json" \
  -d '{
    "text": "Questo è un test del modulo voiceover di PipelineGen.",
    "language": "it",
    "filename": "test_audio_01.mp3"
  }'
```

---

## 🎬 3. Artlist (Clip Search)

### 3.1 Smart Search (Smart Pipeline)
Downloads matching clips using a preset. Returns the Job ID (see Jobs section).
```bash
curl -i -X POST http://77.93.152.122:8080/api/artlist/run-smart \
  -H "Authorization: Bearer velox_...026" \
  -H "Content-Type: application/json" \
  -d '{
    "term": "cyberpunk city",
    "preset": "youtube_1080p_7s",
    "limit": 3
  }'
```

### 3.2 Live Search (No download)
Returns metadata directly from the website via Node.js scraper.
```bash
curl -i -X POST "http://77.93.152.122:8080/api/artlist/search/live?term=nature&limit=5" \
  -H "Authorization: Bearer velox_master_key_2026" \
  -H "X-Internal: true"
```

---

## 📺 4. YouTube (Estrazione Clip)

### 4.1 Estrai una clip da un video YouTube
Invia un job per scaricare una specifica porzione di un video YouTube.
```bash
curl -i -X POST http://77.93.152.122:8080/api/clips/process \
  -H "Authorization: Bearer velox_master_key_2026" \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
    "start_time": "00:00:15",
    "duration": 10,
    "subject": "estrazione di prova"
  }'
```

---

## 📝 5. Script Generation (Ollama)

### 5.1 Genera Script Testuale (solo testo)
Genera uno script narrativo con metadata YouTube (descrizione, tags, titoli tradotti) per tutte le lingue richieste.
```bash
curl -i -X POST http://77.93.152.122:8080/api/script/generate \
  -H "Authorization: Bearer velox_master_key_2026" \
  -H "Content-Type: application/json" \
  -d '{
    "topic": "Come vivere una vita semplice e prospera",
    "title": "Vita Semplice",
    "language": "it",
    "tone": "documentary",
    "duration": 90,
    "languages": ["en", "es", "fr", "de"]
  }'
```

**Response include metadata per ogni lingua:**
```json
{
  "ok": true,
  "topic": "Come vivere una vita semplice e prospera",
  "title": "Vita Semplice",
  "script": "Testo dello script generato...",
  "word_count": 150,
  "est_duration": 90,
  "metadata": [
    {
      "language": "en",
      "title": "Simple Life",
      "description": "Discover how to live a simple and prosperous life...",
      "tags": ["lifestyle", "motivation", "success"]
    },
    {
      "language": "it",
      "title": "Vita Semplice",
      "description": "Scopri come vivere una vita semplice e prospera...",
      "tags": ["stile di vita", "motivazione", "successo"]
    }
  ]
}
```

### 5.2 Genera Script con Immagini (async)
Genera script riscritto + immagini per ogni scena + voiceover unificato + traduzioni. Ritorna un job_id per tracciare il progresso.
```bash
curl -i -X POST http://77.93.152.122:8080/api/script/generate-with-images \
  -H "Authorization: Bearer velox_master_key_2026" \
  -H "Content-Type: application/json" \
  -d '{
    "source_text": "Testo o articolo da trasformare in video explicativo...",
    "title": "Vita Semplice",
    "language": "en",
    "languages": ["it", "es", "fr"],
    "style": "documentary",
    "scene_count": 8,
    "images_per_scene": 1,
    "width": 1344,
    "height": 768
  }'
```

**Risposta iniziale (job enqueued):**
```json
{
  "ok": true,
  "job_id": "job_xxx",
  "status": "queued"
}
```

**Risultato finale (via `/api/script/jobs/:job_id`):**
```json
{
  "ok": true,
  "job_id": "job_xxx",
  "status": "completed",
  "progress": 100,
  "result": {
    "output_dir": "/path/to/output",
    "doc_id": "google-doc-id",
    "doc_url": "https://docs.google.com/...",
    "word_count": 450,
    "est_duration": 180,
    "scenes_count": 8,
    "metadata": [
      {"language": "en", "title": "...", "description": "...", "tags": [...]},
      {"language": "it", "title": "...", "description": "...", "tags": [...]}
    ]
  }
}
```

### 5.3 Status Job Script Generation
```bash
curl -i http://77.93.152.122:8080/api/script/jobs/JOB_ID \
  -H "Authorization: Bearer velox_master_key_2026"
```

### 5.4 Genera Documento Script Completo (vecchio endpoint)
```bash
curl -i -X POST http://77.93.152.122:8080/api/scriptdocs/generate \
  -H "Authorization: Bearer velox_master_key_2026" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Il mistero delle piramidi",
    "style": "documentary",
    "lang": "it",
    "upload_to_drive": false
  }'
```

---

## 🖼️ 6. AI Images (NVIDIA NIM & Gestione Immagini)

### 6.1 Genera un'immagine AI (Richiede NVIDIA_API_KEY nel config)
```bash
curl -i -X POST http://77.93.152.122:8080/api/images/generate/nvidia \
  -H "Authorization: Bearer velox_master_key_2026" \
  -H "Content-Type: application/json" \
  -d '{
    "prompt": "A futuristic server room glowing with neon lights, highly detailed, 8k",
    "width": 1024,
    "height": 1024
  }'
```

### 6.2 Anima un'immagine (Zoom out di 7 secondi)
*Sostituisci `HASH_IMMAGINE` con l'hash restituito dalla generazione o ricerca.*
```bash
curl -i -X POST http://77.93.152.122:8080/api/images/animate \
  -H "Authorization: Bearer velox_master_key_2026" \
  -H "Content-Type: application/json" \
  -d '{
    "image_hash": "HASH_IMMAGINE_QUI",
    "duration": 7
  }'
```

---

## ⚙️ 7. Jobs (Monitoraggio Attività Asincrone)

### 7.1 Lista tutti i Jobs recenti
```bash
curl -i http://77.93.152.122:8080/api/jobs \
  -H "Authorization: Bearer velox_master_key_2026"
```

### 7.2 Dettagli di un Job specifico
*Sostituisci `JOB_ID` con l'ID reale (es. `job-12345`).*
```bash
curl -i http://77.93.152.122:8080/api/jobs/JOB_ID/full \
  -H "Authorization: Bearer velox_master_key_2026"
```
