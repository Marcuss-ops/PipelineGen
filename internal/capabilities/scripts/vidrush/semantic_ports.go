// Package vidrush — semantic_ports.go: the port and pure
// value contracts of the Fase 1-5 semantic cutover (SceneIR → VisualNER →
// MediaSampler → Local Stock → MediaCert).
//
// Only interfaces and function types live here; the implementations are in
// semantic_chain.go and the MediaCert barrier in mediacert_barrier.go.
// BrainRust's VisualNER and MediaSampler release workers are reached through
// these interfaces so production can swap in the stdio-JSON FFI adapter
// without the coordinator knowing about process spawning.
//
// The entity value (contract.VisualEntity) and the pure projections over it
// (grounding, deduplication, identity ranking) live in scripts/contract and
// scripts/semantics: they are shared with the scripts root, so they cannot
// live in a package the root does not depend on. What remains here is the
// semantic chain's own port surface.
//
// Named entities use the shared NERBackend contract below; editorial phrase
// selection remains a separate deterministic surface over scene text and the
// configured lexicon.
package vidrush

import (
	"context"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediacert"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/stockintelligence"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// NERBackend is the shared extraction contract for interchangeable named-
// entity backends. Implementations return source-grounded UTF-8 byte spans;
// the scene enricher owns the single validation/normalization/deduplication
// path regardless of which backend is selected.
type NERBackend interface {
	Extract(ctx context.Context, language string, sourceText string, entityCount int) ([]contract.VisualEntity, error)
}

// VisualNERPort is retained as the domain-specific name used by the scene
// pipeline; it is the same contract as NERBackend, not a second interface.
type VisualNERPort = NERBackend

// LocalStockResolverPort is the LOCAL FIRST PROVIDER SECOND resolver.
// The stockintelligence.Service is the production implementation; it consults
// the local pgvector search + SQLite hydrate first and falls back to the provider
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

// imageSearchEntities derives the identity surface from the normal NLP
// entities. People and brands are prioritized for verified identity imagery;
// when neither category exists, the historical entity fan-out is preserved.
func imageSearchEntities(entities []contract.VisualEntity, categoryOnly bool) []contract.VisualEntity {
	if len(entities) == 0 {
		return nil
	}
	identities := make([]contract.VisualEntity, 0, len(entities))
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
	if categoryOnly {
		return nil
	}
	return append([]contract.VisualEntity(nil), entities...)
}
