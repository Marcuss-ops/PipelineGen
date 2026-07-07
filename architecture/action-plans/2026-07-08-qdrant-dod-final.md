# Qdrant Definition of Done — Final Action Plan (2026-07-08)

**Date:** 2026-07-08
**Author:** PipelineGen Agent (da verdetto Marcuss-ops, 2026-07-07 live verification)
**Owner:** architecture doc maintainer + the 5 surface owners cited per band
**Scope:** Consolidamento della DoD finale globale per Qdrant. A differenza di `QDRANT-CHAIN-VERIFY-2026-07-04` (che verifica il chain esistente) e `QDRANT-PREFLIGHT-EXECUTION-2026-07-04` (che mappa i 11 sanity probe SQL/curl), questo piano definisce i **12 gate di chiusura DoD finale** che devono essere verdi PRIMA di dichiarare Qdrant DONE a livello globale.
**Status:** **NOT_YET_DOD** (godlike/07 no-fake-availability — vedi §0 verdetto)
**Parent waves:**
- `artchitecture/current.yaml#QDRANT-CHAIN-VERIFY-2026-07-04` (status: in_progress, 6 per-PR linked_issues)
- `architecture/current.yaml#QDRANT-PREFLIGHT-EXECUTION-2026-07-04` (status: in_progress, 16 per-PR linked_issues)
- `architecture/current.yaml#PR-QDRANT-FULL-STACK-AUTOMATED` (canonical SHA `2352163c`, status: shipped, the 11-test binary)
- `architecture/current.yaml#ARTLIST-DOD-2026-07-07` (sister DoD for Artlist, same structure)
**Audit-trail anchor:** `architecture/current.yaml#QDRANT-DOD-FINAL-2026-07-08` (NEW slim-shape entry)
**Companion entries:** `AGENTS.md` §Recent cross-cutting closures (audit-pin mirror) + `CHANGELOG.md` `## Unreleased` (closure meta-entry).

---

## §0 — Verdetto onesto (godlike/07 NO-FAKE-AVAILABILITY)

> **Qdrant NON può ancora ricevere DoD finale globale**, anche se alcune slice reali (Artlist boxing, singola run YouTube) sembrano funzionare.

**Motivo:**
1. `cmd/admin/qdrant_preflight.go` esiste con 11 test canonici (`var AllTests`, SOLE source-of-truth per il list), ma i test 3-8, 10 e 11 sono ancora **stub** (`cmd/admin/qdrant_preflight_stubs.go`) che ritornano `ErrPreflightNotImplemented`. Il framework del gate c'è, ma non ancora la prova della catena completa.
2. Coverage produttori parziale: YouTube ✅ + Voiceover ✅ + Artlist ✅ (live 2026-07-07 su `ARTLIST-DOD-2026-07-07`) + **Stock ❌** (forward-pointer `PR-QDRANT-DOD-STOCK-PRODUCER` deadline 2026-08-08).
3. Le 4 assertion obbligatorie (media_assets.index_state INDEXED + Qdrant scroll finds asset_id + hybrid search ritorna score>0.5 + payload.lifecycle_state=ACTIVE) non hanno ancora un **aggregator** che le verifichi insieme — ogni assertion ha il suo test preflight, ma non c'è un "guardiano di coerenza" che fallisca se una assertion passa individualmente ma il DO.D. nel suo insieme no.
4. `PR-QDRANT-FULL-STACK-AUTOMATED` (canonical SHA `2352163c`, 2026-07-04) shippa il binary ma è **scaffolding** (workflow_dispatch only, FAIL-loudly on unmet prereqs); la promozione a push-on-main è gated sul carry-forward + full-stack bringup + seed (forward-pointer `PR-QDRANT-FULL-STACK-AUTOMATED-PROMOTE` deadline 2026-08-15).

**Formula finale da usare:**

> Qdrant è DONE quando:
> (a) ogni gate 1-12 sotto descritto è verde;
> (b) `cmd/admin qdrant-preflight` finisce con `0 FAIL` (test 9 SKIP è ammesso per il chaos-day manuale);
> (c) ogni producer reale (YouTube + Artlist + Stock + Voiceover) produce `asset.index.requested.v1`, l'outbox completa senza fake-success, `media_assets.index_state=INDEXED`, Qdrant scroll trova l'asset, hybrid search lo ritorna con score>0.5, payload contiene `asset_id + source + media_type + lifecycle_state=ACTIVE + search_text + embedding_version_text + embedding_version_visual`, il supersede gate elimina eventi obsoleti, lifecycle filter nasconde asset deleted, Qdrant spento → retry/recovery anziché completamento falso.

---

## §1 — TL;DR: 12 gate della DoD finale

```
              QDRANT DOD FINALE — 12 GATE STATE MATRIX (post-verdict 2026-07-08)
              ──────────────────────────────────────────────────────────────────

  ┌── GATE GIÀ COPERTI ──────────────────────────────────────────────────────┐
  │  ✅ Gate 1: Config fail-closed                                          │  [PR-QDRANT-CONFIG-MISMATCH-GATE]
  │  ✅ Gate 5: IndexingHandler corretto                                    │  [PR-QDRANT-INDEXCLIP-GUARD, ship 2026-07-04]
  └────────────────────────────────────────────────────────────────────────┘

  ┌── GATE IMPLEMENTATI MA DA ESTENDERE ─────────────────────────────────────┐
  │  🔶 Gate 9: Lifecycle filter (implements delete tombstone check)        │  [PR-QDRANT-SEARCH-LIFECYCLE-FILTER]
  │  🔶 Gate 11: Supersede (Typed SupersedeError presente)                  │  [PR-QDRANT-E2E-SUPERSEDE-GATE]
  └────────────────────────────────────────────────────────────────────────┘

  ┌── GATE ANCORA STUB NEL PREFLIGHT (need real impl) ──────────────────────┐
  │  ❌ Gate 2: Schema Qdrant corretto (TEST-2)                            │  [PR-QDRANT-PREFLIGHT-TEST-2-IMPL]
  │  ❌ Gate 4: 4 mandatory assertions aggregator                          │  [PR-QDRANT-DOD-4-ASSERTIONS] (NEW)
  │  ❌ Gate 6: State machine INDEXED (TEST-5)                              │  [PR-QDRANT-PREFLIGHT-TEST-5-IMPL]
  │  ❌ Gate 7: Qdrant scroll finds asset_id (TEST-6)                       │  [PR-QDRANT-PREFLIGHT-TEST-6-IMPL]
  │  ❌ Gate 8: Hybrid search score>0.5 (TEST-7)                            │  [PR-QDRANT-PREFLIGHT-TEST-7-IMPL]
  │  ❌ Gate 10: Chaos Qdrant spento (TEST-9)                                │  [PR-QDRANT-PREFLIGHT-TEST-9-IMPL + chaos-day]
  │  ❌ Gate 12: 11/11 preflight pass                                        │  [PR-QDRANT-PREFLIGHT-CLOSURE] (umbrella flip)
  └────────────────────────────────────────────────────────────────────────┘

  ┌── GATE PRODUCER COVERAGE (per type) ─────────────────────────────────────┐
  │  ✅ YouTube (Band B in QDRANT-CHAIN-VERIFY)                              │  [PR-QDRANT-E2E-YOUTUBE]
  │  ✅ Voiceover (Band B in QDRANT-CHAIN-VERIFY)                            │  [PR-QDRANT-E2E-VOICEOVER]
  │  ✅ Artlist (ARTLIST-DOD-2026-07-07 live verified)                       │  [ARTLIST-DOD-2026-07-07]
  │  ❌ Stock (NO Producer contract test oggi)                              │  [PR-QDRANT-DOD-STOCK-PRODUCER] (NEW)
  └────────────────────────────────────────────────────────────────────────┘
```

---

## §2 — Gate 1: Config fail-closed (qdrant.enabled ∧ clipindexer.enabled)

### Cosa deve fare
Mai Qdrant acceso con indexer spento. Se `qdrant.enabled=true` E `clipindexer.enabled=false`, il sistema deve fallire al boot oppure l'outbox deve restare retryable, ma non deve mai marcare `completed` senza scrivere in Qdrant.

### Cosa serve per DoD finale
```text
qdrant.enabled=true
clipindexer.enabled=true
vectorStore wired
Qdrant writer wired
IndexingHandler wired
outbox pool running
```

### PR canonico (esistente)
**`PR-QDRANT-CONFIG-MISMATCH-GATE`** — composition-root fail-closed gate via `validateQdrantIndexerCompatibility(cfg *config.Config) error` (mirror del pattern `validateArtlistScraperURL` di ART-002 P0.1). 4 TDD test idempotenti al pattern ART-002 P0.1.

**Files canonici** (esistenti):
- `internal/app/build_bundles_qdrant.go` (gate inline)
- `internal/app/build_bundles_qdrant_test.go` (4 TDD test)
- `config.example.yaml` (comment block)

---

## §3 — Gate 2: Schema Qdrant corretto (5 named vectors + audio optional)

### Cosa deve fare
La collection/alias deve esistere con schema v3:
```text
alias runtime: media_assets_current
schema: v3
```
Named vectors production-required (verificati dal codice canonico `internal/infrastructure/qdrant/schema/`):

| Vector | Dim | Modello | Required per DoD finale? |
|--------|-----|---------|--------------------------|
| `text` | 768 | multilingual-e5-base | ✅ REQUIRED |
| `transcript` | 768 | multilingual-e5-base | ✅ REQUIRED |
| `visual` | 768 | siglip-so400m-patch14-384 | ✅ REQUIRED |
| `bm25_text` | sparse | BM25 server-side | ✅ REQUIRED |
| `audio` | 512 | CLAP-HTSAT | ⚠️ OPTIONAL — codice commentato, NON production-wired |

**Decisione DoD finale (godlike/07 typed-contract):** `audio` vector è **OPTIONAL/FUTURE GATE**, non blocking per la DoD Qdrant attuale. Nel codice canonico `internal/infrastructure/qdrant/schema/schema.go::DefaultV3Schema()` il canale `audio` è dichiarato ma il commento marca CLAP come "non disponibile in produzione".

### Cosa serve per DoD finale
Il test TEST-2 del preflight deve verificare la presenza dei 4 vector required + assenza blanda di audio (warning, non FAIL).

### PR canonico (esistente, da implementare)
**`PR-QDRANT-PREFLIGHT-TEST-2-IMPL`** — fill-in dello stub `testSchemaV3` (oggi `return ErrPreflightNotImplemented`); verification su `http://localhost:6333/collections/media_assets_current` parsando il JSON-schema.

**Files canonici**:
- `cmd/admin/qdrant_preflight.go` (registry `var AllTests[TestIdx=1]`)
- `cmd/admin/qdrant_preflight_stubs.go` (REPLACE stub)
- `cmd/admin/qdrant_preflight_test.go` (NEW 5 TDD case: 4 required present + audio optional logged)

---

## §4 — Gate 3: Producer contract (UPSERT + outbox stessa tx)

### Cosa deve fare
Ogni producer (YouTube/Artlist/Stock/Voiceover) deve creare asset e outbox in modo ATOMICO:
```text
media_assets UPSERT
outbox_events INSERT asset.index.requested
stessa transazione SQLite
payload envelope v1 valido
```

Envelope v1 richiesto (per `outboxevents/registry.go`):
- `schema_version = asset.index.requested.v1`
- `asset_id`
- `source_version`
- `idempotency_key`
- `operation = UPSERT`
- `target_index_version`
- `requested_vectors`
- `embedding_model/version`

Il dispatcher `EnqueueAndIndex` fa proprio UPSERT + outbox insert nella stessa transazione.

### Cosa serve per DoD finale
Per ogni producer: TEST-3 del preflight deve passare (insert row + read-back verifica). **4 producer → 4 enforce coverage slots.**

### PR canonici (esistenti + 1 NEW)
| Producer | Envelope Coverage | Forward-pointer |
|----------|-------------------|------------------|
| YouTube | ✅ (PR-QDRANT-E2E-YOUTUBE closure 2026-07-04) | live |
| Artlist | ✅ (ARTLIST-DOD-2026-07-07 live verified 2026-07-07) | live |
| Voiceover | ✅ (PR-QDRANT-E2E-VOICEOVER closure 2026-07-04) | live |
| **Stock** | ❌ | **`PR-QDRANT-DOD-STOCK-PRODUCER`** (NEW, deadline 2026-08-08) |

### `PR-QDRANT-DOD-STOCK-PRODUCER` (NEW)
Per QDRANT DOD finale globale serve il symmetric coverage Stock. Per `internal/application/assets/providers/stock/stockpipeline/`: la catena termina con la `Orchestrator.RunResilient` Step 6 `stock.finalize` che emette outbox events `asset.index.requested.v1` per ogni chunk. Verificare che il test preflight TEST-3 funzioni anche su asset stock (non solo youtube/artlist/voiceover).

**Files canonici proposti**:
- `cmd/admin/qdrant_preflight.go` (variant: `testOutboxEventsCreated` parametric su producer type)
- `tests/e2e/stock_to_qdrant_test.go` (NEW: 3 sub-tests happy + replay idempotent + supersede)
- `internal/application/assets/providers/stock/stockpipeline/orchestrator_finalize_test.go` (NEW: 1 TDD case asserting envelope v1 shape)

---

## §5 — Gate 4: Outbox non deve mentire (4 mandatory assertions)

### Cosa deve fare
`outbox_events.status=completed` NON basta per la DoD finale. Le 4 assertion obbligatorie:
1. `media_assets.index_state = INDEXED`
2. Qdrant scroll trova `asset_id`
3. Search/hybrid search ritorna il risultato
4. Payload contiene `payload.lifecycle_state=ACTIVE` + `payload.search_text` present

L'action plan QDRANT-CHAIN-VERIFY-2026-07-04 §1.2 dichiara apertamente:
> "per dire 'Qdrant è davvero end-to-end', non basta vedere `outbox_events.status=completed`"

### Cosa serve per DoD finale
Un **aggregator contract test** che fallisce se uno qualsiasi dei 4 punti è fuori. Senza di questo, ogni test preflight può passare individualmente ma il DO.D. nel suo insieme no.

### PR canonico (NEW)
**`PR-QDRANT-DOD-4-ASSERTIONS`** — NEW aggregated contract test che verifica tutti e 4 i punti insieme per un singolo asset round-trip.

**Files canonici proposti**:
- `tests/e2e/qdrant_dod_4_assertions_test.go` (NEW) — hermetic Go test che:
  - prende un asset_id da una seed deterministica
  - wait per `outbox completed` + `index_state=INDEXED`
  - `qdrant scroll(asset_id)` → assert `len(points)==1`
  - Qdrant point payload → assert `lifecycle_state=="ACTIVE"` AND `search_text != ""`
  - `POST /internal/v1/media/search` → assert ≥1 hit con `asset_id` matching
  - se una qualsiasi assertion fallisce → `t.Fatalf` con dettaglio del punto fallito

**Owner**: cmd/archcheck + tests/e2e/ (composition-root wave's `tests/e2e/` test directory)
**Deadline**: 2026-08-15 (post Band B 1st wave)
**godlike/06 SSOT**: l'aggregator è il SOLE owner della "consistent visualisability" assertion (nessun duplicato in altri test file).

---

## §6 — Gate 5: IndexingHandler corretto

### Cosa deve fare
L'handler deve:
1. parsare envelope v1
2. validare campi obbligatori
3. mandare terminal error su payload rotto
4. fare supersede gate su `source_version`
5. chiamare `IndexClip`
6. NON marcare completed se l'indexer è disabled o fallisce

### PR canonico (esistente, SHIPPED)
**`PR-QDRANT-INDEXCLIP-GUARD`** — canonical SHA `e2498709`, ship_date 2026-07-04. Implementa:
- `clipindexer.ErrIndexClipDisabledButEventRequested` typed sentinel
- `errors.Is` guard in IndexingHandler
- `INDEXING_SKIPPED_NO_INDEXER` state in `internal/domain/asset/index_state.go`

### Cosa serve per DoD finale
Verifica che il test preflight TEST-4 (status=completed non fake) passi dopo lo shipping di questo PR. Live su origin/main dal 2026-07-04.

---

## §7 — Gate 6: State machine completa (5-state + 3 failure states)

### Cosa deve fare
State machine canonica:
```text
DISCOVERED → EMBEDDING → EMBEDDED → INDEXING → INDEXED
Failure:    EMBEDDING_FAILED, INDEXING_FAILED, INDEXING_SKIPPED_NO_INDEXER
```

### Cosa serve per DoD finale
Pre-flight TEST-5 deve verificare `media_assets.index_state=INDEXED` + `indexed_at` valorizzato + `source_version` coerente + `indexed_content_hash` coerente.

### PR canonico (esistente, da implementare)
**`PR-QDRANT-PREFLIGHT-TEST-5-IMPL`** — fill-in dello stub. Query SQL `SELECT id, index_state, indexed_at, source_version, indexed_content_hash FROM media_assets WHERE id IN (<seed_ids>)`.

**Files canonici**:
- `cmd/admin/qdrant_preflight_stubs.go` (REPLACE stub `testMediaAssetsIndexStateIndexed`)
- `cmd/admin/qdrant_preflight_test.go` (NEW 3 TDD: INDEXED + indexed_at NOT NULL + source_version not empty)

---

## §8 — Gate 7: Qdrant point reale (scroll filter)

### Cosa serve per DoD finale
Per ogni asset testato:
```bash
curl -s http://localhost:6333/collections/media_assets_current/points/scroll \
  -d '{"limit":5, "with_payload":true, "filter":{"must":[{"key":"asset_id","match":{"value":"<id>"}}]}}'
```
Atteso: 1 point trovato + payload assert (asset_id, source, media_type, lifecycle_state, search_text, embedding_version_text, embedding_version_visual).

### PR canonico (esistente, da implementare)
**`PR-QDRANT-PREFLIGHT-TEST-6-IMPL`** — fill-in dello stub. Implementation usa `internal/infrastructure/qdrant/admin/scroll.go::ScrollByAssetID` port.

---

## §9 — Gate 8: Search/hybrid search (score>0.5)

### Cosa serve per DoD finale
Per ogni source:
```text
YouTube query   → ritorna clip YouTube   (score > 0.5)
Artlist query   → ritorna clip Artlist   (score > 0.5)
Stock query     → ritorna chunk Stock    (score > 0.5)
Voiceover query → ritorna asset voiceover (score > 0.5)
```

Non basta Qdrant scroll — la search prova: (a) embedding query funziona, (b) BM25 funziona, (c) fusion/ranking funziona, (d) SQLite hydration funziona, (e) lifecycle filter.

### PR canonico (esistente, da implementare)
**`PR-QDRANT-PREFLIGHT-TEST-7-IMPL`** — fill-in dello stub. Implementation chiama `POST /internal/v1/media/search mode=hybrid limit=10` con query-parametri diversi per i 4 producer types.

---

## §10 — Gate 9: Lifecycle filter (search esclude asset non attivi)

### Cosa serve per DoD finale
Search deve escludere:
```text
DELETED
DELETE_REQUESTED
DRIVE_DELETE_PENDING
INDEX_DELETE_PENDING
```

### PR canonico (esistente, SHIPPED)
**`PR-QDRANT-SEARCH-LIFECYCLE-FILTER`** (QDRANT-CHAIN-VERIFY Band B #6) — `search.Query.Filters.LifecycleStates = ["ACTIVE"]` typed filter (NOT substring match). Owner: `internal/application/search/aggregator.go`.

---

## §11 — Gate 10: Failure mode Qdrant spento (chaos test)

### Cosa serve per DoD finale
Chaos sequence (mirrors `architecture/action-plans/2026-07-04-qdrant-preflight-execution.md §3 Test 9`):
1. `docker stop pipelinegen-qdrant`
2. New asset → outbox event
3. outbox_status = `pending` per 60s (worker retry-loop engages)
4. outbox `last_error` valorizzato con Qdrant connection error
5. `media_assets.index_state` NON diventa `INDEXED` (rimane PENDING)
6. `docker start pipelinegen-qdrant`
7. outbox arriva a `completed` entro 30s
8. `media_assets.index_state = INDEXED`
9. Qdrant scroll/search trovano l'asset

### PR canonico (esistente, forward-ponterato)
**`PR-QDRANT-PREFLIGHT-TEST-9-RETRY-RECOVERY`** (QDRANT-PREFLIGHT-EXECUTION linked_issues, status: pending) + **`PR-QDRANT-CHAOS-DAY-2026-08-01`** (umbrella scheduling entry).

### Cosa serve per DoD finale
Esecuzione reale del chaos sequence in maintenance window 2026-08-01, confermato in wave-tracker exit_gate flip.

---

## §12 — Gate 11: Supersede (source_version invariant)

### Cosa serve per DoD finale
Se arriva un evento vecchio:
```text
source_version evento != source_version corrente DB
```
allora:
```text
vecchio evento = superseded
nuovo evento = completed
Qdrant contiene solo versione corrente
```

### PR canonico (esistente, SHIPPED)
**`PR-QDRANT-E2E-SUPERSEDE-GATE`** (QDRANT-CHAIN-VERIFY Band B #5, ship_date 2026-07-04) — typed `SupersedeError` + source_version CAS-fence.

### PR canonico (esistente, da implementare)
**`PR-QDRANT-PREFLIGHT-TEST-8-IMPL`** — fill-in dello stub. Implementation emette 2 eventi con `source_version` diversi sulla stessa `aggregate_id`, verifica che il primo diventa `superseded` e il secondo `completed`.

---

## §13 — Gate 12: Preflight automatico 11/11

### Cosa serve per DoD finale
```bash
cmd/admin qdrant-preflight
```
finisce con:
```text
0 FAIL
Test 9 SKIP allowed solo se chaos-day manuale non eseguito
tutti gli altri PASS reali
```

### PR canonico (esistente, forward-pointer)
**`PR-QDRANT-PREFLIGHT-CLOSURE`** (QDRANT-PREFLIGHT-EXECUTION umbrella flip, deadline 2026-08-01) — quando tutti i test 1-11 sono reali e passano, flip `architecture/current.yaml#QDRANT-PREFLIGHT-EXECUTION-2026-07-04` → `status: done / exit_signal: true`.

### Cosa serve per DoD finale
Dipende da TUTTI i precedenti gate. È l'ultimo step del cycle.

---

## §14 — Execution Order (godlike/07 EXPAND → BACKFILL → CUTOVER → CONTRACT)

```
BAND 1 (deadline 2026-07-15, foundation):
  - PR-QDRANT-CONFIG-MISMATCH-GATE      [Gate 1, existing]
  - PR-QDRANT-INDEXCLIP-GUARD           [Gate 5, SHIPPED 2026-07-04]

BAND 2 (deadline 2026-07-25, Band B 1st wave):
  - PR-QDRANT-E2E-YOUTUBE               [Gate 3 YouTube, existing]
  - PR-QDRANT-E2E-VOICEOVER             [Gate 3 Voiceover, existing]
  - PR-QDRANT-E2E-SUPERSEDE-GATE        [Gate 11, existing]
  - PR-QDRANT-SEARCH-LIFECYCLE-FILTER   [Gate 9, existing]

BAND 3 (deadline 2026-08-01, preflight fills + chaos):
  - PR-QDRANT-PREFLIGHT-TEST-2-IMPL     [Gate 2]
  - PR-QDRANT-PREFLIGHT-TEST-5-IMPL     [Gate 6]
  - PR-QDRANT-PREFLIGHT-TEST-6-IMPL     [Gate 7]
  - PR-QDRANT-PREFLIGHT-TEST-7-IMPL     [Gate 8]
  - PR-QDRANT-PREFLIGHT-TEST-8-IMPL     [Gate 11 second-half]
  - PR-QDRANT-PREFLIGHT-TEST-9-RETRY + CHAOS-DAY  [Gate 10]

BAND 4 (deadline 2026-08-08, producer parity):
  - PR-QDRANT-DOD-STOCK-PRODUCER        [Gate 3 Stock, NEW]

BAND 5 (deadline 2026-08-15, aggregator + closure):
  - PR-QDRANT-DOD-4-ASSERTIONS          [Gate 4, NEW]
  - PR-ARTLIST-DOD-12DOD                [Sister closure, existing or pending]
  - PR-QDRANT-PREFLIGHT-CLOSURE         [Gate 12 umbrella flip]

BAND 6 (deadline 2026-09-01, post-wave hotspot cross-val):
  - PR-QDRANT-DOD-HOTSPOT-CROSSREF      [git-log frequency post-wave, NEW]
```

---

## §15 — Stima Realistica

| Scenario | Tempo | Cosa include |
|----------|-------|--------------|
| **Qdrant DoD credibile** (mock forti su Qdrant locale) | **3-4 settimane** | Band 3 test fills (5 PR × ~3h) + Band 4 stock producer (1 PR × ~12h) + Band 5 aggregator + closure (2 PR × ~6h) |
| **Qdrant DoD operativa** (reale 100%) | **5-6 settimane** | sopra + credenziali Qdrant reali (port 6333 with auth) + chaos-day confermato su cloud Qdrant + prod asset traffic per 11-test verifier |

---

## §16 — Wave-Tracker Entry Pointer (canonical anchor)

**Live anchor:** `architecture/current.yaml#QDRANT-DOD-FINAL-2026-07-08` (NEW).
Per godlike/06 SSOT (one canonical owner per fact):
- **Narrative** vive qui (12 gate depth + execution order + stima + cross-refs).
- **Status/state** vive nel YAML tracker (id + status + linked_issues + deadlines).
- **Migration discipline** vive in CHANGELOG.md closure meta-entry.
- **Audit-pin archaeology** vive in AGENTS.md mirror.

---

## §17 — Cross-References (godlike/06 SSOT 3-surface lockstep)

- `architecture/current.yaml#QDRANT-DOD-FINAL-2026-07-08` (NEW wave-tracker anchor)
- `architecture/current.yaml#QDRANT-CHAIN-VERIFY-2026-07-04` (parent wave; 6 per-PR linked_issues covering Gates 1, 5, 9, 11 partial + E2E suites)
- `architecture/current.yaml#QDRANT-PREFLIGHT-EXECUTION-2026-07-04` (parent wave; 16 per-PR linked_issues covering Tests 1-11 + infrastructure bring-up + seed + closure)
- `architecture/current.yaml#PR-QDRANT-FULL-STACK-AUTOMATED` (canonical SHA `2352163c`, the 11-test binary)
- `architecture/current.yaml#ARTLIST-DOD-2026-07-07` (sister DoD action plan; canonica struttura di riferimento)
- `architecture/current.yaml#ARTLIST-DOD-2026-07-07.#audit-trail anchor:` (sister wave-tracker; same structure per canonical SSOT)
- `architecture/action-plans/2026-07-04-qdrant-preflight-execution.md` (operator-side 11-test runbook)
- `architecture/action-plans/2026-07-04-qdrant-verification-chain.md` (chain-level action plan with 11 SQL/curl probes per-gate)
- `architecture/action-plans/2026-07-07-artlist-dod-action-plan.md` (SISTER DoD template, canonare struttura)
- `cmd/admin/qdrant_preflight.go` (`var AllTests` SOLE source-of-truth per 11-test list)
- `cmd/admin/qdrant_preflight_stubs.go` (stubs per Tests 3-8, 10, 11 — da fill-in)
- `internal/infrastructure/qdrant/schema/schema.go::DefaultV3Schema()` (5-vector schema canonica)
- `internal/application/assets/providers/stock/stockpipeline/orchestrator.go` (Stock producer; manca il test producer-contract simmetrico agli altri 3)
- `docs/operations/qdrant-verification-runbook.md` (operator runbook, status: LIVE)
- AGENTS.md §godlike/07 no-fake-availability doctrine (the verdict must be honest)
- AGENTS.md §git-lesson-2/3/4/5 (direct-to-main + Co-authored-by + race-protect + byte-equivalent-replay)

---

## §18 — Lifecycle (audit trail)

| Date | Action | Status | Author |
|---|---|---|---|
| 2026-07-08 | Plan doc lands (this file) | in_progress | PipelineGen Agent (da verdetto Marcuss-ops) |
| 2026-07-08 | Wave-tracker entry `QDRANT-DOD-FINAL-2026-07-08` lands (lockstep) | in_progress | PipelineGen Agent |
| 2026-07-08 | CHANGELOG.md closure meta-entry lands | documentation-only | PipelineGen Agent |
| 2026-07-08 | AGENTS.md mirror entry lands (§Recent cross-cutting closures) | documentation-only | PipelineGen Agent |
| 2026-07-08 | git fetch origin + race-protect + ff-push | direct-to-main | PipelineGen Agent |
| 2026-07-15 | PR-QDRANT-CONFIG-MISMATCH-GATE + INDEXCLIP-GUARD deadlines (Gates 1+5) | pending | (TBD) |
| 2026-07-25 | PR-QDRANT-E2E-* deadlines (Gates 3 YouTube/Voiceover + 9 + 11) | pending | (TBD) |
| 2026-08-01 | PR-QDRANT-PREFLIGHT-TEST-2/5/6/7/8-IMPL + chaos-day deadlines | pending | (TBD) |
| 2026-08-08 | PR-QDRANT-DOD-STOCK-PRODUCER deadline (Gate 3 Stock parity) | pending | (TBD) |
| 2026-08-15 | PR-QDRANT-DOD-4-ASSERTIONS + PR-QDRANT-PREFLIGHT-CLOSURE deadlines (Gates 4 + 12) | pending | (TBD) |
| 2026-09-01 | PR-QDRANT-DOD-HOTSPOT-CROSSREF deadline (post-wave git-log frequency cross-val) | pending | (TBD) |
| post-2026-09-01 | Wave exit_gate flip → `status: shipped + exit_signal: true` ONLY IF 12/12 gate green | UNLOCK | (TBD) |

---

## §19 — Honest scope-lock (godlike/07 summary)

- **what this plan adds vs existing waves**: 4 NEW per-PR linked_issues (PR-QDRANT-DOD-STOCK-PRODUCER + PR-QDRANT-DOD-4-ASSERTIONS + PR-QDRANT-PREFLIGHT-TEST-{2,5,6,7,8}-IMPL are mostly forward-pointers, NOT duplicates) + 1 NEW forward-pointer umbrella entry.
- **what existing waves already cover**: PR-QDRANT-CONFIG-MISMATCH-GATE (Gate 1) + PR-QDRANT-INDEXCLIP-GUARD (Gate 5) + PR-QDRANT-SEARCH-LIFECYCLE-FILTER (Gate 9) + PR-QDRANT-E2E-YOUTUBE + E2E-VOICEOVER + E2E-SUPERSEDE (Gates 3 partial + 11).
- **what this plan does NOT add**: zero duplicate linked_issues (slim-schema ratchet enforced); zero new audio vector contract (declared optional per §3); zero pre-fake 11/11 PASS (the preflight currently has 5 stub tests).
- **verdict**: Qdrant is NOT YET DoD finale globale. Per godlike/07 no-fake-availability: the wave-flip to status:shipped happens ONLY after the 12 gate-by-gate criteria pass AND live verification.

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local> (per AGENTS.md Git-Lesson-3)
