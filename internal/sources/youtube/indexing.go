package youtube

import (
	"context"
	"time"

	concurrent "github.com/Marcuss-ops/PipelineGen/internal/infrastructure"

	"go.uber.org/zap"
)

// triggerAutoIndexing fires a background goroutine to:
//  1. First enrich the clip with YouTube metadata (title, description, tags, language)
//     if missing — this ensures search_text is available for embedding generation.
//     Uses the resilient enrichYouTubeClipWithMetadata which falls back to direct
//     yt-dlp fetch if the original metadata wasn't available during extraction.
//  2. Then generate embeddings and upsert to Qdrant vector store.
func (s *Service) triggerAutoIndexing(ctx context.Context, clipID string) {
	if s.indexer == nil || !s.indexer.IsEnabled() {
		return
	}

	concurrent.SafeGoFunc("youtube-auto-indexing", clipID, func(id string) {
		bgCtx := context.WithoutCancel(ctx)
		indexCtx, cancel := context.WithTimeout(bgCtx, 3*time.Minute)
		defer cancel()

		// Step 1: Enrich with YouTube metadata if missing (resilient — fetches via yt-dlp if needed)
		s.enrichYouTubeClipWithMetadata(indexCtx, id, nil, false)

		// Step 2: Generate embeddings and upsert to Qdrant
		s.log.Info("triggering automatic indexing for YouTube clip", zap.String("clip_id", id))
		if err := s.indexer.IndexClip(indexCtx, id); err != nil {
			s.log.Error("failed to automatically index YouTube clip", zap.String("clip_id", id), zap.Error(err))
		}
	})
}
