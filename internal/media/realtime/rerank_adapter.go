package realtime

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

// RerankCandidate is a single candidate to be reranked by the CrossEncoder.
type RerankCandidate struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// RerankResult is a single reranked result from the CrossEncoder.
type RerankResult struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
}

// RerankResponse wraps the reranking server response.
type RerankResponse struct {
	Results []RerankResult `json:"results"`
}

// RerankAdapter calls the Python embedding server's /rerank endpoint
// to reorder candidates using a CrossEncoder model.
// Implements a 50ms circuit breaker: if the server doesn't respond in time,
// it returns nil (caller falls back to original Qdrant ordering).
type RerankAdapter struct {
	serverURL string
	client    *http.Client
	log       *zap.Logger
}

// NewRerankAdapter creates a new adapter pointing to the embedding server.
func NewRerankAdapter(serverURL string, log *zap.Logger) *RerankAdapter {
	return &RerankAdapter{
		serverURL: serverURL,
		client: &http.Client{
			Timeout: 50 * time.Millisecond, // circuit breaker: 50ms max
		},
		log: log,
	}
}

// Rerank sends candidates to the CrossEncoder and returns them reordered by relevance.
// Returns nil, nil if the reranker is unavailable or times out — the caller should
// fall back to the original Qdrant ordering.
func (r *RerankAdapter) Rerank(ctx context.Context, query string, candidates []RerankCandidate) ([]RerankResult, error) {
	if r == nil || r.serverURL == "" {
		return nil, nil
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	payload := struct {
		Query      string           `json:"query"`
		Candidates []RerankCandidate `json:"candidates"`
	}{
		Query:      query,
		Candidates: candidates,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal rerank request: %w", err)
	}

	url := r.serverURL + "/rerank"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create rerank request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		// Timeout or connection refused — graceful degradation
		r.log.Debug("reranker unavailable, falling back to Qdrant order",
			zap.String("query", query),
			zap.Int("candidates", len(candidates)),
			zap.Error(err),
		)
		return nil, nil
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read rerank response: %w", err)
	}

	if resp.StatusCode != 200 {
		r.log.Debug("reranker returned non-200 status",
			zap.Int("status", resp.StatusCode),
			zap.String("body", string(respBody)),
		)
		return nil, nil
	}

	var result RerankResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse rerank response: %w", err)
	}

	return result.Results, nil
}
