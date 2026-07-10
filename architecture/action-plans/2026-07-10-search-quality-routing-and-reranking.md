# search-quality-routing-and-reranking action plan

**Ship_date:** 2026-07-10
**Wave-tracker anchor:** `architecture/current.yaml#SEARCH-QUALITY-ROUTING-AND-RERANKING-2026-07-10` (DEFERRED per pre-existing `PRE-EXISTING-YAML-PARSE-2026-07-04` carry-forward + `PR-CURRENT-YAML-PARSE-FIX-PART-N` forward-pointer; parent action-plan + CHANGELOG + AGENTS entries are canonical SOLE closure record per `PR-POSTPROCESSOR-UNIFICATION-PHASE-4` precedent)
**Seed user spec:** Italian audit pasted 2026-07-10 covering 10 architectural improvements to the Qdrant + Hybrid + Reranking pipeline.

---

## §0 Honest status snapshot (verified live 2026-07-10)

The codebase already has a solid 7.5/10 foundation from `architecture/action-plans/2026-07-02-unified-semantic-multimodal.md`:

✅ **Already shipped (no work needed):**
- Single collection `media_assets_current` behind the alias (canonical blue-green flip per `cmd/admin/reindex_qdrant_apply.go:179`)
- Multi-vector per asset (`text` 768 + `transcript` 768 + `visual` 768 + `bm25_text` sparse + `audio` optional) per `internal/infrastructure/qdrant/indexing/index_document.go:102`
- Hybrid as default (ANN + BM25 RRF) per `internal/infrastructure/qdrant/search/search_adapter.go`
- Source-aware strategies (per-source `BuildSearchText` at `internal/infrastructure/indexing/searchtext/strategies.go`)
- Hard lifecycle filter `lifecycle_state=ACTIVE` per `semantic_asset_search_adapter.go:276`
- Workspace filter `workspace_id` per `filter_compiler.go:78`
- A canonical `source_version` column on `media_assets` (`canonical.go:168`) used by the IndexingHandler supersede gate via the existing 3-input SHA gate at `clipindexer/indexing.go:113-121`

❌ **Confirmed missing (gap verified via code-search 2026-07-10):**
- **No source-aware channel weight resolver** — `SearchProfileResolver` returns 0 matches codebase-wide. The aggregation layer uses one fixed blend `0.65*qdrant + 0.35*rerank` per `aggregator_types.go:121`. YouTube (transcript-heavy) and stock (visual-heavy) get identical scoring.
- **`source_url` STILL embedded in the Qdrant search text** — the canonical anti-pattern the user spec criticizes is live at `internal/infrastructure/indexing/searchtext/strategies.go:19,65,77,170,202` (segments like `"Source: https://www.youtube.com/watch?v=..."` get pushed into the embedding). The user is right; this has not been fixed.
- **Reranker is a synthesis stub** — `aggregator_semantic.go:331` hardcoded `r = 0.5` ("synthesise >= 0.5 so the rerank weight has floor"). `clip_source_builder.go:45` ships `reranker any` placeholder. Real `*reranker.Client` is wired in composition but the aggregator never asks it for a real score — it draws a floor. The score blend is `MixedScore(qdrantScore, 0.5, 0.35)`.
- **No dense/sparse text split** — `BuildSearchText` (single function) is the only composer; zero matches for `BuildDenseText` / `BuildSparseText`. Both dense embedding and BM25 sparse see the same text (URL noise included).
- **No diversification post-rerank** — 0 matches for `MMR`, `MaximalMarginal`, `videoDedup`, `overlap`. `asset.OverlapSize` (books/books.py) is the only feature named "overlap" and it's about transcript chunking, not result diversification.
- **Filter coverage thin** — only `workspace_id` + `lifecycle_state` + `source` are wired in `filter_compiler.go`. User-spec wants 9 hard filters pre-search: `media_type`, `duration_min/max`, `orientation`, `license`, `resolution`. None implemented.
- **No benchmark corpus** — 0 matches for `expected_asset_ids`, `recall@`, `MRR`, `nDCG`. The aggregator has production telemetry (`metrics_media.go`) but no offline testbed to measure Hybrid > ANN > Qdrant-only retrieval quality.

## §1 Honest-limitation disclosure (godlike/07)

- **YouTube / stock weights in §3 of the user spec are INITIAL values, not universal**. Per the user spec itself: "Questi non sono pesi universali: sono valori iniziali da calibrare con query reali." ⇒ PR-12 (benchmark + 30-day soak) is the canonical verification surface that validates the weights chosen in PR-1.
- **Visual vector is on a separate model surface** — the SigLIP 768-dim visual vector is wired in the indexer (`index_document.go:102`) but the stock search profile assumes it's already populated end-to-end. If the existing visual embedding pipeline is incomplete (a real possibility per `PR-YT-CLIP-VISUAL-EMBED-OPTIONAL`), PR-1 MUST degrade gracefully (default visual weight 0 if no visual payload).
- **Reranker ServerURL is required** — `config.Reranker.URL` defaults to `http://127.0.0.1:8091/rerank`. PR-9 needs the operator to have a `bge-reranker-v2-m3` server reachable. Without it, PR-9 must keep the synthesis stub as a fallback with `cfg.Reranker.Enabled=false`, NOT commit pre-scripted scores.
- **No move to multi-collection.** Per the user spec itself: "Non cambierei tecnologia e non sostituirei Qdrant." This plan NEVER creates per-source collections.

## §2 Goal (the 8 dimensions the plan addresses)

Ship the 8 user-spec dimensions as 13 incremental PRs:

1. **Per-source channel weights** (PR-1..3) — YouTube vs stock vs artlist get dedicated weight profiles; the resolver picks the right profile from request.source.
2. **Clean dense + sparse texts, no URL noise** (PR-4..6) — split `BuildSearchText` into `BuildDenseText` (natural prose for embedding) + `BuildSparseText` (keyword-rich for BM25); both DROP `source_url`.
3. **Hard filter coverage (9 filters)** (PR-7..8) — `media_type` / `duration_min` / `duration_max` / `orientation` / `license` / `resolution` joined to existing `workspace_id` + `lifecycle_state` + `source`. Filters are applied BEFORE Qdrant skip-search so no semantic ranking on irrelevant rows.
4. **Real reranker (no more synthesis)** (PR-9..10) — wire the canonical `*reranker.Client`. Score the top 20–30 hits between Qdrant retrieval and diversification. Reranker server reachable OR synthesis fallback (fail-closed, NOT fail-silently).
5. **MMR diversification** (PR-11) — max N per source video, overlap-interval penalty, visual hash dedup. Runs AFTER reranker as a post-processing stage.
6. **Canonical source_version formula** (PR-12) — extend the existing SHA gate to include `dense_text_hash + sparse_text_hash + schema_version + embedding_model_version`. Make a future model swap automatically supersede stale Qdrant points.
7. **Benchmark dataset (100 real queries)** (PR-13 P3) — versioned JSON with `expected_asset_ids`. Compute `recall@5`, `recall@10`, `MRR`, `nDCG@10`, `latency p50/p95` per source bucket.
8. **Soak + recalibration** (P3 P5 meta) — 30-day soak with the benchmark running nightly; the operator tunes the weights via config-driven profile patches (no code change).

## §3 Per-PR migration sequence (13 atomic closures)

### Priority bands

- **P0 absolute (5 PRs, deadline 2026-08-08)**: PR-1, PR-2, PR-3, PR-4, PR-9 — composition-root contracts the system can't safely ship without.
- **P0 typed-spine (2 PRs, deadline 2026-08-08)**: PR-6, PR-10 — sibling TDD for PR-4 / PR-9.
- **P1 (3 PRs, deadline 2026-08-22)**: PR-5, PR-7, PR-11, PR-13 — incremental improvements with byte-equivalent rollouts.
- **P2 (2 PRs, deadline 2026-09-01)**: PR-8, PR-12 — refinement + validation.
- **P3 docs (1 PR, deadline 2026-09-15)**: PR-13.bench — benchmark metric surface.

### Dependency graph

```
PR-1 (SearchProfile port) ─► PR-2 (composition-root wiring) ─► PR-3 (TDD)
                                          │
                                          ▼
PR-4 (BuildText split: dense-only + sparse-only, drop URL) ─► PR-5 (wire payload) ─► PR-6 (TDD)
                                          │
                                          ▼
PR-7 (Add 6 hard filters) ─► PR-8 (TDD)
                                          │
                                          ▼
PR-9 (Real Reranker wire) ─► PR-10 (TDD)
                                          │
                                          ▼
PR-11 (MMR diversification) ─► PR-12 (canonical source_version formula)
                                          │
                                          ▼
PR-13 (Benchmark dataset v1) ─► soak + recalibration (P5 meta)
```

Each blade is independent. PR-11 can ship WITHOUT PR-9 (MMR over the synthesis-stub scores is still useful); PR-12 is independent of PR-13 (formula canonicalization is a one-shot ingest-time change).

### Per-PR details

#### **1. PR-SEARCH-PROFILE-RESOLVER-CORE** (P0, deadline 2026-07-22)

- **Surface (3 files, ~280 LoC):**
  - `internal/application/search/ports/searchprofile.go` — new `SearchProfile` typed enum + per-profile weight struct:
    - `ProfileName` closed-set: `youtube | stock | artlist | image | voiceover | default` (6 values).
    - `Weights` struct: `Text float64 + Transcript float64 + Visual float64 + BM25 float64 + Metadata float64` (unit sum constraint enforced by compile-time `IsValid()`).
    - `SearchProfileResolver` interface (`Resolve(ctx, ProfileName) (Weights, error)`) + `Registry` concrete (`Register(name, Weights) error` validates closed-set name + `IsValid()` weights; thread-safe after `Freeze`).
  - `internal/application/search/registry.go` — `BackendRegistry`-style lockable registry with `default` profile weights: `Text 30, BM25 30, Visual 20, Metadata 20` (sums to 100). YouTube/stock/artlist overrides registered at composition.
  - `internal/application/search/registry_test.go` — 8 hermetic TDD: closed-set enum completeness + IsValid (sum=100) + Register-after-Freeze typed error + Resolve unknown typed error + nil-receiver typed error + 6 profile-looup assertions.
- **godlike/06 SSOT:** `SearchProfile` + `Weights` lives ONLY at `internal/application/search/ports/searchprofile.go`; the canonical `youtube` profile (`Text 25 / Transcript 35 / BM25 25 / Visual 15`) and `stock` profile (`Text 25 / Visual 45 / BM25 25 / Metadata 5`) live in `NewCanonicalProfiles()` constructor at the same file (one source-of-truth for the weights).
- **godlike/07 typed-error contract:** `ErrProfileUnknown`, `ErrProfileWeightsInvalid`, `ErrRegistryFrozen` (all `errors.Is`-probeable). `IsValid()` is a structural check (sum=100, each ≤1.0) — encoded as method NOT free-floating helper.
- **godlike/07 minimum-blast-radius:** ZERO new exported types beyond `SearchProfile` + `Weights`; ZERO signature changes outside the new port surface.
- **Verification:** `gofmt` clean; `go vet` + `go build` exit 0; 8 NEW TDD PASS, all pre-existing test fail-rate preserved.
- **Forward-pointer:** PR-2 wires the registry.

#### **2. PR-SEARCH-PROFILE-RESOLVER-WIRE** (P0, deadline 2026-07-22)

- **Surface (4 files, ~120 LoC):**
  - `internal/app/wire_search.go` — composition-root `buildSearchProfileResolver() (search.SearchProfileResolver, error)` returns the canonical registry with the 6 profiles locked at `Freeze()` time. The `default` profile is the aggregator-blend baseline.
  - `internal/app/composition.go` — `ComposeRoot` gains `Search *search.SearchProfileResolver` field (additive, nil-tolerant for non-search compose modes).
  - `internal/application/assets/providers/aggregator_semantic.go` — `ServiceDeps.SearchProfileResolver` field on `AggregatorService` (additive); `Blend(hit Score, profile Weights)` helper consumes the resolver instead of the hardcoded 0.65/0.35. Default fallback uses `default` profile if resolver nil.
  - `internal/application/assets/providers/aggregator_test.go` + `aggsem/aggregator_aggregate_test.go` — 5 NEW test cases pinning per-source weight dispatch: youtube input → resolver returns YouTube weights (Transcript dominant); stock input → stock weights (Visual dominant); artlist input → artlist weights; nil-resolver → `default` weights.
- **godlike/06 SSOT:** `buildSearchProfileResolver` lives ONLY at `internal/app/wire_search.go`; the defaults live ONLY at `internal/application/search/ports/searchprofile.go::NewCanonicalProfiles`.
- **godlike/07 NO-FAKE-AVAILABILITY:** when `cfg.Features.SearchProfileResolver=false` (the production flag the operator flips during migration), the resolver is `nil` and the `default` profile is used. NO silent-success on degraded mode: a structured log on composition warns that source-aware weights are disabled.
- **godlike/07 typed-error contract:** blende FallThrough to `default` weights if resolver-lookup returns typed error; this is the canonical 503 path when composition is misconfigured.
- **Verification:** `gofmt` clean; `go vet` + `go build` exit 0; 5 NEW + 17 pre-existing tests PASS.
- **Forward-pointer:** PR-3 pins the resolver contract; PR-13 reads the weight profiles from a versioned benchmark report.

#### **3. PR-SEARCH-PROFILE-RESOLVER-TDD** (P0, deadline 2026-07-22)

- **Surface (2 files, ~190 LoC):**
  - `internal/application/search/search_adapter_test.go` — extend existing test surface with 6 NEW cases pinning wire-shape:
    - `ResolveWeight_YouTube_TranscriptDominant`: Weights{Transcript: 0.35, Text: 0.25, BM25: 0.25, Visual: 0.15, Metadata: 0} — derives Hybrid blend 0.25/0.30/0.10 transcript-aware.
    - `ResolveWeight_Stock_VisualDominant`: Weights{Visual: 0.45, Text: 0.25, BM25: 0.25, Transcript: 0, Metadata: 0.05}.
    - `ResolveWeight_Artlist_TagRich`: Weights{Text: 0.50, BM25: 0.30, Metadata: 0.20}.
    - `ResolveWeight_NilResolver_FallsBackDefault`: Weighted == default profile (text 30 + bm25 30 + visual 20 + metadata 20).
    - `Wire_TestOnlyProfile_Locks`: assert `IsValid()` rejects sum=101 case (typed error contract).
    - `Wire_DuplicatedProfile_FailsAtRegister`: assert duplicate registration raises typed error.
  - `internal/application/search/ports/searchprofile_test.go` — registry tests.
- **All gates GREEN per PR-1 + PR-2 verification conventions.**

#### **4. PR-BUILD-TEXT-DENSE-SPARSE-CORE** (P0, deadline 2026-07-29)

- **Surface (4 files, ~620 LoC):**
  - `internal/infrastructure/indexing/searchtext/build_dense.go` — NEW canonical `BuildDenseText(in SearchTextInput) string` composer. Returns natural-prose sentences optimal for embedding (`Title. Summary. Hook. Scene focusing on subject. Topics: a, b, c. Mentions: speakers; mentioned_people.`). EXPLICITLY drops `source_url` from output.
  - `internal/infrastructure/indexing/searchtext/build_sparse.go` — NEW canonical `BuildSparseText(in SearchTextInput) string` composer. Returns keyword-rich space-separated text for BM25 (`Title summary hook subject speakers mentioned_people topics tags`). EXPLICITLY drops `source_url` from output.
  - `internal/infrastructure/indexing/searchtext/strategies.go` — REMOVE `source_url` from `youtubeAdditionalKeys` slice (line 19) + `stockAdditionalKeys` slice (line 148) + remove all builds referencing `add["source_url"]` (lines 65, 77, 170, 202). The canonical `SearchTextInput.Additional` map can STILL carry source_url as metadata, but neither composer reads it.
  - `internal/infrastructure/indexing/searchtext/builder.go` — `SearchTextComposer` adds 2 NEW methods `BuildDense` + `BuildSparse`. Existing `BuildSearchText` is RETAINED as a deprecated alias pointing to `BuildDense` for backward-compat reading callers (the canonical surface for new code is the split pair).
- **godlike/06 SSOT:** `BuildDenseText` + `BuildSparseText` live ONLY at `internal/infrastructure/indexing/searchtext/{build_dense.go,build_sparse.go}`; `youtubeAdditionalKeys` + `stockAdditionalKeys` slices live ONLY at `strategies.go` (canonical SOLE owner of per-source key lists).
- **godlike/07 NO-FAKE-AVAILABILITY:** dropping `source_url` IS the user-spec fix; but it changes the wire-shape across existing test fixtures. PR-6 re-tests the 4 youtube tests + 1 stock test + 1 generic test to verify dropped segments don't break invariants.
- **godlike/07 minimum-blast-radius:** the deprecated `BuildSearchText` alias keeps 0 callers outside package (searchtext/builder_test.go's 1 call site IS migrated to BuildDense); downstream callers of `SearchTextComposer.BuildSearchText` (Qdrant payload) are SPLIT into 2 call sites: dense payload pulls `BuildDense`, sparse payload pulls `BuildSparse`. PR-5 wires the split.
- **Verification:** `gofmt` clean; `go vet` + `go build` exit 0; 6 NEW TDD test cases + 11 pre-existing tests PASS (1 dropped test for the Source: label).
- **Forward-pointer:** PR-5 wires the dual-compose; PR-13 measures recall improvement on the dropped-URL corpus.

#### **5. PR-BUILD-TEXT-DENSE-SPARSE-WIRE** (P0, deadline 2026-07-29)

- **Surface (3 files, ~180 LoC):**
  - `internal/infrastructure/qdrant/indexing/payload_builder.go::BuildPayloadFromDocument` — if `doc.Dense` exists, push it to the `"text"` channel; if `doc.Sparse` exists, push it to `"bm25_text"` channel. Both go to the same Qdrant point; dense feeds embedding model; sparse feeds BM25.
  - `internal/infrastructure/qdrant/indexing/index_document_types.go` — new `Dense string` + `Sparse string` fields on `IndexedMetadata` (additive, omitempty).
  - `internal/infrastructure/qdrant/indexing/payload_builder_test.go` — 4 NEW tests:
    - `BuildPayloadFromDocument_DenseAndSparse_PopulatesBothChannels`: payload["text"]=dense + payload["bm25_text"]=sparse.
    - `BuildPayloadFromDocument_DenseOnly_OmittedSparse`: omitempty.
    - `BuildPayloadFromDocument_SparseOnly_OmittedDense`: omitempty.
    - `BuildPayloadFromDocument_NoText_BothChannelsEmpty`: empty payload both fields, no payload keys.
- **godlike/06 SSOT:** `IndexedMetadata.Dense` + `Sparse` live ONLY at `internal/infrastructure/qdrant/indexing/index_document_types.go`; the Qdrant payload channel split lives ONLY at `BuildPayloadFromDocument`.
- **godlike/07 NO-FAKE-AVAILABILITY:** if BOTH dense and sparse are empty (e.g. zero-input failure case), the Qdrant point is upserted with the 17 first-class semantic metadata fields but NO text channel — this is operator-visible via the canonical `payload_empty_channels_total` Prometheus counter (forward-pointer `PR-METRICS-PAYLOAD-EMPTY-CHANNELS`).
- **Verification:** `gofmt` clean; `go vet` + `go build` exit 0; 4 NEW tests PASS; pre-existing 15 tests PASS (no regressions).

#### **6. PR-BUILD-TEXT-DENSE-SPARSE-TDD** (P0, deadline 2026-08-08)

- **Surface (1 file, ~330 LoC):**
  - `internal/infrastructure/indexing/searchtext/build_dense_test.go` + `build_sparse_test.go` — 12 hermetic TDD covering each of the 5 source strategies + 1 default + 1 nil-input + 4 godlike/07 NO-FAKE-AVAILABILITY contracts (dropped source_url absent + retained behavior intact + tombstone-content under empty + panic-free on nil-receiver).
- **All gates GREEN.**

#### **7. PR-CANONICAL-FILTERS-EXPAND** (P1, deadline 2026-08-08)

- **Surface (3 files, ~430 LoC):**
  - `internal/application/search/filter_spec.go` — new typed `FilterSpec` struct (10 fields: `Source / MediaType / Subtype / Language / WorkspaceID / LifecycleState / Orientation / License / MinDurationSec / MaxDurationSec / MinResolution / MaxResolution`). Each field typed + omitempty. Compile-time pin `var _ FilterCompilerInput = FilterSpec{}` validates structural interface.
  - `internal/infrastructure/qdrant/search/filter_compiler.go` — extend existing compiler to emit `must` clauses for all 10 fields where non-empty. Duration range becomes a single `range` clause (Qdrant supports numeric range natively). Resolution: optional cap to be decided via `MaxNativeRes`.
  - `internal/application/search/filter_spec_test.go` — 10 NEW tests for each field. Plus a guard test `FilterSpec_NilValues_ProducesEmptyFilter` (no silent-success when no filters set).
- **godlike/06 SSOT:** `FilterSpec` lives ONLY at `internal/application/search/filter_spec.go`; `CompileQdrantFilter` lives ONLY at `filter_compiler.go` (extend; no replace).
- **godlike/07 NO-FAKE-AVAILABILITY:** filter compilation is structural — if a filter value is invalid (e.g. negative duration), the compiler returns `errors.New("filter spec invalid: %w", cause)` and the search returns `400 Bad Request` WITHOUT touching Qdrant.
- **godlike/07 minimum-blast-radius:** zero changes to existing call signatures; `CompileQdrantFilter(ctx, FilterSpec)` is a NEW method, the old `(ctx, CallerFilter)` signature is RETAINED as a passthrough that delegates to `filterSpecFromCallerFilter()` for back-compat.
- **Verification:** `gofmt` clean; `go vet` + `go build` exit 0; 11 NEW tests PASS.
- **Forward-pointer:** PR-11 (MMR diversification) reads `FilterSpec` from caller.

#### **8. PR-CANONICAL-FILTERS-TDD** (P1, deadline 2026-08-15)

- **Surface (2 files, ~210 LoC):** retest the 4 filter_compiler tests + 10 new edge cases for duration negative, zero, oversized; integer vs string union; license format.
- **All gates GREEN.**

#### **9. PR-RERANKER-REAL-CLIENT** (P0, deadline 2026-08-08)

- **Surface (6 files, ~480 LoC):**
  - `internal/infrastructure/ai/reranker/types.go` — new `Request` (12 fields: query + 20 hits with docid/title/summary) + `Response` (10 docid→score pairs) wire-shape DTOs. CRITICAL: `Query` is the FULL combined dense+sparse+visual text; `Doc.Text` is the asset's summary (NOT title).
  - `internal/infrastructure/ai/reranker/client.go` — new `Client` concrete struct + `NewClient(cfg Config) (*Client, error)` fail-closed (cfg.URL empty returns `ErrRerankerURLEmpty`). `Score(ctx, Request) (Response, error)` makes the HTTP call via canonical `pkg/executil.Run` (NOT new HTTP dep). The call uses `process.Run` (canonical Port-Process spawn) so the reranker becomes a per-call HTTP fetch (like VLM).
  - `internal/infrastructure/ai/reranker/scoring.go` — add `RerankerClientPort` interface declared in this file (the canonical surface owner per Pattern 0); old `MixedScore` helper RETAINED for the synthesis-fallback case.
  - `internal/application/assets/providers/aggregator_semantic.go` — `Service` ctor takes optional `RerankerClientPort` (nil → fallback to `MixedScore(synthesised=0.5)` with operator WARN log). Real Client call lands in the post-Qdrant aggregation phase, ENRICHING hits before MMR.
  - `internal/app/wire_script_resolvers.go` — composition-root wires `*reranker.Client` when `cfg.Reranker.Enabled=true` AND `cfg.Reranker.URL!=""`. Both gates fail-closed.
  - `internal/app/composition_test.go` — extend to confirm `Reranker: config.RerankerConfig{Enabled: true, URL: "http://127.0.0.1:8091/rerank"}` builds the real Client.
- **godlike/06 SSOT:** `RerankerClientPort` interface lives ONLY at `internal/infrastructure/ai/reranker/scoring.go`; the concrete `Client` lives ONLY at `client.go`. Same package per godlike/06 SSOT — interface + concrete co-located.
- **godlike/07 typed-error contract:** `ErrRerankerURLEmpty`, `ErrRerankerTimeout`, `ErrRerankerServerFailure` (typed sentinels, dual-%w chains).
- **godlike/07 NO-FAKE-AVAILABILITY:** the synthesis-stub fallback (`MixedScore(synthesised=0.5)`) is OPERATOR-OPT-IN — controlled by `cfg.Reranker.Enabled=false` or `cfg.Reranker.URL=""`. When `Enabled=true` AND `URL=` real, the Client is wired. When synthesis-stubs fire, a one-line operator log fires (`log.Warn("reranker.Client not wired (composition root); falling back to synthesised 0.5 floor")`).
- **godlike/07 minimum-blast-radius:** zero new dependencies (uses existing `pkg/executil.Run`); zero signature changes to existing `*aggregator.AggregatorService` callers (the `RerankerClientPort` field is additive, nil-default).
- **Verification:** `gofmt` clean; `go vet` + `go build` exit 0; 8 NEW TDD PASS (mock HTTPRoundTrip via httptest.NewServer surrogate); pre-existing 11 aggregator tests PASS.
- **Forward-pointer:** PR-10 pins the contract.

#### **10. PR-RERANKER-REAL-CLIENT-TDD** (P0, deadline 2026-08-08)

- **Surface (2 files, ~410 LoC):** hermetic TDD covering the 5 failure modes (URL empty / timeout / server error / decode error / score-out-of-range) + the happy-path round-trip + the 4 contract cases (signature stability for v1 + v1 schema bounds).
- **All gates GREEN.**

#### **11. PR-DIVERSIFICATION-MMR** (P1, deadline 2026-08-15)

- **Surface (5 files, ~520 LoC):**
  - `internal/application/search/diversify/diversify.go` — NEW canonical `MMRDiversifier` struct + `New(cfg MMRConfig) (*MMRDiversifier, error)` fail-closed ctor + `Diversify(ctx, hits []AggregatedHit, k int) (selected []AggregatedHit, err error)`. MMR formula: `score_diversity = λ * similarity_to_query - (1-λ) * max(similarity_to_already_selected)`. λ default 0.7 (user spec "Diversità per provider" guidance).
  - `internal/application/search/diversify/constraints.go` — NEW canonical constraints:
    - **Max N per source video**: group-by `SourceVideoID` + cap group-by count.
    - **Timestamp overlap penalty**: hits within Δ=30s overlap get a 0.5 multiplier on their MMR score; non-overlapping hits pass through.
    - **Visual hash dedup**: hits with the same `FileHash` (canonical from `IndexClip.SetFileHash`) are deduped to keep only the highest-scored one.
    - **Provider diversity**: per `Source` (stock + artlist + youtube) cap to floor(N/k, 3) hits.
  - `internal/application/search/diversify/service.go` — `Service` struct wires MMRDiversifier + post-reranker aggregation. `Service.WithDiversifier(mmr)` fluent setter for composition-root.
  - `internal/application/assets/providers/aggregator_semantic.go` — `Blend(hit Score, profile Weights)` extended: post-reranker hits go through `Diversifier.Diversify(ctx, hits, k=q.Limit)`.
  - `internal/application/search/diversify/diversify_test.go` — 8 hermetic TDD per the 4 constraints + nil-tolerance + k=0/1/100 edge cases + per-source cap.
- **godlike/06 SSOT:** `MMRDiversifier` lives ONLY at `internal/application/search/diversify/diversify.go`; the constraints live ONLY at `constraints.go`.
- **godlike/07 typed-error contract:** `ErrDiversifyNilHits`, `ErrDiversifyInvalidK`, `ErrDiversifyNegativeLambda` (typed sentinels).
- **godlike/07 NO-FAKE-AVAILABILITY:** if Diversifier composition return nil at runtime (e.g. `cfg.Features.MMREnabled=false`), the aggregator falls back to top-k-by-blend-score WITHOUT diversification. Operator WARN log fires.
- **godlike/07 minimum-blast-radius:** zero signature changes to existing callers; the `Diversify` post-processing stage is OPT-IN via composition-root `WithDiversifier`.
- **Verification:** `gofmt` clean; `go vet` + `go build` exit 0; 8 NEW tests PASS.

#### **12. PR-SOURCE-VERSION-FORMULA-CANONICAL** (P0, deadline 2026-08-22)

- **Surface (2 files, ~270 LoC):**
  - `internal/domain/asset/clip_identity.go` — extended `BuildSourceVersion(in AssetWithContext) string` (canonical SOLE owner):
    ```
    sha256(in.FileHash + ":" + sha256(in.DenseText) + ":" + sha256(in.SparseText) + ":" + in.SchemaVersion + ":" + in.EmbeddingModelVersion)
    ```
    The 5 inputs are explicit (file_hash + dense_text_hash + sparse_text_hash + schema_version + embedding_model_version). Empty inputs become ZERO-LENGTH STRINGS in the hash (NOT `""` sentinel — the operator zeros-out a config string to deliberately rebuild all points).
  - `internal/domain/asset/clip_identity_test.go` — 7 hermetic TDD:
    - Identical inputs → identical hash (byte-equivalent).
    - `file_hash` change → hash changes (operator sees stale point deprecated).
    - `dense_text` change → hash changes (transcript upgrade invalidates).
    - `schema_version` change → hash changes (V3 → V4 invalidates).
    - `embedding_model` change → hash changes (BERT → E5 invalidates).
    - Empty inputs → well-defined stable hash (zero-length fields).
    - 1000-iteration determinism (regression guard for non-cryptographic randomness).
- **godlike/06 SSOT:** `BuildSourceVersion` lives ONLY at `internal/domain/asset/clip_identity.go`; the 5 input columns are SOLE-owned by their canonical type (`FileHash`/`Dense`/`Sparse`/`SchemaVersion`/`EmbeddingModelVersion`).
- **godlike/07 NO-FAKE-AVAILABILITY:** the 5 inputs are operator-changeable; a future operator running BGE→E5 swap will automatically supersede all stale indexed points (Qdrant outbox pending ops will re-index via the IndexingHandler supersede gate).
- **Verification:** `gofmt` clean; `go vet` + `go build` exit 0; 7 NEW tests PASS.

#### **13. PR-BENCHMARK-DATASET-V1** (P1, deadline 2026-09-01)

- **Surface (3 files, ~650 LoC):**
  - `architecture/benchmarks/search-quality-v1.json` — NEW canonical benchmark dataset. 100 real queries across 4 source buckets (YouTube 35 + stock 30 + artlist 20 + cross-source 15). Each query has: `query text`, `expected_asset_ids[]` (1–5 hits per query), `acceptable_sources[]`, `filters{}`, `language`, `notes`. Versioned via schema_version field.
  - `tests/benchmarks/search_quality_metrics.go` — NEW hermetic benchmark runner. Runs `Resolve → Blend → Diversify` against `tests/benchmarks/inmemory_index.go` (NEW 200-line in-memory Qdrant surrogate). Computes:
    - `recall@5`, `recall@10` (per query AND per source bucket)
    - `MRR` (Mean Reciprocal Rank)
    - `nDCG@10` (graded relevance from `expected_asset_ids` ranking)
    - `latency_p50`, `latency_p95`
    - `zero_result_rate` (operator-actionable signal)
  - `tests/benchmarks/search_quality_metrics_test.go` — invariant tests for the metrics math (no live-stack dependency; runs end-to-end on the in-memory index).
- **godlike/06 SSOT:** the benchmark dataset lives ONLY at `architecture/benchmarks/search-quality-v1.json`; the in-memory index lives ONLY at `tests/benchmarks/inmemory_index.go`; the metrics runner lives ONLY at `tests/benchmarks/search_quality_metrics.go`.
- **godlike/07 NO-FAKE-AVAILABILITY:** the metrics runner prints "PASS" or "FAIL" against the expected yardstick (recall@5 ≥ 0.7, recall@10 ≥ 0.85, MRR ≥ 0.55, nDCG@10 ≥ 0.6, latency_p95 ≤ 500ms). FAIL prints the deltas vs baseline (the canonical 0.5-synthesis-stub state).
- **godlike/07 minimum-blast-radius:** zero production code change (in-memory test fixture; benchmark dataset is JSON; metrics runner is `_test.go`).
- **Verification:** `go test -short -count=1 ./tests/benchmarks/` exit 0; metrics print ≥ baseline numbers (or yields operator-actionable gap).

## §4 Per-PR execution checklist

For every PR:

1. Read the §3 PR description + per-file surface + godlike/06/07 invariants.
2. Run `gofmt -l {touched_files}` → 0 hits.
3. Run `go vet ./<touched_pkg>/...` → exit 0.
4. Run `go build ./<touched_pkg>/...` → exit 0.
5. Run `go test -short -count=1 -timeout 60s ./<touched_pkg>/` → all PASS (some pre-existing failures are documented in carry-forward per AGENTS.md convention).
6. Confirm Pre-existing failures NOT introduced by this PR via `git stash --include-untracked && go test` round-trip.
7. `git add {touched_files_only_no_-A}` + atomic commit with `Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>` trailer.
8. Race-protect via `git fetch origin && git log --oneline HEAD..@{u}` (must return empty for safe ff-push).
9. `git push origin main` (direct-to-main per AGENTS.md Git-Lesson-2).
10. Append the canonical ship entry to `CHANGELOG.md ## Unreleased` + `AGENTS.md ## Recent cross-cutting closures`.

## §5 Verification gates (per wave)

The wave is "shipped" when:

- (a) All 13 PRs reach `status: shipped` on `origin/main`.
- (b) `bash scripts/ci-architectural-checks.sh` exits 0 with the weight-profile + filter-expansion archcheck gates wired (forward-pointer if the existing archcheck does not yet cover these).
- (c) `go test -short -count=1 ./...` exits 0 (modulo carry-forward).
- (d) On a live PipelineGen deployment, the `tests/benchmarks/search_quality_metrics.go` benchmark passes all 5 metrics thresholds per source bucket.

## §6 Honest scope-lock (godlike/07 minimum-blast-radius)

- **NO** attempt to switch to multi-collection. The user spec is explicit: "Non cambierei tecnologia e non sostituirei Qdrant."
- **NO** attempt to add a large LLM-based reranker. VPS CPU-only: the user spec specifically recommends "un reranker leggero eseguito solo sui primi 20 risultati." ⇒ PR-9 uses the canonical CrossEncoder HTTP service.
- **NO** attempt to add per-clip fine-grained weight tuning UI. The 6 canonical profiles are shipped at PR-1; future weight calibration is via config patches (PR-13.bench P5 meta).
- **The 100-query benchmark is INITIAL seed**, not the final evaluation set. PR-13 is the canonical seed baseline; future waves grow the corpus as the operator observes production traffic.
- **The 6 hard filter values** (`media_type`, `duration_min/max`, `orientation`, `license`, `resolution`) only apply when the asset has those fields populated. Future ingestion waves may add new dimensions (e.g. `is_hd`, `frame_rate`); callers SHOULD pass them through `FilterSpec` as zero-value omitted optional fields.

## §7 Cross-references (godlike/06 umbrella)

- `architecture/current.yaml#UNIFIED-SEMANTIC-MULTIMODAL-2026-07-02` — parent wave that shipped the 7.5/10 foundation. PRs 1-13 build on top of it without re-ship of the foundation.
- `architecture/current.yaml#YOUTUBE-CLIP-DOD-2026-07-08` — sister wave for the YouTube-side ingestion contracts that SOME PRs depend on (e.g. PR-12 source_version uses YouTube ingest's `file_hash`).
- `architecture/current.yaml#SEARCH-QUALITY-ROUTING-AND-RERANKING-2026-07-10` — THIS wave-tracker anchor. Slot flip DEFERRED per pre-existing YAML parse carry-forward; this file + CHANGELOG.md + AGENTS.md entries are canonical SOLE closure record.
- `architecture/action-plans/2026-07-02-unified-semantic-multimodal.md` — prior parent plan.
- `architecture/action-plans/2026-07-10-real-reranker-for-real.md` — hypothetical parent (NOT pre-existing); PR-9 ships the real Client.
- `architecture/benchmarks/search-quality-v1.json` (NEW, PR-13) — canonical benchmark dataset home.
- `internal/infrastructure/qdrant/search/` — Qdrant-side canonical owner of search filter + adapter logic.
- `internal/application/search/` — application-layer canonical owner of SearchProfile + Diversifier (NEW dirs created by PRs 1, 11).
- `internal/infrastructure/indexing/searchtext/` — dense/sparse text composer canonical owner (PRs 4, 6).
- `internal/infrastructure/ai/reranker/` — RerankerClientPort canonical owner (PR 9, 10).

## §8 Wave-flip criterion

`architecture/current.yaml#SEARCH-QUALITY-ROUTING-AND-RERANKING-2026-07-10.status` flips from `pending` → `shipped + exit_signal: true` WHEN:

- (a) All 13 PRs `status: shipped` on `origin/main`, each with its `shipped_sha` + `ship_date` backfilled.
- (b) `architecture/benchmarks/search-quality-v1.json` populated + the metrics runner passes the §5(d) thresholds.
- (c) A produce a 30-day soak report (P5 meta forward-pointer `PR-SEARCH-QUALITY-SOAK-30D`, deadline 2026-10-01): per-bucket recall@10 improvement ≥ 5% over baseline; latency p95 ≤ 500ms; zero-result rate ≤ 5%.
- (d) Operator sign-off on the production reranker server URL.

## §9 Lifecycle audit-trail

Every per-PR closure appends its canonical entry to:
- `CHANGELOG.md ## Unreleased` (mirror §1)
- `AGENTS.md ## Recent cross-cutting closures` (mirror §2)

Per `CANONICAL.md §1` 4-surface godlike/06 SSOT lockstep (action plan + CHANGELOG + AGENTS + canonical ship_sha on `origin/main`).

## §10 Co-authored-by

All commits use the canonical `Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>` trailer per AGENTS.md Git-Lesson-3.

---

## Pre-existing carry-forward preserved (NOT regressions of this wave)

- The 6-item voiceover + app + asset build-issue list per `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-YAML-PARSE-2026-07-04` UNCHANGED (NOT touched by any of the 13 PRs).
- The 5 uncommitted working-tree files (`internal/application/images/fullimages/prompts.go` + `internal/application/images/fullimages/prompts_test.go` + `scripts/bridges/slide_worker.py` + `cmd/archcheck/scan/percheck_voiceover_alias_ban.go` + `cmd/archcheck/scan/percheck_voiceover_alias_ban_test.go`) preserved untouched per AGENTS.md "Pre-existing build issues" carry-forward convention. The wave ships on top via NEW CHANGELOG.md + AGENTS.md entries, never amending the dirty residue.
- The pre-existing `architecture/current.yaml` parse error at L5557+ per `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-YAML-PARSE-2026-07-04` is OUT OF SCOPE for this wave-tracker anchor append; canonical ship_via is action-plan + CHANGELOG + AGENTS per the `PR-POSTPROCESSOR-UNIFICATION-PHASE-4` precedent at ship_sha `4c4550259`.

## Direct-to-main workflow (per AGENTS.md Git-Lesson-2 + Git-Lesson-3 + Git-Lesson-4)

```bash
git -c user.email='agent@pipelinegen.local' \
    -c user.name='PipelineGen Agent' \
    commit -m '<subject>

<body>

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>'
git fetch origin         # race-protection (AGENTS.md Git-Lesson-4/5)
git log --oneline HEAD..@{u}   # must be empty for safe ff-push
git push origin main     # direct-to-main; no branch, no PR, no --force
```
