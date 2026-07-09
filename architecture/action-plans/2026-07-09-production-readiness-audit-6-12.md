# Production Readiness Audit §6-§12 — 2026-07-09

> **Canonical lockstep (per CANONICAL.md §1):** this file ≡
> `CHANGELOG.md ## Unreleased → ### Documentation` ≡
> AGENTS.md mirror entry.

## §0 — Scope (godlike/07 NO-FAKE-AVAILABILITY)

Piano d'azione derivato dall'audit di produzione di Marcuss-ops (2026-07-09).
Coperchia 7 aree critiche (§6-§12) che devono essere verificate PRIMA del
deploy in produzione. Ogni area ha azioni concrete con comandi, criteri di
successo/errore e forward-pointers per le fix.

**Honest scope-lock:** questo documento è puramente OPERATIVO — definisce
cosa testare e come, non implementa codice. Le fix necessarie sono
documentate come PR forward-pointers.

---

## §6 — YouTube Register / Clip Ingestion

### Cosa testare
| Scenario | Comando | Criterio PASS |
|----------|---------|---------------|
| Register singolo URL | `POST /api/media/register-from-youtube` con URL valido | 200/202 + job_id + outbox event creato |
| Register batch | `POST /api/media/register-batch` con 3-5 URL | Tutti i job completano; 0 duplicati |
| Stesso video 2 volte | Register identico 2x | SECONDO call → idempotency o errore chiaro (MAI 10 asset duplicati) |
| URL invalido | URL con video_id inesistente | 400 o errore leggibile; MAI outbox event per URL invalido |
| Download fallito | Video privato/rimosso | Errore chiaro; job FAILED; 0 outbox event "orphaned" |
| Drive upload fallito | Simula quota Drive esaurita | Errore chiaro; media_assets NON scritto senza drive_file_id |

### Comando base
```bash
curl -s -X POST "$BASE/api/media/register-from-youtube" \
  -H "Authorization: Bearer $VELOX_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://www.youtube.com/watch?v=9u4T_o3FxOU",
    "tags": ["boxing", "test"],
    "source": "youtube",
    "metadata": {"test_run": "register_single_001"}
  }' | jq
```

### Verifica DB post-register
```bash
sqlite3 "$DB" "
SELECT id, source, media_type, drive_file_id, file_hash,
       index_state, lifecycle_state, created_at
FROM media_assets
WHERE source = 'youtube'
ORDER BY created_at DESC
LIMIT 10;
"
```

### 🔴 Rosso se
- Stesso video crea 10 asset duplicati
- URL invalido crea outbox event
- Batch fallisce tutto per un solo item rotto
- `drive_file_id` vuoto in `media_assets` dopo successo

### PR forward-pointers
- `PR-REGISTER-DUPLICATE-GUARD` (deadline 2026-08-15) — dedup guard su video_id+start+end
- `PR-REGISTER-BATCH-ATOMIC` (deadline 2026-08-15) — batch deve essere all-or-nothing per ogni item

---

## §7 — ScriptFlow (generate canonico)

### Cosa testare
| Scenario | Endpoint | Criterio PASS |
|----------|----------|---------------|
| Generate semplice | `POST /api/script/generate` | 202 + job_id + status_url |
| Generate da clip reali | generate con `source_type=clips` | Script completo + clip bindings popolati |
| Job status coerente | `/api/script/jobs/:id` vs `/api/jobs/:id/full` | Stessi campi; 0 divergenze |
| `create_doc=false` | Flag esplicito | 200; 0 Google Doc creato |
| `create_doc=true` | Flag esplicito | Doc creato con link restituito O errore chiaro |
| `images=false`, `voiceover=false` | Flag combinati | 200; nessun crash; script produce output base |
| Endpoint legacy 410 | `POST /api/script/generate-from-clips` (old) | 410 Gone + `LegacyDeprecationPayload` body |

### Comando base
```bash
curl -s -X POST "$BASE/api/script/generate" \
  -H "Authorization: Bearer $VELOX_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Test script PipelineGen",
    "language": "it",
    "tone": "informativo",
    "duration_seconds": 300,
    "output": {
      "generate_voiceover": false,
      "generate_scene_images": false,
      "generate_document": false,
      "save_to_db": true
    }
  }' | jq
```

### Verifica post-generate
```bash
# Job status
curl -s "$BASE/api/jobs/$JOB_ID/full" \
  -H "Authorization: Bearer $VELOX_ADMIN_TOKEN" | jq '.status'

# Script row
sqlite3 "$DB" "
SELECT id, title, language, status, created_at
FROM scripts
ORDER BY created_at DESC LIMIT 5;
"
```

### 🔴 Rosso se
- `/api/script/generate` restituisce 500 senza messaggio chiaro
- `create_doc=true` restituisce successo senza Drive link
- Job status diverge tra i 2 endpoint
- Endpoint legacy restituisce 200 (invece di 410)

### PR forward-pointers
- `PR-SCRIPTCONTRACT-CI-GATE-CHECK-64` (shipped) — processor order gate
- `PR-SCRIPTCONTRACT-COMPOSITION-WIRE` (shipped) — PreflightCaps wiring

---

## §8 — Artlist / Stock / Search Live

### Cosa testare
| Scenario | Comando/Query | Criterio PASS |
|----------|---------------|---------------|
| Artlist diagnostics | `GET /api/artlist/diagnostics` | 200; scraper reachable |
| Artlist search live | `POST /api/artlist/search` | 200; risultati non-vuoti |
| Artlist run pipeline | `POST /api/artlist/run` | 202; job SUCCEEDED; media_assets popolato |
| Stock pipeline | `POST /api/stock-pipeline/run` | 202; clips scaricati; Drive upload OK |
| Fallback Pexels/Pixabay | Config con provider alternativo | Risultati dal fallback; non crash |
| Qdrant indexing | Dopo run → query Qdrant | Point trovato; payload popolato |
| Download | `POST /api/stock-pipeline/clips/:id/download` | MP4 >100KB; ffprobe video stream |
| DB update | Query media_assets post-run | `drive_file_id`, `file_hash`, `index_state` popolati |

### Verifica DB post-stock
```bash
sqlite3 "$DB" "
SELECT id, source, media_type, drive_file_id, file_hash,
       index_state, lifecycle_state
FROM media_assets
WHERE source LIKE '%artlist%' OR source LIKE '%stock%'
ORDER BY created_at DESC
LIMIT 20;
"
```

### Verifica outbox post-stock
```bash
sqlite3 "$DB" "
SELECT id, event_type, status, last_error, created_at
FROM outbox_events
WHERE event_type = 'asset.index.requested'
ORDER BY created_at DESC
LIMIT 10;
"
```

### 🔴 Rosso se ("success finto")
- Artlist risponde OK ma 0 clip scaricate
- `media_assets.drive_file_id` vuoto dopo SUCCEEDED
- Qdrant non aggiornato (point assente dopo outbox completed)
- `index_state` stuck in INDEXING_PENDING indefinitamente
- Drive vuoto dopo "upload succeeded"

---

## §9 — Cache e Cleanup

### Cosa testare
| Scenario | Comando | Criterio PASS |
|----------|---------|---------------|
| Temporanei non crescono | `find /tmp -iname "*pipelinegen*" -o -iname "*image*"` | Lista ragionevole; 0 profili Chromium appesi |
| Cache tables esistono | `sqlite3 "$DB" "SELECT name FROM sqlite_master WHERE type='table' AND name LIKE '%cache%';"` | Tabelle presenti |
| Cache scadute ignorate | Inserisci cache vecchia + verifica | Non viene usata come se fosse fresca |
| Orphan cleanup | Dopo job FAILED → verifica Drive | File Drive orfani puliti dall'outbox `voiceover.cleanup.requested` |
| Stock residue | Dopo multipli stock run | 0 cartelle Drive duplicate/empty |

### Comandi diagnostici
```bash
# Temporanei
find /tmp -iname "*pipelinegen*" -o -iname "*image*" 2>/dev/null | head -50

# Cache tables
sqlite3 "$DB" "
SELECT name FROM sqlite_master
WHERE type='table' AND name LIKE '%cache%';
"

# Orphaned Drive files (manual check)
# Vedi scripts/cleanup_stock_residue.py --mode=inventory

# Profili Chromium
ls -la /tmp/puppeteer_* 2>/dev/null | wc -l
```

### 🔴 Rosso se
- `/tmp` cresce senza limite dopo 10+ job
- Profili Chromium restano appesi (100+ dir)
- Cache vecchie vengono usate come se fossero fresche
- Voiceover orphan cleanup non emette outbox event

---

## §10 — Feature Flags / Route Disabilitate

### Regola fondamentale
Ogni feature opzionale DEVE fare UNA di queste cose:

| Stato | Comportamento atteso |
|-------|---------------------|
| Abilitata + cablata | 200/202 |
| Abilitata ma non cablata | 503 chiaro con messaggio |
| Disabilitata | Route assente o 404 |

### Cosa testare
| Feature Flag | Test | Criterio PASS |
|-------------|------|---------------|
| `ArtlistEnabled=true` + URL presente | `POST /api/artlist/run` | 202 |
| `ArtlistEnabled=true` + URL vuoto | Boot → `/ready` | Fail-closed al boot (DL-006) |
| `ArtlistEnabled=false` | Route non montata | 404 |
| `ScriptDocsEnabled=true` + port nil | `POST /api/script-docs/generate` | 503 `ErrReActNotWired` |
| `ScriptDocsEnabled=false` | Route | 404 o non montata |
| Qdrant `enabled=true` + `clipindexer=false` | IndexClip path | `INDEXING_SKIPPED_NO_INDEXER` state |
| Voiceover service nil + `generate_voiceover=true` | `POST /api/script/generate` | 503 `ErrPreflightProcessorMissing` |

### 🔴 Rosso se ("fake availability")
- Endpoint risponde 200 senza provider cablato
- `enabled=true` con `ScraperServerURL=""` → crash silenzioso al boot
- Feature flag non ha effetto sulla route

### PR forward-pointers
- `PR-QDRANT-INDEXCLIP-GUARD` (shipped) — fail-closed on IndexClip disabled
- `ART-002 DL-006` (shipped) — composition-root fail-closed gate

---

## §11 — Auth / Admin / Sicurezza Minima

### Cosa testare
| Scenario | Test | Criterio PASS |
|----------|------|---------------|
| Admin token richiesto | Request senza token su endpoint admin | 401 Unauthorized |
| Endpoint interni non pubblici | `/ready`, `/healthz` accessibili; `/debug/*` protetto | Sì |
| Nessun `local_path` nel JSON pubblico | `grep local_path` sulle risposte API | 0 hit |
| Nessun secret nei log | `grep "client_secret\|refresh_token\|private_key"` | 0 hit |
| Nessun stack trace al client | Forza errore 500 | Messaggio opaco, 0 file path |
| Drive credential non in log | `grep "credentials\|token.json"` su log | 0 hit |
| CORS | Verifica header CORS | Solo se esplicitamente necessario |

### Comandi diagnostici
```bash
# Leak search nei log
grep -R "client_secret\|refresh_token\|private_key\|local_path\|credentials" \
  logs/ data/ .env* 2>/dev/null | head -50

# Token check
grep -R "VELOX_ADMIN_TOKEN" logs/ 2>/dev/null | grep -v "^\s*#" | head -10

# Stack trace check (forza errore)
curl -s "$BASE/api/script/generate" \
  -H "Content-Type: application/json" \
  -d '{"invalid": true}' | jq 'keys'
```

### 🔴 Rosso se
- Endpoint admin accessibile senza token
- Response JSON contiene `local_path` o filesystem path
- Log contiene `client_secret` / `refresh_token` / `private_key`
- Stack trace completo con file path nel JSON response

### PR forward-pointers
- `PR-AUTH-CREDENTIAL-HELPER-SETUP` (shipped) — README Git credential helper docs
- `PR-PROCESS-SECURITY-REVIEW` (shipped) — CWD hijacking fix + secret redaction

---

## §12 — Concorrenza Generale

### Cosa testare (carico concorrente)
| Scenario | Quantità | Criterio PASS |
|----------|----------|---------------|
| Generazioni immagini | 5 parallele | 0 `database is locked`; 0 panic |
| Ricerche media | 5 parallele | 0 timeout; risultati coerenti |
| Script generate | 3 parallele | Tutti completano; 0 `nil pointer` |
| Register YouTube | 3 parallele | 0 duplicati; 0 `context deadline` |
| Artlist run | 2 parallele | 0 profile lock; 0 crash |
| Stock pipeline | 3 parallele con clip diversi | 0 Drive folder collision |

### Comando batch concorrenza
```bash
# Esempio: 3 script generate paralleli
for i in 1 2 3; do
  curl -s -X POST "$BASE/api/script/generate" \
    -H "Authorization: Bearer $VELOX_ADMIN_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"title\": \"Test concorrenza $i\", \"language\": \"it\", \"tone\": \"informativo\", \"duration_seconds\": 120}" \
    | jq -r '.job_id' &
done
wait
```

### Post-concorrenza check
```bash
# Errori nel log
grep -R "database is locked\|panic\|fatal\|nil pointer\|context deadline\|profile lock" \
  logs/ 2>/dev/null | tail -20

# Job status coerente
sqlite3 "$DB" "
SELECT status, COUNT(*)
FROM jobs
WHERE created_at > datetime('now', '-1 hour')
GROUP BY status;
"
```

### 🔴 Rosso se
- `database is locked` in qualsiasi log
- `panic` o `nil pointer` durante operazioni parallele
- `context deadline` su operazione che normalmente impiega <30s
- `profile lock` su Chromium (profili che si incastrano)
- Job rimane in RUNNING indefinitamente dopo timeout

### PR forward-pointers
- WAL mode + `busy_timeout=5000` già attivi (documented in AGENTS.md)
- `pkg/concurrent.WithContext` — first-error-wins + panic recovery

---

## §13 — Execution Order

```
Fase 1 (P0, immediato):
  §11 Auth/sicurezza        ← PRIMA di tutto
  §10 Feature flags         ← verifica composizione

Fase 2 (P0, prima del deploy):
  §7  ScriptFlow            ← endpoint canonico
  §6  YouTube register      ← ingest pipeline
  §12 Concorrenza           ← stress test

Fase 3 (P1, post-deploy):
  §8  Artlist/stock         ← pipeline completa
  §9  Cache/cleanup         ← hygiene

Fase 4 (P2, monitoraggio):
  §11 (controlli ripetuti)  ← regressione auth
  §12 (controlli ripetuti)  ← regressione concorrenza
```

---

## §14 — Lifecycle Audit-trail

| Data | Evento |
|------|--------|
| 2026-07-09 | Action plan creato da audit Marcuss-ops §6-§12 |
| TBD | Fase 1 completata (Auth + Feature flags) |
| TBD | Fase 2 completata (ScriptFlow + YouTube + Concorrenza) |
| TBD | Fase 3 completata (Artlist/Stock + Cache) |
| TBD | Deploy autorizzato |

---

## §15 — Cross-references

- `architecture/action-plans/2026-07-08-youtube-clip-dod-action-plan.md` — YouTube DoD
- `architecture/action-plans/2026-07-08-script-pipeline-contract.md` — ScriptFlow contract
- `architecture/action-plans/2026-07-08-stock-clips-cleanup.md` — Stock cleanup
- `architecture/action-plans/2026-07-08-pattern-12-completion.md` — Drive as Central
- `docs/operations/stock-e2e-runbook.md` — Stock E2E runbook
- `docs/operations/04-remote-worker-production-readiness-tickets.md` — Worker readiness
- `architecture/action-plans/2026-07-09-script-docs-reAct-online-test-plan.md` — ScriptDocs test

---

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>
Co-authored-by: Marcuss-ops
AGENTS.md Git-Lesson-3.
