# Piano di ritiro del codice morto — 2026-11-18

**Stato:** ondata 0 verificata e rinviata (i due file non sono privi di
referenze); **ondata 1 ESEGUITA il 2026-11-18** (137 delle 148 funzioni);
**ondata 2 ESEGUITA il 2026-11-18** (29 delle 34 funzioni, 5 deferite);
**ondata 5 D-ledger ESEGUITA il 2026-11-18** (30 funzioni, decisione: ledger =
feature spenta) e **DualPool ritirato** (7 funzioni, con allowlist di
`testdb.go`); **ondata 8 parzialmente eseguita il 2026-11-18** (26 funzioni,
`capabilities/overlays` a **0** unreachable).
**Stato finale 2026-11-18:** il ticket dead-code è **CHIUSO** (476 totali, 18 in
allowlist classificata: 15 fixture/test-support + 3 superficie dormiente) e la
demolizione overlays è **DONE** (0 unreachable, goldens Rust 34/34, drift gate
Go verde).
**Ricognizione 2026-11-19:** il 476 non era riproducibile sul commit di
chiusura. Misure su worktree puliti: **481** a `5bedaada6`/`b1c7e06cf`,
**483** a `8d55a74a4`, sempre con 0 zero-reference e 0 in `overlays`; il delta
+2 è in `internal/kernel/digest` (`HasPanelGridCollageBytes`,
`PerceptualHashBytes`) e appartiene alla catena person-image/VLM non cablata.
Guard corrente: **≤ 229** misurato sul working tree del 2026-11-21 dopo le
ondate di sweep (`pkg/`, `internal/app/wiring`, `capabilities/remote`,
`mediaregistry`, media-eligibility, contract-corpus, test-support, overlay,
`scene_annotations`, `stockpipeline`, `render_cache`) e le modifiche successive
già presenti nel tree: 445 → 295 → 292 → 277 → 229, di cui **20 in allowlist
test-only** (`tests/tools/*`, `sqlite/sqltest`) e quindi **209 nel perimetro di
produzione**. Il calo 295 → 292 è il ricontrollo a tree stabile del 2026-11-19
(hash dei contenuti invariato per 180 s): ritirati
`AnimationRegistry.MapTypeDefault`, `DefaultPhraseExtractionPolicy` e
`isSHA256`; capabilities (59), scripts (108) e voiceover (8) chiuse per
disposition, platform (61) e kernel/cmd (36+4) restano come registrato.
**Sweep 2026-11-21:** il working tree misurava **300** funzioni unreachable.
Prima ondata: ritirati otto simboli senza riferimento fuori dal file di
definizione — `NewMultiplexedExecutorWithOutputLimit`, `MaterializeMapCandidate`,
`materializedMapHasEntity`, `mapBasemapStyles`, `groundedSpanInAnyScene`,
`SetFilteringConfig`, `Model.HasVectorSpace`, `Model.Validate` — e la misura
successiva è **292**, il guard dell'epoca. Seconda ondata: ritirati il
file `scripts/result_reducer.go` con `VidRushResultReducer`,
`NewVidRushResultReducer`, `Reduce`, `isStaleSegment`, `matchSceneSegment` (e il
relativo test; `SegmentSceneMerger` fu spostato in `scene_commit.go` e poi
ritirato nella terza ondata), il
file `embeddings/contract_probe.go` con `ContractProbe`, `NewContractProbe`,
`Fetch`, il file `translation/argos_translator.go` con `ArgosTranslator`,
`ArgosTranslatorConfig`, `NewArgosTranslator`, `Translate` (il sentinel
`ErrArgosBridgeUnavailable` resta nel server translator), `prompts.Load` con
`loadConfigData`/`loadPromptFile` e il relativo manifest, `ptrutil.BoolDefault`
e `embedding.Verify` (mantenendo `ComponentQuery`, usato dall'embedder). La
misura dopo la seconda ondata è **277** (i due helper test-only
`BatchItem.fail` e `randomSuffix` sono stati spostati in `types_helpers_test.go`).
Terza ondata 2026-11-21: ritirati overlay category/group-limit (27),
`scene_annotations` merger/helper (8), `stockpipeline` naming/publish (7),
`cliprender/render_cache.go` (3) e due zero-reference in
`images/search_queries_fanout.go`; misura finale **229** e guard **≤ 229**
(`make verify-deadcode`).
**Audit dei 229 residui (2026-11-21):** nessuna funzione ha zero riferimenti.
Tutte compaiono in test, contratti/interfacce o catene di feature non cablate,
quindi non esiste un'ulteriore eliminazione a rischio zero: abbassare ancora il
numero richiederebbe una decisione di prodotto su una feature o la rimozione di
test che la certificano. Non è stato fatto in questo sweep. L'inventario test
sotto è storico e include anche i file ritirati il 2026-11-21. Le schede di famiglia sotto restano la
fotografia storica delle 445 misurate a inizio giornata; il conteggio puntuale è
questo. Storia delle ultime ondate: 445 dopo l'ondata 13 del 2026-11-19 (il file
`kernel/script/retrieval_query_builders.go` con 10 funzioni e 3 test, mai
cablato; l'ondata 12 aveva tolto il normalizer audio legacy con 5 DTO e 5 test,
la 11 il tipo `ClipSemanticMetadata`, la 10 i builder `ClipIdentity` e il
validator `RenderPlan`).
**Ripartizione 2026-11-19:** le 445 residue sono distribuite in dodici schede di
famiglia in questa cartella (`TICKET-MEDIAREGISTRY-DEAD-SURFACE-2026-11-19`,
`TICKET-MEDIA-ELIGIBILITY-WIRE-OR-RETIRE-2026-11-19`,
`TICKET-VOICEOVER-HARNESS-AND-SERVICE-SWEEP-2026-11-19`,
`TICKET-SCRIPTS-DEAD-CHAIN-SWEEP-2026-11-19`,
`TICKET-KERNEL-ASSET-DEAD-SURFACE-2026-11-19`,
`TICKET-APP-WIRING-TEST-ONLY-HELPERS-2026-11-19`,
`TICKET-REMOTE-UPLOAD-IDEMPOTENCY-SURFACE-2026-11-19`,
`TICKET-PKG-DEAD-HELPER-SWEEP-2026-11-19`,
`TICKET-PLATFORM-DEAD-HELPER-SWEEP-2026-11-19`,
`TICKET-CAPABILITIES-RESIDUAL-SWEEP-2026-11-19`,
`TICKET-KERNEL-CMD-RESIDUAL-SWEEP-2026-11-19`,
`TICKET-TEST-SUPPORT-OUT-OF-PRODUCTION-2026-11-19`) più
`TICKET-CONTRACT-CORPUS-TEST-HERMETICITY-2026-11-19`, che è un difetto di test e
non conta funzioni. La somma delle tabelle di scope è 445; la lista congelata è
`deadcode-2026-11-19.txt`.
La matrice Rust `overlay_plan_contract` è **30/30**
(misurata il 2026-11-19; il "34" storico è precedente al consolidamento dei
test), drift gate Go verde sul commit pulito. Il codec jobs è stato ritirato e il guard tipizzato sostituito dal
check C3 sul registry (`registry_codec_wiring_test.go`).
**Baseline misurata:** 745 funzioni unreachable in 303 file (`deadcode -test=false ./...`), 0 zero-reference.
**Baseline di questa esecuzione:** 738 totali, 175 in `capabilities/overlays`
(il tree conteneva già la parte iniziale dell'ondata 2).
**Snapshot dopo il caso font:** 598 totali, 35 in `capabilities/overlays`;
`style.go` non ha più funzioni unreachable. Il calo ulteriore viene dall'ondata
1/2 eseguita in parallelo.
**Contesto:** residuo dopo la demolizione del planner Go
(`TICKET-OVERLAY-PLANNER-GO-DEMOLITION-2026-11-18.md`). Il ticket dead-code
(`TICKET-DEAD-CODE-SWEEP-2026-11-18.md`) registra il conteggio, questo documento
decide *cosa* ritirare e *in che ordine*.

## Metodo

Il report è stato analizzato per funzione, non per directory:

- **489 radici**: funzioni che nessun altro file di produzione chiama. Sono i
  punti di spegnimento: si cancellano per prime e portano con sé il codice a
  valle.
- **256 funzioni a valle**: chiamate solo da altre funzioni già morte. Spariscono
  da sole quando cade la radice, ma `deadcode` le segnala solo dopo la rimozione,
  quindi il conteggio va rifatto a ogni ondata.
- **Test-only**: `deadcode -test=false` considera morta anche una funzione
  chiamata solo dai test. Non è un errore: se la feature è ritirata, vanno
  ritirati anche i test. Per ogni cluster l'appendice B elenca i test che chiamano
  i simboli morti, distinti tra stessa suite e fuori package.
- **File ≠ funzione**: molti file contengono DTO, costanti o tipi vivi accanto a
  funzioni morte. Non si cancella un file senza `go build`; i precedenti
  (`SegmentSceneMerger`, `maxGenericSceneImagesPerScene`, `Pinned`) sono la prova.

Le liste complete (file → simboli, test → cluster) sono nelle appendici A e B in
fondo. Le sezioni per ondata qui sotto contengono solo le decisioni e i rischi.

## Gate unico per ogni ondata

Nessuna ondata è "fatta" finché non passano tutti i punti:

1. `go build ./...` → 0.
2. `go vet` sui package toccati → 0.
3. `go test` sui package toccati con `-count=1` → verde, senza indebolire
   asserzioni. Se un test copre anche codice vivo, si taglia il caso, non il file.
4. `go test ./...` prima del commit. Il rosso che questa sezione citava
   (`TestApplyImageAssetStylesRejectsUnhostablePin`) è risolto il 2026-11-18:
   era un test fermo al contratto precedente al r134 (lo still assente è media
   opzionale, un pin senza target viene saltato; i pin oltre le immagini
   pianificate sono troncati). Il test è riscritto in
   `TestApplyImageAssetStylesSkipsPinForSceneWithoutImage` +
   `TestApplyImageAssetStylesTruncatesPinsBeyondPlannedStills`, non con uno
   skip: `go test ./internal/capabilities/scripts -count=1` è verde e
   `go build`/`go vet` su quel package sono 0. Nessun rosso noto resta da
   dichiarare.
5. `"$(go env GOPATH)/bin/deadcode" -test=false ./...` → il totale deve scendere
   almeno del numero di funzioni dell'ondata; un calo maggiore è cascata
   legitima.
6. `python3 ops/tools/reconcile_tickets.py --check` → 0.

Nessun commit tra un'ondata e l'altra: un'ondata = una modifica reviewabile.

## Ordine delle ondate

| # | Cluster | Funzioni | File | Test stessa suite | Test fuori package | Perché qui |
|---|---|---:|---:|---:|---:|---|
| 0 | Pilot standalone | 8 | 2 | 0 | 0 | Nessuna dipendenza, verifica il metodo |
| 1 | A1 budget/charts + A2 preset/motion | 148 | 20 | 0 | 4 | Zero test locali: il guadagno più grande e pulito |
| 2 | A3 contratti overlay | 34 | 14 | 2 | 3 | Sblocca style/font SSOT |
| 3 | G retrieval + I1 audio/render/remote | 48 | 17 | 17 | 5 | Feature legacy isolate |
| 4 | F kernel asset | 40 | 14 | 14 | 3 | Helper di dominio, test dedicati |
| 5 | C jobs codec + D sqlite | 83 | 26 | 27 | 10 | Rischio dati/persistenza: decisione esplicita |
| 6 | E voiceover + B app wiring | 61 | 29 | 32 | 0 | Capability spente, non solo codice |
| 7 | I0 resto + I2 media/entity + I3 piattaforma | 185 | 123 | 133 | 34 | Frammentato: si procede per package |
| 8 | H catena overlay `scripts` | 138 | 60 | 104 | 11 | Ultima: è ciò che tiene in vita gli helper overlays |

I conteggi di test sono per cluster e non deduplicati tra cluster; l'appendice B
è la lista di lavoro vera.

**Primo slice consigliato:** ondata 0 + ondata 1. Sequenza operativa:

1. Cancellare i due file del pilot, `go build ./...`, rilanciare `deadcode`.
2. In A1 rimuovere per prime le radici `ApplyEditorialOverlayBudget*` (17) e i
   loro caller morti; build dopo ogni file.
3. Chart (4): rimuovere `ChartPlacementsForScenes`, `BuildChartItems`,
   `PruneOrphanChartBars`; tagliare i casi Go da `chart_wiring_test.go` e
   `bullets_charts_cert_test.go`, lasciando le asserzioni Rust-owned.
4. In A2 procedere un file alla volta: `preset_image_selection.go` (26),
   `preset_motion_selection.go` (19), `preset_motion_pools.go` (12),
   `preset_selection.go` (11), `planner_motion_pools.go` (8), poi i quattro
   helper motion (21). Build e test dopo ogni file.
5. Rilanciare `deadcode`: atteso 745 → ~590 (156 rimossi, più eventuale
   cascata). Aggiornare il ticket dead-code prima di passare all'ondata 2.

**Eseguito 2026-11-18.** L'ondata 0 non è stata eseguita: entrambi i file sono
referenziati (vedi sotto). L'ondata 1 ha rimosso 137 delle 148 funzioni; le 11
deferite sono elencate nella sezione dell'ondata. Sul tree condiviso `deadcode`
è passato da 738 a 598 totali e da 175 a 35 in `capabilities/overlays`; il
delta restante rispetto a −137 appartiene all'ondata 2 concorrente e alla
cascata.

---

## Ondata 0 — pilot (8 funzioni, 2 file)

**File:** `internal/platform/mutationprobe/mutationprobe.go` (7),
`pkg/immutability/copy.go` (1).

**Verifica 2026-11-18: NON eseguibile come scritto.** I due file non sono privi
di referenze:

- `internal/platform/mutationprobe/mutationprobe.go` è importato da
  `internal/capabilities/cliprender/chunk_mutation_probe_test.go`, che usa
  `Run` e `Probe` come harness opt-in dei gate sugli invarianti di chunk. Il
  ticket orphan-packages lo elenca già tra i package test-support da
  allowlistare, non da cancellare.
- `pkg/immutability/copy.go::CloneWith` è chiamato da
  `internal/capabilities/voiceover/service/types_helpers.go::normalizeBatchRequest`.
  Quella catena è a sua volta morta (ondata 6), ma compila: senza rimuoverla
  insieme, `go build` si rompe.

Il pilot va quindi eseguito con l'ondata 6 (voiceover) e con la decisione
sull'allowlist test-only; non è un gate di taratura indipendente.

**Decisione richiesta:** allowlist per `mutationprobe` e rinvio di
`CloneWith` all'ondata 6.

---

## Ondata 1 — A1 + A2 (148 funzioni, 20 file, 0 test nella stessa suite)

**A1 budget & charts (44):** `visual_budget.go` (17),
`visual_budget_admission.go` (10), `chart_plan.go` (4),
`visual_budget_entity_cards.go` (4), `recap_candidates.go` (3),
`planner_date_image.go` (2), `visual_budget_phrase.go` (2),
`chapter_bullet.go` (1), `recap_plan.go` (1).

**A2 preset & motion (104):** `preset_image_selection.go` (26),
`preset_motion_selection.go` (19), `preset_motion_pools.go` (12),
`preset_selection.go` (11), `planner_motion_pools.go` (8),
`long_phrase_lane.go` (7), `phrase_motion_distinct.go` (7),
`animation_layout.go` (6), `motion_coherence.go` (6),
`animation_directive_validate.go` (1), `planner_motion_assignment.go` (1).

Nessun test dentro `overlays` chiama questi simboli: la suite del planner è già
stata ritirata. Restano **quattro test fuori package** da tagliare, non da
cancellare alla cieca:

| Test | Cosa usa | Azione |
|---|---|---|
| `scripts/chart_wiring_test.go` | `ChartPlacementsForScenes` | Tenere l'asserzione sull'autorizzazione Rust, togliere il calcolo placement Go |
| `scripts/bullets_charts_cert_test.go` | `ChartPlacementsForScenes` | Tenere il filtro categorie; il placement è coperto da `chart_acceptance_36.rs` |
| `scripts/entity_image_motion_rotation_test.go` | cataloghi `Certified*Motions` | Se il catalogo è Rust-owned, sostituire con il contract test Rust; altrimenti tenere la funzione e toglierla dal cluster |
| `scripts/runner_overlay_plan_test.go` | `ImagePresetCandidates`, `DatePresentationMotionCandidates` | Le assertion sui preset scelti dal planner Rust non hanno più un oracle Go: riscrivere sul payload o eliminare il caso |

**Rischio:** i file `preset_*` e `visual_budget_*` contengono tipi e costanti usati
dal payload (`MotionCandidate`, limiti, `SceneInput`). Rimuovere solo le funzioni
elencate in appendice A, poi build; non `rm` dei file.

**Decisione richiesta:** confermare che selezione preset, motion assignment,
budget visivo e placement chart sono Rust-owned. Se una parte è ancora Go-owned,
quel file esce dal cluster e resta nel conteggio.

**Riduzione attesa:** −148, più eventuale cascata su A3/H.

**Eseguito 2026-11-18 — 137/148.** Rimosse tutte le 44 funzioni di A1 e 93 di
A2. Deferite 11 funzioni ancora referenziate, con la motivazione richiesta dal
gate «se una parte è ancora Go-owned, esce dal cluster»:

- `SelectTextMotion` → `capabilities/scripts/overlay_entity_asset_registry.go`
  (catena H, ondata 8);
- `CertifiedLightLeakMotions`, `CertifiedAutomaticEntityCaptionMotions`,
  `RuntimeEntityCaptionMotionAtOffset`, `EntityImageMotionAtOffset`,
  `EntityImageParams` → `capabilities/scripts/entity_image_motions.go`,
  produzione morta ma coperta da cinque test vivi;
- `CertifiedEntityCaptionMotions`, `CertifiedImageWithTextMotions` → drift gate
  di `entity_image_motion_rotation_test.go`, senza oracle Rust equivalente
  verificato;
- `automaticEntityCaptionMotionCandidates`, `normalizeGeneratedCaptionMotion`,
  `rotateMotionAtOffset` → dipendenze delle funzioni deferite.

Test tagliati, non indeboliti: `chart_wiring_test.go` e
`bullets_charts_cert_test.go` perdono i casi di derivazione placement (ora
Rust-owned, `chart_acceptance_36.rs`) e tengono filtro categorie, prefiltro
`charts: disabled`, catena bullet atomica e parsing stretto;
`runner_overlay_plan_test.go` sostituisce l'appartenenza ai pool Go con
`NotEmpty` sul preset/motion scelto da Rust. File interamente morti rimossi:
`animation_layout.go`, `animation_directive_validate.go`,
`planner_date_image.go`, `planner_motion_assignment.go`. `doc.go` è stato
conservato.

---

## Ondata 2 — A3 contratti overlay (34 funzioni, 14 file)

File principali: `style.go` (10), `priority.go` (4), `model/intent.go` (4),
`documentary_phrase_aliases.go` (2), `editorial_sections.go` (2),
`entity_style_sampling.go` (2), `long_copy_motion_guard.go` (2),
`planner_candidates.go` (2), `map_backend.go`, `planner_input.go`,
`planner_map.go`, `selection_seed.go`, `model/overlay_fingerprint.go`,
`render/composite_motion_pools.go` (1 ciascuno).

**Font SSOT — risolto il 2026-11-18.** `font_ssot_parity_test.go` non chiama più
il merge Go: `TestCanonicalFontAssetsParity` confronta i due cataloghi Go e
`TestRustFontVocabularyParity` legge i `font_family`/`caption_font_family` dei
fixture Rust e le costanti `*_FONT`/`*_FAMILY` del planner, fallendo se una
famiglia emessa da Rust non è nel contratto payload o non ha asset. Rust possiede
solo la forma dell'ID (`request.rs`), quindi il test non duplica un catalogo:
ancora la vocabulary Rust ai due cataloghi Go. Rimosse da `style.go` le funzioni
ttenute in vita dal test: `MergeCaptionStyleIntoLayers`, `ImageStyleParams`,
`IsRuntimeCursorParam`, `IsRuntimeImageParam`, `StyleFontColor`,
`IsEntityOverlayKind`. `MergeRuntimeTextStyleParams` è stata **ripristinata**:
`overlay_plan_finalize.go` la usa ora per i chain segment, quindi è viva e non
cancellabile. `style.go` resta con 16 funzioni, tutte vive o referenziate: non è
un file da cancellare, e `deadcode` non segnala più nulla al suo interno.

**Eseguito 2026-11-18 — 29/34.** Questa passata ha rimosso le altre 23 funzioni
del cluster (le sei di `style.go` erano già state ritirate con il caso font
sopra): `priority.go` ×4, `EntityOverlayPlanner.Plan`, `NewEntityOverlayPlanner`,
`SortIntents`, `TemplateResolver.Resolve`, `documentary_phrase_aliases.go` ×2,
`editorial_sections.go` ×2, `entity_style_sampling.go` ×2,
`long_copy_motion_guard.go` ×2, `planner_candidates.go` ×2, `map_backend.go`,
`planner_input.go` (`DefaultMapGroupConfig`, non il file), `planner_map.go`,
`selection_seed.go`, `model/overlay_fingerprint.go`.

`model/intent_test.go` è stato tagliato, non cancellato: restano i test di
`PlanOverlayIntents` e `OverlayIntent`, spariscono i quattro casi che coprivano
solo i simboli rimossi. `planner_input.go` conserva il campo vivo `Pinned`.

**Deferite 5.** `IsRuntimeTextStyleParam`, `IsTextOverlayKind`,
`MergeRuntimeTextStyleParams`, `MergeStyleParam` sono raggiungibili da
`capabilities/scripts/overlay_canvas.go` e `overlay_plan_finalize.go` (catena H,
ondata 8). `CompositeImageMotionPool` è usato da
`scripts/entity_image_motions.go` e da tre test vivi: `deadcode` lo segnala
ancora, insieme alle 11 funzioni deferite dell'ondata 1.

**Riduzione misurata:** 598 → 575 totali, `capabilities/overlays` 35 → 12,
fuori overlays invariato a 563.

---

## Ondata 3 — G retrieval + I1 audio/render/remote (48 funzioni, 17 file)

**G (16):** `retrieval_query_builders.go` (10), `clip_preplan.go` (2),
`builtin_audio_aliases.go`, `generation_errors.go`, `resolved_plan.go`,
`source_spec.go` (1 ciascuno). `BuildYouTubeQueries*` ha un test dedicato e tre
contract test che lo nominano: se la costruzione query è Rust-owned, si ritira il
builder e si riscrivono i contract test sul payload.

**I1 (32):** `audio/timeline_normalizer.go` (7, normalizzazione v1),
`render/validator.go` (7, `ValidateRenderPlan`), `remote/artifact_uploader.go`
(5), `cliprender/render_cache.go` (3), più helper minori. Attenzione ai tipi vivi
`TimelineV2`, `RenderPlan`, `UploadSession`: si eliminano le funzioni, non i
contratti.

**Decisione richiesta:** la normalizzazione timeline v1 serve ancora per job
storici? Se sì, resta e va esclusa dal conteggio.

---

## Ondata 4 — F kernel asset (40 funzioni, 14 file)

Famiglie: durata (`SomeDuration`, `NoDuration`, `ResolveAssetDuration`,
`MediaWindow.*EndUS`), identità clip (`NewYouTubeClipIdentity`,
`NewStockClipIdentity`, `ParseYouTubeClipAssetID`, `BuildIndexEventKey`),
metadati semantici (`AsSearchTextInput`, `ComputeDurationSec`, `MergedTags`),
stati (`rights_state`, `lifecycle_state`, `bcp47`), registry provider.

I test sono dedicati e per famiglia (`duration_test.go`,
`clip_identity_test.go`, `clip_semantic_metadata_test.go`, …): si ritirano con la
funzione. Il rischio è che siano usati come fixture dai test SQLite: il gate
`go test ./...` lo scopre subito.

**Decisione richiesta:** quali di queste API sono parte del contratto pubblico del
kernel e vanno tenute anche senza caller interno? In assenza di consumatori esterni
dichiarati, si ritirano.

---

## Ondata 5 — C jobs codec + D sqlite (83 funzioni, 26 file)

**C (29):** `jobs/codec.go` (13), `preparation_metrics.go` (3),
`preparation_work_estimator.go` (2), `scheduling/preparation_registry.go` (2),
`scheduling/speculation_scheduler.go` (2), `worker/runner.go` (2), più 5 funzioni
singole. **Rischio alto:** `registry_codec_completeness_test.go` verifica la
compatibilità wire di payload e result. Cancellare il codec può rendere
illeggibili job persistiti: serve una nota di migrazione o la conferma che la coda
si può svuotare.

**D (54):** `mediaregistry/ledger.go` (18), `sqlite/pool.go` (7, `DualPool`),
`mediaregistry/tx.go` (7), `sqlite/testdb.go` (5),
`assets/imagesregistry/media_asset_mutations.go` (5),
`twinddl/generate.go` (3), più 9 funzioni singole.

Eccezione metodologica: `testdb.go` è infrastruttura di test usata da test vivi.
Non è "codice morto di prodotto": va **escluso dal conteggio con una allowlist**,
non cancellato. Lo stesso vale per `scripts/usecase/testsupport/doubles.go` (H) e
per il harness operazionale (E).

**Decisione richiesta:** il ledger di projection e il `DualPool` sono feature
spente o fondamenta da riattivare? Se spente, si ritirano con i test; se
fondamenta, si escludono dalla baseline.

**Eseguito 2026-11-18 (D-ledger + DualPool + codec).** Il codec C (13
funzioni) è stato ritirato: nessun costruttore di produzione, quindi nessun
payload persistito ne dipendeva; il guard di completezza è ora il check C3 su
`CanonicalJobDefinitions`/registry. **Eseguiti anche i 15 helper jobs dormienti**
(bundle, delivery, preparation*, scheduling, opzioni runner, `maxRetriesFor`,
`ClaimUntil`): erano duplicati di API vive, quindi i test sono stati riportati
sulle varianti vive e i due seam di osservazione (`Running`, `ActiveWorkFunc`)
spostati nei test. Resta in allowlist `worker.Runner.SetRenewInterval`, usato
dall'e2e black-box di `workerruntime`. Restano le singole funzioni D (3 in
allowlist dormiente).

**Eseguito 2026-11-18 (solo D-ledger).** Decisione: il ledger di projection è
una **feature spenta**, non una fondamenta — `NewLedger` aveva solo due
chiamanti di test e la produzione usa `NewCanonicalIdentityResolver`,
`content_objects` e `source_identity`. Ritirati `ledger.go` (18), i sette helper
transazionali (`tx.go`) e i cinque wrapper `imagesregistry` che esistevano solo
per l'adapter: **30 funzioni**, con `projection_advance_test.go` e i casi
`LinkContent`/`SourcesForAsset` dei test. Restano vivi `execer`,
`registerSource` e `validateAssetSource` (usati da `canonical_identity.go`) e la
loro copertura in `source_write_test.go`. **DualPool: ritirato il surface tx (7 funzioni).** `BeginWriterTx`,
`BeginReaderTx`, `beginTx`, `SourcePath`, `instrumentedTx.Commit`/`Rollback` e
`classifyTxError` erano raggiungibili solo da `pool_test.go`; rimossi con i tre
test dedicati. Restano vivi `NewDualPool` (WAL, sizing, fail-closed) e
`walPragmas`, che è il factory canonico: il pool non è stato cancellato, solo
le entry point tx instrumentate che nessun percorso di produzione usava.
**`testdb.go` (5) va in allowlist, non cancellato:** è fixture condivisa da 12
call site nei test vivi (come `scripts/usecase/testsupport/doubles.go` e il
harness `tests/operational/voiceover_*`). Restano nel conteggio solo le singole
funzioni D (backup, controlplane, executionsteps, outboxevents, performance,
statematrix, twinddl, youtube): 17 funzioni, da valutare una per una. C (codec
job) resta bloccato dalla nota di migrazione.

---

## Ondata 6 — E voiceover + B app wiring (61 funzioni, 29 file)

**E (45):** 23 helper del service (`types_helpers.go` 5,
`process_segment_fingerprint.go` 3, `process.go` 2, `types.go` 2,
`upload_intent.go` 2, più 9 singole), 3 funzioni nel wiring voiceover e 19 del
harness operazionale (`tests/operational/voiceover_harness.go`, `probes.go`,
`report.go`, `voiceover_test_helpers.go`). Il harness è test-only per costruzione:
**va escluso**, non cancellato, finché la suite smoke esiste. Gli helper del
service (fingerprint v3, dedupe, filename, language, metadata) hanno test dedicati
e possono essere ritirati se le feature sono spente.

**B (16):** wiring di capability disabilitate (`gpuGateSlots`,
`wireSourcingAtomic`, `resolveHits`, `attachFinalJobDriveSources`,
`newMiddlewareFeatureFlagsAdapter`, …). Qui la domanda non è "il codice è morto?"
ma "la capability è ritirata o solo spenta?". Se è solo spenta, il wiring resta e
va escluso dalla baseline; se è ritirata, si cancellano wiring e test insieme.

**Decisione richiesta:** elenco delle capability ufficialmente ritirate. Senza
questo elenco l'ondata non parte.

---

## Ondata 7 — I0 resto + I2 + I3 (185 funzioni)

**I0 resto (10):** `pkg/defaults` (3: `DefaultYouTubeConfig`,
`DefaultVoiceoverConfig`, `DefaultMediaConfig` — usati dai test, valutare se sono
API viva), `platform/checksum` (3), `kernel/entitytype` (4: `CorrectSurfaceType`,
`AliasSpellings`).

**I2 (45):** `stockpipeline/naming.go` (4) e `publish` (3),
`mediaregistry/contract.go` (4), `images/entitycatalog` (6),
`entities/type_aliases.go` (3), `mediamemory` (3), `animreg` (3), più file minori.
Famiglie coese: si procede package per package.

**I3 (130, 92 file):** il cluster più disperso. Priorità ai file con più funzioni:
`observability/registry.go` (4), `hmacsign` (3), `retrieval_query_builders` già in
ondata 3, `platform/sqlite/*` residui, `httpserver`, `mutationprobe` già in
ondata 0. `cmd/archcheck/gate/gate.go` ha una sola funzione morta ma **21 test
del package la nominano**: è il caso opposto, un gate mai collegato al comando
(`make archcheck-strict` invoca `cmd/archcheck`, non `gate`). O si cabla `gate`
nel comando, rendendolo vivo, o si ritirano funzione e test insieme. Non è codice
morto "per caso": è un gate dimenticato.

**Decisione richiesta:** per I3 si decide per package, non in blocco. Il piano
propone di partire dai primi 20 file per conteggio e rimandare la coda.

---

## Ondata 8 — H catena overlay `scripts` (138 funzioni, 60 file)

È l'ultima per costruzione: questa catena è l'unico caller di molti helper
overlays, quindi finché resta, `deadcode` continuerà a vedere catene "vive" a
monte. Sottocluster:

| Sottocluster | File | Funzioni |
|---|---|---:|
| Canvas e limiti gruppo | `overlay_canvas.go` (16), `overlay_entity_cards_text.go` (7), `overlay_entity_cards.go` (4), `overlay_category_filter.go` (5) | 32 |
| Entity asset registry | `overlay_entity_asset_registry.go` | 13 |
| Scene annotations | `adapters/scene_annotations.go` | 8 |
| Result reducer | `result_reducer.go` | 4 |
| Audio timeline / overlay plan / payload | `audio_timeline.go` (3), `overlay_plan.go` (3), `final_job_payload.go` (2) | 8 |
| VidRush fanout / discovery | `adapters/vidrush_fanout_types.go` (3), `ports/video_source_discovery.go` (3) | 6 |
| Test support | `usecase/testsupport/doubles.go` | 7 (escludere) |
| Resto | 47 file | 60 |

Dipendenze da preservare, verificate in questa sessione:

- `overlay_plan_compile_limits.go` contiene la costante viva
  `maxGenericSceneImagesPerScene`: non cancellare il file.
- `adapters/scene_annotations_numeric.go` contiene
  `appendGroundedNumberEntities`, viva.
- `ports/research.go` ha tipi usati da `capabilities/research`; il file resta.
- `ports/video_source_discovery.go` definisce `VideoSourceCandidate` e
  `SemanticResearchResult`, usati dai test del package: si rimuovono le funzioni,
  non i tipi.
- `result_reducer.go` definisce `SegmentSceneMerger`, usato da
  `internal/app/wiring`: non è cancellabile senza toccare il wiring.
- `overlay_category_filter.go` è usato dai test riscritti nella demolizione del
  planner.

La rimozione richiede la parità Rust su: limiti di gruppo, raggruppamento date,
dedupe card entità, annotazioni scena, riduzione result. Senza quella parità
l'ondata resta bloccata, ed è corretto che lo sia.

**Decisione richiesta:** ritirare la compilazione overlay Go o mantenerla come
fallback? Se resta fallback, H non si tocca e va esclusa dalla baseline con
motivazione.

**Eseguito 2026-11-18 (sottocluster entity-asset/motion).** Decisione:
**ritiro**, la compilazione overlay resta Rust-owned. Ritirati il file
`overlay_entity_asset_registry.go` (12, nessun caller di produzione),
`assignEntityImageMotions` e `capNamedEntityCardsPerScene` (chiamati solo dai
test), i nove accessor morti di `preset_image_selection.go`, `SelectTextMotion`
e `render/composite_motion_pools.go`: **26 funzioni**, e `capabilities/overlays`
scende a **0** unreachable. Il resto di H (canvas, limiti gruppo, annotazioni,
result reducer) resta: richiede ancora la parità Rust elencata sopra, e i suoi
caller di test vivi non sono stati indeboliti.

---

## Baseline e allowlist

`deadcode -test=false` continuerà a segnalare infrastruttura test-only come morta.
Per non inseguire un numero che non può scendere, il piano propone:

1. Un file `ops/deadcode-baseline.txt` con: conteggio atteso, data, comando.
2. Una allowlist esplicita e motivata per: `sqlite/testdb.go`,
   `scripts/usecase/testsupport/doubles.go`, `tests/operational/voiceover_*`.
3. Un target `make deadcode-check` che fallisce se il totale *sale* sopra
   baseline; il calo si aggiorna a mano a ogni ondata.
4. Il ticket dead-code resta la fonte dei numeri; questo piano la fonte delle
   decisioni.

**Misura 2026-11-21 (sessione successiva):** `deadcode -test=false ./...` = **229**,
`deadcode -test ./...` = **84**. Il guard scende per ratchet a **≤ 229**; la riduzione include
la catena morta dei prompt config (`prompts.Get` senza writer).

### Allowlist classificata — oracoli di test (2026-11-21)

Due catene restano unreachable in produzione ma NON vanno ritirate: sono gli
oracoli che certificano i contratti dei rispettivi package. Restano nel
conteggio `deadcode -test=false` e sono qui classificate con il loro owner,
così il budget 279 è stabile e ogni nuovo ingresso richiede una decisione
esplicita.

| Simbolo | Package | Perché resta | Owner |
| --- | --- | --- | --- |
| `memoryRenderCache.Get` / `.Put` | `internal/capabilities/cliprender` | doppio in-memory di `RenderCache`, costruito da `newMemoryRenderCache()` in `render_cache_memory_test.go` e usato da 4 test (hit, miss, invalidazione, record). Ritirarlo cancellerebbe la sola copertura dell'oracolo di cache. | capabilities/cliprender |
| `NewUploadIntentUseCase` | `internal/capabilities/voiceover/service` | costruttore fail-fast del caso d'uso voiceover async, coperto da `upload_intent_test.go` (happy path + 3 fallimenti di dipendenza). Il wiring di produzione non lo costruisce ancora: è una catena RA-06 §1.3, non codice morto. | capabilities/voiceover |

Regola: un simbolo usato solo dai test resta se il test certifica un contratto
di produzione; si ritira solo quando nessun test lo richiede. Questa tabella è
l'elenco chiuso: aggiungere una riga è una decisione, non una scorciatoia.

### Sweep U1000 — 2026-11-21 (seconda sessione): cosa è stato rimosso davvero

`deadcode -test=false ./...` resta **229** dopo lo sweep: il tool conta solo funzioni
irraggiungibili dal `main`, e **tutte le 229 sono raggiunte da almeno un test o da un
contratto** (censimento: 0 funzioni con zero riferimenti nel repo). Cancellarle significa
cancellare i test che le certificano, non rimuovere debito.

Il codice morto che `deadcode` non vede è stato cercato con
`staticcheck -checks U1000 ./internal/... ./pkg/... ./cmd/...` (test inclusi): **49 simboli**,
di cui **27 rimossi** e **22 residui classificati**.

Rimossi (27 simboli, 5 file):

| Tipo | Simboli |
| --- | --- |
| const (17) | `chartBarMotionID`, `maxImageFrameShadowBlurPX`, `maxNamedEntityCardsPerScene`, `longFamilyDocumentary/3D/ModernApple/ClassicApple/Count`, `laneImages/ImagesWithText/Entities/ImportantPhrase/ShortPhrase/Maps/TimelineDate/MetricStat`, `canonicalMotionCatalogFixture` |
| var (8) | `editorialSectionCatalog`, `longCopyCrashedMotionIDs`, `promptInstructionEchoes`, `shortPhraseRecipeWordGroups`, `capitalizedNameRE` (adapter), `validRightsStatusSet`, `filteringOverride`, `animationLaneKeys` |
| func (2) | `loadCanonicalMotionCatalog`, `containsString` |
| file (5) | `overlays/long_phrase_lane.go`, `overlays/short_phrase_word_groups.go`, `overlays/long_copy_motion_guard.go`, `overlays/motion_catalog_fixture_test.go`, `overlays/render/test_helpers_test.go` |

Residui classificati (22): 21 **campi** di struct ancora referenziate come gusci API
(`providerassets.Registry`, `mediamemory.StrategyRegistry`, `localization.Scheduler`,
`render.SmartResolver`, `indexing.VisualSummaryService`, `overlays/model.EntityOverlayPlanner`):
rimuovere i campi senza ritirare il tipo e i call site è cosmesi, e ritirare i tipi è una
decisione di contratto. 1 **var**: `allowedMetadataKeys`, che non è morto — è letto come testo
dal percheck `percheck_metadata_key_registry`; la cancellazione è stata provata e riporta
`total_violations: 2`, `has_hard_gate_hits: true`, quindi è stato ripristinato da HEAD.

Verifiche: `go build ./...`, `go vet` sui package toccati, `gofmt -l` vuoto, `go test` su
`overlays/...`, `kernel/asset/...`, `scripts/adapters`, `scripts`; `archcheck --strict` exit 0.
Guard dead code invariato: **≤ 229**.

## Decisioni da prendere prima di iniziare

1. Preset, motion assignment, budget e chart: tutto Rust-owned? (ondata 1)
2. Font merge: API Go per la parità o parità solo Rust? (ondata 2)
3. Timeline v1 e codec job: servono per dati storici? (ondate 3 e 5)
4. Ledger projection e `DualPool`: feature spenta o fondamenta? (ondata 5)
   — **risposta: entrambi spenti come surface di scrittura; ledger ritirato per
   intero, `DualPool` ritirato nel surface tx e tenuto come factory vivo
   (2026-11-18).**
5. Capability disabilitate nel wiring: ritirate o solo spente? (ondata 6)
6. Catena overlay Go: ritiro o fallback? (ondata 8)
   — **risposta: ritiro; sottocluster entity-asset/motion rimosso 2026-11-18.**
7. Allowlist test-only approvata? (trasversale)

Le ondate 0, 1 e 2 non dipendono da tutte le risposte: la 0 parte subito, la 1
dopo la conferma al punto 1, la 2 dopo il punto 2.

---

## Appendici

L'appendice A elenca, per ogni cluster, tutti i file con i simboli esatti da
rimuovere. L'appendice B elenca i test che toccano quei simboli, divisi tra stessa
suite e fuori package. Entrambe sono generate dal report `deadcode` del
2026-11-18 e vanno rigenerate dopo ogni ondata.

## Appendice A — simboli per file
Elenco completo generato dal report `deadcode`. `Tipo.Metodo` indica un metodo; gli altri sono funzioni libere.

### A1 budget & charts — 44 funzioni, 9 file
| File | # | Simboli morti |
|---|---:|---|
| `internal/capabilities/overlays/visual_budget.go` | 17 | `ApplyEditorialOverlayBudget`, `ApplyEditorialOverlayBudgetWithBulletLane`, `ApplyEditorialOverlayBudgetWithImageLimit`, `ApplyEditorialOverlayBudgetWithLimit`, `ApplyEditorialOverlayBudgetWithLimits`, `ApplyEditorialOverlayBudgetWithPhraseLanes`, `LimitOverlayItemsPerScene`, `applyEditorialOverlayBudget`, `chartBarIndices`, `chartLabelIndices`, `editorialLabelIndices`, `editorialLabelIndicesExcludingBullets`, `mapGroupInt`, `preBudgetedDateChainIndices`, `rankedUniqueKindIndices`, `rankedUniqueMapIndices`, `retainOverlayItems` |
| `internal/capabilities/overlays/visual_budget_admission.go` | 10 | `dedupeImageIndicesByContent`, `imageOverlayIdentity`, `overlayItemIsBullet`, `overlayWindowsOverlap`, `rankedUniqueBulletIndices`, `rankedUniqueOverlayIndices`, `rankedUniqueOverlayPhraseLanes`, `rankedUniqueOverlayPhraseLanesExcludingBullets`, `removeBulletIndices`, `selectNonOverlappingPhraseLane` |
| `internal/capabilities/overlays/chart_plan.go` | 4 | `BuildChartItems`, `ChartPlacementsForScenes`, `PruneOrphanChartBars`, `chartWindowCrowded` |
| `internal/capabilities/overlays/visual_budget_entity_cards.go` | 4 | `isTextOnlyNamedEntityCard`, `namedEntityCardIdentity`, `namedEntityCardKind`, `rankedNamedEntityCardIndices` |
| `internal/capabilities/overlays/recap_candidates.go` | 3 | `BuildRecapCandidatesThrough`, `RecapPlacementPoints`, `normalizeRecapTitle` |
| `internal/capabilities/overlays/planner_date_image.go` | 2 | `MoveImagesAsideForOverlappingDateCards`, `overlayItemDimension` |
| `internal/capabilities/overlays/visual_budget_phrase.go` | 2 | `ApplyPhraseOverlayBudget`, `ApplyPhraseOverlayBudgetWithLimit` |
| `internal/capabilities/overlays/chapter_bullet.go` | 1 | `ChapterBulletMotionCandidates` |
| `internal/capabilities/overlays/recap_plan.go` | 1 | `BuildRecapPlanItems` |

### A2 presets & motion — 104 funzioni, 11 file
| File | # | Simboli morti |
|---|---:|---|
| `internal/capabilities/overlays/preset_image_selection.go` | 26 | `CertifiedAutomaticEntityCaptionMotions`, `CertifiedEntityCaptionMotions`, `CertifiedImageMotions`, `CertifiedImageRecipeMotions`, `CertifiedImageWithTextMotions`, `CertifiedLightLeakMotions`, `EntityCaptionMotionAtOffset`, `EntityCaptionMotionPool`, `EntityImageMotionAtOffset`, `EntityImageParams`, `ImageMotionAtOffset`, `ImageOverlayParams`, `ImageWithTextMotionAtOffset`, `ImageWithTextMotionPool`, `RandomImageMotionOffset`, `RuntimeEntityCaptionMotionAtOffset`, `SelectEntityCaptionMotionAt`, `SelectEntityImageAnimation`, `SelectEntityImageMotionAt`, `SelectImageAnimation`, `SelectImageMotionAt`, `SelectLightLeakMotion`, `automaticEntityCaptionMotionCandidates`, `normalizeGeneratedCaptionMotion`, `rotateMotionAtOffset`, `selectImageMotion` |
| `internal/capabilities/overlays/preset_motion_selection.go` | 19 | `ShortPhraseVisibleMotionCandidates`, `animationCount`, `defaultPhraseMotionSequence`, `heavyPhraseFamilyWindow`, `laneAnimationCount`, `limitedMotionPool`, `limitedShuffledMotionPool`, `selectHeavyPhraseMotion`, `selectHeavyPhraseMotionLimited`, `selectMotionFromPool`, `selectPhraseMotion`, `selectPhraseMotionLimited`, `selectPhrasePreset`, `selectShortPhraseMotion`, `selectShortPhraseMotionLimited`, `shortPhraseInterleavedOrder`, `shortPhraseRendererCompatible`, `shuffleMotionPool`, `visiblePhraseEntrancePool` |
| `internal/capabilities/overlays/preset_motion_pools.go` | 12 | `CertifiedHeavyPhraseMotions`, `IsShortPhraseRecipe`, `LongPhraseMotionCandidates`, `RenderSafeTextMotions`, `SelectTextMotion`, `ShortPhraseMotionCandidates`, `ShortPhraseMotionWordCount`, `ShortPhraseMotionsForWordCount`, `certifiedPhraseFamily`, `containsMotion`, `flattenShortPhraseMotionCandidates`, `phraseMotionFamilyCandidates` |
| `internal/capabilities/overlays/preset_selection.go` | 11 | `DateMotionFamilyCandidates`, `DatePresentationMotionCandidates`, `ImagePresetCandidates`, `MetricPresentationMotionCandidates`, `NumberPresentationForEntityType`, `NumberPresentationForEntityTypeWithDateMotions`, `NumberPresentationForEntityTypeWithLimit`, `NumberPresentationTemplateForEntityType`, `SelectEntityNamePreset`, `dateMotionsInFamily`, `selectPresentationMotion` |
| `internal/capabilities/overlays/planner_motion_pools.go` | 8 | `PhraseExitFrames`, `imageWithTextPoolFor`, `itemPriority`, `normalizePinnedPhraseMotion`, `phraseMotionParams`, `validateImageWithTextMotionPool`, `validatePhraseMotionPool`, `validateShortPhraseRecipePin` |
| `internal/capabilities/overlays/long_phrase_lane.go` | 7 | `longPhraseFamilyBucketOf`, `longPhraseFamilyWindow`, `longPhraseInterleavedOrder`, `selectLongPhraseMotion`, `selectLongPhraseMotionFromFamilies`, `selectLongPhraseMotionLimited`, `text3DFreeLongPhraseMotions` |
| `internal/capabilities/overlays/phrase_motion_distinct.go` | 7 | `EnsureDistinctNumberMotionsPinned`, `EnsureDistinctPhraseMotionsPinned`, `IsRemovedGeneratedPhraseMotion`, `excludeRemovedGeneratedPhraseMotions`, `intersectMotionPool`, `phraseMotionCandidatesForItem`, `rotateMotionPastUsed` |
| `internal/capabilities/overlays/animation_layout.go` | 6 | `ApplyAnimationConsistency`, `ApplyAnimationConsistencyPinned`, `FinalizePlannerAnimationLayout`, `PrepareCompiledAnimationLayout`, `ResolveAnimationSelections`, `pinnedPhraseMotionIndexes` |
| `internal/capabilities/overlays/motion_coherence.go` | 6 | `CoherenceDominantDirection`, `WithCoherenceSeed`, `coherenceKey`, `coherentMotionPool`, `motionHorizontalDirection`, `oppositeDirection` |
| `internal/capabilities/overlays/animation_directive_validate.go` | 1 | `ValidateAnimationDirective` |
| `internal/capabilities/overlays/planner_motion_assignment.go` | 1 | `assignPlannerItemMotions` |

### A3 overlay contracts — 34 funzioni, 14 file
| File | # | Simboli morti |
|---|---:|---|
| `internal/capabilities/overlays/style.go` | 10 | `ImageStyleParams`, `IsEntityOverlayKind`, `IsRuntimeCursorParam`, `IsRuntimeImageParam`, `IsRuntimeTextStyleParam`, `IsTextOverlayKind`, `MergeCaptionStyleIntoLayers`, `MergeRuntimeTextStyleParams`, `MergeStyleParam`, `StyleFontColor` |
| `internal/capabilities/overlays/priority.go` | 4 | `ContentPriority`, `DegradeOverlaps`, `contentPriorityForTemplate`, `maxConcurrentWithin` |
| `internal/capabilities/overlays/model/intent.go` | 4 | `EntityOverlayPlanner.Plan`, `NewEntityOverlayPlanner`, `SortIntents`, `TemplateResolver.Resolve` |
| `internal/capabilities/overlays/documentary_phrase_aliases.go` | 2 | `ResolveDocumentaryPhraseMotion`, `resolveDocumentaryPhraseMotions` |
| `internal/capabilities/overlays/editorial_sections.go` | 2 | `EditorialSections`, `OrganizeEditorialPlan` |
| `internal/capabilities/overlays/entity_style_sampling.go` | 2 | `StampEntityStyle`, `stampEntityStyle` |
| `internal/capabilities/overlays/long_copy_motion_guard.go` | 2 | `excludeLongCopyCrashingMotions`, `isLongCopyCrashingMotion` |
| `internal/capabilities/overlays/planner_candidates.go` | 2 | `clampImageWindow`, `dedupeCandidateIDs` |
| `internal/capabilities/overlays/map_backend.go` | 1 | `ResolveMapBackend` |
| `internal/capabilities/overlays/planner_input.go` | 1 | `DefaultMapGroupConfig` |
| `internal/capabilities/overlays/planner_map.go` | 1 | `deterministicMapMotionStart` |
| `internal/capabilities/overlays/selection_seed.go` | 1 | `SelectImageGroupMotion` |
| `internal/capabilities/overlays/model/overlay_fingerprint.go` | 1 | `RenderKey` |
| `internal/capabilities/overlays/render/composite_motion_pools.go` | 1 | `CompositeImageMotionPool` |

### B app wiring — 16 funzioni, 10 file
| File | # | Simboli morti |
|---|---:|---|
| `internal/app/wiring/wire_services_orchestration.go` | 4 | `WireMinimal`, `minimalLifecycle.AddProbe`, `minimalLifecycle.Start`, `minimalLifecycle.Stop` |
| `internal/app/wiring/adapters_middleware.go` | 2 | `newMiddlewareFeatureFlagsAdapter`, `newMiddlewareRateLimitAdapter` |
| `internal/app/wiring/rendering/runtime.go` | 2 | `gpuGateSlots`, `resolveGPUGateSlots` |
| `internal/app/wiring/script/remote_final_job.go` | 2 | `RemoteFinalJobAdapter.attachFinalJobDriveSources`, `finalJobAdapterString` |
| `internal/app/wiring/assets_register_sourcing.go` | 1 | `wireSourcingAtomic` |
| `internal/app/wiring/module_media_ingest.go` | 1 | `isAIImageIngestSource` |
| `internal/app/wiring/script_generation_runtime.go` | 1 | `NewSceneTextGenerator` |
| `internal/app/wiring/wire_assets.go` | 1 | `NewAssetDestResolver` |
| `internal/app/wiring/lifecycle/runtime.go` | 1 | `SafeCall` |
| `internal/app/wiring/script/remote_final_job_resolve.go` | 1 | `RemoteFinalJobAdapter.resolveHits` |

### C jobs codec — 29 funzioni, 11 file
| File | # | Simboli morti |
|---|---:|---|
| `internal/capabilities/jobs/codec.go` | 13 | `NewTypedCodec`, `NewTypedCodecAdapter`, `TypedCodec.DecodePayload`, `TypedCodec.DecodeResult`, `TypedCodec.EncodePayload`, `TypedCodec.EncodeResult`, `TypedCodec.JobType`, `TypedCodecAdapter.DecodePayload`, `TypedCodecAdapter.DecodeResult`, `TypedCodecAdapter.EncodePayload`, `TypedCodecAdapter.EncodeResult`, `TypedCodecAdapter.JobType`, `TypedCodecAdapter.SchemaVersion` |
| `internal/capabilities/jobs/preparation_metrics.go` | 3 | `NewPreparationMetrics`, `PreparationMetrics.RecordClaimRatio`, `PreparationMetrics.RecordClaimSnapshot` |
| `internal/capabilities/jobs/preparation_work_estimator.go` | 2 | `PreparationWorkEstimator.Bootstrap`, `PreparationWorkEstimator.Expect` |
| `internal/capabilities/jobs/scheduling/preparation_registry.go` | 2 | `JobPreparationRegistry.AllTypes`, `JobPreparationRegistry.IsFrozen` |
| `internal/capabilities/jobs/scheduling/speculation_scheduler.go` | 2 | `ActiveWorkFunc.ActiveWorkAvailable`, `ResourceScheduler.Running` |
| `internal/capabilities/jobs/worker/runner.go` | 2 | `Runner.SetRenewInterval`, `Runner.WithClaimSnapshotter` |
| `internal/capabilities/jobs/bundle.go` | 1 | `NewBundle` |
| `internal/capabilities/jobs/delivery.go` | 1 | `NewDeliveryHandler` |
| `internal/capabilities/jobs/preparation_fingerprint.go` | 1 | `NewPreparationUnit` |
| `internal/capabilities/jobs/worker_execution.go` | 1 | `Worker.maxRetriesFor` |
| `internal/capabilities/jobs/queue/claim.go` | 1 | `ClaimUntil` |

### D sqlite & media registry — 54 funzioni, 15 file
| File | # | Simboli morti |
|---|---:|---|
| `internal/platform/sqlite/mediaregistry/ledger.go` | 18 | `Ledger.AdvanceActiveProjectionSequence`, `Ledger.AppendEvent`, `Ledger.ContentForAsset`, `Ledger.FinishRun`, `Ledger.LatestEventSequence`, `Ledger.LatestVectorEventSequence`, `Ledger.LinkContent`, `Ledger.ListProjections`, `Ledger.ReadCounts`, `Ledger.RegisterBackup`, `Ledger.RegisterProjection`, `Ledger.RegisterSource`, `Ledger.SourcesForAsset`, `Ledger.StartRun`, `Ledger.UpsertTaxonomy`, `NewLedger`, `defaultJSON`, `nonEmpty` |
| `internal/platform/sqlite/pool.go` | 7 | `DualPool.BeginReaderTx`, `DualPool.BeginWriterTx`, `DualPool.SourcePath`, `DualPool.beginTx`, `classifyTxError`, `instrumentedTx.Commit`, `instrumentedTx.Rollback` |
| `internal/platform/sqlite/mediaregistry/tx.go` | 7 | `Ledger.AppendEventTx`, `Ledger.LinkContentTx`, `Ledger.RegisterSourceTx`, `Ledger.UpsertTaxonomyTx`, `appendEvent`, `linkContent`, `upsertTaxonomy` |
| `internal/platform/sqlite/testdb.go` | 5 | `MustExec`, `NewMigratedTestDB`, `NewTestDB`, `NewTestDBWithSchema`, `testMigrationsDir` |
| `internal/platform/sqlite/assets/imagesregistry/media_asset_mutations.go` | 5 | `LinkMediaAssetContentDB`, `LinkMediaAssetContentTx`, `UpdateMediaAssetTaxonomy`, `UpdateMediaAssetTaxonomyDB`, `UpdateMediaAssetTaxonomyTx` |
| `internal/platform/sqlite/twinddl/generate.go` | 3 | `NormalizeCheck`, `NormalizeDefault`, `NormalizeExpr` |
| `internal/platform/sqlite/backup.go` | 1 | `DatabaseSet.BackupSet` |
| `internal/platform/sqlite/migrations_verify.go` | 1 | `removedTablesIn` |
| `internal/platform/sqlite/assets/youtubediscoveries/youtube_discoveries_helpers.go` | 1 | `ResolveDateAfter` |
| `internal/platform/sqlite/controlplane/verifier.go` | 1 | `New` |
| `internal/platform/sqlite/executionsteps/store.go` | 1 | `NewSQLiteStoreWithDB` |
| `internal/platform/sqlite/outboxevents/pool.go` | 1 | `Pool.WithClock` |
| `internal/platform/sqlite/outboxevents/supersede.go` | 1 | `NewSupersede` |
| `internal/platform/sqlite/performance/projection.go` | 1 | `NewProjection` |
| `internal/platform/sqlite/statematrix/statematrix.go` | 1 | `CasingFamily` |

### E voiceover & harness — 45 funzioni, 19 file
| File | # | Simboli morti |
|---|---:|---|
| `tests/operational/voiceover_harness.go` | 6 | `NewVoiceoverHarness`, `VoiceoverHarness.Curl`, `VoiceoverHarness.RotateToken`, `defaultStr`, `getenvDefault`, `readTokenFromFile` |
| `tests/operational/voiceover_probes.go` | 6 | `VoiceoverHarness.ProbeJobs`, `VoiceoverHarness.ProbeMediaAssets`, `VoiceoverHarness.ProbeOutboxEvents`, `VoiceoverHarness.ProbeVoiceovers`, `VoiceoverHarness.runSQLiteQuery`, `sqlEscape` |
| `tests/operational/voiceover_report.go` | 6 | `VoiceoverHarness.Assert`, `VoiceoverHarness.AssertHTTPStatus`, `VoiceoverHarness.Assertf`, `VoiceoverHarness.RecordJobID`, `VoiceoverHarness.WriteReport`, `VoiceoverHarness.recordDBProbe` |
| `internal/capabilities/voiceover/service/types_helpers.go` | 5 | `BatchItem.fail`, `normalizeBatchRequest`, `normalizeBatchRequestWithDefaults`, `randomSuffix`, `voiceOverridesFromMetadata` |
| `internal/app/wiring/voiceover/build_bundles_voiceover_rate_limits.go` | 3 | `NewRateLimitedPublisher`, `NewRateLimitedPublisherWithGate`, `rateLimitedPublisher.Publish` |
| `internal/capabilities/voiceover/service/process_segment_fingerprint.go` | 3 | `BuildVoiceoverContentFingerprint`, `BuildVoiceoverContentFingerprintV3`, `DefaultVoiceoverPostPolicy` |
| `internal/capabilities/voiceover/service/process.go` | 2 | `resolveVoiceForLanguage`, `voiceOverrideFor` |
| `internal/capabilities/voiceover/service/types.go` | 2 | `BatchItem.UnmarshalJSON`, `BatchRequest.PayloadMap` |
| `internal/capabilities/voiceover/service/upload_intent.go` | 2 | `NewUploadIntentUseCase`, `UploadIntentUseCase.Execute` |
| `internal/capabilities/voiceover/service/dedupe.go` | 1 | `DedupeDecision.String` |
| `internal/capabilities/voiceover/service/filename.go` | 1 | `SanitizeFilename` |
| `internal/capabilities/voiceover/service/finalizer.go` | 1 | `NewVoiceoverFinalizer` |
| `internal/capabilities/voiceover/service/language.go` | 1 | `MustParseLanguage` |
| `internal/capabilities/voiceover/service/metadata.go` | 1 | `Service.resolveDestination` |
| `internal/capabilities/voiceover/service/parent_state.go` | 1 | `FanoutResultSnapshotAccessor.Snapshot` |
| `internal/capabilities/voiceover/service/pipeline_error.go` | 1 | `NewPipelineError` |
| `internal/capabilities/voiceover/service/service.go` | 1 | `NewService` |
| `internal/capabilities/voiceover/service/stylegroup.go` | 1 | `ParseStyleGroup` |
| `tests/operational/voiceover_test_helpers.go` | 1 | `requireExplicitSmokeDB` |

### F kernel asset — 40 funzioni, 14 file
| File | # | Simboli morti |
|---|---:|---|
| `internal/kernel/asset/duration.go` | 8 | `MediaWindow.SourceEndUS`, `MediaWindow.TimelineEndUS`, `MediaWindow.Validate`, `NoDuration`, `OptionalDuration.Validate`, `OptionalDuration.Value`, `ResolveAssetDuration`, `SomeDuration` |
| `internal/kernel/asset/detail/clip_identity.go` | 5 | `BuildIndexEventKey`, `NewClipIdentity`, `NewStockClipIdentity`, `NewYouTubeClipIdentity`, `ParseYouTubeClipAssetID` |
| `internal/kernel/asset/detail/clip_semantic_metadata.go` | 5 | `ClipSemanticMetadata.AsSearchTextInput`, `ClipSemanticMetadata.Clone`, `ClipSemanticMetadata.ComputeDurationSec`, `ClipSemanticMetadata.IsEmpty`, `ClipSemanticMetadata.MergedTags` |
| `internal/kernel/asset/detail/store_helpers.go` | 5 | `Service.Count`, `Service.Delete`, `Service.Get`, `Service.List`, `Service.Save` |
| `internal/kernel/asset/rights_state.go` | 4 | `RestrictedRightsStatuses`, `RightsStatus.IsPublishable`, `RightsStatus.String`, `RightsStatus.Valid` |
| `internal/kernel/asset/bcp47.go` | 2 | `IsLanguageUndeterminable`, `IsSupported` |
| `internal/kernel/asset/provider_registry.go` | 2 | `ProviderRegistry.All`, `ProviderRegistry.ByID` |
| `internal/kernel/asset/detail/scoring.go` | 2 | `Calculate`, `TokenScore` |
| `internal/kernel/asset/detail/source_catalog.go` | 2 | `SourceCatalog.Definitions`, `SourceCatalog.Names` |
| `internal/kernel/asset/evidence.go` | 1 | `CanonicalEvidenceTextSourceValues` |
| `internal/kernel/asset/language_registry.go` | 1 | `NewLanguageRegistryFromCodes` |
| `internal/kernel/asset/lifecycle_state.go` | 1 | `CanonicalLifecycleStateValues` |
| `internal/kernel/asset/detail/ai_stock.go` | 1 | `AIStockFolderPath` |
| `internal/kernel/asset/detail/search_text.go` | 1 | `ComposerRegistry.Register` |

### G kernel retrieval — 16 funzioni, 6 file
| File | # | Simboli morti |
|---|---:|---|
| `internal/kernel/script/retrieval_query_builders.go` | 10 | `BuildYouTubeQueries`, `BuildYouTubeQueriesWithExplicit`, `isRetrievalEntity`, `joinQuery`, `normalizedQueryLimit`, `temporalTerms`, `termQuery`, `termValues`, `uniqueQueries`, `weightedValues` |
| `internal/kernel/script/clip_preplan.go` | 2 | `ClipPrePlan.Validate`, `ResolvedClipSlot.Validate` |
| `internal/kernel/script/builtin_audio_aliases.go` | 1 | `BuiltInAudioCanonicalAliases` |
| `internal/kernel/script/generation_errors.go` | 1 | `IsClipNativePlanningFailed` |
| `internal/kernel/script/resolved_plan.go` | 1 | `NewResolvedGenerationPlan` |
| `internal/kernel/script/source_spec.go` | 1 | `NarrativeClipView.Validate` |

### H scripts overlay chain — 138 funzioni, 60 file
| File | # | Simboli morti |
|---|---:|---|
| `internal/capabilities/scripts/overlay_canvas.go` | 16 | `applyOverlayGroupLimits`, `applyPhraseLaneCaps`, `dropCategoryDisabledCandidates`, `effectiveMapOverlayCeiling`, `effectivePhraseOverlayCeiling`, `hasSegmentOverlayCategories`, `isRuntimeTextStyleParam`, `isTextOverlayKind`, `mergeStyleParam`, `normalizeEntityImageLayer`, `overlayGroupLimit`, `overlayGroupOf`, `overlayImageLayersHaveCaptions`, `overlayStyleParams`, `stripOverlayImageCaptions`, `validateOverlayGroupLimits` |
| `internal/capabilities/scripts/overlay_entity_asset_registry.go` | 13 | `appendUniqueAssetRef`, `applyVerifiedEntityAssets`, `bindVerifiedChainAssets`, `chainEntitySegment`, `chainSegmentName`, `chainSegments`, `entityCaptionOf`, `loadVerifiedEntityAssets`, `overlayItemEntityName`, `overlayItemWantsCertifiedImage`, `planUsesRegisteredEntity`, `setChainSegmentAsset`, `verifiedEntityTextCard` |
| `internal/capabilities/scripts/adapters/scene_annotations.go` | 8 | `NewVidRushSceneMerger`, `VidRushSceneMerger.Merge`, `appendDistinctPersonAliasMentions`, `expandPersonCanonicalName`, `findAllAnnotationSpans`, `hasVerifiedBrandImage`, `normalizeBrandAssetName`, `sceneAnnotations` |
| `internal/capabilities/scripts/overlay_entity_cards_text.go` | 7 | `applyHeavySentenceBoost`, `dateHasYear`, `dedupeOverlappingGroundedDates`, `explicitDateSurfaces`, `normalizeOverlayText`, `sameGroundedDate`, `scoreForDate` |
| `internal/capabilities/scripts/usecase/testsupport/doubles.go` | 7 | `CanonicalFixture`, `DefaultFakeResult`, `FakeOllamaGen.GenerateScript`, `ItemForTimings`, `StubPostProcessor.Name`, `StubPostProcessor.Policy`, `StubPostProcessor.Process` |
| `internal/capabilities/scripts/overlay_category_filter.go` | 5 | `filterOverlayItemsByCategory`, `filterScenePhrasesByCategory`, `overlayCategoryEnabled`, `overlayItemCategory`, `overlayItemCategoryDisabled` |
| `internal/capabilities/scripts/overlay_entity_cards.go` | 4 | `admitIdentityImagesByProductionPlan`, `demoteEntityImageToTextCard`, `imageCandidate`, `overlayItemHasVerifiedIdentityAsset` |
| `internal/capabilities/scripts/result_reducer.go` | 4 | `NewVidRushResultReducer`, `VidRushResultReducer.Reduce`, `isStaleSegment`, `matchSceneSegment` |
| `internal/capabilities/scripts/audio_timeline.go` | 3 | `CompileCanonicalAudioPlan`, `CompileCanonicalAudioPlanWithTimings`, `ValidateChunkedVoiceovers` |
| `internal/capabilities/scripts/overlay_plan.go` | 3 | `Runner.analyzeSemanticBundles`, `Runner.attachVisualSemantics`, `semanticBulletSpans` |
| `internal/capabilities/scripts/adapters/vidrush_fanout_types.go` | 3 | `NewVidRushProviderFanout`, `NewVidRushProviderFanoutWithCache`, `NewVidRushProviderFanoutWithYouTube` |
| `internal/capabilities/scripts/ports/video_source_discovery.go` | 3 | `ProviderQueryPlan.Phrases`, `ProviderQueryPlan.Validate`, `VideoSourceCandidate.Validate` |
| `internal/capabilities/scripts/entity_image_motions.go` | 2 | `assignEntityImageMotions`, `capNamedEntityCardsPerScene` |
| `internal/capabilities/scripts/final_job_payload.go` | 2 | `BuildFinalJobPayloads`, `NormalizeFinalJobHTTPSRuntimeAssets` |
| `internal/capabilities/scripts/overlay_numeric_grounding.go` | 2 | `numericEntityGroundedForLanguage`, `numericValues` |
| `internal/capabilities/scripts/overlay_plan_compile_limits.go` | 2 | `resolveImageOverlayCeiling`, `wholeScenePhraseText` |
| `internal/capabilities/scripts/overlay_plan_finalize.go` | 2 | `applyOverlayStyleToItems`, `imageStyleParamName` |
| `internal/capabilities/scripts/render_queue_map_cache.go` | 2 | `loadDynamicMapVideoCache`, `storeDynamicMapVideoCache` |
| `internal/capabilities/scripts/runner_dispatch.go` | 2 | `dispatchLanguagePriority`, `orderedSceneLanguages` |
| `internal/capabilities/scripts/runner_execution_pipeline.go` | 2 | `RunnerPhaseSequence`, `runStagePhase` |
| `internal/capabilities/scripts/runner_render_units.go` | 2 | `RenderUnitCount`, `fixedMediaRenderUnits` |
| `internal/capabilities/scripts/translated_nlp.go` | 2 | `Runner.runTranslatedNLP`, `Runner.runTranslatedNLPWithSourceAnnotations` |
| `internal/capabilities/scripts/adapters/media_resolver_entity_binding.go` | 2 | `findEntityImageCandidate`, `sceneIdentityIndex.diagnostic` |
| `internal/capabilities/scripts/adapters/scene_annotations_numeric.go` | 2 | `appendGroundedNumberEntities`, `overlapsExistingNumberEntity` |
| `internal/capabilities/scripts/adapters/vidrush_asset_surface.go` | 2 | `FinalizeVidRushBindings`, `ValidateVidRushSegmentAssetBindings` |
| `internal/capabilities/scripts/adapters/processor/typed_fail_adapters.go` | 2 | `NewUnavailableEntityExtractionAdapter`, `unavailableEntityExtractionAdapter.ExtractEntities` |
| `internal/capabilities/scripts/entity_projection.go` | 1 | `projectEntityCompatibility` |
| `internal/capabilities/scripts/execution_lineage.go` | 1 | `CertifyExecutionLineage` |
| `internal/capabilities/scripts/final_job_compiled_plan.go` | 1 | `finalJobCompiledPlanPayload` |
| `internal/capabilities/scripts/final_job_image_preflight.go` | 1 | `finalJobImagePreflight` |
| `internal/capabilities/scripts/final_job_segment_identity.go` | 1 | `trimFinalJobSceneTail` |
| `internal/capabilities/scripts/final_job_source.go` | 1 | `restoreFinalJobFixedMedia` |
| `internal/capabilities/scripts/incremental_coordinator.go` | 1 | `NewVidRushIncrementalCoordinator` |
| `internal/capabilities/scripts/model_result.go` | 1 | `applyRustChartValues` |
| `internal/capabilities/scripts/model_run.go` | 1 | `ShouldRetry` |
| `internal/capabilities/scripts/overlay_scene_images.go` | 1 | `sceneImageCandidate` |
| `internal/capabilities/scripts/render_attempt_analytics.go` | 1 | `BuildRenderAttemptAnalytics` |
| `internal/capabilities/scripts/runner_deps.go` | 1 | `NewGenerationGate` |
| `internal/capabilities/scripts/runner_phase_voiceover.go` | 1 | `buildVoiceoverWork` |
| `internal/capabilities/scripts/semantic_production_plan.go` | 1 | `ScriptSearchFeedbackFromBundles` |
| `internal/capabilities/scripts/adapters/entity_image_filter.go` | 1 | `sourcePathHasCanonicalPersonName` |
| `internal/capabilities/scripts/adapters/processor_names.go` | 1 | `CanonicalProcessorNames` |
| `internal/capabilities/scripts/adapters/processor_vidrush_materialization.go` | 1 | `NewVidRushMaterializationProcessor` |
| `internal/capabilities/scripts/adapters/vidrush_fanout_plan.go` | 1 | `contextualImageLaneRequested` |
| `internal/capabilities/scripts/adapters/vidrush_helpers.go` | 1 | `VidRushCacheMetrics` |
| `internal/capabilities/scripts/adapters/vidrush_image_validation.go` | 1 | `validateVidRushImageHead` |
| `internal/capabilities/scripts/adapters/processor/gemmamemory.go` | 1 | `BuildFreshVariantPrompt` |
| `internal/capabilities/scripts/generation/plan_builder.go` | 1 | `BuildPlans` |
| `internal/capabilities/scripts/geography/enrichment.go` | 1 | `GeocodeGroundedPlaceNames` |
| `internal/capabilities/scripts/ports/research.go` | 1 | `ResearchRankerFunc.Rank` |
| `internal/capabilities/scripts/runner/backpressure.go` | 1 | `NewGenerationGate` |
| `internal/capabilities/scripts/runner/dispatch.go` | 1 | `RequestedVoiceoverLanguages` |
| `internal/capabilities/scripts/scene/synthesizer.go` | 1 | `CleanProseFallbackText` |
| `internal/capabilities/scripts/usecase/clip_sampler_impl.go` | 1 | `NewDefaultClipSampler` |
| `internal/capabilities/scripts/usecase/clip_sampler_registry.go` | 1 | `NewClipSamplerRegistry` |
| `internal/capabilities/scripts/usecase/research_subject_filter.go` | 1 | `researchCandidateSearchName` |
| `internal/capabilities/scripts/usecase/source_research_validate.go` | 1 | `validateResearchSource` |
| `internal/capabilities/scripts/usecase/gencore/engine_prompt.go` | 1 | `resetClipEvidenceFieldCharsForTest` |
| `internal/capabilities/scripts/usecase/gencore/persistence.go` | 1 | `buildGenerationResult` |
| `internal/capabilities/scripts/usecase/gencore/segment_prompt_layout.go` | 1 | `resetSegmentLayoutForTest` |

### I0 pilot utilities — 18 funzioni, 7 file
| File | # | Simboli morti |
|---|---:|---|
| `internal/platform/mutationprobe/mutationprobe.go` | 7 | `Enabled`, `Run`, `buildFailure`, `moduleRoot`, `runChildTest`, `runProbe`, `tail` |
| `internal/kernel/entitytype/entitytype.go` | 4 | `AliasSpellings`, `CorrectSurfaceType`, `Families`, `surfaceTypeTokens` |
| `internal/platform/checksum/checksum.go` | 3 | `IsProviderMD5`, `ProviderMD5Checksum`, `lowercaseHex` |
| `pkg/defaults/media.go` | 1 | `DefaultMediaConfig` |
| `pkg/defaults/voiceover.go` | 1 | `DefaultVoiceoverConfig` |
| `pkg/defaults/youtube.go` | 1 | `DefaultYouTubeConfig` |
| `pkg/immutability/copy.go` | 1 | `CloneWith` |

### I1 audio/render/remote — 32 funzioni, 11 file
| File | # | Simboli morti |
|---|---:|---|
| `internal/capabilities/audio/timeline_normalizer.go` | 7 | `IsCanonicalTimelineV2`, `MarshalTimelineV2`, `NormalizeLegacyTimeline`, `NormalizeTimelineJSON`, `normalizeAudio`, `normalizeValue`, `normalizeVideo` |
| `internal/capabilities/render/validator.go` | 7 | `NewRenderPlanValidator`, `RenderPlanValidator.Validate`, `ValidateRenderPlan`, `ValidatedRenderPlan.MarshalJSON`, `ValidatedRenderPlan.Plan`, `clonePlan`, `validateFinalAudioContract` |
| `internal/capabilities/remote/artifact_uploader.go` | 5 | `CanonicalUploadStateValues`, `IllegalTransitionError.Error`, `IllegalTransitionError.Is`, `NewIllegalTransitionError`, `NewUploadSession` |
| `internal/capabilities/cliprender/render_cache.go` | 3 | `memoryRenderCache.Get`, `memoryRenderCache.Put`, `newMemoryRenderCache` |
| `internal/capabilities/remote/complete_job_idempotency.go` | 3 | `CompleteJobIdempotencyKey`, `CompleteJobIdempotencyKeyDiagnostic`, `IsValidCompleteJobIdempotencyKey` |
| `internal/capabilities/remote/idempotency.go` | 2 | `ArtifactIdempotencyKey`, `IsValidIdempotencyKey` |
| `internal/capabilities/audio/strategy.go` | 1 | `ResolveAudioRenderStrategy` |
| `internal/capabilities/cliprender/async_worker.go` | 1 | `encodeContinuationPayload` |
| `internal/capabilities/cliprender/continuation.go` | 1 | `ParseRenderPhase` |
| `internal/capabilities/cliprender/contract_ssot.go` | 1 | `NewResolvedContract` |
| `internal/capabilities/render/plan.go` | 1 | `isSHA256` |

### I2 media & entity pipeline — 45 funzioni, 24 file
| File | # | Simboli morti |
|---|---:|---|
| `internal/capabilities/assets/providers/stock/stockpipeline/naming.go` | 4 | `TimestampParentLeafName`, `sectionDownloadForPlans`, `toPublishChunk`, `toPublishMetadata` |
| `internal/capabilities/mediaregistry/contract.go` | 4 | `ValidateProjectionSequence`, `ValidateProjectionTransition`, `fmtProjectionSequenceError`, `fmtProjectionTransitionError` |
| `internal/capabilities/animreg/animation_registry.go` | 3 | `AnimationRegistry.Entries`, `AnimationRegistry.MapTypeDefault`, `AnimationRegistry.VisualIntents` |
| `internal/capabilities/assets/providers/stock/stockpipeline/publish/publish.go` | 3 | `PerClipLeafName`, `containsAlphanumeric`, `timestampLeaf` |
| `internal/capabilities/entities/type_aliases.go` | 3 | `CanonicalEntityIDCandidates`, `CanonicalEntityTypes`, `legacyCanonicalEntityID` |
| `internal/capabilities/images/entitycatalog/verified_asset_registry.go` | 3 | `LoadVerifiedAssetRegistry`, `verifiedAssetMediaType`, `verifyVerifiedAssetEntry` |
| `internal/capabilities/mediamemory/types_enums.go` | 3 | `IsKnownDiscoveryStatus`, `IsKnownMaterializationStatus`, `IsKnownRightsStatus` |
| `internal/capabilities/mediaregistry/hashes.go` | 3 | `Fingerprint`, `NormalizeContentSHA256`, `ValidateContentSHA256` |
| `internal/capabilities/images/entitycatalog/alias_graph.go` | 2 | `CanonicalPersonIDWithDisambiguator`, `PersonAliasGraph.Aliases` |
| `internal/capabilities/mediaregistry/editing_assets_policy.go` | 2 | `EditingAssetsPolicy.SelectTransitionSFX`, `ParseEditingAssetsPolicy` |
| `internal/capabilities/mediaregistry/editorial_catalog.go` | 2 | `EditorialTransitionAliases`, `ValidateEditorialAudioCatalog` |
| `internal/capabilities/animreg/animation_registry_projection.go` | 1 | `VisualIntents` |
| `internal/capabilities/animreg/catalog_digest.go` | 1 | `CatalogDigest` |
| `internal/capabilities/assets/providers/stock/stockpipeline/finalization_render.go` | 1 | `noOpCutter.Cut` |
| `internal/capabilities/assets/providers/stock/stockpipeline/render_dto.go` | 1 | `Clip.Succeeded` |
| `internal/capabilities/assets/providers/stock/stockpipeline/service_constructors.go` | 1 | `NewTestStockPipeline` |
| `internal/capabilities/assets/providers/stock/stockpipeline/step_publish.go` | 1 | `buildPublishPlan` |
| `internal/capabilities/assets/providers/stock/stockpipeline/util.go` | 1 | `formatDuration` |
| `internal/capabilities/entities/entity_media_resolver.go` | 1 | `EntityMediaIndex.IndexForEntity` |
| `internal/capabilities/entities/overlay_window.go` | 1 | `EntitySpokenWindowDuration` |
| `internal/capabilities/images/entitycatalog/state_policy.go` | 1 | `TransitionCandidateStatus` |
| `internal/capabilities/mediamemory/resolver_lookup.go` | 1 | `fingerprintForNormalized` |
| `internal/capabilities/mediaregistry/canonical_identity.go` | 1 | `DeriveAssetSourceID` |
| `internal/capabilities/mediaregistry/index_eligibility_resolver.go` | 1 | `ResolveIndexEligibility` |

### I3 misc platform — 130 funzioni, 92 file
| File | # | Simboli morti |
|---|---:|---|
| `pkg/defaults/defaults.go` | 5 | `Duration`, `Float64`, `Truthy`, `Validate`, `validateSSOTs` |
| `internal/kernel/observability/registry.go` | 4 | `AllComponents`, `AllOperations`, `AllStages`, `StageMilestones` |
| `pkg/hmacsign/signer.go` | 4 | `Verify`, `VerifyMultipleSecrets`, `VerifyWithReplayWindow`, `subtleEqual` |
| `internal/kernel/models/registry.go` | 3 | `Model.HasVectorSpace`, `Model.Identity`, `Model.Validate` |
| `internal/platform/ai/vlm/client.go` | 3 | `Client.DedupCheck`, `Client.ValidateScript`, `Client.VisualTagImage` |
| `internal/platform/httpserver/module_base.go` | 3 | `Registry.Find`, `Registry.GetEnabled`, `Registry.Register` |
| `internal/platform/postgres/media/eligibility_reader.go` | 3 | `MediaEligibilityReader.AssetTaxonomy`, `MediaEligibilityReader.DB`, `NewMediaEligibilityReader` |
| `internal/platform/rustworker/worker.go` | 3 | `NewResourceLimiter`, `ProcessRunner.Run`, `ResourceLimiter.Acquire` |
| `pkg/termutil/terms_from_text.go` | 3 | `CleanTerms`, `TermsFromFields`, `defaultOpts` |
| `internal/app/knobs/registry.go` | 2 | `Bool`, `Registry` |
| `internal/capabilities/assets/artifacts/finalizer.go` | 2 | `NewFinalizer`, `NewFinalizerWithAssetIndex` |
| `internal/capabilities/assets/providers/stock/stockplan/youtube_highlights.go` | 2 | `PartialDownloadPlan.Validate`, `PartialDownloadPlan.YTDLPSection` |
| `internal/capabilities/images/search_queries_fanout.go` | 2 | `ImageStorageService.runRetrievalFallbackForProvider`, `ImageStorageService.runRetrievalRung` |
| `internal/capabilities/translation/argos_translator.go` | 2 | `ArgosTranslator.Translate`, `NewArgosTranslator` |
| `internal/capabilities/youtube/adapters/youtube_asset_mapper.go` | 2 | `firstNonEmpty`, `toExistingClip` |
| `internal/capabilities/youtube/metadata/quality_scoring.go` | 2 | `calculateQualityScore`, `isSponsorSegment` |
| `internal/kernel/media/assembly_contract.go` | 2 | `DefaultAssemblyReadyVideoContract`, `StreamSignature.Fingerprint` |
| `internal/platform/embeddings/contract_probe.go` | 2 | `ContractProbe.Fetch`, `NewContractProbe` |
| `internal/platform/instaeditcalendar/client.go` | 2 | `Client.CancelEvent`, `Client.GetEvent` |
| `internal/platform/instaeditcalendar/schedule.go` | 2 | `Client.WaitUntilDue`, `ScheduleGate.WaitUntilDue` |
| `internal/platform/ollama/prompts/config.go` | 2 | `Config.ApplyFilteringConfig`, `Load` |
| `internal/platform/ollama/prompts/manifest.go` | 2 | `loadConfigData`, `loadPromptFile` |
| `pkg/localeutil/locale.go` | 2 | `IsValid`, `ParsedLocale.String` |
| `pkg/security/url.go` | 2 | `AddAllowedHost`, `ValidateVideoID` |
| `pkg/veloxclient/routes.go` | 2 | `RouteMediaClipsFor`, `RouteMediaResolve` |
| `cmd/admin/internal/cleanup/flatten_images_plan.go` | 1 | `imageFolder.childFolder` |
| `cmd/admin/internal/cli/helpers.go` | 1 | `ResolveDBPath` |
| `cmd/admin/internal/rendering/gen_api_docs_manifest.go` | 1 | `writeRuntimeRouteManifest` |
| `cmd/archcheck/gate/gate.go` | 1 | `Walk` |
| `cmd/archcheck/scan/textban/textban.go` | 1 | `CountCommentResidue` |
| `internal/capabilities/assets/monitor/types_dto.go` | 1 | `SetDefaultNowForTests` |
| `internal/capabilities/assets/providers/stock/enrichment/emitter.go` | 1 | `StubAssetPublishedEmitter.EmitAssetPublished` |
| `internal/capabilities/channelprofile/registry.go` | 1 | `Reset` |
| `internal/capabilities/execution/steps/store.go` | 1 | `CanonicalStepStatusValues` |
| `internal/capabilities/images/image_contract_aliases.go` | 1 | `NewYamlResolverFromMap` |
| `internal/capabilities/images/contract/resolver.go` | 1 | `NewYamlResolverFromMap` |
| `internal/capabilities/linguistics/lexicon_aliasing.go` | 1 | `DefaultPhraseExtractionPolicy` |
| `internal/capabilities/linguistics/lexicon_registry.go` | 1 | `MustNewProfiles` |
| `internal/capabilities/sourcing/index_status.go` | 1 | `ParseSourcingIndexStatusFromBytes` |
| `internal/capabilities/translation/ports.go` | 1 | `unimplemented.Translate` |
| `internal/capabilities/youtube/dto/tagutil_clean.go` | 1 | `CleanClipName` |
| `internal/kernel/digest/perceptual.go` | 1 | `IsPerceptualHash` |
| `internal/kernel/digest/validate.go` | 1 | `ValidateSHA256` |
| `internal/kernel/embedding/handshake.go` | 1 | `Verify` |
| `internal/kernel/idempotency/keys.go` | 1 | `AssetKey` |
| `internal/kernel/idempotency/keys_hash.go` | 1 | `BuildKey` |
| `internal/kernel/job/deferral.go` | 1 | `Deferred` |
| `internal/kernel/job/payload_match.go` | 1 | `ValidatePayloadNotMatch` |
| `internal/kernel/job/preparation.go` | 1 | `PreparationClaimBandName` |
| `internal/kernel/job/stage_progress.go` | 1 | `FlattenStageProgress` |
| `internal/kernel/media/clipsequence/presets.go` | 1 | `Presets` |
| `internal/kernel/observability/concurrency_sweep.go` | 1 | `derivePhaseConcurrency` |
| `internal/kernel/observability/context.go` | 1 | `RunFromContext` |
| `internal/kernel/observability/report.go` | 1 | `DeriveBatchReport` |
| `internal/kernel/sceneir/validator.go` | 1 | `MissingProfileCount` |
| `internal/platform/ai/reranker/client.go` | 1 | `NewClient` |
| `internal/platform/assets/materializer/materializer.go` | 1 | `Dest` |
| `internal/platform/audio/worker_protocol.go` | 1 | `Processor.sendSynthesizeRequest` |
| `internal/platform/config/defaults.go` | 1 | `DefaultVoiceoverDefaults` |
| `internal/platform/downloader/youtube_gate.go` | 1 | `resetYouTubeGateForTest` |
| `internal/platform/drive/uploader_chunk.go` | 1 | `resetUploadChunkForTest` |
| `internal/platform/embeddings/http_text_embedder.go` | 1 | `NewHTTPTextEmbedder` |
| `internal/platform/filesystem/pathutil.go` | 1 | `EnsureWithinDir` |
| `internal/platform/httpserver/runtime_descriptor.go` | 1 | `Registry.RuntimeModules` |
| `internal/platform/httpserver/server.go` | 1 | `Server.GetRouter` |
| `internal/platform/httpserver/middleware/idempotency.go` | 1 | `Idempotency.Stop` |
| `internal/platform/httpserver/middleware/middleware_feature_flags.go` | 1 | `ScriptClipsEnabled` |
| `internal/platform/httpserver/middleware/middleware_logger.go` | 1 | `GetDroppedLogs` |
| `internal/platform/httpserver/middleware/middleware_workspace_scope.go` | 1 | `RequireWorkspaceScope` |
| `internal/platform/httpserver/middleware/ports.go` | 1 | `noopEnvReader.Getenv` |
| `internal/platform/media/rustexec/client.go` | 1 | `NewClient` |
| `internal/platform/media/rustexec/executor.go` | 1 | `NewResourceLimiter` |
| `internal/platform/media/rustexec/observed_executor.go` | 1 | `NewObservedExecutor` |
| `internal/platform/observability/metrics_runreports.go` | 1 | `newRunReportsCollectorWithVectors` |
| `internal/platform/ollama/client/admission_metrics.go` | 1 | `Client.hasAdmissionObserver` |
| `internal/platform/ollama/client/client_keepalive.go` | 1 | `resetEnvKeepAliveForTest` |
| `internal/platform/ollama/client/websearch.go` | 1 | `NewWebSearcher` |
| `internal/platform/ollama/types/utils.go` | 1 | `SetFilteringConfig` |
| `internal/platform/overlays/gpu_gate.go` | 1 | `NewGPUGate` |
| `internal/platform/postgres/media/media_searcher.go` | 1 | `SystemSearchRequest` |
| `internal/platform/process/tools_checker.go` | 1 | `NewToolsChecker` |
| `internal/platform/ptrutil/ptrutil.go` | 1 | `BoolDefault` |
| `internal/platform/rendercacheinfo/info.go` | 1 | `Reset` |
| `internal/platform/renderinggen/font_assets.go` | 1 | `resetFontAssetCache` |
| `internal/platform/renderinggen/queue_asset_prefetch.go` | 1 | `resetObjectPresenceCacheForTest` |
| `internal/platform/stager/http_source_stager.go` | 1 | `HTTPSourceStager.cleanupStagedSource` |
| `pkg/background/detach.go` | 1 | `InFlight` |
| `pkg/retry/decision.go` | 1 | `asAdapter` |
| `pkg/retry/errors.go` | 1 | `Retryable` |
| `pkg/retry/options.go` | 1 | `DefaultOptions` |
| `pkg/termutil/extract_likely_names.go` | 1 | `LooksLikePersonName` |
| `pkg/textutil/textutil.go` | 1 | `TokenizeWithStopWords` |

## Appendice B — test da ritirare o riscrivere
**Stessa suite** = test nello stesso package dei file morti. **Fuori package** = test che importano esplicitamente il package e chiamano un simbolo esportato con selettore `pkg.Simbolo(`. Un test elencato non è automaticamente da cancellare: se copre anche funzioni vive va tagliato, non rimosso.

### A1 budget & charts
- Test stessa suite (0): nessuno
- Test fuori package (2): `internal/capabilities/scripts/bullets_charts_cert_test.go`, `internal/capabilities/scripts/chart_wiring_test.go`

### A2 presets & motion
- Test stessa suite (0): nessuno
- Test fuori package (2): `internal/capabilities/scripts/entity_image_motion_rotation_test.go`, `internal/capabilities/scripts/runner_overlay_plan_test.go`

### A3 overlay contracts
- Test stessa suite (2): `internal/capabilities/overlays/model/intent_test.go`, `internal/capabilities/overlays/render/composite_motion_pools_test.go`
- Test fuori package (3): `internal/capabilities/scripts/composite_group_motion_test.go`, `internal/capabilities/scripts/entity_image_motion_rotation_test.go`, `internal/capabilities/scripts/font_ssot_parity_test.go`

### B app wiring
- Test stessa suite (12): `internal/app/wiring/atomic_fail_closed_test.go`, `internal/app/wiring/health_wiring_test.go`, `internal/app/wiring/lifecycle/runtime_contract_test.go`, `internal/app/wiring/lifecycle/runtime_integration_test.go`, `internal/app/wiring/lifecycle_capability_disabled_test.go`, `internal/app/wiring/lifecycle_job_runner_test.go`, `internal/app/wiring/media_ingest_test.go`, `internal/app/wiring/rendering/runtime_test.go`, `internal/app/wiring/script/remote_final_job_drive_source_test.go`, `internal/app/wiring/script/remote_final_job_resolve_test.go`, `internal/app/wiring/script/scene_text_generator_test.go`, `internal/app/wiring/wire_test.go`
- Test fuori package (0): nessuno

### C jobs codec
- Test stessa suite (14): `internal/capabilities/jobs/bundle_test.go`, `internal/capabilities/jobs/delivery_test.go`, `internal/capabilities/jobs/preparation_fingerprint_test.go`, `internal/capabilities/jobs/preparation_metrics_test.go`, `internal/capabilities/jobs/preparation_work_estimator_test.go`, `internal/capabilities/jobs/preparation_workload_ema_sqlite_test.go`, `internal/capabilities/jobs/queue/claim_test.go`, `internal/capabilities/jobs/registry_codec_completeness_test.go`, `internal/capabilities/jobs/registry_wiring_test.go`, `internal/capabilities/jobs/scheduling/resource_scheduler_test.go`, `internal/capabilities/jobs/worker/runner_claim_snapshot_test.go`, `internal/capabilities/jobs/worker/runner_observability_test.go`, `internal/capabilities/jobs/worker/runner_test.go`, `internal/capabilities/jobs/workflow_step_completed_test.go`
- Test fuori package (0): nessuno

### D sqlite & media registry
- Test stessa suite (13): `internal/platform/sqlite/backup_set_test.go`, `internal/platform/sqlite/controlplane/verifier_test.go`, `internal/platform/sqlite/executionsteps/store_test.go`, `internal/platform/sqlite/mediaregistry/content_links_test.go`, `internal/platform/sqlite/mediaregistry/projection_advance_test.go`, `internal/platform/sqlite/migrations_declared_tables_test.go`, `internal/platform/sqlite/outboxevents/pool_resiliency_test.go`, `internal/platform/sqlite/outboxevents/supersede_envelope_test.go`, `internal/platform/sqlite/outboxevents/supersede_test.go`, `internal/platform/sqlite/performance/registry_test.go`, `internal/platform/sqlite/performance/sampler_test.go`, `internal/platform/sqlite/pool_test.go`, `internal/platform/sqlite/state_matrix_live_test.go`
- Test fuori package (10): `internal/app/wiring/performance_projection_integration_test.go`, `internal/capabilities/assets/catalogsync/service_test.go`, `internal/capabilities/assets/maintenance/service_test.go`, `internal/capabilities/assets/monitor/youtube_discoveries_scoring_test.go`, `internal/capabilities/jobs/policy_service_test.go`, `internal/capabilities/jobs/service_test.go`, `internal/platform/postgres/media/backfill_test.go`, `internal/platform/sqlite/migrations_twin_source_test.go`, `internal/platform/sqlite/scripts/scripts_test.go`, `internal/platform/sqlite/state_matrix_live_test.go`

### E voiceover & harness
- Test stessa suite (20): `internal/app/wiring/voiceover/build_bundles_voiceover_rate_limits_wait_test.go`, `internal/capabilities/voiceover/service/fingerprint_v3_test.go`, `internal/capabilities/voiceover/service/process_metadata_test.go`, `internal/capabilities/voiceover/service/process_segment_e2e_fase5_test.go`, `internal/capabilities/voiceover/service/process_segment_e2e_fase6_test.go`, `internal/capabilities/voiceover/service/service_p01_state_machine_test.go`, `internal/capabilities/voiceover/service/timing_fingerprint_test.go`, `internal/capabilities/voiceover/service/types_test.go`, `internal/capabilities/voiceover/service/types_typed_test.go`, `internal/capabilities/voiceover/service/upload_intent_test.go`, `internal/capabilities/voiceover/service/voice_override_test.go`, `tests/operational/voiceover_b2_test.go`, `tests/operational/voiceover_c1_tts_missing_test.go`, `tests/operational/voiceover_c2_legacy_fallback_test.go`, `tests/operational/voiceover_c4_outbox_decoupling_test.go`, `tests/operational/voiceover_d2_parent_aggregator_state_test.go`, `tests/operational/voiceover_d3_required_optional_test.go`, `tests/operational/voiceover_e2e_smoke_test.go`, `tests/operational/voiceover_happy_test.go`, `tests/operational/voiceover_harness_test.go`
- Test fuori package (0): nessuno

### F kernel asset
- Test stessa suite (14): `internal/kernel/asset/bcp47_test.go`, `internal/kernel/asset/detail/ai_stock_test.go`, `internal/kernel/asset/detail/clip_identity_test.go`, `internal/kernel/asset/detail/clip_semantic_metadata_test.go`, `internal/kernel/asset/detail/core_scoring_test.go`, `internal/kernel/asset/detail/search_text_test.go`, `internal/kernel/asset/detail/source_catalog_test.go`, `internal/kernel/asset/duration_test.go`, `internal/kernel/asset/evidence_test.go`, `internal/kernel/asset/language_registry_test.go`, `internal/kernel/asset/lifecycle_state_test.go`, `internal/kernel/asset/lifecycle_test.go`, `internal/kernel/asset/provider_registry_test.go`, `internal/kernel/asset/rights_state_test.go`
- Test fuori package (3): `internal/capabilities/assets/texttracks/materializer_concurrency_test.go`, `internal/capabilities/assets/texttracks/materializer_test.go`, `internal/capabilities/youtube/usecase/text_track_resolver_test.go`

### G kernel retrieval
- Test stessa suite (3): `internal/kernel/script/builtin_audio_aliases_test.go`, `internal/kernel/script/immutability_test.go`, `internal/kernel/script/retrieval_query_builders_test.go`
- Test fuori package (3): `internal/capabilities/mediaregistry/builtin_audio_vocabulary_contract_test.go`, `internal/capabilities/scripts/generation/plan_builder_video_metadata_contract_test.go`, `internal/capabilities/scripts/usecase/gencore/clip_native_enforce_test.go`

### H scripts overlay chain
- Test stessa suite (104): `internal/capabilities/scripts/adapters/entity_image_catalog_integration_test.go`, `internal/capabilities/scripts/adapters/entity_image_identity_index_test.go`, `internal/capabilities/scripts/adapters/incremental_stock_gate_test.go`, `internal/capabilities/scripts/adapters/media_manifest_shim_test.go`, `internal/capabilities/scripts/adapters/postprocessor_progress_test.go`, `internal/capabilities/scripts/adapters/processor/compat_adapters_test.go`, `internal/capabilities/scripts/adapters/processor/gemmamemory_test.go`, `internal/capabilities/scripts/adapters/processor_default_policy_test.go`, `internal/capabilities/scripts/adapters/processor_internet_images_binding_test.go`, `internal/capabilities/scripts/adapters/processor_internet_images_test.go`, `internal/capabilities/scripts/adapters/processor_names_test.go`, `internal/capabilities/scripts/adapters/processor_vidrush_materialization_test.go`, `internal/capabilities/scripts/adapters/scene_annotations_identity_test.go`, `internal/capabilities/scripts/adapters/scene_annotations_test.go`, `internal/capabilities/scripts/adapters/scene_execution_test.go`, `internal/capabilities/scripts/adapters/segment_scene_merger_test.go`, `internal/capabilities/scripts/adapters/vidrush_cache_metrics_test.go`, `internal/capabilities/scripts/adapters/vidrush_contract_test.go`, `internal/capabilities/scripts/adapters/vidrush_disabled_provider_test.go`, `internal/capabilities/scripts/adapters/vidrush_fanout_plan_branch_test.go`, `internal/capabilities/scripts/adapters/vidrush_helpers_test.go`, `internal/capabilities/scripts/adapters/vidrush_registry_searchers_test.go`, `internal/capabilities/scripts/audio_timeline_test.go`, `internal/capabilities/scripts/av_sync_certification_test.go`, `internal/capabilities/scripts/bullet_group_limit_test.go`, `internal/capabilities/scripts/bullets_charts_cert_test.go`, `internal/capabilities/scripts/chart_wiring_test.go`, `internal/capabilities/scripts/composite_group_motion_test.go`, `internal/capabilities/scripts/dynamic_map_video_cache_test.go`, `internal/capabilities/scripts/editorial_bullet_values_test.go`, `internal/capabilities/scripts/entity_annotation_projection_test.go`, `internal/capabilities/scripts/entity_compatibility_test.go`, `internal/capabilities/scripts/entity_image_motion_rotation_test.go`, `internal/capabilities/scripts/entity_projection_test.go`, `internal/capabilities/scripts/entity_style_wire_test.go`, `internal/capabilities/scripts/execution_lineage_test.go`, `internal/capabilities/scripts/final_job_compiled_plan_test.go`, `internal/capabilities/scripts/final_job_demolition_benchmark_test.go`, `internal/capabilities/scripts/final_job_dto_test.go`, `internal/capabilities/scripts/final_job_https_assets_test.go`, `internal/capabilities/scripts/final_job_image_preflight_test.go`, `internal/capabilities/scripts/final_job_overlay_ssot_gate_test.go`, `internal/capabilities/scripts/final_job_payload_test.go`, `internal/capabilities/scripts/final_job_pending_test.go`, `internal/capabilities/scripts/final_job_report_test.go`, `internal/capabilities/scripts/final_job_stale_receipt_test.go`, `internal/capabilities/scripts/final_job_stock_boundary_test.go`, `internal/capabilities/scripts/final_job_timing_test.go`, `internal/capabilities/scripts/font_ssot_parity_test.go`, `internal/capabilities/scripts/generation/plan_builder_test.go`, `internal/capabilities/scripts/generation/plan_builder_video_metadata_contract_test.go`, `internal/capabilities/scripts/incremental_coordinator_expected_scenes_test.go`, `internal/capabilities/scripts/incremental_coordinator_provider_budget_test.go`, `internal/capabilities/scripts/incremental_coordinator_stale_details_test.go`, `internal/capabilities/scripts/incremental_coordinator_test.go`, `internal/capabilities/scripts/overlay_background_test.go`, `internal/capabilities/scripts/overlay_category_filter_test.go`, `internal/capabilities/scripts/overlay_category_prefilter_test.go`, `internal/capabilities/scripts/overlay_entity_asset_registry_chain_test.go`, `internal/capabilities/scripts/overlay_entity_asset_registry_test.go`, `internal/capabilities/scripts/overlay_entity_cards_test.go`, `internal/capabilities/scripts/overlay_entity_cards_text_test.go`, `internal/capabilities/scripts/overlay_group_dates_test.go`, `internal/capabilities/scripts/overlay_group_limits_test.go`, `internal/capabilities/scripts/overlay_image_budget_test.go`, `internal/capabilities/scripts/overlay_image_glow_test.go`, `internal/capabilities/scripts/overlay_numeric_rescue_test.go`, `internal/capabilities/scripts/overlay_plan_whole_scene_phrase_test.go`, `internal/capabilities/scripts/overlay_scene_images_test.go`, `internal/capabilities/scripts/overlay_scene_input_test.go`, `internal/capabilities/scripts/render_attempt_analytics_test.go`, `internal/capabilities/scripts/render_run_id_test.go`, `internal/capabilities/scripts/result_reducer_test.go`, `internal/capabilities/scripts/runner/backpressure_test.go`, `internal/capabilities/scripts/runner/dispatch_test.go`, `internal/capabilities/scripts/runner_dispatch_test.go`, `internal/capabilities/scripts/runner_lifecycle_test.go`, `internal/capabilities/scripts/runner_phase_script_fixed_test.go`, `internal/capabilities/scripts/runner_phase_sequence_test.go`, `internal/capabilities/scripts/runner_render_units_test.go`, `internal/capabilities/scripts/runner_stage_instrumentation_test.go`, `internal/capabilities/scripts/runner_tts_pool_test.go`, `internal/capabilities/scripts/scene/synthesizer_test.go`, `internal/capabilities/scripts/scene_semantic_candidates_test.go`, `internal/capabilities/scripts/segment_no_voiceover_test.go`, `internal/capabilities/scripts/semantic_identity_admission_test.go`, `internal/capabilities/scripts/translated_nlp_property_test.go`, `internal/capabilities/scripts/translated_nlp_test.go`, `internal/capabilities/scripts/usecase/clip_sampler_audit_test.go`, `internal/capabilities/scripts/usecase/clip_sampler_impl_test.go`, `internal/capabilities/scripts/usecase/clip_sampler_registry_test.go`, `internal/capabilities/scripts/usecase/gencore/clip_evidence_truncate_test.go`, `internal/capabilities/scripts/usecase/gencore/engine_generate_test.go`, `internal/capabilities/scripts/usecase/gencore/generate_one_usecase_test.go`, `internal/capabilities/scripts/usecase/gencore/generation_postprocess_test.go`, `internal/capabilities/scripts/usecase/gencore/persistence_translated_text_test.go`, `internal/capabilities/scripts/usecase/gencore/segment_prompt_layout_test.go`, `internal/capabilities/scripts/usecase/research_subject_filter_test.go`, `internal/capabilities/scripts/usecase/script_timing_test.go`, `internal/capabilities/scripts/usecase/source_resolver_curate_test.go`, `internal/capabilities/scripts/usecase/source_resolver_search_semantics_test.go`, `internal/capabilities/scripts/vidrush_e2e_test.go`, `internal/capabilities/scripts/vidrush_metrics_test.go`, `internal/capabilities/scripts/vidrush_youtube_stale_fencing_test.go`
- Test fuori package (11): `internal/capabilities/scripts/adapters/source_registry_test.go`, `internal/capabilities/scripts/adapters/tractors_e2e_test.go`, `internal/capabilities/scripts/adapters/vidrush_replay_idempotency_test.go`, `internal/capabilities/scripts/generation/plan_builder_contract_test.go`, `internal/capabilities/scripts/generation/plan_builder_test.go`, `internal/capabilities/scripts/geocoding_region_test.go`, `internal/capabilities/scripts/geocoding_test.go`, `internal/capabilities/scripts/usecase/gencore/generate_one_usecase_test.go`, `internal/capabilities/scripts/usecase/prompt_injection_p2b_test.go`, `internal/capabilities/scripts/usecase/script_timing_test.go`, `internal/platform/media/rustexec/semantic_engine_e2e_test.go`

### I0 pilot utilities
- Test stessa suite (6): `internal/kernel/entitytype/entitytype_test.go`, `internal/platform/checksum/checksum_test.go`, `pkg/defaults/defaults_test.go`, `pkg/defaults/media_test.go`, `pkg/defaults/voiceover_test.go`, `pkg/defaults/youtube_test.go`
- Test fuori package (2): `internal/capabilities/cliprender/chunk_mutation_probe_test.go`, `internal/capabilities/entities/type_aliases_test.go`

### I1 audio/render/remote
- Test stessa suite (14): `internal/capabilities/audio/contract_test.go`, `internal/capabilities/audio/timeline_normalizer_test.go`, `internal/capabilities/cliprender/certify_test.go`, `internal/capabilities/cliprender/continuation_test.go`, `internal/capabilities/cliprender/contract_registry_test.go`, `internal/capabilities/cliprender/fingerprint_batch_test.go`, `internal/capabilities/cliprender/plan_test.go`, `internal/capabilities/cliprender/settle_deferral_test.go`, `internal/capabilities/cliprender/worker_restart_test.go`, `internal/capabilities/cliprender/worker_test.go`, `internal/capabilities/remote/artifact_uploader_test.go`, `internal/capabilities/remote/complete_job_test.go`, `internal/capabilities/remote/idempotency_factory_test.go`, `internal/capabilities/render/validator_test.go`
- Test fuori package (2): `internal/app/wiring/lifecycle_job_runner_test.go`, `internal/platform/renderinggen/clip_overlay_composite_test.go`

### I2 media & entity pipeline
- Test stessa suite (26): `internal/capabilities/animreg/animation_catalog_digest_test.go`, `internal/capabilities/assets/providers/stock/stockpipeline/naming_test.go`, `internal/capabilities/assets/providers/stock/stockpipeline/ports_test.go`, `internal/capabilities/assets/providers/stock/stockpipeline/publish/publish_test.go`, `internal/capabilities/assets/providers/stock/stockpipeline/publish_contract_test.go`, `internal/capabilities/assets/providers/stock/stockpipeline/publish_plan_test.go`, `internal/capabilities/assets/providers/stock/stockpipeline/section_layout_test.go`, `internal/capabilities/assets/providers/stock/stockpipeline/service_constructor_test.go`, `internal/capabilities/assets/providers/stock/stockpipeline/step_extract_clips_concurrency_test.go`, `internal/capabilities/assets/providers/stock/stockpipeline/step_extract_clips_helpers_test.go`, `internal/capabilities/assets/providers/stock/stockpipeline/step_sub_operations_test.go`, `internal/capabilities/assets/providers/stock/stockpipeline/util_test.go`, `internal/capabilities/entities/entity_media_resolver_test.go`, `internal/capabilities/entities/type_aliases_test.go`, `internal/capabilities/images/entitycatalog/alias_graph_test.go`, `internal/capabilities/images/entitycatalog/state_policy_test.go`, `internal/capabilities/images/entitycatalog/verified_asset_registry_test.go`, `internal/capabilities/mediamemory/normalizer_test.go`, `internal/capabilities/mediamemory/ranker_oracle_gates_test.go`, `internal/capabilities/mediamemory/resolver_test.go`, `internal/capabilities/mediamemory/types_test.go`, `internal/capabilities/mediaregistry/editing_assets_policy_test.go`, `internal/capabilities/mediaregistry/editorial_catalog_test.go`, `internal/capabilities/mediaregistry/editorial_projection_test.go`, `internal/capabilities/mediaregistry/hashes_test.go`, `internal/capabilities/mediaregistry/projection_test.go`
- Test fuori package (1): `internal/platform/sqlite/mediaregistry/canonical_identity_test.go`

### I3 misc platform
- Test stessa suite (101): `cmd/admin/internal/cleanup/flatten_images_drive_test.go`, `cmd/admin/internal/rendering/gen_api_docs_manifest_test.go`, `cmd/archcheck/gate/gate_test.go`, `internal/capabilities/assets/artifacts/finalizer_test.go`, `internal/capabilities/assets/monitor/types_dto_clock_test.go`, `internal/capabilities/assets/providers/stock/enrichment/idempotency_test.go`, `internal/capabilities/assets/providers/stock/stockplan/youtube_highlights_test.go`, `internal/capabilities/assets/providers/stock/stockplan/youtube_partial_timeline_test.go`, `internal/capabilities/channelprofile/channelprofile_test.go`, `internal/capabilities/execution/steps/store_test.go`, `internal/capabilities/images/resolver_test.go`, `internal/capabilities/images/search_queries_engines_test.go`, `internal/capabilities/images/storage_search_test.go`, `internal/capabilities/images/territory_handlers_test.go`, `internal/capabilities/images/upload_handler_test.go`, `internal/capabilities/linguistics/lexicon_registry_test.go`, `internal/capabilities/sourcing/index_status_test.go`, `internal/capabilities/translation/argos_server_translator_test.go`, `internal/capabilities/translation/argos_translator_test.go`, `internal/capabilities/youtube/adapters/youtube_adapters_test.go`, `internal/capabilities/youtube/metadata/service_test.go`, `internal/kernel/digest/digest_test.go`, `internal/kernel/digest/perceptual_test.go`, `internal/kernel/digest/verifier_test.go`, `internal/kernel/embedding/handshake_test.go`, `internal/kernel/idempotency/keys_test.go`, `internal/kernel/job/deferral_test.go`, `internal/kernel/job/payload_match_test.go`, `internal/kernel/job/stage_progress_test.go`, `internal/kernel/media/clipsequence/plan_test.go`, `internal/kernel/observability/concurrency_sweep_test.go`, `internal/kernel/observability/observability_test.go`, `internal/kernel/observability/resource_report_test.go`, `internal/kernel/observability/slot_wait_test.go`, `internal/kernel/sceneir/compiler_test.go`, `internal/platform/ai/reranker/client_test.go`, `internal/platform/assets/materializer/materializer_test.go`, `internal/platform/audio/processor_test.go`, `internal/platform/downloader/caption_flags_test.go`, `internal/platform/downloader/downloader_test.go`, `internal/platform/downloader/mirror_sections_test.go`, `internal/platform/downloader/youtube_gate_test.go`, `internal/platform/drive/uploader_chunk_test.go`, `internal/platform/embeddings/contract_probe_test.go`, `internal/platform/embeddings/http_text_embedder_test.go`, `internal/platform/filesystem/pathutil_shared_test.go`, `internal/platform/httpserver/feature_flags_verification_test.go`, `internal/platform/httpserver/middleware/middleware_logger_test.go`, `internal/platform/httpserver/middleware/middleware_workspace_scope_test.go`, `internal/platform/httpserver/routes_test.go`, `internal/platform/httpserver/runtime_descriptor_test.go`, `internal/platform/httpserver/server_clips_e2e_test.go`, `internal/platform/httpserver/server_models_endpoint_test.go`, `internal/platform/httpserver/server_pipeline_e2e_test.go`, `internal/platform/httpserver/server_test.go`, `internal/platform/instaeditcalendar/outbox_test.go`, `internal/platform/media/rustexec/audio_gate_concurrent_render_test.go`, `internal/platform/media/rustexec/audio_gate_differential_e2e_test.go`, `internal/platform/media/rustexec/audio_gate_vo_bgm_e2e_test.go`, `internal/platform/media/rustexec/audio_gate_vo_sfx_e2e_test.go`, `internal/platform/media/rustexec/client_test.go`, `internal/platform/media/rustexec/executor_test.go`, `internal/platform/media/rustexec/mediaexec_golden_test.go`, `internal/platform/media/rustexec/observed_executor_test.go`, `internal/platform/media/rustexec/protocol_test.go`, `internal/platform/media/rustexec/timestamp_parse_test.go`, `internal/platform/media/rustexec/video_processor_compat_test.go`, `internal/platform/observability/metrics_runreports_test.go`, `internal/platform/observability/progress_ratio_collector_test.go`, `internal/platform/ollama/client/admission_observer_test.go`, `internal/platform/ollama/client/admission_test.go`, `internal/platform/ollama/client/client_core_wire_test.go`, `internal/platform/ollama/client/client_errors_p1c_test.go`, `internal/platform/ollama/client/client_generate_test.go`, `internal/platform/ollama/client/client_health_test.go`, `internal/platform/ollama/client/client_keepalive_test.go`, `internal/platform/ollama/client/client_transport_test.go`, `internal/platform/ollama/client/client_warm_test.go`, `internal/platform/ollama/client/websearch_test.go`, `internal/platform/ollama/prompts/manifest_test.go`, `internal/platform/overlays/gpu_gate_test.go`, `internal/platform/postgres/media/eligibility_reader_test.go`, `internal/platform/postgres/media/hnsw_indexes_test.go`, `internal/platform/postgres/media/vector_surfaces_test.go`, `internal/platform/process/runner_test.go`, `internal/platform/process/tools_checker_test.go`, `internal/platform/rendercacheinfo/info_test.go`, `internal/platform/renderinggen/font_assets_test.go`, `internal/platform/renderinggen/queue_asset_prefetch_presence_test.go`, `internal/platform/stager/http_source_stager_test.go`, `pkg/background/detach_test.go`, `pkg/defaults/defaults_test.go`, `pkg/hmacsign/signer_test.go`, `pkg/localeutil/locale_test.go`, `pkg/retry/decision_test.go`, `pkg/retry/errors_test.go`, `pkg/retry/retry_test.go`, `pkg/security/url_test.go`, `pkg/termutil/extract_likely_names_test.go`, `pkg/termutil/terms_from_text_test.go`, `pkg/veloxclient/routes_test.go`
- Test fuori package (31): `cmd/admin/internal/cleanup/zombie_sweep_test.go`, `internal/app/wiring/rendering/handlers_test.go`, `internal/capabilities/assets/clips/gate_test.go`, `internal/capabilities/assets/diagnostics/gate_test.go`, `internal/capabilities/assets/gate_test.go`, `internal/capabilities/assets/lifecycle/service_test.go`, `internal/capabilities/assets/register/gate_test.go`, `internal/capabilities/assets/search/gate_test.go`, `internal/capabilities/assets/soundeffect/gate_test.go`, `internal/capabilities/assets/stock/gate_test.go`, `internal/capabilities/assets/storage/gate_test.go`, `internal/capabilities/assets/voiceover/gate_test.go`, `internal/capabilities/assets/youtube/gate_test.go`, `internal/capabilities/images/gate_test.go`, `internal/capabilities/jobs/gate_test.go`, `internal/capabilities/jobs/worker_dispatch_retry_gate_test.go`, `internal/capabilities/script/gate_test.go`, `internal/capabilities/scripts/adapters/vidrush_perceptual_test.go`, `internal/capabilities/scripts/channel_profile_apply_test.go`, `internal/capabilities/scripts/runner_phase_sequence_test.go`, `internal/capabilities/scripts/usecase/llm_errors_p1c_test.go`, `internal/capabilities/system/gate_test.go`, `internal/capabilities/youtube/adapters/extractor_process_test.go`, `internal/capabilities/youtube/usecase/extraction_subfolder_test.go`, `internal/capabilities/youtube/usecase/service_test.go`, `internal/platform/httpserver/feature_flags_verification_test.go`, `internal/platform/httpserver/gate_test.go`, `internal/platform/httpserver/middleware/gate_test.go`, `internal/platform/httpserver/transport/gate_test.go`, `internal/platform/ollama/client/client_errors_p1c_test.go`, `internal/platform/sqlite/jobs/preparation_claim_snapshot_test.go`
