package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
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

// ---------- PR-ARTLIST-CONFIG-PREFIX (July 2026) ----------
//
// TestConfigLoaderReadsVeloxArtlistScraperServerURLFromEnv pins the
// loader binding for cfg.External.ArtlistScraperServerURL →
// env VELOX_ARTLIST_SCRAPER_SERVER_URL. Before this PR the loader
// read the bare ARTLIST_SCRAPER_SERVER_URL (which docker-compose.yml
// never set: only the VELOX_-prefixed form was exported). Result:	// fail-closed gate #5 in internal/app/build_bundles_artlist_artlist.go fired
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

// ---------- PR-ARTLIST-AUTHORIZED-BY-DEFAULT (P1, July 2026) ----------
//
// TestConfigDefaults_ArtlistAcquisitionIsAuthorized pins the LOAD-BEARING
// default flip on the LOADER side (not just resolver-side). When
// applyDefaults() runs on a fresh Config struct with no env/yaml
// overrides, the new defaults MUST be applied verbatim:
//
//	ArtlistAcquisitionMode:    "authorized_api"
//	ArtlistDailyDownloadLimit: 10
//
// Without this test, a future regression that flips the struct tags back
// to manual_import / limit=0 would only be caught downstream via the
// resolver test in internal/infrastructure/artlist/downloader/resolver_test.go
// (which mirrors the plumb-through but does not exercise the struct tag
// itself). The loader defaults are the canonical source of truth; pin
// them explicitly with the testify assertion-apis (matches the convention
// of prior tests in this file where applicable).
func TestConfigDefaults_ArtlistAcquisitionIsAuthorized(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)

	assert.Equal(t, "authorized_api", cfg.External.ArtlistAcquisitionMode,
		"PR-ARTLIST-AUTHORIZED-BY-DEFAULT P1: loader default for ArtlistAcquisitionMode MUST be authorized_api")
	assert.Equal(t, 10, cfg.External.ArtlistDailyDownloadLimit,
		"PR-ARTLIST-AUTHORIZED-BY-DEFAULT P1: loader default for ArtlistDailyDownloadLimit MUST be 10")
}

// TestConfigOverride_ArtlistAcquisitionMode_ManualImport pins the env
// override path: when ARTLIST_ACQUISITION_MODE=manual_import is set
// EXPLICITLY, the operator's value MUST win over the P1 default
// (godlike/06 SSOT: env > yaml > default resolution order in applyEnvVars).
// This guards the cutover from accidentally swallowing the manual_import
// escape hatch.
func TestConfigOverride_ArtlistAcquisitionMode_ManualImport(t *testing.T) {
	t.Setenv("ARTLIST_ACQUISITION_MODE", "manual_import")
	cfg := &Config{}
	applyDefaults(cfg)
	applyEnvVars(cfg)
	assert.Equal(t, "manual_import", cfg.External.ArtlistAcquisitionMode,
		"operator opt-out via ARTLIST_ACQUISITION_MODE=manual_import MUST take precedence over the P1 default")
}

// ---------- PR-ARTLIST-SKIP-TRANSCRIPTION-OPT-IN (July 2026) ----------
//
// TestConfigDefaults_ArtlistSkipTranscriptionIsFalse pins the loader
// default for cfg.External.ArtlistSkipTranscription. The godlike/07
// fail-closed default MUST be false so the mandatory transcription
// (PR-ARTLIST-MANDATORY-TRANSCRIPTION) survives when the operator does
// not opt in. Without this pin, a future regression that flips the
// default OR removes the env/yaml binding would silently re-enable
// the deterministic RETRY_WAIT loop that motivated this PR.
//
// The test mirrors the ArtlistDailyDownloadLimit pattern: fresh
// Config{}, applyDefaults() only — no env, no yaml.
func TestConfigDefaults_ArtlistSkipTranscriptionIsFalse(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	assert.False(t, cfg.External.ArtlistSkipTranscription,
		"PR-ARTLIST-SKIP-TRANSCRIPTION-OPT-IN: loader default for ArtlistSkipTranscription MUST be false (mandatory transcription preserved)")
}

// TestConfigOverride_ArtlistSkipTranscription_True pins the env override
// path: when the operator sets ARTLIST_SKIP_TRANSCRIPTION=true
// explicitly, the loader MUST bind it. This is the canonical escape
// hatch for environments where the `whisper` binary is unavailable
// (the deterministic RETRY_WAIT root cause this PR fixes).
func TestConfigOverride_ArtlistSkipTranscription_True(t *testing.T) {
	t.Setenv("ARTLIST_SKIP_TRANSCRIPTION", "true")
	cfg := &Config{}
	applyDefaults(cfg)
	applyEnvVars(cfg)
	assert.True(t, cfg.External.ArtlistSkipTranscription,
		"operator opt-in via ARTLIST_SKIP_TRANSCRIPTION=true MUST bind (escape hatch for non-whisper environments)")
}
