package artlist

import (
	"context"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// fakeConceptRepository records upserted concepts and returns a deterministic ID.
type fakeConceptRepository struct {
	concepts []mediamemory.MediaConcept
}

func (r *fakeConceptRepository) Upsert(ctx context.Context, c mediamemory.MediaConcept) (mediamemory.MediaConcept, error) {
	c.ID = "concept-" + c.PhraseFingerprint[:8]
	c.CreatedAt = time.Now().UTC()
	c.UpdatedAt = c.CreatedAt
	r.concepts = append(r.concepts, c)
	return c, nil
}

func (r *fakeConceptRepository) FindByID(ctx context.Context, id string) (mediamemory.MediaConcept, error) {
	for _, c := range r.concepts {
		if c.ID == id {
			return c, nil
		}
	}
	return mediamemory.MediaConcept{}, mediamemory.ErrConceptNotFound
}

func (r *fakeConceptRepository) FindByFingerprint(ctx context.Context, language, fingerprint string) (mediamemory.MediaConcept, error) {
	for _, c := range r.concepts {
		if c.Language == language && c.PhraseFingerprint == fingerprint {
			return c, nil
		}
	}
	return mediamemory.MediaConcept{}, mediamemory.ErrConceptNotFound
}

func (r *fakeConceptRepository) FindManyByFingerprints(ctx context.Context, language string, fingerprints []string) ([]mediamemory.MediaConcept, error) {
	return nil, nil
}

// fakeBindingRepository records upserted bindings.
type fakeBindingRepository struct {
	bindings []mediamemory.MediaBinding
}

func (r *fakeBindingRepository) Upsert(ctx context.Context, b mediamemory.MediaBinding) (mediamemory.MediaBinding, error) {
	b.ID = "binding-" + b.ConceptID + "-" + string(b.SlotKind)
	r.bindings = append(r.bindings, b)
	return b, nil
}

func (r *fakeBindingRepository) FindByID(ctx context.Context, id string) (mediamemory.MediaBinding, error) {
	return mediamemory.MediaBinding{}, nil
}

func (r *fakeBindingRepository) ListApprovedByConcept(ctx context.Context, conceptID string, slotKinds []mediamemory.SlotKind, limit int) ([]mediamemory.MediaBinding, error) {
	return nil, nil
}

func (r *fakeBindingRepository) ListApprovedByConcepts(ctx context.Context, conceptIDs []string, slotKinds []mediamemory.SlotKind, limit int) (map[string][]mediamemory.MediaBinding, error) {
	return nil, nil
}

func (r *fakeBindingRepository) ListByConcept(ctx context.Context, conceptID string) ([]mediamemory.MediaBinding, error) {
	return nil, nil
}

func (r *fakeBindingRepository) ListByAsset(ctx context.Context, assetID string) ([]mediamemory.MediaBinding, error) {
	return nil, nil
}

func (r *fakeBindingRepository) Delete(ctx context.Context, id string) error {
	return nil
}

func TestMediaMemoryLinker_CreatesMayaConceptsAndBindings(t *testing.T) {
	concepts := &fakeConceptRepository{}
	bindings := &fakeBindingRepository{}
	linker := newMediaMemoryLinker(concepts, bindings, mediamemory.NewDefaultNormalizer(""), zap.NewNop())

	ctx := context.Background()
	err := linker.linkMayaConcepts(ctx, "asset-123", "it", mediamemory.SlotPrimaryVideo)
	assert.NoError(t, err)

	// 7 concepts: topic,  entities, action, 3 concepts.
	assert.Len(t, concepts.concepts, 7, "expected 7 Maya concepts to be created")
	assert.Len(t, bindings.bindings, 7, "expected 7 Maya bindings to be created")

	// Verify topic concept.
	var topic *mediamemory.MediaConcept
	for i := range concepts.concepts {
		c := concepts.concepts[i]
		if c.CanonicalText == "civilizzazione maya" {
			topic = &c
			break
		}
	}
	assert.NotNil(t, topic, "topic concept 'civilizzazione maya' should exist")
	assert.Equal(t, mediamemory.ConceptTopic, topic.ConceptType)

	// Verify a binding for the topic concept points to the asset and slot.
	found := false
	for _, b := range bindings.bindings {
		if b.ConceptID == topic.ID && b.AssetID == "asset-123" && b.SlotKind == mediamemory.SlotPrimaryVideo {
			found = true
			assert.Equal(t, mediamemory.OriginAutoLink, b.Origin)
			assert.Equal(t, mediamemory.ApprovalApproved, b.ApprovalStatus)
			assert.Equal(t, mediamemory.ProviderArtlist, b.Provider)
		}
	}
	assert.True(t, found, "expected a binding for the topic concept")
}

func TestMediaMemoryLinker_DisabledWhenDepsNil(t *testing.T) {
	linker := newMediaMemoryLinker(nil, nil, nil, zap.NewNop())
	assert.True(t, linker.disabled(), "linker should be disabled when dependencies are nil")

	ctx := context.Background()
	err := linker.linkMayaConcepts(ctx, "asset-123", "it", mediamemory.SlotPrimaryVideo)
	assert.NoError(t, err)
}
