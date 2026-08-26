package config

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/models"
)

func validRerankerConfig() RerankerConfig {
	return RerankerConfig{
		Enabled:   true,
		URL:       "http://127.0.0.1:8091/rerank",
		Model:     models.Reranker.ID,
		TopK:      30,
		TimeoutMs: 150,
		Weight:    0.35,
	}
}

func TestRerankerConfigValidateAcceptsCanonicalSettings(t *testing.T) {
	if err := validRerankerConfig().Validate(); err != nil {
		t.Fatalf("canonical reranker config rejected: %v", err)
	}
}

func TestRerankerConfigValidateRejectsNonCanonicalModel(t *testing.T) {
	cfg := validRerankerConfig()
	cfg.Model = "other/reranker"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "canonical") {
		t.Fatalf("expected canonical-model validation error, got %v", err)
	}
}

func TestRerankerConfigValidateRejectsInvalidBounds(t *testing.T) {
	tests := []struct {
		name string
		edit func(*RerankerConfig)
	}{
		{"empty url", func(c *RerankerConfig) { c.URL = "" }},
		{"zero top_k", func(c *RerankerConfig) { c.TopK = 0 }},
		{"negative timeout", func(c *RerankerConfig) { c.TimeoutMs = -1 }},
		{"weight above one", func(c *RerankerConfig) { c.Weight = 1.1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validRerankerConfig()
			tt.edit(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestRerankerConfigValidateAllowsDisabledSidecar(t *testing.T) {
	if err := (RerankerConfig{}).Validate(); err != nil {
		t.Fatalf("disabled reranker should not require a sidecar: %v", err)
	}
}
