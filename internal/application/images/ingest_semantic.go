package images

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
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

// indexAssetInVectorStore is a no-op now owned by MetadataService.
// (metadata_service.go::MetadataService.indexAssetInVectorStore)
