package clipresolver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// PythonEmbeddingProvider implements EmbeddingProvider by calling the Python embedding server.
type PythonEmbeddingProvider struct {
	serverURL string
}

// NewPythonEmbeddingProvider creates a new provider that uses the Python embedding server.
func NewPythonEmbeddingProvider(serverURL string) *PythonEmbeddingProvider {
	return &PythonEmbeddingProvider{
		serverURL: serverURL,
	}
}

// EmbedText returns the embedding for a given text.
func (p *PythonEmbeddingProvider) EmbedText(ctx context.Context, text string) ([]float64, error) {
	if p.serverURL == "" {
		return nil, fmt.Errorf("embedding server URL not configured")
	}

	payload := map[string]string{
		"text": text,
	}
	body, _ := json.Marshal(payload)

	url := fmt.Sprintf("%s/embed", strings.TrimSuffix(p.serverURL, "/"))
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding server returned status %d", resp.StatusCode)
	}

	var result struct {
		Embedding []float64 `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Embedding, nil
}

