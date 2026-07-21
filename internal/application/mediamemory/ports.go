// Package mediamemory — ports.go is the slim port surface (godlike/06
// SSOT: every collaborator behind the capability flows through one
// of these interfaces; composition root wires concrete adapters).
//
// godlike/06 SSOT (composition pattern): the repositories defined here
// are the SOLE seams between the mediamemory capability and any
// durable store (SQLite for Phase 1.x, Qdrant for the semantic
// channel in Phase 2). A future agent that swaps in a different store
// implements THESE ports, NOT a parallel seam.
//
// godlike/06 SSOT (narrow port doctrine): every interface carries only
// the methods actually consumed by the mediamemory capability. No
// god-ports. The Logger / Clock / MetricsSink are tiny telemetry
// seams so unit tests can use noop implementations.
//
// godlike/07 NO-FAKE-AVAILABILITY (fail-closed returns): all
// repository ports MUST surface typed sentinels (declared in
// types.go) via errors.Is. No silent zero-value returns on a
// miss, no swallowed IO errors. The pair (errors.Is pattern +
// typed sentinels) is the lingua franca of the whole package.
package mediamemory

import (
	"context"
	"time"
)

// ── Logger port ────────────────────────────────────────────────────
//
// Mirror of search.Logger — slim logging surface. Production wiring
// usually routes through a zap adapter (see internal/app/*).
type Logger interface {
	Info(msg string, keysAndValues ...any)
	Warn(msg string, keysAndValues ...any)
	Debug(msg string, keysAndValues ...any)
	Error(msg string, keysAndValues ...any)
}

// noopLogger swallows every call. Used when callers pass nil and
// in tests where noise must be zero. Mirrors search.noopLogger.
type noopLogger struct{}

func (noopLogger) Info(string, ...any)  {}
func (noopLogger) Warn(string, ...any)  {}
func (noopLogger) Debug(string, ...any) {}
func (noopLogger) Error(string, ...any) {}

// NoopLogger returns a Logger that drops every message. Convenience
// for tests.
func NoopLogger() Logger { return noopLogger{} }

// Clock port — defined narrowly so tests can pin time deterministically.
type Clock interface {
	Now() time.Time
}

// realClock delegates to time.Now.
type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

// RealClock returns a Clock backed by time.Now. Composition-root uses
// this in production; tests inject a fake.
func RealClock() Clock { return realClock{} }

// MetricsSink is the narrow observability port. Implementations
// should match the existing assets/middleware.MetricsSink contract
// in shape; tests inject noop.
type MetricsSink interface {
	IncCounter(name string, labels ...string)
	ObserveHistogram(name string, value float64, labels ...string)
}

// noopMetrics drops every metric. Used in unit tests.
type noopMetrics struct{}

func (noopMetrics) IncCounter(string, ...string)                {}
func (noopMetrics) ObserveHistogram(string, float64, ...string) {}

// NoopMetrics returns a MetricsSink that drops every observation.
func NoopMetrics() MetricsSink { return noopMetrics{} }

// ── ConceptRepository ──────────────────────────────────────────────

// ConceptRepository owns the canonical read/write surface for
// media_concepts. UNIQUE(language, phrase_fingerprint) is enforced at
// the SQL layer (forward-pointer to Phase 1.2). Concrete impl in
// internal/infrastructure/database/sqlite/mediamemory/concepts_repository.go.
type ConceptRepository interface {
	// Upsert inserts or updates a concept keyed by
	// (language, phrase_fingerprint). On conflict the existing row is
	// updated and the same ID is returned.
	Upsert(ctx context.Context, c MediaConcept) (MediaConcept, error)

	// FindByID wraps ErrConceptNotFound when the row is missing.
	FindByID(ctx context.Context, id string) (MediaConcept, error)

	// FindByFingerprint is the Level 0 (exact-match) lookup used by
	// VisualResolver before any fan-out to external providers.
	FindByFingerprint(ctx context.Context, language, fingerprint string) (MediaConcept, error)

	// FindManyByFingerprints is the bulk variant for batch resolvers.
	FindManyByFingerprints(ctx context.Context, language string, fingerprints []string) ([]MediaConcept, error)
}

// ── BindingRepository ──────────────────────────────────────────────

// BindingRepository owns the canonical read/write surface for
// media_bindings. UNIQUE(concept_id, asset_id, slot_kind) is the
// SQL SSOT (forward-pointer to Phase 1.2); violations surface as
// wrapped ErrDuplicateBinding.
type BindingRepository interface {
	// Upsert inserts or updates a binding. SlotsKind MUST be in the
	// canonical closed set (callers MUST pre-validate via
	// IsKnownSlotKind); otherwise ErrInvalidSlotKind is returned.
	Upsert(ctx context.Context, b MediaBinding) (MediaBinding, error)

	FindByID(ctx context.Context, id string) (MediaBinding, error)

	// ListApprovedByConcept is the resolver's Level-0 hot path:
	// approved bindings only, ordered by SuccessScore desc.
	ListApprovedByConcept(ctx context.Context, conceptID string, slotKinds []SlotKind, limit int) ([]MediaBinding, error)

	// ListApprovedByConcepts is the batched variant used by
	// the Level 3-7 semantic adapter (qdrant_semantic.go) when
	// it joins N Qdrant concept hits to their canonical
	// bindings in ONE round-trip. The result map is keyed by
	// concept_id; missing entries mean "no approved bindings
	// for this concept + slot kind". The same ordering,
	// slot-filter, and limit-per-concept semantics as
	// ListApprovedByConcept apply.
	ListApprovedByConcepts(ctx context.Context, conceptIDs []string, slotKinds []SlotKind, limit int) (map[string][]MediaBinding, error)

	// ListByConcept returns every binding (any status) for the
	// concept, ordered by updated_at desc. Used by the dashboard
	// "Visual Memory" page / admin diff / audit trail. NOT used
	// by the resolver hot path (use ListApprovedByConcept there).
	ListByConcept(ctx context.Context, conceptID string) ([]MediaBinding, error)

	// ListByAsset returns every binding that references an asset_id
	// (used by anti-repetition on the same-source-clip check).
	ListByAsset(ctx context.Context, assetID string) ([]MediaBinding, error)

	// Delete is provided for admin reindex flows; it is not used by
	// the resolver hot path.
	Delete(ctx context.Context, id string) error
}

// ── QueryCacheRepository ───────────────────────────────────────────

// QueryCacheRepository owns media_query_cache. The cache key is
// SHA256(language + normalized_phrase + visual_intent_version). It is
// distinct from the script-generation cache (different SSOT per
// godlike/06).
type QueryCacheRepository interface {
	Get(ctx context.Context, fingerprint string) (QueryCacheEntry, bool, error)
	Put(ctx context.Context, entry QueryCacheEntry) error
	Invalidate(ctx context.Context, fingerprint string) error
}

// QueryCacheEntry is the persisted shape of one cache hit. Kept here
// (not in types.go) because it is a port-level envelope, not a
// canonical business entity.
type QueryCacheEntry struct {
	ID                string
	PhraseFingerprint string
	Language          string
	RequestJSON       string
	ResultJSON        string
	ProviderStateJSON string
	HitCount          int
	ExpiresAt         *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// ── CandidateRepository ────────────────────────────────────────────

// CandidateRepository owns media_candidates. Used by the discovery
// and linker workers. Reads from here surface Cold/Warm/Hot tiers.
type CandidateRepository interface {
	// UpsertInsert inserts a NEW candidate row keyed by
	// (provider, provider_asset_id). Conflicts on the unique key
	// MUST surface ErrDuplicateBinding (re-using the typed envelope
	// because the semantic intent — duplicate of media_candidates —
	// is fail-closed).
	UpsertInsert(ctx context.Context, c MediaCandidate) (MediaCandidate, error)

	FindByID(ctx context.Context, id string) (MediaCandidate, error)

	// ListByProvider returns cold candidates for a provider. Workers
	// iterate to pick top-K for materialization.
	ListByProvider(ctx context.Context, provider string, limit int) ([]MediaCandidate, error)

	// ListPendingMaterialization is the Warm→Hot promotion read
	// (rights-verified only).
	ListPendingMaterialization(ctx context.Context, limit int) ([]MediaCandidate, error)

	UpdateStatus(ctx context.Context, id string, discovery DiscoveryStatus, mat MaterializationStatus) error
}

// ── UsageRepository ────────────────────────────────────────────────

// UsageRepository owns media_usage_events. Used by FeedbackService
// and the ranker success-score promotion loop.
//
// godlike/06 SSOT (Fase 2.3 anti-repetition contract): the
// repository exposes ListProjectUsages so the resolver can read
// the canonical same-project same-asset / channel-saturation /
// consecutive-source identity for every event without a runtime
// join against media_assets. ChannelID and VideoID are denormalized
// into the row by Fase 2.3 wiring (forward-pointer to
// migrations/sqlite/169_mediamemory_anti_repetition_columns.sql).
type UsageRepository interface {
	Append(ctx context.Context, ev UsageEvent) error
	ListByConcept(ctx context.Context, conceptID string, limit int) ([]UsageEvent, error)
	ListByAsset(ctx context.Context, assetID string, limit int) ([]UsageEvent, error)
	// ListProjectUsages returns every event for a project (newest
	// first) so the resolver can apply the Fase 2.3
	// repetition_penalty formula (same-asset penalty +
	// channel-saturation penalty + consecutive-video penalty)
	// using the canonical append-only audit trail. ChannelID and
	// VideoID flow through verbatim from the columns added in
	// migration 169.
	ListProjectUsages(ctx context.Context, projectID string, limit int) ([]UsageEvent, error)
}

// ── External ports (consumed by composition-root adapters) ────────

// SearchFanOut is the canonical port through which MediaMemory
// delegates Level 3 (internet/provider) search. The production
// adapter is search.Aggregator (composition root narrows it).
//
// godlike/06 SSOT: this port IS NOT a parallel search engine — it is
// a thin facade over the existing Canonical SearchFanOut already
// present in search/. Level-3 queries route THROUGH this port and
// back into the chosen provider fan-out.
//
// godlike/07 NO-FAKE-AVAILABILITY: SearchFanOut MUST surface typed
// sentinels from the search package (errors.Is chain) so the
// resolver can branch on partial / no-backend / all-failed.
type SearchFanOut interface {
	Search(ctx context.Context, q SearchFanOutQuery) (SearchFanOutResult, error)
}

// SearchFanOutQuery is the narrow input shape consumed by MediaMemory.
// The production adapter translates it into search.Query.
type SearchFanOutQuery struct {
	Text       string
	Language   string
	MediaTypes []string
	Sources    []string
	Limit      int
}

// SearchFanOutResult is the narrow output shape consumed by
// MediaMemory. The production adapter translates search.Result into
// this shape.
type SearchFanOutResult struct {
	Candidates    []MediaCandidate
	Partial       bool
	BackendNames  []string
	BackendErrors map[string]string
}

// SemanticLookup is the Level 1 (semantic) port. The production
// adapter proxies to the existing search.Aggregator in semantic
// mode and projects to MediaCandidate.
//
// Phase 2 forwards: SearchFanOut/SemanticLookup may collapse once
// the semantic backend is fully wired.
type SemanticLookup interface {
	LookupByConcept(ctx context.Context, conceptType ConceptType, text string, language string, limit int) ([]MediaCandidate, error)
}

// EmbeddingIndexer is the Level 1 ingest port. The production adapter
// proxies to the canonical EmbeddingChannelRegistry
// (internal/application/search).
//
// godlike/06 (narrow port doctrine): the port owns single-
// concept mutation ONLY. Batch reindex is an admin/orchestrator
// concern, not an indexer concern — composition root callers
// iterate the canonical ConceptRepository for a language and
// invoke IndexConcept per row. Keeping the port single-entity
// avoids leaking the ordering / batching / ownership of
// per-language iteration into the indexer seam.
type EmbeddingIndexer interface {
	IndexConcept(ctx context.Context, c MediaConcept) error
	DeindexConcept(ctx context.Context, conceptID string) error
	// ReindexConcept bumps the concept's embedding_version to
	// targetVersion (or — when targetVersion is empty — to the
	// canonical next version computed via
	// qdrantschema.BumpEmbeddingVersion) and rewrites the
	// canonical Qdrant point at the SAME point ID. The Qdrant
	// point's payload field `embedding_version` updates to the
	// new value; vectors are re-computed using the same
	// EmbeddingChannelRegistry.
	//
	// godlike/06 SSOT (Level 0 cache independence under
	// versioning): rebumping the version does NOT mutate
	// PhraseFingerprint. The Normalizer hashes (language +
	// normalized_phrase + visual_intent_version) deterministically
	// from the original canonical text so the (language,
	// fingerprint) tuple is invariant under reindex. Callers
	// relying only on ConceptRepository.FindByFingerprint see the
	// same concept_id resolve to the same canonical row before
	// and after the bump — that's the contract this port method
	// preserves.
	ReindexConcept(ctx context.Context, c MediaConcept, targetVersion string) error
}

// StockPipelineAcquirer is the materialization port. The production
// adapter wraps the canonical stockpipeline surface (download +
// segment + validate + index). godlike/06 SSOT: mediamemory does NOT
// re-implement the pipeline — it ONLY invokes this port.
type StockPipelineAcquirer interface {
	// Materialize downloads/segments the candidate and produces a
	// canonical media_assets row. AssetID on the returned candidate
	// is set on success. Errors wrap ErrCandidateMaterializationFailed.
	Materialize(ctx context.Context, candidate MediaCandidate, opts MaterializeOptions) (MediaCandidate, error)
}

// MaterializeOptions configures the acquire call.
type MaterializeOptions struct {
	// TargetSlot hints the stockpipeline which segment quality to
	// prefer ("primary_video" → higher bitrate, "secondary_image"
	// → thumbnail-grade, ...).
	TargetSlot SlotKind
	// HotCache controls whether the bytes are staged locally
	// (Hot) or only stored on Drive (Warm). Cold is the default.
	HotCache bool
	// MaxDurationMs caps the segment download window.
	MaxDurationMs int64
	// ProjectID scopes the materialization for rights enforcement.
	ProjectID string
} // RightsValidator is the rights brand-check port. The production
// adapter reads the metadata registry for license_basis /
// allowed_channels / allowed_regions / expiration.
type RightsValidator interface {
	Validate(ctx context.Context, c MediaCandidate, projectID string) (RightsDecision, error)
}

// AcquisitionPlanner is the canonical port defined in
// acquisition_planner.go (godlike/06 SSOT — single canonical home).
// It owns the Cold→Warm→Hot tiering decision; concrete impl is
// defaultAcquisitionPlanner in that file.

// RightsDecision is the verdict produced by the rights port.
// godlike/07 NO-FAKE-AVAILABILITY: Verdict == AllowConditional
// requires non-empty Conditions, otherwise the ranker MUST apply
// full rights_penalty.
type RightsDecision struct {
	Verdict    RightsVerdict
	Reason     string
	Conditions []string
}

// RightsVerdict enum (godlike/06 closed set).
type RightsVerdict string

const (
	RightsVerdictAllow            RightsVerdict = "allow"
	RightsVerdictAllowConditional RightsVerdict = "allow_conditional"
	RightsVerdictDeny             RightsVerdict = "deny"
)

// IsKnownRightsVerdict reports whether v is in the canonical closed set.
// godlike/06 SSOT: predicate lives NEXT TO its enum (this file), keeping
// every RightsVerdict surface (constant + predicate + future typed-
// sentinel) co-located for grep + drift-pinning.
func IsKnownRightsVerdict(v RightsVerdict) bool {
	switch v {
	case RightsVerdictAllow, RightsVerdictAllowConditional, RightsVerdictDeny:
		return true
	default:
		return false
	}
}
