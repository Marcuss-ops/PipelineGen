package images

import (
	"context"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/media/realtime"
	"velox/go-master/internal/media/vectorstore"
)

// callSemanticTagger RIMOSSO: usa semantic.Tagger() o semantic.MetadataWriter.Write() direttamente.
// callSemanticTagger era un duplicato identico di semantic.Tagger() — contribuiva alla
// frammentazione dei metadati. Ogni media type ora usa lo stesso semantic.Tagger() centralizzato.

func (s *Service) callLLMFallback(ctx context.Context, mediaType, prompt, style string) string {
	if s.llmGen == nil {
		return prompt
	}
	desc, err := s.llmGen.GenerateDescription(ctx, mediaType, prompt, style)
	if err != nil {
		s.log.Warn("LLM fallback failed", zap.Error(err))
		return prompt
	}
	return desc
}

func (s *Service) indexAssetInVectorStore(ctx context.Context, assetID, source, name, localPath, driveLink, style, mediaType, searchText string, tags []string) {
	if s.vectorSvc == nil {
		return
	}

	// 1. Get passage embedding from Python server (type="passage" per E5 prefix)
	adapter := realtime.NewPythonEmbeddingAdapter(s.cfg.ClipIndexer.ServerURL)
	embedding, err := adapter.EmbedPassage(ctx, searchText)
	if err != nil {
		s.log.Warn("Failed to generate embedding for search_text", zap.String("asset_id", assetID), zap.Error(err))
		return
	}

	// Convert to float32
	vec := make([]float32, len(embedding))
	for i, f := range embedding {
		vec[i] = float32(f)
	}

	// 2. Upsert to Qdrant
	vAsset := vectorstore.VectorAsset{
		AssetID:       assetID,
		Source:        source,
		Name:          name,
		LocalPath:     localPath,
		DriveLink:     driveLink,
		Style:         style,
		MediaType:     mediaType,
		TextEmbedding: vec,
		Tags:          tags,
		CreatedAt:     time.Now(),
	}

	if err := s.vectorSvc.UpsertAsset(ctx, vAsset); err != nil {
		s.log.Warn("Failed to upsert to vector store", zap.String("asset_id", assetID), zap.Error(err))
		return
	}

	// 3. Update DB status if it's an image
	if mediaType == "image" {
		_ = s.repo.UpdateEmbeddingStatus(ctx, assetID, "ready")
	}

	s.log.Info("Asset indexed in vector store", zap.String("asset_id", assetID), zap.String("media_type", mediaType))
}
