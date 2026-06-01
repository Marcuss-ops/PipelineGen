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

// EmbedText calls the Python embedding server's /embed endpoint with type="query".
// Used for real-time matching (search queries).
func (a *PythonEmbeddingAdapter) EmbedText(ctx context.Context, text string) ([]float64, error) {
	return a.callEmbedder(ctx, "/embed", text, "query")
}

// EmbedPassage calls the Python embedding server's /embed endpoint with type="passage".
// Used for document indexing into Qdrant. E5 requires separate prefixes for query vs passage.
func (a *PythonEmbeddingAdapter) EmbedPassage(ctx context.Context, text string) ([]float64, error) {
	return a.callEmbedder(ctx, "/embed", text, "passage")
}

// EmbedVisual calls the Python embedding server's /embed_visual endpoint.
func (a *PythonEmbeddingAdapter) EmbedVisual(ctx context.Context, text string) ([]float64, error) {
	return a.callEmbedder(ctx, "/embed_visual", text)
}

// EmbedAudio calls the Python embedding server's /embed_audio endpoint.
func (a *PythonEmbeddingAdapter) EmbedAudio(ctx context.Context, text string) ([]float64, error) {
	return a.callEmbedder(ctx, "/embed_audio", text)
}

// VisualAnalysisResult contains the image fingerprint returned by the embedding server.
type VisualAnalysisResult struct {
	Embedding  []float64 `json:"embedding"`
	PHash      string    `json:"phash"`
	Dimensions int       `json:"dimensions"`
	Width      int       `json:"width"`
	Height     int       `json:"height"`
}

func (a *PythonEmbeddingAdapter) AnalyzeImage(ctx context.Context, imagePath string) (*VisualAnalysisResult, error) {
	body := map[string]string{"image_path": imagePath}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", a.serverURL+"/visual_analyze", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("visual analyze request: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("visual analyze returned %d: %s", resp.StatusCode, string(respBody))
	}
	var result VisualAnalysisResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func (a *PythonEmbeddingAdapter) callEmbedder(ctx context.Context, endpoint, text string, embedType ...string) ([]float64, error) {
	embedTypeVal := "query"
	if len(embedType) > 0 {
		embedTypeVal = embedType[0]
	}
	body := map[string]string{"text": text, "type": embedTypeVal}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", a.serverURL+endpoint, bytes.NewReader(data))
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
