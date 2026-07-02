// Package search — narrow port interfaces + canonical domain types for
// the MediaSearch capability. The package is organised into three
// territories that mirror godlike/06 "one owner per fact" rule:
//
//   ┌─ SemanticEnrichment ───────────────────────────────────────────┐
//   │  Asset → SearchDocument                                       │
//   │   Owners: artlist/semantic_enricher.go (Artlist provider),    │
//   │           images/metadata_service.go (pipeline images),       │
//   │           clipindexer (YouTube clips).                        │
//   │   Output: SearchDocument{AssetID, QdrantPointID, Payload...} │
//   └────────────────────────────────────────────────────────────────┘
//
//   ┌─ IndexProjection ──────────────────────────────────────────────┐
//   │  SearchDocument → Qdrant payload                               │
//   │   Owner: infrastructure/qdrant/index_writer.go                 │
//   │           (implements clipindexer.VectorStoreIndexer)          │
//   │   Bridge: SearchDocument is a typed envelope that mirrors the  │
//   │           Qdrant IndexSchema fields 1:1 (no Locator leak).    │
//   └────────────────────────────────────────────────────────────────┘
//
//   ┌─ MediaSearch ───────────────────────────────────────────────────┐
//   │  SearchRequest → []SearchHit                                   │
//   │   Orchestrator: search.Aggregator (registered SearchBackends). │
//   │   Ports consumed per backend path:                             │
//   │     - QueryEmbedder   (Fase 6: NEW, separates from store)     │
//   │     - VectorStorePort (assets/search, locator-free, ANN+RRF)  │
//   │     - MediaReadRepository + AssetDeliveryService (hydratation) │
//   │   Surface: GET /internal/v1/media/search (mediasearch handler) │
//   └────────────────────────────────────────────────────────────────┘
//
// Fase 6 Spina Dorsale (July 2026):
//   - Introduces canonical SearchDocument type that ALL three territories
//     share. Enrichers produce it, the indexer consumes it, the search
//     surface returns hits derived from it.
//   - Promotes embedding generation from the historical merged
//     VectorSearchPort mediator into a dedicated QueryEmbedder port.
//     VectorStorePort (locator-free ANN/hybrid) remains the sole
//     retrieval surface.
//   - mediasearch.VectorSearchPort is marked Deprecated: with the new
//     two-port composition (QueryEmbedder + VectorStorePort) as
//     migration target. Removal is deferred to Fase 7 / Fase 8 once
//     e2e test stubs migrate (see architecture/deprecations.yaml
//     #SEARCH-VECTORSEARCHPORT-MERGE).
package search

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
)

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

	// Search runs the query and returns up to req.Limit candidates
	// matching q. The aggregator respects ctx.Done(): the backend
	// MUST honour cancellation and avoid leaking goroutines after
	// the call ends. Errors propagate to Result.ProviderErrors[name]
	// with Result.Partial = true (the aggregator NEVER fails the
	// whole search on a single backend error; partial is preferred).
	Search(ctx context.Context, q Query) ([]Candidate, error)
}

// ── BackendRegistry ────────────────────────────────────────────────
//
// BackendRegistry is the freezeable backend catalog. Register/Freeze
// run once during composition root wiring; after Freeze() any call
// to Register returns ErrFrozen. Mirrors providers.Registry's
// RWMutex + typed-nil-pointer + Empty-Name contract — same patterns
// mean the same operational guarantees.
type BackendRegistry struct {
	mu      sync.RWMutex
	entries map[string]SearchBackend
	frozen  bool
}

// Sentinel errors. Mirrors providers.Registry so operator muscle
// memory transfers between the two.
var (
	ErrAlreadyRegistered = errors.New("search: backend already registered")
	ErrFrozen            = errors.New("search: registry frozen")
	ErrNilBackend        = errors.New("search: nil backend")
	ErrEmptyName         = errors.New("search: backend Name() returned empty")
)

// ErrMissingWorkspace is returned when the search surface is invoked
// without a workspace in the auth context. The handler maps this to
// HTTP 403 — worker principals cannot bypass through the body.
//
// Commit 2 BACKFILL/CUTOVER (July 2026): promoted from
// mediasearch.ErrMissingWorkspace to the canonical search package
// (godlike/06 SSOT — the search capability owns its own workspace
// enforcement contract). The legacy mediasearch.ErrMissingWorkspace
// is now a Go-level alias of this canonical sentinel (same pointer,
// so errors.Is traverses the chain transparently).
//
// Wraps `errors.Is` cleanly: errors.Is(err, search.ErrMissingWorkspace)
// returns true for the canonical sentinel AND for the legacy alias
// (single pointer identity — the alias is the same variable).
var ErrMissingWorkspace = errors.New("search: workspace context required")

// ErrHybridRequiresSparse is returned when mode=hybrid is requested
// but the pipeline cannot produce a real dense+sparse retrieval
// (sparse channel missing from VectorConfig, OR the BM25 tokenizer
// returns nil for the query — e.g. all tokens <2 chars after
// punctuation stripping). Handler maps to HTTP 422.
//
// Commit 2 BACKFILL/CUTOVER (July 2026): promoted from
// mediasearch.ErrHybridRequiresSparse to the canonical search
// package. The legacy alias mediasearch.ErrHybridRequiresSparse
// is now a Go-level pointer-identical re-export of this sentinel.
var ErrHybridRequiresSparse = errors.New("search: hybrid mode requires a configured sparse vector channel and a BM25-tokenizable query")

// ErrNoBackendAvailable is returned when the BackendRegistry has
// zero eligible backends for the query (e.g. no backend advertises
// the requested media type capability). Handler maps to HTTP 503.
//
// Commit 2 BACKFILL/CUTOVER (July 2026): promoted from
// mediasearch.ErrNoBackendAvailable to the canonical search package.
var ErrNoBackendAvailable = errors.New("search: no backend available for the requested query")

// ErrAllBackendsFailed is returned when every eligible backend
// returned an error (the fan-out produced zero successful results).
// Handler maps to HTTP 502 (Bad Gateway — upstream backends are
// reachable but all failed).
//
// Commit 2 BACKFILL/CUTOVER (July 2026): promoted from
// mediasearch.ErrAllBackendsFailed to the canonical search package.

// NewBackendRegistry returns an empty, mutable registry.
func NewBackendRegistry() *BackendRegistry {
	return &BackendRegistry{entries: make(map[string]SearchBackend)}
}

// Register adds a backend under its Name(). Returns:
//   - ErrNilBackend        if b is the zero SearchBackend value, OR a
//     typed-nil pointer (Kind==Ptr && IsNil).
//   - ErrEmptyName         if b.Name() returns "" (checked pre-Lock).
//   - ErrFrozen            if the registry is already frozen.
//   - ErrAlreadyRegistered if a backend with the same Name exists.
func (r *BackendRegistry) Register(b SearchBackend) error {
	if b == nil {
		return ErrNilBackend
	}
	if rv := reflect.ValueOf(b); rv.Kind() == reflect.Ptr && rv.IsNil() {
		return ErrNilBackend
	}
	name := b.Name()
	if name == "" {
		return ErrEmptyName
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return ErrFrozen
	}
	if _, exists := r.entries[name]; exists {
		return fmt.Errorf("%w: %q", ErrAlreadyRegistered, name)
	}
	r.entries[name] = b
	return nil
}

// Freeze locks the registry. Idempotent: safe to call multiple times.
// After Freeze, Register returns ErrFrozen and lookups become
// effectively wait-free.
func (r *BackendRegistry) Freeze() {
	r.mu.Lock()
	r.frozen = true
	r.mu.Unlock()
}

// IsFrozen reports whether the registry has been frozen.
func (r *BackendRegistry) IsFrozen() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.frozen
}

// All returns every registered backend, sorted by Name() so callers
// can rely on a deterministic iteration order.
func (r *BackendRegistry) All() []SearchBackend {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]SearchBackend, 0, len(r.entries))
	for _, b := range r.entries {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Eligible returns the registered backends matching q.Sources
// AND q.MediaTypes. The two filters compose with AND semantics.
//
//   - Sources: if q.Sources is non-empty, the candidate set is
//     reduced to backends whose Name() appears in the canonicalised
//     source list (alias resolution via ResolveCanonicals).
//     Empty canonical set (all aliases unknown) → empty result.
//   - MediaTypes: applied after Sources. Backends whose
//     Capabilities intersect with the canonicalised media-type
//     filter win; backends with no intersection are dropped.
//
// Empty q.Sources AND empty q.MediaTypes → every backend is
// eligible (the legacy "all" behaviour is preserved).
// Sort order is Name() for determinism, same as All().
func (r *BackendRegistry) Eligible(q Query) []SearchBackend {
	all := r.All()

	// 1. Sources filter (fail-fast on unknown aliases).
	canonicalSources := ResolveCanonicals(q.Sources)
	if len(q.Sources) > 0 && len(canonicalSources) == 0 {
		// All sources supplied were unknown aliases. NO
		// silent fallback: return empty result so callers
		// learn the misuse instead of getting a deceptively
		// full response from every backend.
		return []SearchBackend{}
	}
	if len(canonicalSources) > 0 {
		allow := make(map[string]struct{}, len(canonicalSources))
		for _, s := range canonicalSources {
			allow[s] = struct{}{}
		}
		filtered := make([]SearchBackend, 0, len(all))
		for _, b := range all {
			if _, ok := allow[b.Name()]; ok {
				filtered = append(filtered, b)
			}
		}
		all = filtered
	}

	// 2. MediaTypes filter (legacy behaviour preserved).
	if len(q.MediaTypes) == 0 {
		return all
	}
	want := make(map[Capability]struct{}, len(q.MediaTypes))
	for _, m := range q.MediaTypes {
		if m == "" {
			continue
		}
		want[Capability(m)] = struct{}{}
	}
	if len(want) == 0 {
		return all
	}
	out := make([]SearchBackend, 0, len(all))
	for _, b := range all {
		for _, c := range b.Capabilities() {
			if _, ok := want[c]; ok {
				out = append(out, b)
				break
			}
		}
	}
	return out
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

// ── Three-territory surface (Fase 6 Spina Dorsale) ───────────────────────────

// SearchDocument is the canonical typed envelope that bridges the three
// search territories (SemanticEnrichment, IndexProjection, MediaSearch).
//
// Shape rationale (godlike/06 "one owner per fact" + the QDRANT-001
// locator-leak rule): the struct is the SSOT for the Qdrant IndexSchema
// payload — every field except QdrantPointID is mirrored 1:1 to a
// payload key by internal/infrastructure/qdrant/payload_mapper.go.
// No server-internal locator (LocalPath, DriveLink, DriveFileID,
// InternalRootURL, FileSystemPath, raw collection/vector names) is
// allowed in this struct; a locator leak here would flow through every
// downstream surface and break the QDRANT-004 acceptance criterion
// ("Nessun path locale o secret esposto").
//
// Producers (SemanticEnrichment territory) MUST populate all payload
// fields they expect to be filterable on; consumers (IndexProjection +
// MediaSearch) MUST treat missing fields as zero-value gracefully.
// SchemaVersion tracks forward-compatible evolution of the payload —
// readers reject unknown versions.
type SearchDocument struct {
	// SchemaVersion is the version of this document contract. Currently
	// always 1. Bumped if a structural field is added.
	SchemaVersion int

	// AssetID is the canonical asset identifier (UUID). Mirrors the
	// media_assets.id column.
	AssetID string

	// QdrantPointID is the per-asset Qdrant point identifier (set by
	// the IndexProjection territory after a successful Upsert). It is
	// the read-side correlation key for retrievals + cleanups; absent
	// from freshly-produced SearchDocuments (no Qdrant write yet).
	QdrantPointID string

	// Payload fields (every key mirrors the Qdrant IndexSchema's
	// payload) — see infra/qdrant/schema/schema.go for the canonical
	// names. All fields are json-stable strings / string-lists so the
	// payload_mapper conversion is lossless.
	Source         string   // "youtube" | "artlist" | "local" | ...
	Name           string   // human-readable asset name
	Category       string   // taxonomy category slug
	MediaType      string   // "video" | "image" | "audio"
	Style          string   // visual style (optional)
	Language       string   // BCP-47 (optional, drive by content)
	YouTubeVideoID string   // canonical YouTube video identifier (optional)
	YouTubeURL     string   // canonical YouTube web URL (optional)
	StartTime      string   // HH:MM:SS(.mmm) — for clip-style assets
	EndTime        string   // HH:MM:SS(.mmm) — for clip-style assets
	Tags           []string // free-form tags (lowercased, deduped)
	SearchText     string   // semantic-search text (title+summary+topics)
}

// AsPayloadMap flattens a SearchDocument into the canonical Qdrant
// payload map. Mirrors the field-to-key contract — same string for
// each name as in infra/qdrant/schema/schema.go (canonical truth) and
// infra/qdrant/payload_mapper.go (read-side). The SchemaVersion is
// NOT included (Qdrant does not version its payload; version is
// tracked separately in infra indexer logs).
//
// Use this from IndexProjection producers (artlist/semantic_enricher,
// images/metadata_service) so the write surface and read surface
// agree byte-for-byte. The MediaSearch side reads payload via the
// infra qdrant/payload_mapper.go's read path — never via this
// function (the direction is wrong for retrieval).
func (d SearchDocument) AsPayloadMap() map[string]any {
	out := map[string]any{
		"asset_id": d.AssetID,
	}
	if d.Source != "" {
		out["source"] = d.Source
	}
	if d.Name != "" {
		out["name"] = d.Name
	}
	if d.Category != "" {
		out["category"] = d.Category
	}
	if d.MediaType != "" {
		out["media_type"] = d.MediaType
	}
	if d.Style != "" {
		out["style"] = d.Style
	}
	if d.Language != "" {
		out["language"] = d.Language
	}
	if d.YouTubeVideoID != "" {
		out["youtube_video_id"] = d.YouTubeVideoID
	}
	if d.YouTubeURL != "" {
		out["youtube_url"] = d.YouTubeURL
	}
	if d.StartTime != "" {
		out["start_time"] = d.StartTime
	}
	if d.EndTime != "" {
		out["end_time"] = d.EndTime
	}
	if len(d.Tags) > 0 {
		out["tags"] = d.Tags
	}
	if d.SearchText != "" {
		out["search_text"] = d.SearchText
	}
	return out
}

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
// Method shape mirrors internal/infrastructure/qdrant.TextEmbedder
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
// (see internal/infrastructure/qdrant/client_search.go::SparseText
// + SparseVectorName pair semantics).
const (
	ChannelText       = "text"        // 768d multilingual-e5-base (semantic meaning)
	ChannelTranscript = "transcript"  // 768d multilingual-e5-base (Whisper transcript content)
	ChannelVisual     = "visual"      // 768d SigLIP-text encoder (forward-pointer; PR-CROSS-MODAL-TEXT-TO-VISUAL)
	ChannelAudio      = "audio"       // 512d CLAP-text encoder (forward-pointer; PR-CROSS-MODAL-TEXT-TO-VISUAL)
	ChannelSparse     = "bm25_text"   // sparse BM25; server-side inference; ERR_NOT_APPLICABLE on query-time
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

// Typed-error contract (godlike/07 typed-error). Registry impls MUST
// return these sentinels (errors.New(...)) or wrap them via fmt.Errorf("%w")
// so callers can errors.Is the specific failure mode.
var (
	// ErrChannelUnknown is returned when the channel name is not in
	// the canonical closed set. Surfaces a programming error at the
	// orchestrator rather than a misconfiguration at composition root.
	ErrChannelUnknown = errors.New("search: unknown embedding channel")

	// ErrChannelNotConfigured is returned when the channel is in the
	// canonical closed set but no adapter has been wired at the
	// composition root. Distinguishes "we don't yet support this
	// channel" from "the channel doesn't even exist".
	ErrChannelNotConfigured = errors.New("search: channel recognized but no adapter wired")

	// ErrChannelNotApplicable is returned when the channel accepts
	// file-path index-time inputs (visual/audio) but the caller is
	// invoking EmbedQuery (a text-input port). The visual/audio
	// channels are NOT query-time text-encodable in the canonical
	// surface until PR-CROSS-MODAL-TEXT-TO-VISUAL lands a SigLIP-text
	// / CLAP-text encoder. The sparse channel returns this sentinel
	// because Qdrant handles BM25 inference server-side; no Go-side
	// encoder is needed.
	ErrChannelNotApplicable = errors.New("search: channel does not support text-query encoding (use index-time file input instead)")
)

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
