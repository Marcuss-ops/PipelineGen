package images

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

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

// indexAssetInVectorStore persists the clip/image embedding into a
// vector store after a clip/image has been semantically enriched.
//
// PG-034 (June 2026): Qdrant removed. The function is preserved as a
// no-op so callers in ingest_direct.go (et al.) compile unchanged.
// The DB-side embedding JSON is still persisted via
// s.repo.UpdateEmbeddingData in the earlier semantic-enrichment leg
// (not in this no-op), so embeddings survive in SQLite canonically.
func (s *Service) indexAssetInVectorStore(ctx context.Context, assetID, source, name, localPath, driveLink, style, mediaType, searchText string, tags []string) {
	if s == nil {
		return
	}
	if s.log != nil {
		s.log.Debug("indexAssetInVectorStore noop (Qdrant removed PG-034)",
			zap.String("asset_id", assetID),
			zap.String("media_type", mediaType))
	}
	_ = source
	_ = name
	_ = localPath
	_ = driveLink
	_ = style
	_ = searchText
	_ = tags
}
