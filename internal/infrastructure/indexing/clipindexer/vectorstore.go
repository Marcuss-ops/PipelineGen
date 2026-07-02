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

// UpsertVectorStoreBulk pushes multiple indexed clips to Qdrant in a single batch.
// Much faster than N individual UpsertVectorStore calls for bulk operations.
// Returns a terminal error when vectorStore is nil (composition bug).
func (s *Service) UpsertVectorStoreBulk(ctx context.Context, clipIDs []string) error {
	if s.vectorStore == nil {
		return fmt.Errorf("clipindexer: vector store not wired for bulk upsert (%d clips) — SetVectorStore was never called (composition error)", len(clipIDs))
	}
	if len(clipIDs) == 0 {
		return nil
	}
	if err := s.vectorStore.UpsertFromClips(ctx, clipIDs); err != nil {
		s.log.Warn("failed to batch upsert clips to vector store",
			zap.Int("count", len(clipIDs)),
			zap.Error(err))
		return err
	}
	s.log.Debug("vector store batch upserted clips",
		zap.Int("count", len(clipIDs)))
	return nil
}
