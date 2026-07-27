// Package mediamemory — resolver.go is the canonical SSOT for the
// VisualResolver that composes the 9-level priority pipeline
// (architecture doc, section 10):
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
// godlike/06 SSOT (strict priority cascade): levels 1..9 are
// inspected IN ORDER; the FIRST level returning a non-empty
// candidate set wins, and the remaining levels are NOT consulted
// for that slot. Only when the winning level returns an empty
// set is the next level tried. This is canonical to the
// architecture doc's "ordine di priorità": a manual-approved
// phrase hit MUST short-circuit the cascade (so a human
// association never gets overwritten by an external search).
//
// godlike/07 NO-FAKE-AVAILABILITY: every level surfaces a typed
// failure rather than silently fall-through. Levels 0/1 hit the
// cache via ConceptRepository; Level 8 falls through to
// CandidateRepository; Level 9 hits the external SearchFanOut
// (IF ResolvePolicy.AllowExternalSearch = true). Per-level
// errors propagate into the ResolveResult.Warnings array so
// callers can branch on Partial/BackendErrors deterministically.
//
// File split (godlike/06 single canonical home per layer):
//   - resolver.go                 : Resolver port + VisualResolver struct + ResolverDeps + ctors + pins + EmbeddingVersion  ← this file
//   - resolver_lookup.go          : canonicalConceptForLookup + fingerprintForNormalized
//   - resolver_orchestration.go   : Resolve + resolveScene + candidatesForSlot + levelExactMatch + mediaTypesForSlot + priorSceneVideoID + defaultResolverLimit
//   - resolver_scoring.go         : rankedCandidate + buildFilterFlags + aspectMismatchFor + buildRankingInput + durationFitScore + clamp01 + sort + layerFromFilteredCandidate + upgradeSource
//   - resolver_projection.go      : bindingsToFilteredCandidates + candidatesToFilteredCandidates
//   - resolver_brain.go           : errInvalidPhrase + Search method (brain.MediaMemoryResolutionPort impl)
package mediamemory

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/brain"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
)

// Resolver is the canonical port exposed to api/mediamemory.
// Concrete impl is VisualResolver (this file, below).
type Resolver interface {
	// Resolve produces a ResolveResult for the input request. The
	// resolver MUST honour ctx.Done() at every level iteration.
	Resolve(ctx context.Context, req ResolveRequest) (ResolveResult, error)
}

// ── Canonical implementation (concrete) ───────────────────────────

// VisualResolver is the canonical implementation of Resolver. It
// composes the 9-level priority pipeline. Each level reads from
// the canonical ports declared in ports.go — there is no parallel
// "fast path" (godlike/06 SSOT).
//
// godlike/06 SSOT (Fase 2.3 anti-repetition wiring): the resolver
// holds an optional UsageRepository (nil-safe composition) so the
// per-project history read can flow through the canonical
// append-only audit log. When nil, the resolver degrades
// gracefully: RepetitionPenalty stays 0 (no penalty input
// available) and the ranker still scores candidates normally.
type VisualResolver struct {
	concepts   ConceptRepository
	bindings   BindingRepository
	external   SearchFanOut
	semantic   SemanticLookup
	usage      UsageRepository // optional; nil-safe for backward compat
	ranker     Ranker
	normalizer Normalizer // godlike/06 SSOT: SINGLE canonical normalization surface
	log        Logger
	clock      Clock
	metrics    MetricsSink
}

// ResolverDeps keeps the resolver composition boundary typed and stable as
// optional anti-repetition and observability ports evolve.
type ResolverDeps struct {
	Concepts ConceptRepository
	Bindings BindingRepository
	External SearchFanOut
	Semantic SemanticLookup
	Usage    UsageRepository
	Ranker   Ranker
	Log      Logger
	Clock    Clock
	Metrics  MetricsSink
}

// NewVisualResolver constructs the resolver with the canonical
// dependency set. Composition root wires concrete adapters.
//
// godlike/06 SSOT: the Normalizer is REQUIRED (composition-root
// must inject *defaultNormalizer from normalizer.go) so the
// Level 0/1/2 fingerprint lookup uses the canonical SHA256 +
// NFC + lowercase + dedup-whitespace + terminal-punctuation-strip
// algorithm. A nil normalizer triggers NewCanonicalNormalizer
// (composition-root-friendly default) so test harnesses can
// pass nil without breaking the SSOT.
//
// godlike/06 SSOT (Fase 2.3 wiring): the optional UsageRepository
// is the consumer seam for ListProjectUsages. A nil usage
// surfaces as "anti-repetition disabled" — penalties stay 0 and the
// ranker still scores candidates normally. Composition root wires
// the canonical concrete UsageRepository (sqlite-backed) unless
// the caller explicitly opts out (e.g. test harnesses).
func NewVisualResolver(deps ResolverDeps) *VisualResolver {
	deps.Usage = nil
	return NewVisualResolverWithUsage(deps)
}

// NewVisualResolverWithUsage is the canonical Fase 2.3
// constructor. Composition root uses this form when wiring the
// concrete UsageRepository so repetition_penalty has identity.
func NewVisualResolverWithUsage(deps ResolverDeps) *VisualResolver {
	if deps.Log == nil {
		deps.Log = NoopLogger()
	}
	if deps.Clock == nil {
		deps.Clock = RealClock()
	}
	if deps.Metrics == nil {
		deps.Metrics = NoopMetrics()
	}
	return &VisualResolver{
		concepts:   deps.Concepts,
		bindings:   deps.Bindings,
		external:   deps.External,
		semantic:   deps.Semantic,
		usage:      deps.Usage,
		ranker:     deps.Ranker,
		normalizer: NewDefaultNormalizer(""), // godlike/06 SSOT: canonical SHA256 surface
		log:        deps.Log,
		clock:      deps.Clock,
		metrics:    deps.Metrics,
	}
}

// Compile-time assertion: VisualResolver satisfies Resolver.
var _ Resolver = (*VisualResolver)(nil)

// Compile-time assertion: VisualResolver satisfies the Brain's
// MediaMemoryResolutionPort. This is the single bridge that exposes
// the 9-level cascade to the canonical Brain orchestrator.
var _ brain.MediaMemoryResolutionPort = (*VisualResolver)(nil)

// EmbeddingVersion returns the version of the embedding model / schema
// used by the semantic search path consumed by the cascade. The cascade
// owns this version; the Brain reads it only for the decision fingerprint.
func (r *VisualResolver) EmbeddingVersion() string {
	return media.VersionEmbedding
}
