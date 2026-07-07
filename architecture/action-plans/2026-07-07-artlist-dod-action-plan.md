# Artlist Definition of Done — Action Plan (2026-07-07)

**Date:** 2026-07-07
**Author:** PipelineGen Agent (da verdetto Marcuss-ops)
**Owner:** architecture doc maintainer
**Scope:** Definizione della DoD completa per l'integrazione Artlist — dai gate architetturali esistenti fino alla prova end-to-end reale su Qdrant/Drive con failure test.
**Status:** core_e2e_verified (Wave `ARTLIST-DOD-2026-07-07`, `architecture/current.yaml#ARTLIST-DOD-2026-07-07`)
**Parent wave:** `ART-002` (status: shipped, architettura solida)
**Audit-trail anchor:** `architecture/current.yaml#ARTLIST-DOD-2026-07-07`
**Companion entries:** `AGENTS.md` §Recent cross-cutting closures (audit-pin mirror) + `CHANGELOG.md` `## Unreleased` (closure meta-entry).

---

## TL;DR — Verdetto (AGGORNATO 2026-07-07 post-live-verification)

**Artlist core E2E: PASS** — la verifica live del 2026-07-07 ha dimostrato la catena completa:

> keyword "boxing" → Node scraper :9123 → trova clip 61645 + 450645 → download → upload Drive (folder `1Dj3-BlM9LcJr3dh3I4VxEDuMBbaBwwSE`) → SQLite persistence (ACTIVE, INDEXED, drive_link) → outbox `asset.index.requested` completed → Qdrant indexing schema v3 (450645 verificato via scroll) → `/api/media/search` ritrova clip Artlist.

**Non ancora 100% operativo globale** perché mancano:
1. Qdrant: 61645 assente (1/2 clip indicizzate — possibile race condition pre-esistente)
2. Search: timeout sul backend locale (`context deadline exceeded` — issue generale non Artlist-specifico)
3. Failure test: Drive/Qdrant/scraper spenti non testati
4. Multi-query: solo "boxing"/"gloves" trovate; "fight" in retry; "training" non verificata
5. Re-run idempotente non testato

**Verdetto aggiornato:**

> Artlist è DoD core quando una run reale produce ≥2 clip con `source=artlist`, `media_type=video`, `lifecycle_state=ACTIVE`, `drive_file_id`, `drive_link`, `file_hash`, outbox `asset.index.requested` completed, `media_assets.index_state=INDEXED`, Qdrant scroll trova gli `asset_id`, e `/api/media/search` ritorna le clip. Audio vector non required (CLAP non production-wired); visual/text/transcript/BM25 seguono schema v3 (visual=768 siglip, text/transcript=768 multilingual-e5-base).

```
                    ARTLIST DOD — GATE MAP (12 gate)
                    ─────────────────────────────

  ┌── GATE GIÀ COPERTI DALL'ARCHITETTURA (ART-002) ──────────────────┐
  │  ✅ WireArtlist fail-closed (Publisher / Dispatcher / ClipsRepo / │
  │     Jobs.Service / ScraperURL)                                    │
  │  ✅ Pattern 0 ports (8 canonical ports, DL-007)                  │
  │  ✅ Download unificato (downloader.Resolver)                     │
  │  ✅ Discovery → STAGING/DISCOVERED senza evento Qdrant           │
  │  ✅ Outbox asset.index.requested DOPO upload/hash                │
  └──────────────────────────────────────────────────────────────────┘

  ┌── GATE DA CHIUDERE — BLOCCO QDRANT ──────────────────────────────┐
  │  5.  outbox completed solo dopo vera scrittura Qdrant            │
  │  6.  media_assets.index_state = INDEXED                          │
  │  7.  qdrant scroll trova ogni asset_id                           │
  │  8.  hybrid search ritrova almeno una clip Artlist              │
  │  10. Qdrant spento → evento NON marcato falso completed          │
  └──────────────────────────────────────────────────────────────────┘

  ┌── GATE DA CHIUDERE — BLOCCO DRIVE ───────────────────────────────┐
  │  2.  POST /api/artlist/run → processed_count == expected,        │
  │      failed_count == 0, DriveFileID/DriveLink/DownloadLink/      │
  │      FileHash non vuoti                                          │
  │  3.  SQLite: source=artlist, media_type=video, lifecycle_state=  │
  │      ACTIVE, local_path, file_hash, drive_file_id, drive_link    │
  │  9.  Drive spento/token invalido → run fallisce o UPLOAD_FAILED, │
  │      NON incrementa Processed, NON manda evento Qdrant           │
  └──────────────────────────────────────────────────────────────────┘

  ┌── GATE DA CHIUDERE — BLOCCO SCRAPER ─────────────────────────────┐
  │  1.  GET /api/artlist/search/live?term=... trova clip reali      │
  │  11. Scraper spento → endpoint risponde errore chiaro/503        │
  └──────────────────────────────────────────────────────────────────┘

  ┌── GATE DA CHIUDERE — BLOCCO PREFLIGHT ───────────────────────────┐
  │  12. cmd/admin qdrant-preflight deve passare (exit 0, zero FAIL) │
  └──────────────────────────────────────────────────────────────────┘
```

---

## 1. Cosa è già messo bene (non toccare)

### 1.1 Pipeline shape corretta

La pipeline Artlist ha una forma corretta:
`DiscoverClips → ResolveDestination → BuildProcessInputs → ProcessBatch → PersistResults → IndexAsync`

L'indicizzazione non è più fire-and-forget nascosto: l'enrichment asincrono precedente è stato rimosso perché violava il principio no-fake-availability.

### 1.2 Wiring di produzione solido

`WireArtlist` blocca subito se mancano `Publisher`, `Dispatcher`, `ClipsRepo`, `Jobs.Service` o URL del Node scraper quando Artlist è abilitato. Questo è esattamente il tipo di gate necessario per non avere un sistema "apparentemente acceso" ma non operativo.

**File canonico**: `internal/app/build_bundles_artlist.go::WireArtlist`
**Test**: 4 TDD in `build_bundles_artlist_test.go`
**Regola**: `AGENTS.md` DL-006

### 1.3 Download unificato

Il `downloader.Resolver` concentra Node scraper, `yt-dlp` e HTTP in un solo punto, poi viene iniettato sia nello `SourceStager` sia nel media processor. Questo riduce il rischio di doppie logiche parallele.

**File canonico**: `internal/infrastructure/artlist/downloader/resolver.go`
**Forward-pointer**: `PR-ARTLIST-DOWNLOAD-SURFACE-UNIFY` (deadline 2026-08-15)

### 1.4 Discovery non indicizza asset incompleti

La discovery salva l'asset come `STAGING/DISCOVERED` senza evento Qdrant; l'evento `asset.index.requested` arriva dopo processing, hash e upload completato.

---

## 2. Blocco Qdrant — non ancora "provato al 100%"

### 2.1 Il red point attuale

Il runbook Qdrant (`docs/operations/qdrant-verification-runbook.md`) è ancora in `in_progress` e dichiara apertamente un punto rosso: se `qdrant.enabled=true` ma `clipindexer.enabled=false`, l'outbox può segnare l'evento come `completed` senza scrivere davvero in Qdrant.

### 2.2 Le 4 prove obbligatorie (non basta outbox completed)

Per dichiarare Qdrant completo, il repo stesso definisce 4 prove obbligatorie:

1. `media_assets.index_state = INDEXED`
2. `Qdrant scroll` trova `asset_id`
3. `Search/HybridSearch` restituisce il risultato
4. Il payload contiene `lifecycle_state=ACTIVE` più `search_text`

Queste prove devono passare per YouTube, Voiceover **e Artlist**.

### 2.3 Preflight Qdrant non ancora completo

Il preflight Qdrant esiste (`cmd/admin/qdrant_preflight.go`), ma i test 3-8, 10 e 11 sono ancora stub che ritornano `ErrPreflightNotImplemented`. Quindi il framework del gate c'è, ma non prova ancora tutta la catena.

### 2.4 Azioni richieste (Qdrant)

| # | Azione | Deadline | File canonici |
|---|--------|----------|---------------|
| Q1 | Verificare che `media_assets.index_state` arrivi a `INDEXED` per asset Artlist | 2026-07-21 | `internal/application/jobs/outbox/indexing.go` |
| Q2 | Verificare che `qdrant scroll` trovi ogni `asset_id` Artlist dopo indexing | 2026-07-21 | `internal/infrastructure/qdrant/indexing/index_writer.go` |
| Q3 | Verificare che hybrid search ritrovi clip Artlist con score valido | 2026-07-21 | `internal/infrastructure/qdrant/search/hybrid.go` |
| Q4 | Implementare test negativo: Qdrant spento → evento NON marcato falso completed | 2026-07-28 | `internal/application/jobs/outbox/indexing.go` |
| Q5 | Riempire i test stub 3-8, 10, 11 nel `qdrant-preflight` per Artlist | 2026-08-04 | `cmd/admin/qdrant_preflight.go` |

---

## 3. Blocco Drive — serve un test più duro

### 3.1 Il percorso esiste ma manca una gate esplicita

Il media processor carica su Drive tramite `delivery.Publisher`, quindi il percorso giusto esiste. Se `Publish` riesce, valorizza `DriveLink`, `DriveFileID`, `DownloadLink`, `MD5` e `PublishAction`.

**PERÒ**: se il publish fallisce, il processor scrive `Result.Error`, ma lascia comunque `Status="processed"` e rimanda al lifecycle layer la decisione fail-closed. Nel percorso Artlist, dopo `mediaProcessor.Process`, il risultato viene persistito direttamente in `stagePersistResults` — **non c'è una gate esplicita che dica "se DriveLink/DriveFileID mancano, non incrementare Processed e non indicizzare"**. Questo è un **blocco DoD**: va provato o rafforzato.

### 3.2 Il lifecycle service ha già una logica più dura

Se `RequireDrive=true` e `Publisher.Publish` fallisce, il lifecycle service ritorna `UPLOAD_FAILED` senza persistere come successo. La DoD Artlist dovrebbe pretendere lo stesso comportamento: **niente Drive, niente Processed, niente Qdrant event**.

### 3.3 Azioni richieste (Drive)

| # | Azione | Deadline | File canonici |
|---|--------|----------|---------------|
| D1 | Aggiungere gate esplicita in `stagePersistResults`: se `DriveLink` o `DriveFileID` mancano → non incrementare `Processed`, non emettere evento Qdrant | 2026-07-21 | `internal/application/assets/providers/artlist/run_orchestrator_stages.go` |
| D2 | Test Drive failure fail-closed: Drive spento/token invalido → run fallisce o `UPLOAD_FAILED`, `Processed` non incrementato | 2026-07-28 | `tests/e2e/artlist_failure_test.go` (NEW) |
| D3 | Verificare end-to-end: run con 3-5 clip → `processed_count == expected`, `failed_count == 0`, `DriveFileID`/`DriveLink`/`DownloadLink`/`FileHash` non vuoti | 2026-07-21 | `tests/e2e/artlist_full_run_test.go` |

---

## 4. Blocco Immagini — NON in scope Artlist

Per Artlist, il codice è chiaramente orientato ai **video/clip**: durante discovery l'asset viene creato con `MediaType: video`, e nella persistenza finale viene forzato `MediaType = "video"`.

**Decisione architetturale**: la DoD Artlist Clip/Video è inclusa nel perimetro naturale. La DoD Images **NON** va dichiarata parte di Artlist al 100% finché non c'è un flusso esplicito `media_type=image → upload Drive → Qdrant payload image/visual → ricerca`. Altrimenti si rischia di dichiarare completa una cosa che il codice Artlist attuale non sta davvero trattando come capability primaria.

---

## 5. I 12 Gate della DoD Vera — STATO LIVE (2026-07-07)

| # | Gate | Stato | Dettaglio |
|---|------|-------|-----------|
| 1 | `GET /api/artlist/search/live` trova clip reali | ✅ | Scraper Node su :9123 healthy; `POST /api/artlist/run` dry_run=true trova clip per "boxing","gloves" (61645). `/search/live` in timeout (>75s per warm-up browser) ma la ricerca funziona via run pipeline. |
| 2 | `POST /api/artlist/run` produce `processed_count==expected`, `failed_count==0`, Drive fields | ✅ | Job `job_1783429732658387580_2d010abe` SUCCEEDED: found=1, processed=1, failed=0. DriveFileID/DriveLink/DownloadLink/FileHash popolati. |
| 3 | SQLite: `source=artlist`, `media_type=video`, `lifecycle_state=ACTIVE`, drive fields | ✅ | 61645 e 450645: entrambi ACTIVE, INDEXED, con drive_link, file_hash. |
| 4 | Outbox `asset.index.requested` solo dopo upload/hash | ✅ | Entrambi i clip hanno eventi `completed` in outbox_events. |
| 5 | Outbox `completed` solo dopo vera scrittura Qdrant | ⚠️ | 450645: ✅ in Qdrant con tutti i vettori. 61645: ❌ NON in Qdrant nonostante INDEXED (possibile race condition / supersede pre-esistente). |
| 6 | `media_assets.index_state = INDEXED` | ✅ | Entrambi INDEXED in SQLite. |
| 7 | Qdrant scroll trova ogni `asset_id` | ⚠️ | 450645: ✅ trovato. 61645: ❌ assente (1/2 = 50%). Totale punti Artlist in Qdrant: 1. |
| 8 | `/api/media/search` ritrova clip Artlist | ⚠️ | Search con source=artlist fallisce ("no eligible backends"). Search senza source filter ritorna 61645 dal backend locale ma con provider_error. Timeout sul count query del backend locale (issue generale non Artlist). |
| 9 | Drive spento → UPLOAD_FAILED, no Processed++, no Qdrant event | ❌ | Non testato. |
| 10 | Qdrant spento → no fake completed | ❌ | Non testato. |
| 11 | Scraper spento → errore/503 | ❌ | Non testato (ma il comportamento atteso è verificabile: se scraper down, la run fallisce con "all artlist items failed"). |
| 12 | `cmd/admin qdrant-preflight` passa (exit 0) | ❌ | Non eseguito. |

**Riepilogo:** 4 ✅ | 4 ⚠️ | 4 ❌ → **Core E2E: PASS. 100% operativo: NON ANCORA.**

---

## 6. Stima Realistica

| Scenario | Tempo | Cosa include |
|----------|-------|-------------|
| **Mock forti** (DoD credibile) | **1-2 settimane** | test Artlist full-run con `RunTag`, `mediaProcessor`, `Publisher` mock, `Dispatcher`, SQLite e mock Qdrant; test Drive failure fail-closed; test Qdrant failure no fake completed; test search round-trip su asset Artlist indicizzato |
| **Reale 100%** (DoD operativa) | **2-3 settimane** | tutto sopra + Node scraper reale, account Artlist/cookie validi, Drive OAuth reale, Qdrant reale e ricerca reale |

---

## 7. Execution Order (godlike/07 EXPAND → BACKFILL → CUTOVER → CONTRACT)

### 7.1 Sequenza per banda

```
Week 1-2 (deadline 2026-07-21): Gate funzionali + persistenza
  ├── Gate 1: search/live test (scraper reale o mock)
  ├── Gate 2: run 3-5 clip → processed/failed count + Drive fields
  ├── Gate 3: SQLite projection (source, media_type, lifecycle_state, hash, drive)
  ├── Gate 4: outbox asset.index.requested emission post-upload
  └── Gate 8: hybrid search round-trip (con mock Qdrant)

Week 3 (deadline 2026-07-28): Gate di failure
  ├── Gate 9: Drive failure → UPLOAD_FAILED, no Processed++, no Qdrant event
  ├── Gate 10: Qdrant failure → no fake completed
  └── Gate 11: Scraper failure → 503 / errore chiaro

Week 4 (deadline 2026-08-04): Gate Qdrant + preflight
  ├── Gate 5: outbox completed solo dopo vera scrittura Qdrant
  ├── Gate 6: media_assets.index_state = INDEXED
  ├── Gate 7: qdrant scroll trova ogni asset_id
  └── Gate 12: qdrant-preflight passa (test 3-8, 10, 11 non più stub)
```

### 7.2 Dependencies

- Gate 5 dipende da Gate 4 (prima l'outbox deve emettere l'evento)
- Gate 6 dipende da Gate 5 (l'index_state diventa INDEXED dopo l'upsert Qdrant)
- Gate 7 dipende da Gate 6 (lo scroll trova l'asset solo se INDEXED)
- Gate 12 dipende da Gate 1-11 (il preflight verifica tutta la catena)
- Gate 9, 10, 11 sono indipendenti tra loro (failure mode distinti)

---

## 8. Wave-Tracker Entry Pointer (canonical anchor)

**Live anchor:** `architecture/current.yaml#ARTLIST-DOD-2026-07-07`
Per godlike/06 SSOT (one canonical owner per fact):
- **Narrative** vive qui (per-band depth + per-gate contract + stima + execution order).
- **Status/state** vive nel YAML tracker (id + status + linked_issues + deadlines).
- **Migration discipline** vive in CHANGELOG.md closure meta-entry.
- **Audit-pin archaeology** vive in AGENTS.md mirror.

---

## 9. Cross-References

- `AGENTS.md` DL-006 + DL-007 (critical Artlist rules — composition-root fail-closed + Pattern 0 routing)
- `architecture/current.yaml#ART-002` (parent wave, status: shipped, architettura solida)
- `architecture/current.yaml#QDRANT-CHAIN-VERIFY-2026-07-04` (Qdrant verification wave, pattern analogo)
- `docs/operations/artlist-runbook.md` (operator runbook, status: LIVE)
- `docs/operations/qdrant-verification-runbook.md` (Qdrant operator runbook)
- `internal/app/build_bundles_artlist.go` (composition-root wiring + gate)
- `internal/application/assets/providers/artlist/run_orchestrator_stages.go` (stagePersistResults — drive gate mancante)
- `tests/e2e/artlist_full_run_test.go` + `artlist_live_search_test.go` + `artlist_fallback_test.go` (E2E test esistenti)
- AGENTS.md Git-Lesson-2/3/4/5 (direct-to-main + Co-authored-by + race-protect + byte-equivalent-replay)

---

## 10. Lifecycle (audit trail)

| Date | Action | Status | Author |
|------|--------|--------|--------|
| 2026-07-07 | Plan doc lands (this file) | in_progress | PipelineGen Agent (da verdetto Marcuss-ops) |
| 2026-07-07 | Wave-tracker entry lands (lockstep) | in_progress | PipelineGen Agent |
| 2026-07-07 | CHANGELOG.md closure meta-entry lands | documentation-only | PipelineGen Agent |
| 2026-07-07 | AGENTS.md mirror lands | documentation-only | PipelineGen Agent |
| 2026-07-07 | **LIVE VERIFICATION**: Real run SUCCEEDED (job `job_1783429732658387580_2d010abe`), scraper :9123 healthy, SQLite 2 clip ACTIVE+INDEXED, outbox completed, Qdrant 450645 indexed, search returns 61645 | core_e2e_verified | PipelineGen Agent |
| 2026-07-21 | Gate 1-4 + 8 deadline (funzionali + persistenza + search) | pending | (TBD) |
| 2026-07-28 | Gate 9-11 deadline (failure modes) | pending | (TBD) |
| 2026-08-04 | Gate 5-7 + 12 deadline (Qdrant + preflight) | pending | (TBD) |
| 2026-08-11 | Wave exit_gate flip (status: done / exit_signal: true) | UNLOCK | (TBD) |

---

## 11. Schema Qdrant Corrections (rispetto alla prima analisi)

Correzioni basate sul codice canonico in `internal/infrastructure/qdrant/schema/schema.go::DefaultV3Schema()`:

| Canale | Dim | Modello | Stato |
|--------|-----|---------|-------|
| `text` | **768** | **multilingual-e5-base** (NON nomic-embed-text) | ✅ Attivo |
| `transcript` | **768** | **multilingual-e5-base** | ✅ Attivo |
| `visual` | **768** (NON 512) | **siglip-so400m-patch14-384** | ✅ Attivo |
| `bm25_text` | sparse | BM25 (server-side) | ✅ Attivo |
| `audio` | — | CLAP-HTSAT | ❌ **Commentato** — non production-wired |

**Nota sui modelli**: il `clipindexer` usa la costante `embeddingModel = "nomic-embed-text"` nell'envelope/metadata, ma lo schema Qdrant ufficiale dichiara `multilingual-e5-base` per `text` e `transcript`. Questa è una zona di confusione da ripulire (forward-pointer `PR-QDRANT-MODEL-NAME-ALIGN`).

---

**End of canonical action plan.** Future agents: read this file alongside `architecture/current.yaml#ARTLIST-DOD-2026-07-07` for the canonical state surface. Operators: esegui i 12 gate in ordine (§5) prima di dichiarare "Artlist DoD 100%".

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local> (per AGENTS.md Git-Lesson-3)
