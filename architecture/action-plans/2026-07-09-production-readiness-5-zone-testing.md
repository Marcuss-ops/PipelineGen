# Production Readiness 5-Zone Testing Plan

> **Date**: 2026-07-09
> **Author**: Marcuss-ops (audit) + PipelineGen Agent (action plan)
> **Rule**: NO branches, direct-to-main per AGENTS.md Git-Lesson-2
> **Principle**: HTTP 200/202 non basta. Ogni test deve verificare tutta la catena:
> HTTP → job/outbox → DB → file/Drive/Qdrant → stato finale.

---

## §0 Status snapshot

5 zone critiche da testare prima della produzione. Ogni zona ha test
specifici con condizioni di fallimento ("Rosso se") documentate.

---

## §1 Zona 1: Jobs / Worker / Runner (P0)

**Priorità**: CRITICA — se la job queue non è solida, tutto il resto
può sembrare funzionare ma poi rimanere appeso.

### Test da fare

1. **Stats baseline**: `curl -s "$BASE/api/jobs/stats" | jq`
2. **Enqueue + polling**: enqueue job valido → polling fino a SUCCEEDED
3. **Job invalido → FAILED**: job con payload errato deve andare a FAILED
4. **Cancel durante RUNNING**: cancellare un job in esecuzione
5. **Retry di FAILED**: un job FAILED deve poter essere ri-retried
6. **Retry di SUCCEEDED deve fallire**: idempotency check
7. **Stats API coerenti col DB**: confronto API stats vs `sqlite3 DB "SELECT status,COUNT(*) FROM jobs GROUP BY status"`

### Rosso se

- Job resta queued/running per sempre
- progress=100 ma status ancora RUNNING
- SUCCEEDED con error
- Retry di un job già riuscito rigenera file duplicati

### Route da verificare

- `POST /api/jobs` — enqueue
- `GET /api/jobs` — list
- `GET /api/jobs/stats` — statistics
- `GET /api/jobs/:id/full` — full detail
- `POST /api/jobs/:id/cancel` — cancel
- `POST /api/jobs/:id/retry` — retry
- `GET /api/jobs/:id/events` — events

### Canonic owner

- `internal/application/jobs/` — job broker spine
- `internal/infrastructure/database/sqlite/jobs/` — SQLite store
- `internal/api/` — HTTP transport

---

## §2 Zona 2: Outbox (P0)

**Priorità**: CRITICA — l'outbox collega asset, indicizzazione, Qdrant,
webhook, cleanup e side effects.

### Test base

```bash
sqlite3 "$DB" "
SELECT id,event_type,aggregate_id,status,attempt_count,max_attempts,
       last_error,event_key,created_at,updated_at
FROM outbox_events
ORDER BY id DESC LIMIT 20;
"
```

### Transizioni attese

- `pending → processing → completed`
- In caso di errore: `pending/retry → dead_letter`
- Stati canonici: pending, processing, completed, dead_letter, superseded

### Rosso se

- Eventi processing appesi (stuck in processing)
- completed con last_error non vuoto
- Duplicati su event_key
- Qdrant spento ma evento segnato come completato (silent-success)

### Canonic owner

- `internal/application/jobs/outbox/` — outbox handler/dispatcher
- `internal/infrastructure/database/sqlite/outboxevents/` — repository
- `internal/domain/asset/index_state.go` — state machine

---

## §3 Zona 3: Media Assets + Drive + Qdrant (P0)

**Priorità**: CRITICA — ogni asset deve esistere in tutti gli strati.

### Test base — DB

```bash
sqlite3 "$DB" "
SELECT id,source,media_type,filename,drive_file_id,file_hash,
       lifecycle_state,index_state,created_at
FROM media_assets
ORDER BY created_at DESC LIMIT 20;
"
```

### Test base — Outbox per asset

```bash
sqlite3 "$DB" "
SELECT id,event_type,aggregate_id,status,last_error,payload_json
FROM outbox_events
WHERE event_type='asset.index.requested'
ORDER BY id DESC LIMIT 20;
"
```

### Invarianti per asset generato

- media_type corretto (image/video/audio/voiceover)
- drive_file_id non vuoto
- file_hash non vuoto
- lifecycle_state=ACTIVE
- outbox asset.index.requested emesso

### Rosso se

- File su Drive ma assente in DB
- DB ACTIVE ma Qdrant non indicizzato
- file_hash vuoto
- Asset cancellato che appare ancora nella search

### Canonic owner

- `internal/application/assets/lifecycle/` — lifecycle service
- `internal/infrastructure/drive/` — Drive uploader
- `internal/infrastructure/database/sqlite/assets/` — media_assets repo

---

## §4 Zona 4: Qdrant / Indicizzazione Semantica (P1)

**Priorità**: ALTA — verifica che i punti siano dentro Qdrant dopo
generazione/upload.

### Test base — Qdrant scroll

```bash
curl -s http://localhost:6333/collections/media_assets_current/points/scroll \
  -H "Content-Type: application/json" \
  -d '{
    "limit": 5,
    "with_payload": true,
    "with_vector": false
  }' | jq
```

### Payload atteso per immagine

- `source` coerente (youtube/artlist/stock/generated)
- `media_type=image`
- `lifecycle_state=ACTIVE`

### Rosso se

- Search API trova l'asset ma Qdrant no
- Qdrant contiene asset DELETED
- Payload con campi mancanti (source, media_type, lifecycle_state)

### Canonic owner

- `internal/infrastructure/qdrant/indexing/` — IndexWriter
- `internal/application/jobs/outbox/indexing.go` — IndexingHandler
- `internal/infrastructure/qdrant/search/` — SearchAdapter

---

## §5 Zona 5: Search Aggregata (P1)

**Priorità**: ALTA — la zona che ti dice se i tuoi asset sono
davvero riutilizzabili.

### Test base

```bash
curl -s -X POST "$BASE/api/media/search" \
  -H "Content-Type: application/json" \
  -d '{"query": "boxing mayweather", "mode": "hybrid", "limit": 10}' | jq
```

### Test da fare

1. Query vuota → 400
2. Mode hybrid
3. Mode ann
4. Filter source=youtube
5. Filter source=artlist
6. Filter media_type=audio
7. Cursor pagination
8. Asset DELETED/STAGING non devono uscire

### Rosso se

- Risultati duplicati
- Cursor ripete la stessa pagina
- Asset cancellati compaiono
- drive_link/local_path finiscono nel payload search

### Canonic owner

- `internal/api/assets/` — search handler
- `internal/application/search/` — search service
- `internal/infrastructure/qdrant/search/` — Qdrant backend

---

## §6 Execution order

| Fase | Zona | Deadline | Status |
|------|------|----------|--------|
| 1 | Jobs / Worker / Runner | 2026-07-15 | pending |
| 2 | Outbox | 2026-07-15 | pending |
| 3 | Media Assets + Drive + Qdrant | 2026-07-22 | pending |
| 4 | Qdrant semantica | 2026-07-22 | pending |
| 5 | Search aggregata | 2026-07-29 | pending |

## §7 Honest scope-lock

- Ogni zona ha 1 shell smoke operator-facing + 1 Go hermetic test
- I test devono girare CONTRO un server live (port 8000)
- Rosso = forward-pointer PR canonico, non bypass
- Wave-flip a `shipped` solo quando TUTTE le zone sono verdi

## §8 Cross-references

- `architecture/current.yaml#STOCK-E2E-BATTERY-2026-07-05` — stock pipeline E2E
- `architecture/action-plans/2026-07-04-qdrant-verification-chain.md` — Qdrant chain
- `architecture/action-plans/2026-07-08-qdrant-dod-final.md` — Qdrant DoD finale
- `docs/operations/qdrant-verification-runbook.md` — Qdrant runbook
- `docs/operations/stock-e2e-runbook.md` — stock E2E runbook
