package clipindexer

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// SetVectorStore sets the vector store indexer for Qdrant upsert after indexing.
func (s *Service) SetVectorStore(vs VectorStoreIndexer) {
	s.vectorStore = vs
}

// SetProjectionSequenceAdvancer wires the optional checkpoint advancer called
// after a successful Qdrant upsert. It keeps the ACTIVE projection's
// source_registry_seq current with incremental indexing so the startup
// sequence gate does not fail closed on the next restart.
func (s *Service) SetProjectionSequenceAdvancer(advancer capregistry.ProjectionSequenceAdvancer) {
	s.projectionAdvancer = advancer
}

// advanceProjectionSequence advances the ACTIVE projection checkpoint after a
// successful upsert. It is best-effort: the index already succeeded, and a
// transient advance failure is logged and self-heals on the next successful
// index. nil advancer (tests / Qdrant disabled) is a no-op.
func (s *Service) advanceProjectionSequence(ctx context.Context, clipID string) {
	if s.projectionAdvancer == nil {
		return
	}
	if err := s.projectionAdvancer.AdvanceActiveProjectionSequence(ctx); err != nil {
		s.log.Warn("clip indexed but projection checkpoint advance failed (will retry on next index)",
			zap.String("clip_id", clipID),
			zap.Error(err))
	}
}

// UpsertVectorStore pushes the newly indexed clip to Qdrant if a vector store is configured.
// Returns a terminal error when vectorStore is nil — this is a composition bug:
// SetVectorStore was never called but the service is enabled and called.
func (s *Service) UpsertVectorStore(ctx context.Context, clipID string) error {
	if s.vectorStore == nil {
		return fmt.Errorf("clipindexer: vector store not wired for %s — SetVectorStore was never called (composition error)", clipID)
	}
	if err := s.vectorStore.UpsertFromClip(ctx, clipID); err != nil {
		s.log.Warn("failed to upsert clip to vector store",
			zap.String("clip_id", clipID),
			zap.Error(err))
		return err
	}
	s.log.Debug("vector store upserted clip",
		zap.String("clip_id", clipID))
	return nil
}
