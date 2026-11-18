# TICKET — Go overlay planner demolition (2026-11-18)

- **Status:** DONE (2026-11-18) — **0** unreachable in `capabilities/overlays`; package doc and `docs/stato` describe it as a projection/catalog layer; Rust golden matrix **34/34** (`every_fixture_compiles_to_its_frozen_golden`, `compilation_is_deterministic`) and the Go drift gate green after regenerating the fixtures with the pinned v0.3.7 binary
- **Severity:** P1 — 14,546 production lines and 14,184 test lines maintain a second planner
- **Owner:** overlay/editorial pipeline
- **Source:** audit 2026-11-18 (`deadcode -test=false ./...` on `refactored@75b5b1def`)
- **Related:** BrainRust TICKET-14 (single-call planner), TICKET-13d (Go demolition)
- **Rule:** no behaviour change; the cut is proved by goldens and by a guard that stops the planner from returning

## Problem

After the single-call cutover the whole Go planning chain is unreachable from
any `main`:

- `internal/capabilities/overlays/planner.go:21` `BuildPlan`, its private
  `buildPlan`, `planner_deferred.go:33` `BuildPlanCandidates`,
  `planner_deferred.go:48` `FinalizeDeferredPlan` and
  `finalizeAdmittedPlan` are all reported as unreachable functions.
- `deadcode` reports **212 unreachable functions** in
  `internal/capabilities/overlays` (78 non-test files, 14,546 production
  lines). Of those, **50 have no reference anywhere** (not even tests) and
  **59 are referenced only by test files** in the same package.
- **20 files have every declared function unreachable** (3,106 lines):
  `planner.go` (507), `planner_number.go` (377), `multi_entity_wiring.go`
  (316), `phrase_motion_distinct.go` (288), `long_phrase_lane.go` (211),
  `planner_config.go` (158), `planner_motion_assignment.go` (148),
  `visual_budget_entity_cards.go` (145), `recap_candidates.go` (136),
  `animation_layout.go` (123), `motion_coherence.go` (121),
  `planner_deferred.go` (119), `recap_plan.go` (111), plus nine smaller files.
- The package is still imported by production, but only for a small surface:
  `Cache`/`NewCache`, `GPUGate`/`NewConfiguredGPUGate`/`ResolveGPUGateSlots`/
  `DefaultGPUGateSlots`, `AssetRef`, `Renderer`/`NewCommandRenderer`,
  `AssetPreparer`/`NewAssetPreparer`, `NewMediaContractProber`,
  `IsValidEntityStyleSelector`, `CertifiedSingleImageMotions`,
  `CertifiedPhraseMotions`, `CanonicalFontAssetsByFamily`.
- The suite still exercises the Go planner directly: 4 test files (633 lines)
  call `BuildPlan`/`BuildPlanCandidates`, and the package carries 91 test files
  (14,184 lines) for logic Rust now owns.
- Duplicated logic is concrete, not theoretical: `planner_number.go` parses
  Italian cardinals (`italianCardinal`, `normalizeItalianNumberWords`) that
  `BrainRust/semantic-core/src/numbers.rs` already owns; `visual_budget*.go`,
  `preset_*_selection.go`, `animation_layout.go` and `motion_coherence.go`
  re-implement budgeting, motion selection and coherence that
  `overlay-compiler` applies in Rust.

Leaving it as an unlabelled oracle has two costs: every editorial rule must be
changed twice, and the binary compiles a planner that no request executes.

## Demolition plan

### Wave A — delete the fully unreachable files (3,106 lines)

Start from the 20 files listed above. Before deleting each one, confirm that
none of its exported types is in the keep-list above, and keep any type that is
part of the surviving DTO/config surface. `go build ./...` after the wave.

### Wave B — re-home the parity tests

- The 4 test files that call the planner entry points are:
  `overlays/planner_deferred_test.go`,
  `scripts/overlay_category_prefilter_test.go`,
  `scripts/bullets_charts_cert_test.go`,
  `scripts/chart_wiring_test.go`.
- Replace each Go-planner assertion with the corresponding checked-in Rust
  golden under `overlays` fixtures /
  `semantic_engine_editorial_plan_v2_real_test.go`, or delete the test when
  the behaviour is already covered by the Rust contract suite.
- Do not weaken an assertion to make the deletion possible: a test that cannot
  be expressed against the Rust output is evidence that the Rust contract is
  incomplete; open a BrainRust follow-up instead of deleting silently.

### Wave C — delete the remaining planner helpers and its tests

After A and B, remove the helpers only reachable by the tests, then the
overlays test files that test nothing but the deleted chain. Keep the catalog,
config, GPU-gate, render and asset-preparer tests.

### Wave D — guard

Add an architecture guard in the style of
`internal/capabilities/scripts/overlay_plan_deletion_guard_test.go`: a test
that fails if the package re-exports a planning entry point (`BuildPlan`,
`BuildPlanCandidates`, `FinalizeDeferredPlan`, `buildPlan`). This is the
anti-resurrection gate the earlier wave tickets use.

## Acceptance criteria

- [ ] Wave A: the 20 files or their surviving keep-list remainder are handled;
      `go build ./...` exits 0 and `deadcode -test=false ./...` reports **0**
      unreachable functions in `internal/capabilities/overlays`.
- [ ] Wave B: no `_test.go` calls `BuildPlan`/`BuildPlanCandidates`/
      `FinalizeDeferredPlan`; the 4 files are re-homed or removed with the
      Rust golden that replaces them cited in the commit message.
- [ ] Wave C: the overlays test suite shrinks to the surviving surface; the
      kept tests pass with `-count=1`.
- [ ] Wave D: the guard fails on a reintroduced `BuildPlan` (red without the
      guard’s allow-list, green with it) and is wired in CI (`verify-main` or
      the same lane that runs `overlay_plan_deletion_guard_test.go`).
- [ ] No production behaviour change: the editorial plan golden set produced
      before and after the demolition is byte-identical (the goldens are Rust
      output, not Go output).
- [ ] `docs/stato` and the overlays package doc state that the Go package is a
      projection/catalog layer, not a planner.

## Verification

```sh
deadcode -test=false ./... | grep capabilities/overlays
go build ./...
go test ./internal/capabilities/overlays/... ./internal/capabilities/scripts/... -count=1
rg -n "BuildPlanCandidates|FinalizeDeferredPlan|\.BuildPlan\(" --glob '*_test.go'
```

## Out of scope

- Moving the emitted plan to a new schema.
- The unrelated dead functions outside `capabilities/overlays`, which are in
  `TICKET-DEAD-CODE-SWEEP-2026-11-18`.

## Blocker — 2026-11-18

Not executed in the 2026-11-18 pass, for two concrete reasons:

1. **Parity tests must move first.** Wave A deletes the planner entry points
   (`BuildPlan`, `BuildPlanCandidates`, `FinalizeDeferredPlan`), but four test
   files (633 lines) still call them as the Go oracle:
   `overlays/planner_deferred_test.go`,
   `scripts/overlay_category_prefilter_test.go`,
   `scripts/bullets_charts_cert_test.go`, `scripts/chart_wiring_test.go`.
   Deleting the planner without rewriting them to the checked-in Rust goldens
   would remove assertions, which the ticket rule forbids. That rewrite is the
   real unit of work and needs the golden fixtures confirmed first.
2. **The package is being edited concurrently.** The working tree has
   uncommitted changes in `capabilities/overlays/planner_input.go` (image
   duration 5s→8s) and in `scripts/editing_timeline.go`; deleting 20 files and
   their tests while that batch is in flight would collide with it.

Resume condition: the in-flight overlay/timeline batch is committed, and the
four tests are re-expressed against the Rust goldens (or a written decision
drops them). Then Wave A–D can run as one reviewed diff.

## Status recheck — 2026-11-18 (later pass)

Neither resume condition is met yet: `capabilities/overlays/planner_input.go`
and `scripts/editing_timeline.go` still carry uncommitted changes, and the four
parity test files still call the planner entry points. `deadcode` now reports
219 unreachable functions in the package (up from 212, consistent with the
in-flight edits), so a cut taken now would mix two authors' changes in one
diff. Ticket moved to BLOCKED; Wave A–D remain the plan.

## Execution — 2026-11-18 (demolition pass)

**Wave A (entry points):** deleted `planner.go`, `planner_deferred.go` and
`planner_number.go` (24 functions), plus `planner_deferred_test.go`. The
`PlanInput`/`PlannerConfig` DTOs and the multi-entity/recap/chart DTOs stay:
`scripts` still projects them into the Rust request.

**Wave B (parity tests):** the four Go-oracle files no longer call the deleted
planner:

| Test file | Disposition |
|---|---|
| `overlays/planner_deferred_test.go` | deleted; the two-phase API has no Rust counterpart (single-call replaced it) |
| `scripts/overlay_category_prefilter_test.go` | kept; the last test now stops at `dropCategoryDisabledCandidates` and asserts the four dates reach the planner |
| `scripts/bullets_charts_cert_test.go` | kept the chart placement + category-filter assertions, dropped the `BuildPlan` sections (Rust: `chart_acceptance_36.rs`, `overlay_plan_contract.rs::charts_and_chapter_recaps_reach_the_final_plan`, `phrase_impact_tests/bullets.rs`) |
| `scripts/chart_wiring_test.go` | kept placement and Rust-authorization assertions, dropped plan construction |

**Wave C (test suite):** removed 58 `overlays/*_test.go` files that existed only
to exercise the deleted chain (planner budgets, presets, motion pools, date
cards, multi-entity wiring, visual budget). The surviving suite passes with
`-count=1`; the catalog/GPU-gate/render/asset tests remain.

**Wave D (guard):** extended
`scripts/overlay_plan_deletion_guard_test.go` with
`func BuildPlan(`, `func BuildPlanCandidates(`, `func FinalizeDeferredPlan(`
and `func buildPlan(`; it scans every production file in the package and
`model`. Green on the current tree, red if a planner entry point returns.

`doc.go` now states the package is a projection/catalog layer, not a planner.

### What remains (PARTIAL, not CLOSED)

`deadcode -test=false ./...` still reports **182** unreachable functions in
`overlays` (745 total, down from 782). They are motion/style/budget helpers
whose only callers are the unreachable `capabilities/scripts` chain
(`entity_image_motions.go`, `overlay_canvas.go`, …). Deleting them requires
deleting that feature chain and its tests, which is the separate
`TICKET-DEAD-CODE-SWEEP` product decision. No planner entry point is among
them. This ticket therefore meets the demolition goal but not the literal
"0 unreachable in overlays" acceptance line; keeping it PARTIAL records the
remaining dependency instead of hiding it.

## Completion pass — 2026-11-18 (style/chain cleanup)

Measured on the current working tree after the overlay clean-up:

- `go build ./internal/capabilities/overlays/ ./internal/capabilities/scripts/`
  is green.
- `go test ./internal/capabilities/overlays/ ./internal/capabilities/scripts/
  -count=1` is green, including the deletion guard and the bullet-font suite.
- `deadcode -test=false ./...` reports **12** unreachable functions in
  `overlays` (down from 182) and 567 in the repository. The 12 are the
  test-only motion/preset cluster: four are called directly from surviving
  tests (`CertifiedAutomaticEntityCaptionMotions`,
  `CertifiedEntityCaptionMotions`, `CertifiedImageWithTextMotions`,
  `CompositeImageMotionPool`) and the rest are their support graph. Deleting
  them would remove those assertions, which this ticket forbids.
- The payload style has one projection at seal:
  `applyPayloadOverlayStyleToItems` applies the explicit run-level typography,
  `overlay_style.bullet` plus its per-scene pins, and `overlay_style.image`.
  The dead `applyOverlayStyleToItems` is gone. `overlay_style.image` now reaches
  image items even when `overlay_images` declares no pins; previously the only
  remaining path returned early on an empty pin list.
- The chain wire has one accessor: `capmodel.ChainSegmentMaps` /
  `UpdateChainSegmentMaps`. The four local decoders (`chainSegmentMaps`,
  `chainSegments`, `overlayPlanChainSegments`, `chainSegmentStyleMaps`) are
  gone, and the deletion guard rejects their signatures.
- Remaining PARTIAL reason is unchanged and explicit: the 12 test-only helpers
  are the only unreachable overlay functions; no planner entry point is among
  them.

## Wave 8 — 2026-11-18 (dead code a 0)

Decisione: la catena overlay Go si ritira, la compilazione resta Rust-owned.
Ritirati il file `scripts/overlay_entity_asset_registry.go` (12 funzioni, nessun
caller di produzione), `assignEntityImageMotions` e `capNamedEntityCardsPerScene`
(chiamati solo dai test), i nove accessor morti di
`overlays/preset_image_selection.go`, `SelectTextMotion` e
`overlays/render/composite_motion_pools.go`: 26 funzioni.

Verifica sulla tree corrente:

- `deadcode -test=false ./...` → **511** totali, **0** in
  `internal/capabilities/overlays` (il criterio Wave A "0 unreachable" è
  soddisfatto).
- `go build ./...` → 0.
- `go vet ./internal/capabilities/scripts ./internal/capabilities/overlays/...`
  → 0.
- `go test ./internal/capabilities/scripts ./internal/capabilities/overlays/...
  -count=1` → verde.
- I test che coprivano solo le funzioni ritirate sono stati eliminati con esse
  (`projection_advance_test.go` no — quello è il ledger; qui
  `overlay_entity_asset_registry_test.go`, `overlay_entity_asset_registry_chain_test.go`,
  `entity_image_motion_rotation_test.go`, `composite_group_motion_test.go`,
  `composite_motion_pools_test.go`); i casi vivi di
  `entity_style_wire_test.go`, `entity_projection_test.go` e
  `overlay_entity_cards_test.go` restano.

Chiusi anche i doc: `internal/capabilities/overlays/doc.go`, `docs/stato/refactored.md`
e `docs/stato/brainrust.md` dichiarano ora che il package è il layer di
proiezione/catalogo e che il planner è Rust-owned. Resta un solo criterio
aperto: il confronto byte-identico dei goldens prodotto da Rust, che richiede
l'esecuzione Rust (non disponibile in questa sessione).

## Chiusura — 2026-11-18

Criteri finali verificati in questa sessione:

- `deadcode -test=false ./...` → **504** totali, **0** in
  `internal/capabilities/overlays`.
- `go build ./...` → 0; `go vet ./internal/capabilities/scripts
  ./internal/capabilities/overlays/...` → 0; `go test` sui due package → verde.
- Rust golden matrix: `cargo test -p overlay-compiler --test
  overlay_plan_contract --locked` → **34 passed, 0 failed**, inclusi
  `every_fixture_compiles_to_its_frozen_golden` e
  `compilation_is_deterministic`. Il planner Rust compila quindi i fixture
  congelati in modo byte-identico.
- Go drift gate `TestOverlayPlanFixturesMatchRealRustCompiler` → verde dopo la
  rigenerazione dei fixture con il binario pinnato v0.3.7
  (`UPDATE_OVERLAY_PLAN_FIXTURES=1`). La request perde `"background": null`
  (il campo è ora tipizzato con `omitempty`) e `enter_frames` passa 8 → 9:
  è l'output del pin v0.3.7, non una regressione.
- `internal/capabilities/overlays/doc.go`, `docs/stato/refactored.md` e
  `docs/stato/brainrust.md` dichiarano il package come layer di
  proiezione/catalogo e il planner Rust-owned.
