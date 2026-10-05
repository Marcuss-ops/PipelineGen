// Package scriptgeneration — vidrush_semantic_ports.go: the port and pure
// value contracts of the Fase 1-5 semantic cutover (SceneIR → VisualNER →
// MediaSampler → Local Stock → MediaCert).
//
// Only interfaces, function types, the VisualEntity wire mirror and the PURE
// helpers that operate on it live here; the implementations are in
// vidrush_semantic_chain.go and the MetalCert barrier in
// vidrush_mediacert_barrier.go. The Rust crates (rust/visualner,
// rust/mediasampler) are reached through these interfaces so production can
// swap in the stdio-JSON FFI adapter without the coordinator knowing about
// process spawning.
//
// Extracted 2026-09-12 from vidrush_semantic_chain.go to keep every file under
// max_lines_per_file_strict=600 (godlike/08 forward-prevention cap).
//
// The grounding / entity fan-out helpers below were moved here from
// vidrush_semantic_chain.go on 2026-09-16 for the same reason. They are pure
// functions over VisualEntity and source text — no receiver, no I/O — so they
// belong with the value contract they operate on. Important phrase candidates
// are selected in the leaf package scripts/phrases, then grounded here; entity
// fan-out helpers validate and project identities without NLP calls.
//
// Named entities use the shared NERBackend contract below; editorial phrase
// selection remains a separate deterministic surface over scene text and the
// configured lexicon.
package scriptgeneration

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediacert"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/stockintelligence"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// VisualEntity is the common source-grounded entity returned by every NER
// backend and projected through the scene pipeline.
type VisualEntity struct {
	Text     string               `json:"text"`
	Type     scriptpkg.EntityType `json:"type"`
	Score    float32              `json:"score"`
	Start    int                  `json:"start"`
	End      int                  `json:"end"`
	Evidence string               `json:"evidence,omitempty"`
}

// NERBackend is the shared extraction contract for interchangeable named-
// entity backends. Implementations return source-grounded UTF-8 byte spans;
// the scene enricher owns the single validation/normalization/deduplication
// path regardless of which backend is selected.
type NERBackend interface {
	Extract(ctx context.Context, language string, sourceText string, entityCount int) ([]VisualEntity, error)
}

// VisualNERPort is retained as the domain-specific name used by the scene
// pipeline; it is the same contract as NERBackend, not a second interface.
type VisualNERPort = NERBackend

// VisualNERBackendRegistry contains the concrete backends available in one
// composition. Selection is explicit and never falls back to another backend:
// that keeps benchmark results and runtime behavior reproducible.
type VisualNERBackendRegistry struct {
	backends map[string]NERBackend
}

// NewVisualNERBackendRegistry validates and copies the available backend map.
func NewVisualNERBackendRegistry(backends map[string]NERBackend) (*VisualNERBackendRegistry, error) {
	if len(backends) == 0 {
		return nil, fmt.Errorf("scriptgeneration: at least one NER backend is required")
	}
	registered := make(map[string]NERBackend, len(backends))
	for name, backend := range backends {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" || backend == nil {
			return nil, fmt.Errorf("scriptgeneration: NER backend name and implementation are required")
		}
		if _, exists := registered[key]; exists {
			return nil, fmt.Errorf("scriptgeneration: duplicate NER backend %q", key)
		}
		registered[key] = backend
	}
	return &VisualNERBackendRegistry{backends: registered}, nil
}

// Resolve selects a configured backend or returns an error. There is no
// silent fallback because that would invalidate quality/performance results.
func (r *VisualNERBackendRegistry) Resolve(name string) (NERBackend, error) {
	if r == nil {
		return nil, fmt.Errorf("scriptgeneration: NER backend registry is not configured")
	}
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return nil, fmt.Errorf("scriptgeneration: NER backend selection is required")
	}
	backend, ok := r.backends[key]
	if !ok {
		return nil, fmt.Errorf("scriptgeneration: NER backend %q is unavailable (registered: %s)", key, strings.Join(sortedNERBackendNames(r.backends), ", "))
	}
	return backend, nil
}

func sortedNERBackendNames(backends map[string]NERBackend) []string {
	names := make([]string, 0, len(backends))
	for name := range backends {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// LocalStockResolverPort is the LOCAL FIRST PROVIDER SECOND resolver.
// The stockintelligence.Service is the production implementation; it consults
// the local Qdrant search + SQLite hydrate first and falls back to the provider
// only when local_candidates < threshold or best_score < minimum_quality.
type LocalStockResolverPort interface {
	Resolve(ctx context.Context, req stockintelligence.ResolveRequest) (stockintelligence.ResolveResult, error)
}

// MediaCertifierPort certifies a completed VidRush run against a spec. The
// mediacert.Certify function is the production implementation. A
// CERTIFIED=false report must fail the job even when JobStatus=SUCCEEDED.
type MediaCertifierPort interface {
	Certify(ctx context.Context, spec mediacert.Spec, result mediacert.MediaResult) (mediacert.Report, error)
}

// MediaCertifierFunc adapts the canonical mediacert.Certify function to the
// pipeline boundary. It deliberately contains no certification rules.
type MediaCertifierFunc func(context.Context, mediacert.Spec, mediacert.MediaResult) (mediacert.Report, error)

func (f MediaCertifierFunc) Certify(ctx context.Context, spec mediacert.Spec, result mediacert.MediaResult) (mediacert.Report, error) {
	return f(ctx, spec, result)
}

// MediaCertSpecResolver creates the run-specific contract from the resolved
// plan instead of using a hard-coded fixture in production.
type MediaCertSpecResolver interface {
	ResolveMediaCertSpec(*scriptpkg.ResolvedGenerationPlan) mediacert.Spec
}

type MediaCertSpecResolverFunc func(*scriptpkg.ResolvedGenerationPlan) mediacert.Spec

func (f MediaCertSpecResolverFunc) ResolveMediaCertSpec(plan *scriptpkg.ResolvedGenerationPlan) mediacert.Spec {
	return f(plan)
}

func generationPlanLanguage(plan *scriptpkg.ResolvedGenerationPlan) string {
	if plan == nil {
		return ""
	}
	return plan.Language
}

func groundImportantPhrases(source string, entities []VisualEntity, phrases []string, limit int) []string {
	if limit <= 0 {
		limit = len(phrases)
	}
	out := make([]string, 0, min(limit, len(phrases)))
	seen := make(map[string]struct{}, len(phrases))

	// Entity spans do not depend on the phrase being tested, so they are
	// resolved ONCE per segment instead of once per (phrase, entity) pair. The
	// previous nested resolution made this gate O(phrases × entities × source
	// length) on every scene.
	entitySpans := make([]scriptpkg.AnnotationSpan, 0, len(entities))
	for _, entity := range entities {
		if entitySpan, entityOK := findEntitySpan(source, entity.Text); entityOK {
			entitySpans = append(entitySpans, entitySpan)
		}
	}

	for _, phrase := range phrases {
		phrase = strings.TrimSpace(phrase)
		if len(strings.Fields(phrase)) < 2 {
			continue
		}
		// The proper-name filter (phrases.ContainsProperNamePair) is applied
		// exactly once per candidate source: phrases.Select owns the
		// selector-derived candidates, and the operator-supplied
		// MediaExtractionPolicy hints are filtered at their ingress in
		// vidrush_semantic_chain.go. Re-checking here would validate the same
		// selector output a second time.
		span, ok := findEntitySpan(source, phrase)
		if !ok {
			continue
		}
		// A phrase is editorial text, not an entity-bearing label. Reject
		// spans that overlap any extracted named entity, including
		// partial forms such as "LeBron James and Johann" or "Sebastian
		// Bach". Those belong to the entity surface only.
		phraseOverlapsEntity := false
		for _, entitySpan := range entitySpans {
			if span.StartRune < entitySpan.EndRune && entitySpan.StartRune < span.EndRune {
				phraseOverlapsEntity = true
				break
			}
		}
		if phraseOverlapsEntity {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(span.Text))
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, span.Text)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// entityRuneSpans resolves the RUNE spans of the grounded entity surfaces in
// source. The phrase selector consumes these as its blocked ranges, so no
// phrase can cover a name the entity overlays own. Grounding stays here, with
// findEntitySpan and the VisualEntity contract, which keeps scripts/phrases a
// leaf package with a neutral input instead of a second entity model.
func entityRuneSpans(source string, entities []VisualEntity) [][2]int {
	spans := make([][2]int, 0, len(entities))
	for _, entity := range entities {
		if span, ok := findEntitySpan(source, entity.Text); ok {
			spans = append(spans, [2]int{span.StartRune, span.EndRune})
		}
	}
	return spans
}

// imageSearchEntities derives the identity surface from the normal NLP
// entities. People and brands are prioritized for verified identity imagery;
// when neither category exists, the historical entity fan-out is preserved.
func imageSearchEntities(entities []VisualEntity, categoryOnly ...bool) []VisualEntity {
	if len(entities) == 0 {
		return nil
	}
	identities := make([]VisualEntity, 0, len(entities))
	for _, entity := range entities {
		if !strings.EqualFold(string(entity.Type), string(scriptpkg.EntityTypePerson)) &&
			!strings.EqualFold(string(entity.Type), "BRAND") &&
			!strings.EqualFold(string(entity.Type), "LOGO") &&
			!strings.EqualFold(string(entity.Type), string(scriptpkg.EntityTypeOrganization)) &&
			!strings.EqualFold(string(entity.Type), "ORG") {
			continue
		}
		identities = append(identities, entity)
	}
	if len(identities) > 0 {
		return identities
	}
	// Category/value selectors do not fan numeric/date/location values into
	// image search. Preserve the legacy fallback only for truly broad entity
	// requests with no selected typed category.
	if len(categoryOnly) > 0 && categoryOnly[0] {
		return nil
	}
	return append([]VisualEntity(nil), entities...)
}

func normalizeVisualPersonName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(value, "While "), "while "))
	for _, suffix := range []string{"'s", "’s"} {
		if len(value) > len(suffix) && strings.EqualFold(value[len(value)-len(suffix):], suffix) {
			value = strings.TrimSpace(value[:len(value)-len(suffix)])
			break
		}
	}
	return value
}
