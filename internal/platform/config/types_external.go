package config

import (
	"os"
	"strings"
)

type ExternalConfig struct {
	OllamaURL            string `yaml:"ollama_url" env:"OLLAMA_ADDR" default:"http://localhost:11434"`
	OllamaModel          string `yaml:"ollama_model" env:"OLLAMA_MODEL" default:"gemma4:e4b"`
	OllamaEmbedModel     string `yaml:"ollama_embed_model" env:"OLLAMA_EMBED_MODEL"`
	OllamaMetadataModel  string `yaml:"ollama_metadata_model" env:"OLLAMA_METADATA_MODEL" default:""`
	OllamaTimeoutSeconds int    `yaml:"ollama_timeout_seconds" env:"OLLAMA_TIMEOUT" default:"600"`
	// OllamaWarmTimeoutSeconds is separate from the per-request timeout:
	// startup/model residency may legitimately spend several minutes loading a
	// cold model into VRAM before the first token is produced.
	OllamaWarmTimeoutSeconds    int      `yaml:"ollama_warm_timeout_seconds" env:"VELOX_OLLAMA_WARM_TIMEOUT" default:"600"`
	YtdlpPath                   string   `yaml:"ytdlp_path" env:"YTDLP_PATH" default:"yt-dlp"`
	YoutubePlayerClientFallback []string `yaml:"youtube_player_client_fallback" env:"VELOX_YOUTUBE_PLAYER_CLIENT_FALLBACK" default:"[]"`
	YoutubeMinSleepSeconds      int      `yaml:"youtube_min_sleep_seconds" env:"YTDLP_MIN_SLEEP_SECONDS" default:"2"`
	YoutubeMaxSleepSeconds      int      `yaml:"youtube_max_sleep_seconds" env:"YTDLP_MAX_SLEEP_SECONDS" default:"5"`
	YoutubeGlobalConcurrency    int      `yaml:"youtube_global_concurrency" env:"VELOX_YOUTUBE_GLOBAL_YTDLP_CONCURRENCY" default:"3"`
	Youtube429CooldownSeconds   int      `yaml:"youtube_429_cooldown_seconds" env:"VELOX_YOUTUBE_429_COOLDOWN_SECONDS" default:"60"`
	FfmpegPath                  string   `yaml:"ffmpeg_path" env:"FFMPEG_PATH" default:"ffmpeg"`
	RustMusclesPath             string   `yaml:"rust_muscles_path" env:"VELOX_RUST_MUSCLES_PATH" default:"bin/pipelinegen-muscles"`
	RustVisualNERPath           string   `yaml:"rust_visualner_path" env:"VELOX_RUST_VISUALNER_PATH" default:"bin/visualner"`
	// RustSemanticEnginePath is the unified semantic worker
	// (`semantic_manifest.v2`): one analyze call per script replaces the
	// per-category VisualNER bullet/chart calls.
	RustSemanticEnginePath string `yaml:"rust_semantic_engine_path" env:"VELOX_RUST_SEMANTIC_ENGINE_PATH" default:"bin/semantic-engine"`
	RustMediaSamplerPath   string `yaml:"rust_mediasampler_path" env:"VELOX_RUST_MEDIASAMPLER_PATH" default:"bin/mediasampler"`
	VisualNERBackend       string `yaml:"visualner_backend" env:"VELOX_VISUALNER_BACKEND" default:"rust"`
	SpacyNERURL            string `yaml:"spacy_ner_url" env:"VELOX_SPACY_NER_URL" default:""`
	NvidiaAPIKey           string `yaml:"nvidia_api_key" env:"NVIDIA_API_KEY" default:""`
	NvidiaModel            string `yaml:"nvidia_model" env:"NVIDIA_MODEL" default:"stabilityai/sdxl-turbo"`
	NvidiaLocalNIMURL      string `yaml:"nvidia_local_nim_url" env:"NVIDIA_LOCAL_NIM_URL" default:"http://localhost:8000/v1/infer"`

	// VeloxMasterURL is the canonical address of a remote PipelineGen master.
	// Workers and external clients (n8n, Google Flow sidecars, scripts)
	// read this env var (VELOX_MASTER_URL) so deployments don't have to
	// hardcode hosts. Defaults to http://127.0.0.1:8000 for local dev.
	//
	// Compose/Docker patterns:
	//   - Docker Compose service:  http://velox-server:8000
	//   - Master on host, worker  in Docker: http://host.docker.internal:8000
	//     (Linux requires extra_hosts: ["host.docker.internal:host-gateway"])
	//   - Local dev:  http://127.0.0.1:8000
	VeloxMasterURL string `yaml:"velox_master_url" env:"VELOX_MASTER_URL" default:"http://127.0.0.1:8000"`

	// VeloxBaseURL is the publicly routable URL of THIS PipelineGen
	// server, used by the image service to construct webhook_url for
	// remote image generation callbacks. Distinct from VeloxMasterURL
	// (which is the broker the worker speaks to): VeloxBaseURL is the
	// hostname remote-sidecar clients use to reach us.
	VeloxBaseURL string `yaml:"velox_base_url" env:"VELOX_BASE_URL" default:""`

	// Remote image endpoint (Google Flow on remote server).
	RemoteImageEndpointURL string `yaml:"remote_image_endpoint_url" env:"REMOTE_IMAGE_ENDPOINT_URL" default:""`
	UseNvidiaForLLM        bool   `yaml:"use_nvidia_for_llm" env:"VELOX_USE_NVIDIA_FOR_LLM" default:"false"`
	NvidiaLLMModel         string `yaml:"nvidia_llm_model" env:"VELOX_NVIDIA_LLM_MODEL" default:"meta/llama-3.1-8b-instruct"`

	PixabayAPIKey  string `yaml:"pixabay_api_key" env:"PIXABAY_API_KEY" default:""`
	PixabayBaseURL string `yaml:"pixabay_base_url" env:"PIXABAY_BASE_URL" default:"https://pixabay.com/api"`
	PexelsAPIKey   string `yaml:"pexels_api_key" env:"PEXELS_API_KEY" default:""`
	PexelsBaseURL  string `yaml:"pexels_base_url" env:"PEXELS_BASE_URL" default:"https://api.pexels.com/v1"`

	// Default "node-scraper" relative to working dir.
	NodeScraperDir string `yaml:"node_scraper_dir" env:"VELOX_NODE_SCRAPER_DIR" default:"node-scraper"`

	// YouTube cookies + JS runtime for yt-dlp. The canonical deployment
	// override is VELOX_YOUTUBE_COOKIES_FILE; ResolveYouTubeCookiesPath
	// retains YT_COOKIES_PATH as a migration-only fallback.
	YouTubeCookiesPath   string `yaml:"youtube_cookies_path" env:"VELOX_YOUTUBE_COOKIES_FILE" default:""`
	YouTubeJSRuntimePath string `yaml:"youtube_js_runtime_path" env:"YT_JS_RUNTIME_PATH" default:"node"`

	// SearXNG — strictly OPTIONAL sidecar for LLM RAG augmentation.
	//
	// Default URL is the canonical SearXNG compose URL (port 8080). The
	// runtime only calls SearXNG when:
	//
	//   1. SEARXNG_URL is non-empty after env + yaml resolution, AND
	//   2. The configured URL responds to /health at startup (see
	//      composeIntegration's SearXNG probe). If the URL is unreachable
	//      the server logs WARN and disables web-search features without
	//      failing the boot — jobs that REQUIRE SearXNG then return
	//      `provider_not_configured` (overnight-error contract, see
	//      provider_sync.go).
	//
	// Operators that don't use SearXNG should leave SEARXNG_URL at default
	// and skip starting the sidecar (most production deployments). The
	// system reports "SearXNG unavailable" in /api/system/doctor and the
	// affected code paths are documented in AGENTS.md.
	SearxngURL              string `yaml:"searxng_url" env:"SEARXNG_URL" default:"http://127.0.0.1:8080"`
	SearxngMaxResults       int    `yaml:"searxng_max_results"     env:"SEARXNG_MAX_RESULTS"     default:"12"`
	SearxngLanguage         string `yaml:"searxng_language" env:"SEARXNG_LANGUAGE" default:"en"`
	SearxngEngines          string `yaml:"searxng_engines" env:"SEARXNG_ENGINES" default:"bing,mwmbl"`
	WebSearchTimeoutSeconds int    `yaml:"web_search_timeout_seconds" env:"SEARXNG_TIMEOUT" default:"15"`

	// ResearchFallbackProvider controls the fallback web search provider
	// when SearXNG is unavailable. Canonical values:
	//
	//   "duckduckgo" — DuckDuckGo HTML text search (best-effort, no API key)
	//   ""           — no fallback (research fails closed if SearXNG is down)
	//
	// Default: "duckduckgo" (multi-provider fallback).
	ResearchFallbackProvider string `yaml:"research_fallback_provider" env:"RESEARCH_FALLBACK_PROVIDER" default:"duckduckgo"`

	// ResearchTargetPoolSize is the minimum number of subject-valid
	// search results required before the page-fetch phase begins.
	// Higher values improve evidence diversity but increase latency.
	//
	// Default: 8 (tuned for boxing research pipelines).
	ResearchTargetPoolSize int `yaml:"research_target_pool_size" env:"RESEARCH_TARGET_POOL_SIZE" default:"8"`

	// PR-011 (July 2026): Stock RLM/LLM enrichment pass.
	//
	// StockEnrichmentEnabled gates the canonical enrichment pipeline
	// (internal/capabilities/assets/providers/stock/enrichment). When
	// false, the composition root skips wiring the EnrichmentHandler
	// (mirror of the StockPipelineEnabled pattern in
	// internal/capabilities/assets/stock/module.go). When true, the worker pool picks up
	// `media.stock_rlm_enrich` jobs and dispatches them to the
	// EnrichmentHandler via the CompiledJobRegistry.
	//
	// Default false (godlike/07 fail-closed): operators must explicitly
	// opt-in. The enrichment LLM call is the load-bearing pre-condition;
	// the job type would otherwise be silently retried forever
	// (godlike/07 no-fake-availability: missing-wiring = no-enqueue, not
	// enqueue-and-fail-on-llm-error).
	StockEnrichmentEnabled bool `yaml:"stock_enrichment_enabled" env:"STOCK_ENRICHMENT_ENABLED" default:"false"`

	// ParseArenaLLM is the Ollama model identifier used by the stock
	// RLM/LLM enrichment pass (PR-011). The "parse_arena" prefix is the
	// canonical naming convention for the parse-arena model family
	// (gemma4:e4b + gemma4:e2b siblings). When empty, the enrichment
	// handler falls back to cfg.External.OllamaModel at composition
	// time (defense-in-depth; never silently blank).
	ParseArenaLLM string `yaml:"parse_arena_llm" env:"PARSE_ARENA_LLM" default:""`

	// EnrichmentPromptVersion is the per-capability system-prompt
	// version for the stock RLM/LLM enrichment pass (PR-011B).
	// Canonical values: "v1" (English, default) | "v2" / "v2-it"
	// (Italian). Empty falls back to "v1" per godlike/07 fail-closed
	// (selectSystemPrompt in llm_client.go).
	EnrichmentPromptVersion string `yaml:"enrichment_prompt_version" env:"ENRICHMENT_PROMPT_VERSION" default:""`

	// RenderingGenQueueURL is the address of the central RenderingGen
	// queue service (POST /jobs, GET /jobs/{id}). When non-empty, the
	// script-generation runtime enqueues render work to the central
	// queue and waits for the certified artifact instead of running the
	// local Rust executor. Empty keeps the local executor as default.
	RenderingGenQueueURL string `yaml:"renderinggen_queue_url" env:"RENDERINGGEN_QUEUE_URL" default:""`

	// RenderingGenPollIntervalMS tunes the polling FALLBACK used while
	// waiting for a job's certified artifact.
	//
	// The primary path is event-driven: the RenderingGen queue client
	// long-polls GET /jobs/{id}/wait, so a terminal transition is observed
	// directly instead of at the next sampling tick. This key therefore only
	// matters for a queue deployment without that route (or a client without
	// the wait capability), where the interval is pure dead time on every
	// job: a clip whose render finishes just after a poll is only noticed at
	// the next tick.
	//
	// 0 (default) keeps the built-in 250 ms fallback cadence. See
	// waitClipQueue and waitForCompletion for the two fallback loops.
	RenderingGenPollIntervalMS int `yaml:"renderinggen_poll_interval_ms" env:"RENDERINGGEN_POLL_INTERVAL_MS" default:"0"`

	// GeocodingBaseURL wires a Nominatim-compatible geocoder for grounded
	// location overlays. Empty leaves location map generation unavailable.
	GeocodingBaseURL string `yaml:"geocoding_base_url" env:"VELOX_GEOCODING_BASE_URL" default:""`
	// GeocodingUserAgent identifies this application to the geocoding service.
	// It is required by the adapter when a geocoder endpoint is configured;
	// public Nominatim users must include a monitored contact URL or address.
	GeocodingUserAgent string `yaml:"geocoding_user_agent" env:"VELOX_GEOCODING_USER_AGENT" default:""`
	// GeocodingCacheDir overrides the durable positive-result cache. A relative
	// path is rooted under storage.data_dir; empty uses <data_dir>/geocoding-cache.
	GeocodingCacheDir string `yaml:"geocoding_cache_dir" env:"VELOX_GEOCODING_CACHE_DIR" default:""`
	// MapPlateManifestPath points to the operator-certified local raster
	// manifest. Empty disables map overlays; configured manifests are validated
	// at runtime construction and invalid manifests fail startup.
	MapPlateManifestPath string `yaml:"map_plate_manifest_path" env:"VELOX_MAP_PLATE_MANIFEST_PATH" default:""`
	// GeoMapPlateGeneratorPath is ChrononTemplate's Web Mercator tile-to-PNG
	// bridge. When configured, PipelineGen generates content-addressed local
	// basemap plates at the script locations instead of requiring a static map
	// manifest. Empty preserves the static-manifest path above.
	GeoMapPlateGeneratorPath string `yaml:"geo_map_plate_generator_path" env:"VELOX_GEO_MAP_PLATE_GENERATOR_PATH" default:""`
}

// ResolveYouTubeCookiesPath returns the one canonical cookie path used by
// every YouTube yt-dlp command. It never reads the cookie file. The
// VELOX_YOUTUBE_COOKIES_FILE-backed field wins; YT_COOKIES_PATH is retained
// only as a compatibility bridge for older deployments and is not a default
// filename, so missing authentication remains visible instead of silently
// targeting a repository-local cookie file.
func (c *ExternalConfig) ResolveYouTubeCookiesPath() string {
	if path := strings.TrimSpace(os.Getenv("VELOX_YOUTUBE_COOKIES_FILE")); path != "" {
		return path
	}
	if c != nil {
		if path := strings.TrimSpace(c.YouTubeCookiesPath); path != "" {
			return path
		}
	}
	return strings.TrimSpace(os.Getenv("YT_COOKIES_PATH"))
}

// ResolvedYtdlpPath returns the configured yt-dlp path, falling back to "yt-dlp" if empty.
func (c *ExternalConfig) ResolvedYtdlpPath() string {
	if c.YtdlpPath != "" {
		return c.YtdlpPath
	}
	return "yt-dlp"
}

// ResolvedYouTubeSleepSeconds returns the configured pacing interval for
// YouTube yt-dlp requests. A non-positive minimum disables pacing; the
// maximum is clamped to the minimum so callers always receive a valid range.
func (c *ExternalConfig) ResolvedYouTubeSleepSeconds() (int, int) {
	if c == nil {
		return 0, 0
	}
	min := c.YoutubeMinSleepSeconds
	if min < 0 {
		min = 0
	}
	max := c.YoutubeMaxSleepSeconds
	if max < min {
		max = min
	}
	return min, max
}

// ResolvedMasterURL returns the configured master URL, falling back to
// the canonical dev default (127.0.0.1:8000) if empty. The default aligns
// with ServerConfig.Port so workers running locally connect to the
// in-process server without explicit config.
func (c *ExternalConfig) ResolvedMasterURL() string {
	if c.VeloxMasterURL != "" {
		return c.VeloxMasterURL
	}
	return "http://127.0.0.1:8000"
}
