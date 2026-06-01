// Package reranker provides a standalone CrossEncoder reranking client.
// It calls the Python reranker_server.py to reorder Qdrant search results
// for all media types: clips, stock, artlist, images, voiceovers, AI video.
//
// Design principles:
//   - Standalone module (not coupled to realtime or Qdrant)
//   - Circuit breaker: HTTP timeout prevents pipeline blocking
//   - Graceful degradation: returns original order on failure
//   - Multi-media: candidate Text field handles any media type description
package reranker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Candidate represents a single search result to be reranked.
// Text should be a rich description: Title, Description, Tags, SceneType, Mood, Language.
type Candidate struct {
	ID          string   `json:"id"`
	Text        string   `json:"text"`
	QdrantScore *float64 `json:"qdrant_score,omitempty"`
}

// Result represents a reranked search result.
type Result struct {
	ID          string   `json:"id"`
	RerankScore float64  `json:"rerank_score"`
	QdrantScore *float64 `json:"qdrant_score,omitempty"`
}

// Request is the payload sent to the Python reranker server.
type Request struct {
	Query      string      `json:"query"`
	Candidates []Candidate `json:"candidates"`
}

// Response is the payload received from the Python reranker server.
type Response struct {
	Results []Result `json:"results"`
}

// Config holds the reranker client configuration.
type Config struct {
	Enabled   bool          `yaml:"enabled" default:"false"`
	URL       string        `yaml:"url" default:"http://127.0.0.1:8091/rerank"`
	Model     string        `yaml:"model" default:"BAAI/bge-reranker-v2-m3"`
	TopK      int           `yaml:"top_k" default:"30"`
	TimeoutMs int           `yaml:"timeout_ms" default:"150"`
	Weight    float64       `yaml:"weight" default:"0.35"`
}

// WithDefaults returns a copy with sensible defaults applied.
func (c Config) WithDefaults() Config {
	if c.TopK <= 0 {
		c.TopK = 30
	}
	if c.TimeoutMs <= 0 {
		c.TimeoutMs = 150
	}
	if c.Weight <= 0 || c.Weight > 1 {
		c.Weight = 0.35
	}
	return c
}

// Timeout returns the configured timeout as a time.Duration.
func (c Config) Timeout() time.Duration {
	return time.Duration(c.TimeoutMs) * time.Millisecond
}

// Client is a standalone CrossEncoder reranker client.
// It calls the Python reranker_server.py via HTTP.
type Client struct {
	cfg     Config
	http    *http.Client
	enabled bool
}

// NewClient creates a new reranker client.
func NewClient(cfg Config) *Client {
	return &Client{
		cfg:     cfg.WithDefaults(),
		http:    &http.Client{Timeout: cfg.WithDefaults().Timeout()},
		enabled: cfg.Enabled,
	}
}

// IsEnabled returns whether the reranker is available.
func (c *Client) IsEnabled() bool {
	return c != nil && c.enabled && c.cfg.URL != ""
}

// Rerank sends candidates to the CrossEncoder and returns them reordered by relevance.
// Returns nil, error on failure — caller should fall back to original Qdrant ordering.
func (c *Client) Rerank(ctx context.Context, query string, candidates []Candidate) ([]Result, error) {
	if !c.IsEnabled() {
		return nil, fmt.Errorf("reranker disabled")
	}
	if len(candidates) == 0 {
		return []Result{}, nil
	}

	// Limit to TopK
	if len(candidates) > c.cfg.TopK {
		candidates = candidates[:c.cfg.TopK]
	}

	body, err := json.Marshal(Request{
		Query:      query,
		Candidates: candidates,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal rerank request: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, c.cfg.Timeout())
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create rerank request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reranker request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("reranker returned %d", resp.StatusCode)
	}

	var parsed Response
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode rerank response: %w", err)
	}

	return parsed.Results, nil
}

// BuildCandidateText creates a rich description string for the reranker.
// The more fields provided, the better the CrossEncoder can understand the match.
// Works for any media type: clips, stock, artlist, images, voiceovers, AI video.
func BuildCandidateText(title, description string, tags []string, style, sceneType, language string) string {
	parts := make([]string, 0, 6)
	if title != "" {
		parts = append(parts, "Title: "+title)
	}
	if description != "" {
		parts = append(parts, "Description: "+description)
	}
	if len(tags) > 0 {
		tagsStr := ""
		for i, t := range tags {
			if i > 0 {
				tagsStr += ", "
			}
			tagsStr += t
		}
		parts = append(parts, "Tags: "+tagsStr)
	}
	if style != "" {
		parts = append(parts, "Style: "+style)
	}
	if sceneType != "" {
		parts = append(parts, "Scene: "+sceneType)
	}
	if language != "" {
		parts = append(parts, "Language: "+language)
	}
	// Join with newlines for rich multi-field representation
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += "\n"
		}
		result += p
	}
	return result
}
