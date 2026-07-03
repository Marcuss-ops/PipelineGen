# Unified Semantic Multimodal Search — Action Plan

**Date:** 2026-07-02
**Author:** PipelineGen Agent
**Owner:** architecture doc maintainer
**Scope:** FASE-6 architecture verdict follow-through — the canonical Ingestion + Retrieval pipelines for semantic multimodal search, mapped 1:1 to the codebase's canonical files.
**Status:** in_progress (Wave 30, `architecture/current.yaml#id-30`)

---

## TL;DR

The FASE-6 architecture verdict (Italian audit snapshot, June 2026) identified **8 gaps** between the canonical `search.Aggregator` surface and a unified semantic multimodal search pipeline. The action plan lands the recommendation as **two canonical half-pipelines** — **Ingestion** and **Retrieval** — each mapped 1:1 to the files that own its data flow. The Ingestion half is anchored on `internal/application/assets/enrichment/` (typed state machine) and `internal/infrastructure/qdrant/embedders.go` (3 adapter channels). The Retrieval half is anchored on `internal/app/search_backends.go` (composition bridge), `internal/application/search/aggregator.go` (fanout + dedup + ranking), and `internal/application/mediasearch/` (orchestrator + signed delivery). The diagram below is the single source of truth for "where does a query flow / where does an asset get embedded".

---

## 1. Recommendation — ASCII Diagram (Ingestion + Retrieval)

```
                                  INGESTION  (asset -> vector)
                                  ─────────────────────────────────────────────────

  ┌─────────────────┐   ┌──────────────────────────────┐   ┌──────────────────────┐
  │ VLM Sidecar     │   │ internal/infrastructure/     │   │ Qdrant v3 IndexSchema│
  │ (Python)        │   │ qdrant/embedders.go          │   │ (per channel)        │
  │                 │   │                              │   │                      │
  │ /embed_visual   │──▶│  ImageEmbedderAdapter  ──────│──▶│  visual  (SigLIP     │
  │ /embed_audio    │──▶│  AudioEmbedderAdapter  ──────│──▶│   so400m, 768d)      │
  │ /embed_text     │──▶│  TextEmbedderAdapter   ──────│──▶│  audio   (CLAP       │
  │                 │   │  ────────────────────────    │   │   HTSAT, 512d)       │
  │                 │   │  ErrChannelUnavailable on    │   │  text    (E5-base,   │
  │                 │   │  HTTP 501 (model not loaded) │   │   768d)              │
  └─────────────────┘   └──────────────────────────────┘   └──────────────────────┘
                                       │                           ▲
                                       │ (a) sets EmbedState        │ (b) await
                                       ▼     on media_assets        │     outbox
  ┌──────────────────────────────────────────────────────────────────────────────┐
  │ internal/application/assets/enrichment/                                      │
  │ ────────────────────────────────────────                                     │
  │                                                                              │
  │  ports.go                                                                     │
  │  ────────                                                                     │
  │  EnrichRepositoryPort        SetEnrichState / GetEnrichState                 │
  │  EnrichStateMachinePort      Transition / MarkPending /                     │
  │                              ClaimForEnrichment /                            │
  │                              MarkEnriched / MarkFailed                        │
  │                                                                              │
  │  state_machine.go                                                             │
  │  ───────────────                                                             │
  │  *EnrichStateMachine  (validEdges closed-set)                                │
  │                                                                              │
  │      ┌──────────┐   ┌──────────┐   ┌──────────┐                               │
  │      │ PENDING  │──▶│ENRICHING │──▶│ENRICHED  │  (terminal success)         │
  │      │          │   │          │   └──────────┘                               │
  │      │          │   │          │──▶┌──────────┐                               │
  │      │          │   │          │   │ FAILED   │  (terminal — admin-reset)    │
  │      │          │   └──────────┘   └──────────┘                               │
  │      └──────────┘                                                           │
  │      FAILED ─── admin-reindex only ──▶ PENDING (godlike/07)                 │
  │                                                                              │
  │  errors.go:  4 sentinels + IllegalEnrichTransitionError{From,To}             │
  └──────────────────────────────────────────────────────────────────────────────┘
                                       │
                                       │ (c) outbox event
                                       ▼
                              ┌──────────────────┐
                              │ media_assets     │
                              │  + outbox_event  │
                              │  (SQLite, SSOT)  │
                              └──────────────────┘


                                  RETRIEVAL  (query -> ranked hits)
                                  ─────────────────────────────────────────────────

  ┌──────────┐
  │ Client   │  POST /internal/v1/media/search  (JSON: {query, mode, limit, filters})
  │ (HTTP)   │
  └────┬─────┘
       │
       ▼
  ┌──────────────────────────────────────────────────────────────────────────────┐
  │ internal/application/mediasearch/                                            │
  │ ────────────────────────────────────                                         │
  │                                                                              │
  │  types.go                                                                    │
  │  ────────                                                                    │
  │  MediaSearchRequest / Response / QueryEcho / SearchHit                       │
  │  MediaSearchFilter (= search.Filters; godlike/06 type-identity alias)        │
  │  WorkspaceContext  {WorkspaceID, ProjectID, PrincipalID, IsAdmin}            │
  │  SearchMode        (= search.SearchMode; godlike/06 type-identity alias)     │
  │                                                                              │
  │  ports.go                                                                    │
  │  ────────                                                                    │
  │  VectorSearchPort     (Embed + VectorStore)         [DEPRECATED → Fase 6]    │
  │  MediaReadRepository  (GetMany, workspace+states)                            │
  │  AssetDeliveryService (BuildAuthorizedURL, signed)                           │
  │                                                                              │
  │  Service.Search(ctx, MediaSearchRequest) → MediaSearchResponse               │
  │  1. embed query (search.EmbeddingChannelRegistry)                            │
  │  2. fanout to backends (Hybrid mode → VectorStore.HybridSearch)              │
  │  3. hydrate (MediaReadRepository.GetMany, allowStates=ACTIVE)                │
  │  4. mint signed delivery URL (delivery.BuildAuthorizedURL per QDRANT-004)    │
  │  5. assemble MediaSearchResponse with QueryEcho.ChannelsUsed + Mode           │
  └────────────────┬─────────────────────────────────────────────────────────────┘
                   │
                   ▼
  ┌──────────────────────────────────────────────────────────────────────────────┐
  │ internal/app/search_backends.go      (composition-only bridge per Wave 19)   │
  │ ──────────────────────────────────                                           │
  │                                                                              │
  │  BuildSearchBackends(opts) → *search.BackendRegistry (Frozen)                │
  │  BuildCanonicalSearchFanOut(opts) → search.SearchFanOut + registry           │
  │                                                                              │
  │   providerSearchBackend  (artlist, youtube, stock)                            │
  │   localSearchBackend     (sqassets.ClipsRepository → AdvancedSearchRepo)      │
  │   semanticSearchBackend  (5-channel EmbeddingChannelRegistry +               │
  │                            VectorStore + MediaRepo + Delivery)                │
  │                                                                              │
  │  fail-closed: every Register error aborts the build (no partial coverage)    │
  │  graceful-degradation: nil Embeddings/VectorStore/MediaRepo/Delivery →      │
  │    semantic backend silently skipped; provider + local remain registered     │
  └────────────────┬─────────────────────────────────────────────────────────────┘
                   │
                   ▼
  ┌──────────────────────────────────────────────────────────────────────────────┐
  │ internal/application/search/aggregator.go                                    │
  │ ──────────────────────────────────────────                                   │
  │                                                                              │
  │  Aggregator.Search(ctx, q)                                                   │
  │  1. Decode cursor  → SkipSet                                                 │
  │  2. Trim text + clamp limit (DefaultLimit..MaxLimit)                          │
  │  3. Pick eligible backends (BackendRegistry.Eligible(q))                     │
  │     (text ∩ backend caps; MediaTypes ∩ Capabilities)                         │
  │  4. Fan-out via pkg/concurrent errgroup:                                      │
  │     per-backend timeout (local 2s, provider 5s, semantic 8s)                 │
  │     per-backend errors demoted to ProviderErrors + Partial (no cancel)       │
  │  5. Merge candidates  (4-key dedup: source|SourceRef+hash)                    │
  │  6. RankByScore  (Score DESC, Source ASC, AssetID ASC)                        │
  │  7. Trim to q.Limit + EncodeCursor(merged)                                   │
  │  8. Return Result{Items, NextCursor, ProviderErrors, Partial}                 │
  │                                                                              │
  │  typed errors:  ErrNoEligibleBackends / ErrSemanticBackendUnavailable /      │
  │                 ErrAllBackendsFailed / ErrCursorEncoding                      │
  └────────────────┬─────────────────────────────────────────────────────────────┘
                   │
                   ├──────┐
                   │      │ (5-channel embedding)
                   ▼      ▼
  ┌────────────────────┐  ┌──────────────────────────────────────────────────────┐
  │ search.QueryEmbedder│  │  search.EmbeddingChannelRegistry                    │
  │  (legacy, single-  │  │  Embed(ctx, channel, text) ([]float32, error)        │
  │   text)             │  │  5 closed channels: text/transcript/visual/audio/    │
  │  DEPRECATED         │  │                       sparse (bm25_text)             │
  └────────────────────┘  │  3 sentinels: ErrChannelUnknown /                    │
                         │              ErrChannelNotConfigured /               │
                         │              ErrChannelNotApplicable                 │
                         │  concrete: newEmbeddingRegistryAdapter at            │
                         │              internal/app/adapters_infra.go           │
                         └──────────────────────────────────────────────────────┘
```

---

## 2. Canonical-file mapping (1:1 with the diagram)

| Diagram box | Canonical file | Role |
|-------------|---------------|------|
| **VLM Sidecar (Python)** | `scripts/services/embedding_server/{text,visual,audio}.py` | Out-of-process embedding inference (HTTP). Out of scope for this plan; tracked at `architecture/audits/qdrant-cutover-2026-06-29.md`. |
| **`qdrant/embedders.go`** | `internal/infrastructure/qdrant/embedders.go` | 3 adapter channels (TextEmbedder / ImageEmbedder + AudioEmbedder) wrapping the Python sidecar via `asset.Embedder` + `ImageEmbedderConfig{ServerURL, Timeout}` + `AudioEmbedderConfig{ServerURL, Timeout}`. Returns `ErrChannelUnavailable` on HTTP 501 (model not loaded). |
| **`qdrant/embedders.go` — Model validation** | `internal/infrastructure/qdrant/embedders.go`::`modelNameMatches` + `IndexSchema.GetDense(channel)` | QDRANT-001 envelope validation: sidecar-returned `model` / `model_version` / `dimensions` must match the canonical `IndexSchema` manifest (vendor-prefix tolerant via last-component match). |
| **Qdrant v3 IndexSchema (per channel)** | `internal/infrastructure/qdrant/schema/schema.go`::`DefaultV3Schema` | 4 dense channels (text 768d, transcript 768d, visual 768d, audio 512d) + 1 sparse (bm25_text). Already declared — zero production-side churn for Phase 4 audio. |
| **Enrichment ports** | `internal/application/assets/enrichment/ports.go` | Pattern 0: `EnrichRepositoryPort` (SetEnrichState + GetEnrichState on `media_assets.enrich_state` column) + `EnrichStateMachinePort` (Transition + MarkPending + ClaimForEnrichment + MarkEnriched + MarkFailed). godlike/06 SSOT. |
| **Enrichment state machine** | `internal/application/assets/enrichment/state_machine.go`::`*EnrichStateMachine` + `validEdges` | Typed 4-state machine (PENDING/ENRICHING/ENRICHED/FAILED) with `validEdges` closed-set. FAILED is terminal-sink; recovery is operator-triggered admin-reindex (godlike/07 explicit-retry-via-admin). |
| **Enrichment typed errors** | `internal/application/assets/enrichment/errors.go` | 4 sentinels (`ErrEnrichStateNotWired` / `ErrIllegalEnrichTransition` / `ErrEnrichStateMissing` / `ErrEnrichAssetIDRequired`) + `IllegalEnrichTransitionError{From, To}` typed envelope supporting `errors.Is` + `errors.As` probes. |
| **media_assets + outbox** | `migrations/sqlite/123_enrich_state.sql` + `internal/infrastructure/database/sqlite/assets/clips_enrich_state.go` | SQLite SSOT: `enrich_state` column on `media_assets` (migration 123) + atomic UPDATE primitive. Outbox dispatcher decouples VLM writeback from synchronous embedding call. |
| **Mediasearch types** | `internal/application/mediasearch/types.go` | `MediaSearchRequest{Query, Mode, Limit, MinScore, Filters, Workspace}` + `MediaSearchResponse{OK, Query, Count, Hits, RequestID, IndexVersion}` + `SearchHit{AssetID, Score, MatchedChannels, ...}` (NEVER carries local_path — QDRANT-004 SSOT) + `QueryEcho{Normalized, ChannelsUsed, Mode}`. |
| **Mediasearch ports** | `internal/application/mediasearch/ports.go` | `MediaReadRepository.GetMany(workspace, assetIDs, allowStates)` (batched SQLite hydration, lifecycle allowlist at SQL layer) + `AssetDeliveryService.BuildAuthorizedURL(workspace, assetID)` (HMAC-SHA256 short-TTL signed URL). |
| **Mediasearch service** | `internal/application/mediasearch/` (`service.go` not in this plan — see QDRANT-004) | Orchestrator: embed → fanout → hydrate → mint signed URL → assemble `MediaSearchResponse`. |
| **Composition bridge (backends)** | `internal/app/search_backends.go` | `BuildSearchBackends(opts) → *search.BackendRegistry` (Frozen). Registers `providerSearchBackend` + `localSearchBackend` + `semanticSearchBackend`. Composition-only bridge per Wave 19 rule. |
| **Composition bridge (fanout + telemetry)** | `internal/app/search_backends.go`::`BuildCanonicalSearchFanOut` | Wraps the registry in `*search.Aggregator` + the canonical `search.SearchFanOut` (telemetry decorator). Single composition seam for ALL search entry points (YouTube + Assets + Mediasearch + FindDuplicates). |
| **Canonical aggregator** | `internal/application/search/aggregator.go`::`Aggregator` | 8-step PR 9 pipeline (cursor decode → eligible backends → fanout via errgroup → merge dedup → rank → cursor encode → return). Partial-preferred (single backend errors do not cancel the response). |
| **Per-backend timeouts** | `internal/application/search/aggregator.go`::`PerBackendTimeout` | local 2s, provider 5s, semantic 8s. Composition root overrides via `Aggregator.SetPerBackendTimeouts`. |
| **Typed errors (aggregator)** | `internal/application/search/aggregator.go`::errors + `internal/application/search/errors.go` (cross-ref) | `ErrNoEligibleBackends` / `ErrSemanticBackendUnavailable` (hybrid mode) / `ErrAllBackendsFailed` / `ErrCursorEncoding`. fail-closed contract. |
| **Legacy single-text embedder (DEPRECATED)** | `internal/application/search/ports.go`::`QueryEmbedder` | `Embed(ctx, text)` — single-channel. DEPRECATED in favour of `EmbeddingChannelRegistry` (Fase 6 split per `architecture/current.yaml#id-30 PR-EMBEDDING-CHANNEL-REGISTRY`). Removal deadline 2026-08-15. |
| **Canonical 5-channel registry** | `internal/application/search/ports.go`::`EmbeddingChannelRegistry` + `ChannelEncoder` | `Embed(ctx, channel, text) ([]float32, error)` — 5 closed channels (text / transcript / visual / audio / sparse). 3 sentinels. Concrete at `internal/app/adapters_infra.go::newEmbeddingRegistryAdapter`. |
| **Qdrant client (vector store)** | `internal/infrastructure/qdrant/searcher.go` + `internal/application/assets/search/ports.go::VectorStorePort` | `Search(ctx, VectorSearchRequest) ([]VectorSearchResult, error)` + `HybridSearch(ctx, HybridSearchRequest)`. 4 keys (QdrantPointID + dense + transcript + sparse) per QDRANT-001. |
| **Qdrant client (upsert from clips)** | `internal/infrastructure/qdrant/index_writer.go` + `internal/infrastructure/qdrant/payload_mapper.go` | `UpsertFromClips(ctx, clipIDs)` reads `media_assets` (incl. `audio_embedding` per Phase 4) → `IndexDocumentToPoint` (case ChannelAudio per payload_mapper.go:412) → Qdrant upsert via outbox. |
| **SQLite hydration (ClipsRepository)** | `internal/infrastructure/database/sqlite/assets/clips_repository.go` | `GetMany(ctx, workspace, assetIDs, allowStates)` — batched SQL with `lifecycle_state IN (allowStates)` filter. The post-query guard in `mediasearch.Service` layers defence-in-depth (PR 1 — Lifecycle state SSOT). |

---

## 3. Verdict → closure mapping (8 gaps → canonical files)

Per `architecture/current.yaml#id-30` verdict (Italian audit snapshot, June 2026) — 8 gaps, each closed by a specific file in this action plan:

| Verdict | Gap | Closed by | Status |
|---------|-----|-----------|--------|
| §1 | semantic backend non collegato | `internal/app/search_backends.go`::`semanticSearchBackend` + `BuildSearchBackends` | **CLOSED** (FASE-6) |
| §2 | mode hybrid\|ann decorativo | `internal/application/search/aggregator.go`::`Aggregator.Search` (branches on `q.Mode`) | **CLOSED** (FASE-6) |
| §3 | QueryEmbedder non collegato | `internal/application/search/ports.go`::`EmbeddingChannelRegistry` (5 channels) + `internal/app/adapters_infra.go::newEmbeddingRegistryAdapter` | **CLOSED** (FASE-6 + PR-EMBEDDING-CHANNEL-REGISTRY) |
| §4 | text-to-visual cross-modale | `internal/infrastructure/embeddings/siglip_text_embedder.go` (SigLIP text encoder, 768d) | **CLOSED** (PR-CROSS-MODAL-TEXT-TO-VISUAL) |
| §5 | canale audio non completo | `internal/infrastructure/indexing/clipindexer/indexing_api.go` (Phase 4) + `internal/infrastructure/qdrant/embedders.go::audioEmbedderAdapter` | **CLOSED** (PR-AUDIO-CHANNEL-EXTENSION) |
| §6 | filtri non uniformi | `internal/app/search_backends.go` (provider reads `q.Filters.MediaType`; local forwards `Category` + `Language` + `Tags`) | **CLOSED** (PR-AGGREGATE-FILTER-UNIFORM) |
| §7 | workspace propagation assente | `internal/application/mediasearch/types.go::WorkspaceContext` + `internal/application/mediasearch/ports.go::MediaReadRepository.GetMany(workspace, ...)` | **CLOSED** (PR-AGENTE2-ACTOR) |
| §8 | preview URL non firmata | `internal/application/mediasearch/ports.go::AssetDeliveryService.BuildAuthorizedURL` (HMAC-SHA256) | **CLOSED** (FASE-6) |

**Net status:** 8/8 gaps closed on `origin/main` as of 2026-07-02. The wave-tracker entry `architecture/current.yaml#id-30` remains `status: in_progress / exit_signal: false` because the 4 forward-pointer `linked_issues` (PR-AGGREGATE-FILTER-UNIFORM / PR-CROSS-MODAL-TEXT-TO-VISUAL / PR-ENRICHMENT-STATE-MACHINE / PR-E2E-SEMANTIC-MULTIMODAL-TEST) carry their own deadlines (2026-07-25 / 2026-08-01 / 2026-08-15 / 2026-07-02 respectively); the wave flips to `done / exit_signal: true` once all four linked_issues reach `status: shipped`.

---

## 4. Honest limitation declaration (godlike/07)

1. **The diagram reflects the production state as of `origin/main` HEAD (commit `357714db` post-rebase).** Future canonical additions (e.g. a 6th embedding channel beyond the 5 closed-set) require an additive Phase 5 PR — the diagram will be updated in lockstep.
2. **`scripts/services/embedding_server/{text,visual,audio}.py` is the Python sidecar** the Go adapters call into. The Go surface (`internal/infrastructure/qdrant/embedders.go`) is the canonical seam; the Python sidecar is treated as an external HTTP dependency. Per `architecture/audits/qdrant-cutover-2026-06-29.md`, the sidecar is out of scope for this Go-side plan.
3. **The aggregator's per-backend timeouts (local 2s, provider 5s, semantic 8s) are PR 9 spec defaults** that the composition root can override via `Aggregator.SetPerBackendTimeouts`. Production deployments should measure the p95 + p99 of each backend and tune the timeouts per `architecture/audits/search-aggregator-timeouts-2026-06-30.md` (forward-pointer).
4. **The semantic backend's 4-port contract** (`Embeddings` + `VectorStore` + `MediaRepo` + `Delivery` per `BuildSearchBackends`) requires ALL FOUR to be non-nil for registration. When any one is nil, the semantic backend is silently skipped and the aggregator falls back to provider + local only (graceful degradation). Operators should monitor the "semantic backend not registered" log line as a deployment-readiness signal.
5. **The enrichment state machine's FAILED terminal-sink** means operator intervention is REQUIRED for recovery (admin-reindex endpoint). The VLM sweeper does NOT auto-retry FAILED rows — per `architecture/current.yaml#PR-ENRICHMENT-STATE-BACKFILL` forward-pointer, the admin endpoint is a separate PR.
6. **Pre-existing build issues carry forward unchanged** (per AGENTS.md "minimal blast radius"): `workerruntime / composition.go / module_media.go / worker_registry_e2e_test.go / composition_test.go / creator_runtime_test.go` — 5-item carry-forward list (out of scope for this plan; tracked in CHANGELOG forward-pointer).

---

## 5. Forward-pointers (follow-up actions)

1. **PR-ENRICHMENT-STATE-TIER-1-WIRING** (deadline 2026-09-15) — BACKFILL wave for the enrichment state machine. Threads the live `*EnrichStateMachine` into the composition root (autotag.Service.WithEnrichStateMachine setter) + invokes `ClaimForEnrichment / MarkEnriched / MarkFailed` at the VLM sweep seam. Until that lands, the EXPAND-phase discipline holds: ingest stamps PENDING; the VLM sweeper reads the typed-state filter; the typed state-machine is the canonical surface but not yet invoked at the sweep seam.
2. **PR-SEARCH-AGGREGATOR-TIMEOUTS-TUNING** (deadline pending) — measure p95 + p99 of each backend (`local` / `semantic` / per-provider) against production traffic; update `DefaultLocalBackendTimeout` / `DefaultProviderBackendTimeout` / `DefaultSemanticBackendTimeout` in `internal/application/search/aggregator.go` per measured data. Operators should NOT flip the timeouts without data backing.
3. **PR-ENRICHMENT-ADMIN-REINDEX-ENDPOINT** (deadline pending) — implements the operator-triggered `FAILED→PENDING` reset path so the VLM sweeper can re-claim a failed row. Without this, FAILED is a true sink and operators must manipulate SQLite directly. Per godlike/07 no-fake-availability, this endpoint MUST be explicit and audited (typed `MarkPending` on a row that was previously `FAILED` requires admin RBAC + audit log).
4. **E2E smoke on `/internal/v1/media/search`** (deadline pending) — exercise the full Retrieval half of the diagram against a live server + populated Qdrant + populated SQLite. The `TestE2E_SemanticSearchMultimodal` suite (`internal/application/mediasearch/e2e_semantic_test.go`, 4/4 green) covers the in-test orchestrator surface; a real-server smoke test (`pkg/veloxclient.SubmitAsync` against `localhost:8080`) is the next step.
5. **`scripts/ci-architectural-checks.sh` Check update** (deadline pending) — forward-prevention gate that bans direct `internal/infrastructure/qdrant.embedders.go` callers from `internal/application/**` (mirrors the Check 51+52+53 posture for the audio/artifact/complete-job surfaces). Until this lands, drift is possible via a hand-rolled `http.Post` to the sidecar URL bypassing the adapter.

---

## 6. Wave-tracker entry cross-references

This action plan + the wave-tracker entry are the canonical audit surfaces for the unified semantic multimodal search. The slim-shape `architecture/current.yaml#id-30` carries the live tracker; this `architecture/action-plans/2026-07-02-unified-semantic-multimodal.md` carries the narrative (current.yaml slim schema strips narrative per the zero-legacy policy).

Predecessor action plans that share the Ingestion + Retrieval surface:

- `architecture/action-plans/2026-06-29-qdrant-cutover.md` (FASE-6) — Qdrant sidecar envelope canonization + point ID canonization (QDRANT-001 baseline). Predecessor of PR-EMBEDDING-CHANNEL-REGISTRY.
- `architecture/action-plans/2026-07-01-image-territories-cutover.md` (FASE-8) — image-territories cycle break + reorganization. Out of scope for this plan; cited for the image-side forward-pointer only.

Successor action plans tracked under `architecture/current.yaml#id-30.linked_issues`:

- `PR-AGGREGATE-FILTER-UNIFORM` (deadline 2026-07-25) — provider backend reads `q.Filters.MediaType`; local backend forwards `Category` + `Language` + `Tags` to SQLite FTS-fallback LIKE conditions via `pkg/sqlutil.BuildFallbackLikeConditions` per AGENTS.md FTS5 ban.
- `PR-CROSS-MODAL-TEXT-TO-VISUAL` (deadline 2026-08-01) — visual channel uses SigLIP-text encoder (768d) so text query and visual-encoded frame live in the SAME vector space. **SHIPPED** per `architecture/current.yaml#id-30 PR-CROSS-MODAL-TEXT-TO-VISUAL.status: done`.
- `PR-ENRICHMENT-STATE-MACHINE` (deadline 2026-08-15) — canonical 4-state typed enum (PENDING/ENRICHING/ENRICHED/FAILED) for `media_assets.enrich_state` (migration 123) + typed state-machine wrapper. **SHIPPED** per `architecture/current.yaml#id-30 PR-ENRICHMENT-STATE-MACHINE.status: done`.
- `PR-E2E-SEMANTIC-MULTIMODAL-TEST` (deadline 2026-07-02) — `TestE2E_SemanticSearchMultimodal` (4/4 subtests green) covers video / image / audio / music with a never-in-metadata phrase + asserts score > 0.5 + signed delivery URL. **SHIPPED** per `architecture/current.yaml#id-30 PR-E2E-SEMANTIC-MULTIMODAL-TEST.status: shipped`.
- `PR-AUDIO-CHANNEL-EXTENSION` (deadline 2026-07-02) — Phase 4 audio embedding (CLAP-HTSAT 512d) added to `indexViaAPI`. **SHIPPED** per `architecture/current.yaml#id-30 PR-AUDIO-CHANNEL-EXTENSION.status: shipped` (commit `357714db`).
- `CANONICAL-DRIFT-MIG094` (deadline 2026-07-25) — pre-existing drift in `internal/infrastructure/database/canonical.go::CanonicalMediaAssetsSchema` (missing migration 094 columns). Out of scope for this plan; non-blocking; tracked separately.

---

## 7. Author + sign-off

- **Author:** PipelineGen Agent
- **Date:** 2026-07-02
- **Owner:** architecture doc maintainer
- **Co-authored-by:** PipelineGen Agent `<agent@pipelinegen.local>` (per AGENTS.md Git-Lesson-3)
- **Commit (planned):** `docs(architecture): 2026-07-02 unified-semantic-multimodal action plan` (direct-to-main per AGENTS.md Git-Lesson-2; Co-authored-by trailer; no --force)
- **Audit-pin canonical anchor:** `architecture/current.yaml#id-30` is the live wave-tracker; this action plan is the narrative companion.
