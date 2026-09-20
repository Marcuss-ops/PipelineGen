// Package mediamemory — ports.go is the canonical port surface for the
// mediamemory capability (godlike/06 SSOT: one canonical owner per fact).
//
// Every interface declared here is the SOLE seam between the mediamemory
// capability and any durable store (SQLite for Phase 1.x, Qdrant for the
// semantic channel in Phase 2). A future agent that swaps in a different
// store implements THESE ports, NOT a parallel seam.
//
// godlike/06 SSOT (narrow port doctrine): every interface carries only
// the methods actually consumed by the mediamemory capability. No
// god-ports. The Logger / Clock / MetricsSink are tiny telemetry
// seams so unit tests can use noop implementations.
//
// godlike/07 NO-FAKE-AVAILABILITY (fail-closed returns): all
// repository ports MUST surface typed sentinels (declared in
// types_sentinels.go) via errors.Is.
//
// File layout (capability-level SSOT):
//
//	ports.go             — interfaces only (the port surface)
//	ports_telemetry.go   — telemetry implementations (noopLogger, realClock, noopMetrics)
//	ports_types.go       — port-level data shapes (envelopes, options, verdicts)
//	types.go             — package doc + SlotKind alias
//	types_enums.go       — closed-set enums (ConceptType, ApprovalStatus, …) + IsKnown predicates + Provider tags
//	types_entities.go    — MediaConcept, MediaBinding, MediaCandidate, BatchSpec, Batch, BatchChild, UsageEvent
//	types_resolver.go    — VisualIntent, SceneSpec, Layer, CandidateOption, …
//	types_linker.go      — LinkerRequest, LinkerResult, EncodingChannels, MediaEmbedding, …
//	types_sentinels.go   — typed fail-closed sentinel errors
package mediamemory

import (
	"context"
	"time"
)

// ── Logger port ────────────────────────────────────────────────────

type Logger interface {
	Info(msg string, keysAndValues ...any)
	Warn(msg string, keysAndValues ...any)
	Debug(msg string, keysAndValues ...any)
	Error(msg string, keysAndValues ...any)
}

// ── Clock port ─────────────────────────────────────────────────────

type Clock interface {
	Now() time.Time
}

// ── MetricsSink ────────────────────────────────────────────────────

type MetricsSink interface {
	IncCounter(name string, labels ...string)
	ObserveHistogram(name string, value float64, labels ...string)
}

// ── ConceptRepository ──────────────────────────────────────────────

type ConceptRepository interface {
	Upsert(ctx context.Context, c MediaConcept) (MediaConcept, error)
	FindByID(ctx context.Context, id string) (MediaConcept, error)
	FindByFingerprint(ctx context.Context, language, fingerprint string) (MediaConcept, error)
	FindManyByFingerprints(ctx context.Context, language string, fingerprints []string) ([]MediaConcept, error)
}

// ── BindingRepository ──────────────────────────────────────────────

type BindingRepository interface {
	Upsert(ctx context.Context, b MediaBinding) (MediaBinding, error)
	FindByID(ctx context.Context, id string) (MediaBinding, error)
	ListApprovedByConcept(ctx context.Context, conceptID string, slotKinds []SlotKind, limit int) ([]MediaBinding, error)
	ListApprovedByConcepts(ctx context.Context, conceptIDs []string, slotKinds []SlotKind, limit int) (map[string][]MediaBinding, error)
	ListByConcept(ctx context.Context, conceptID string) ([]MediaBinding, error)
	ListByAsset(ctx context.Context, assetID string) ([]MediaBinding, error)
	Delete(ctx context.Context, id string) error
}

// ── BindingMutationDispatcher ──────────────────────────────────────

type BindingMutationDispatcher interface {
	UpsertBinding(ctx context.Context, b MediaBinding) (MediaBinding, error)
	DeleteBinding(ctx context.Context, id, conceptID string) error
}

// ── QueryCacheRepository ───────────────────────────────────────────

type QueryCacheRepository interface {
	Get(ctx context.Context, fingerprint string) (QueryCacheEntry, bool, error)
	Put(ctx context.Context, entry QueryCacheEntry) error
	Invalidate(ctx context.Context, fingerprint string) error
}

// ── CandidateRepository ────────────────────────────────────────────

type CandidateRepository interface {
	UpsertInsert(ctx context.Context, c MediaCandidate) (MediaCandidate, error)
	FindByID(ctx context.Context, id string) (MediaCandidate, error)
	ListByProvider(ctx context.Context, provider string, limit int) ([]MediaCandidate, error)
	ListPendingMaterialization(ctx context.Context, limit int) ([]MediaCandidate, error)
	UpdateStatus(ctx context.Context, id string, discovery DiscoveryStatus, mat MaterializationStatus) error
}

// ── UsageRepository ────────────────────────────────────────────────

// UsageRepository was DELETED here on 2026-09-20. It was the read/write port
// for the append-only UsageEvent audit log; its single implementation
// (sqlite/mediamemory/usage_repository.go) had ZERO callers repo-wide — not
// even its own tests — and its single consumer (FeedbackService) was never
// constructed by composition. A port with no implementation and no caller is
// the structural-deadcode case, so both halves were removed rather than left
// as a seam nobody can wire.

// ── External ports ─────────────────────────────────────────────────

type SearchFanOut interface {
	Search(ctx context.Context, q SearchFanOutQuery) (SearchFanOutResult, error)
}

type SemanticLookup interface {
	LookupByConcept(ctx context.Context, conceptType ConceptType, text string, language string, limit int) ([]MediaCandidate, error)
}

type KeyframeVisualIndexer interface {
	IndexKeyframe(ctx context.Context, videoID string, tsMs int64, assetID string, language string, vec []float32, model string) error
}

type EmbeddingIndexer interface {
	IndexConcept(ctx context.Context, c MediaConcept) error
	DeindexConcept(ctx context.Context, conceptID string) error
	ReindexConcept(ctx context.Context, c MediaConcept, targetVersion string) error
}

type StockPipelineAcquirer interface {
	Materialize(ctx context.Context, candidate MediaCandidate, opts MaterializeOptions) (MediaCandidate, error)
}

type RightsValidator interface {
	Validate(ctx context.Context, c MediaCandidate, projectID string) (RightsDecision, error)
}

// ── Fase 3.2 linker ports ─────────────────────────────────────────

// TranscriptExtractor, KeyframeExtractor, VisualDescriptionGenerator,
// EntityDetector, EmbeddingEncoder and LinkerWorker were REMOVED here on
// 2026-09-20, together with types_linker.go and the Fase 3.1/3.2/3.3 batch
// surface (batch_service*, discovery_worker, acquisition_planner).
//
// WHY DELETION RATHER THAN A DEBT CARD. The whole batch + linker pipeline was
// reachable ONLY from its own tests: no production composition site ever
// constructed a BatchService, a DiscoveryWorker or a LinkerWorker
// (NewDefaultBatchServiceWithWorker, NewDefaultDiscoveryWorker and
// NewDefaultAcquisitionPlanner had zero callers outside _test.go),
// mediamemory_wiring.go wires only the Resolver and the BindingService, and
// nothing consumed the BatchService or LinkerWorker interfaces. A port whose
// only implementor is a test stub, next to a service the binary cannot
// construct, is the structural reading of "godlike/07 no-fake-availability":
// test-only reachability is debt, so the surface goes rather than the tests.
//
// The surviving production seam is MaterializeWorker
// (media_materialize_worker.go): the stockpipeline asset materializer
// constructs it at runtime and calls PromoteOnDemand, so it stays — and so
// does AcquisitionPromote, the promote envelope its request carries.
