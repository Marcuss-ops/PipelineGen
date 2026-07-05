# Cleanup Priority 1-5 — Action Plan (2026-07-25)

**Companion narrative per `architecture/current.yaml#CLEANUP-PRIORITY-1-5-2026-07-25`.**

## §1 — Contesto e_scope

Audit Hard-Tech del 2026-07-25 prodotto dal flow di priorità marcate a caldo dall'utente.
5 superfici con accumulo di debito tecnico rumoroso, ciascuna con un blast-radius
distinto ma un denominatore comune: **godlike/07 no-fake-availability** (nessun silent-success,
nessun noop-residual, nessun fallback hard-coded).

## §2 — Per-Priority Decomposition

### Priority 1 — handler_legacy_* /generate-from-clips e /generate-with-images
- **Audit-pin surface** (file canonici):
  - `internal/api/script/handler_flow.go::RegisterRoutes` righe 113+114 (le route POST)
  - `internal/api/script/handler_legacy_from_clips.go` (~184 LoC)
  - `internal/api/script/handler_legacy_with_images.go` (~92 LoC)
  - `internal/api/script/handler_legacy_deprecation.go` (counter declarations)
  - `internal/api/script/handler_legacy_warnings.go` (~helper)
- **Verdict**: FASE 2.1 FREEZE-phase retirement pattern (SSOT già esiste in wave-tracker entry
  `architecture/current.yaml#FASE-2.1-VOICE-FREEZE`). Le 2 route restano vive fino al 2026-12-31
  con Prometheus counters `legacy_generate_from_clips_total` + `legacy_generate_with_images_total`
  che monitorano il traffico settimanale. **NON** è un git-rm secco: è un FREEZE con log.Warn
  + audit-pin Header `X-Deprecated: true` sulle 4xx responses (additionally surfaces client
  diagnostic).
- **Per-PR execution**:
  - **PR-CLEANUP-P1-LEGACY-410**: convertire le 2 route a HTTP 410 Gone con payload JSON che
    punta a `/generate` (= V2 envelope canonical endpoint) + migration Sunset header RFC 8594.
    Triplo path: (a) operatori attivi migrano esplicitamente; (b) clients che ignorano il
    410 continuano sui vecchi path senza uno stack trace; (c) il counter legacy_*_total
    decrementa naturalmente fino a 0 = trigger per git-rm finale.

### Priority 2 — ScriptFlowDeps / module.go (~22 fields, 18 nil-tolerant, 12 ignoti)
- **Audit-pin surface** (file canonici):
  - `internal/api/script/handler_deps.go::ScriptFlowDeps` (22 fields, ~12 ignored post-Build)
  - `internal/api/script/module.go::Dependencies` (mirror, 22 fields)
  - `internal/api/script/handler_deps.go` linee 50-100 (il docblock che ammette "12 deps
    ignored for compatibility")
- **Verdict**: godlike/06 SSOT splittaggera. `module.go::Build` valida SOLO `Engine +
  EnabledFunc` (mandatory) — gli altri 18 sono nil-tolerant con 503-equivalent sentinels
  runtime. **`ScriptFlowDeps` non può essere rimosso** (è il construction seam byte-stable
  preservato da PR-SCRIPT-DEPENDENCIES-EXTRACT 2026-07-04) ma il suo sottoinsieme vivo
  può essere potato dopo audit di runtime-reads.
- **Per-PR execution**:
  - **PR-CLEANUP-P2-SCRIPT-DEPS** (deadline 2026-08-15): rg static-read attivo per ogni
    campo di `ScriptFlowDeps`; i campi con 0 reader attivo vengono fisicamente rimossi
    dal struct (godlike/07 zero-legacy); NewScriptFlowHandler trims gli assignment orfani;
    i nil-tolerant sentinels runtime restano (sono la difesa in profondità per Build).

### Priority 3 — noopEntityExtractionAdapter + noopMetadataGenerationAdapter
- **Audit-pin surface** (file canonico):
  - `internal/application/scripts/adapters/compat_adapters.go` (l'intero file)
  - 2 constructors: `NewEntityExtractionAdapter(any)` + `NewMetadataGenerationAdapter(any, string)`
  - 2 zero-call methods: `ExtractEntities → &EntityResult{}, nil` + `GenerateMetadata → nil, nil`
- **Verdict**: godlike/07 no-fake-availability violatione CRITICA. Silent success con payload
  vuoto = i post-processori che li chiamano credono di aver generato metadata/entità quando
  in realtà non è successo nulla. Pattern "ship d'un fake-availability carrier"; nessun
  valore di compat-layer perché non c'è interop layer attivo sopra.
- **Per-PR execution**:
  - **PR-CLEANUP-P0-NOOP-KILL** (deadline 2026-08-01): physically git-rm del file intero.
    Production wiring DEVE passare adapter reali (composition-root pre-costruisce i veri
    `EntityExtractor` + `MetadataGenerator` concreti). Se qualche caller runtime li usa ancora
    oggi, viene rilevato da `go build ./...` (le chiamate non vengono sostituite da ad-hoc
    constructors) → fail-closed al composition time (godlike/07 fail-fast-at-boot > fail-slow-at-first-/run).

### Priority 4 — Qdrant readiness double probe + noop stubs
- **Audit-pin surface** (file canonici):
  - `cmd/admin/qdrant_readiness.go::qdrantReadiness` (orchestrator, ~250 LoC)
  - `cmd/admin/qdrant_readiness.go::qdrantProbeAndSchema` (probe function, ~30 LoC)
  - `cmd/admin/qdrant_readiness_checks.go` linee 374-401 (readiNoopOutbox + readiNoopPayload
    + 2 nil-returning methods)
  - sempre in `qdrantReadiness`: `report.Checks["qdrant_active_collection_real"]` (linee
    ~305+307) impostato in 2 posti (probe path + check map).
- **Verdict**: 2 problemi distinti. (A) **Double probe**: `qdrantReadiness` chiama
  `qdrantProbeAndSchema` (~30 LoC che fa probe + schema + comparison) E POI popola `report.Checks["qdrant_active_collection_real"]`
  AND c'è un secondo check dedicato `checkQdrantActiveCollection` nel registry `readinessCheck`
  map che fa di nuovo probe + schema + compare. (B) **noop stubs**: `readiNoopOutbox` +
  `readiNoopPayload` ritornano sempre `nil` per le 3 metodi `EnqueueReindex` + `EnqueueDelete`
  + `DeletePayloadKeys`. Se Production wiring non passa i reali outbox/payload port, la gate
  diventa un fake `pass`.
- **Per-PR execution**:
  - **PR-CLEANUP-P1-QDRANT-PROBE** (deadline 2026-08-08): rimuovere il blocco probe-side
    mapping `qdrantProbeAndSchema` + il check `checkQdrantActiveCollection` dedicato (uno dei due)
    — consolidare il probe solo nel check dedicato; oppure rimuovere il check dedicato e tenere
    solo il probe. Decision via `rg _ = readinessCheck` post-audit.
  - **PR-CLEANUP-P1-QDRANT-NOOP-REMOVAL** (deadline 2026-08-08): rimuovere `readiNoopOutbox`
    + `readiNoopPayload` types + le 3 nil-returning methods. Composition-root MUST pre-costruire
    i reali port; il readiness gate fall-closed loudly al boot-time se nil (godlike/07).

### Priority 5 — resolveMaxRetries 3 rami legacy + Registry opzionale
- **Audit-pin surface** (file canonici):
  - `internal/application/jobs/enqueue_service.go::resolveMaxRetries` (~25 LoC, 3 branches:
    `currentMR < 0 → 0` + `currentMR > 0 → currentMR` + `currentMR == 0 && hasRegistry → DefaultMaxRetries` +
    altrimenti `return 3`)
  - `internal/application/jobs/service.go::Service.WithRegistry` (~15 LoC, fluent setter opzionale)
  - 4-5 `WithRegistry(reg)` callers production-side (Worker.WithRegistry, Runner.WithRegistry,
    Dispatcher.WithRegistry, Service.WithRegistry) tutti in modalità fluent opzionale
- **Verdict**: godlike/07 zero-legacy debt. Il fallback hard-coded `return 3` è una SSOT violatione
  ("il fatto MaxAttempts default" ha 2 owner — il caller + il registry). `WithRegistry` opzionale
  mantiene il legacy 3-retry path alive anche quando nessuno chiama il setter.
- **Per-PR execution**:
  - **PR-CLEANUP-P2-RETRIES-WIRING** (deadline 2026-08-15): (a) `NewService(...).WithRegistry(...)`
    diventa obbligatorio al construction time (no-fluent-setter disaccoppiato); (b) quando
    `registry.HasType(jobType) == false`, emit `log.Warn` udibile (operational dashboard visibility)
    al primo enqueue → fail-loud, fail-fast. (c) `resolveMaxRetries` semplificato: solo 2 rami
    (`negative sentinel` + `registry.DefaultMaxRetries(jobType)`), niente fallback hard-coded.

## §3 — Per-Band Execution Order

3 priority band (godlike/06 slim-schema + wave-ratchet discipline):

### Band A — P0 absolute (deadline 2026-08-01)
1. **PR-CLEANUP-P0-NOOP-KILL** (Priority 3) — physically git-rm `compat_adapters.go`.
   Rationale: godlike/07 no-fake-availability CRITICA, silent-success blooper production-blocker.

### Band B — P1 (deadline 2026-08-08)
2. **PR-CLEANUP-P1-LEGACY-410** (Priority 1) — Freeze-pattern retired route a HTTP 410 Gone
   con X-Deprecated header + audit counters (FASE-2.1-VOICE-FREEZE reuse pattern).
3. **PR-CLEANUP-P1-QDRANT-PROBE** (Priority 4A) — collapse double probe + retire
   checkQdrantActiveCollection (o il probe-side mapping).
4. **PR-CLEANUP-P1-QDRANT-NOOP-REMOVAL** (Priority 4B) — git-rm readiNoopOutbox +
   readiNoopPayload types + i 3 nil-returning methods.

### Band C — P2 (deadline 2026-08-15)
5. **PR-CLEANUP-P2-SCRIPT-DEPS** (Priority 2) — potatura `ScriptFlowDeps` + `Dependencies` dopo
   audit runtime-reads; trim NewScriptFlowHandler assignment.
6. **PR-CLEANUP-P2-RETRIES-WIRING** (Priority 5) — `WithRegistry` mandatory + log.Warn when
   no registry + simplified `resolveMaxRetries` (2 branches).

## §4 — Migration Sequence (godlike/07 EXPAND→BACKFILL→CUTOVER→CONTRACT)

Per ciacun PR:
- **EXPAND**: nuovo surface live + ci-gate forward-prevention (ban raw call sites).
- **BACKFILL**: migrate production callers alla nuova API typed.
- **CUTOVER**: retire legacy surface con deprecation record `architecture/deprecations.yaml`.
- **CONTRACT**: physical git-rm del legacy surface + ci-gate tightened to "no allowlist row".

## §5 — Per-PR Verification Gates (godlike/07 minimum-blast-radius)

Per ciascuna PR:
- `gofmt -l` clean sul targeted file subtree.
- `go vet ./<package>/...` exit 0.
- `go build ./<package>/...` exit 0 (subtree-only, fail-fast isolation).
- `go test -short -count=1 ./<package>/...` PASS (TDD coverage sul contratto nuovo).
- `rg <retired_symbol> internal/ cmd/` → 0 hits production-code (zero-legacy audit-pin).

## §6 — Honest Scope-Lock Disclosures (godlike/07 minimum-blast-radius)

- **`ScriptFlowDeps` + `Dependencies`** NON possono essere rimossi interamente (sono i construction
  seam byte-stable preservati da PR-SCRIPT-DEPENDENCIES-EXTRACT 2026-07-04 + il C-1 module
  precedent di 11 conversioni). Si può solo potare i campi con 0 reader attivo (Band C PR).
- **handler_legacy_\*.go** NON vengono git-rm oggi; restano per FASE 2.1 FREEZE-phase fino al
  2026-12-31 (wave-tracker entry `architecture/current.yaml#FASE-2.1-VOICE-FREEZE` è già shipped).
  PR-CLEANUP-P1-LEGACY-410 eleva il freeze a 410 Gone (operator-visible migration signal).
- **resolveMaxRetries fallback hard-coded 3** NON viene rimosso fisicamente fino a quando il
  Registry non è mandatory al construction-time (Band C); il PR intermedio rimuove solo il
  fluent setter opzionale, lasciando il 3-retry come fail-closed escape hatch.
- **5-item pre-existing build issues** carry-forward unchanged per `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04`
  (workerruntime syntax + monitor tolower + monitor enqueuer + stockpipeline redeclaration +
  module_media dispatcher literal + images routing cycle — gli ultimi 5 NON SO risolti in
  questo wave; sono carry-forward storico).

## §7 — Cross-References (godlike/06 SSOT lockstep)

- `architecture/current.yaml#CLEANUP-PRIORITY-1-5-2026-07-25` (wave-tracker anchor; questo file)
- `architecture/current.yaml#FASE-2.1-VOICE-FREEZE` (precedent per Priority 1 FREEZE pattern)
- `architecture/current.yaml#PR-DEAD-CODE-PURGE-2026-07-25` (precedent per StyleRegistry.Register
  0-action false-premise closure — usato come modello per git-rm senza codice change)
- `architecture/current.yaml#ART-001` (precedent doc-only 0-action closures per i backfill PRs)
- AGENTS.md §Pattern 5 (canonical split-pattern per Scripts deps pruning)
- AGENTS.md §Pattern 6 (per-file granularity per i 6 PRs).

## §8 — Honest-Limitation Disclosure (godlike/07)

L'analisi STATICA (priority-by-complexity + accumulated-risk) NON sostituisce
una validazione post-wave con `git log --since=90.days`. Forward-pointer:
**PR-CLEANUP-HOTSPOT-CROSSREF** (deadline 2026-08-22) cross-valida via `git log --since=90.days
--pretty=format: --name-only | sort | uniq -c | sort -rn | head -30` se alti-frequency hotspots
NON catturati dal piano corrente (slim-schema append-only ratchet li aggiungerebbe).

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>. AGENTS.md Git-Lesson-3.
