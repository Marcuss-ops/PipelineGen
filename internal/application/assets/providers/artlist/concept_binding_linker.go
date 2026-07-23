package artlist

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory"
	"go.uber.org/zap"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
)

// mediaMemoryLinker creates media_concepts and media_bindings rows for a
// materialized Artlist asset. It is an OPTIONAL dependency: when the
// repositories are not wired, linking is skipped without failing the run.
//
// godlike/06 SSOT: topic-specific linking (Maya, boxing, history, ...) MUST
// route through the SAME mediamemory ports; this linker is the single
// Artlist-specific adapter, not a parallel binding table.
type mediaMemoryLinker struct {
	concepts   mediamemory.ConceptRepository
	bindings   mediamemory.BindingRepository
	normalizer mediamemory.Normalizer
	log        *zap.Logger
}

// newMediaMemoryLinker returns a linker bound to the canonical mediamemory
// repositories. Any nil dependency makes the linker a no-op (graceful
// degradation when mediamemory is not wired).
func newMediaMemoryLinker(concepts mediamemory.ConceptRepository, bindings mediamemory.BindingRepository, normalizer mediamemory.Normalizer, log *zap.Logger) *mediaMemoryLinker {
	return &mediaMemoryLinker{
		concepts:   concepts,
		bindings:   bindings,
		normalizer: normalizer,
		log:        log,
	}
}

// conceptSpec pairs raw concept text with its type so the linker can
// create media_concepts and media_bindings without embedding topic
// knowledge in the linker itself.
type conceptSpec struct {
	text        string
	conceptType mediamemory.ConceptType
}

// linkConcepts creates the requested concepts and binds each one to the
// provided asset. It is idempotent at the repository level: concepts are
// upserted by (language, phrase_fingerprint) and bindings are upserted
// by (concept_id, asset_id, slot_kind).
func (l *mediaMemoryLinker) linkConcepts(ctx context.Context, assetID, language string, slot media.SlotKind, specs []conceptSpec) error {
	if l.disabled() {
		return nil
	}
	if assetID == "" {
		return nil
	}

	for _, spec := range specs {
		concept, err := l.ensureConcept(ctx, spec.text, language, spec.conceptType)
		if err != nil {
			l.log.Warn("failed to ensure concept",
				zap.String("concept", spec.text),
				zap.Error(err))
			continue
		}
		if err := l.ensureBinding(ctx, concept.ID, assetID, slot); err != nil {
			l.log.Warn("failed to ensure binding",
				zap.String("concept", concept.CanonicalText),
				zap.String("asset_id", assetID),
				zap.Error(err))
		}
	}
	return nil
}

// mayaConceptSpecs is the hard-coded Maya/Venus concept set used by the
// legacy Artlist Maya run. It is a package-level catalog to avoid repeated
// allocations.
// TODO: replace with topic-driven concept discovery once the run request
// carries an explicit topic/concept list.
var mayaConceptSpecs = []conceptSpec{
	{"civilizzazione maya", mediamemory.ConceptTopic},
	{"maya", mediamemory.ConceptEntity},
	{"venere", mediamemory.ConceptEntity},
	{"osservare", mediamemory.ConceptAction},
	{"astronomia maya", mediamemory.ConceptTopic},
	{"templi maya", mediamemory.ConceptTopic},
	{"pianeta venere", mediamemory.ConceptTopic},
}

// linkMayaConcepts is the legacy entry point for Maya topic runs.
// It is a thin wrapper around the generic linkConcepts helper so the
// topic-specific concept list can be moved to configuration later.
func (l *mediaMemoryLinker) linkMayaConcepts(ctx context.Context, assetID, language string, slot media.SlotKind) error {
	return l.linkConcepts(ctx, assetID, language, slot, mayaConceptSpecs)
}

// ensureConcept upserts a media_concepts row from raw text.
func (l *mediaMemoryLinker) ensureConcept(ctx context.Context, text, language string, conceptType mediamemory.ConceptType) (mediamemory.MediaConcept, error) {
	concept, err := l.normalizer.Normalize(ctx, text, language)
	if err != nil {
		return mediamemory.MediaConcept{}, fmt.Errorf("normalize concept %q: %w", text, err)
	}
	concept.ConceptType = conceptType

	return l.concepts.Upsert(ctx, concept)
}

// ensureBinding upserts a media_bindings row.
func (l *mediaMemoryLinker) ensureBinding(ctx context.Context, conceptID, assetID string, slot media.SlotKind) error {
	binding := mediamemory.MediaBinding{
		ConceptID:      conceptID,
		AssetID:        assetID,
		SlotKind:       slot,
		Origin:         mediamemory.OriginAutoLink,
		ApprovalStatus: mediamemory.ApprovalApproved,
		Provider:       mediamemory.ProviderArtlist,
	}
	_, err := l.bindings.Upsert(ctx, binding)
	return err
}

func (l *mediaMemoryLinker) disabled() bool {
	return l == nil || l.concepts == nil || l.bindings == nil || l.normalizer == nil || l.log == nil
}
