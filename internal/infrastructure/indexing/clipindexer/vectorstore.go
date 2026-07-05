package clipindexer

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// SetVectorStore sets the vector store indexer for Qdrant upsert after indexing.
func (s *Service) SetVectorStore(vs VectorStoreIndexer) {
	s.vectorStore = vs
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
