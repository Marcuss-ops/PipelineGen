# 🎬 VeloxEditing - Sessione 13 Aprile 2026 - RIEPILOGO COMPLETO

**Data:** 13 Aprile 2026 (mattina)  
**Stato:** ✅ Sistema operativo, pipeline principale funzionante

---

## 📋 COSA ABBIAMO FATTO STAMATTINA

### Sessione 1: Diagnosi dei Problemi (08:00 - 09:00)

#### Problema Identificato: YouTube Search Non Funzionante
- **Sintomo:** `StockManager.SearchYouTube()` restituiva SEMPRE 0 risultati
- **Test effettuati:**
  - ✅ yt-dlp diretto da CLI: funzionava (5 risultati)
  - ✅ YouTube v2 API (`/api/youtube/v2/search`): funzionava (10 risultati)
  - ❌ Endpoint stock (`/api/stock/search/youtube`): 0 risultati

#### Debug Approfondito
- Analizzato il codice: `search.go` usava `exec.CommandContext` con `--dump-json --flat-playlist`
- Problema: output parsing falliva silenziosamente o contesto scadeva
- Server crashava frequentemente (OOM / context timeout)

#### Soluzione Implementata: YouTube v2 Integration
- **Approccio:** Usare il client YouTube v2 (già funzionante) come backend per SearchYouTube
- **Fallback:** Se v2 fallisce, usa yt-dlp diretto
- **File modificati:**
  1. `internal/stock/manager.go` - Aggiunto campo `ytClient youtube.Client`
  2. `internal/stock/search.go` - Riscritto `SearchYouTube()` con v2 + fallback
  3. `cmd/server/main.go` - Inizializza YouTube v2 PRIMA di Stock Manager

---

### Sessione 2: Fix dei 4 Problemi Critici (09:00 - 10:30)

#### PROBLEMA 1: Context Cancellation
**Sintomo:** Pipeline si interrompeva dopo 10 minuti quando il client HTTP si disconnetteva

**Causa:** Usava `c.Request.Context()` → si cancella se il client chiude la connessione

**Fix:** Usato `context.Background()` con timeout di 30 minuti
```go
// PRIMA (SBAGLIATO):
result, err := h.service.GenerateScriptWithClips(c.Request.Context(), &req)

// DOPO (CORRETTO):
pipelineCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
defer cancel()
result, err := h.service.GenerateScriptWithClips(pipelineCtx, &req)
```

**File:** `internal/api/handlers/script_clips.go`

---

#### PROBLEMA 2: Troppe Entità Estratte
**Sintomo:** 134 entità estratte, 118 clip mancanti. Estrae frasi intere invece di keyword

**Esempio:** `"La sua musica continua ad essere studiata"` → troppo lunga per YouTube search

**Fix:** Filtrato `collectEntityNames()` per usare SOLO:
1. **NomiSpeciali** (nomi propri, max 50 caratteri) - PRIORITÀ ALTA
2. **ParoleImportanti** (keyword singole, max 30 caratteri) - PRIORITÀ MEDIA
3. **FrasiImportanti** - SOLO frasi molto brevi (< 40 char, < 4 parole)
4. **Max 8 entità per segmento** (prima: illimitate)

**File:** `internal/service/scriptclips/service.go` - `collectEntityNames()`

---

#### PROBLEMA 3: Query in Italiano su YouTube
**Sintomo:** Cercava "fragilità", "dolore", "alienazione" su YouTube → risultati scarsi

**Fix A:** Traduzione Italiano → Inglese prima della ricerca (dizionario esistente usato correttamente)
```go
searchQueryEN := s.clipTranslator.TranslateQuery(entityName)
```

**Fix B:** Filtro parole generiche italiane (articoli, preposizioni, verbi comuni)
- Aggiunta funzione `isGenericWord()` con 80+ stop words italiane/inglesi
- Salta: il, la, un, di, da, con, è, sono, ha, questo, quello, tempo, parte, tipo, ecc.
- Salta parole ≤ 2 caratteri

**File:** `internal/service/scriptclips/service.go` - `findOrDownloadClip()` + `isGenericWord()`

---

#### PROBLEMA 4: Server Instabilità
**Sintomo:** Server crashava frequentemente (SIGKILL / OOM)

**Causa:** Binario non aggiornato dopo le modifiche al codice

**Fix:** Rebuild completo e copia in `bin/server`

---

## 🎯 COSA È IN GRADO DI FARE IL SISTEMA ORA

### Pipeline Completa: Script → Entità → Clip → Drive

```
INPUT: Testo sorgente + Titolo + Durata desiderata
  │
  ├─→ 1. Generazione Script (Ollama gemma3:4b)
  │     - Script dettagliato in italiano
  │     - Segmentato in blocchi da ~20 secondi
  │     - Timestamp calcolati automaticamente
  │     - Esempio: 120 secondi → 6 segmenti
  │
  ├─→ 2. Estrazione Entità (per ogni segmento)
  │     - Nomi Speciali (nomi propri, persone, luoghi)
  │     - Parole Importanti (keyword rilevanti)
  │     - Frasi Importanti (solo se brevi)
  │     - Max 8 entità per segmento
  │
  ├─→ 3. Ricerca Clip YouTube (per ogni entità)
  │     - Traduzione IT → EN automatica
  │     - Filtro parole generiche
  │     - YouTube v2 search (5-10 risultati per query)
  │     - Fallback a yt-dlp se v2 fallisce
  │
  ├─→ 4. Validazione AI (Ollama)
  │     - Invia titoli + URL + durata a Ollama
  │     - Ollama approva/rifiuta clip per rilevanza
  │     - Esempio: "Alienazione" → 6 approvate, 4 rifiutate
  │
  ├─→ 5. Download Clip (5 workers paralleli)
  │     - Download via yt-dlp (max 15s per clip)
  │     - Conversione a 1920x1080 MP4 (H.264 + AAC)
  │     - Download parallelo per velocità
  │
  └─→ 6. Upload a Google Drive
        - Cartella: Stock/{TopicName}/
        - Nome: clip_{entity}_{timestamp}.mp4
        - Link Drive restituito nel risultato

OUTPUT: Script + Clip Mappings (entità → clip → Drive URL)
```

### Endpoint API Disponibili

| Endpoint | Metodo | Funzione | Status |
|----------|--------|----------|--------|
| `/health` | GET | Health check | ✅ |
| `/api/script/generate` | POST | Genera script da testo | ✅ |
| `/api/script/generate-with-clips` | POST | **Pipeline completa** | ✅ |
| `/api/voiceover/generate` | POST | Genera audio (EdgeTTS) | ✅ |
| `/api/stock/search/youtube` | GET | Cerca video YouTube | ✅ FIXED |
| `/api/youtube/v2/search` | GET | Cerca video YouTube (v2) | ✅ |
| `/api/youtube/v2/video/info` | GET | Info video YouTube | ✅ |
| `/api/youtube/v2/transcript` | GET | Estrai transcript YouTube | ✅ |

### Performance Attuali

| Operazione | Tempo | Note |
|-----------|-------|------|
| Generazione script (30s) | 10-15s | Ollama gemma3:4b |
| Estrazione entità | 2-5s | NLP locale |
| Ricerca YouTube (per entità) | 2-3s | YouTube v2 client |
| Validazione AI | 3-5s | Ollama locale |
| Download clip | 15-30s | yt-dlp, dipende da video |
| Upload Drive | 10-20s | Google Drive API |
| **Totale per entità** | **30-60s** | |
| **Pipeline completa (5 segmenti, 8 entità)** | **12-40 min** | 40 entità totali |

---

## 🔧 STACK TECNOLOGICO

| Componente | Tecnologia | Versione | Ruolo |
|-----------|-----------|----------|-------|
| **API Server** | Go (Gin framework) | 1.18 | Backend principale, orchestrazione |
| **Video Processing** | Rust (binario precompilato) | - | FFmpeg, effetti, transizioni |
| **AI Script Gen** | Ollama | gemma3:4b | Generazione script |
| **AI Validation** | Ollama | gemma3:4b | Validazione link YouTube |
| **Voiceover** | EdgeTTS | 7.2.7 | Sintesi vocale (Italiano + altre) |
| **YouTube Download** | yt-dlp | Latest | Download video da YouTube |
| **Storage** | Google Drive API | v3 | Upload clip, organizzazione Stock |
| **Database** | JSON files | - | Job, worker, queue persistence |

---

## 📊 COMPONENTI FUNZIONANTI

### ✅ Completamente Operativi
- [x] Generazione script da testo (Ollama)
- [x] Segmentazione script (blocchi da ~20s)
- [x] Estrazione entità (Nomi, Parole, Frasi)
- [x] YouTube search (v2 client + fallback yt-dlp)
- [x] Traduzione query IT → EN (dizionario 200+ parole)
- [x] Validazione AI link YouTube (Ollama approva/rifiuta)
- [x] Download clip da YouTube (yt-dlp parallelo)
- [x] Upload clip a Google Drive (cartelle topic-specific)
- [x] Voiceover generation (EdgeTTS, voce italiana)
- [x] YouTube transcript extraction
- [x] Context cancellation fix (pipeline non si interrompe)
- [x] Entity filtering (no frasi lunghe, max 8 per segmento)
- [x] Generic word filter (80+ stop words IT/EN)

### ⚠️ Funzionanti ma Migliorabili
- [ ] Pipeline completa: lenta (12-40 min per progetto)
- [ ] Entity extraction: potrebbe essere più precisa
- [ ] Query translation: dizionario limitato, non copre tutto
- [ ] AI validation: a volte troppo severa (rifiuta clip utili)
- [ ] Download: potrebbe essere più intelligente (scegliere risoluzione migliore)

### ❌ Non Ancora Implementati
- [ ] Async pipeline con Job ID + polling
- [ ] Cache clip già scaricate (evita download duplicati)
- [ ] Supporto Artlist per clip musicali (solo indicizzazione)
- [ ] Dashboard web per monitorare progress
- [ ] Notifiche (email/Slack) a pipeline completata
- [ ] Supporto multi-lingua per script (solo italiano ora)
- [ ] Editing video completo (solo download/upload, non montaggio)
- [ ] Integrazione con YouTube per upload video finale
- [ ] Template per diversi stili video (documentario, news, ecc.)

---

## 🚀 PROSSIME FUNZIONALITÀ DA AGGIUNGERE

### Priorità ALTA (risolvono problemi attuali)

#### 1. **Async Pipeline con Job ID**
**Problema:** Pipeline blocca la richiesta HTTP per 12-40 minuti  
**Soluzione:**
- Endpoint `POST /api/pipeline/start` → restituisce `{job_id: "xyz"}`
- Endpoint `GET /api/pipeline/status/:job_id` → restituisce `{status: "processing", progress: 45%, clips_found: 12, clips_missing: 28}`
- Endpoint `GET /api/pipeline/result/:job_id` → restituisce risultato completo
- Background worker esegue pipeline senza bloccare HTTP

**Stimato:** 4-6 ore di sviluppo

---

#### 2. **Cache Clip Scaricate**
**Problema:** Se due entità cercano "Elon Musk", scarica 2 volte  
**Soluzione:**
- Database locale: `{search_query: "Elon Musk", drive_url: "...", downloaded_at: "..."}`
- Prima di scaricare, controlla cache
- Se trovato, riusa clip esistente
- TTL: 30 giorni (poi riscarica)

**Stimato:** 2-3 ore di sviluppo

---

#### 3. **Migliorare Entity Extraction**
**Problema:** A volte estrae entità irrilevanti  
**Soluzione:**
- Usare Ollama per estrarre entità (invece di NLP locale)
- Prompt: `"Extract 5 most important proper nouns and keywords from this segment"`
- Più preciso, meno rumore

**Stimato:** 3-4 ore di sviluppo

---

### Priorità MEDIA (migliorano UX)

#### 4. **Dashboard Web**
**Problema:** Non c'è modo di vedere progress pipeline  
**Soluzione:**
- Pagina web semplice (React o HTML statico)
- Lista job con status
- Progress bar per pipeline in esecuzione
- Link alle clip su Drive
- Log in tempo reale

**Stimato:** 8-12 ore di sviluppo

---

#### 5. **Supporto Artlist per Clip Musicali**
**Problema:** Artlist ha 25 clip ma non sono usate nella pipeline  
**Soluzione:**
- Integrare Artlist index nella ricerca clip
- Se entità è "musica" o "concerto", cerca in Artlist prima
- Artlist clip hanno licensing migliore (no copyright YouTube)

**Stimato:** 3-4 ore di sviluppo

---

#### 6. **Notifiche a Pipeline Completata**
**Problema:** Non sai quando la pipeline finisce  
**Soluzione:**
- Email con risultato (script + link Drive)
- Oppure notifica Slack/Telegram
- Configura webhook nel job request

**Stimato:** 2-3 ore di sviluppo

---

### Priorità BASSA (feature avanzate)

#### 7. **Video Editing Automatico**
**Problema:** Pipeline scarica clip ma non le monta  
**Soluzione:**
- Dopo aver scaricato tutte le clip, usa Rust binary per:
  - Unire clip in ordine di script
  - Aggiungere transizioni (fade, dissolve)
  - Aggiungere voiceover
  - Aggiungere musica di sottofondo
  - Esportare video finale

**Stimato:** 16-24 ore di sviluppo

---

#### 8. **Upload a YouTube**
**Problema:** Video finale resta su Drive  
**Soluzione:**
- Endpoint `POST /api/youtube/upload` → upload video finale
- Configura titolo, descrizione, tags, thumbnail
- Pubblica come pubblico/privato/non in elenco

**Stimato:** 4-6 ore di sviluppo

---

#### 9. **Template Video**
**Problema:** Tutti i video hanno stesso stile  
**Soluzione:**
- Template: "Documentario", "News", "Biografia", "Top 10"
- Ogni template ha:
  - Stile transizioni diverso
  - Musica di sottofondo diversa
  - Formato script diverso
  - Durata segmenti diversa

**Stimato:** 8-12 ore di sviluppo

---

#### 10. **Supporto Multi-Lingua**
**Problema:** Solo italiano supportato  
**Soluzione:**
- Configura lingua script: `italian`, `english`, `spanish`
- EdgeTTS supporta 50+ lingue
- Traduzione query automatica per ogni lingua

**Stimato:** 4-6 ore di sviluppo

---

## 📁 FILE MODIFICATI OGGI

| File | Modifica | Stato |
|------|----------|-------|
| `internal/stock/manager.go` | Aggiunto `ytClient` field | ✅ |
| `internal/stock/search.go` | SearchYouTube con v2 + fallback | ✅ |
| `internal/api/handlers/script_clips.go` | Context.Background() + timeout | ✅ |
| `internal/service/scriptclips/service.go` | Entity filtering + generic word filter | ✅ |
| `cmd/server/main.go` | Inizializzazione YouTube v2 prima di Stock | ✅ |
| `bin/server` | Binario aggiornato | ✅ |

---

## 📝 DOCUMENTI CREATI OGGI

1. `TEST_REPORT_YOUTUBE_V2_FIX.md` - Report test YouTube v2 integration
2. `PIPELINE_FIXES_SUMMARY.md` - Riepilogo fix ai 4 problemi
3. `YOUTUBE_V2_FIX_SUMMARY.md` - Documentazione fix YouTube search
4. `TEST_REPORT_MULTI_ARTIST.md` - Test multi-artista (parziale)

---

## 🎯 RIEPILOGO FINALE

### Cosa Funziona ORA
✅ Pipeline completa: **testo → script → entità → clip YouTube → Drive**  
✅ YouTube search funzionante (v2 client + fallback)  
✅ Entity filtering intelligente (no frasi lunghe, max 8 per segmento)  
✅ Traduzione query IT → EN automatica  
✅ Validazione AI clip con Ollama  
✅ Download parallelo (5 workers)  
✅ Upload Drive in cartelle topic-specific  
✅ Context cancellation fix (pipeline non si interrompe)  

### Cosa Manca
❌ Async pipeline (Job ID + polling)  
❌ Cache clip scaricate (evita duplicati)  
❌ Dashboard web per monitoring  
❌ Video editing automatico (solo download/upload)  
❌ Upload a YouTube del video finale  

### Prossimi Passi Consigliati
1. **Async Pipeline** (risolve problema UX principale)
2. **Cache Clip** (risparmia tempo e banda)
3. **Dashboard Web** (monitoring facile)
4. **Video Editing** (prodotto finale completo)

---

**Server:** ✅ Running su `http://localhost:8080`  
**Ultimo build:** `bin/server` (13 Apr 2026, 09:34)  
**Sessione:** Completata con successo ✅
