package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestConfigUnmarshalReadsFallbackProviderKeysAndStockPipelineFlag(t *testing.T) {
	raw := []byte(`
external:
  pixabay_api_key: "pixabay-123"
  pixabay_base_url: "https://example.test/pixabay"
  pexels_api_key: "pexels-456"
  pexels_base_url: "https://example.test/pexels"
features:
  stock_pipeline_enabled: true
`)

	cfg := &Config{}
	applyDefaults(cfg)

	if err := yaml.Unmarshal(raw, cfg); err != nil {
		t.Fatalf("yaml unmarshal failed: %v", err)
	}

	if cfg.External.PixabayAPIKey != "pixabay-123" {
		t.Fatalf("unexpected pixabay key: %q", cfg.External.PixabayAPIKey)
	}
	if cfg.External.PixabayBaseURL != "https://example.test/pixabay" {
		t.Fatalf("unexpected pixabay base url: %q", cfg.External.PixabayBaseURL)
	}
	if cfg.External.PexelsAPIKey != "pexels-456" {
		t.Fatalf("unexpected pexels key: %q", cfg.External.PexelsAPIKey)
	}
	if cfg.External.PexelsBaseURL != "https://example.test/pexels" {
		t.Fatalf("unexpected pexels base url: %q", cfg.External.PexelsBaseURL)
	}
	if !cfg.Features.StockPipelineEnabled {
		t.Fatal("expected stock_pipeline_enabled to be true")
	}
}
