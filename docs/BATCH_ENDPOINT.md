# Batch Script Generation — `POST /api/script/generate-batch`

Genera script multi-capitolo da una lista di topic con source text. Supporta voiceover per-capitolo, traduzioni, Google Docs, e memoria cache.

---

## Indice
- [Payload Completo](#payload-completo)
- [Esempi curl](#esempi-curl)
- [Flags & Opzioni](#flags--opzioni)
- [Flow di Esecuzione](#flow-di-esecuzione)
- [Response](#response)
- [Voiceover per Capitolo](#voiceover-per-capitolo)
- [Linee Guida (guidelines)](#linee-guida-guidelines)
- [Polling Async](#polling-async)
- [Errori Comuni](#errori-comuni)

---

## Payload Completo

```json
{
  "doc_title": "How to Live Like The Amish",
  "channel_id": "my-channel",
  "items": [
    {
      "topic": "Plain Budget: The Spending Filter",
      "source_text": "Start by explaining how modern people earn more but feel poorer..."
    }
  ],
  "language": "en",
  "tone": "documentary",
  "duration": 600,
  "target_words_per_item": 1500,
  "voiceover": false,
  "save_to_db": true,
  "async": true,
  "no_chapters": false,
  "guidelines": "Write in a practical, actionable style...",
  "force_refresh": false,
  "languages": ["es", "fr"],
  "drive_folder_id": "1ABCdef...",
  "model": "gemma4:e4b",
  "prompt_version": "book_writer_v3",
  "editor_prompt_version": "book_editor_v2",
  "qa_prompt_version": "qa_book_v1"
}
```

---

## Esempi curl

### 1. Batch base (sync, 2 capitoli, nessun voiceover)

```bash
curl -X POST http://127.0.0.1:8081/api/script/generate-batch \
  -H "Authorization: Bearer velox_master_key_2026" \
  -H "Content-Type: application/json" \
  -d '{
    "doc_title": "Simple Living Guide",
    "channel_id": "simple-living",
    "items": [
      {
        "topic": "Minimalism at Home",
        "source_text": "Explain how to declutter your home step by step..."
      },
      {
        "topic": "Mindful Spending",
        "source_text": "Teach readers how to track every expense..."
      }
    ],
    "language": "en",
    "voiceover": false,
    "save_to_db": false,
    "async": false
  }'
```

### 2. Batch con voiceover per-capitolo (async, 8 capitoli)

```bash
curl -X POST http://127.0.0.1:8081/api/script/generate-batch \
  -H "Authorization: Bearer velox_master_key_2026" \
  -H "Content-Type: application/json" \
  -d '{
    "doc_title": "Frugal Living: 8 Steps to Financial Freedom",
    "channel_id": "frugal-living",
    "items": [
      {"topic": "Plain Budget", "source_text": "Explain the Amish-inspired spending filter..."},
      {"topic": "Infinite Pantry", "source_text": "Create a practical food storage system..."}
    ],
    "language": "en",
    "voiceover": true,
    "save_to_db": true,
    "async": true,
    "target_words_per_item": 1500,
    "guidelines": "Write in a practical, actionable style..."
  }'

# Response:
# {
#   "ok": true,
#   "async": true,
#   "job_id": "job_xxx...",
#   "status_url": "/api/jobs/job_xxx.../full"
# }
```

### 3. Batch con outline automatico (solo macro-topic)

```bash
curl -X POST http://127.0.0.1:8081/api/script/generate-batch \
  -H "Authorization: Bearer velox_master_key_2026" \
  -H "Content-Type: application/json" \
  -d '{
    "doc_title": "Italian Cooking Basics",
    "channel_id": "cooking",
    "outline_topic": "Italian cooking techniques for beginners",
    "num_chapters": 5,
    "language": "en",
    "voiceover": false,
    "async": true,
    "save_to_db": true
  }'
```

### 4. Polling stato job

```bash
curl -s http://127.0.0.1:8081/api/jobs/job_xxx.../full | jq
```

---

## Flags & Opzioni

| Campo | Default | Descrizione |
|-------|---------|-------------|
| `doc_title` | **required** | Titolo del documento |
| `channel_id` | `"default-batch"` | Canale per memoria cache |
| `items` | `[]` | Array di `{topic, source_text}`. Max 50 |
| `language` | `"it"` | Lingua di generazione (`en`, `it`, `es`, `fr`) |
| `tone` | `"documentary"` | Tono dello script |
| `duration` | `600` | Durata in secondi (min 120) |
| `target_words_per_item` | `1800` | Parole target per capitolo (800-5000) |
| `voiceover` | `false` | Genera voiceover MP3 per-capitolo |
| `save_to_db` | `false` | Salva su DB (tabella `scripts`) |
| `async` | `false` | Esecuzione asincrona (job system) |
| `no_chapters` | `false` | Testo fluido senza titoli capitolo |
| `guidelines` | `""` | Istruzioni extra per l'LLM (max 4000 char) |
| `force_refresh` | `false` | Bypassa cache esatta |
| `languages` | `[]` | Lingue extra per traduzioni |
| `drive_folder_id` | dalla config | Cartella Google Drive per upload |
| `model` | dalla config | Modello Ollama da usare |
| `num_chapters` | `5` | (solo con `outline_topic`) |

---

## Flow di Esecuzione

```
1. Validazione         → doc_title + channel_id required
2. Defaults            → language="en", tone="documentary", etc.
3. Outline (opz.)      → Se outline_topic, LLM genera N titoli
4. Web Search          → SearXNG parallelo (concurrency 4)
5. Source Resolution   → YouTube URL → yt-dlp transcript
6. Source Split        → testi > 4000 parole → [SEGMENT]
7. Memory Gate         → cache check → salta LLM se esatto
8. Ollama Generation   → Per ogni capitolo (con retry 3×)
9. Quality Check       → EXPAND/COMPRESS via LLM
10. Memory Save        → gemma_script_outputs
11. Google Doc         → HTML upload su Google Drive
12. Voiceover          → Per-capitolo (MP3 separati)
13. Traduzioni         → Ollama Translate + unified voiceover
14. Save DB            → scripts table
15. Response           → script, doc_url, chapter_voiceovers
```

---

## Response

### Sync mode (async: false)

```json
{
  "ok": true,
  "title": "Frugal Living: 8 Steps to Financial Freedom",
  "script": "Frugal Living: 8 Steps to Financial Freedom\n\n...full script text with all chapters...",
  "doc_url": "https://docs.google.com/document/d/...",
  "voiceover_link": "https://drive.google.com/file/d/...",
  "translations": { ... },
  "timings": [
    {
      "topic": "Plain Budget: The Amish-Inspired Spending Filter",
      "word_count": 1270,
      "generation_duration_ms": 36111,
      "qa_duration_ms": 28729,
      "total_duration_ms": 67905,
      "status": "completed",
      "cache_status": "miss",
      "voiceover_link": "https://drive.google.com/file/d/..."
    }
  ],
  "target_words_per_item": 1500,
  "failed_chapters": [],
  "failed_chapter_count": 0
}
```

### Async mode (async: true) — response immediata

```json
{
  "ok": true,
  "async": true,
  "job_id": "job_1780814221057009813_e1a2ecec",
  "status": "queued",
  "status_url": "/api/jobs/job_1780814221057009813_e1a2ecec/full"
}
```

---

## Voiceover per Capitolo

Quando `voiceover: true`, ogni capitolo riceve un file MP3 separato:

- `{docTitle}_chapter_01_{lang}.mp3`
- `{docTitle}_chapter_02_{lang}.mp3`
- ...

I link sono restituiti in `chapter_voiceovers` (mappa `chapter_number → drive_link`).

`cleanForVoiceover()` pulisce automaticamente:
- `#` headings, `*bold*`, `---` separators
- `[bracket annotations]`
- `> blockquotes`
- `Table of Contents` line
- `Chapter 1:`, `Item 1:`, `Parte 1:` labels (anche con titolo dopo)

---

## Linee Guida (guidelines)

Il campo `guidelines` permette di passare istruzioni generali all'LLM per **ogni capitolo**. Le guidelines vengono iniettate nel prompt di ogni capitolo come blocco `[GUIDELINES]`.

**Consigli per guidelines efficaci:**

```
"Write in a practical, actionable style with concrete examples.
Use an encouraging but direct tone. Explain the why before the how.
Include questions for the reader to reflect on.
Use short sentences and simple language for better TTS quality.
Start each chapter with a real-world scenario or statistic.
Include specific numbers: costs, savings, timeframes.
End each chapter with a call to action."
```

**Altri esempi:**

```
"Italian version: Scrivi come un insegnante paziente.
Usa metafore della vita quotidiana. Ogni capitolo deve avere
almeno 3 esempi pratici e 1 esercizio per il lettore."
```

```
"YouTube script style: Hook in first sentence.
Keep paragraphs under 3 sentences. End with engagement question.
No jargon. Conversational but authoritative."
```

---

## Polling Async

```bash
# Poll fino a completamento
JOB_ID="job_xxx..."
while true; do
  result=$(curl -s http://127.0.0.1:8081/api/jobs/$JOB_ID/full)
  status=$(echo "$result" | python3 -c "import sys,json; print(json.load(sys.stdin).get('status','unknown'))")
  progress=$(echo "$result" | python3 -c "import sys,json; print(json.load(sys.stdin).get('progress',0))")
  echo "[$(date)] Status: $status | Progress: $progress%"
  if [ "$status" = "completed" ] || [ "$status" = "failed" ]; then
    echo "$result" | python3 -m json.tool
    break
  fi
  sleep 10
done
```

---

## Errori Comuni

| Errore | Causa | Soluzione |
|--------|-------|-----------|
| `"language \"it\" is not supported"` | Config multilingual senza `it` | Usa `"en"` o aggiorna config.yaml |
| `"drive_folder_id is required"` | Nessun drive folder configurato | Passa `drive_folder_id` nel payload |
| `"items must contain at least 1 item"` | Array `items` vuoto | Aggiungi almeno 1 item con `topic` + `source_text` |
| `"target_words_per_item must be between 800 and 5000"` | Valore fuori range | Usa 800-5000 |
| `ActiveKey dedup` | Stesso `doc_title` di un job in corso | Cambia `doc_title` o aspetta che finisca |
| Voiceover bloccato al 94% | edge-tts che processa testo lungo | Disabilita voiceover o riduci parole per capitolo |

---

## Note per Agenti (AI Assistants)

- **Ordinamento capitoli**: L'array `items` preserva l'ordine. Il primo item → Chapter 1, ecc.
- **Etichette dinamiche**: L'heading usa `Chapter` (EN), `Capitolo` (IT), `Chapitre` (FR), `Capítulo` (ES) in base a `language`.
- **Cache**: La Memory Gate evita rigenerazioni identiche. Usa `force_refresh: true` per bypassare.
- **Google Doc**: Viene creato automaticamente quando `docClient` è configurato (vedi `flow_doc.go::maybeCreateGoogleDoc` e `batch_doc.go::createBatchDoc`). Il flag `create_doc` esiste solo sull'endpoint separato `POST /api/script/generate-from-catalog` (`GenerateFromCatalogRequest`), NON sul batch.
- **DB**: `save_to_db: true` salva su `scripts` table con versioning automatico.
- **Il voiceover per-capitolo è molto più veloce** di un voiceover unificato (ogni capitolo è ~1300 parole invece di ~11000).
