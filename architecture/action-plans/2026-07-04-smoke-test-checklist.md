# Smoke Test Checklist — PipelineGen E2E Verification

**Created**: 2026-07-04
**Status**: in_progress
**Owner**: `architecture`
**Deadline**: 2026-08-01 (first pass)

## Rationale

Ogni test verifica **HTTP → job/outbox → DB → file/Drive/Qdrant → stato finale**,
perché guardare solo la risposta API rischia falsi successi.

Placeholder usati nei comandi:

```bash
BASE="http://localhost:8080"
DB="data/pipelinegen.db"   # cambia con il path reale del tuo SQLite
```

---

# 1. Jobs / Worker / Runner

Route live: `POST /api/jobs`, `GET /api/jobs`, `GET /api/jobs/stats`, `GET /api/jobs/:id`,
`GET /api/jobs/:id/full`, `POST /api/jobs/:id/cancel`, `POST /api/jobs/:id/retry`, `GET /api/jobs/:id/events`.

## Test 1 — Enqueue job valido

```bash
curl -s -X POST "$BASE/api/jobs" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "voiceover.generate",
    "project": "test",
    "video_name": "jobs-smoke-test",
    "payload": {
      "request_id": "jobs_smoke_001",
      "items": [
        {
          "text": "Test job runner.",
          "language": "it-IT",
          "filename": "jobs_smoke.mp3",
          "required": true
        }
      ],
      "destination": {
        "kind": "explicit",
        "folder_id": "ROOT_OR_TEST_FOLDER_ID"
      }
    },
    "priority": 5,
    "max_retries": 1,
    "active_key": "jobs-smoke-001"
  }' | jq
```

**Atteso**: HTTP 202, `job_id` presente, `job.status = queued/pending`, `progress = 0`

**DB check**:

```bash
sqlite3 "$DB" "
SELECT id,type,status,progress,error,active_key,created_at,updated_at
FROM jobs
WHERE active_key='jobs-smoke-001'
ORDER BY created_at DESC
LIMIT 5;
"
```

**Rosso se**: nessuna riga, status resta queued/running per sempre, progress 100 ma status RUNNING, error vuoto ma FAILED.

---

## Test 2 — Polling job fino a SUCCEEDED

```bash
JOB_ID="INSERISCI_JOB_ID"

for i in $(seq 1 60); do
  curl -s "$BASE/api/jobs/$JOB_ID/full" | jq '{id,type,status,progress,current_step,retryable,result,events_count:(.events|length)}'
  sleep 2
done
```

**Atteso**: status finale = SUCCEEDED, progress = 100, events non vuoto, retryable = false, result valorizzato.

**DB check**:

```bash
sqlite3 "$DB" "
SELECT id,type,status,progress,result,error
FROM jobs WHERE id='$JOB_ID';
SELECT job_id,type,message,created_at
FROM job_events WHERE job_id='$JOB_ID' ORDER BY created_at;
"
```

**Rosso se**: events vuoto, current_step fermo senza eventi, progress 100 ma RUNNING, SUCCEEDED con error, FAILED senza error.

---

## Test 3 — Job FAILED pulito

Enqueue con payload invalido (es. `voiceover.generate` senza `items`):

```bash
curl -s -X POST "$BASE/api/jobs" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "voiceover.generate",
    "project": "test",
    "video_name": "jobs-failed-test",
    "payload": {
      "request_id": "jobs_failed_001",
      "items": []
    },
    "priority": 5,
    "max_retries": 0,
    "active_key": "jobs-failed-001"
  }' | jq
```

**Atteso**: status = FAILED, error non vuoto, eventi includono failure/error.

---

## Test 4 — Cancel durante RUNNING

Serve job lento (ScriptFlow, image generation, stock, voiceover multi-item):

```bash
JOB_ID="INSERISCI_JOB_RUNNING"

curl -s -X POST "$BASE/api/jobs/$JOB_ID/cancel" | jq
curl -s "$BASE/api/jobs/$JOB_ID/full" | jq '{id,status,progress,retryable,result,events}'
```

**Atteso**: message = "job cancelled", status = CANCELLED/CANCELED, worker non scrive result finale.

**Rosso se**: cancel risponde OK ma job va a SUCCEEDED, cancel su terminale cambia stato, cancel lascia lock appeso.

---

## Test 5 — Retry di FAILED

```bash
FAILED_JOB_ID="INSERISCI_JOB_FAILED"

curl -s -X POST "$BASE/api/jobs/$FAILED_JOB_ID/retry" | jq
```

**Atteso**: job resettato, status torna queued/running, attempt coerente, events includono retry.

**Rosso se**: duplicati incontrollati, retry non resetta error, retry resta RUNNING senza worker.

---

## Test 6 — Retry di SUCCEEDED

```bash
SUCCEEDED_JOB_ID="INSERISCI_JOB_SUCCEEDED"

curl -s -X POST "$BASE/api/jobs/$SUCCEEDED_JOB_ID/retry" | jq
```

**Atteso**: errore 4xx/409, nessun nuovo job, nessun side effect duplicato.

**Rosso se**: retry di SUCCEEDED rigenera file/Drive/outbox.

---

## Test 7 — Stats coerenti

```bash
curl -s "$BASE/api/jobs/stats" | jq
sqlite3 "$DB" "SELECT status, COUNT(*) FROM jobs GROUP BY status;"
```

**Atteso**: stats API = count DB, nessun numero negativo, running/failed/succeeded coerenti.

---

# 2. Outbox Generale

Stati: `pending`, `processing`, `completed`, `dead_letter`, `superseded`.

## Test 1 — pending → processing → completed

```bash
sqlite3 "$DB" "
SELECT id,event_type,aggregate_id,status,attempt_count,max_attempts,last_error,event_key,created_at,updated_at
FROM outbox_events
ORDER BY id DESC LIMIT 20;
"
```

**Atteso**: pending → processing durante claim → completed, attempt_count >= 1, last_error vuoto su completed.

**Rosso se**: processing appeso oltre lease TTL, completed con last_error, pending mai claimato.

---

## Test 2 — asset.index.requested valido

```bash
sqlite3 "$DB" "
SELECT id,event_type,aggregate_id,status,payload_json,event_key,last_error
FROM outbox_events
WHERE event_type='asset.index.requested'
ORDER BY id DESC LIMIT 10;
"
```

**Atteso payload**: `schema_version = asset.index.requested.v1`, `asset_id`, `source_version`, `idempotency_key`, `operation = UPSERT`.

---

## Test 3 — Evento terminale invalido → dead_letter

```bash
sqlite3 "$DB" "
INSERT INTO outbox_events
(event_type, aggregate_id, aggregate_type, payload_json, event_key, status, created_at, updated_at)
VALUES
('asset.index.requested', 'bad-asset-terminal', 'media_asset',
 '{\"schema_version\":\"wrong\",\"event_id\":\"x\",\"asset_id\":\"bad-asset-terminal\",\"source_version\":\"abc\",\"idempotency_key\":\"bad\"}',
 'test-terminal-bad-schema-001', 'pending', datetime('now'), datetime('now'));
"
```

**Atteso**: status = dead_letter, attempt_count basso, last_error spiega schema_version mismatch.

---

## Test 4 — Errore retryable (Qdrant/embedding spento)

Spegni Qdrant o embedding server, genera asset, controlla:

```bash
sqlite3 "$DB" "
SELECT id,event_type,aggregate_id,status,attempt_count,last_error,next_attempt_at
FROM outbox_events
WHERE event_type='asset.index.requested'
ORDER BY id DESC LIMIT 10;
"
```

**Atteso**: status torna pending/retry, attempt_count cresce, next_attempt_at valorizzato, dopo max_attempts → dead_letter.

**Rosso se**: marked completed con Qdrant giù, resta processing per sempre.

---

## Test 5 — Superseded

```bash
sqlite3 "$DB" "
SELECT id,aggregate_id,status,payload_json,last_error
FROM outbox_events
WHERE event_type='asset.index.requested' AND status='superseded'
ORDER BY id DESC LIMIT 20;
"
```

**Atteso**: evento vecchio = superseded, evento nuovo = completed, Qdrant contiene solo versione corrente.

---

## Test 6 — Duplicate event_key

```bash
sqlite3 "$DB" "
SELECT event_key, COUNT(*), GROUP_CONCAT(status)
FROM outbox_events
WHERE event_key != ''
GROUP BY event_key
HAVING COUNT(*) > 1
ORDER BY COUNT(*) DESC LIMIT 20;
"
```

**Atteso**: zero righe duplicate.

---

## Test 7 — Event type coverage

```bash
sqlite3 "$DB" "
SELECT event_type, status, COUNT(*)
FROM outbox_events
GROUP BY event_type, status
ORDER BY event_type, status;
"
```

Devi vedere comportarsi bene: `asset.index.requested`, `asset.index.delete_requested`, `voiceover.cleanup.requested`, `metadata_export`, `provider_sync`, `delivery/webhook`, `workflow_step_completed`, `workflow_step_failed`.

---

# 3. ScriptFlow / Script Generation

Route live: `POST /generate`, `POST /generate-from-clips`, `POST /generate-with-images`,
`GET /clips/search`, `GET /jobs/:id`, `POST /:id/sections/:section_id/regenerate`, `POST /cache/evict`.

## Test 1 — Generate semplice

```bash
curl -s -X POST "$BASE/api/script/generate" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Test script PipelineGen",
    "language": "it",
    "tone": "informativo",
    "duration_seconds": 300
  }' | jq
```

**Atteso**: HTTP 200/202, job_id o script presente, nessun panic se Docs/Voiceover non richiesti.

---

## Test 2 — Generate-from-clips con clip reali

1. Trova clip:

```bash
sqlite3 "$DB" "
SELECT id,source,media_type,filename,drive_file_id,file_hash,index_state
FROM media_assets
WHERE media_type='video' AND lifecycle_state='ACTIVE'
ORDER BY created_at DESC LIMIT 5;
"
```

2. Usa `clip_ids` reali nel payload.

**Atteso**: script usa solo clip esistenti, ogni scena punta a clip reale, doc_link presente se `create_doc=true`.

**Rosso se**: clip inesistente → SUCCEEDED, scene senza clip_id, doc vuoto, job success senza result.

---

## Test 3 — Generate-with-images

**Atteso**: script generato, immagini associate se richieste, nessun crash se provider non configurato, errore chiaro se obbligatorio.

**Rosso se**: SUCCEEDED ma immagini assenti senza warning, job image figlio fallisce ma parent SUCCEEDED.

---

## Test 4 — Clips search dentro ScriptFlow

```bash
curl -s "$BASE/api/script/clips/search?q=mayweather&limit=10" | jq
```

**Atteso**: clip reali, nessun drive_link/local_path se contratto lo vieta, filtri coerenti.

**Rosso se**: asset DELETED/STAGING, duplicati, clip senza id.

---

## Test 5 — Job status ScriptFlow coerente

```bash
SCRIPT_JOB_ID="INSERISCI_JOB_ID"
curl -s "$BASE/api/script/jobs/$SCRIPT_JOB_ID" | jq
curl -s "$BASE/api/jobs/$SCRIPT_JOB_ID/full" | jq
```

**Atteso**: stato coerente tra le due route, eventi coerenti, result compatibile.

---

## Test 6 — Regenerate section

```bash
SCRIPT_ID="INSERISCI_SCRIPT_ID"
SECTION_ID="INSERISCI_SECTION_ID"

curl -s -X POST "$BASE/api/script/$SCRIPT_ID/sections/$SECTION_ID/regenerate" \
  -H "Content-Type: application/json" \
  -d '{"instruction": "Rendi questa sezione più coinvolgente ma mantieni i fatti."}' | jq
```

**Atteso**: solo sezione target cambia, script/versione aggiornata, nessuna perdita altre sezioni.

**Rosso se**: cambia tutto il documento, section_id inesistente → 200.

---

## Test 7 — Cache evict

```bash
curl -s -X POST "$BASE/api/script/cache/evict" \
  -H "Content-Type: application/json" \
  -d '{"scope": "script", "key": "test"}' | jq
```

Ripeti generazione uguale dopo evict.

**Atteso**: dopo evict deve rigenerare o segnare miss.

---

## Test 8 — Fallback Docs/Drive/Voiceover

Esegui con `create_doc=false`, `voiceover=false`, `images=false` → script funziona.
Poi con `create_doc=true` → doc_url/doc_id se configurati, errore chiaro altrimenti.

---

# 4. Images

## Test 1 — Generate image

Payload minimo:

```json
{
  "prompt": "A cinematic image of a boxing arena, realistic lighting",
  "style": "realistic",
  "language": "en",
  "destination": {
    "folder_id": "ROOT_OR_IMAGE_TEST_FOLDER_ID"
  }
}
```

**Atteso**: `media_type = image`, `source = image/generated_image`, `drive_file_id` valorizzato, `file_hash` non vuoto, `lifecycle_state = ACTIVE`, outbox `asset.index.requested` creato.

**Rosso se**: immagine su disco ma non in media_assets, Drive ok ma DB assente, file_hash vuoto.

---

## Test 2 — Ingest direct

**Atteso**: file riconosciuto come image, metadata coerenti, no duplicati con stesso hash.

**Rosso se**: stesso file → 10 righe, mime type sbagliato, lifecycle_state vuoto.

---

## Test 3 — Metadata ingest

```bash
sqlite3 "$DB" "
SELECT id,metadata_json
FROM media_assets
WHERE media_type='image'
ORDER BY created_at DESC LIMIT 5;
"
```

**Atteso**: prompt/style/source, width/height se disponibili, content_hash/file_hash.

---

## Test 4 — Outbox index image

```bash
sqlite3 "$DB" "
SELECT e.id,e.aggregate_id,e.status,e.last_error,e.payload_json
FROM outbox_events e
JOIN media_assets m ON m.id=e.aggregate_id
WHERE m.media_type='image' AND e.event_type='asset.index.requested'
ORDER BY e.id DESC LIMIT 20;
"
```

**Atteso**: event created, completed se Qdrant/ClipIndexer attivi, dead_letter solo con errore vero.

---

## Test 5 — Qdrant visual vector (se disponibile)

```bash
IMAGE_ASSET_ID="INSERISCI_IMAGE_ASSET_ID"

curl -s http://localhost:6333/collections/media_assets_current/points/scroll \
  -H "Content-Type: application/json" \
  -d "{
    \"limit\": 5,
    \"with_payload\": true,
    \"with_vector\": false,
    \"filter\": {
      \"must\": [
        {\"key\":\"asset_id\",\"match\":{\"value\":\"$IMAGE_ASSET_ID\"}}
      ]
    }
  }" | jq
```

**Atteso**: `payload.source image/generated_image`, `payload.media_type image`, `payload.lifecycle_state ACTIVE`.

---

## Test 6 — Cleanup temporanei

```bash
find /tmp -iname "*pipelinegen*" -o -iname "*image*" | head -50
```

**Atteso**: nessuna crescita infinita, file temporanei rimossi.

---

# 5. Clips Core

## Test 1 — Clip search

```bash
curl -s -X POST "$BASE/api/media/search" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "boxing",
    "sources": ["clips"],
    "mode": "hybrid",
    "filters": {"media_type": "video"},
    "limit": 10
  }' | jq
```

**Atteso**: items array, asset_id, media_type video, lifecycle_state implicito ACTIVE.

---

## Test 2 — Clip download

```bash
sqlite3 "$DB" "
SELECT id,filename,drive_file_id,drive_link
FROM media_assets
WHERE media_type='video' AND drive_file_id IS NOT NULL
ORDER BY created_at DESC LIMIT 5;
"
```

**Atteso**: HTTP 200 o redirect firmato, file scaricabile, dimensione > 0, mime corretto.

**Rosso se**: 200 con body vuoto, drive_file_id assente ma API OK.

---

## Test 3 — Bulk upload

Carica 2 file piccoli → 2 righe, 2 Drive files, file_hash non vuoto, outbox index per ciascuno.

**Rosso se**: parziale → success totale, un fallimento fa sparire l'altro, hash duplicato non gestito.

---

## Test 4 — Tag update

```bash
sqlite3 "$DB" "
SELECT id,tags,metadata_json,updated_at
FROM media_assets WHERE id='INSERISCI_ASSET_ID';
"
```

**Atteso**: tags aggiornati, updated_at cambia, metadata coerenti.

---

## Test 5 — Metadata update

**Atteso**: JSON valido, campi nuovi preservati, campi vecchi non cancellati.

---

## Test 6 — Soft delete

```bash
sqlite3 "$DB" "
SELECT id,lifecycle_state,index_state,deleted_at
FROM media_assets WHERE id='INSERISCI_ASSET_ID';
SELECT id,event_type,status,last_error
FROM outbox_events WHERE aggregate_id='INSERISCI_ASSET_ID' ORDER BY id DESC;
"
```

**Atteso**: lifecycle_state = DELETED/DELETE_PENDING, `asset.index.delete_requested` presente, Qdrant point eliminato.

**Rosso se**: clip cancellata da DB ma Qdrant resta ACTIVE, Drive eliminato ma DB ACTIVE.

---

# 6. Register from YouTube

Route: `POST /api/media/register-from-youtube`, `POST /api/media/register-batch`.

## Test 1 — Register singolo URL

```bash
curl -s -X POST "$BASE/api/media/register-from-youtube" \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://www.youtube.com/watch?v=9u4T_o3FxOU",
    "tags": ["boxing", "test"],
    "source": "youtube",
    "metadata": {"test_run": "register_single_001"}
  }' | jq
```

**Atteso**: HTTP 200/202, asset_id o job_id presente.

---

## Test 2 — Register batch

```bash
curl -s -X POST "$BASE/api/media/register-batch" \
  -H "Content-Type: application/json" \
  -d '{
    "items": [
      {"url": "https://www.youtube.com/watch?v=9u4T_o3FxOU", "tags": ["boxing"]},
      {"url": "https://www.youtube.com/watch?v=dQw4w9WgXcQ", "tags": ["music"]}
    ],
    "metadata": {"test_run": "register_batch_001"}
  }' | jq
```

**Atteso**: risultato per item, success/failure separati, batch non fallisce tutto se un item invalido.

---

## Test 3 — Duplicato stesso video

Ripeti stesso `register-from-youtube` due volte.

**Atteso**: non crea duplicati, stesso `youtube_video_id` → stessa riga o metadata aggiornati.

---

## Test 4 — Errore URL non valido

```bash
curl -s -X POST "$BASE/api/media/register-from-youtube" \
  -H "Content-Type: application/json" \
  -d '{"url": "not-a-youtube-url"}' | jq
```

**Atteso**: HTTP 400/422, nessuna riga media_assets, nessun outbox event.

---

# 7. Search Aggregata

Route: `POST /api/media/search`. Body: `query`, `sources`, `mode`, `filters`, `limit`, `cursor`.

## Test 1 — Query obbligatoria

```bash
curl -s -X POST "$BASE/api/media/search" \
  -H "Content-Type: application/json" \
  -d '{"query":""}' | jq
```

**Atteso**: HTTP 400, messaggio "query is required".

---

## Test 2 — Hybrid search base

```bash
curl -s -X POST "$BASE/api/media/search" \
  -H "Content-Type: application/json" \
  -d '{"query": "boxing mayweather", "mode": "hybrid", "limit": 10}' | jq
```

**Atteso**: items array, next_cursor, partial boolean, provider_errors.

---

## Test 3 — ANN search

```bash
curl -s -X POST "$BASE/api/media/search" \
  -H "Content-Type: application/json" \
  -d '{"query": "boxing mayweather", "mode": "ann", "limit": 10}' | jq
```

**Atteso**: risultati semantici, nessun errore se Qdrant attivo, partial=true se backend fallisce.

---

## Test 4 — Filtro source YouTube

```bash
curl -s -X POST "$BASE/api/media/search" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "mayweather",
    "sources": ["youtube"],
    "filters": {"source": "youtube", "media_type": "video"},
    "limit": 10
  }' | jq
```

**Atteso**: tutti source=youtube, media_type=video.

---

## Test 5 — Filtro Voiceover audio

```bash
curl -s -X POST "$BASE/api/media/search" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "test voiceover",
    "sources": ["voiceover"],
    "filters": {"source": "voiceover", "media_type": "audio", "language": "it-IT"},
    "limit": 10
  }' | jq
```

**Atteso**: source voiceover, media_type audio, language it-IT.

---

## Test 6 — Filtro Artlist video

```bash
curl -s -X POST "$BASE/api/media/search" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "city night cinematic",
    "sources": ["artlist"],
    "filters": {"source": "artlist", "media_type": "video"},
    "limit": 10
  }' | jq
```

**Atteso**: source artlist, media_type video, solo ACTIVE.

---

## Test 7 — Cursor pagination

```bash
# Page 1
curl -s -X POST "$BASE/api/media/search" \
  -H "Content-Type: application/json" \
  -d '{"query": "boxing", "limit": 2}' | tee /tmp/search_page1.json | jq

# Page 2
CURSOR=$(jq -r '.next_cursor // empty' /tmp/search_page1.json)
curl -s -X POST "$BASE/api/media/search" \
  -H "Content-Type: application/json" \
  -d "{\"query\": \"boxing\", \"limit\": 2, \"cursor\": \"$CURSOR\"}" | jq
```

**Atteso**: seconda pagina diversa, nessun duplicato, cursor invalido → 422.

---

## Test 8 — Lifecycle ACTIVE only

**Atteso**: asset DELETED/STAGING/PROCESSING non appaiono nei risultati.

---

## Test 9 — Nessun locator nel payload search

```bash
curl -s -X POST "$BASE/api/media/search" \
  -H "Content-Type: application/json" \
  -d '{"query": "boxing", "limit": 5}' | jq '.items[] | {id,source,media_type,drive_link,local_path}'
```

**Atteso**: drive_link assente/null, local_path assente/null (SearchAdapter ha rimosso `LocalPath` e `DriveLink` dal DTO).

---

# Sequenza di esecuzione consigliata

```
1. Jobs smoke test (Test 1)
2. Jobs failed/cancel/retry (Test 3-6)
3. Outbox pending/completed/dead_letter/superseded (Test 1-7)
4. ScriptFlow generate semplice (Test 1)
5. ScriptFlow generate-from-clips (Test 2)
6. Images generate/ingest (Test 1-6)
7. Clips search/download/update/delete (Test 1-6)
8. Register from YouTube singolo/batch/duplicato (Test 1-4)
9. Search aggregata hybrid/ann/filtri/cursor (Test 1-9)
```

## Regola d'oro

```
HTTP 200/202 non basta.
Deve tornare coerente anche:
  jobs.status
  job_events
  media_assets
  outbox_events
  Drive file
  Qdrant point/search

Se uno strato dice "success" e un altro dice "missing/failed/running", lì c'è il bug vero.
```
