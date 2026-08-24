// Package mediamemory — registry.go is the canonical freezeable
// strategy registry for the MediaMemory capability.
//
// godlike/06 SSOT (mirror of search.BackendRegistry): the
// StrategyRegistry is the SOLE owner of the canonical vocabulary
// of concept-type strategies, visual-action strategies, and
// provider-specific resolution strategies. New strategies
// (e.g. a new concept-type handler for ConceptEmotion) plug in
// via Register() at composition root; after Freeze() any late
// call to Register returns ErrFrozen. The locking + empty-name +
// typed-nil-pointer guards mirror the search.BackendRegistry
// contract verbatim so the operational guarantees are uniform
// across the project.
//
// godlike/06 SSOT (one canonical owner per fact): this is the
// ONLY mutable registry in the mediamemory capability. The
// durable registries (binding_repository, concept_repository)
// are owned by the repo ports — Phase 1.2 wires their concrete
// SQLite impls. The StrategyRegistry is the in-process, mutable,
// composition-time registry (different lifecycle, different
// owner).
//
// godlike/07 NO-FAKE-AVAILABILITY: ErrNilStrategy +
// ErrEmptyName + ErrFrozen + ErrAlreadyRegistered are the typed
// sentinels wrapping the canonical envelope so callers branch
// via errors.Is. String-matching for "duplicate" or "frozen"
// returns is a programming error.
package mediamemory

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
)

// ── Typed error envelope (godlike/07) ──────────────────────────────

var (
	ErrNilStrategy = errors.New(
		"mediamemory: nil strategy passed to Register (typed-nil pointers are rejected)",
	)
	ErrEmptyName = errors.New(
		"mediamemory: strategy Name() returned empty string (canonical SSOT requires non-empty identity)",
	)
	ErrFrozen = errors.New(
		"mediamemory: registry is frozen; further Register calls are rejected",
	)
	ErrAlreadyRegistered = errors.New(
		"mediamemory: a strategy with the same Name is already registered",
	)
	ErrStrategyNotFound = errors.New(
		"mediamemory: no strategy registered for the requested name",
	)
)

// Strategy is the per-name contract the registry holds. Each
// strategy knows how to handle one concept-type / visual-action /
// provider-specific lookup. Concrete strategy kinds live in
// resolver.go siblings or as separate strategy_*.go files (future
// forward-pointer to PR-MEDIAMEMORY-STRATEGIES).
type Strategy interface {
	// Name returns the canonical identifier (e.g. "maya", "boxing",
	// "history", "emotion_specific"). Must be unique within a
	// StrategyRegistry. Stable across calls.
	Name() string

	// ConceptTypes returns the closed set of ConceptType values the
	// strategy applies to. Empty means the strategy is registered
	// for every concept type (catch-all wildcard; use sparingly).
	ConceptTypes() []ConceptType

	// ResolvesConcept is a hint to VisualResolver about whether this
	// strategy is eligible for a given concept. The resolver
	// composes Strategy.Eligible across multiple registries; the
	// strategy itself MUST NOT be the final arbiter of resolution.
	ResolvesConcept(c MediaConcept) bool
}

// NormalizationStrategy is a Strategy specialised for the normalizer
// registry. It adds a single Normalize() entrypoint on top of the
// strategy contract.
type NormalizationStrategy interface {
	Strategy
	// Normalize produces a MediaConcept-shaped record for the input
	// phrase. Implementations populate CanonicalText / NormalizedText
	// / PhraseFingerprint / ConceptType. They MUST NOT call into
	// the database (port discipline).
	Normalize(text, language string) (MediaConcept, error)
}

// RankingStrategy is a Strategy specialised for the ranker registry.
// New ranking modes (time-aware, diversity-only, ...) plug in here.
type RankingStrategy interface {
	Strategy
	// Rank produces a deterministic re-ordering of the input
	// slice. MUST NOT mutate the input.
	Rank(in []Layer) ([]Layer, error)
}

// ── StrategyRegistry ───────────────────────────────────────────────

// StrategyRegistry is the canonical in-process, freezeable registry
// of mediamemory strategies. Locking contract (parallel of
// search.BackendRegistry):
//
//   - Register         acquires mu.Lock()  and releases via defer.
//   - Lookup / All     acquire mu.RLock()   and release via defer.
//   - Freeze           acquires mu.Lock()  (write lock so a concurrent
//     Register blocks until
//     the writer observes the
//     frozen bit).
type StrategyRegistry struct {
	mu      sync.RWMutex
	entries map[string]Strategy
	frozen  bool
}

// NewStrategyRegistry returns an empty, mutable registry.
func NewStrategyRegistry() *StrategyRegistry {
	return &StrategyRegistry{entries: make(map[string]Strategy)}
}

// Register adds a strategy under its Name().
// Returns:
//   - ErrNilStrategy         if s is the zero Strategy value or a
//     typed-nil pointer.
//   - ErrEmptyName           if s.Name() returns "".
//   - ErrFrozen              if the registry is already frozen.
//   - ErrAlreadyRegistered   if a strategy with the same Name exists.
func (r *StrategyRegistry) Register(s Strategy) error {
	if s == nil {
		return ErrNilStrategy
	}
	if rv := reflect.ValueOf(s); rv.Kind() == reflect.Ptr && rv.IsNil() {
		return ErrNilStrategy
	}
	name := s.Name()
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
	r.entries[name] = s
	return nil
}

// Freeze locks the registry. Idempotent.
func (r *StrategyRegistry) Freeze() {
	r.mu.Lock()
	r.frozen = true
	r.mu.Unlock()
}

// IsFrozen reports whether the registry has been frozen.
func (r *StrategyRegistry) IsFrozen() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.frozen
}

// All returns every strategy, sorted by Name() for determinism.
func (r *StrategyRegistry) All() []Strategy {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Strategy, 0, len(r.entries))
	for _, s := range r.entries {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Lookup returns the strategy under `name`. ErrStrategyNotFound is
// wrapped so callers branch via errors.Is.
func (r *StrategyRegistry) Lookup(name string) (Strategy, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.entries[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrStrategyNotFound, name)
	}
	return s, nil
}

// EligibleFor returns the subset of strategies whose
// ConceptTypes() intersect c.ConceptType. Wildcards
// (ConceptTypes() == nil) are always eligible.
func (r *StrategyRegistry) EligibleFor(c MediaConcept) []Strategy {
	all := r.All()
	out := make([]Strategy, 0, len(all))
	for _, s := range all {
		if s.ResolvesConcept(c) {
			out = append(out, s)
		}
	}
	return out
}

// freezableRegistry is the canonical narrow interface the
// composition root wires against. godlike/06 SSOT: production
// adapter code is written against freezableRegistry, not against
// *StrategyRegistry directly, so a future Swap (e.g. to a
// concurrent-map backed registry) does not break call sites.
type freezableRegistry interface {
	Freeze()
	IsFrozen() bool
}

// Compile-time assertion: *StrategyRegistry implements the canonical
// freezableRegistry interface. Drift in field layout or method set
// surfaces as a build error, not a runtime nil-deref.
var _ freezableRegistry = (*StrategyRegistry)(nil)
