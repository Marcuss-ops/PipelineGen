// Package qdrantmm — qdrant_indexer.go is the canonical concrete
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
package qdrantmm

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
	qdrantindexing "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/indexing"
	platformschema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// Concept point IDs use schema.ConceptPointIDPrefix (SSOT in
// schema/projection_contract.go); no local copy here.

// QdrantIndexer is the canonical mediamemory.EmbeddingIndexer
// backed by Qdrant + EmbeddingChannelRegistry.
type QdrantIndexer struct {
	client   *transport.Client
	writer   qdrantindexing.ProjectionWriter
	embedder search.EmbeddingChannelRegistry
	log      *zap.Logger
}

// NewQdrantIndexer constructs the canonical QdrantIndexer.
func NewQdrantIndexer(client *transport.Client, embedder search.EmbeddingChannelRegistry, log *zap.Logger) *QdrantIndexer {
	return &QdrantIndexer{client: client, writer: qdrantindexing.NewTransportProjectionWriter(client), embedder: embedder, log: log}
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
	if i == nil || i.client == nil || i.writer == nil || i.embedder == nil {
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

	// godlike/07 NO-FAKE-AVAILABILITY: a concept that has not
	// yet been assigned an embedding_version must NOT be indexed
	// silently with the canonical version — the column-vs-payload
	// drift would surface as an out-of-band diagnostic later.
	// Callers MUST set EmbeddingVersion before calling
	// IndexConcept (admin reindex flow uses ReindexConcept which
	// bumps the version before re-writing).
	if c.EmbeddingVersion == "" {
		return fmt.Errorf(
			"mediamemory: concept %q has empty embedding_version (call ReindexConcept or set before IndexConcept): %w",
			c.ID, mediamemory.ErrInvalidBindingInput,
		)
	}

	payload := map[string]any{
		"concept_id":         c.ID,
		"language":           c.Language,
		"phrase_fingerprint": c.PhraseFingerprint,
		"concept_type":       string(c.ConceptType),
		"canonical_text":     c.CanonicalText,
		"normalized_text":    c.NormalizedText,
		"embedding_version":  c.EmbeddingVersion,
	}

	point := schema.Point{
		ID:      platformschema.ConceptPointIDPrefix + c.ID,
		Vectors: vectors,
		Payload: payload,
	}

	if err := i.writer.UpsertProjection(ctx, schema.ConceptCollectionName, []schema.Point{point}); err != nil {
		return fmt.Errorf("mediamemory: QdrantIndexer upsert concept=%q: %w", c.ID, err)
	}
	return nil
}

// DeindexConcept deletes the Qdrant point that corresponds to
// the canonical concept ID.
func (i *QdrantIndexer) DeindexConcept(ctx context.Context, conceptID string) error {
	if i == nil || i.client == nil || i.writer == nil {
		return fmt.Errorf("mediamemory: QdrantIndexer not wired (client required): %w",
			mediamemory.ErrSemanticNotConfigured)
	}
	if conceptID == "" {
		return fmt.Errorf("mediamemory: DeindexConcept with empty conceptID: %w",
			mediamemory.ErrInvalidBindingInput)
	}
	pointID := platformschema.ConceptPointIDPrefix + conceptID
	if err := i.writer.DeleteProjection(ctx, schema.ConceptCollectionName, []string{pointID}); err != nil {
		return fmt.Errorf("mediamemory: QdrantIndexer delete concept=%q: %w", conceptID, err)
	}
	return nil
}

// ReindexConcept bumps the concept's embedding_version and
// rewrites the canonical Qdrant point. The point ID is unchanged
// (concept-<conceptID>) so an in-place overwrite supersedes the
// old vectors + payload with the new ones. The
// ConceptRepository row's embedding_version column must be
// updated by the caller (typically the admin orchestrator that
// owns the reindex loop).
//
// godlike/06 SSOT (Level 0 cache independence under versioning):
// targetVersion="" triggers qdrantschema.BumpEmbeddingVersion
// which computes the canonical next version from the existing
// c.EmbeddingVersion. PhraseFingerprint is INVARIANT under this
// bump — ConceptRepository.FindByFingerprint continues to
// resolve to the same concept_id before and after the rewrite.
// That's the property this contract preserves.
//
// godlike/07: a malformed prior version propagates the
// qdrantschema.BumpEmbeddingVersion error wrapped at the call
// site; the caller decides whether to fall back on the JSON
// manifest of last-known-good versions or abort.
func (i *QdrantIndexer) ReindexConcept(ctx context.Context, c mediamemory.MediaConcept, targetVersion string) error {
	if i == nil || i.client == nil || i.embedder == nil {
		return fmt.Errorf("mediamemory: QdrantIndexer not wired (client + registry required): %w",
			mediamemory.ErrSemanticNotConfigured)
	}
	if c.ID == "" {
		return fmt.Errorf("mediamemory: ReindexConcept with empty concept_id: %w",
			mediamemory.ErrInvalidBindingInput)
	}
	if targetVersion == "" {
		next, err := schema.BumpEmbeddingVersion(c.EmbeddingVersion)
		if err != nil {
			return fmt.Errorf("mediamemory: ReindexConcept bump from %q: %w", c.EmbeddingVersion, err)
		}
		targetVersion = next
	}
	// godlike/06 SSOT (parameter-copy clarity): c is the callee's
	// parameter copy, NOT a reference into the caller's
	// MediaConcept. The assignment below touches only this
	// function's local view before the chained IndexConcept call
	// observes the bumped version. The caller's struct is left
	// untouched so admin reindex loops can fold the result back
	// into ConceptRepository.Upsert without an extra round-trip.
	c.EmbeddingVersion = targetVersion
	return i.IndexConcept(ctx, c)
}
