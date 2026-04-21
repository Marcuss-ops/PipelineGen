# Script Generation API - Documentazione

## Panoramica

La generazione script avviene nel modulo `worker_modules/remote_generator.py`. Il sistema genera script per video YouTube partendo dai sottotitoli reali, con la seguente struttura:

```
[INTRO] + [SEGMENTO 1] + [SEGMENTO 2] + [SEGMENTO 3] + [SEGMENTO 4] + [OUTRO]
```

## Configurazione Attuale

### Parametri di Generazione

| Parametro | Valore | Descrizione |
|-----------|--------|-------------|
| `num_segments` | 4 | Numero massimo segmenti |
| `target_words_per_segment` | 1900 | Parole target per segmento (~10-12 minuti) |
| `model` | gemma3:12b | Modello LLM utilizzato |
| `duration` | 20 min (max) | Durata massima del video |
| `vtt_cache_ttl` | 24 ore | Tempo di vita cache VTT |

### Struttura dello Script

#### 1. INTRO (~80 parole)
- Generata dal **titolo** + **prime 1000 parole** dei sottotitoli YouTube
- Hook immediato per catturare l'attenzione

#### 2. SEGMENTI (4 segmenti, ~1900 parole ciascuno)
- I sottotitoli YouTube vengono divisi in 4 parti uguali
- Rimozione automatica delle ripetizioni

#### 3. OUTRO (~40 parole)
- Ringraziamento semplice

## API Endpoints

### POST /api/video/create-master

Genera script + voiceover automaticamente.

```bash
curl -X POST http://77.93.152.122:5000/api/video/create-master \
  -H "Content-Type: application/json" \
  -d '{
    "video_name": "Titolo Video",
    "script_text": "[SCRIPT WILL BE GENERATED]",
    "youtube_url": "https://www.youtube.com/watch?v=VIDEO_ID",
    "voiceover_languages": ["pt-BR"],
    "voiceover_drive_folder": "FOLDER_ID"
  }'
```

### POST /api/voiceover/generate

Genera voiceover da script esistente (senza generare script).

```bash
curl -X POST http://77.93.152.122:5000/api/voiceover/generate \
  -H "Content-Type: application/json" \
  -d '{
    "script_text": "Il testo dello script...",
    "languages": ["pt-BR", "en"],
    "drive_folder_id": "FOLDER_ID",
    "video_title": "Titolo Video"
  }'
```

### POST /api/clip/batch-download

Scarica più clip YouTube in parallelo.

```bash
curl -X POST http://77.93.152.122:5000/api/clip/batch-download \
  -H "Content-Type: application/json" \
  -d '{
    "drive_folder_id": "FOLDER_ID",
    "clips": [
      {"youtube_url": "https://www.youtube.com/watch?v=XXX", "start_time": "0:00", "end_time": "0:25", "video_title": "Clip1"},
      {"youtube_url": "https://www.youtube.com/watch?v=YYY", "start_time": "0:00", "end_time": "0:25", "video_title": "Clip2"},
      {"youtube_url": "https://www.youtube.com/watch?v=ZZZ", "start_time": "0:00", "end_time": "0:25", "video_title": "Clip3"}
    ]
  }'
```

### POST /api/clip/download-youtube

Scarica una singola clip YouTube.

```bash
curl -X POST http://77.93.152.122:5000/api/clip/download-youtube \
  -H "Content-Type: application/json" \
  -d '{
    "youtube_url": "https://www.youtube.com/watch?v=VIDEO_ID",
    "start_time": "0:00",
    "end_time": "0:25",
    "drive_folder_id": "FOLDER_ID",
    "video_title": "Titolo Clip"
  }'
```

### POST /api/stock/search-youtube

Cerca video stock su YouTube.

```bash
curl -X POST http://77.93.152.122:5000/api/stock/search-youtube \
  -H "Content-Type: application/json" \
  -d '{
    "subject": "hip hop interview",
    "max_results": 5
  }'
```

### POST /api/stock/create-folder

Crea cartella stock su Drive.

```bash
curl -X POST http://77.93.152.122:5000/api/stock/create-folder \
  -H "Content-Type: application/json" \
  -d '{
    "folder_name": "NomeCartella",
    "group_name": "NomeGruppo"
  }'
```

## VTT Caching

Il sistema memorizza nella cache i sottotitoli YouTube scaricati:
- **Posizione cache**: `/tmp/vtt_cache`
- **TTL**: 24 ore
- **Beneficio**: Richieste successive per lo stesso video sono più veloci

## Flusso di Generazione

```
1. Estrazione sottotitoli YouTube (yt-dlp)
   ↓
2. Check cache VTT (se presente, salta download)
   ↓
3. Generazione INTRO (titolo + prime 1000 parole VTT)
   ↓
4. Divisione VTT in 4 segmenti uguali
   ↓
5. Riscrivi ogni segmento (~1900 parole)
   ↓
6. Generazione OUTRO
   ↓
7. Combinazione: INTRO + SEGMENTI + OUTRO
   ↓
8. (Opzionale) Generazione voiceover
```

## Errori Comuni

| Errore | Causa | Soluzione |
|--------|-------|-----------|
| No VTT found | YouTube non ha sottotitoli | Usa un video con sottotitoli |
| Ollama timeout | Modello lento | Aumentare timeout |
| Master 500 | Problema server Master | Controllare log Master |
| Requested format unavailable | YouTube blocca download | Prova altro video |

## File Configurazione

- **Generazione script:** `worker_modules/remote_generator.py`
- **API endpoint:** `studio_app_remote.py`

## Riferimenti

- Worker health: `http://77.93.152.122:5000/api/health`
- Tracing dashboard: `http://77.93.152.122:5555/`
