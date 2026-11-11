// Package wiring owns production semantic-search readiness composition.
package wiring

import (
	"context"
	"strings"

	mediasearchapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediasearch"
)
import (
	"database/sql"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	brainCore "github.com/Marcuss-ops/PipelineGen/internal/capabilities/brain/core"
	brainIntent "github.com/Marcuss-ops/PipelineGen/internal/capabilities/brain/intent"
	brainNormalizer "github.com/Marcuss-ops/PipelineGen/internal/capabilities/brain/normalizer"
	brainPlanner "github.com/Marcuss-ops/PipelineGen/internal/capabilities/brain/planner"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
	sqliteMediaMemory "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/mediamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	"go.uber.org/zap"
)

type semanticBackendInspector interface {
	HasBackend(name string) bool
}

// semanticReadinessChecker probes only canonical media-search dependencies.
// POSTGRES-MEDIA-CUTOVER removed Qdrant reachability, legacy media
// hydration and Ollama presence from this contract. Embedder/backend
// readiness is derived
// from the semantic backend that actually passed the composition gate; that
// gate requires the E5 embedding registry plus the canonical PostgreSQL
// retrieval+hydration store and delivery dependency.
type semanticReadinessChecker struct {
	embedderWired        bool
	semanticBackendWired bool
	aggregator           mediasearchapi.AggregatorSearcher
	mediaPostgres        interface{ PingContext(context.Context) error }
}

var _ mediasearchapi.SemanticReadyChecker = (*semanticReadinessChecker)(nil)

// newSemanticReadinessChecker adapts composition-root singletons without
// importing concrete database or embedding transports into the handler.
func newSemanticReadinessChecker(root *ComposeRoot, aggregator mediasearchapi.AggregatorSearcher) *semanticReadinessChecker {
	c := &semanticReadinessChecker{aggregator: aggregator}
	if inspector, ok := aggregator.(semanticBackendInspector); ok {
		c.semanticBackendWired = inspector.HasBackend("semantic")
		// The semantic backend is registered only after Embeddings is non-nil,
		// so this is the real E5 composition signal. Ollama is unrelated.
		c.embedderWired = c.semanticBackendWired
	}
	if root != nil && root.MediaPostgres != nil {
		c.mediaPostgres = root.MediaPostgres
	}
	return c
}

// Ready returns nil only when every canonical semantic-search dependency is
// healthy. Failures are returned together through the typed Subsystems map.
func (c *semanticReadinessChecker) Ready(ctx context.Context) error {
	subs := make(map[string]string, 3)

	if !c.embedderWired {
		subs["embedder"] = "E5 semantic embedding dependency not wired"
	}
	if c.aggregator == nil || !c.semanticBackendWired {
		subs["semantic_backend"] = "semantic search backend not registered"
	}
	if c.mediaPostgres == nil {
		subs["media_postgres"] = "media PostgreSQL not wired"
	} else if err := c.mediaPostgres.PingContext(ctx); err != nil {
		subs["media_postgres"] = sanitizeReadinessMessage(err.Error())
	}

	if len(subs) == 0 {
		return nil
	}
	return readinessSubsystemsError{subs: subs}
}

type readinessSubsystemsError struct {
	subs map[string]string
}

func (e readinessSubsystemsError) Error() string {
	parts := make([]string, 0, len(e.subs))
	for k, v := range e.subs {
		if v != "" {
			parts = append(parts, k+"="+v)
		}
	}
	if len(parts) == 0 {
		return "semantic search readiness: ok"
	}
	return "semantic search not ready: " + strings.Join(parts, "; ")
}

func (e readinessSubsystemsError) Subsystems() map[string]string { return e.subs }

func sanitizeReadinessMessage(msg string) string {
	msg = strings.Join(strings.Fields(msg), " ")
	if len([]rune(msg)) > 200 {
		msg = string([]rune(msg)[:200])
	}
	return msg
}

// WireMediasearchReadiness assembles the canonical readiness checker. Index
// version remains unknown unless a live manifest adapter is supplied elsewhere;
// no fake version is invented here.
func WireMediasearchReadiness(root *ComposeRoot, aggregator mediasearchapi.AggregatorSearcher) mediasearchapi.SemanticReadyChecker {
	return newSemanticReadinessChecker(root, aggregator)
}

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

// NewMediaMemoryQdrantStack wires the Phase 2.1 adapters in
// one place so boot-time and test wiring share the exact
// dependency graph.

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
	adapter, err := mediamemory.NewSearchFanOutAdapter(searchFanOut)
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
		NewMediaMemoryRankerAdapter(mmRanker),
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
