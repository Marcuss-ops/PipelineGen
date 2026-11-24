# CLEANUP-02 — Helper test-only dentro file di produzione

**Data:** 2026-11-24
**Priorità:** P1 (igiene, rischio basso)
**Stato:** DONE
**Owner:** `capabilities/scripts`, `capabilities/cliprender`, `stockpipeline`, `cmd/admin`
**Origine:** radiografia 2026-11-24, §2B
**Base:** `refactored` @ `2a3ea0b2c` (working tree pulito)

## 1. Problema

Dodici helper non esportati vivono in file di produzione ma sono raggiunti solo
da test dello stesso package. Questo ha due effetti:

- la superficie di produzione contiene codice che non serve a nessun job;
- `deadcode -test=false` li classifica come irraggiungibili, ma non si possono
  cancellare senza perdere l'oracolo del test.

La classe di riferimento è `internal/capabilities/scripts`: il package è già a
87 file di produzione (`find … -maxdepth 1`), quindi ogni file in più ha un
costo di governance.

## 2. Soluzione

Spostare ogni helper nel `_test.go` che lo usa (o in un helper di test
condiviso), lasciando invariata la logica. Il codice di produzione smette di
compilare questi simboli; i test continuano a usarli.

| Helper | File di produzione | Destinazione |
| --- | --- | --- |
| `setDefaultLocalizedIdentityMatcherForTests` | `scripts/translated_nlp_localization.go` | `scripts/lexicon_testmain_test.go` |
| `storeDynamicMapVideoCache` | `scripts/renderqueue/render_queue_map_cache.go` | `scripts/renderqueue/dynamic_map_video_cache_test.go` |
| `semanticBulletSpans` | `scripts/overlay_plan.go` | `scripts/scene_semantic_candidates_test.go` |
| `randomClipStartSFXIntents` | `scripts/ports_execution.go` | `scripts/random_clip_start_sfx_test.go` |
| `contextualImageLaneRequested` | `scripts/vidrush/vidrush_fanout_plan.go` | `scripts/vidrush/vidrush_fanout_plan_branch_test.go` |
| `validateVidRushImageHead` | `scripts/vidrush/vidrush_image_validation.go` | `scripts/vidrush/vidrush_contract_test.go` |
| `sourcePathHasCanonicalPersonName` | `scripts/vidrush/entity_image_filter.go` | `scripts/vidrush/entity_image_catalog_integration_test.go` |
| `capSemanticVerificationCandidates` | `scripts/vidrush/annotation_type.go` | `scripts/vidrush/image_semantic_gate_test.go` |
| `encodeContinuationPayload` | `cliprender/async_worker.go` | nuovo `cliprender/payload_test_helpers_test.go` |
| `buildStockManifest` | `stockpipeline/aliases.go` | `stockpipeline/lease_extractor_test.go` (chiamare `core.BuildStockManifest`) |
| `buildPublishPlan` | `stockpipeline/publish/step_publish.go` | `stockpipeline/publish/publish_plan_test.go` |
| `writeRuntimeRouteManifest` | `cmd/admin/internal/rendering/gen_api_docs_manifest.go` | `cmd/admin/internal/rendering/gen_api_docs_manifest_test.go` |

### Casi che richiedono attenzione

- `capSemanticVerificationCandidates` ha un gemello **vivo** in
  `scripts/adapters/scene_annotations.go:301`, usato in produzione. La copia
  `vidrush` è solo l'oracolo del test: si sposta nel test, non si tocca
  l'adapter.
- `buildStockManifest` è un alias di `core.BuildStockManifest`; i due call-site
  del test possono chiamare direttamente il core, così non resta nemmeno una
  copia test-only dell'alias.
- `encodeContinuationPayload` è usato da tre file di test: serve un helper
  condiviso in un nuovo `_test.go`, non tre copie.

## 3. Check finali

```bash
cd refactored
# I simboli non devono più comparire in file di produzione.
rg -n 'func (setDefaultLocalizedIdentityMatcherForTests|storeDynamicMapVideoCache|semanticBulletSpans|randomClipStartSFXIntents|contextualImageLaneRequested|validateVidRushImageHead|sourcePathHasCanonicalPersonName|capSemanticVerificationCandidates|encodeContinuationPayload|buildStockManifest|buildPublishPlan|writeRuntimeRouteManifest)' \
  --glob '!**/*_test.go'
# atteso: nessun risultato
go build ./...
go test ./internal/capabilities/scripts/... ./internal/capabilities/cliprender/... \
  ./internal/capabilities/assets/providers/stock/stockpipeline/... \
  ./cmd/admin/internal/rendering/... -count=1
gofmt -l <file toccati>
```

## 4. Esito (2026-11-24)

- 12 helper spostati. `rg` sui file di produzione non trova più le definizioni;
  resta solo il gemello vivo in `scripts/adapters/scene_annotations.go`.
- Copie test-only: `scripts/cleanup_test_helpers_test.go`,
  `scripts/renderqueue/cleanup_test_helpers_test.go`,
  `cliprender/payload_test_helpers_test.go`,
  `scripts/vidrush/cleanup_test_helpers_test.go`,
  `publish/publish_plan_helpers_test.go`,
  `cmd/admin/internal/rendering/write_runtime_route_manifest_test.go`.
- PASS: `renderqueue`, `vidrush`, `cliprender`, `stockpipeline/...`, `jobs`,
  `clips`, `velox`, `sqlite`, `instaeditpublishing`, `youtube/jobs`,
  `mediamemory`, `brain/normalizer`, `pkg/*`.
- Riverifica finale: il move concorrente è chiuso e il test binary root passa
  (`go test ./internal/capabilities/scripts -count=1`, 20,4 s). Passano anche
  `renderqueue`, `vidrush`, `cliprender`, `stockpipeline` e
  `cmd/admin/internal/rendering`.
- `rg` sui file di produzione non trova i 12 helper; resta solo il gemello vivo
  `capSemanticVerificationCandidates` in `scripts/adapters/scene_annotations.go`.
- `go vet` sulla superficie stabile → rc 0.

