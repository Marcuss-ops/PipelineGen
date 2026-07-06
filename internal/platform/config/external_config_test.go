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

// TestConfigUnmarshalReadsArtlistCookiesPath verifies the canonical
// external_config_test for cfg.External.ArtlistCookiesPath (added
// 2026-07-06 in PR-ARTLIST-COOKIES-CONFIG).
//
// Contract (godlike/07 fail-closed empty default):
//  1. When yaml does NOT set the field, applyDefaults populates it
//     with the canonical empty default (NOT a hardcoded `/tmp/...` path).
//  2. When yaml sets the field, the value is bound verbatim to
//     cfg.External.ArtlistCookiesPath.
//
// The downloader (internal/infrastructure/downloader/downloader.go) reads
// the field via NewYTDLP; when empty it SKIPS the --cookies flag entirely
// so operators see a visible 403 from Artlist instead of a silent failure
// on a non-existent cookies file.
func TestConfigUnmarshalReadsArtlistCookiesPath(t *testing.T) {
	// Case 1: empty default (godlike/07 fail-closed).
	emptyCfg := &Config{}
	applyDefaults(emptyCfg)
	if got := emptyCfg.External.ArtlistCookiesPath; got != "" {
		t.Fatalf("expected empty default for ArtlistCookiesPath, got %q", got)
	}

	// Case 2: custom value via yaml binding.
	raw := []byte(`external:
  artlist_cookies_path: "/var/lib/pipelinegen/artlist_cookies.txt"
`)
	customCfg := &Config{}
	applyDefaults(customCfg)
	if err := yaml.Unmarshal(raw, customCfg); err != nil {
		t.Fatalf("yaml unmarshal failed: %v", err)
	}
	if got, want := customCfg.External.ArtlistCookiesPath, "/var/lib/pipelinegen/artlist_cookies.txt"; got != want {
		t.Fatalf("expected ArtlistCookiesPath %q, got %q", want, got)
	}
}
