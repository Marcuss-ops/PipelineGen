package clipindexer

import (
	"context"

	"go.uber.org/zap"
)

// SetVectorStore sets the vector store indexer for Qdrant upsert after indexing.
func (s *Service) SetVectorStore(vs VectorStoreIndexer) {
	s.vectorStore = vs
}

// UpsertVectorStore pushes the newly indexed clip to Qdrant if a vector store is configured.
func (s *Service) UpsertVectorStore(ctx context.Context, clipID string) error {
	if s.vectorStore == nil {
		s.log.Warn("vector store not configured, skipping Qdrant upsert",
			zap.String("clip_id", clipID))
		return nil
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
func (s *Service) UpsertVectorStoreBulk(ctx context.Context, clipIDs []string) error {
	if s.vectorStore == nil || len(clipIDs) == 0 {
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
