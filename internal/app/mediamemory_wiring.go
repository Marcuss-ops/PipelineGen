// Package app — mediamemory_wiring.go is the composition-root
// wiring for the mediamemory capability's Phase 2.1
// semantic-search adapters (QdrantIndexer +
// QdrantSemanticLookup).
//
// godlike/06 SSOT (composition pattern): this file is the
// ONLY bridge between the mediamemory capability and the
// Qdrant + search infra for semantic indexing / lookup.
//
// godlike/06 SSOT (narrow port doctrine): EnsureConceptCollection
// is the canonical idempotent collection-creation step;
// NewMediaMemoryQdrantStack is the canonical adapter builder;
// NoopSemanticLookup is the canonical fallback.
//
// godlike/07 NO-FAKE-AVAILABILITY: when composition boot cannot
// reach Qdrant, the noop semantic lookup is the canonical
// fallback. The noop returns (nil, nil) — a graceful Level-3-7
// miss — NOT a typed sentinel error. Returning
// ErrSemanticBackendFailed here would trip the resolver into
// thinking the BACKEND is broken when actually it's
// intentionally absent.
package app

import (
	"context"
	"database/sql"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	brainCore "github.com/Marcuss-ops/PipelineGen/internal/application/brain/core"
	brainIntent "github.com/Marcuss-ops/PipelineGen/internal/application/brain/intent"
	brainNormalizer "github.com/Marcuss-ops/PipelineGen/internal/application/brain/normalizer"
	brainPlanner "github.com/Marcuss-ops/PipelineGen/internal/application/brain/planner"
	"github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory"
	mmadapters "github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/application/search"
	sqliteMediaMemory "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/mediamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/qdrantmm"
	qdrantschema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/collections"
	platformschema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// MediaMemoryQdrantWiring is the bag of dependencies.
type MediaMemoryQdrantWiring struct {
	Transport    *transport.Client
	Embedder     search.EmbeddingChannelRegistry
	ConceptsRepo mediamemory.ConceptRepository
	BindingsRepo mediamemory.BindingRepository
	Log          *zap.Logger
}

// MediaMemoryQdrantStack is the wired surface passed into
// mediamemory.NewVisualResolver.
type MediaMemoryQdrantStack struct {
	Indexer mediamemory.EmbeddingIndexer
	Lookup  mediamemory.SemanticLookup
}

// EnsureConceptCollection creates the pipelinegen_media_concepts
// collection if absent. godlike/07 NO-FAKE-AVAILABILITY: a nil
// transport returns wrapped ErrSemanticNotConfigured so a
// downstream resolver can branch via errors.Is.
func EnsureConceptCollection(ctx context.Context, transportClient *transport.Client, log *zap.Logger) error {
	if transportClient == nil {
		return fmt.Errorf("mediamemory: EnsureConceptCollection transport is nil: %w",
			mediamemory.ErrSemanticNotConfigured)
	}
	mgr, err := collections.NewProjectionManagerFor(platformschema.MediaConceptsProjection(), transportClient, log)
	if err != nil {
		return fmt.Errorf("mediamemory: EnsureConceptCollection: %w", err)
	}
	if err := mgr.CreateCollection(ctx, qdrantschema.ConceptCollectionName); err != nil {
		return fmt.Errorf("mediamemory: EnsureConceptCollection: %w", err)
	}
	return nil
}

// NewMediaMemoryQdrantStack wires the Phase 2.1 adapters in
// one place so boot-time and test wiring share the exact
// dependency graph.
func NewMediaMemoryQdrantStack(deps MediaMemoryQdrantWiring) (*MediaMemoryQdrantStack, error) {
	if deps.Transport == nil ||
		deps.Embedder == nil ||
		deps.ConceptsRepo == nil ||
		deps.BindingsRepo == nil {
		return nil, fmt.Errorf(
			"mediamemory: composition root missing dependencies (transport + registry + concept repo + binding repo are required): %w",
			mediamemory.ErrSemanticNotConfigured,
		)
	}
	log := deps.Log
	if log == nil {
		log = zap.NewNop()
	}
	return &MediaMemoryQdrantStack{
		Indexer: qdrantmm.NewQdrantIndexer(deps.Transport, deps.Embedder, log),
		Lookup:  qdrantmm.NewQdrantSemanticLookup(deps.Transport, deps.Embedder, deps.ConceptsRepo, deps.BindingsRepo, log),
	}, nil
}

// WireMediaMemoryResolver builds the canonical mediamemory.Resolver.
// It wires the MediaMemory cascade as a brain.MediaMemoryResolutionPort,
// builds the canonical Brain orchestrator over that port, and returns
// a MediaMemoryResolver that delegates every resolve call to the Brain.
//
// This makes the Brain the single orchestrator: the cascade only
// supplies candidates, the ranker scores them, and the planner
// assigns slots. The old VisualResolver.Resolve path is no longer
// used for production requests.
//
// godlike/06 SSOT: this is the single composition site for the
// production MediaMemory resolver. Callers that need a Resolver must
// use this function and must not construct VisualResolver or
// MediaMemoryResolver manually.
func WireMediaMemoryResolver(searchFanOut search.SearchFanOut, db *sql.DB, log *zap.Logger) (mediamemory.Resolver, error) {
	if db == nil {
		return nil, fmt.Errorf("mediamemory: db is required to wire the cascade resolver")
	}
	logAdapter := mediamemoryZapLogger{log}
	concepts := sqliteMediaMemory.NewConceptsRepository(db)
	bindings := sqliteMediaMemory.NewBindingsRepository(db)
	mmRanker := mediamemory.NewDefaultRanker(nil, logAdapter)
	adapter, err := mmadapters.NewSearchFanOutAdapter(searchFanOut)
	if err != nil {
		return nil, fmt.Errorf("mediamemory: search fanout adapter: %w", err)
	}

	// The cascade itself is the sole MediaMemoryResolutionPort
	// implementation. It feeds raw candidates to the Brain.
	cascade := mediamemory.NewVisualResolver(
		mediamemory.ResolverDeps{
			Concepts: concepts, Bindings: bindings, External: adapter,
			Semantic: NoopSemanticLookup{}, Ranker: mmRanker,
			Log: logAdapter, Clock: mediamemory.RealClock(), Metrics: mediamemory.NoopMetrics(),
		},
	)

	// Brain is the single orchestrator; the same MediaMemory ranker
	// instance is reused so scoring policy is not duplicated.
	b := brainCore.NewCanonicalBrain(
		brainNormalizer.NewDefaultNormalizer(),
		brainIntent.NewDefaultResolver(),
		cascade,
		wiring.NewMediaMemoryRankerAdapter(mmRanker),
		brainPlanner.NewDefaultPlanner(),
	)

	return mediamemory.NewMediaMemoryResolver(b), nil
}

// WireBindingService builds the canonical mediamemory.BindingService
// backed by the SQLite BindingMutationDispatcher.
//
// godlike/06 SSOT: this is the single composition site for the
// binding write surface. Callers that need a BindingService must
// use this function and must not construct defaultBindingService
// manually.
func WireBindingService(
	db *sql.DB,
	txmgr outbox.TxManager,
	outboxRepo *outboxevents.Repository,
	log *zap.Logger,
) mediamemory.BindingService {
	concepts := sqliteMediaMemory.NewConceptsRepository(db)
	bindings := sqliteMediaMemory.NewBindingsRepository(db)
	primitives, ok := bindings.(sqliteMediaMemory.BindingMutationPrimitives)
	if !ok {
		panic("mediamemory: bindings repository must implement BindingMutationPrimitives")
	}
	dispatcher := sqliteMediaMemory.NewBindingDispatcher(primitives, outboxRepo, txmgr)
	return mediamemory.NewDefaultBindingService(concepts, bindings, dispatcher, mediamemoryZapLogger{log}, mediamemory.RealClock())
}

// mediamemoryZapLogger bridges *zap.Logger to mediamemory.Logger.
type mediamemoryZapLogger struct {
	log *zap.Logger
}

func (l mediamemoryZapLogger) Info(msg string, kv ...any)  { l.log.Info(msg, anyToZap(kv)...) }
func (l mediamemoryZapLogger) Warn(msg string, kv ...any)  { l.log.Warn(msg, anyToZap(kv)...) }
func (l mediamemoryZapLogger) Debug(msg string, kv ...any) { l.log.Debug(msg, anyToZap(kv)...) }
func (l mediamemoryZapLogger) Error(msg string, kv ...any) { l.log.Error(msg, anyToZap(kv)...) }

func anyToZap(kv []any) []zap.Field {
	if len(kv) == 0 {
		return nil
	}
	fields := make([]zap.Field, 0, (len(kv)+1)/2)
	for i := 0; i < len(kv); i += 2 {
		key := fmt.Sprint(kv[i])
		if i+1 < len(kv) {
			fields = append(fields, zap.Any(key, kv[i+1]))
		} else {
			fields = append(fields, zap.String(key, "<missing value>"))
		}
	}
	return fields
}

// NoopSemanticLookup is the canonical noop SemanticLookup for
// dev mode / /admin/visual-memory running in isolation.
type NoopSemanticLookup struct{}

// NewNoopSemanticLookup returns a noop SemanticLookup.
func NewNoopSemanticLookup() mediamemory.SemanticLookup {
	return NoopSemanticLookup{}
}

// LookupByConcept returns (nil, nil) — graceful Level 3-7 miss.
func (NoopSemanticLookup) LookupByConcept(
	_ context.Context,
	_ mediamemory.ConceptType,
	_ string,
	_ string,
	_ int,
) ([]mediamemory.MediaCandidate, error) {
	return nil, nil
}
