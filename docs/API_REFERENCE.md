# PipelineGen API Reference

Questa guida contiene esempi pronti all'uso (callable) per interagire con il backend di PipelineGen da qualsiasi computer nella rete.

## 🛠️ Configurazione Iniziale

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

### 1.2 System Doctor (Stato dettagliato dei moduli)
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
curl -i -X POST http://77.93.152.122:8080/api/voiceover/generate \
  -H "Authorization: Bearer velox_master_key_2026" \
  -H "Content-Type: application/json" \
  -d '{
    "text": "Questo è un test del modulo voiceover di PipelineGen.",
    "language": "it",
    "filename": "test_audio_01.mp3"
  }'
```

---

## 🎬 3. Artlist (Ricerca Clip)

### 3.1 Ricerca Intelligente (Smart Pipeline)
Scarica le clip corrispondenti usando un preset. Restituisce l'ID del Job (vedi sezione Jobs).
```bash
curl -i -X POST http://77.93.152.122:8080/api/artlist/run-smart \
  -H "Authorization: Bearer velox_master_key_2026" \
  -H "Content-Type: application/json" \
  -d '{
    "term": "cyberpunk city",
    "preset": "youtube_1080p_7s",
    "limit": 3
  }'
```

### 3.2 Ricerca Live (Senza scaricare)
Restituisce i metadati direttamente dal sito tramite lo scraper Node.js.
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
curl -i -X POST http://77.93.152.122:8080/api/youtubeclip/extract \
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

## 📝 5. Script & Intelligence (Ollama)

### 5.1 Genera un Documento Script Completo
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
