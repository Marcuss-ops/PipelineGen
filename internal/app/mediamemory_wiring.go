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
	"fmt"

	"go.uber.org/zap"

	braincore "github.com/Marcuss-ops/PipelineGen/internal/application/brain/core"
	brainintent "github.com/Marcuss-ops/PipelineGen/internal/application/brain/intent"
	brainnormalizer "github.com/Marcuss-ops/PipelineGen/internal/application/brain/normalizer"
	brainplanner "github.com/Marcuss-ops/PipelineGen/internal/application/brain/planner"
	brainranker "github.com/Marcuss-ops/PipelineGen/internal/application/brain/ranker"
	brainsearch "github.com/Marcuss-ops/PipelineGen/internal/application/brain/search"
	"github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/application/search"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/collections"
	qdrantschema "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
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
	mgr := collections.NewCollectionManager(transportClient, qdrantschema.ConceptIndexSchema(), log)
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
		Indexer: adapters.NewQdrantIndexer(deps.Transport, deps.Embedder, log),
		Lookup:  adapters.NewQdrantSemanticLookup(deps.Transport, deps.Embedder, deps.ConceptsRepo, deps.BindingsRepo, log),
	}, nil
}

// WireMediaMemoryResolver builds the canonical mediamemory.Resolver
// backed by the Brain. All search (exact memory, local catalog,
// semantic, external providers) is delegated to the Brain, which in
// turn uses the canonical search.SearchFanOut through the
// CandidateSearcher port.
//
// godlike/06 SSOT: this is the single composition site for the
// MediaMemoryResolver. Callers that need a Resolver must use this
// function and must not construct VisualResolver or
// MediaMemoryResolver manually.
func WireMediaMemoryResolver(searchFanOut search.SearchFanOut, log *zap.Logger) mediamemory.Resolver {
	_ = log
	candidateSearcher := brainsearch.NewCandidateSearcherAdapter(searchFanOut)
	n := brainnormalizer.NewDefaultNormalizer()
	ir := brainintent.NewDefaultResolver()
	r := brainranker.NewDefaultRanker()
	p := brainplanner.NewDefaultPlanner()
	b := braincore.NewCanonicalBrain(n, ir, candidateSearcher, r, p)
	return mediamemory.NewMediaMemoryResolver(b)
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
