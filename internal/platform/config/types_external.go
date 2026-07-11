package config

type ExternalConfig struct {
	OllamaURL            string `yaml:"ollama_url" env:"OLLAMA_ADDR" default:"http://localhost:11434"`
	OllamaModel          string `yaml:"ollama_model" env:"OLLAMA_MODEL" default:"gemma4:e4b"`
	OllamaEmbedModel     string `yaml:"ollama_embed_model" env:"OLLAMA_EMBED_MODEL" default:"nomic-embed-text"`
	OllamaMetadataModel  string `yaml:"ollama_metadata_model" env:"OLLAMA_METADATA_MODEL" default:""`
	OllamaTimeoutSeconds int    `yaml:"ollama_timeout_seconds" env:"OLLAMA_TIMEOUT" default:"600"`
	YtdlpPath            string `yaml:"ytdlp_path" env:"YTDLP_PATH" default:"yt-dlp"`
	FfmpegPath           string `yaml:"ffmpeg_path" env:"FFMPEG_PATH" default:"ffmpeg"`
	NvidiaAPIKey         string `yaml:"nvidia_api_key" env:"NVIDIA_API_KEY" default:""`
	NvidiaModel          string `yaml:"nvidia_model" env:"NVIDIA_MODEL" default:"stabilityai/sdxl-turbo"`
	NvidiaLocalNIMURL    string `yaml:"nvidia_local_nim_url" env:"NVIDIA_LOCAL_NIM_URL" default:"http://localhost:8000/v1/infer"`

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

	// vLLM backend for continuous batching (OpenAI-compatible API).
	// When USE_VLLM=true, the Chat() client sends requests to VLLM_URL
	// instead of Ollama. Mutually exclusive with UseNvidiaForLLM.
	UseVLLM   bool   `yaml:"use_vllm" env:"USE_VLLM" default:"false"`
	VLLMURL   string `yaml:"vllm_url" env:"VLLM_URL" default:"http://localhost:8000"`
	VLLMModel string `yaml:"vllm_model" env:"VLLM_MODEL" default:"gemma4:e4b"`

	PixabayAPIKey  string `yaml:"pixabay_api_key" env:"PIXABAY_API_KEY" default:""`
	PixabayBaseURL string `yaml:"pixabay_base_url" env:"PIXABAY_BASE_URL" default:"https://pixabay.com/api"`
	PexelsAPIKey   string `yaml:"pexels_api_key" env:"PEXELS_API_KEY" default:""`
	PexelsBaseURL  string `yaml:"pexels_base_url" env:"PEXELS_BASE_URL" default:"https://api.pexels.com/v1"`

	// NodeScraperDir directory containing the Node.js scraper scripts (artlist_search.js, etc.).
	// Default "node-scraper" relative to working dir.
	NodeScraperDir string `yaml:"node_scraper_dir" env:"VELOX_NODE_SCRAPER_DIR" default:"node-scraper"`

	// YouTube cookies + JS runtime for yt-dlp.
	YouTubeCookiesPath   string `yaml:"youtube_cookies_path" env:"YT_COOKIES_PATH" default:"cookies.txt"`
	YouTubeJSRuntimePath string `yaml:"youtube_js_runtime_path" env:"YT_JS_RUNTIME_PATH" default:"node"`

	// SearXNG — strictly OPTIONAL sidecar for LLM RAG augmentation.
	//
	// Default URL is the canonical SearXNG dev URL (port 18080). The
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
	SearxngURL              string `yaml:"searxng_url" env:"SEARXNG_URL" default:"http://127.0.0.1:18080"`
	SearxngMaxResults       int    `yaml:"searxng_max_results"     env:"SEARXNG_MAX_RESULTS"     default:"5"`
	WebSearchTimeoutSeconds int    `yaml:"web_search_timeout_seconds" env:"SEARXNG_TIMEOUT" default:"15"`

	// Artlist scraper optimizations
	ArtlistScraperServerURL        string `yaml:"artlist_scraper_server_url" env:"ARTLIST_SCRAPER_SERVER_URL" default:""`
	ArtlistLiveSearchCacheTTLHours int    `yaml:"artlist_live_search_cache_ttl_hours" env:"ARTLIST_CACHE_TTL_HOURS" default:"24"`

	// Artlist cookies path for yt-dlp (July 2026): replaces the hardcoded
	// `/tmp/artlist_cookies.txt` in internal/infrastructure/downloader/downloader.go.
	// Empty default (godlike/07 fail-closed): when unset, the downloader SKIPS the
	// `--cookies` flag entirely so operators see a visible 403 from Artlist instead
	// of a silent `--cookies /nonexistent/path` failure. Operators who need
	// authenticated Artlist downloads set ARTLIST_COOKIES_PATH to a real file
	// (typically produced by `yt-dlp --cookies-from-browser chrome`).
	ArtlistCookiesPath string `yaml:"artlist_cookies_path" env:"ARTLIST_COOKIES_PATH" default:""`

	// ArtlistSearchStrategy controls the Pexels/Pixabay fallback chain
	// (PR-AUDIT-5, July 2026). Canonical values:
	//
	//   artlist_only               — ONLY the Artlist scraper (no Pixabay/Pexels).
	//                                The safest default.
	//   artlist_then_public_fallback — Artlist scraper first, then Pixabay + Pexels
	//                                  as fallback (the prior implicit behaviour).
	//   public_only_for_dev          — ONLY Pixabay + Pexels (no scraper).
	//
	// Default: artlist_only (godlike/07 fail-closed — no external stock sources
	// without explicit operator opt-in). The prior implicit fallback chain is
	// now gated by this config field so operators see exactly which searchers
	// are active at boot time.
	ArtlistSearchStrategy string `yaml:"artlist_search_strategy" env:"ARTLIST_SEARCH_STRATEGY" default:"artlist_only"`

	// ArtlistAcquisitionMode controls whether Artlist assets are acquired
	// automatically or imported manually. Canonical values:
	//
	//   manual_import — PipelineGen does NOT download automatically. Users
	//                     download assets from Artlist and place them in the
	//                     import folder; the pipeline ingests them and records
	//                     provenance. This is the default (godlike/07 fail-closed).
	//   authorized_api  — Automatic search+download is allowed, typically under
	//                     an Enterprise/API agreement. Subject to the daily
	//                     download limit configured below.
	//
	// Default: manual_import.
	ArtlistAcquisitionMode string `yaml:"artlist_acquisition_mode" env:"ARTLIST_ACQUISITION_MODE" default:"manual_import"`

	// ArtlistAccountID is the logical account identifier used for rate-limit
	// and audit tracking. Single-tenant deployments can leave it as "default";
	// multi-tenant setups can scope downloads per account.
	ArtlistAccountID string `yaml:"artlist_account_id" env:"ARTLIST_ACCOUNT_ID" default:"default"`

	// ArtlistDailyDownloadLimit is the maximum number of Artlist assets that
	// can be downloaded automatically per account per day. A value of 0 means
	// automatic downloads are disabled (default). Operators must set a positive
	// value to enable automatic downloads in authorized_api mode.
	ArtlistDailyDownloadLimit int `yaml:"artlist_daily_download_limit" env:"ARTLIST_DAILY_DOWNLOAD_LIMIT" default:"0"`

	// PR-011 (July 2026): Stock RLM/LLM enrichment pass.
	//
	// StockEnrichmentEnabled gates the canonical enrichment pipeline
	// (internal/application/assets/providers/stock/enrichment). When
	// false, the composition root skips wiring the EnrichmentHandler
	// (mirror of the StockPipelineEnabled pattern in
	// api/assets/stock/module.go). When true, the worker pool picks up
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
}

// ResolvedYtdlpPath returns the configured yt-dlp path, falling back to "yt-dlp" if empty.
func (c *ExternalConfig) ResolvedYtdlpPath() string {
	if c.YtdlpPath != "" {
		return c.YtdlpPath
	}
	return "yt-dlp"
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
