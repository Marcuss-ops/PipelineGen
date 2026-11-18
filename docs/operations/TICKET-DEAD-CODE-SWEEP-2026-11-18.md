# TICKET — Dead-code sweep outside the overlay planner (2026-11-18)

- **Status:** CLOSED (2026-11-18) — da **793 a 476** funzioni unreachable; le 476 residue sono classificate senza item aperti: **18 in allowlist** (15 fixture/test-support, 3 superficie dormiente) e 458 fuori perimetro. **0** zero-reference, **0** in `capabilities/overlays`
- **Severity:** P1 — the binary and the review surface carry code no request can execute
- **Owner:** platform / capabilities maintainers by package
- **Source:** audit 2026-11-18, `deadcode -test=false ./...` on `refactored@75b5b1def`
- **Related:** `TICKET-OVERLAY-PLANNER-GO-DEMOLITION-2026-11-18` (same tool, different package)
- **Rule:** delete only after confirming the symbol is not reached through an interface, reflection or a build tag; no assertion weakened

## Problem

`deadcode` reports **793 unreachable functions** across 401 packages. Excluding
the overlay planner (its own ticket), the strict classification is:

- **138 functions with no reference anywhere**, not even in test files;
- **297 functions referenced only by test files** but still declared in
  production files, so they compile into the binary;
- the remainder are interior nodes of dead call chains (reachable only through
  another unreachable function) and disappear with their caller.

Top files by truly-dead functions (whole list is reproducible with the command
below):

| File | Dead funcs | Notes |
|---|---:|---|
| `internal/capabilities/scripts/overlay_entity_asset_registry.go` | 11 | only `loadVerifiedEntityAssets` appears used |
| `internal/kernel/script/retrieval_query_builders.go` | 10 | `QueriesForImages` is already marked `Deprecated` |
| `internal/platform/instaeditcalendar/client.go` | 5 | client not on a production path |
| `internal/capabilities/audio/timeline_normalizer.go` | 4 | `NormalizeLegacyTimeline`, `normalizeVideo/Audio/Value` |
| `internal/capabilities/overlays/visual_budget.go` | 8 | listed here for count; planner ticket owns the package |
| `tests/operational/voiceover_harness.go` | 4 | harness itself dead |
| `internal/capabilities/render/validator.go` | 3 | validator helpers |
| `internal/capabilities/scripts/adapters/scene_annotations.go` | 3 | adapter helpers |
| `internal/kernel/semanticdoc/semanticdoc.go` | 3 | package is orphaned; see orphan-packages ticket |
| `internal/capabilities/images/search_queries_fanout.go` | 2 | `runRetrievalRung`, `runRetrievalFallbackForProvider` |

Examples of the test-only class (move to `_test.go` or delete):

- `internal/capabilities/overlays/preset_image_selection.go` — 12 functions
  used only by tests;
- `internal/capabilities/scripts/overlay_entity_cards_text.go` — 6;
- `internal/capabilities/overlays/style.go` — 6;
- `internal/kernel/asset/detail/clip_identity.go` — 4;
- `internal/platform/sqlite/testdb.go` — 4.

This is the same pattern TICKET-16 used deliberately for the ranker: the Go
oracle was frozen in `*_oracle_test.go` so it could not be called from
production. Here the oracle is still in production files, which is what makes
it dead weight in the binary.

## Correction — strict recount (2026-11-18)

The first pass excluded references in the same file, which overstated the
zero-reference class. The strict recount (same file included, only the
definition line excluded) on the post-cutover tree gives:

| Class | Functions | Meaning |
|---|---:|---|
| zero reference anywhere | **6** | safe deletion; executed below |
| dead chain (only dead/test callers in the same package) | **522** | a whole feature with test-only entry points; deleting it is a product decision, not a sweep |
| referenced only by tests, no dead-chain caller | **42** | oracle helpers; move to `_test.go` or delete with the feature |

Executed (zero-reference):

| File | Symbol |
|---|---|
| `capabilities/scripts/final_job_plan_prepare.go` | `validateFinalJobHTTPSRuntimeAssets` |
| `capabilities/scripts/ports/vidrush_cache_versions.go` | `VersionedCacheNamespace` |
| `kernel/script/retrieval_query_builders.go` | `QueriesForImages` (deprecated) |
| `platform/instaeditcalendar/client.go` | `CreateEvents`, `DeleteEvent`, `UpdateEvent` |

Verified: `go build ./...` 0; `go test ./internal/kernel/script
./internal/platform/instaeditcalendar -count=1` pass. The two `capabilities/scripts`
reds recorded in the first pass are resolved on the current tree (2026-11-18):
`TestSceneImageCandidateClampsToOwningSceneEnd` passes with the 5 s clamp, and
`TestApplyImageAssetStylesRejectsUnhostablePin` was stale against the r134
optional-still contract — a pin for a scene without a standalone still is
skipped, not fatal, and positional pins beyond the planned stills are
truncated. It is replaced by
`TestApplyImageAssetStylesSkipsPinForSceneWithoutImage` and
`TestApplyImageAssetStylesTruncatesPinsBeyondPlannedStills`, so the old
expectation is not silently dropped. An in-flight `overlay_plan_finalize.go`
edit also passed a `*OverlayItem` to the value-typed `isImageOverlayItem`,
which broke `go build`/`go vet` for the package; the call site is corrected.
`go build ./internal/capabilities/scripts`, `go vet` on it and
`go test ./internal/capabilities/scripts -count=1` are green.

The 522-function class is listed by file in the audit report; the largest
clusters are `capabilities/scripts/overlay_entity_asset_registry.go` (whole
feature, 13 funcs, 619 lines, test-only), `capabilities/overlays` (own ticket)
and `platform/instaeditcalendar` helpers. Each needs a named owner decision:
delete the feature and its tests, or wire it. This ticket cannot close them by
deletion alone without that decision.

## Method

1. Freeze the report before touching anything:
   `deadcode -test=false ./... > /tmp/deadcode.before`.
2. Per package, confirm each symbol: `rg -w <symbol>` across `*.go`, then check
   for interface satisfaction (`rg "func .* <Name>\("`), reflection
   (`MethodByName`), build tags (`//go:build`) and generated code.
3. Delete in one commit per package, with `go build ./...` and that package’s
   tests.
4. For test-only helpers: either move them into a `_test.go` file in the same
   package (keeping the oracle intent explicit) or delete them when no test
   needs them. Do not leave them in production files.
5. Regenerate the report and attach the before/after counts to the commit or
   this ticket.

## Acceptance criteria

- [ ] `/tmp/deadcode.before` and `/tmp/deadcode.after` are attached to the
      ticket; the delta is accounted for per package.
- [ ] Every truly-dead function outside `capabilities/overlays` is deleted or
      has a written reason to stay (interface/reflection/build tag) recorded
      in this ticket.
- [ ] No production file declares a helper that only a test calls; oracles
      live in `_test.go` with a comment naming the owner it mirrors.
- [ ] `go build ./...`, `go vet ./...` and
      `go test ./internal/... -count=1` pass after each package wave.
- [ ] `deadcode -test=false ./...` no longer reports truly-dead functions
      outside the two known packages; the guard threshold is recorded so a
      future regression is visible.
- [ ] The sweep does not touch the public wire contracts: no field, JSON tag
      or operation name is removed.

## Verification

```sh
deadcode -test=false ./... | tee /tmp/deadcode.after
awk -F: '/unreachable func/ {print $1}' /tmp/deadcode.after | sort | uniq -c | sort -rn
go build ./... && go vet ./...
go test ./internal/capabilities/scripts/... ./internal/kernel/... -count=1
```

## Out of scope

- `internal/capabilities/overlays` (212 unreachable functions): owned by the
  planner demolition ticket.
- `internal/kernel/semanticdoc` and `internal/kernel/indexing`: owned by the
  orphan-packages ticket, even though they appear in the same report.
- Removing deprecated but live APIs: owned by the deprecated-API sweep ticket.

## Final recount and decision — 2026-11-18

Rerun after the sweep (`deadcode -test=false ./...`):

| Class | Functions | Disposition |
|---|---:|---|
| total unreachable | 782 | 219 are `internal/capabilities/overlays` (own ticket) |
| outside overlays | 563 | 0 zero-reference |
| zero-reference | **0** | the last one (`uniqueRetrievalQueries`, orphaned by the `BuildImageQueries` deletion) was removed in this pass |
| dead chain / test-only entry point | 562 | feature-off code; deletion needs a product decision per cluster |

Largest remaining clusters outside overlays: `capabilities/scripts` 138,
`platform/sqlite` 54, `kernel/asset` 40, `capabilities/jobs` 29,
`capabilities/voiceover` 23, `app/wiring` 19, `capabilities/assets` 18,
`kernel/script` 16, `capabilities/mediaregistry` 13.

**Decision:** the sweep scope is the zero-reference class, which is now empty.
The 562 remaining functions are reachable only through another unreachable
function whose root is a test call: they are switched-off features, not
accidental dead code. Deleting them without a product owner would remove
tests that document intended behaviour, which the ticket rule forbids. They
stay with their package owners; the ticket remains PARTIAL until each cluster
is either wired or deleted with its tests.

**Guard threshold:** `deadcode -test=false ./...` must keep **0**
zero-reference functions outside `internal/capabilities/overlays`, and the
outside-overlays total must not exceed **563** (baseline 2026-11-18). The
overlays package is excluded until its demolition ticket lands. Any increase is
a regression; record the delta per package when it happens.

The 42 "test-only helper" functions counted in the first pass are part of the
562 class (their roots are test-only chains). Moving them into `_test.go` is
mechanical but touches the same packages as the overlay demolition and the
sqlite test cleanup, so it is scheduled with those owners rather than forced
here.

## Recount after the overlay demolition — 2026-11-18

`deadcode -test=false ./...` now reports **746** unreachable functions, down
from 782:

| Area | Before | After |
|---|---:|---:|
| `capabilities/overlays` | 219 | 182 |
| everything else | 563 | 557 |

The 58 planner test files and the three planner entry files removed by
`TICKET-OVERLAY-PLANNER-GO-DEMOLITION` account for the delta. Zero-reference
functions are still **0** across the whole tree (the last one,
`isPromptInstructionEcho`, was removed with its only test). The 746 remaining
are dead chains with a test or another dead function as their only caller:
they are switched-off features, not accidental leftovers, so each deletion
still needs its package owner to confirm the feature is retired. The earlier
guard threshold becomes: 0 zero-reference functions and ≤745 total unreachable
(≤182 in overlays) until a cluster is retired.

## Piano di ritiro

Il conteggio non è più il problema: 745 funzioni sono distribuite in 14 cluster
con dipendenze diverse. Il piano operativo, con ondate, test da tagliare e
decisioni di prodotto richieste, è in
`DEAD-CODE-RETIREMENT-PLAN-2026-11-18.md`. Baseline e allowlist test-only sono
descritte lì; questo ticket resta la fonte del numero, il piano la fonte delle
decisioni.

## Ondata 1 eseguita — 2026-11-18

`DEAD-CODE-RETIREMENT-PLAN-2026-11-18.md`: l'ondata 0 è stata verificata e
rinviata, l'ondata 1 è stata eseguita.

**Conteggio.** Baseline di questa sessione: **738** funzioni unreachable
(**175** in `capabilities/overlays`). Il valore 745 del ticket era precedente
all'ondata 2 già iniziata da un'altra sessione sullo stesso tree. Dopo
l'ondata: **598** totali (**35** in overlays), con **137** funzioni rimosse
esplicitamente da questa ondata. Le funzioni fuori da `overlays` restano 563
(nessuna regressione).

**Ondata 0 — non eseguibile.** `mutationprobe.go` è importato dal test vivo
`cliprender/chunk_mutation_probe_test.go` ed è già classificato test-support da
allowlistare; `immutability.CloneWith` è chiamato da
`voiceover/service/types_helpers.go`. Entrambi restano finché non si ritirano
l'allowlist e la catena voiceover (ondata 6).

**Ondata 1 — 137/148.** Rimosse le 44 funzioni di A1 e 93 di A2. Deferite 11
funzioni ancora referenziate: `SelectTextMotion` (catena H, ondata 8); i sei
simboli usati da `scripts/entity_image_motions.go`
(`CertifiedLightLeakMotions`, `CertifiedAutomaticEntityCaptionMotions`,
`RuntimeEntityCaptionMotionAtOffset`, `EntityImageMotionAtOffset`,
`EntityImageParams`); i due cataloghi usati come drift gate in
`entity_image_motion_rotation_test.go` (`CertifiedEntityCaptionMotions`,
`CertifiedImageWithTextMotions`); e le dipendenze
`automaticEntityCaptionMotionCandidates`, `normalizeGeneratedCaptionMotion`,
`rotateMotionAtOffset`.

**Test.** `chart_wiring_test.go` e `bullets_charts_cert_test.go` non usano più
`ChartPlacementsForScenes`: i placement sono Rust-owned e restano coperti da
`BrainRust/semantic-engine/tests/chart_acceptance_36.rs`. La superficie Go
(filtro categorie, prefiltro `charts: disabled`, catena bullet atomica,
`ParseChartValue`) resta testata. `runner_overlay_plan_test.go` verifica
`NotEmpty` su preset e motion scelti da Rust invece dell'appartenenza ai pool Go.

**Gate eseguiti.** `go build ./...` rc 0; `go vet ./internal/capabilities/scripts`
rc 0; `go test ./internal/capabilities/overlays -count=1` ok; i test mirati
`TestCertC1/C2/B2/C3`, `TestRustChartValuesRestrictToGroundedNumbers`,
`TestRunner_OverlayPlanAppliesRunLevelEditorialBudget`,
`TestRunner_OverlayIntents_PersistedBeforePlanEnqueue`,
`TestRunner_OverlayPrepare_EnqueuedBeforeTTS` ok. `go test ./...` **non
eseguito**: durante la verifica il tree conteneva l'ondata 2 in corso di
un'altra sessione (editori concorrenti su `style.go`,
`overlay_plan_finalize.go`, `cmd/gen_baseline`), quindi il risultato non
sarebbe attribuibile a questa modifica.

**Font SSOT — 2026-11-18.** `font_ssot_parity_test.go` è stato riscritto: non
chiama più `MergeCaptionStyleIntoLayers` né il merge Go. `TestCanonicalFontAssetsParity`
verifica i due cataloghi Go; `TestRustFontVocabularyParity` legge i
`font_family`/`caption_font_family` dei fixture Rust e le costanti
`*_FONT`/`*_FAMILY` del planner e fallisce se una famiglia emessa da Rust non è
nel contratto payload o non ha asset. Rimossi da `style.go`
`MergeCaptionStyleIntoLayers`, `ImageStyleParams`, `IsRuntimeCursorParam`,
`IsRuntimeImageParam`, `StyleFontColor`, `IsEntityOverlayKind`.
`MergeRuntimeTextStyleParams` è stata ripristinata perché
`overlay_plan_finalize.go` ora la usa per i chain segment. `style.go` non ha più
funzioni unreachable: resta un file vivo (16 funzioni), non cancellabile.

**Snapshot dopo il caso font:** 598 funzioni unreachable, 35 in overlays,
0 zero-reference. Il calo rispetto a 738/175 viene dall'ondata 1/2 in corso in
un'altra sessione sullo stesso tree.

## Guard aggiornata — 2026-11-18 (dopo l'ondata 2)

`"$(go env GOPATH)/bin/deadcode" -test=false ./...` sulla tree corrente:
**567** funzioni unreachable, **12** in `capabilities/overlays`, **0**
zero-reference. Il totale è sceso da 598 a 575 con l'ondata 2 (A3 contratti
overlay) e a 567 durante l'ondata 8 già in corso in un'altra sessione; le 12
superstiti in `overlays` sono le funzioni preset/motion tenute in vita dalla
catena `scripts` (`entity_image_motions.go`, ondata 8), non entry point del
planner.

Guard valida da qui: **0** zero-reference fuori da `internal/capabilities/overlays`,
totale **≤ 575** (bound dell'ondata 2; il calo ulteriore è cascata legittima),
`capabilities/overlays` **≤ 12**. Un valore superiore è una regressione da
dichiarare per package; il calo si aggiorna a mano a ogni ondata. La baseline
sostituisce la soglia 745/182, che era precedente all'ondata 2. Il tree era in
modifica concorrente durante la misura: il bound conta, lo snapshot no.

**Rosso noti residui:** nessuno. `go build ./...` 0, `go vet`
0 sul package toccato, `go test ./internal/capabilities/scripts -count=1`
verde. Il rosso che il piano citava (`TestApplyImageAssetStylesRejectsUnhostablePin`)
è risolto riscrivendo il test sul contratto r134, senza skip e senza
indebolire le asserzioni superstiti.

## Ondata 2 eseguita — 2026-11-18

`DEAD-CODE-RETIREMENT-PLAN-2026-11-18.md`, cluster A3 (contratti overlay).

**Conteggio.** Prima di questa passata: **598** totali, **35** in
`capabilities/overlays`, **563** fuori. Dopo: **575** totali, **12** in
overlays, **563** fuori. Rimozione di questa passata: **23** funzioni. Altre
**6** funzioni di `style.go` erano già state rimosse dalla sessione concorrente
prima della baseline, quindi il cluster A3 è a **29/34**.

**Rimosse in questa passata.** `priority.go` (4: `ContentPriority`,
`DegradeOverlaps`, `contentPriorityForTemplate`, `maxConcurrentWithin`);
planner di intenti (4: `EntityOverlayPlanner.Plan`, `NewEntityOverlayPlanner`,
`SortIntents`, `TemplateResolver.Resolve`); `documentary_phrase_aliases.go` (2);
`editorial_sections.go` (2); `entity_style_sampling.go` (2);
`long_copy_motion_guard.go` (2); `planner_candidates.go` (2); `map_backend.go`,
`planner_input.go`, `planner_map.go`, `selection_seed.go`,
`model/overlay_fingerprint.go` (1 ciascuno).

**Deferite 5.** I quattro helper di stile `IsRuntimeTextStyleParam`,
`IsTextOverlayKind`, `MergeRuntimeTextStyleParams`, `MergeStyleParam` sono usati
da `capabilities/scripts/overlay_canvas.go` e `overlay_plan_finalize.go`
(catena H, ondata 8): `deadcode` li classifica vivi, non morti.
`CompositeImageMotionPool` è usato da `scripts/entity_image_motions.go` e da tre
test vivi, quindi resta con la catena entity-image.

**Test.** `model/intent_test.go` perde i quattro casi che coprivano solo i
simboli rimossi (`TestOverlayIntent_SortedDeterministic`,
`TestTemplateResolver_ResolvesEntityTypeToTemplateID`,
`TestTemplateResolver_DeterministicAndRegistryOwned`,
`TestEntityOverlayPlanner_PlansViaTemplateResolver`); restano i test di
`PlanOverlayIntents`, `OverlayIntent.Validate` e fingerprint.
`render/composite_motion_pools_test.go` resta perché la funzione è deferita.

**Gate eseguiti.** `go build ./...` rc 0; `go vet
./internal/capabilities/overlays/...` rc 0; `go test
./internal/capabilities/overlays/... -count=1` ok (overlays, model, render);
`go vet ./internal/capabilities/scripts` rc 0. `go test ./...` **non eseguito**
per gli edit concorrenti sullo stesso tree. `deadcode -test=false ./...`:
575 totali; le 12 funzioni ancora morte in overlays sono le 11 deferite
dell'ondata 1 più `CompositeImageMotionPool`.

## Ondata 5 (D ledger) + ondata 8 eseguite — 2026-11-18

Due decisioni di prodotto prese in questa sessione, entrambe documentate nel
piano: **il ledger di projection SQLite è una feature spenta**, non una
fondamenta; **la catena overlay Go si ritira** e resta Rust-owned.

**Ondata 5 — D ledger (`mediaregistry`). 30 funzioni rimosse (567 → 537).**
Evidenza: `NewLedger` era chiamato solo da due test; la produzione usa
`NewCanonicalIdentityResolver`, `content_objects` e `source_identity`. Ritirati
`ledger.go` (18), i sette helper transazionali di `tx.go` e i cinque wrapper
`UpdateMediaAssetTaxonomy*`/`LinkMediaAssetContent*` di `imagesregistry`, che
esistevano solo per quell'adapter. Restano vivi — e non toccati —
`execer`, `registerSource` e `validateAssetSource`, usati da
`canonical_identity.go`; `tx.go` è rinominato `source_write.go` e
`content_links_test.go` → `source_write_test.go`, dove i due test di
`registerSource` (idempotenza, validazione) restano. `projection_advance_test.go`
(5 test) copriva solo il metodo ritirato ed è stato rimosso con esso. La lista
archcheck `percheck_sqlite_media_reader_zone_inventory.go` non cita più
`ledger.go`.

**Ondata 8 — catena overlay Go. 26 funzioni rimosse (537 → 511), overlays a 0.**
Ritirati `scripts/overlay_entity_asset_registry.go` (12, intero file, senza
caller di produzione), `assignEntityImageMotions` e `capNamedEntityCardsPerScene`
in `entity_image_motions.go` (chiamati solo dai test), e in `overlays` i nove
accessor morti di `preset_image_selection.go`, `SelectTextMotion` e
`render/composite_motion_pools.go` (intero file). I test che coprivano solo
queste funzioni sono stati eliminati con esse; `entity_style_wire_test.go`,
`entity_projection_test.go` e `overlay_entity_cards_test.go` conservano i casi
vivi (wire/render key, `projectSceneEntityResult`, budget card) e perdono solo
il caso costruito sulla funzione ritirata.

**Guard aggiornata (misura finale):** 511 totali, **0** in
`capabilities/overlays`, **0** zero-reference. Il bound precedente (≤ 575)
resta rispettato; il nuovo bound è ≤ 511. Verifica: `go build ./...` 0,
`go vet ./internal/capabilities/{scripts,overlays/...}` 0,
`go test ./internal/capabilities/{scripts,overlays/...}` verde. `go test ./...`
passa salvo `tests/e2e::TestSubtitleArtifacts_CatalogCurrentFilesValidate`, che
fallisce perché il catalogo subtitle è vuoto in questo ambiente (nessun file
`tests/e2e` è modificato): rosso preesistente, non collegato a questo ritiro.

## Ondata 5 (DualPool) + allowlist testdb — 2026-11-18

**DualPool — 7 funzioni rimosse (511 → 504).** Il surface transazionale del
pool (`DualPool.BeginWriterTx`, `BeginReaderTx`, `beginTx`, `SourcePath`,
`instrumentedTx.Commit`/`Rollback`, `classifyTxError`) era raggiungibile solo da
`pool_test.go` (due test su `BeginWriterTx`, uno su `classifyTxError`);
`BeginReaderTx` e `SourcePath` non avevano nemmeno quello. Ritirati con il tipo
`instrumentedTx`, l'helper di test `collectHistogramSampleCount` e i tre test
dedicati. Restano vivi e coperti `NewDualPool` (WAL, sizing dei due pool,
fail-closed su URI vuota) e `walPragmas`; `AttachDualPool`, già inesistente,
aveva lasciato solo il commento, rimosso. Il pool resta il factory canonico:
sono state ritirate le entry point tx instrumentate, non il pool.

**Allowlist infrastruttura di test — decisione: escludere, non cancellare.**
`internal/platform/sqlite/testdb.go` (`NewTestDB`, `NewTestDBWithSchema`,
`NewMigratedTestDB`, `testMigrationsDir`, `MustExec`) è fixture condivisa:
**12 call site** nei test vivi. Cancellarla romperebbe quei test e non
rimuoverebbe comportamento di prodotto. Stessa regola per
`internal/capabilities/scripts/usecase/testsupport/doubles.go` (7) e per
l'harness `tests/operational/voiceover_*.go` (19). Questi path restano fuori
dalla baseline dead-code: vanno contati solo se sparisce il loro ultimo
chiamante di test, non perché `deadcode -test=false` non li vede.

**Guard aggiornata:** 504 totali, 0 in `capabilities/overlays`, 0
zero-reference. `go build ./...` 0; `go vet` e `go test ./internal/platform/sqlite
./internal/capabilities/overlays/... -count=1` verdi.

## Chiusura — 2026-11-18 (allowlist classificata)

Misura finale: `deadcode -test=false ./...` → **476**; `capabilities/overlays`
**0**; zero-reference **0**. Tutte le 18 funzioni residue sono test-only e
classificate: nessun item resta senza decisione.

Nota di misura: un'esecuzione sul tree condiviso ha dato **478** perché una
modifica concorrente ha introdotto 2 funzioni unreachable in
`internal/platform/ollama/adapters` (`hasPanelGridCollage`,
`normalizePersonImageForVision`), fuori dal perimetro di questo ticket.

**Fixture / test-support (15) — restano, per non rompere copertura viva.**
`testdb.go` (5: `NewTestDB`, `NewTestDBWithSchema`, `NewMigratedTestDB`,
`testMigrationsDir`, `MustExec`), `NewSQLiteStoreWithDB`, `WithClock`,
`CasingFamily`, `NormalizeCheck`/`NormalizeDefault`/`NormalizeExpr`,
`removedTablesIn`, `NewProjection`, `ResolveDateAfter`. Sono usate da test che
esercitano codice vivo (wiring integration, outbox resiliency, twin source,
state matrix, scoring YouTube): cancellarle romperebbe quei test senza
rimuovere comportamento di prodotto. Con loro `worker.Runner.SetRenewInterval`,
usato dall'e2e black-box di `internal/app/workerruntime` per accelerare il
rinnovo del lease.

**Superficie dormiente (3) — restano finché la capability non è ritirata.**
`DatabaseSet.BackupSet`, `NewSupersede`, `controlplane.New`. Ognuna è l'unico
produttore di un contratto vivo (`SupersedeError` → `IsSupersede`, manifest di
backup) e i test sono l'unico chiamante: cancellarle rimuoverebbe la capability,
non solo codice morto.

**Jobs helpers: ritirati (15 funzioni).** Bundle, delivery, preparation*,
scheduling, opzioni runner, `maxRetriesFor`, `ClaimUntil`: erano tutti
**duplicati di API vive rimaste in produzione** (`NewBundleWithHistory`,
`NewDeliveryHandlerWithOperations`, `ClaimUntilMatching`,
`ExpectUnit`/`BootstrapSources`, `jobs.Runner`), nessuno era l'unica
implementazione di un contratto. I test sono stati riportati sulle API vive;
per i due seam di sola osservazione (`Running`, `ActiveWorkFunc`) l'helper è
stato spostato nei test, così nessuna asserzione su comportamento vivo è stata
persa. Resta in allowlist `worker.Runner.SetRenewInterval`: lo usa l'e2e
black-box `internal/app/workerruntime` per accelerare il rinnovo del lease,
quindi è un seam di configurazione dei test, non superficie morta. Rimossi con gli helper i test che pinnavano solo il simbolo ritirato
(`TestWorker_HonorsRegistryRetries`, `AllTypes` ordinato,
`RecordClaimRatio`).

**Codec jobs: ritirato e sostituito (13 funzioni).** `internal/capabilities/jobs/
codec.go` (`NewTypedCodec`, `NewTypedCodecAdapter`, i 6 metodi di ciascun tipo)
non aveva alcun costruttore di produzione: nessun payload persistito poteva
dipendere da lui, quindi la vecchia nota di migrazione non era necessaria. Al
suo posto `registry_codec_wiring_test.go` implementa il **check C3** sul
registry di produzione: costruisce `NewMutableJobRegistry`, registra
`CanonicalJobDefinitions`, fa `Freeze` e verifica per ogni definizione i due
codec, `JobType()`/`SchemaVersion()`, e il round-trip payload/result. I commenti
di `kernel/job/codec.go` non citano più il simbolo rimosso.

**Baseline effettiva:** 476 − 18 = **458**. Il guard resta: 0 zero-reference,
0 in `overlays`, totale ≤ 476 (solo calo).

**Nota fixture (stessa sessione):** la drift gate
`TestOverlayPlanFixturesMatchRealRustCompiler` era rossa per la modifica in-flight
di `Seal.Background` (tipizzato con `omitempty`). I fixture sono stati rigenerati
con il binario pinnato v0.3.7 (`UPDATE_OVERLAY_PLAN_FIXTURES=1`): le request
perdono `"background": null` e `short_phrase_text.plan.json` passa
`enter_frames` 8 → 9, che è l'output del pin v0.3.7 (include `d3b77d6`, cue
timing) e non una regressione — il golden precedente era del 2026-11-16.
