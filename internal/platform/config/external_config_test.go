package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

func TestExternalConfigDefaultsTextToGemma2B(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	assert.Equal(t, "gemma4:e2b", cfg.External.OllamaModel,
		"text generation must default to the latency-first 2B model; 4B stays opt-in per item")
}

func TestExternalConfigRustVisualNERPathBindings(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	assert.Equal(t, "bin/visualner", cfg.External.RustVisualNERPath)

	t.Setenv("VELOX_RUST_VISUALNER_PATH", "/opt/pipelinegen/visualner")
	applyEnvVars(cfg)
	assert.Equal(t, "/opt/pipelinegen/visualner", cfg.External.RustVisualNERPath)

	if err := yaml.Unmarshal([]byte("external:\n  rust_visualner_path: /opt/brainrust/visualner\n"), cfg); err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, "/opt/brainrust/visualner", cfg.External.RustVisualNERPath)
}

// TestExternalConfigRenderingGenStoreURLBindings pins the single read of
// RENDERINGGEN_STORE_URL: the field carries the local store default, the YAML
// key binds it, and the environment overrides both. The composition root then
// consumes the parsed field instead of calling os.Getenv itself.
func TestExternalConfigRenderingGenStoreURLBindings(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	assert.Equal(t, "http://127.0.0.1:9000", cfg.External.RenderingGenStoreURL)

	if err := yaml.Unmarshal([]byte("external:\n  renderinggen_store_url: https://store.example\n"), cfg); err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, "https://store.example", cfg.External.RenderingGenStoreURL)

	t.Setenv("RENDERINGGEN_STORE_URL", "https://store-env.example")
	applyEnvVars(cfg)
	assert.Equal(t, "https://store-env.example", cfg.External.RenderingGenStoreURL)
}

func TestExternalConfigLocationOverlayBindings(t *testing.T) {
	t.Run("defaultsKeepGeocodingAndMapsDisabled", func(t *testing.T) {
		cfg := &Config{}
		applyDefaults(cfg)
		assert.Empty(t, cfg.External.GeocodingBaseURL)
		assert.Empty(t, cfg.External.GeocodingCacheDir)
		assert.Empty(t, cfg.External.MapPlateManifestPath)
		assert.Empty(t, cfg.External.GeocodingUserAgent)
	})

	t.Run("yamlBindsLocationSources", func(t *testing.T) {
		cfg := &Config{}
		applyDefaults(cfg)
		raw := []byte(`external:
  geocoding_base_url: "https://geo.example/search"
  geocoding_user_agent: "PipelineGen test (https://example.org/contact)"
  geocoding_cache_dir: "cache/geocoding"
  map_plate_manifest_path: "maps/plates.json"
`)
		if err := yaml.Unmarshal(raw, cfg); err != nil {
			t.Fatalf("yaml unmarshal failed: %v", err)
		}
		assert.Equal(t, "https://geo.example/search", cfg.External.GeocodingBaseURL)
		assert.Equal(t, "PipelineGen test (https://example.org/contact)", cfg.External.GeocodingUserAgent)
		assert.Equal(t, "cache/geocoding", cfg.External.GeocodingCacheDir)
		assert.Equal(t, "maps/plates.json", cfg.External.MapPlateManifestPath)
	})

	t.Run("environmentBindsLocationSources", func(t *testing.T) {
		t.Setenv("VELOX_GEOCODING_BASE_URL", "https://geo-env.example/search")
		t.Setenv("VELOX_GEOCODING_USER_AGENT", "PipelineGen env test")
		t.Setenv("VELOX_GEOCODING_CACHE_DIR", "/var/cache/pipelinegen/geo")
		t.Setenv("VELOX_MAP_PLATE_MANIFEST_PATH", "/etc/pipelinegen/plates.json")
		cfg := &Config{}
		applyDefaults(cfg)
		applyEnvVars(cfg)
		assert.Equal(t, "https://geo-env.example/search", cfg.External.GeocodingBaseURL)
		assert.Equal(t, "PipelineGen env test", cfg.External.GeocodingUserAgent)
		assert.Equal(t, "/var/cache/pipelinegen/geo", cfg.External.GeocodingCacheDir)
		assert.Equal(t, "/etc/pipelinegen/plates.json", cfg.External.MapPlateManifestPath)
	})
}

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

// TestExternalConfigResolveYouTubeCookiesPath pins the canonical cookie
// resolver without reading any cookie file. VELOX_YOUTUBE_COOKIES_FILE is
// bound by the struct tag during config loading; YT_COOKIES_PATH is only the
// compatibility bridge when the canonical field is empty.
func TestExternalConfigResolveYouTubeCookiesPath(t *testing.T) {
	t.Run("canonicalEnvWinsOverConfigAndLegacy", func(t *testing.T) {
		t.Setenv("VELOX_YOUTUBE_COOKIES_FILE", "/env/youtube.cookies.txt")
		t.Setenv("YT_COOKIES_PATH", "/legacy/youtube.cookies.txt")
		cfg := &Config{External: ExternalConfig{YouTubeCookiesPath: "/yaml/youtube.cookies.txt"}}
		assert.Equal(t, "/env/youtube.cookies.txt", cfg.External.ResolveYouTubeCookiesPath())
	})

	t.Run("configWinsOverLegacy", func(t *testing.T) {
		t.Setenv("VELOX_YOUTUBE_COOKIES_FILE", "")
		t.Setenv("YT_COOKIES_PATH", "/legacy/youtube.cookies.txt")
		cfg := &Config{External: ExternalConfig{YouTubeCookiesPath: "/yaml/youtube.cookies.txt"}}
		assert.Equal(t, "/yaml/youtube.cookies.txt", cfg.External.ResolveYouTubeCookiesPath())
	})
	t.Run("legacyBridge", func(t *testing.T) {
		t.Setenv("YT_COOKIES_PATH", "/legacy/youtube.cookies.txt")
		cfg := &Config{}
		assert.Equal(t, "/legacy/youtube.cookies.txt", cfg.External.ResolveYouTubeCookiesPath())
	})
	t.Run("unsetIsEmpty", func(t *testing.T) {
		t.Setenv("YT_COOKIES_PATH", "")
		cfg := &Config{}
		assert.Empty(t, cfg.External.ResolveYouTubeCookiesPath())
	})
}

func TestExternalConfigYoutubeSleepBinding(t *testing.T) {
	t.Run("defaultsEnableProductionPacing", func(t *testing.T) {
		cfg := &Config{}
		applyDefaults(cfg)
		assert.Equal(t, 2, cfg.External.YoutubeMinSleepSeconds)
		assert.Equal(t, 5, cfg.External.YoutubeMaxSleepSeconds)
		assert.Equal(t, 2, func() int { min, _ := cfg.External.ResolvedYouTubeSleepSeconds(); return min }())
	})

	t.Run("yamlBindsAndClampsRange", func(t *testing.T) {
		cfg := &Config{}
		applyDefaults(cfg)
		raw := []byte(`external:
  youtube_min_sleep_seconds: 3
  youtube_max_sleep_seconds: 8
`)
		if err := yaml.Unmarshal(raw, cfg); err != nil {
			t.Fatalf("yaml unmarshal failed: %v", err)
		}
		min, max := cfg.External.ResolvedYouTubeSleepSeconds()
		assert.Equal(t, 3, min)
		assert.Equal(t, 8, max)

		cfg.External.YoutubeMaxSleepSeconds = 1
		min, max = cfg.External.ResolvedYouTubeSleepSeconds()
		assert.Equal(t, 3, min)
		assert.Equal(t, 3, max)
	})

	t.Run("envBindsValues", func(t *testing.T) {
		t.Setenv("YTDLP_MIN_SLEEP_SECONDS", "4")
		t.Setenv("YTDLP_MAX_SLEEP_SECONDS", "9")
		cfg := &Config{}
		applyDefaults(cfg)
		applyEnvVars(cfg)
		assert.Equal(t, 4, cfg.External.YoutubeMinSleepSeconds)
		assert.Equal(t, 9, cfg.External.YoutubeMaxSleepSeconds)
	})
}

func TestExternalConfigYoutubePlayerClientFallbackBinding(t *testing.T) {
	t.Run("defaultIsEmpty", func(t *testing.T) {
		cfg := &Config{}
		applyDefaults(cfg)
		assert.Empty(t, cfg.External.YoutubePlayerClientFallback)
	})

	t.Run("yamlBindsList", func(t *testing.T) {
		cfg := &Config{}
		applyDefaults(cfg)
		raw := []byte(`external:
  youtube_player_client_fallback: [ios, web_creator]
`)
		if err := yaml.Unmarshal(raw, cfg); err != nil {
			t.Fatalf("yaml unmarshal failed: %v", err)
		}
		assert.Equal(t, []string{"ios", "web_creator"}, cfg.External.YoutubePlayerClientFallback)
	})

	t.Run("envBindsCommaSeparatedList", func(t *testing.T) {
		t.Setenv("VELOX_YOUTUBE_PLAYER_CLIENT_FALLBACK", " ios, web_creator, tv ")
		cfg := &Config{}
		applyDefaults(cfg)
		applyEnvVars(cfg)
		assert.Equal(t, []string{"ios", "web_creator", "tv"}, cfg.External.YoutubePlayerClientFallback)
	})
}

// ---------- YouTube gate + 429 pacing (YT-GATE) ----------

func TestExternalConfigYouTubeGateBinding(t *testing.T) {
	t.Run("gateDefaultsAreProductionShaped", func(t *testing.T) {
		cfg := &Config{}
		applyDefaults(cfg)
		assert.Equal(t, 3, cfg.External.YoutubeGlobalConcurrency)
		assert.Equal(t, 60, cfg.External.Youtube429CooldownSeconds)
	})

	t.Run("yamlBindsGateFields", func(t *testing.T) {
		cfg := &Config{}
		applyDefaults(cfg)
		raw := []byte(`external:
  youtube_global_concurrency: 7
  youtube_429_cooldown_seconds: 120
`)
		if err := yaml.Unmarshal(raw, cfg); err != nil {
			t.Fatalf("yaml unmarshal failed: %v", err)
		}
		assert.Equal(t, 7, cfg.External.YoutubeGlobalConcurrency)
		assert.Equal(t, 120, cfg.External.Youtube429CooldownSeconds)
	})

	t.Run("envBindsGateFields", func(t *testing.T) {
		t.Setenv("VELOX_YOUTUBE_GLOBAL_YTDLP_CONCURRENCY", "5")
		t.Setenv("VELOX_YOUTUBE_429_COOLDOWN_SECONDS", "90")
		cfg := &Config{}
		applyDefaults(cfg)
		applyEnvVars(cfg)
		assert.Equal(t, 5, cfg.External.YoutubeGlobalConcurrency)
		assert.Equal(t, 90, cfg.External.Youtube429CooldownSeconds)
	})
}
