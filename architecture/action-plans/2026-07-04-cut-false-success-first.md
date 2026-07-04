# Action Plan — Cut false-success, poi stock correctness, poi split (2026-07-04)

> **Trigger**: review mirata del repo (Marcuss-ops, 2026-07-04) — la priorità
> più intelligente non è "spezzare tutto": è **tagliare prima i punti dove
> il sistema può mentire**, poi i file ad alta frequenza, poi gli
> stub/dead path. La regola è brutale: se non c'è user story attuale, non
> deve vivere nel codice.

---

## Verdetto di priorità (dalla review)

Il sistema può MENTIRE in 4 punti ad alto impatto operativo. Ogni bug in
uno di questi può far sembrare completato qualcosa che non lo è
(false-success chain). Vanno chiusi **prima** di qualsiasi split
estetico.

| # | Area                              | Perché prima                                                    | Rischio se ignorato                                          |
|---|-----------------------------------|----------------------------------------------------------------|--------------------------------------------------------------|
| 1 | **Qdrant false-success chain**    | `qdrant.enabled=true` + `clipindexer.enabled=false` ⇒ outbox "completed" ma Qdrant vuoto | `outbox_events.status=completed` mente — serve `media_assets.index_state` + Qdrant scroll + search per confermare |
| 2 | **Generated image search 200-vuoto** | `GET /api/images/generated/search` ritorna 200 + lista vuota | "endpoint vivo ma feature pending" — smoke test passano ma l'utente non vede mai risultati |
| 3 | **Monitor `discovery.go` watermark** | commento dice "fail loud" ma codice logga+swallow `MaxDiscoveredAt` e `UpdateCursor` | incoerenza semantica — un giorno un cursor write failure passa inosservato |
| 4 | **Stock pipeline error swallow**  | `_ = MarkFailed` + `_ = f.Close()` + `var rng` globale + stub su step | il worker "completa" senza che le scritture siano davvero andate a buon fine |

---

## Ordine operativo (regola d'oro: tagliare il comportamento sbagliato PRIMA di dividere)

1. **Chiudere i false-success prima di tutto** (Qdrant, generated search, monitor watermark). Qui ogni bug può far sembrare completato qualcosa che non lo è.
2. **Stock pipeline subito dopo**, ma **non partendo dallo split estetico**. Prima: error swallow, `var rng`, fallback legacy, stub che saltano step. Poi split di `service.go` e `orchestrator_steps.go`.
3. **Rimuovere stub e future-proofing**: `scene_stubs.go`, `media_curator_stubs.go`, `discoverSearchQueries` in `discovery.go`, `StyleRegistry.Register`. La regola è brutale: se non c'è user story attuale, non deve vivere nel codice.
4. **Ridurre superfici duplicate**: endpoint immagini duplicati, typed enum duplicati, DTO/shape parallele, commenti storici che descrivono vecchie wave invece della semantica attuale.
5. **Solo alla fine file splitting "pulito"**: spezzare file lunghi ha senso solo dopo aver eliminato i path sbagliati; altrimenti si distribuisce il disordine in più file.

---

## Blocco 1 — P0 rapido (Qdrant + generated search + monitor watermark)

### `PR-QDRANT-CONFIG-MISMATCH-GATE` — fail-closed al boot

- **Cosa**: `validateQdrantIndexerCompatibility(cfg *config.Config) error` in `internal/app/build_bundles_qdrant_gates.go` (mirror del pattern `validateArtlistScraperURL` di ART-002 P0.1).
- **Quando**: `qdrant.enabled=true` MA `clipindexer.enabled=false` (o viceversa) ⇒ abort con messaggio actionable.
- **Fix**: 4 siti composition-root dove il check inline esiste già, promosso a helper canonico.
- **Test**: 4 TDD test (nil-cfg, both-disabled, both-enabled, qdrant-enabled-no-clipindexer-fail-closed).
- **Pattern**: godlike/07 no-fake-availability + godlike/06 SSOT (single canonical owner).
- **Deadline**: 2026-07-15.

### `PR-QDRANT-INDEXCLIP-GUARD` — guard su IndexClip

- **Cosa**: nuova typed sentinel `ErrIndexClipDisabledButEventRequested` + IndexingHandler fail-closed su `IndexClip` disabled-return-nil, NON silent-success.
- **Quando**: l'evento `asset.index.requested` arriva ma `IndexClip` ritorna nil per indexer disabilitato.
- **Fix**: typed state `INDEXING_SKIPPED_NO_INDEXER` in `media_assets.index_state` (5-state machine: `DISCOVERED` + `INDEXING` + `INDEXED` + `INDEX_PENDING` + `INDEXING_SKIPPED_NO_INDEXER`).
- **Test**: 5 TDD test sull'IndexingHandler (event-arrives-when-disabled, retry-policy, audit-pin, no-outbox-mark-completed, ttl-replay).
- **Pattern**: godlike/07 fail-closed-at-event-time (non solo al boot).
- **Deadline**: 2026-07-15.

### `PR-GENERATED-SEARCH-FIX` — `GET /api/images/generated/search` honesto

- **Decisione vincolante**: implementare davvero `ListImagesByOrigin` OPPURE rispondere 501 con typed not-implemented.
- **Raccomandazione review**: implementare (più valore, è già pronto come storage method).
- **Cosa**: riga `WHERE origin = ?` + `ORDER BY created_at DESC` + `LIMIT 200` con filter-by-locale opzionale.
- **Test**: 4 TDD test (empty, 1-row, locale-filter, 200-cap).
- **Pattern**: niente "endpoint vivo ma feature pending" — o implementi o dichiari 501.
- **Deadline**: 2026-07-15.

### `PR-MONITOR-WATERMARK-HONESTY` — semantica unica

- **Decisione vincolante**: o best-effort dichiarato ovunque, o failure reale propagata. Oggi è incoerente.
- **Raccomandazione review**: failure reale propagata (operationally safer).
- **Cosa**: `recordCycleEndWatermark` ritorna error invece di log+swallow; il broker decide retry policy; niente silent success.
- **Test**: 3 TDD test (sql-error-propagated, cursor-error-propagated, happy-path-no-error).
- **Pattern**: godlike/07 no-fake-availability + commento upfront che dichiara la semantica scelta.
- **Deadline**: 2026-07-15.

---

## Blocco 2 — Stock pipeline cleanup (prima correctness, poi split)

### `PR-STOCK-CORRECTNESS-FIX` — fix errori veri prima dello split

- **Cosa**:
  - `_ = o.stepStore.MarkFailed(...)` → `if err := o.stepStore.MarkFailed(...); err != nil { return err }` (propagation)
  - `_ = f.Close()` → `defer f.Close(); if err := f.Close(); err != nil { log.Warn + counter.Inc }`
  - `var rng` globale → seed deterministico da fingerprint del job (no global state)
  - Stub su step (`stepProcessBatch` che salta su clip-missing) → log+counter+return-typed-error
- **Test**: 6 TDD test (mark-failed-error-propagated, file-close-error-warn, deterministic-seed-same-input-same-output, clip-missing-not-skipped, fallback-removed, no-global-rng-state).
- **Pattern**: godlike/07 no-fake-availability (mai silent success).
- **Deadline**: 2026-07-22 (before any split).

### `PR-STOCK-SERVICE-SPLIT` — split `service.go` (DOPO il correctness fix)

- **Cosa**: `service.go` (~914 LoC, 27 edit recenti) → 5 file per capability:
  - `service.go` (orchestrator)
  - `job_handler.go` (handler)
  - `types_run.go` (typed envelopes)
  - `source_staging.go` (staging)
  - `manifest_builder.go` (manifest construction)
- **Test**: file-level smoke (existing tests pass byte-equivalent).
- **Pattern**: AGENTS.md Pattern 5 (per-capability split, ≤3 capabilities per file).
- **Deadline**: 2026-08-01 (after correctness fix lands).

### `PR-STOCK-ORCHESTRATOR-SPLIT` — split `orchestrator_steps.go`

- **Cosa**: `orchestrator_steps.go` (~949 LoC, stub + error swallow) → 8 file per fase:
  - `orchestrator.go` (slim orchestrator)
  - `phase_resolve.go` (source resolution)
  - `phase_plan.go` (planning)
  - `phase_stage.go` (staging)
  - `phase_manifest.go` (manifest build)
  - `phase_validate.go` (validation)
  - `phase_emit.go` (chunk emit)
  - `phase_project.go` (Qdrant projection)
- **Deadline**: 2026-08-01 (after correctness fix).

---

## Blocco 3 — Dead code removal (audit + delete brutale)

### `PR-STYLE-REGISTRY-REGISTER-AUDIT`

- **Cosa**: `rg "StyleRegistry\.Register\|s\.Register\b" internal/` per cercare caller reali.
- **Se 0 caller reali**: `git rm` di `StyleRegistry.Register` + rimozione dal port.
- **Se serve solo per YAML bootstrap**: deve esistere solo `Load`, non `Register`.
- **Test**: smoke test che la rimozione non rompe nulla.
- **Deadline**: 2026-07-25.

### `PR-SCENE-STUBS-AUDIT`

- **Cosa**: `internal/application/scripts/scene_stubs.go` ha tipi "per far compilare" dopo eliminazione della vera implementazione. Audit:
  - I tipi sono usati da test vecchi? → aggiornare test o rimuovere fixture
  - I tipi sono placeholder per future impl? → rimuovere (no future-proofing)
  - I tipi sono usati da codice reale? → promuoverli a tipi canonical
- **Deadline**: 2026-07-25.

### `PR-MEDIA-CURATOR-STUBS-AUDIT`

- **Cosa**: `media_curator_stubs.go` dichiara "future" + "reserved for future". Stessa audit logic.
- **Deadline**: 2026-07-25.

### `PR-MONITOR-DISCOVERY-QUERIES-AUDIT`

- **Cosa**: `discoverSearchQueries` in `internal/application/assets/monitor/discovery.go` — sezione del codice "future-proofing" su keyword extraction.
- **Audit**: è chiamato da qualcuno? Se no, delete.
- **Deadline**: 2026-07-25.

---

## Blocco 4 — Riduzione superfici duplicate

### `PR-DEDUP-IMAGES-ENDPOINT`

- **Cosa**: `/api/images/generate` vs `/api/images/generated/generate` producono stessa semantica con JSON identico.
- **Decisione**: tenere `/api/images/generated/generate` (più descrittivo), deprecare `/api/images/generate` con sunset header RFC 8594.
- **Deadline**: 2026-08-01.

### `PR-REMOVE-FUTURE-WAVE-COMMENTS`

- **Cosa**: rimuovere `// future wave`, `// reserved for future`, `// will be reintroduced` quando non c'è implementazione immediata.
- **Pattern**: godlike/07 minimum-blast-radius (no documentation debt per feature non implementate).
- **Deadline**: 2026-07-25.

---

## Blocco 5 — CI architectural checks (solo dopo YAML canonical fix)

### `PR-FIX-YAML-PARSE-LINE-1551`

- **Cosa**: fix del pre-existing YAML parse error in `architecture/current.yaml` (stray `?` char at ~line 1551 col 1) che rompe Check 19.
- **Deadline**: 2026-08-01.

### `PR-REMOVE-CI-AWK-SED-FALLBACKS`

- **Cosa**: dopo il fix YAML, rimuovere i fallback awk/sed in `scripts/ci-architectural-checks.sh` che compensano il file rotto.
- **Pattern**: CI deve essere semplice, non compensare file rotti.
- **Deadline**: 2026-08-08.

---

## Wave-tracker entry (da aggiungere a `architecture/current.yaml`)

```yaml
- id: CUT-FALSE-SUCCESS-FIRST-2026-07-04
  owner_capability: architecture
  status: pending
  exit_signal: false
  deadline: 2026-08-15
  description: |
    Review del 2026-07-04 (Marcuss-ops): priorità false-success-first
    sopra a split estetico. 4 P0 dove il sistema può mentire
    (Qdrant outbox-completed-no-write, generated search 200-vuoto,
    monitor watermark incoerente, stock error swallow). 3 blocchi
    operativi: P0 fix (Blocco 1), stock correctness+split (Blocco 2),
    dead code removal (Blocco 3). 5+ azioni cliccabili pronte.
  linked_issues:
    - PR-QDRANT-CONFIG-MISMATCH-GATE      # deadline 2026-07-15
    - PR-QDRANT-INDEXCLIP-GUARD           # deadline 2026-07-15
    - PR-GENERATED-SEARCH-FIX             # deadline 2026-07-15
    - PR-MONITOR-WATERMARK-HONESTY        # deadline 2026-07-15
    - PR-STOCK-CORRECTNESS-FIX            # deadline 2026-07-22 (PRIMA dello split)
    - PR-STOCK-SERVICE-SPLIT              # deadline 2026-08-01
    - PR-STOCK-ORCHESTRATOR-SPLIT         # deadline 2026-08-01
    - PR-STYLE-REGISTRY-REGISTER-AUDIT   # deadline 2026-07-25
    - PR-SCENE-STUBS-AUDIT                # deadline 2026-07-25
    - PR-MEDIA-CURATOR-STUBS-AUDIT        # deadline 2026-07-25
    - PR-MONITOR-DISCOVERY-QUERIES-AUDIT  # deadline 2026-07-25
    - PR-DEDUP-IMAGES-ENDPOINT            # deadline 2026-08-01
    - PR-REMOVE-FUTURE-WAVE-COMMENTS      # deadline 2026-07-25
    - PR-FIX-YAML-PARSE-LINE-1551         # deadline 2026-08-01
    - PR-REMOVE-CI-AWK-SED-FALLBACKS      # deadline 2026-08-08
```

---

## Cosa NON farei adesso

- Non creerei nuove interfacce, registry o framework interni.
- Non aggiungerei nuove action plan se il codice può essere corretto direttamente.
- Non spezzerei file solo perché lunghi se dentro hanno ancora false-success o stub: prima si taglia il comportamento sbagliato, poi si divide.

## La prossima mossa più pulita

**P0 generated search + Qdrant false-success** — sono piccoli ma ad
altissimo impatto sulla fiducia del sistema. Aprire i due PR oggi.

## Honest-limitation (godlike/07)

Il verdetto di priorità è STATIC (per complessità + rischio accumulato),
non da `git log --since` frequency measurement. Cross-validation
post-wave via `PR-CODE-QUALITY-HOTSPOT-CROSSREF` (deadline 2026-08-15)
se emergono hotspot ad alta frequenza non catturati qui.

Pre-existing build issues (5-item carry-forward list per
`architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04`)
NON sono regressioni di questo action plan — carry-forward
immutato.

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>.
