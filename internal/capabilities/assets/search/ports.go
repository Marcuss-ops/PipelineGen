// Package search — ports.go is the slim interface surface after
// PR-SEARCH-PORTS-SPLIT (2026-07-04, pre-deadline 49 days early).
//
// What lived here pre-split (674 LoC god file): SearchBackend interface +
// BackendRegistry struct + Logger port + SearchDocument + 8 sentinels +
// QueryEmbedder + 5 channel constants + 3 channel-typed errors +
// EmbeddingChannelRegistry + MediaAsset + MediaReadRepository +
// AssetDeliveryService + SearchableLifecycleStates.
//
// What lives here post-split (~250 LoC): SearchBackend interface +
// Logger port + QueryEmbedder + 5 channel constants + CanonicalChannelNames
// + IsKnownChannel + ChannelEncoder + EmbeddingChannelRegistry +
// MediaReadRepository + AssetDeliveryService + SearchableLifecycleStates.
//
// The 5 extracted surfaces land in 5 new sibling files per AGENTS.md
// Pattern 5 (capability-stable file split):
//   - registry.go (NEW) — BackendRegistry struct + Register/Freeze/All/Eligible
//   - errors.go (canonical SSOT) — all 15+ sentinels (was scattered across
//     ports.go + types.go + errors.go pre-split)
//   - types_query.go (NEW) — Capability + SearchMode + Filters + Cursor +
//     DefaultLimit/MaxLimit + Actor + Query (moved from types.go)
//   - types_result.go (NEW) — Candidate + Result (moved from types.go)
//   - document.go (NEW) — SearchDocument + AsPayloadMap + MediaAsset
//
// Phase 6 Spina Dorsale (July 2026) — the three territories that mirror
// godlike/06 "one owner per fact" rule:
//
//	┌─ SemanticEnrichment ───────────────────────────────────────────┐
//	│  Asset → SearchDocument                                       │
//	│   Owners: artlist/semantic_enricher.go (Artlist provider),    │
//	│           images/metadata_service.go (pipeline images),       │
//	│           clipindexer (YouTube clips).                        │
//	│   Output: SearchDocument{AssetID, QdrantPointID, Payload...} │
//	└────────────────────────────────────────────────────────────────┘
//
//	┌─ IndexProjection ──────────────────────────────────────────────┐
//	│  SearchDocument → Qdrant payload                               │
//	│   Owner: platform/qdrant/index_writer.go                 │
//	│           (implements clipindexer.VectorStoreIndexer)          │
//	│   Bridge: SearchDocument is a typed envelope that mirrors the  │
//	│           Qdrant IndexSchema fields 1:1 (no Locator leak).    │
//	└────────────────────────────────────────────────────────────────┘
//
//	┌─ MediaSearch ───────────────────────────────────────────────────┐
//	│  SearchRequest → []SearchHit                                   │
//	│   Orchestrator: search.Aggregator (registered SearchBackends). │
//	│   Ports consumed per backend path:                             │
//	│     - QueryEmbedder   (Fase 6: NEW, separates from store)     │
//	│     - VectorStorePort (assets/search, locator-free, ANN+RRF)  │
//	│     - MediaReadRepository + AssetDeliveryService (hydratation) │
//	│   Surface: GET /internal/v1/media/search (mediasearch handler) │
//	└────────────────────────────────────────────────────────────────┘
package search

import "context"

// SearchBackend is the contract every aggregator backend satisfies.
//
// A SearchBackend alone is not responsible for cross-tenant filtering,
// indexing, or asset lifecycle transitions — it just runs a query
// against one source and returns up to req.Limit candidates that survive
// the backend's own scoring. The aggregator owns dedup, ranking, and
// cursor stability across the whole backend fan-out.
//
// Implementations live under internal/app (composition root owns the
// ONLY capability-crossing adapters per Wave 19):
//   - providerBackendAdapter   wraps providers.SearchProvider (SSOT registry)
//   - localBackendAdapter      wraps assets.ClipsRepository (kernel search)
//
// PR-SEARCH-LEGACY-MEDIASEARCH-BACKEND-REMOVAL (June 2026): the
// historical `semanticBackendAdapter` wrapping *mediasearch.Service
// is git-rm'd. The surviving `mediasearch` package is a thin
// re-export surface (WorkspaceContext + AssetDeliveryService +
// MediaSearch{Request,Response,Filter} + SearchMode alias) for the
// 4 remaining callers; workspace-gated semantic routing now lives
// directly inside search.Aggregator paths (per-scope), not in a
// dedicated Service composition.
type SearchBackend interface {
	// Name returns the human-readable backend identifier
	// (e.g. "youtube","artlist","local","semantic"). Must be unique
	// within a BackendRegistry. Stable across calls. Empty Name
	// triggers the registry's typed-nil-and-empty guards.
	Name() string

	// Capabilities advertises which MediaTypes this backend returns.
	// Used by BackendRegistry.Eligible to filter by Query.MediaTypes.
	// Eg. a video-only provider returns []Capability{CapVideo}.
	Capabilities() []Capability

	// Universe reports which search universe this backend serves.
	//   - SearchCatalog   : canonical index (semantic Qdrant backend +
	//     local SQLite backend); never makes a live provider call.
	//   - SearchDiscovery: live providers (artlist, youtube, stock,
	//     images); never queries Qdrant.
	// SearchBlended is a QUERY-level value only and MUST NOT be
	// returned here. BackendRegistry.Eligible filters by this value
	// according to Query.EffectiveUniverse().
	Universe() SearchUniverse

	// Search runs the query and returns up to req.Limit candidates
	// matching q. The aggregator respects ctx.Done(): the backend
	// MUST honour cancellation and avoid leaking goroutines after
	// the call ends. Errors propagate to Result.ProviderErrors[name]
	// with Result.Partial = true (the aggregator NEVER fails the
	// whole search on a single backend error; partial is preferred).
	Search(ctx context.Context, q Query) ([]Candidate, error)
}

// ── Logger port ─────────────────────────────────────────────────────
//
// Logger is the narrow logging surface used by Aggregator + adapters.
// Type compatibility: search.Logger has the same shape as the
// existing assets/search.Logger; production adapters usually
// implement it via zapLogAdapter (see internal/app/assets_adapters.go).
type Logger interface {
	Info(msg string, keysAndValues ...any)
	Warn(msg string, keysAndValues ...any)
	Debug(msg string, keysAndValues ...any)
	Error(msg string, keysAndValues ...any)
}

// noopLogger swallows every log call; used when callers pass nil
// and in tests where noise must be zero. Mirrors other noop loggers
// in internal/application/*.
type noopLogger struct{}

func (noopLogger) Info(string, ...any)  {}
func (noopLogger) Warn(string, ...any)  {}
func (noopLogger) Debug(string, ...any) {}
func (noopLogger) Error(string, ...any) {}

// ── QueryEmbedder (Fase 6 Spina Dorsale) ────────────────────────────

// QueryEmbedder is the application-layer port owned by the MediaSearch
// territory. It performs textual query embedding for live retrieval.
//
// Fase 6 (July 2026, Spina Dorsale): extracted from the historical
// merged VectorSearchPort mediator which combined embedding + retrieval
// behind a single interface. The split unblocks Phase 7 / Phase 8
// (off-line batch indexers no longer need to wire an embedder; the
// IndexProjection territory uses producers' pre-computed embeddings
// directly via clipindexer.VectorStoreIndexer.UpsertFromClip).
//
// Method shape mirrors internal/platform/qdrant.TextEmbedder
// (single `Embed(ctx, text) ([]float32, error)`) so the existing
// qdrant.NewTextEmbedderAdapter concrete satisfies this port with no
// adapter translation — the compile-time assertion lives in
// internal/app/adapters_infra.go::var _ search.QueryEmbedder = ...
//
// Future evolution: a typed EmbedQueryForVector(vname string) variant
// is NOT added here; the orchestrator picks the canonical text vector
// (search.VectorConfig.TextVectorName) before calling Embed, so the
// port stays an embedding mechanism, not a vector-routing router.
type QueryEmbedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// ── EmbeddingChannelRegistry (PR-EMBEDDING-CHANNEL-REGISTRY, July 2026) ────────
//
// Per godlike/06 one-canonical-owner-per-fact: the multi-channel
// embedding surface lives on a single typed port. The legacy
// QueryEmbedder is the single-text embedder; the new
// EmbeddingChannelRegistry is the canonical owner of the 5-channel
// vector vocabulary. The semantic backend (search_backend_semantic.go)
// delegates to this port instead of switching on 5 inline encoders,
// so adding a new channel (e.g. SigLIP-text for cross-modal visual
// search per PR-CROSS-MODAL-TEXT-TO-VISUAL) plugs in via composition
// root without touching the backend.

// Canonical channel-name vocabulary. Closed set; canonical SSOT for
// the multi-channel embedding surface (godlike/06). Qdrant vector
// names ("text"/"transcript"/"visual"/"audio"/"bm25_text") MUST match
// these constants 1:1 at the wire adapter layer so the registry,
// the schema, and the orchestrator agree byte-for-byte. The
// sparse channel uses the wire-level "bm25_text" name because that
// is what Qdrant expects for server-side BM25 inference
// (see internal/platform/qdrant/client_search.go::SparseText
// + SparseVectorName pair semantics).
const (
	ChannelText       = "text"       // 768d intfloat/multilingual-e5-base (semantic meaning)
	ChannelTranscript = "transcript" // 768d intfloat/multilingual-e5-base (Whisper transcript content)
	ChannelVisual     = "visual"     // 768d SigLIP-text encoder (forward-pointer; PR-CROSS-MODAL-TEXT-TO-VISUAL)
	ChannelAudio      = "audio"      // 512d CLAP-text encoder (forward-pointer; PR-CROSS-MODAL-TEXT-TO-VISUAL)
	ChannelSparse     = "bm25_text"  // sparse BM25; server-side inference; ERR_NOT_APPLICABLE on query-time
)

// CanonicalChannelNames returns the closed set of channel names
// the registry MUST recognize. Used by registry concrete impls
// to differentiate ErrChannelUnknown from ErrChannelNotConfigured.
// Order matches the canonical semantic-of-source: text-first.
func CanonicalChannelNames() []string {
	return []string{ChannelText, ChannelTranscript, ChannelVisual, ChannelAudio, ChannelSparse}
}

// IsKnownChannel reports whether name is in CanonicalChannelNames().
// Composition-root registry impls SHALL use this to distinguish the
// godlike/07 typed-error contract:
//   - name unknown → ErrChannelUnknown
//   - known but no adapter wired → ErrChannelNotConfigured
func IsKnownChannel(name string) bool {
	switch name {
	case ChannelText, ChannelTranscript, ChannelVisual, ChannelAudio, ChannelSparse:
		return true
	default:
		return false
	}
}

// ChannelEncoder is the per-channel adapter contract consumed by
// EmbeddingChannelRegistry concrete impls. Each adapter implements
// text-query to channel-vector encoding. The implementation is
// hidden behind the application-layer port (godlike/06 SSOT);
// composition root wires the concrete adapters (typically wrapping
// the existing qdrant.TextEmbedder for text+transcript channels and
// stubbing the rest).
type ChannelEncoder interface {
	EmbedTextQuery(ctx context.Context, text string) ([]float32, error)
}

// EmbeddingChannelRegistry is the canonical multi-channel embedding port.
//
// Single text input per call (per architecture/current.yaml#id-30
// PR-EMBEDDING-CHANNEL-REGISTRY spec). The semantic backend delegates
// to this port instead of switching on 5 inline encoders:
//   - text channel today: qdrant.TextEmbedder wrapped as ChannelEncoder
//   - transcript: same TextEmbedder (transcript content is text)
//   - visual: forward-pointer stub returning ErrChannelNotConfigured
//     until PR-CROSS-MODAL-TEXT-TO-VISUAL lands a SigLIP-text encoder
//   - audio: forward-pointer stub returning ErrChannelNotConfigured
//     until PR-CROSS-MODAL-TEXT-TO-VISUAL lands a CLAP-text encoder
//   - sparse: forward-pointer stub returning ErrChannelNotApplicable
//     because Qdrant handles BM25 inference server-side via SparseText
//
// godlike/06 SSOT: this port is the canonical owner of the
// channel-name vocabulary. New encoders plug in by adding a new
// channel constant + wiring a ChannelEncoder at composition root;
// the semantic backend does NOT change.
type EmbeddingChannelRegistry interface {
	EmbedQuery(ctx context.Context, channel string, text string) ([]float32, error)
}

// ── Canonical MediaReadRepository + AssetDeliveryService (Commit 3-A, July 2026) ───────────
//
// Per Commit 2 BACKFILL/CUTOVER (which promoted the typed-error sentinels
// from mediasearch → search), Commit 3-A promotes the canonical ports so
// the search capability owns its hydration + delivery seams.
//
// godlike/06 SSOT: the canonical owner of MediaReadRepository +
// AssetDeliveryService is the SEARCH capability (this file). The
// legacy `internal/application/mediasearch.MediaReadRepository` /
// `.AssetDeliveryService` remain pointer-identical Go-level interface
// aliases of these canonical surfaces (errors.Is + structural
// compatibility through the type-identity rule) until Commit 3-B folds
// the 14 production consumers. Per the godlike/07 EXPAND-phase
// discipline, NEW adapters implement the canonical interfaces; legacy
// ports are read-only during the migration window.
//
// QDRANT-004 invariant: `MediaAsset` (declared in document.go per
// PR-SEARCH-PORTS-SPLIT) deliberately carries NO server-internal
// locator (no LocalPath, no DriveLink, no raw DriveFileID).
// Operator/admin surfaces that legitimately need to surface file paths
// consume `duplicates.DuplicateMatch` from
// `internal/application/assets/duplicates/types.go` (the canonical
// owner per godlike/06 one-owner-per-fact). The `Candidate` shape
// (declared in types_result.go per PR-SEARCH-PORTS-SPLIT) was also
// locked down as part of Commit 3-A: LocalPath + DriveLink have been
// removed; Find-by-hash matchups surface the duplicate match via a
// separate path.
//
// Both canonical ports consume `search.Actor` (the canonical tenant
// identity owned by the search capability; declared in
// types_query.go per PR-SEARCH-PORTS-SPLIT). The legacy
// `mediasearch.WorkspaceContext{WorkspaceID, ProjectID, PrincipalID,
// IsAdmin}` is retain-only during the migration window: the canonical
// fields are `WorkspaceID` (canonical) + `UserID` (renamed from
// `PrincipalID`) + `IsAdmin`. ProjectID was dropped — workspace
// isolation needs no separate project scoping per godlike/06.

// MediaReadRepository fetches canonical asset metadata from SQLite.
//
// SEARCH-T07-LIFECYCLE-DEL (P0, 2026-07-15, Phase 9 cycle 2 closure):
// the interface NO LONGER takes an `allowStates []string` parameter.
// The canonical ACTIVE-only filter is owned by the search capability
// per godlike/06 one-canonical-owner-per-fact — exposing the
// parameter at the interface boundary re-opens the drift class that
// SEARCH-T07-001 narrowly closed (the pre-PR fail-open path where
// `len(allowSet) > 0` short-circuited the lifecycle filter and let
// deleted/archived/pending rows reach the semantic backend). The
// canonical searchable-projection semantics are pinned at the
// implementation (see internal/app/adapters_media_search.go +
// `SearchableLifecycleStates` for the SSOT constant) — the interface
// is intentionally drift-free.
//
// The post-query guard in search.SemanticBackend layers defence-in-
// depth on top of the SQL pre-filter — without both, a future
// hydrate-on-read drift could re-leak DELETED/DELETE_REQUESTED/
// DRIVE_DELETE_PENDING rows.
//
// Commit 3-A promotion (July 2026): the canonical interface takes
// `search.Actor` (NOT the legacy `mediasearch.WorkspaceContext`). The
// adapter bridging ClipsRepository → MediaReadRepository is the
// composition-root only (Pattern 0 — see internal/app/adapters_media_
// search.go and searchReadAdapter/assetReadAdapter siblings).
//
// Workspace isolation: the implementation MUST apply workspace_id
// filtering from `actor.WorkspaceID` (forward-compatible with the
// QDRANT-001 media_assets.workspace_id column when it lands).
type MediaReadRepository interface {
	GetMany(
		ctx context.Context,
		actor Actor,
		assetIDs []string,
	) ([]MediaAsset, error)
}

// AssetDeliveryService mints short-lived signed URLs that authorise
// a client to download or stream an asset's bytes for a bounded TTL.
//
// Commit 3-A promotion (July 2026): the canonical interface takes
// `search.Actor`. Per QDRANT-004 acceptance criterion "Delivery URL
// protetto", signatures are HMAC-SHA256 with a server-side secret of
// at least 32 bytes (mirror of pkg/hmacsign canonicalisation rules;
// the future GET /api/internal/v1/deliver handler will consume
// the receiver-side Verify helper exported alongside BuildAuthorizedURL).
type AssetDeliveryService interface {
	BuildAuthorizedURL(ctx context.Context, actor Actor, assetID string) (string, error)
}

// SearchableLifecycleStates is the canonical allowlist of
// lifecycle_state values that survive the hydration phase.
//
// Commit 3-A → Commit 3-B (July 2026) promotion: previously lived as
// `mediasearch.SearchableLifecycleStates = []string{"ACTIVE"}`;
// promoted to the canonical search capability owner per godlike/06
// (the search capability is the canonical owner of which lifecycle
// states are surfaced to clients — the domain/asset package owns
// the enum shape but the search capability owns the searchable-projection
// semantics).
//
// The single value is ACTIVE; pre-<aural>-migrations the list carried
// legacy lowercase values (\"active\", \"searchable\") pruned by data
// migrations. Anything outside this set — STAGING, PROCESSING,
// DELETE_PENDING, DELETE_REQUESTED, DRIVE_DELETE_PENDING,
// INDEX_DELETE_PENDING, DELETED — MUST be filtered both in SQL
// (primary, via the canonical MediaReadRepository.GetMany impl,
// which hardcodes this SSOT constant at the call site) and in the
// post-query guard (defence-in-depth, in semanticSearchBackend).
//
// SEARCH-T07-LIFECYCLE-DEL (P0, 2026-07-15): the constant is now the
// SSOT for the SQL pre-filter at the call site. Implementations of
// MediaReadRepository MUST NOT expose a `allowStates` parameter at
// the interface boundary — the canonical ACTIVE-only semantics are
// pinned at the implementation (composition-root adapter) so the
// interface is intentionally drift-free per godlike/06
// one-canonical-owner-per-fact.
var SearchableLifecycleStates = []string{"ACTIVE", "PUBLISHED"}
