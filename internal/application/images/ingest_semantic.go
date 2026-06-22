package images

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/\1"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"go.uber.org/zap"
)

// callSemanticTagger RIMOSSO: usa semantic.Tagger() o semantic.MetadataWriter.Write() direttamente.
// callSemanticTagger era un duplicato identico di semantic.Tagger() — contribuiva alla
// frammentazione dei metadati. Ogni media type ora usa lo stesso semantic.Tagger() centralizzato.

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

	// 2. PERSIST embedding in DB (so it survives Qdrant wipes)
	if s.repo != nil && s.repo.DB() != nil {
		embJSON, _ := json.Marshal(embedding)
		_ = s.repo.UpdateEmbeddingData(ctx, assetID, string(embJSON), "ready")
	}

	// 3. Upsert to Qdrant
	vAsset := qdrant.VectorAsset{
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
		// DB already has the embedding, Qdrant can be retried later
		return
	}

	// 4. Update DB status to reflect Qdrant success
	if s.repo != nil && s.repo.DB() != nil {
		_ = s.repo.UpdateEmbeddingData(ctx, assetID, "", "completed")
	}

	s.log.Info("Asset indexed in vector store",
		zap.String("asset_id", assetID),
		zap.String("media_type", mediaType),
		zap.Int("embedding_dim", len(vec)),
	)
}
