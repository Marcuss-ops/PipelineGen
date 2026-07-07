# Artlist DoD 100% — Action Plan (2026-07-07)

**Date:** 2026-07-07
**Status:** core_e2e_verified → target: 100% operativo
**Parent:** `ARTLIST-DOD-2026-07-07` (action plan companion)
**Repo:** direct-to-main, NO branches, push frequente

---

## Stato Attuale (Post Live Verification)

| Gate | Stato | Issue |
|------|-------|-------|
| 1-4 | ✅ PASS | Scraper, real run, SQLite, outbox |
| 5 | ⚠️ 1/2 | 61645 missing from Qdrant |
| 6 | ✅ PASS | index_state=INDEXED |
| 7 | ⚠️ 1/2 | Solo 450645 in Qdrant |
| 8 | ⚠️ | Search timeout backend locale |
| 9-12 | ❌ | Failure test + preflight non eseguiti |

---

## Azioni per il 100% (8 azioni in ordine)

### Azione 1: Reindicizzare 61645 in Qdrant
**File:** `internal/infrastructure/indexing/clipindexer/indexing_api.go`
**Descrizione:** 61645 è INDEXED in SQLite ma assente da Qdrant. Lanciare re-indexing manuale o investigare perché l'outbox completed non ha prodotto il punto.
**Verifica:** `curl Qdrant scroll` per asset_id=61645 deve restituire 1 punto.

### Azione 2: Fix search backend timeout
**File:** `internal/api/mediasearch/handler.go`, `internal/app/search_backend_provider.go` o `search_backend_local.go`
**Descrizione:** `POST /api/media/search` con source=artlist ritorna "no eligible backends". Senza source filter, il backend "local" va in timeout (`context deadline exceeded`). Fixare il wiring o la query SQLite.
**Verifica:** `curl /api/media/search mode=ann query=boxing sources=["artlist"]` deve restituire ≥1 risultato.

### Azione 3: Multi-query test (3+ keyword diverse)
**File:** `tests/operational/artlist_multi_query_smoke.sh` (NEW)
**Descrizione:** Eseguire dry_run per "boxing", "training", "fight", "gloves", "punch" e verificare che ≥3 keyword trovino clip.
**Verifica:** ≥3/5 keyword trovano ≥1 clip ciascuna.

### Azione 4: Re-run idempotente
**File:** `tests/operational/artlist_idempotent_smoke.sh` (NEW)
**Descrizione:** Eseguire due run reali consecutive con la stessa keyword "boxing". La seconda run deve essere idempotente (stessi clip_id, no duplicati, no errori).
**Verifica:** Second run SUCCEEDED, stessi asset_id, no nuovi duplicati in media_assets.

### Azione 5: Test Drive failure fail-closed
**File:** `tests/operational/artlist_drive_failure_smoke.sh` (NEW)
**Descrizione:** Rinominare temporaneamente `token.json`, eseguire run, verificare che il job fallisca con `UPLOAD_FAILED` e che `Processed` NON venga incrementato.
**Verifica:** Job status = FAILED, error contiene "drive" o "upload", processed=0.

### Azione 6: Test Qdrant failure fail-closed
**File:** `tests/operational/artlist_qdrant_failure_smoke.sh` (NEW)
**Descrizione:** Fermare Qdrant (`docker stop`), eseguire run, verificare che outbox NON segni completed senza scrittura reale.
**Verifica:** outbox status = pending/retry, media_assets.index_state != INDEXED.

### Azione 7: Test Scraper failure
**File:** `tests/operational/artlist_scraper_failure_smoke.sh` (NEW)
**Descrizione:** Killare Node scraper, chiamare `/api/artlist/search/live`, verificare risposta errore/503.
**Verifica:** HTTP status != 200, messaggio errore chiaro.

### Azione 8: Preflight automation
**File:** `cmd/admin/qdrant_preflight.go`
**Descrizione:** Riempire i test stub 3-8, 10, 11 con verifiche reali per Artlist.
**Verifica:** `go run ./cmd/admin qdrant-preflight` exit 0.

---

## Execution Order

```
Step 1 (NOW): Azione 1 (reindex 61645) → sblocca Gate 5+7
Step 2 (NOW): Azione 2 (fix search) → sblocca Gate 8
Step 3 (TODAY): Azione 3 (multi-query) + Azione 4 (idempotent)
Step 4 (TODAY): Commit e push dopo ogni azione
Step 5 (THIS WEEK): Azione 5-6-7 (failure tests)
Step 6 (THIS WEEK): Azione 8 (preflight)
Step 7: Wave flip ARTLIST-DOD-2026-07-07 → status: shipped
```

---

## Per-Action Commit Convention

Ogni azione segue il pattern:
```bash
git add <files> && git commit -m 'feat/fix/test(artlist): <azione N> — <descrizione breve>'
git fetch origin && git rebase origin/main && git push origin main
```

**NO branches. NO --force. NO PR.** Direct-to-main per AGENTS.md Git-Lesson-2.

---
**End of action plan.** Esegui le 8 azioni nell'ordine sopra. Dopo ogni azione, commit + push diretto su main.
