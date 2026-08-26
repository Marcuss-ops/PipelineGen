package config

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/models"
)

// RerankerConfig holds settings for the CrossEncoder reranking service.
// The reranker is a post-Qdrant precision layer that improves semantic
// relevance for all media types (clips, stock, artlist, images, voiceover).
//
// Enabled defaults to true (canonical recipe: Qdrant top_k window → BGE
// re-score → top 5 results). Operators disable it explicitly with
// VELOX_RERANKER_ENABLED=false; while disabled the Qdrant ranking is
// returned untouched and no sidecar is required.
type RerankerConfig struct {
	Enabled   bool    `yaml:"enabled" env:"VELOX_RERANKER_ENABLED" default:"true"`
	URL       string  `yaml:"url" env:"VELOX_RERANKER_URL" default:"http://127.0.0.1:8091/rerank"`
	Model     string  `yaml:"model" env:"VELOX_RERANKER_MODEL"`
	TopK      int     `yaml:"top_k" env:"VELOX_RERANKER_TOP_K" default:"30"`
	TimeoutMs int     `yaml:"timeout_ms" env:"VELOX_RERANKER_TIMEOUT_MS" default:"150"`
	Weight    float64 `yaml:"weight" env:"VELOX_RERANKER_WEIGHT" default:"0.35"`
}

// Validate checks the enabled reranker contract. Disabled reranking remains
// valid without a running sidecar; enabled reranking must use the canonical
// registry model and bounded transport/scoring settings.
func (c RerankerConfig) Validate() error {
	return ValidateRerankerSettings(c.Enabled, c.URL, c.Model, c.TopK, c.TimeoutMs, c.Weight)
}

// ValidateRerankerSettings is shared by configuration and the HTTP adapter so
// both composition paths enforce exactly the same canonical contract.
func ValidateRerankerSettings(enabled bool, rawURL, model string, topK, timeoutMs int, weight float64) error {
	if !enabled {
		return nil
	}
	if strings.TrimSpace(model) != models.Reranker.ID {
		return fmt.Errorf("model %q is not the canonical reranker %q", model, models.Reranker.ID)
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("url %q is invalid", rawURL)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("url scheme %q is unsupported; use http or https", parsed.Scheme)
	}
	if topK <= 0 || topK > 1000 {
		return fmt.Errorf("top_k %d is outside the allowed range 1..1000", topK)
	}
	if timeoutMs <= 0 || timeoutMs > 60000 {
		return fmt.Errorf("timeout_ms %d is outside the allowed range 1..60000", timeoutMs)
	}
	if weight < 0 || weight > 1 {
		return fmt.Errorf("weight %.4f is outside the allowed range 0..1", weight)
	}
	return nil
}
