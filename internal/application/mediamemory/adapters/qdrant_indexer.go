// Package adapters — qdrant_indexer.go is the canonical concrete
// mediamemory.EmbeddingIndexer that bridges the mediamemory
// capability to Qdrant via the canonical EmbeddingChannelRegistry.
//
// godlike/06 SSOT (one canonical owner per fact): the indexer owns
// the per-concept upsert path.
//
// godlike/06 SSOT (composition pattern): this adapter is the SOLE
// bridge between mediamemory and the Qdrant + search stack for
// concept upsert.
//
// godlike/07 NO-FAKE-AVAILABILITY: an absent EmbeddingChannelRegistry
// or Qdrant client is fail-closed: IndexConcept / DeindexConcept
// return wrapped ErrSemanticNotConfigured.
package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/application/search"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
)

// conceptPointIDPrefix is the canonical Qdrant point-id
// derivation for media_concepts.
const conceptPointIDPrefix = "concept-"

// QdrantIndexer is the canonical mediamemory.EmbeddingIndexer
// backed by Qdrant + EmbeddingChannelRegistry.
type QdrantIndexer struct {
	client   *transport.Client
	embedder search.EmbeddingChannelRegistry
	log      *zap.Logger
}

// NewQdrantIndexer constructs the canonical QdrantIndexer.
func NewQdrantIndexer(client *transport.Client, embedder search.EmbeddingChannelRegistry, log *zap.Logger) *QdrantIndexer {
	return &QdrantIndexer{client: client, embedder: embedder, log: log}
}

// Compile-time assertion: QdrantIndexer satisfies
// mediamemory.EmbeddingIndexer.
var _ mediamemory.EmbeddingIndexer = (*QdrantIndexer)(nil)

// IndexConcept computes the multi-channel embedding for the
// canonical phrase and writes a single Qdrant point into
// pipelinegen_media_concepts. ChannelSparse is server-side BM25
// (not computed client-side). ErrChannelNotConfigured /
// ErrChannelNotApplicable are silently skipped for visual +
// audio channels that are stubs today.
func (i *QdrantIndexer) IndexConcept(ctx context.Context, c mediamemory.MediaConcept) error {
	if i == nil || i.client == nil || i.embedder == nil {
		return fmt.Errorf("mediamemory: QdrantIndexer not wired (client + registry required): %w",
			mediamemory.ErrSemanticNotConfigured)
	}
	if c.ID == "" {
		c.ID = uuid.NewString()
	}

	passage := c.CanonicalText
	if passage == "" {
		passage = c.NormalizedText
	}
	if passage == "" {
		return fmt.Errorf(
			"mediamemory: concept %q has empty canonical + normalized text (cannot embed): %w",
			c.ID, mediamemory.ErrInvalidPhrase,
		)
	}

	vectors := make(map[string]any, 1)
	for _, ch := range search.CanonicalChannelNames() {
		if ch == search.ChannelSparse {
			continue
		}
		vec, err := i.embedder.EmbedQuery(ctx, ch, passage)
		if err != nil {
			if errors.Is(err, search.ErrChannelNotConfigured) ||
				errors.Is(err, search.ErrChannelNotApplicable) {
				continue
			}
			return fmt.Errorf("mediamemory: QdrantIndexer embed channel=%q: %w", ch, err)
		}
		if len(vec) == 0 {
			continue
		}
		vectors[ch] = vec
	}

	if len(vectors) == 0 {
		return fmt.Errorf(
			"mediamemory: QdrantIndexer concept=%q has zero configured channels: %w",
			c.ID, mediamemory.ErrSemanticBackendFailed,
		)
	}

	payload := map[string]any{
		"concept_id":         c.ID,
		"language":           c.Language,
		"phrase_fingerprint": c.PhraseFingerprint,
		"concept_type":       string(c.ConceptType),
		"canonical_text":     c.CanonicalText,
		"normalized_text":    c.NormalizedText,
		"embedding_version":  schema.ConceptEmbeddingVersion,
	}

	point := schema.Point{
		ID:      conceptPointIDPrefix + c.ID,
		Vectors: vectors,
		Payload: payload,
	}

	if err := i.client.UpsertPoints(ctx, schema.ConceptCollectionName, []schema.Point{point}); err != nil {
		return fmt.Errorf("mediamemory: QdrantIndexer upsert concept=%q: %w", c.ID, err)
	}
	return nil
}

// DeindexConcept deletes the Qdrant point that corresponds to
// the canonical concept ID.
func (i *QdrantIndexer) DeindexConcept(ctx context.Context, conceptID string) error {
	if i == nil || i.client == nil {
		return fmt.Errorf("mediamemory: QdrantIndexer not wired (client required): %w",
			mediamemory.ErrSemanticNotConfigured)
	}
	if conceptID == "" {
		return fmt.Errorf("mediamemory: DeindexConcept with empty conceptID: %w",
			mediamemory.ErrInvalidBindingInput)
	}
	pointID := conceptPointIDPrefix + conceptID
	if err := i.client.DeletePoints(ctx, schema.ConceptCollectionName, []string{pointID}); err != nil {
		return fmt.Errorf("mediamemory: QdrantIndexer delete concept=%q: %w", conceptID, err)
	}
	return nil
}
