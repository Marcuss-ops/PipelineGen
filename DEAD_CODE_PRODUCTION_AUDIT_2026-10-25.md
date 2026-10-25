# Audit codice morto di produzione — `refactored` — 2026-10-25

## TL;DR

- **406 package** Go nel modulo; **369** sono raggiungibili dai tre entrypoint di
  produzione (`cmd/server`, `cmd/worker`, `cmd/admin`). I restanti **37** sono
  tool di CI/ops o package di test: **nessun package di produzione è orfano**.
  Il codice morto vive dentro package vivi, non in package interi.
- `deadcode` con radici **solo i main di produzione** trova **1.339 funzioni
  irraggiungibili** in **155 package**. **149 file non-test** hanno *tutte* le
  funzioni irraggiungibili (**28.199 righe**), ma solo 48 sono eliminabili
  senza toccare tipi/const/helper ancora vivi. Sul totale delle funzioni:
  - **241** stanno in **48 file** le cui funzioni sono *tutte* morte e che non
    contengono tipi/helper usati dalla produzione: **9.575 righe**.
  - **109** stanno in **12 file** che sono quasi interamente morti ma contengono
    ancora tipi/helper vivi: seconda ondata, ~3.800 righe, richiede un
    micro-refactor.
  - Le altre **~989** sono funzioni morte sparse in file ancora vivi (overlays,
    kernel/script, kernel/asset/detail, entities, voiceover, …).
- **I 48 file sono stati rimossi in una copia hardlink del repo e
  `go build ./...` è tornato verde.** Nessuno dei file rimossi ha `init()`,
  `//go:linkname` o `//go:generate`. Sono tenuti in vita **solo dai test**.
- I **28 file di test** associati (6.372 righe) vanno rimossi/adattati. Dopo il
  taglio, `go vet ./...` segnala riferimenti residui in altri file di test:
  elencati in §5.
- **Perché sono sopravvissuti alle potature precedenti:** il commit
  `2b9a9c198` ("drop 46 unreachable Go files") ha usato
  `deadcode -test ./...`, cioè ha trattato i test come radici di raggiungibilità.
  Il codice usato solo dai test risultava vivo. Questo audit usa le radici di
  produzione, quindi trova esattamente quel residuo.

## 1. Metodo

Entry point considerati (sono quelli costruiti nel `Dockerfile`):

| Binario | Sorgente | Immagine |
|---|---|---|
| `pipelinegen` | `cmd/server` | `server-runtime` |
| `pipelinegen-worker` | `cmd/worker` | `worker-runtime` |
| `pipelinegen-admin` | `cmd/admin` | `admin-runtime` |

Comandi eseguiti:

```bash
go list -deps ./cmd/server ./cmd/worker ./cmd/admin
deadcode ./cmd/server ./cmd/worker ./cmd/admin
```

Verifica del taglio, in una copia hardlink (`.tmp/deadcode-verify`) per non
toccare il working tree:

```bash
cp -al refactored .tmp/deadcode-verify
# rimozione dei 48 file + dei 28 test associati
cd .tmp/deadcode-verify
go build ./...   # exit 0
go vet ./...     # errori residui solo nei test, vedi §5
```

Criteri:

- una funzione è "morta in produzione" se `deadcode` non la raggiunge da
  nessuno dei tre main;
- un file è "tagliabile interamente" se **tutte** le sue funzioni sono morte e
  la rimozione non rompe il build (quindi non definisce tipi/const/var usati da
  codice vivo);
- "ultimo commit" è l'ultimo commit git che tocca il file; molti sono sweep
  `errcheck` o refactor globali, non modifiche funzionali. Il codice di questi
  blocchi è datato **giugno–settembre 2026** nei commenti di testa.

## 2. Blocchi tagliabili subito — build verificata

Totale: **48 file, 9.575 righe di produzione + 6.372 righe di test**.

| Blocco | File | LOC prod | LOC test | Ultimo commit | Perché è morto |
|---|---:|---:|---:|---|---|
| Research boxing (`EvidencePack`/ranking) | 2 | 911 | 206 | 2026-08-23 | Solo il test `research_fanout_test.go` lo importa; la produzione usa `kernel/script.ResearchEvidencePack`, che è un altro tipo |
| Mediamemory scene visual plan | 4 | 1.258 | 998 | 2026-10-23 (errcheck) | Generator/worker/DTO/layout non sono istanziati; `SceneVisualPlan` vive in `types_resolver.go` e resta |
| Script usecases legacy | 4 | 1.097 | 1.224 | 2026-08-31 | Planner deterministico, scene-plan builder, post-voiceover composer e semaphore: solo test |
| Qdrant maintenance + audit | 11 | 1.648 | 1.009 | 2026-10-23 (errcheck) | `qdrant-maintenance` non è più registrato in `cmd/admin/subcommands.go`; `dr-qdrant` usa solo `dr_adapter.go`; `audit` è importato solo dallo scanner morto |
| VidRush legacy adapters | 3 | 367 | 307 | 2026-09-20 | Source resolver, visual-window planner e segment-research adapter: solo test; il path VidRush attivo usa i registry searcher |
| Processor family legacy | 8 | 1.126 | 591 | 2026-09-12 | Image/clip/semantic/materialization processor sostituiti dai `processor_vidrush_*` e dagli adapter attivi |
| SQLite asset channels | 3 | 676 | 268 | 2026-10-23 (errcheck) | I tre repository (license/release/rendition) sono istanziati solo dai test; le tabelle/migrazioni restano |
| Assets ingest legacy | 2 | 608 | 255 | 2026-10-25 (errcheck) | Vecchio `ClipIngestPipeline` e `ImageStoreAdapter`; nessun costruttore in produzione |
| Qdrant indexing legacy | 3 | 713 | 814 | 2026-10-23 (errcheck) | Wire envelope e indexer/semantic mediamemory: nessun chiamante di produzione; il piano media Qdrant è ritirato |
| Overlay render helpers | 4 | 541 | 170 | 2026-10-16 | Dead-air, plan fingerprint, scene-intelligence candidates, visual continuity: usati solo dai test (vedi rischio §6) |
| Voiceover dead files | 2 | 380 | 285 | 2026-09-20 | `usecase.go` e `e2e_fixtures.go`: nessun chiamante di produzione |
| Providerassets catalog builder | 1 | 121 | 80 | 2026-09-20 | `NewCatalogBuilder`/`NewProviderPolicyRegistry`: nessun chiamante; il tipo `ProviderAsset` resta |
| Render fingerprint | 1 | 129 | 165 | 2026-08-23 | `ComputeSceneFingerprint`/`AssetsFromManifest`: solo test |

### 2.1 Research boxing — il caso più evidente

`internal/capabilities/research/ranking.go` (500 righe) e
`evidence_pack.go` (411 righe) erano già stati potati come **DEAD** e poi
ripristinati dal commit `9be5da737`:

```text
fix: restore research (used by scripts/usecase test) + overlay contract + cliprender GOP
- internal/capabilities/research/*: restore 5 files (was pruned as DEAD but
  research_fanout_test.go:12 imports it for EvidencePack quality gate,
  prod 0 test 1 but test is LIVE — move to KEEP with hotspot, not DELETE)
```

Cioè: **produzione 0, test 1**. Il blocco è un contratto "boxing" (entità,
fonti, credibilità, ranking finanziario) che non è mai entrato nel path di
produzione. Il test `research_fanout_test.go` va rimosso o riscritto sul
contratto generico, poi i due file e i due test dedicati possono sparire.

### 2.2 Qdrant maintenance/audit

`cmd/admin/subcommands.go` non contiene più `qdrant-maintenance`. Il package
`internal/capabilities/audit` è importato solo da
`internal/capabilities/maintenance/scanner.go`, che a sua volta è morto
(`QdrantScannerAdapter` non viene mai istanziato). `dr-qdrant` usa solo
`dr_adapter.go`, che **resta**. Il blocco è fermo al codice `FASE 1.2` di
giugno/luglio 2026; gli ultimi commit sono pulizie `errcheck` e il baseline del
2026-09-20.

### 2.3 Mediamemory scene visual plan

I tipi `SceneVisualPlan` e `Layer` sono definiti in
`internal/capabilities/mediamemory/types_resolver.go` e sono usati dalla
produzione (`scripts/adapters/processor_visual_planning.go`,
`postprocess_result.go`, `pipeline_result.go`). Sono morti **solo** il
generatore di default, il materialize worker, il DTO e il layout. Non eliminare
`types_resolver.go`.

### 2.4 Processor family legacy

I file del blocco sono:

```text
internal/capabilities/scripts/adapters/processor/semantic_asset_search.go
internal/capabilities/scripts/adapters/processor/clip_resolver.go
internal/capabilities/scripts/adapters/processor/processor_images.go
internal/capabilities/scripts/adapters/processor/processor_images_fanout.go
internal/capabilities/scripts/adapters/processor/asset_materializer.go
internal/capabilities/scripts/adapters/processor/entity_media_links.go
internal/capabilities/scripts/adapters/processor/materialization_pipeline.go
internal/capabilities/scripts/adapters/processor/segment_understanding_model.go
```

`typed_fail_adapters.go` **non** è nel blocco: 2 delle 4 funzioni sono vive.

## 3. Seconda ondata — file quasi interamente morti ma con tipi/helper vivi

Questi file non si possono eliminare in blocco senza un micro-refactor. La
verifica di build ha mostrato esattamente cosa resta vivo.

| File | LOC | Funzioni morte | Cosa resta vivo | Azione minima |
|---|---:|---:|---|---|
| `jobs/completion/complete_job_service.go` + `complete_with_artifacts_service.go` + `complete_with_artifacts_helpers.go` + `publish_and_complete_use_case.go` | 1.581 | 26/26 | `WithArtifactsService` è referenziato da `complete_job_ports.go` (pin d'interfaccia) | rimuovere il pin/porta o lasciare uno stub del tipo, poi eliminare i 4 file e i test |
| `scripts/adapters/vidrush_youtube_adapter.go` | 579 | 31/34 | 3 helper (`youtubeSourceRequired`, `youtubeSourcesForSegment`, `youtubeQuery`) usati da `vidrush_fanout_*.go` | estrarre i 3 helper, eliminare provider/metodi |
| `platform/qdrant/search/semantic_asset_search_adapter.go` | 340 | 7/7 | `semanticAssetSearchAdapter` e `validateScope` usati da `slots_search.go` | tenere i due simboli, eliminare il resto |
| `capabilities/render/strategy.go` | 323 | 10/10 | `CompatibilityReport`, `StrategyResolver`, `ExecutionRender` usati da `capabilities/replay/engine.go` | tenere i tipi, eliminare i metodi |
| `capabilities/mediamemory/registry.go` | 220 | 7/7 | `RankingStrategy` usato da `ranker.go` | tenere il tipo, eliminare il registry |
| `capabilities/voiceover/service/parent_state.go` | 217 | 3/3 | costanti `ParentState`/`ParentFailed`/… usate da `voiceover/service/jobs` | tenere le costanti |
| `platform/qdrant/schema/projection_contract.go` | 218 | 9/9 | tipi/costanti delle projection usati da `qdrant/collections` | tenere i tipi, eliminare le funzioni |
| `capabilities/assets/providerassets/registry.go` | 164 | 9/9 | solo il tipo `Registry`, usato dal campo **mai assegnato** `VidRushDeliveryPorts.ProviderAssets` | rimuovere il campo (una riga), poi eliminare il file |
| `capabilities/voiceover/service/types_helpers.go` | 158 | 7/7 | helper usati da `process.go`, `stages.go`, `promo.go` | **non eliminare**: è un falso positivo di `deadcode` |

Totale seconda ondata: **~3.800 righe**, di cui ~3.200 eliminabili dopo i
micro-refactor.

Nota importante: `types_helpers.go` dimostra che `deadcode` non è infallibile
sulle chiamate a metodi/helper. La build di verifica è il criterio decisivo.

## 4. Long tail — funzioni morte in file vivi

Restano **~989 funzioni morte** in file che contengono anche codice vivo. Non
sono "blocchi interi" e richiedono rimozione selettiva. Le concentrazioni
maggiori:

| Directory | LOC in file con tutte le funzioni morte | Funzioni | Nota |
|---|---:|---:|---|
| `internal/capabilities/overlays` | 2.042 | ~46 | `visual_budget`, `preset_image_selection`, `scheduler`, `binding`, `plan`: molte funzioni morte, ma i file hanno parti vive |
| `internal/kernel/script` | 1.270 | 43 | visual beat planner, editorial graph/timeline/identity, narrative clip view: fondazione "P0/P1" non cablata |
| `internal/kernel/asset/detail` | 1.186 | 16 | clip semantic metadata, metadata registry, search terms, visual summary, image enums: tipi condivisi |
| `internal/capabilities/voiceover/service` | 755 | 14 | vecchio batch/parent aggregation; parte dei tipi è viva |
| `internal/capabilities/entities` | 1.094 | 38 | resolver/indexer in-memory; le interfacce sono usate, le implementazioni no |
| `internal/capabilities/remote` | 603 | 4 | `CompleteJobRequest.Validated`/`ValidateArtifacts`: tipi vivi, metodi morti |
| `internal/capabilities/overlays/render` | 541 | 16 | già incluso nel taglio verificato; sono oracoli di test |
| `internal/capabilities/scripts` (top-level) | 385 | 8 | `service.go`, `timing_certification.go`, `result_reducer.go` |
| `internal/kernel/job/workspace` | 429 | 6 | `manager.go`: solo test |
| `internal/capabilities/youtube/usecase` | 413 | 3 | `PublishRenditionsToYouTubeAsset`: solo test |
| `internal/capabilities/indexing` | 377 | 2 | `VisualSummaryService`: solo test |
| `internal/capabilities/assets/ingest` | 608 | 14 | già incluso nel taglio verificato |
| `internal/capabilities/scripts/usecase` | 1.097 | 24 | già incluso nel taglio verificato |

Priorità consigliata per la long tail: prima i file con **zero riferimenti nei
test** (nessun costo di manutenzione dei test), poi quelli usati solo da test
oracolo.

## 5. Test da sistemare

I 28 test con lo stesso nome dei file rimossi (6.372 righe) vanno eliminati.
Dopo la loro rimozione, `go vet ./...` segnala ancora riferimenti ai simboli
cancellati nei seguenti package di test:

- `internal/capabilities/overlays/render` (`scene_intelligence_test.go`:
  `ComparisonCandidates`)
- `internal/capabilities/render` (`strategy_test.go`: `hash64`, dal
  `fingerprint.go` rimosso)
- `internal/capabilities/scripts/usecase`
  (`pacquiao_broner_pre_planner_schema_contract_test.go`:
  `NewDeterministicPlanner`; `research_fanout_test.go`: `research.EvidencePack`)
- `internal/capabilities/scripts/adapters`
  (`scene_execution_test.go`, `tractors_e2e_test.go`,
  `vidrush_disabled_provider_test.go`: `VidRushSourceResolver`;
  `vidrush_t5_e2e_test.go`: `NewVidRushSegmentResearchAdapter`,
  `PlanVisualWindows`)
- `internal/capabilities/voiceover/service`
  (`process_segment_remote_orphan_test.go`: `qdrantE2EDB`, dal file
  `e2e_fixtures.go` rimosso)
- `internal/capabilities/scripts/adapters/processor`
  (`fixed_media_guard_test.go`: `NewImageProcessor`)
- `internal/platform/qdrant/indexing/mediamemory`
  (`qdrant_frame_indexer_test.go`: `collectionFromPath`)
- `internal/platform/qdrant/indexing/clipindexer`
  (`wire_envelope_back_compat_test.go`: `ParseWireEnvelope`)

**Baseline già rossa prima del taglio** (non causata da questo audit):

- `internal/capabilities/assets/providers/stock/stockpipeline`
  (`step_sub_operations_test.go`: `fmt` importato e non usato)
- `internal/platform/media/rustexec`
  (`scene_intelligence_test.go`: `DecodeSIManifest` non definito)
- `internal/capabilities/scripts/usecase/gencore`
  (`generation_postprocess_test.go`: `ArtlistSearchMs`)
- `cmd/admin/internal/audit`
  (`repair_drive_links_report_test.go`: campo `RefreshDocs`)

Non ho eseguito `go test` (richiederebbe DB/Postgres/Qdrant e servizi live).
La verifica è `go build ./...` verde + `go vet` per la compilazione dei test.

## 6. Cosa NON tagliare

- **`pkg/veloxclient`** — 1.010 righe, 39/41 funzioni irraggiungibili dalla
  produzione, ma è il client usato da `cmd/velox` (CLI ops) e da
  `tests/operational/worker-integration`. Non è codice di produzione, ma non è
  morto: verificare eventuali consumatori esterni prima di toccarlo.
- **`cmd/archcheck`, `cmd/architecture-aggregate`,
  `cmd/capability-inventory-aggregate`, `cmd/goldensummary`, `cmd/mediacert`,
  `cmd/model-registry-gen`, `cmd/overlay-model-alias-gen`, `cmd/regen-current-yaml`**
  — tool di CI/governance, fuori dal closure di produzione. Non sono candidati.
- **`internal/platform/sqlite/rendermetrics`** — usato da `cmd/goldensummary`
  (tool, non produzione).
- **`internal/capabilities/overlays/render`** — è nel taglio verificato, ma i
  suoi test sono oracoli di parità (plan fingerprint, dead air, continuità). Se
  il gate di parità è ancora considerato utile, va tenuto: il taglio è
  "production-safe", non "coverage-safe".
- **`internal/platform/sqlite/testdb.go`,
  `internal/platform/media/processor/test_helpers.go`** — infrastruttura di
  test, non codice di produzione.

## 7. Piano consigliato

1. **Wave 1 (subito, ~16k righe totali):** rimuovere i 48 file di §2 e i 28
   test associati; sistemare i riferimenti elencati in §5; eseguire
   `go build ./...`, poi `make verify-agent`, e una sola volta
   `make verify-main` prima del push.
2. **Wave 2 (~3.800 righe):** micro-refactor di §3 (stub dei tipi, estrazione
   dei 3 helper YouTube, rimozione del campo `ProviderAssets`), poi rimozione.
3. **Wave 3 (~989 funzioni):** long tail di §4, partendo dai file con zero
   riferimenti nei test. Non è un taglio di file interi: serve una revisione
   per package.

Il taglio di Wave 1 è già stato verificato con `go build ./...` verde in una
copia hardlink. Non è stato applicato al working tree.

## Appendix A — I 48 file della Wave 1

```text
internal/capabilities/audit/audit_collection.go
internal/capabilities/audit/audit_payload.go
internal/capabilities/audit/audit_reconciler.go
internal/capabilities/audit/legacyaudit.go
internal/capabilities/assets/ingest/adapter_image.go
internal/capabilities/assets/ingest/clip_ingest_pipeline.go
internal/capabilities/assets/providerassets/catalog_builder.go
internal/capabilities/maintenance/audit.go
internal/capabilities/maintenance/delete-invalid.go
internal/capabilities/maintenance/output.go
internal/capabilities/maintenance/reaper.go
internal/capabilities/maintenance/repair-locators.go
internal/capabilities/maintenance/scanner.go
internal/capabilities/maintenance/service.go
internal/capabilities/mediamemory/media_materialize_worker.go
internal/capabilities/mediamemory/scene_visual_plan_dto.go
internal/capabilities/mediamemory/scene_visual_plan_generator.go
internal/capabilities/mediamemory/scene_visual_plan_layout.go
internal/capabilities/overlays/render/dead_air.go
internal/capabilities/overlays/render/plan_fingerprint.go
internal/capabilities/overlays/render/scene_intelligence_candidates.go
internal/capabilities/overlays/render/visual_continuity.go
internal/capabilities/render/fingerprint.go
internal/capabilities/research/evidence_pack.go
internal/capabilities/research/ranking.go
internal/capabilities/scripts/adapters/processor/asset_materializer.go
internal/capabilities/scripts/adapters/processor/clip_resolver.go
internal/capabilities/scripts/adapters/processor/entity_media_links.go
internal/capabilities/scripts/adapters/processor/materialization_pipeline.go
internal/capabilities/scripts/adapters/processor/processor_images.go
internal/capabilities/scripts/adapters/processor/processor_images_fanout.go
internal/capabilities/scripts/adapters/processor/segment_understanding_model.go
internal/capabilities/scripts/adapters/processor/semantic_asset_search.go
internal/capabilities/scripts/adapters/vidrush_segment_research_adapter.go
internal/capabilities/scripts/adapters/vidrush_source_resolver.go
internal/capabilities/scripts/adapters/vidrush_visual_window_planner.go
internal/capabilities/scripts/usecase/deterministic_planner.go
internal/capabilities/scripts/usecase/post_voiceover_composer.go
internal/capabilities/scripts/usecase/sceneplan_builder.go
internal/capabilities/scripts/usecase/semaphore_usecase.go
internal/capabilities/voiceover/service/e2e_fixtures.go
internal/capabilities/voiceover/service/usecase.go
internal/platform/qdrant/indexing/clipindexer/wire_envelope.go
internal/platform/qdrant/indexing/mediamemory/qdrant_indexer.go
internal/platform/qdrant/indexing/mediamemory/semantic.go
internal/platform/sqlite/assets/channels/asset_license_repository.go
internal/platform/sqlite/assets/channels/asset_release_repository.go
internal/platform/sqlite/assets/channels/asset_rendition_repository.go
```

## Appendix B — comandi di verifica

```bash
# raggiungibilità di produzione
go list -deps ./cmd/server ./cmd/worker ./cmd/admin > /tmp/prod-deps.txt
deadcode ./cmd/server ./cmd/worker ./cmd/admin > /tmp/deadcode.txt

# build della copia con i 48 file rimossi
cp -al refactored .tmp/deadcode-verify
# rm dei file di Appendix A + dei 28 test associati
cd .tmp/deadcode-verify && go build ./...
```
