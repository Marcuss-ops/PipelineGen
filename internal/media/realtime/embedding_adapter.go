package realtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// PythonEmbeddingAdapter calls the Python embedding server (/embed endpoint)
// to generate text embeddings for real-time matching.
type PythonEmbeddingAdapter struct {
	serverURL  string
	httpClient *http.Client
}

// NewPythonEmbeddingAdapter creates a new adapter pointing to the embedding server.
func NewPythonEmbeddingAdapter(serverURL string) *PythonEmbeddingAdapter {
	return &PythonEmbeddingAdapter{
		serverURL:  serverURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// EmbedText calls the Python embedding server's /embed endpoint with the given text.
func (a *PythonEmbeddingAdapter) EmbedText(ctx context.Context, text string) ([]float64, error) {
	body := map[string]string{"text": text}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", a.serverURL+"/embed", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("embedding server returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Embedding []float64 `json:"embedding"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return result.Embedding, nil
}
