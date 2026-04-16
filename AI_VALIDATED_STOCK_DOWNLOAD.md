# 🎬 AI-Validated YouTube Stock Download Pipeline

**Data:** April 13, 2026  
**Funzionalità:** Download automatico di stock da YouTube con validazione AI prima del download

---

## 🔄 Come Funziona il Pipeline Completo

### Pipeline: Script + Clips con AI Validation

```
1. Genera script da source text (Ollama)
2. Estrai entities da ogni segmento (Ollama)
3. Per ogni entity:
   a. Cerca su YouTube (yt-dlp)
   b. 🤖 INVIA I LINK A GEMMA per validazione
   c. Scarica SOLO i link approvati da AI
   d. Upload a Drive in Stock/{TopicName}/
4. Ritorna mapping completo: script → segments → entities → clips (con Drive links)
```

---

## 🤖 AI Link Validation - Dettagli

### Prompt inviato a Gemma

```
You are an expert video curator for a documentary about {entityName}.

I have found these YouTube videos that might be used as stock footage.
Please evaluate each one and tell me which are RELEVANT and USEFUL.

YOUTUBE VIDEOS:
1. Title: "XXXTentacion Full Interview 2017"
   URL: https://youtube.com/watch?v=ABC123
   Duration: 1800s

2. Title: "Random unrelated video"
   URL: https://youtube.com/watch?v=XYZ789
   Duration: 30s

For each video:
- APPROVED: relevant (interviews, biographical, news, performance)
- REJECTED: not relevant (unrelated, spam, wrong topic)

Respond with ONLY JSON:
{
  "approved": [1, 3],
  "rejected": [2, 4],
  "reasoning": "Video 1 is a full interview..."
}
```

### Criteri di Validazione

Gemma valuta:
- ✅ **Rilevanza al topic** - Interviste, documentari, news reports
- ✅ **Qualità video** - Video più lunghi sono preferiti (più materiale per stock)
- ✅ **Tipo contenuto** - Interviste > Performance > News > Altro
- ❌ **Rifiuta** - Spam, contenuto non correlato, qualità troppo bassa

---

## 🚀 Come Usare

### Endpoint: `POST /api/script/generate-with-clips`

```bash
curl -X POST http://localhost:8080/api/script/generate-with-clips \
  -H "Content-Type: application/json" \
  -d '{
    "title": "XXXTentacion",
    "source_text": "Jahseh Dwayne Ricardo Onfroy, known as XXXTentacion...",
    "language": "italian",
    "duration": 1200,
    "entity_count_per_segment": 8
  }'
```

### Cosa Succede Dietro le Quinte

1. **Script Generation** (~30-60s)
   - Ollama genera script dettagliato (~1300+ parole)

2. **Entity Extraction** (~15-30s)
   - Estrae: Nomi Speciali, Frasi Importanti, Parole Importanti
   - Esempio entities per XXXTentacion:
     - `XXXTentacion`, `Florida`, `SoundCloud`, `Look At Me`, `SAD!`

3. **YouTube Search + AI Validation** (~20-40s per entity)
   ```
   Entity: "XXXTentacion"
   ├─ YouTube Search: 10 risultati trovati
   ├─ 🤖 Ollama Validation:
   │   ├─ ✅ APPROVED: [1, 3, 5, 7] (interviste, documentari)
   │   └─ ❌ REJECTED: [2, 4, 6, 8, 9, 10] (fan edits, shorts)
   └─ Download: Solo i 4 approvati
   ```

4. **Download + Drive Upload** (~30-60s per clip)
   ```
   Download: yt-dlp → /tmp/velox/downloads/clip_*.mp4
   Upload:   Drive → Stock/XXXTentacion/clip_XXXTentacion_*.mp4
   ```

5. **Risultato Finale**
   ```json
   {
     "ok": true,
     "script": "...",
     "word_count": 1319,
     "segments": [
       {
         "segment_index": 0,
         "text": "XXXTentacion è nato in Florida...",
         "start_time": "00:00:00",
         "end_time": "00:00:20",
         "entities": {
           "nomi_speciali": ["XXXTentacion", "Florida"],
           "parole_importanti": ["rap", "hip-hop"]
         },
         "clip_mappings": [
           {
             "entity": "XXXTentacion",
             "search_query_en": "XXXTentacion",
             "clip_found": true,
             "clip_status": "downloaded_and_uploaded",
             "youtube_url": "https://youtube.com/watch?v=ABC123",
             "drive_url": "https://drive.google.com/file/d/...",
             "ai_validated": true,
             "ai_reasoning": "Full interview, highly relevant"
           }
         ]
       }
     ],
     "total_clips_found": 12,
     "total_clips_missing": 2,
     "processing_time": 180.5
   }
   ```

---

## ⚙️ Configurazione

### Parametri del Servizio

Nel file `cmd/server/main.go`:

```go
scriptClipsService = scriptclips.NewScriptClipsService(
    scriptGen,
    entityService,
    stockMgr,
    driveClient,
    cfg.GetDownloadDir(),
    "",                    // Drive folder ID (default: crea automaticamente)
    "",                    // Topic (default: usa il title dalla request)
    20,                    // stockFallbackCount: max 20 clip per entity
    ollamaClient,          // Client Ollama per validazione AI
    true,                  // validateLinks: ABILITATA per default
)
```

### Disabilitare AI Validation

Se vuoi skippare la validazione AI per velocità:

```go
scriptClipsService = scriptclips.NewScriptClipsService(
    ...,
    false,  // validateLinks: DISABILITATA
)
```

---

## 📊 Performance

### Tempi Stimati (per entity)

| Step | Tempo | Note |
|------|-------|------|
| YouTube Search | 2-5s | yt-dlp flat playlist |
| AI Validation | 10-20s | Ollama genera risposta JSON |
| Download Clip | 15-45s | Dipende da durata/qualità |
| Drive Upload | 10-30s | Dipende da dimensione file |
| **Totale per entity** | **37-100s** | |

### Download Parallelizzati

Il sistema usa **5 workers concorrenti** per entity diverse:

```
Entity 1: [Search] → [AI Validate] → [Download] → [Upload]
Entity 2: [Search] → [AI Validate] → [Download] → [Upload]  (parallelo)
Entity 3: [Search] → [AI Validate] → [Download] → [Upload]  (parallelo)
...
```

**Speedup:** ~5x rispetto al processing sequenziale

---

## 🔍 Logging

Durante l'esecuzione vedrai log come:

```
INFO  Searching clip for entity  entity=XXXTentacion query_en=XXXTentacion
INFO  YouTube search completed   query=XXXTentacion results=10
INFO  Validating YouTube links with Ollama AI  entity=XXXTentacion total_results=10
INFO  Sending YouTube links to Ollama for validation  entity=XXXTentacion links_count=10
INFO  Ollama validation completed  entity=XXXTentacion approved=4 rejected=6
INFO  Clip downloaded and uploaded to Drive  entity=XXXTentacion drive_url=...
```

---

## 🎯 Esempio Completo: XXXTentacion

### Input

```bash
curl -X POST http://localhost:8080/api/script/generate-with-clips \
  -H "Content-Type: application/json" \
  -d '{
    "title": "XXXTentacion",
    "source_text": "Jahseh Dwayne Ricardo Onfroy, known as XXXTentacion, was born January 23, 1998 in Plantation, Florida. He became famous with Look At Me, SAD!, Moonlight. He was killed June 18, 2018 during a robbery at age 20. The murder trial involved four defendants: Michael Boatright, Trayvon Newsome, Dedrick Williams, and Robert Allen who testified against the others.",
    "language": "italian",
    "duration": 1200,
    "entity_count_per_segment": 8
  }'
```

### Entità Estratte (esempio)

- **Nomi Speciali:** XXXTentacion, Florida, SoundCloud, Boatright, Allen
- **Frasi Importanti:** "killed during a robbery at age 20", "Robert Allen testified"
- **Parole Importanti:** murder, trial, robbery, testimony

### Clip Cercate per Ogni Entity

| Entity | Query YouTube | AI Approved | Downloaded | Drive Folder |
|--------|--------------|-------------|------------|--------------|
| XXXTentacion | XXXTentacion | 4/10 | ✅ | Stock/XXXTentacion/ |
| Florida | Florida | 3/10 | ✅ | Stock/XXXTentacion/ |
| SoundCloud | SoundCloud | 2/10 | ✅ | Stock/XXXTentacion/ |
| Boatright | Boatright | 3/10 | ✅ | Stock/XXXTentacion/ |
| Allen | Allen | 2/10 | ✅ | Stock/XXXTentacion/ |
| murder | murder trial | 4/10 | ✅ | Stock/XXXTentacion/ |
| trial | court trial | 3/10 | ✅ | Stock/XXXTentacion/ |
| robbery | robbery news | 2/10 | ✅ | Stock/XXXTentacion/ |

### Risultato Drive

```
Google Drive/
└── Stock/
    └── XXXTentacion/
        ├── clip_XXXTentacion_1234567890.mp4
        ├── clip_Florida_1234567891.mp4
        ├── clip_SoundCloud_1234567892.mp4
        ├── clip_Boatright_1234567893.mp4
        └── ... (altri clip)
```

---

## ⚠️ Note Importanti

1. **AI Validation aggiunge ~10-20s per entity** - Ma evita download inutili
2. **Ollama deve essere in esecuzione** - `systemctl status ollama`
3. **yt-dlp deve essere installato** - `which yt-dlp`
4. **Google OAuth token valido** - `token.json` aggiornato
5. **Max 5 download concorrenti** - Per evitare rate limiting da YouTube/Drive

---

## 🚀 Prossimi Miglioramenti

- [ ] Cache dei risultati AI validation (stessi link → stessa risposta)
- [ ] Retry automatico per download falliti
- [ ] Progress tracking durante il pipeline
- [ ] Supporto per Artlist/Pexels come fonti alternative
- [ ] Thumbnail generation per clip scaricati
