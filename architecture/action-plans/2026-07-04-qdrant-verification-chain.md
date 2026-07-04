# Qdrant Verification Chain — End-to-End Action Plan

**Date:** 2026-07-04
**Author:** PipelineGen Agent
**Owner:** architecture doc maintainer + 5 per-capability owners (see Wave-tracker)
**Scope:** End-to-end behavior verification of the Qdrant indexing chain (media_assets → outbox_events asset.index.requested → IndexingHandler → clipindexer.IndexClip → QdrantRuntime.Writer → Qdrant collection alias media_assets_current) + 2 P0 fail-closed semantic guards + 4 P1/P2 e2e test suites + 1 Operator Pre-flight Checklist (11 SQL/curl probes).
**Status:** in_progress (Wave QDRANT-CHAIN-VERIFY-2026-07-04, `architecture/current.yaml#QDRANT-CHAIN-VERIFY-2026-07-04`)
**Audit-trail anchor:** `architecture/current.yaml#QDRANT-CHAIN-VERIFY-2026-07-04`
**Companion entries:** `architecture/deprecations.yaml` (deprecation schema, reserve as needed) + `AGENTS.md` §Recent cross-cutting closures (audit-pin mirror) + `CHANGELOG.md` `## Unreleased` (closure meta-entry).

---

## TL;DR

The Qdrant indexing chain (media_assets → outbox → IndexingHandler → clipindexer.IndexClip → Writer → QdrantRuntime → Qdrant runtime alias `media_assets_current` → SearchAdapter/HybridSearch) is **architetturalmente ben messo**, ma presenta un **red point critico** quando `qdrant.enabled=true` e `clipindexer.enabled=false`: il outbox può marcare `asset.index.requested` come `completed` senza scrivere in Qdrant (perché `IndexClip` short-circuit quando clipindexer è disabled). L'azione: **P0 fail-closed semantici** + **P1/P2 e2e test suites** che vincolano l'osservabilità end-to-end (non basta `outbox_events.status=completed` per dire "Qdrant funziona"). I sanity-check SQL/curl sono documentati come **Operator Pre-flight Checklist** (NON PRs — non inquinano `linked_issues`).
Per godlike/06 SSOT: la wave è **parallela e ortogonale** a `PR-QDRANT-FINAL-DECISION` (decisione live-or-dead delle 8 Qdrant fields in `composition.go`). Wave formale **`blocker: ["EXTERNAL-AUDIT-2026-07-04"]`** perché l'esecuzione è deferred al completamento di quella decision architetturale.

```
                       QDRANT CHAIN VERIFY — PRIORITY BANDS (2 PR + 1 Checklist)
                       ─────────────────────────────────────────────────────────

  ┌──── BAND A ──────── P0 FAIL-CLOSED SEMANTICS (deadline 2026-07-15) ────────┐
  │  1. PR-QDRANT-CONFIG-MISMATCH-GATE                                        │
  │     [qdrant.enabled=true && clipindexer.enabled=false → boot error]       │
  │  2. PR-QDRANT-INDEXCLIP-GUARD                                             │
  │     [IndexClip disabled ⇒ event NOT marked completed — silent-success     │
  │      guard + IndexingStatus typed state-machine]                          │
  └───────────────────────────────────────────────────────────────────────────┘

  ┌──── BAND B ──────── P1/P2 E2E TEST SUITES (deadline 2026-07-25→08-01) ────┐
  │  3. PR-QDRANT-E2E-YOUTUBE                                                 │
  │     [YouTube clip pipeline → outbox → media_assets.index_state=INDEXED    │
  │      → Qdrant scroll finds asset_id → hybrid search returns clip]         │
  │  4. PR-QDRANT-E2E-VOICEOVER                                               │
  │     [Voiceover finalization emits asset.index.requested → media_assets    │
  │      → outbox → Qdrant payload.source=voiceover]                         │
  │  5. PR-QDRANT-E2E-SUPERSEDE-GATE                                          │
  │     [source_version mismatch → IndexingHandler returns SupersedeError →  │
  │      outbox event = superseded → Qdrant contains only current version]    │
  │  6. PR-QDRANT-SEARCH-LIFECYCLE-FILTER                                     │
  │     [DELETED / DELETE_REQUESTED assets excluded from search results       │
  │      via lifecycle_state ACTIVE filter]                                  │
  └───────────────────────────────────────────────────────────────────────────┘

  ┌──── OPERATOR PRE-FLIGHT CHECKLIST ── 11 sanity probes (NO PR — SQL/curl) ─┐
  │  Test 1: health Qdrant                                                   │
  │  Test 2: schema collection v3 (text/transcript/visual/audio/bm25_text)    │
  │  Test 3: outbox event creato dopo YouTube (asset.index.requested)         │
  │  Test 4: outbox consumer completa davvero (status=completed)              │
  │  Test 5: media_assets passa a INDEXED                                    │
  │  Test 6: punto realmente dentro Qdrant (scroll per asset_id)             │
  │  Test 7: ricerca semantica / hybrid search                                │
  │  Test 8: supersede gate                                                   │
  │  Test 9: Qdrant spento (retry/recovery, non falso success)                │
  │  Test 10: delete Qdrant (tombstone lifecycle)                            │
  │  Test 11: voiceover → Qdrant                                             │
  └───────────────────────────────────────────────────────────────────────────┘
```

---

## 1. Honest Limitation Declaration (godlike/07)

### 1.1 The red point

Per il codice attuale, se:
```text
qdrant.enabled = true
clipindexer.enabled = false
```
Si può avere **falso successo di indicizzazione**. Perché:
- In `buildQdrantDeps`, se Qdrant è enabled ma il ClipIndexer è disabled, la runtime Qdrant viene costruita ma `runtime.Writer` non viene collegato a `clipIndexerService` tramite `SetVectorStore`.
- In `BuildOutboxBundle`, se `cfg.Qdrant.Enabled` è true, registra i core handlers con `qd.ClipIndexerService`.
- `IndexClip`, quando clipindexer è disabled, fa semplicemente `return nil` (skipping).
- L'outbox marca l'evento come `completed` senza scrivere davvero in Qdrant.

**Mitigazione richiesta (P0 fail-closed):** vietare la combo `qdrant.enabled=true && clipindexer.enabled=false` al boot (composition-time gate) OPPURE garantire che `IndexingHandler` non completi l'evento quando IndexClip ritorna nil-status. Entrambi gli approcci sono accettabili per godlike/07; il composition-time gate è preferito (fail-fast at boot > fail-slow at first /run).

### 1.2 Quattro assertion obbligatorie (non basta outbox completed)

Per dire "Qdrant è davvero end-to-end", non basta vedere `outbox_events.status=completed`:

1. `media_assets.index_state = INDEXED` (state machine EMBEDDING → EMBEDDED → INDEXING → INDEXED completata).
2. `Qdrant scroll` trova l'`asset_id` (punto materialmente scritto nel vector store).
3. `Search/HybridSearch` ritorna quel risultato (retrieval funzionante, non solo write).
4. `payload.lifecycle_state = ACTIVE` + `payload.search_text` presente (payload contract rispettato).

Se questi quattro passano per YouTube + Voiceover + Artlist, Qdrant è davvero end-to-end.

### 1.3 Static priority vs git-log frequency

Questa action plan è derivata da un'analisi statica del flusso Qdrant (catena di chiamate + outbox contract + state machine) — non da una misurazione di git-log frequency. Il forward-pointer entry `PR-QDRANT-CHAIN-VERIFY-HOTSPOT-CROSSREF` (analogo a `PR-GODOBJ-HOTSPOT-CROSSREF`, deadline 2026-08-15) porta la cross-reference post-wave:
```bash
git log --since=90.days --pretty=format: --name-only \
  | grep -E '^(internal/application/(assets|indexing)|internal/infrastructure/qdrant|internal/app/build_bundles_qdrant)' \
  | sort | uniq -c | sort -rn | head -20
```
Se emergono high-frequency hotspots non in questa lista (es. una `qdrant_admin` CLI emersa di recente), il forward-pointer li aggiunge alla wave-tracker SENZA inline rewrite (slim-schema ratchet).

### 1.4 Pre-existing build issues carry forward unchanged (per CHANGELOG convention)

Stesso carry-forward della sessione 2026-07-04:
- `monitor/enqueue.go` (`strings.ToLower` undefined)
- `monitor/scheduler.go` (`NewUnboundJobEnqueuer` undefined)
- `internal/app/workerruntime/{preflight.go, run.go}` syntax errors (FIX-APP-WORKERRUNTIME-SYNTAX shipped 2026-07-04 — `03d42b0c`)
- `internal/application/images/routing` (carry-forward import cycle)

Ogni per-PR commit in questa wave atterra in isolamento sul suo subtree e passa `gofmt + go vet + go build + go test -short` indipendentemente. Whole-project `go build ./...` è non-blocking per la CHANGELOG forward-pointer convention.

---

## 2. Band A — P0 fail-closed semantics

### 2.1 PR-QDRANT-CONFIG-MISMATCH-GATE (composition-root, internal/app)

**Cosa deve fare:** vietare la combo `qdrant.enabled=true && clipindexer.enabled=false` al boot. La gate abortisce `RegisterQdrantRuntime` (o equivalente in `internal/app/build_bundles_qdrant.go`) con un typed error `ErrQdrantIndexerMismatch` che nomina entrambi gli escape hatches (set `VELOX_FEATURE_CLIP_INDEXER_ENABLED=true` OR disable via `VELOX_FEATURE_QDRANT_ENABLED=false`).

**godlike/07 fail-closed rationale:** mai degradare silenziosamente a "Qdrant acceso ma IndexClip non scrive". La composizione deve fallire loud al boot, non alla prima `/run` request quando il falso successo si materializza.

**godlike/06 SSOT (single canonical owner):** la gate è nel composition root (`internal/app/build_bundles_qdrant.go` o `internal/app/wire_processes.go`). Estrazione in helper `validateQdrantIndexerCompatibility(cfg *config.Config) error` per unit-testabilità indipendente dal bundle (mirror del pattern `validateArtlistScraperURL` di ART-002 P0.1).

**Files to touch:**
- `internal/app/build_bundles_qdrant.go` (gate inline, position-aware)
- `internal/app/build_bundles_qdrant_test.go` (4 TDD tests)
- `config.example.yaml` (comment block: configurazione qdrant + clip-indexer)

**Verifier surface:** 4 TDD test idempotenti al pattern ART-002 P0.1:
- `TestValidateQdrantIndexer_NilCfg_ReturnsError` (defensive nil-guard)
- `TestValidateQdrantIndexer_BothDisabled_ReturnsNil` (zero-state skip)
- `TestValidateQdrantIndexer_BothEnabled_ReturnsNil` (happy path)
- `TestValidateQdrantIndexer_QdrantEnabledIndexerDisabled_ReturnsError` (canonical fail-closed case asserting 5 substrings: `QdrantEnabled=true` + `ClipIndexerEnabled=false` + `QDRANT-CHAIN-VERIFY-2026-07-04 P0` + `VELOX_FEATURE_CLIP_INDEXER_ENABLED=true` + `VELOX_FEATURE_QDRANT_ENABLED=false` disable-hint)

**Deadline:** 2026-07-15 (2 settimane; mirror ART-002 P0.1 cadence).

### 2.2 PR-QDRANT-INDEXCLIP-GUARD (IndexingHandler not silent-success)

**Cosa deve fare:** garantire che quando `IndexClip` ritorna `nil` (clipindexer disabled check interno), `IndexingHandler` NON notifichi l'outbox dispatcher per marcare l'evento come `completed`. Deve invece ritornare un typed error che il pool outbox tratti come `retryable` (NON terminal — non bloccare la chain ma permettere retry quando clipindexer torna enabled).

**godlike/07 typed-error contract:**
- `ErrIndexClipDisabledButEventRequested` (`errors.New` typed sentinel) per lo stato-level guard
- il pool outbox pool's `markEventCompleted` policy deve distinguere: `nil error` ⇒ completed; `errors.Is(err, ErrIndexClipDisabledButEventRequested)` ⇒ status=pending + retry policy (eventualmente dead_letter dopo N attempts con backoff esponenziale per il caso in cui clipindexer resti disabled)

**godlike/06 SSOT (canonical IndexingStatus state machine):** introdurre/rafforzare la state machine su `media_assets.index_state`:
- `EMBEDDING → EMBEDDED → INDEXING → INDEXED`
- FAILED paths: `EMBEDDING_FAILED` e `INDEXING_FAILED`
- NEW: `INDEXING_SKIPPED_NO_INDEXER` (per il case clipindexer disabled)

Per la state machine canonical, sfruttare i pattern già esistenti in PR-GODOBJ-PR-D YouTube discovery ledger + la `index_state` resident in `internal/infrastructure/database/sqlite/assets/clipindexer` (canonical post PR-AUDIO-CHANNEL-EXTENSION 2026-07-02).

**Files to touch:**
- `internal/application/assets/indexing/handler.go` (o nome canonico equivalente — verificare via `rg` post-discovery)
- `internal/application/assets/indexing/types.go` (NEW typed sentinel + state transitions)
- `internal/application/assets/indexing/handler_test.go` (5 TDD tests: happy + skip-on-disabled + retry-on-flapping)

**Deadline:** 2026-07-15 (parallelo a PR-QDRANT-CONFIG-MISMATCH-GATE; stessa finestra).

---

## 3. Band B — P1/P2 E2E test suites

### 3.1 PR-QDRANT-E2E-YOUTUBE (YouTube → index → search round-trip)

**Test contract:** YouTube clip pipeline → outbox → media_assets.index_state=INDEXED → Qdrant scroll finds asset_id → hybrid search returns clip + payload.lifecycle_state=ACTIVE + payload.search_text.

**godlike/06 SSOT (5-step projection sequence per godlike/06 qdrant-projection):**
1. Commit metadata in SQLite (media_assets row).
2. Persist outbox record `asset.index.requested.v1` IN SAME transaction.
3. Update Qdrant asynchronously and idempotently (con `payload.asset_id` + idempotency_key).
4. Track projection version (`source_version` + `target_index_version`).
5. Allow complete rebuild from SQLite (per godlike/07 fail-safe).

**godlike/07 typed-error asserts:** nessuno string-matching, nessun substring check — tutto via `errors.As` + `errors.Is` sui sentinels canonical (per il precedent `INTERNAL-V1-MEDIA-SEARCH`).

**NEW canonical surfaces esposte (post-PR):**
- `internal/application/assets/integration_test/qdrant_e2e_youtube.go` (o `tests/e2e/`, parallel-artlist pattern) — black-box `qdrant_e2e_test` package. Mirrors P2.1/P2.2/P2.3 di ART-002 (`tests/e2e/` directory + `e2e` package). Tight timeouts + hermetic fixtures + real components.

**Sub-tests (5):**
1. `happy_path_youtube_clip_to_qdrant_to_search` — full flow con un Mayweather-style fixture.
2. `replay_is_no_op` — replay same idempotency_key → ZERO new points, 1 final state.
3. `discover_search_via_text_query` — query con "boxing controversy" → top hit is the indexed clip.
4. `discover_search_via_transcript` — query usa transcript channel.
5. `lifecycle_active_filter` — DELETE_REQUESTED asset escluso dai risultati anche se presente in Qdrant.

**Deadline:** 2026-07-25 (3 settimane).

### 3.2 PR-QDRANT-E2E-VOICEOVER (Voiceover → index → search round-trip)

**Test contract:** Voiceover finalization (Stage 5 commit + post-commit verification, post PR-VO-FINALIZER-STEP6-EXTRACT closure 2026-07-04) emits `asset.index.requested` inside the transaction IF `FileHash` is present → media_assets.source=voiceover + media_assets.media_type=audio + outbox completed → Qdrant payload.source=voiceover.

**godlike/06 SSOT cross-references:**
- Voiceover finalizer `internal/application/voiceover/finalizer.go` (canonical SSOT post PR-VO-DECOMPOSITION-2026-07-04 wave closures 2026-07-04). The atomic outbox enqueue lives in the same TX as the media_assets write (the canonical "commit metadata in same transaction" step).
- `QdrantRuntime.Writer.UpsertAsset(ctx, asset)` — needs to support `MediaType=audio` for Voiceover writes (verify; if not, extend via `AssetStore.UpsertMany` polymorphic port).

**Sub-tests (3):**
1. `voiceover_finalizer_emits_outbox_event` — Voiceover pipeline produces outbox event IF file_hash present.
2. `voiceover_no_hash_no_index` — Voiceover without FileHash does NOT emit outbox event (no false indexing).
3. `voiceover_qdrant_payload_contract` — Qdrant payload matches canonical wire shape (source=voiceover, media_type=audio, language, search_text present).

**Deadline:** 2026-07-25 (parallela a PR-QDRANT-E2E-YOUTUBE; stessa finestra).

### 3.3 PR-QDRANT-E2E-SUPERSEDE-GATE (source_version invariant)

**Test contract:** asse 1: indicizza asset A con source_version vecchia → asse 2: aggiorna stesso asset con nuovo file_hash/source_version → asse 3: arriva vecchio asset.index.requested event.

**Expected:**
- vecchio evento = `superseded`
- nuovo evento = `completed`
- Qdrant contiene SOLO versione corrente (old point replaced o deleted+new_inserted a seconda del pattern canonico; verificare via rg).

**godlike/07 typed-error contract:** `SupersedeError` (già presente nel IndexingHandler surface per older commit 4-expanded della Stock cutover) — verificare che la check è su `event.SourceVersion` vs current `media_assets.source_version`.

**Files to touch:**
- `internal/application/assets/integration_test/qdrant_supersede_test.go` (NEW)
- `internal/application/assets/indexing/supersede.go` (o nome canonico; verificare esistenza) — solo se manca typed-error guard

**Sub-tests (2):**
1. `happy_path_supersede_old_event` — old event skipped + new event indexed.
2. `outoforder_events_processed_by_source_version` — eventi arrivati out-of-order → tutti superseded tranne quello con source_version=current.

**Deadline:** 2026-07-25.

### 3.4 PR-QDRANT-SEARCH-LIFECYCLE-FILTER (search adapter filter contract)

**Test contract:** `SearchAdapter` imposes `lifecycle_state=ACTIVE` filter on Qdrant query (DELETED / DELETE_REQUESTED / DRIVE_DELETE_PENDING / INDEX_DELETE_PENDING excluded). Soft-delete via lifecycle_state is the canonical semantics.

**godlike/06 SSOT cross-references:**
- Blocco 3.1 deletion state machine (id-28 closure 2026-07-01) established 5-state machine (ACTIVE → DELETE_REQUESTED → DRIVE_DELETE_PENDING → INDEX_DELETE_PENDING → DELETED).
- The 15-min VLM sweeper + the outbox dispatcher + the `drive.FileLifecycle` adapter co-operate per the id-28 closure.

**godlike/07 canonical search filter:** `search.Query.Filters.LifecycleStates = ["ACTIVE"]` (NOT substring match, NOT `IF lifecycle_state == ACTIVE` ad-hoc check) — typed-filter via `search.Filters` struct (per Pattern 0 post Wave 30 semantic multimodal search).

**Files to touch:**
- `internal/application/search/aggregator.go` — verify filter is propagated to ALL registered backends (semantic + provider + local).
- `tests/e2e/qdrant_lifecycle_filter_test.go` (NEW).

**Sub-tests (3):**
1. `active_assets_visible_in_search` — INSERT asset with lifecycle_state=ACTIVE → searchable.
2. `deleted_assets_hidden_from_search` — INSERT asset with lifecycle_state=DELETED → NOT searchable.
3. `soft_delete_during_search_returns_correct_set` — flip ACTIVE→DELETE_REQUESTED mid-search → asset disappears.

**Deadline:** 2026-08-01 (3.5 settimane; latest Band B deadline per l'integrazione aggregatore-side).

---

## 4. Operator Pre-flight Checklist (NO PR — SQL/curl sanity probes)

Per godlike/06 SSOT slim-schema, i sanity check SQL/curl NON diventano `linked_issues` (non sono "fix-this-now" tasks — sono smoke test per l'operatore). Vivono invece come **Operator Pre-flight Checklist** documentato in questo file (operatore esegue al boot o dopo ogni deploy).

**Regola operativa:** prima di dichiarare "Qdrant end-to-end funziona", esegui i 11 test in ordine.

### Test 1 — health Qdrant
```bash
curl -s http://localhost:6333/health
curl -s http://localhost:6333/collections | jq
```
**Atteso:** `media_assets_current` esiste come alias runtime + collection fisica v3 esiste + no 401/403/connection refused.

### Test 2 — schema collection v3
```bash
curl -s http://localhost:6333/collections/media_assets_current | jq
```
**Atteso:** `vectors.text:768`, `vectors.transcript:768`, `vectors.visual:768`, `vectors.audio:512`, `sparse_vectors.bm25_text` (DefaultV3Schema).

### Test 3 — outbox event creato dopo YouTube
```sql
SELECT id, event_type, aggregate_id, status, payload_json
FROM outbox_events
WHERE event_type = 'asset.index.requested'
ORDER BY id DESC LIMIT 20;
```
**Atteso:** status ∈ {pending, processing, completed}; aggregate_id = media_assets.id; event_key = `index:<asset_id>:<content_hash>:...`; payload_json contiene `schema_version: asset.index.requested.v1`.

### Test 4 — outbox consumer completa davvero
(stessa query di Test 3, dopo qualche secondo.)
**Atteso:** status=completed; last_error vuoto; completed_at valorizzato; attempt_count ≥ 1.

### Test 5 — media_assets passa a INDEXED
```sql
SELECT id, source, media_type, lifecycle_state, index_state,
       source_version, file_hash, metadata_json
FROM media_assets ORDER BY updated_at DESC LIMIT 20;
```
**Atteso:** index_state=INDEXED; lifecycle_state=ACTIVE; source_version + file_hash non vuoti; metadata_json contiene `indexed_content_hash`.

### Test 6 — punto realmente dentro Qdrant
```bash
ASSET_ID=<id>
curl -s http://localhost:6333/collections/media_assets_current/points/scroll \
  -H "Content-Type: application/json" \
  -d "{\"limit\":5,\"with_payload\":true,\"with_vector\":false,\"filter\":{\"must\":[{\"key\":\"asset_id\",\"match\":{\"value\":\"$ASSET_ID\"}}]}}" | jq
```
**Atteso:** 1 punto trovato; payload.asset_id = id; payload.source ∈ {youtube, voiceover, artlist, image}; payload.media_type valorizzato; payload.lifecycle_state=ACTIVE; payload.search_text presente.

### Test 7 — ricerca semantica / hybrid search
```bash
curl -s -X POST http://localhost:8080/internal/v1/media/search \
  -H "Content-Type: application/json" \
  -d '{"q":"boxing controversy commentary","mode":"hybrid","workspace_id":"<ws>"}' | jq
```
**Atteso:** top hit è la clip indicizzata; score > 0.5; preview_url firmata; media_type round-trips.

### Test 8 — supersede gate
```sql
SELECT id, aggregate_id, status, payload_json, last_error
FROM outbox_events
WHERE event_type='asset.index.requested' AND aggregate_id='<asset_id>'
ORDER BY id DESC;
```
**Atteso:** vecchi eventi = `superseded`; evento corrente = `completed`.

### Test 9 — Qdrant spento (resilienza)
1. Stop Qdrant (`docker compose stop qdrant`).
2. Genera nuova clip.
3. Osserva outbox: status=pending, attempt_count cresce, last_error valorizzato con Qdrant connection error.
4. Restart Qdrant (`docker compose start qdrant`).
5. Osserva outbox: status=completed entro max attempts.
6. Osserva media_assets.index_state: alla fine = INDEXED.

### Test 10 — delete Qdrant (tombstone)
Esegui delete via API oppure un tombstone test. Verifica:
- outbox event `asset.index.requested` con `lifecycle_state=DELETED`
- media_assets.lifecycle_state = DELETED
- Qdrant scroll per quell'asset_id → 0 punti
- search adapter non ritorna quell'asset nei risultati (lifecycle filter)

### Test 11 — voiceover → Qdrant
```sql
SELECT id, source, media_type, language, drive_file_id, file_hash, index_state
FROM media_assets WHERE source='voiceover' ORDER BY created_at DESC LIMIT 10;

SELECT id, event_type, aggregate_id, status, last_error
FROM outbox_events
WHERE event_type='asset.index.requested'
  AND aggregate_id IN (SELECT id FROM media_assets WHERE source='voiceover')
ORDER BY id DESC LIMIT 20;

curl -s http://localhost:6333/collections/media_assets_current/points/scroll \
  -H "Content-Type: application/json" \
  -d '{"limit":5,"with_payload":true,"filter":{"must":[{"key":"source","match":{"value":"voiceover"}}]}}' | jq
```
**Atteso:** media_assets.source=voiceover + media_type=audio; outbox completed; Qdrant payload.source=voiceover + media_type=audio.

---

## 5. Execution Order & Locks (godlike/07 EXPAND→BACKFILL→CUTOVER→CONTRACT)

### 5.1 Sequencing within QDRANT-CHAIN-VERIFY-2026-07-04

1. **Band A PR-QDRANT-CONFIG-MISMATCH-GATE** (deadline 2026-07-15). 1 PR, 4 tests. Independent of all other PRs in the wave. SHARE LOCK: none (composition root, no contention).
2. **Band A PR-QDRANT-INDEXCLIP-GUARD** (deadline 2026-07-15). 1 PR, 5 tests + 1 NEW typed sentinel. SHARE LOCK: `internal/application/assets/indexing/*` (canonical IndexingHandler surface). May contend with PR-GODOBJ-1 (`youtube usecase`) + PR-GODOBJ-2 (`monitor scheduler`) — coordinate via `git fetch && git log --oneline @{u}..HEAD` per AGENTS.md Git-Lesson-2/4/5.
3. **Band B PR-QDRANT-E2E-YOUTUBE** (deadline 2026-07-25). 1 PR, 5 e2e subtests in `tests/e2e/qdrant_e2e_test.go`. SHARE LOCK: `tests/e2e/` directory with ART-002 P2.1/P2.2/P2.3 + Wave 30 E2E.
4. **Band B PR-QDRANT-E2E-VOICEOVER** (deadline 2026-07-25). 1 PR, 3 e2e subtests. Shares `tests/e2e/` directory with Band B #3.
5. **Band B PR-QDRANT-E2E-SUPERSEDE-GATE** (deadline 2026-07-25). 1 PR, 2 subtests + maybe 1 light tightening on supersede.go.
6. **Band B PR-QDRANT-SEARCH-LIFECYCLE-FILTER** (deadline 2026-08-01). 1 PR, 3 e2e subtests + maybe 1 aggregator tightening. SHARE LOCK: `internal/application/search/aggregator.go` (Wave 30 audit pin) + `tests/e2e/`.

### 5.2 Sequential dependency

Band A → Band B: Band A MUST ship first (the fail-closed gates are preconditions for meaningful Band B tests — if Band A isn't there, Band B tests "would pass even with red-point bug live" pre-Band A).

Parallelo: PR-#3 + PR-#4 share tests/e2e/ directory. PR-#6 shares `internal/application/search/` with Wave 30 forward-pointers.

### 5.3 Blocker to PR-QDRANT-FINAL-DECISION (godlike/06 SSOT)

The wave-tracker entry has `blocker: ["EXTERNAL-AUDIT-2026-07-04"]` (the umbrella for `PR-QDRANT-FINAL-DECISION`). Per the EXTERNAL-AUDIT-2026-07-04 deadline 2026-08-01, the decision on the 8 Qdrant fields in `composition.go` (live-or-dead) MUST complete BEFORE this wave's Band B #6 closes (the lifecycle-filter surface intersects with the liveness decision on `LocatorCleaner` + `QdrantDeleter`).

If `PR-QDRANT-FINAL-DECISION` picks "retire", this wave's tasks #5 + #6 (supersede gate + lifecycle filter) are marked closed/obsolete (the underlying infrastructure is gone). If it picks "live", tasks #5 + #6 fully validate.

This is the canonical godlike/06 single-owner-per-fact discipline: `PR-QDRANT-FINAL-DECISION` owns the lifecycle, `QDRANT-CHAIN-VERIFY-2026-07-04` owns the behavior correctness. Two orthogonal surfaces, one blocker.

### 5.4 Migration sequence (godlike/07 EXPAND → BACKFILL → CUTOVER → CONTRACT)

Per canonical godlike/07 discipline adopted from PR-ARTLIST-* + PR-DRIVE-005 + PR-GODOBJ-* entries:

- **EXPAND** (now → deadline 2026-07-15): Band A fail-closed gates land; legacy surface coexists in parallel.
- **BACKFILL** (deadline 2026-07-25 → 2026-08-01): Band B E2E tests fix the test-fixture gaps; each test runs GREEN against the canonical surface.
- **CUTOVER** (deadline 2026-08-15 + PR-QDRANT-CHAIN-VERIFY-HOTSPOT-CROSSREF): forward-pointer verification — if hot-git-log paths emerge, slim-schema `linked_issues` adds them append-only.
- **CONTRACT** (deadline TBD post-metric-zero): each P0 fail-closed + each P1 e2e test gets physical `architecture/deprecations.yaml` entry only if removal is required (likely NOT for fail-closed guards — they STAY as composition-time gates per godlike/07 fail-fast-at-boot doctrine; removal would re-introduce the red point).

---

## 6. Wave-tracker Entry Pointer (canonical anchor)

**Live anchor:** `architecture/current.yaml#QDRANT-CHAIN-VERIFY-2026-07-04`. Per godlike/06 SSOT (one canonical owner per fact):
- **Narrative** lives here (per-band depth + per-test contract + Operator Checklist + locks).
- **Status/state** lives in the YAML tracker (id + status + linked_issues + deadlines + blocker).
- **Migration discipline** lives in CHANGELOG.md closure meta-entry.
- **Audit-pin archaeology** lives in agent-facing surfaces (config gate + production godoc comments).

Operator surfaces read YAML; engineering surfaces read markdown; cross-reference is the wave-tracker entry's `linked_issues[].id` value.

---

## 7. Cross-references

- `AGENTS.md` §godlike/06 SSOT (one canonical owner per fact) → §"Documentation Map" → §"Qdrant Entity Associations"
- `AGENTS.md` §godlike/07 (typed-error contract; no fake availability)
- `AGENTS.md` §Pattern 0 (port abstraction layer; compile-time `var _ pin`)
- `AGENTS.md` Pattern 5 (`qdrant_maintenance.go` per-mode split precedent in GODOBJ Band 3 #12)
- `AGENTS.md` Git-Lesson-2 (direct-to-main workflow; no `--no-ff`, no `--force`)
- `AGENTS.md` Git-Lesson-4 (non-fast-forward race recovery)
- `AGENTS.md` Git-Lesson-5 (byte-equivalent-replay acceptance)
- `architecture/current.yaml#PR-QDRANT-FINAL-DECISION` (decision lead time: deadline 2026-08-01)
- `architecture/current.yaml#GODOBJ-2026-07-03` (PR-GODOBJ-12 = `cmd/admin/qdrant_maintenance.go` per-mode split, Band 3 #12, mechanical)
- `architecture/current.yaml#AUDIT-RESIDUE-2026-07-04` (Q-prefixed build/test blockers Q4..Q9; Q9 is blocker:true for voiceover FanoutVoiceoversUseCase)
- `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` (meta-closure + 4-item carry-forward)
- `architecture/current.yaml#EXTERNAL-AUDIT-2026-07-04` (umbrella for Qdrant final-disposition decision — THIS wave's blocker)
- `architecture/current.yaml#id-28` (Blocco 3.1 deletion state machine — 5-state ACTIVE → DELETE_REQUESTED → DRIVE_DELETE_PENDING → INDEX_DELETE_PENDING → DELETED)
- `architecture/current.yaml#PR-AUDIO-CHANNEL-EXTENSION` (Clipindexer 4-channel extension including audio — Band B #4 voiceover→qdrant pre-condition)
- `architecture/current.yaml#id-30` (Wave 30 semantic multimodal search — Band B #6 lifecycle filter overlap)
- `AGENTS.md` §"Architecture (see ARCHITECTURE.md)" Qdrant section + SCHEMA v3 + association flow.
- `AGENTS.md` §"Critical Artlist rules (DL-006, DL-007)" (mirror Pattern 0 + composition-root fail-closed discipline pattern — Band A #1 directly inherits this precedent).

---

## 8. Lifecycle (audit trail)

| Date | Action | Status | Author |
|---|---|---|---|
| 2026-07-04 | Plan doc lands (this file) | in_progress | PipelineGen Agent |
| 2026-07-04 | Wave-tracker entry lands (lockstep) | in_progress | PipelineGen Agent |
| 2026-07-04 | CHANGELOG.md closure meta-entry lands | documentation-only | PipelineGen Agent |
| 2026-07-04 | AGENTS.md mirror lands (Recent cross-cutting closures) | documentation-only | PipelineGen Agent |
| 2026-07-04 | git fetch + rebase onto origin/main + ff-push | direct-to-main | PipelineGen Agent |
| 2026-07-15 | PR-QDRANT-CONFIG-MISMATCH-GATE + PR-QDRANT-INDEXCLIP-GUARD deadlines (Band A) | pending PRs | (TBD) |
| 2026-07-25 | PR-QDRANT-E2E-YOUTUBE + VOICEOVER + SUPERSEDE-GATE deadlines (Band B 1st wave) | pending PRs | (TBD) |
| 2026-08-01 | PR-QDRANT-SEARCH-LIFECYCLE-FILTER deadline (Band B 2nd wave) | pending PR | (TBD) |
| 2026-08-01 | PR-QDRANT-FINAL-DECISION deadline (BLOCCO dell'esecuzione) | forward-cite | (TBD) |
| 2026-08-15 | PR-QDRANT-CHAIN-VERIFY-HOTSPOT-CROSSREF deadline (post-wave moat check) | pending PR | (TBD) |
| post-2026-08-15 | Wave exit_gate flip (status: done / exit_signal: true) | UNLOCK | (TBD) |

---

**End of canonical action plan.** Future agents: read this file alongside `architecture/current.yaml#QDRANT-CHAIN-VERIFY-2026-07-04` for the canonical state surface. Operators: read §4 (Operator Pre-flight Checklist) BEFORE declaring "Qdrant end-to-end funziona" — i 4 assertion obbligatori (media_assets.index_state INDEXED + Qdrant scroll finds asset_id + Search returns the result + payload.lifecycle_state ACTIVE) sono il discipline finale.

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local> (per AGENTS.md Git-Lesson-3)
