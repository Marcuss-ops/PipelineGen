// Package reranker provides a standalone CrossEncoder reranking client.
// It calls the Python reranker_server.py to reorder Qdrant search results
// for all media types: clips, stock, artlist, images, voiceovers, AI video.
//
// Design principles:
//   - Standalone module (not coupled to realtime or Qdrant)
//   - Bounded failure: HTTP timeout prevents indefinite pipeline blocking
//   - Fail-closed: transport and contract errors are returned to the search layer
//   - Multi-media: candidate Text field handles any media type description
package reranker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/models"
	platformconfig "github.com/Marcuss-ops/PipelineGen/internal/platform/config"
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
	Enabled   bool    `yaml:"enabled" env:"VELOX_RERANKER_ENABLED" default:"false"`
	URL       string  `yaml:"url" env:"VELOX_RERANKER_URL" default:"http://127.0.0.1:8091/rerank"`
	Model     string  `yaml:"model" env:"VELOX_RERANKER_MODEL" default:"BAAI/bge-reranker-v2-m3"`
	TopK      int     `yaml:"top_k" env:"VELOX_RERANKER_TOP_K" default:"30"`
	TimeoutMs int     `yaml:"timeout_ms" env:"VELOX_RERANKER_TIMEOUT_MS" default:"150"`
	Weight    float64 `yaml:"weight" env:"VELOX_RERANKER_WEIGHT" default:"0.35"`
}

// WithDefaults returns a copy with sensible defaults applied.
func (c Config) WithDefaults() Config {
	if strings.TrimSpace(c.URL) == "" {
		c.URL = "http://127.0.0.1:8091/rerank"
	}
	if strings.TrimSpace(c.Model) == "" {
		c.Model = models.Reranker.ID
	}
	if c.TopK == 0 {
		c.TopK = 30
	}
	if c.TimeoutMs == 0 {
		c.TimeoutMs = 150
	}
	if c.Weight == 0 {
		c.Weight = 0.35
	}
	return c
}

// Validate enforces the same canonical contract as the top-level config
// loader. Keeping this check at the adapter boundary prevents manually
// assembled composition configs from bypassing validation.
func (c Config) Validate() error {
	return platformconfig.ValidateRerankerSettings(c.Enabled, c.URL, c.Model, c.TopK, c.TimeoutMs, c.Weight)
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

// NewClient creates a new reranker client. Call NewValidatedClient from the
// composition root when enabled configuration must fail closed at boot.
func NewClient(cfg Config) *Client {
	return newClient(cfg.WithDefaults())
}

// NewValidatedClient creates a client and rejects an invalid enabled
// configuration instead of silently producing a disabled client.
func NewValidatedClient(cfg Config) (*Client, error) {
	resolved := cfg.WithDefaults()
	if err := resolved.Validate(); err != nil {
		return nil, err
	}
	return newClient(resolved), nil
}

func newClient(resolved Config) *Client {
	return &Client{
		cfg:     resolved,
		http:    &http.Client{Timeout: resolved.Timeout()},
		enabled: resolved.Enabled && resolved.Model == models.Reranker.ID,
	}
}

// IsEnabled returns whether the reranker is available. A configured model
// outside the canonical registry is treated as unavailable rather than
// silently creating a second reranking contract.
func (c *Client) IsEnabled() bool {
	return c != nil && c.enabled && c.cfg.URL != ""
}

// Weight returns the configured blend weight used when mixing
// Qdrant and reranker scores.
func (c *Client) Weight() float64 {
	if c == nil {
		return 0
	}
	return c.cfg.Weight
}

// TopK returns the configured rerank candidate budget.
func (c *Client) TopK() int {
	if c == nil {
		return 0
	}
	return c.cfg.TopK
}

// Rerank sends candidates to the CrossEncoder and returns them reordered by relevance.
// Any transport or response-contract failure is returned to the caller.
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
	if err := validateResults(candidates, parsed.Results); err != nil {
		return nil, fmt.Errorf("invalid rerank response: %w", err)
	}

	return parsed.Results, nil
}

// validateResults ensures the sidecar returned a complete, one-to-one
// reranking for the requested candidate window. Partial or fabricated output
// is an error so the enabled search capability cannot return an unverified rank.
func validateResults(candidates []Candidate, results []Result) error {
	if len(results) != len(candidates) {
		return fmt.Errorf("result count %d does not match candidate count %d", len(results), len(candidates))
	}
	expected := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.ID) == "" {
			return fmt.Errorf("candidate has empty id")
		}
		if _, exists := expected[candidate.ID]; exists {
			return fmt.Errorf("duplicate candidate id %q", candidate.ID)
		}
		expected[candidate.ID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(results))
	for _, result := range results {
		if _, ok := expected[result.ID]; !ok {
			return fmt.Errorf("unknown result id %q", result.ID)
		}
		if _, duplicate := seen[result.ID]; duplicate {
			return fmt.Errorf("duplicate result id %q", result.ID)
		}
		if math.IsNaN(result.RerankScore) || math.IsInf(result.RerankScore, 0) {
			return fmt.Errorf("non-finite score for result %q", result.ID)
		}
		seen[result.ID] = struct{}{}
	}
	if len(seen) != len(expected) {
		return fmt.Errorf("response omitted %d candidate(s)", len(expected)-len(seen))
	}
	return nil
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
