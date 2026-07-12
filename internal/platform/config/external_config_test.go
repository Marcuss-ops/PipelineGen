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

// ---------- PR-ARTLIST-CONFIG-PREFIX (July 2026) ----------
//
// TestConfigLoaderReadsVeloxArtlistScraperServerURLFromEnv pins the
// loader binding for cfg.External.ArtlistScraperServerURL →
// env VELOX_ARTLIST_SCRAPER_SERVER_URL. Before this PR the loader
// read the bare ARTLIST_SCRAPER_SERVER_URL (which docker-compose.yml
// never set: only the VELOX_-prefixed form was exported). Result:
// fail-closed gate #5 in internal/app/build_bundles_artlist.go fired
// in production even though the env value was present via YAML.
// This test makes the binding contract explicit so any future
// re-prefix or env rename surfaces here as a test failure.
//
// Cases:
//  1. Empty environment (unset): applyDefaults populates the field
//     with the canonical empty default (fail-closed; when the env is
//     not set, the YAML also not set, the loader falls through to
//     default:"" and gate #5 fires if the feature is enabled).
//  2. Env VELOX_ARTLIST_SCRAPER_SERVER_URL set via t.Setenv: the
//     loader MUST bind it verbatim into the field. (Use t.Setenv so
//     the test is hermetic — no cross-test pollution.)
//  3. YAML key `artlist_scraper_server_url` set: the YAML path still
//     works because the yaml:"" struct tag was intentionally left
//     unchanged during the env-prefix cutover (PR-ARTLIST-CONFIG-PREFIX
//     YAML key continuity).
func TestConfigLoaderReadsVeloxArtlistScraperServerURLFromEnv(t *testing.T) {
	// Case 1: empty default (godlike/07 fail-closed).
	t.Run("EmptyEnvYieldsEmptyDefault", func(t *testing.T) {
		cfg := &Config{}
		applyDefaults(cfg)
		if got := cfg.External.ArtlistScraperServerURL; got != "" {
			t.Fatalf("expected empty default for ArtlistScraperServerURL, got %q", got)
		}
	})

	// Case 2: env VELOX_ARTLIST_SCRAPER_SERVER_URL binds verbatim.
	// The applyEnvVars path (config.go::Load) reads each struct field's
	// `env:` tag and binds the corresponding process env via
	// reflect-driven lookup. Reproduce the same lookup shape here so
	// the test pins the binding without depending on the full Load
	// chain (which would require a file-backed YAML — overkill for a
	// single env-binding assertion).
	t.Run("VeloxEnvBindsVerbatim", func(t *testing.T) {
		const want = "http://artlist-scraper:9123"
		t.Setenv("VELOX_ARTLIST_SCRAPER_SERVER_URL", want)
		// Simulate applyEnvVars reflection: lookup each field via the
		// env:"..." struct tag in types_external.go. We exercise the
		// canonical helper by running it against a fresh Config{} so
		// the loader's own bookkeeping is responsible for the binding.
		cfg := &Config{}
		applyDefaults(cfg)
		applyEnvVars(cfg)
		if got := cfg.External.ArtlistScraperServerURL; got != want {
			t.Fatalf("env VELOX_ARTLIST_SCRAPER_SERVER_URL did not bind to ArtlistScraperServerURL: want %q, got %q", want, got)
		}
	})

	// Case 3: YAML key `artlist_scraper_server_url` still works
	// (PR-ARTLIST-CONFIG-PREFIX yaml:"" tag was intentionally preserved).
	t.Run("YamlKeyStillBinds", func(t *testing.T) {
		// Unset env so the binding comes purely from YAML for this test.
		t.Setenv("VELOX_ARTLIST_SCRAPER_SERVER_URL", "")
		raw := []byte(`external:
  artlist_scraper_server_url: "http://127.0.0.1:9123"
`)
		cfg := &Config{}
		applyDefaults(cfg)
		applyEnvVars(cfg)
		if err := yaml.Unmarshal(raw, cfg); err != nil {
			t.Fatalf("yaml unmarshal failed: %v", err)
		}
		if got, want := cfg.External.ArtlistScraperServerURL, "http://127.0.0.1:9123"; got != want {
			t.Fatalf("yaml key artlist_scraper_server_url did not bind: want %q, got %q", want, got)
		}
	})
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
