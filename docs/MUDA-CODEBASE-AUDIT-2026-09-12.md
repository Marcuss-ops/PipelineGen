# Muda audit — codebase waste inventory (2026-09-12)

> **Dated snapshot — paths reflect the tree at time of writing (2026-09-12).**
> Retained as prior art for the open tickets that cite it; file:line references
> are historical and NOT maintained. Verify any path with `ls`/`git ls-files`
> before acting on it.

> **Scope:** the whole `refactored/` module (`internal/`, `pkg/`, `cmd/`, `make/`,
> `architecture/`, `docs/`), plus the three Rust crates under `rust/`.
> **Method:** measured, not asserted. Every finding below carries a
> file:line reference or a reproducible command. Categories are the seven
> Muda/toyota wastes mapped onto software: *dead code* (defects-in-waiting),
> *over-processing* (over-engineering), *inventory* (bloated state/test doubles),
> *motion* (redundant work), *waiting* (blocking/stalls), *transport*
> (redundant roundtrips), *over-production* (built for use cases that do not exist).
>
> **Prior art in this repo — do not duplicate:** `docs/PIPELINE-WASTE-AUDIT-2026-09-12.md`
> (clip.render hot path), `docs/CLIP-RENDER-DEMOLITION-2026-09-12.md` (clip.render
> demolition, 1 item demolished / 5 verified already gone), and
> `docs/PIPELINE-ORCHESTRATION-AUDIT-2026-09-12.md`. This audit is the
> **repo-wide** complement to those three; findings already closed there are not
> restated.

---

## 0. Baseline (measured)

| Metric | Value |
|---|---|
| Go files (`internal`, `pkg`, `cmd`) | 4 766 |
| Go LOC total | 883 014 |
| Go LOC production | 486 357 |
| Go LOC test | 396 657 (45 % of all Go) |
| Interfaces declared (production) | 865 |
| — of which single-method | 564 (65 %) |
| Make targets (unique names) | 236 across 18 include files |
| archcheck perchecks / gates | 56 / 5 |
| Declared HTTP routes (`architecture/routes.yaml`) | 120 |
| Rust crates | 3 (all referenced from Go, all built by `make`) |
| Docs markdown | 47, of which 3 tickets |
| `TODO` / `FIXME` / `XXX` / `Deprecated:` markers | 59 / 2 / 4 / 26 |
| Commented-out-code-looking lines | ~1 257 |

**Headline:** this is not a codebase with accidental dead wood. It is a
deliberately gated hexagonal architecture whose waste is concentrated in
**one orphaned subsystem, five orphaned packages, and a set of duplicated
leaf helpers**. The expensive, cross-cutting wins are all in *deletion*, not
refactoring.

---

## 1. What is already gated (do not re-report these as findings)

These classes of waste are already mechanically prevented, so an audit that
"finds" them is finding noise:

- **Obsolete endpoints** — `architecture/routes.yaml` (120 routes) plus
  `cmd/archcheck/gates/gate_c2_route_manifest_main.go` enforce code↔manifest
  parity. A route cannot silently rot.
- **Unreferenced dependencies** — `go mod tidy -diff` is empty; the module
  graph is exact.
- **Secret leakage** — `cookies.txt` and `credentials.json` exist on disk at the
  repo root but are **untracked** (`git ls-files` empty) and covered by
  `.gitignore` (lines 32, 167). `make verify-no-secrets` gates this class.
- **Unbounded carry-forward debt** — `architecture/policy.yaml::debt_budget`
  caps the weighted `PRE-EXISTING-*` score; procedure in
  `docs/operations/debt-budget.md`. Current `architecture/issues.yaml` carries
  **0** `PRE-EXISTING-*` entries — the budget is clean.
- **`time.Sleep` in hot paths** — zero production call sites. Every hit is a
  comment documenting a *removed* sleep (`internal/capabilities/jobs/worker/runner_lease.go:61-65`,
  `pkg/retry/clock.go:77,179`) or a test helper.

Reporting these would inflate the audit without pointing at real work.

---

## 2. Dead & unreachable code

All entries below were verified by resolving the *production* importer set
(importers excluding the package's own directory and excluding `_test.go`).
A package with zero production importers is unreachable from any binary.

| ID | Item | Prod LOC | Severity | Effort | Evidence |
|---|---|---|---|---|---|
| **D1** | `internal/app/wiring/chronon/` — entire package | **861** (＋444 test) | **High** | S | Only importer is `internal/app/wiring/render_attempt_analytics_wiring_test.go:17`. `NewChrononNativeCertifier` called only from its own test. `ReadChrononMeasuredPhases` called only from `chronon_timing_projection_test.go`. `WireChrononMetricsAdapter` duplicates the **live** path `internal/app/wiring/rendering/metrics.go:14 → cliprender.NewChrononMetricsAdapter`. `chronon_wire.go` even documents that its former plan projector "was deleted", leaving only the certification probe — which itself is never wired. |
| **D2** | `internal/platform/images/pexels/` — superseded provider | **263** | **High** | S | Only importer is `internal/capabilities/imagesearch/live_relevance_e2e_test.go:57`. The live "pexels" provider is `internal/platform/artlist/fallback` (see `internal/app/wiring/build_bundles_artlist_providers.go:147` and `build_provider_catalog.go:38`). Two independent Pexels clients were built; one shipped. **RESOLVED 2026-09-19** — package deleted; the e2e harness now drives the shipped `artlist/fallback` Pexels client (no more native-image leg, no duplicate live calls). |
| **D3** | `internal/platform/sqlite/checkpoint/` — stub store | 100 | Medium | S | Declares `ErrNotWired = errors.New("checkpoint sqlite adapter: not wired")` (`store.go:17`). Only importer is `tests/e2e/replay_resume_e2e_test.go`. `checkpoint.New` never called in production. **Reclassified 2026-09-19 — NOT a stub, NOT fat.** The `ErrNotWired` sentinel only fires on a nil `*sql.DB` (`New`); the adapter over a real DB is a complete, tested durable store. It is an *unfinished wiring*: `Runner.SetCheckpointResolver` (`runner_deps.go:382`) has no production caller, and the only `capcheckpoint.ArtifactVerifier` (`cas.ArtifactVerifier`) verifies **CAS** objects while the audio checkpoint records a media/Drive artifact (`finalAudio.FinalAudioSHA256` + `finalAudio.DriveLink`, `runner_phase_audio.go:288-289`). A naive wire would fail verification on every run and never resume. Wiring required an artifact verifier matching the audio artifact's real store — **RESOLVED 2026-09-19 by WIRING it** with the media-SSOT verifier; no deletion. See §2.1. |
| **D4** | `internal/platform/sqlite/replay/` — stub store | 107 | Medium | S | Same shape: `ErrNotWired` (`store.go:18`), only importer is the same e2e test. **Reclassified 2026-09-19 — NOT a stub, NOT fat.** nil-DB guard only; the adapter is a complete durable store. Unwired end-to-end: there is **no production producer** (`BundleStore.Save` has no non-test caller) **and no consumer** (`JobsHandler.SetReplay` has no non-test caller, no `capreplay.Dispatcher` implementation), so `POST /api/jobs/:id/replay` returns 503 today. Wiring = producer (save the bundle after a render) + dispatcher + strategy resolver + the CAS staging asset source — **still OPEN 2026-09-19: no production `capreplay.Dispatcher` implementation exists, so this is an unwired feature, not a deletion.** See §2.1. |
| **D5** | `internal/capabilities/assets/providers/stock/stockpipeline/reconcile/` | 78 | Medium | S | Only importer is `reconcile_boundary_test.go:12`. |
| **D6** | Doc-only packages with zero importers | ~20 | Low | S | `internal/kernel/errors/` contains **only** `doc.go`; `internal/platform/` contains **only** `doc.go`. |
| **D7** | Orphan make target `verify-vidrush-dry` | — | Low | XS | Declared at `make/operations.smoke.mk:37`; zero references from any other target, doc, `AGENTS.md` or `README.md`. |
| **D8** | Stale documentation pointing at deleted files | — | Medium | XS | `architecture/observability-measurement-matrix.yaml:1082` names `internal/app/wiring/chronon_clip_renderer.go` as the "write path". **That file does not exist.** It is the missing half of D1 — the wiring that would have made the orphan subsystem reachable was itself removed, and the matrix was never updated. |

**Subtotal: ~1 429 production LOC unreachable**, plus the stale matrix row.
Nothing in that set is reachable from `cmd/server` or `cmd/worker`.

### 2.1 Resolution log (2026-09-19)

Verified against the current tree by resolving the **production** caller set
(importers excluding the package directory and excluding `_test.go`), the same
method this audit used.

| Item | Outcome | Evidence |
|---|---|---|
| **D1** | **Deleted** (2026-09-18) | Package gone; `render_attempt_analytics_wiring_test.go` now covers the live `wireRenderAttemptRecorder` only. |
| **D2** | **Deleted** (2026-09-19) | `internal/platform/images/pexels/` removed; the sole importer (`live_relevance_e2e_test.go`) retargeted at the shipped `artlist/fallback` Pexels client. |
| **D3** | **WIRED, not deleted** (2026-09-19) | The store was never a stub — `ErrNotWired` only guards a nil `*sql.DB`. `BuildScriptGenerationRuntime` now builds `sqlitecheckpoint.New(root.DB.DB)` and calls `runner.SetCheckpointResolver(capcheckpoint.NewResolver(store, verifier))`. The verifier is a NEW media-SSOT adapter (`internal/platform/postgres/media/checkpoint_artifact_verifier.go`), because the audio checkpoint records the *published voiceover artifact* (`finalAudio.FinalAudioSHA256`, `runner_phase_audio.go:288`), whose durable home is `media_assets` — the CAS verifier would have answered "missing" on every run and silently disabled resume. The adapter is derived from the committer's own engine and is fail-closed: a miss is a definitive staleness → re-render. |
| **D4** | **OPEN — NOT reachable as a bounded change** (2026-09-19) | The store is complete (`sqlite/replay`) and the e2e durability test certifies it. Three independent gaps block a correct wiring, and only the third is decisive: (1) no **producer** — `BundleStore.Save` has NO non-test caller; (2) no **`capreplay.Dispatcher`** — no production implementation exists, only the e2e/handler test doubles; (3) **no job that can carry the bundle's payload** — the replay bundle embeds `render.RenderPlan`, and the only place a sealed `render.RenderPlan` exists in production is the localized-render pipeline, which runs **in-process** (`LocalizedRenderEnqueuer.EnqueueLocalizedRender` → the localization service compiles and executes the plan in the same call, `internal/app/wiring/localized_render_enqueuer.go`), NOT as a queued job. So `Dispatch` has no existing seam to reuse: wiring replay means designing a NEW job type + payload schema + handler and registering it, plus the post-render bundle save and an environment source. That is a feature with product semantics (and its own payload-schema/ownership gates), not a cleanup — left open deliberately rather than guessed. |
| **D5** | **Deleted** (2026-09-18) | `stockpipeline/reconcile/` removed; contracts pinned in the parent package. |

**D1b — Chronon metrics projection: WIRED (2026-09-19).** The previous
verification found `rendering.NewChrononMetricsAdapter`,
`cliprender.ParseChrononSidecar` and `cliprender.ChrononMetricsAdapter` reachable
only from tests: the production outcome carried the sidecar *reference*
(`Outcome.ChrononTimingStorageKey/URL/SHA256`, `cliprender/ports.go:143-146`) but
nothing fetched the bytes, so the matrix row claiming `DONE/MET` was false. Now
wired end to end: `cliprender.Worker.publishChrononTiming`
(`chronon_timing_projection.go`, called from
`worker_completion.go::completeRendered`) fetches through the new
`renderinggen.ChrononTimingFetcher` (object store `/objects/<key>`, re-hashed
against the content address), parses once, and publishes through the
`OperationReportProjectionRecorder` seam. Bound in
`registerClipRender`; a half-wiring leaves the projection off. Best-effort by
construction — it can never fail a render.

**D2 follow-up — the media-type lie, corrected (2026-09-19).**
`build_provider_catalog.go` registered the `pexels` and `pixabay` providers with
`MediaType: "image"` while wiring the **video** searchers (`fallback.Pexels` /
`fallback.Pixabay` → `/v1/videos/search`, returning clip-typed assets). The
policy table is now `providerCatalogPolicies()` with `video` for all three
entries, pinned by `TestProviderCatalogPoliciesDeclareTheServedSurface` so the
declaration cannot drift from the wired surface again. Note the residual
question this exposes: there is currently **no** live Pexels/Pixabay *image*
surface in production — just the video clients. The declaration is now
cross-checked against real adapter output, not just a literal:
`TestProviderCatalogPoliciesMatchTheShippedAdapterSurface` drives the shipped
`fallback.Pexels` against a canned `/v1/videos/search` response and derives the
expected policy vocabulary from the returned `ProviderAsset.MediaType`. With
`"image"` reintroduced the test fails with
`policy declares media type "image" for pexels, but the wired adapter returns "clip" assets (policy vocabulary "video")`
— verified by temporarily reverting the fix.

### 2.2 Verification log (2026-09-19)

| Check | Result |
|---|---|
| `platform/postgres/media` checkpoint artifact verifier vs **real PostgreSQL** (`TEST_POSTGRES_DSN` → `pipelinegen-postgres-test`) | **PASS** — registered digest → `Exists+SHA256Matches`; unknown digest → definitive miss (no error); empty digest → no availability |
| `tests/e2e` `TestE2E_RenderCrashResumeRestartReplay` (the durable checkpoint/resume + replay contract the wiring now uses) | **PASS** |
| Media-type regression pin (bug reintroduced on purpose) | **FAILS as intended**, then restored |
| `go build ./...` / `go vet ./...` / `cmd/archcheck --strict` / ownership `--dry-run` | **PASS** (`passed: true`, 0 violations) |

The DSN-gated suite is NOT hermetic by design: without `TEST_POSTGRES_DSN` it
skips rather than faking availability. CI must therefore export the DSN
(`make test-postgres`) for the first row above to be meaningful — a green
`go test` without it means "skipped", not "verified".

**Marker debt (lower severity, high count):**
- 59 `TODO`, 2 `FIXME`, 4 `XXX`, 26 `Deprecated:` across production Go.
- ~1 257 lines matching `^\s*//\s*(func|if|for|return|err :=|var|const)`. This
  heuristic over-counts (doc comments legitimately start that way), so treat it
  as a *sampling* signal: the honest sub-population is whatever survives
  `rg '^\s*//\s*(if|for|return|err :=)' -g '!*_test.go'` after manual review.

### 2.3 Importer-scan kills (2026-09-19)

Method: enumerate every package under `internal/` + `cmd/`, then count importers
in **non-test** `.go` files only. Two packages had zero production importers and
were deleted; a third turned out to hold a *dead guard* rather than dead code.

| ID | Item | Size | Evidence | Verdict |
|---|---|---|---|---|
| **D9** | `internal/platform/images/chrome` (+ `visual_validate`) | 15 files, **3 579 LOC** | Retired concrete Chrome/Playwright image-generation infrastructure. `NewChromeImageProvider` / `NewChromeImageProviderPoolFromProfile` have **no non-test construction site**; the only non-test text naming the package is the retirement guard `TestBuildImagesServiceDoesNotWireChromeImageGeneration` (which asserts the composition root leaves `ImagesGenAIDeps.ImageGen` nil); the `chrome-pool-prewarm` prose left in `wire_services.go` / `wire_services_orchestration.go` / `wire_services_startup_plan.go` is stale — no Go symbol remains. | **DELETED** |
| **D10** | `internal/platform/sqlite/catalog` | 6 files, **365 LOC** | **Zero** importers anywhere, tests included. The directory contains **no `_test.go` at all**, so the earlier claim that it "still carries its own package tests" was stale; `NewRepository` and every `CatalogRecord` type have zero references; its last consumer (`SourceCatalog` via `wiring.searchCatalogAdapter` / `RepoBundle.CatalogRepo`) was removed in the seventh pass. It was exempt from `percheck_sqlite_media_reader_ban` only by the `internal/platform/sqlite` zone prefix, so no debt-register entry had to be removed. | **DELETED** |
| **D11** | `internal/capabilities/images/architecture_boundary_test.go` | 1 test | The guard forbade imports of `internal/infrastructure/images/chrome`, a path that **does not exist in this tree**, so it had silently stopped guarding anything. It now matches `/images/chrome`, covering both spellings. | **REPAIRED** |

**KEPT deliberately — the capability-side seam is LIVE and fail-closed.**
`images/generation` builds `NewDefaultRegistry(log, deps.GenAI.ImageGen)` with a
nil `ImageGenerator` in production, and `GoogleSlidesProvider`'s `Healthy`,
`Generate` and `TriggerPrewarm` each return early / a typed error on a nil
delegate. That was read, not assumed: there is **no** nil-delegate panic on the
production path, so the seam is not fat.

**D4 — fifth independent proof (still OPEN, by decision).** No registered job
type can carry the replay bundle payload. The complete job-type registry (the
families in `kernel/job/canonical_definitions.go` plus every `Type*` constant
declared under `internal/capabilities`) contains **no plan-carrying type**:
`clip.render` carries a `RenderRequest` and compiles a `ClipRenderPlanV1`
**inside** the worker, which is a different type from the `render.RenderPlan`
the bundle embeds. A production `Dispatcher` therefore requires a NEW job type
+ payload codec + handler + registration — a product decision, not a deletion.

| **D12** | dead AI-image-generation config knobs | 3 fields | `ConcurrencyConfig` carried `MaxConcurrentNvidiaGenerations`,
`MaxConcurrentGoogleSlidesGenerations` and `GoogleSlidesProfileID`, and **no production statement read any of them** (the Nvidia lane has no implementation at all; the Chrome/Google-Slides pool is the package deleted above, D9). Removed from `types.go` and from both example configurations. | **DELETED** |

The retirement is pinned by `TestConfig_RetiredAIImageGenerationKnobsAreGone`
(`internal/platform/config/config_test.go`, proven non-vacuous by re-adding a key
to an example and watching it fail): the two example configurations must not
list the keys, **and** a config that still carries them must LOAD rather than
fail closed. That second half is deliberate and is the opposite of the retired
`storage.primary_db_path`, which the loader rejects loudly: operators keep a
git-ignored `config.yaml`, and ignoring an inert concurrency limit has no
failure mode, whereas ignoring a database path would silently repoint the
process at a different database.

Net of 2.3: **-23 files, -4 755 lines** in the deleted packages, plus the three
retired config fields and their six example-configuration lines, with the full
`go test ./...` sweep, `go vet ./...`, `cmd/archcheck --strict`, ownership
`--dry-run` and `verify-component-coverage` all green afterwards.

---

## 3. Over-engineering & over-production (YAGNI)

| ID | Item | Severity | Effort | Evidence |
|---|---|---|---|---|
| **O1** | **The certification subsystem is over-production.** `chronon_native_certifier.go` (449 lines) builds a full host-readiness probe — GPU identity caching, environment fingerprinting, real 1-second NVDEC→CUDA/Vulkan→NVENC certification clip, auto re-certification on binary/driver change — for a capability (`ChrononNativeCertified`) that is consulted by **zero production call sites**. The string `ChrononNativeCertified` appears only inside that file's own comments. | **High** | S (delete) / M (wire) | `grep -rn "ChrononNativeCertified"` → 3 hits, all in `chronon_native_certifier.go` comments |
| **O2** | **Test-double mass.** `internal/platform/sqlite/assets/imagesregistry/testsupport/` is 1 724 LOC; the single file `sqlite_asset_committer_testdouble.go` is **983 lines** — the largest non-`_test.go`-suffixed file in the repository. Every interface change propagates here. | Medium | M | `wc -l` over `find internal pkg cmd -name '*.go' ! -name '*_test.go'` |
| **O3** | **Verify-surface sprawl.** 236 make targets across 18 include files, including 12+ overlapping verification tiers (`verify-base`, `-foundation`, `-static`, `-fast`, `-dev`, `-agent`, `-changed`, `-changed-components`, `-components`, `-main`, `-full`, `-push`, `-race`, `-race-components`, `-unit-race`, `-clean-checkout-build`, `-split`). Tier proliferation makes "which gate do I run?" an oral tradition rather than a documented contract. | Medium | M | `Makefile:31,33` aggregates 40+ targets into the `verify` phony |
| **O4** | **Interface surface.** 865 production interfaces, 564 of them single-method. Interface-per-port is the deliberate house style (hexagonal, per `CONTRIBUTING.md`), and most single-method ports are correct (`io.Reader` idiom). The cost is real but this is **not** a finding to act on wholesale — see §7. | Low | L | see §0 |

---

## 4. Redundancy & duplication (DRY)

Duplication here is concentrated in **one-function leaf helpers re-implemented
per package**. The repo has no `pkg/stringsx`-style shared helper home, so each
new package copies the previous one.

| Helper | Implementations | Severity | Effort |
|---|---|---|---|
| `firstNonEmpty` (＋ `firstNonEmptyProvider`, `…String`, `…Image`, `…ImageURL`, `…Semantic`) | **22** | **High** | S |
| `nonEmpty` (＋ `nonEmptyJSON`, `nonEmptyTrim`, `nonEmptyParagraphs` ×2) | **10** | Medium | S |
| `isSHA256` / `isSHA256Hex` | **8** | Medium | S |
| `missingDepError` — **byte-identical** across 6 files | **6** | Medium | XS |
| `nonEmptyParagraphs` — byte-identical across 2 files | 2 | Low | XS |

Locations (representative, full list reproducible with
`grep -rn '^func firstNonEmpty' --include=*.go internal pkg | grep -v _test`):

```text
firstNonEmpty   internal/capabilities/cliprender/adapters/helpers.go:6
                internal/capabilities/jobs/completion/publish_and_complete_use_case.go:396
                internal/capabilities/youtube/adapters/youtube_asset_mapper.go:14
                internal/capabilities/scripts/entity_annotations.go:182
                internal/platform/drive/artifact_publisher_adapter.go:362
                internal/platform/delivery/registry_transport.go:81
                internal/platform/renderinggen/overlay_artifact_publisher.go:160
                internal/platform/qdrant/indexing/payload_builder.go:112
                internal/platform/postgres/media/media_committer.go:111
                internal/platform/artlist/{scraper/scraper.go:445,downloader/resolver_url_helpers.go:57,fallback/pixabay.go:248}
                internal/platform/media/rustexec/semantic_adapters.go:166
                internal/platform/sqlite/assets/artlist/artlist_searcher.go:118
                internal/app/wiring/adapters_scenetext_helpers.go:13
                internal/app/wiring/vidrush/vidrush_materialization.go:463
                internal/capabilities/assets/providers/{artlist/provider_utils.go:67,stock/stockpipeline/manifest_projection.go:180}

missingDepError internal/capabilities/assets/clips/{operations:132,publication:106,
                ingest:114,catalog:172,processing:111,bulk:108}/module.go
```

**Note the semver trap in `firstNonEmpty`:** two variants are *not*
interchangeable. `firstNonEmpty(values ...string)` returns the first non-empty;
`internal/capabilities/jobs/completion/publish_and_complete_use_case.go:396`
declares `firstNonEmpty(value, fallback string)` — a **two-arg fallback**, a
different contract under the same name. Any de-duplication must first split the
name, or it will silently change behaviour.

**Duplicated subsystem:** D1 is also an over-processing instance — two Chronon
metrics adapters exist (`internal/app/wiring/chronon/chronon_metrics_wiring.go:13`
and `internal/app/wiring/rendering/metrics.go:14`), the second being the live one.

---

## 5. Bloated state & caching (inventory / motion)

**Verdict: clean. This is the category with no actionable finding, and saying so
is the honest result.**

- `pkg/cacheutil.LRU` is a bounded, sharded LRU and is used at every cache site
  that matters: `internal/platform/drive/folder_manager.go:126` (Drive folder
  cache), `internal/kernel/digest/verifier.go:67` (digest verification memo),
  `internal/capabilities/youtube/usecase/search_service.go:74-75` (search +
  metadata L1), `internal/capabilities/cliprender/worker_result.go:214`
  (subtitle facts). No unbounded cache was found.
- **Zero** package-level mutable maps and **zero** global `sync.Map` in
  production. The 2 280 in-code `map[string]…` declarations are overwhelmingly
  static lookup tables (`allowedSortColumns`, `canonicalFonts`, `languageNames`,
  `validSourceTypes`, …), i.e. constants, not accumulating state.
- The only "inventory" cost is O2: 1 724 LOC of test doubles that must track
  production interfaces. That is a maintenance tax, not a leak.

**Transport / redundant roundtrips:** nothing new beyond what
`docs/PIPELINE-WASTE-AUDIT-2026-09-12.md` already closed (per-clip full-file
SHA-256 memoised, artifact single-pass hash-while-streaming, content-addressed
prefetch dedup, cached digest reuse in the publisher).

---

## 6. Performance bottlenecks (waiting) — and what is already fixed

| Item | Status |
|---|---|
| `time.Sleep` in production hot paths | **Already eliminated.** All hits are comments documenting removal. |
| Errgroup / parallel fan-out discipline | **Good.** 37 production uses across 17 files, including the hot ones (`renderinggen/queue_client.go`, `queue_asset_prefetch.go`, `cliprender/preparer.go`, `cliprender_publisher.go`, `localization/scheduler.go`). |
| Sequential per-item I/O loops (56 in `cliprender`/`renderinggen`/`scripts`) | **Spot-check only.** Many are *correctly* sequential (ordering, lease semantics, deterministic plan sealing). No blanket parallelisation is warranted. |
| clip.render submit/settle occupancy, parent-finalisation latency, per-clip source reads | **Closed** by the benchmark suite (`make bench-cliprender`, 10 scenarios) and documented in `docs/tickets/TICKET-CLIP-RENDER-ASYNC-COMPLETION.md` §5 / §8. |
| Build/verify wall-clock | O3 above — 236 targets is the *main* waiting cost for a developer, not any single test. |

The repo has an unusual, deliberately maintained performance instrument
(`internal/capabilities/performance/benchmark.go`,
`internal/capabilities/scripts/render_concurrency_benchmark_test.go`,
`make bench-cliprender`). Keep it; it is the reason the hot-path findings above
could be closed with evidence instead of opinion.

---

## 6b. Time-boxed debt already on a clock (found while scanning)

These are not new findings — they are *scheduled* waste that some owner has
already accepted with an expiry date. An audit should surface them so nobody
mistakes them for resolved:

| Expiry | Item | Owner surface |
|---|---|---|
| 2026-12-31 | `internal/capabilities/assets/register/requests.go:53` — "backward-compat until 2026-12-31 (per FASE 2.1 freeze)" | asset registration |
| 2026-12-31 | `LEXICON_MIRROR_DEBT`: hardcoded linguistic maps in `internal/capabilities/imagesearch/knowledge.go` and `internal/capabilities/scripts/adapters/vidrush_artlist_isolation.go` — migrate to `LexiconRegistry` / `config/lexicons/**` | lexicon SSOT |

Both are enforced as **non-fatal residue** by `archcheck` today, so they will not
remind anyone when they expire. If the deadline is real, promote them to a
failing check at the expiry date; otherwise delete the date and stop pretending.

Also carried by `archcheck` as known residue (migration in progress, not waste):
large `percheck_metadata_key_registry bare-key-residue` set (legacy
`Asset.Metadata` typed-accessor migration) and comment-only canonical-reference
counts in `internal/kernel/asset/rights_state.go`. `has_hard_gate_hits` is
currently **false**, so none of these block.

---

## 7. Prioritised action list

Ordered by *impact ÷ effort*. Severity is blast radius (does it slow every
developer / every render, or just add noise?); effort is S ≤ half a day,
M ≤ 2 days, L > 2 days.

### P0 — delete the unreachable subsystem (est. −1 300 prod LOC, −444 test LOC)

1. **D1 + O1 + D8 — remove `internal/app/wiring/chronon/` entirely** (S).
   Delete the package; delete `render_attempt_analytics_wiring_test.go`'s
   dependency on it (the metrics assertion it makes is already covered against
   the live path in `internal/app/wiring/rendering/metrics.go`); fix the stale
   `observability-measurement-matrix.yaml:1082` row to name the real writer.
   *Before deleting, answer one question:* was the certification probe meant to
   gate the render lane (`chronon_wire.go` says "the render lane is gated on a
   certified Chronon before it is exposed to the queue")? If yes, this is a
   **wiring gap, not dead code** — and closing it is a P0 feature, not a
   deletion. That single decision determines whether D1 is a delete or a wire.
2. **D2 — remove `internal/platform/images/pexels/`** (S). Repoint the one e2e
   test at the live `artlist/fallback` provider. **DONE 2026-09-19** — see §2.1.
3. **D3 + D4 — remove the `ErrNotWired` stub stores** (S). They exist only to
   satisfy interfaces that production never constructs. Either delete both
   packages and their sole e2e consumer, or replace the e2e test's dependency
   with a real implementation — the `ErrNotWired` sentinel says the intent was
   the latter but it was never finished. **REVISED 2026-09-19 (§2.1): they are
   not stubs.** `ErrNotWired` only guards a nil `*sql.DB`; both adapters are
   complete and durable. The real gap is wiring (no production constructor for
   either), and for D3 the only `ArtifactVerifier` verifies CAS objects while the
   audio checkpoint records a media/Drive artifact. Treat as two open feature
   decisions: wire (with a matching verifier + producer + dispatcher) or retire
   — delete neither on the strength of this audit alone.
4. **D5 — remove `stockpipeline/reconcile/`** (S), **D6 — remove the two
   doc-only packages** (XS), **D7 — delete the `verify-vidrush-dry` target** (XS).

### P1 — collapse duplicated leaf helpers (est. −120 LOC, removes 48 copies)

5. **Create `pkg/stringutil` (or extend an existing `pkg/text*`)** with the
   three canonical helpers, then replace all call sites (S–M). **Split the
   contract variants first** (§4: `firstNonEmpty(v ...string)` vs
   `firstNonEmpty(value, fallback)`) so the merge cannot silently change
   behaviour. Add a `percheck` banning *new* local `firstNonEmpty` declarations
   in `internal/` — this repo already backstops its conventions with perchecks,
   so the regression is preventable, not just fixable.
6. **`missingDepError` (6 byte-identical copies) → one exported helper** (XS).
   These six `assets/clips/*/module.go` files are already a submodule family;
   one shared `moduleutil` import removes five copies with zero behavioural risk.

### P2 — reduce the maintenance surface

7. **O3 — document the verify-tier lattice as a decision table** in
   `CONTRIBUTING.md` (S). Do not delete tiers blind: `verify-agent` and
   `verify-fast` are load-bearing for the agent loop. Consolidate only the
   tiers with no distinct purpose (the `-changed` vs `-changed-components` vs
   `-components` trio is the obvious first candidate).
8. **O2 — split the 983-line asset-committer test double** along the same
   seams as the interface it implements (M). This is the highest-cost test
   artifact in the repo and the one most likely to block an interface change.

### P3 — keep, do not touch

9. **O4 (865 interfaces / 564 single-method)** — *no action.* The house style is
   interface-per-port and the gates depend on it. Wholesale de-abstraction would
   be the single most destructive change available and would save nothing
   measurable.
10. **`pkg/cacheutil.LRU` and the performance-instrument packages** — keep. They
    are the reason this audit could be evidence-based.

---

## 8. Things to build next

Ranked by expected return. Each is a *new capability*, not a cleanup:

1. **Decide and land the Chronon certification question** (blocking D1). Either
   delete it, or wire it so `ChrononNativeCertified` actually gates the render
   lane. Leaving it half-built is the worst of the three options: 449 lines of
   host-readiness logic that no operator can observe.
2. **A `make waste-report` target** that re-runs the exact commands in this
   audit and prints the drift (orphan packages, duplicate-helper counts, marker
   counts). The repo already has the gating culture for this; today it has no
   *waste* gate, only architecture gates.
3. **A `percheck_no_local_leaf_helpers` gate** (see P1 step 5) so duplicated
   `firstNonEmpty`/`nonEmpty`/`isSHA256` cannot be re-introduced.
4. **An orphan-package gate**: any `internal/**` package with zero non-test
   importers fails `archcheck`. This audit found six such packages by hand; the
   check is ~30 lines and permanently closes the category.
5. **A second Pexels-class defence**: D2 happened because two provider
   implementations were built for one integration. A capability-inventory check
   asserting one live implementation per external provider would catch the next
   one (`architecture/capability_inventory.yaml` is already the right home).
6. **Extend `make bench-cliprender` into a CI perf gate** that fails on
   regression rather than only reporting — the harness exists and is
   `-race`-clean; it just has no threshold.

---

## 9. Reproduction commands

```bash
# zero-importer production packages (the D1–D6 detector)
IMPL='github.com/Marcuss-ops/PipelineGen'
for d in $(find internal -name '*.go' ! -name '*_test.go' -exec dirname {} + | sort -u); do
  pkg="${IMPL}/${d#./}"
  prod=$(grep -rl "\"$pkg\"" --include='*.go' internal pkg cmd tests 2>/dev/null | grep -v "^$d/" | grep -v '_test.go' | wc -l)
  any=$(grep -rl "\"$pkg\"" --include='*.go' internal pkg cmd tests 2>/dev/null | grep -v "^$d/" | wc -l)
  [ "$any" -gt 0 ] && [ "$prod" -eq 0 ] && echo "TEST-ONLY: $d"
done

# duplicate leaf helpers
for f in firstNonEmpty nonEmpty isSHA256 isSHA256Hex missingDepError; do
  printf '%-16s %s\n' "$f" "$(grep -rn "^func $f" --include='*.go' internal pkg | grep -v _test | wc -l)"
done

# marker debt
grep -rn 'TODO\|FIXME\|XXX' --include='*.go' internal pkg cmd | wc -l

# baseline
find internal pkg cmd -name '*.go' ! -name '*_test.go' -exec cat {} + | wc -l
grep -rn '^type [A-Z][A-Za-z0-9_]* interface {' --include='*.go' internal pkg | grep -v _test | wc -l
```

---

## 10. Bottom line

The pipeline itself is not where the waste is — that ground was already covered
and closed by the clip.render suite. The waste that remains is **structural**:
one fully-built subsystem (`internal/app/wiring/chronon/`) that was never wired,
five packages that no binary can reach, a doc row pointing at a deleted file, and
48 copies of four leaf helpers.

Total recoverable production code: **~1 430 LOC orphaned + ~120 LOC duplicated**,
with **444 LOC of orphaned tests** attached. Nothing in that set is on a hot
path, and none of it requires touching the working render pipeline — which is
exactly why it is the right next move: high-confidence, low-risk subtraction
before any further optimisation.
