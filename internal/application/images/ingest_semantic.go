package images

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"go.uber.org/zap"
)

// pythonEmbeddingAdapter is a local replacement for the removed
// realtime.NewPythonEmbeddingAdapter. It calls the Python embedding
// server directly via HTTP.
type pythonEmbeddingAdapter struct {
	serverURL string
	client    *http.Client
}

func newPythonEmbeddingAdapter(serverURL string) *pythonEmbeddingAdapter {
	return &pythonEmbeddingAdapter{
		serverURL: serverURL,
		client:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (a *pythonEmbeddingAdapter) EmbedPassage(ctx context.Context, text string) ([]float64, error) {
	if a.serverURL == "" {
		return nil, fmt.Errorf("embedding server URL not configured")
	}
	reqBody := map[string]string{"text": text, "type": "passage"}
	body, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, "POST", a.serverURL+"/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding server returned %d", resp.StatusCode)
	}
	var result struct {
		Embedding []float64 `json:"embedding"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return nil, err
	}
	return result.Embedding, nil
}

// callSemanticTagger RIMOSSO: usa semantic.Tagger() o semantic.MetadataWriter.Write() direttamente.
// callSemanticTagger era un duplicato identico di semantic.Tagger() — contribuiva alla
// frammentazione dei metadati. Ogni media type ora usa lo stesso semantic.Tagger() centralizzato.

func (s *Service) indexAssetInVectorStore(ctx context.Context, assetID, source, name, localPath, driveLink, style, mediaType, searchText string, tags []string) {
	if s.vectorSvc == nil {
		return
	}

	// 1. Get passage embedding from Python server (type="passage" per E5 prefix)
	adapter := newPythonEmbeddingAdapter(s.cfg.ClipIndexer.ServerURL)
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
