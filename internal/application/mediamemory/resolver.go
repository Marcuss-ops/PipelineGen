// Package mediamemory — resolver.go is the canonical VisualResolver
// that composes the 9-level priority pipeline (architecture doc,
// section 10):
//
//  1. Phrase esatta approvata manualmente      (Level 0, hot path)
//  2. Frase normalizzata                       (Level 0, normalized retry)
//  3. Concetto semanticamente equivalente     (Level 1, Qdrant)
//  4. Entità                                   (Level 1, entity fan-out)
//  5. Azione visiva                            (Level 1, action hint)
//  6. Keyword                                  (Level 1, BM25 sparse)
//  7. Categoria                                (Level 1, topic)
//  8. Catalogo locale                          (Level 2, SQLite binding + media_assets)
//  9. Provider esterno                         (Level 3, SearchFanOut)
//
// godlike/06 SSOT: VisualResolver is the SINGLE owner of the
// resolution order. Every route that produces a SceneVisualPlan
// (whether API, batch, or admin) routes through this resolver
// (forward-pointer; sister surface to ClipResolver in the existing
// scripts/usecase package, which is the per-script generative
// counterpart).
//
// godlike/07 NO-FAKE-AVAILABILITY: every level surfaces a typed
// failure rather than silently fall-through. Levels 0/1 hit the
// cache via ConceptRepository; Level 2 falls through to
// CandidateRepository; Level 3 hits the external SearchFanOut
// (IF ResolvePolicy.AllowExternalSearch = true). Partial provider
// failures are tolerated by propagating Partial=true on the result
// (no fake availability).
package mediamemory

import "context"

// Resolver is the canonical port exposed to api/mediamemory.
// Concrete impl is VisualResolver (this file, below).
type Resolver interface {
	// Resolve produces a ResolveResult for the input request. The
	// resolver MUST honour ctx.Done() at every level iteration.
	Resolve(ctx context.Context, req ResolveRequest) (ResolveResult, error)
}

// ── Canonical implementation (skeleton) ───────────────────────────

// VisualResolver is the canonical implementation of Resolver. It
// composes the 9-level priority pipeline. Each level reads from
// the canonical ports declared in ports.go — there is no parallel
// "fast path" (godlike/06 SSOT).
type VisualResolver struct {
	concepts ConceptRepository
	bindings BindingRepository
	cache    QueryCacheRepository
	external SearchFanOut
	semantic SemanticLookup
	ranker   Ranker
	plan     AcquisitionPlanner
	log      Logger
	clock    Clock
	metrics  MetricsSink
}

// NewVisualResolver constructs the resolver with the canonical
// dependency set. Composition root wires concrete adapters.
func NewVisualResolver(
	concepts ConceptRepository,
	bindings BindingRepository,
	cache QueryCacheRepository,
	external SearchFanOut,
	semantic SemanticLookup,
	ranker Ranker,
	plan AcquisitionPlanner,
	log Logger,
	clock Clock,
	metrics MetricsSink,
) *VisualResolver {
	if log == nil {
		log = NoopLogger()
	}
	if clock == nil {
		clock = RealClock()
	}
	if metrics == nil {
		metrics = NoopMetrics()
	}
	return &VisualResolver{
		concepts: concepts,
		bindings: bindings,
		cache:    cache,
		external: external,
		semantic: semantic,
		ranker:   ranker,
		plan:     plan,
		log:      log,
		clock:    clock,
		metrics:  metrics,
	}
}

// Compile-time assertion: VisualResolver satisfies Resolver.
var _ Resolver = (*VisualResolver)(nil)

// Resolve is the canonical entrypoint. Phase 1.x wires Level 0
// (exact cache hit) + Level 8 (local catalog) + Level 9 (external
// fan-out); Phase 2 fills Levels 1–7 (semantic + parafrasi).
//
// godlike/06 SSOT: returns a ResolveResult envelope EVEN ON ERROR
// for the partial case (some scenes resolved, some failed). Pure
// failures (no scene could be planned) return (zero, err).
func (r *VisualResolver) Resolve(_ context.Context, _ ResolveRequest) (ResolveResult, error) {
	// Phase 1.x: skeleton. Implementations land in subsequent
	// phases per the architecture doc's "Ordine di implementazione":
	//   Phase 1.x — Exact match + local catalog + SearchFanOut
	//   Phase 2   — Semantic match + parafrasi + diversity
	//   Phase 3   — Discovery worker + linker worker
	//   Phase 4   — Visual channel + 3-layer plans
	//
	// Forbidden pattern (no fake availability): do not return an
	// empty ResolveResult without surfacing the typed error.
	return ResolveResult{}, errNotImplemented("mediamemory: VisualResolver.Resolve not yet implemented (Phase 1.x)")
}
