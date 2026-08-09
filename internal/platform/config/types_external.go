package config

import (
	"os"
	"strings"
)

type ExternalConfig struct {
	OllamaURL                   string   `yaml:"ollama_url" env:"OLLAMA_ADDR" default:"http://localhost:11434"`
	OllamaModel                 string   `yaml:"ollama_model" env:"OLLAMA_MODEL" default:"gemma4:e4b"`
	OllamaEmbedModel            string   `yaml:"ollama_embed_model" env:"OLLAMA_EMBED_MODEL" default:"nomic-embed-text"`
	OllamaMetadataModel         string   `yaml:"ollama_metadata_model" env:"OLLAMA_METADATA_MODEL" default:""`
	OllamaTimeoutSeconds        int      `yaml:"ollama_timeout_seconds" env:"OLLAMA_TIMEOUT" default:"600"`
	YtdlpPath                   string   `yaml:"ytdlp_path" env:"YTDLP_PATH" default:"yt-dlp"`
	YoutubePlayerClientFallback []string `yaml:"youtube_player_client_fallback" env:"VELOX_YOUTUBE_PLAYER_CLIENT_FALLBACK" default:"[]"`
	YoutubeMinSleepSeconds      int      `yaml:"youtube_min_sleep_seconds" env:"YTDLP_MIN_SLEEP_SECONDS" default:"0"`
	YoutubeMaxSleepSeconds      int      `yaml:"youtube_max_sleep_seconds" env:"YTDLP_MAX_SLEEP_SECONDS" default:"0"`
	FfmpegPath                  string   `yaml:"ffmpeg_path" env:"FFMPEG_PATH" default:"ffmpeg"`
	MediaExecutor               string   `yaml:"media_executor" env:"VELOX_MEDIA_EXECUTOR" default:"go"`
	RustMusclesPath             string   `yaml:"rust_muscles_path" env:"VELOX_RUST_MUSCLES_PATH" default:"pipelinegen-muscles"`
	NvidiaAPIKey                string   `yaml:"nvidia_api_key" env:"NVIDIA_API_KEY" default:""`
	NvidiaModel                 string   `yaml:"nvidia_model" env:"NVIDIA_MODEL" default:"stabilityai/sdxl-turbo"`
	NvidiaLocalNIMURL           string   `yaml:"nvidia_local_nim_url" env:"NVIDIA_LOCAL_NIM_URL" default:"http://localhost:8000/v1/infer"`

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
	SearxngMaxResults       int    `yaml:"searxng_max_results"     env:"SEARXNG_MAX_RESULTS"     default:"5"`
	SearxngLanguage         string `yaml:"searxng_language" env:"SEARXNG_LANGUAGE" default:"en"`
	SearxngEngines          string `yaml:"searxng_engines" env:"SEARXNG_ENGINES" default:"bing,mwmbl,wiby"`
	WebSearchTimeoutSeconds int    `yaml:"web_search_timeout_seconds" env:"SEARXNG_TIMEOUT" default:"15"`

	// Artlist scraper optimizations
	// (PR-ARTLIST-CONFIG-PREFIX, July 2026): env var renamed from the
	// bare ARTLIST_SCRAPER_SERVER_URL to VELOX_-prefixed form so it
	// matches docker-compose.yml + the Velox internal-services naming
	// convention (VELOX_MASTER_URL, VELOX_NODE_SCRAPER_DIR, ...). The
	// YAML key `artlist_scraper_server_url` is unchanged for backward
	// compatibility with existing config.example.yaml consumers.
	// godlike/07 fail-closed: cutover is pure (no fallback to the bare
	// name); validateArtlistScraperURL in internal/app/build_bundles_artlist.go
	// surfaces the missing env at boot with the new VELOX_-prefixed
	// escape-hatch instructions in the error message.
	ArtlistScraperServerURL        string `yaml:"artlist_scraper_server_url" env:"VELOX_ARTLIST_SCRAPER_SERVER_URL" default:""`
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
	//   manual_import   — Operator-pinned opt-out: PipelineGen does NOT
	//                     download automatically. Users download assets
	//                     from Artlist themselves and place them in the
	//                     import folder; the pipeline ingests them and
	//                     records provenance. Reserved for Artlist
	//                     Enterprise agreements that prohibit server-side
	//                     fetches, or for environments where the operator
	//                     explicitly wants to enforce manual curation.
	//   authorized_api  — Automatic search+download is allowed under an
	//                     Enterprise/API agreement; per-day volume is
	//                     capped by ArtlistDailyDownloadLimit below.
	//
	// Default: authorized_api (PR-ARTLIST-AUTHORIZED-BY-DEFAULT P1, July 2026).
	// The P1 cutover flips the historical fail-closed default
	// (manual_import) so a fresh deploy of PipelineGen is immediately
	// useful for the Artlist-driven stock pipeline. The blast-radius
	// cap is the daily limit (default 10, next field); operators that
	// need to opt OUT of automatic downloads set the env var explicitly
	//   ARTLIST_ACQUISITION_MODE=manual_import
	// and the resolvePath gate in the downloader returns
	// ErrManualImportActive (godlike/07 fail-closed at the resolver
	// surface, NOT at the loader surface — the loader reports
	// authorized_api by default + a typed error surfaces on first
	// /run request when manual_import is active).
	ArtlistAcquisitionMode string `yaml:"artlist_acquisition_mode" env:"ARTLIST_ACQUISITION_MODE" default:"authorized_api"`

	// ArtlistAccountID is the logical account identifier used for rate-limit
	// and audit tracking. Single-tenant deployments can leave it as "default";
	// multi-tenant setups can scope downloads per account.
	ArtlistAccountID string `yaml:"artlist_account_id" env:"ARTLIST_ACCOUNT_ID" default:"default"`

	// ArtlistDailyDownloadLimit is the maximum number of automatic Artlist
	// downloads per account per day when AcquisitionMode=authorized_api.
	// A value of 0 disables automatic downloads even in authorized_api mode
	// (the resolver returns ErrAutomaticDownloadsDisabled and the audit
	// row is not recorded).
	//
	// Default: 10 (PR-ARTLIST-AUTHORIZED-BY-DEFAULT P1, July 2026). The
	// 10/day cap aligns with Artlist's per-account quota for non-Enterprise
	// plans; operators with Enterprise agreements that permit higher
	// volume set the env override
	//   ARTLIST_DAILY_DOWNLOAD_LIMIT=N   (N > 0)
	// Operators that prefer to opt out of automatic downloads entirely
	// keep AcquisitionMode=manual_import and the value of this field
	// becomes irrelevant (the resolver gate fires before the limit check).
	ArtlistDailyDownloadLimit int `yaml:"artlist_daily_download_limit" env:"ARTLIST_DAILY_DOWNLOAD_LIMIT" default:"10"`

	// ArtlistSkipTranscription gates the PR-ARTLIST-MANDATORY-TRANSCRIPTION
	// step (whisper call + text_track write) per operator opt-in. When
	// true, the orchestrator treats each clip as processed after
	// mediaProcessor.Process succeeds WITHOUT calling Transcribe and
	// WITHOUT writing a text_track row — the clip lands on Drive with
	// no transcript.
	//
	// This is a deliberate escape hatch for environments where the
	// `whisper` binary (or the `openai-whisper` Python package + model
	// weights) is unavailable. The mandatory transcription contract
	// was the deterministic-cause root of a RETRY_WAIT loop on hosts
	// without whisper: Transcribe failed on every clip,
	// EvaluateRunOutcome fired the "all artlist items failed" verdict,
	// and ScheduleRetry bounced every job.
	//
	// Default: false (godlike/07 fail-closed — preservation of the
	// mandatory semantics). Operators who cannot install whisper in
	// production MUST set
	//   ARTLIST_SKIP_TRANSCRIPTION=true
	// in their env. This is an explicit, audited operator intent:
	// every skipped transcript is a documented data-quality gap, not a
	// silent failure.
	//
	// NOTE: this flag is NOT a fix for "transcription is broken" — it
	// is the operator-side escape hatch. The canonical fix is to
	// install `whisper` and leave the flag at false. The operator
	// decision matrix:
	//   - production with transcripts:    false (default)
	//   - staging / CI / air-gapped env:  true (explicit opt-in)
	ArtlistSkipTranscription bool `yaml:"artlist_skip_transcription" env:"ARTLIST_SKIP_TRANSCRIPTION" default:"false"`

	// Artlist (PR-CONFIG-SPLIT, July 2026): nested config-grouping of
	// the artlist-related keys. The flat fields above (ArtlistAcquisitionMode,
	// ArtlistDailyDownloadLimit) remain for backward compatibility with
	// pre-split configs; the YAML loader prefers the nested shape when
	// both are present. Operators that adopt the nested shape get the
	// acquisition_mode / auto_download policy split (orthogonal concerns:
	// auto_download.enabled=false still allows authorized_api search
	// but blocks automatic downloads for manual curation).
	Artlist ArtlistConfig `yaml:"artlist"`

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

// ArtlistConfig groups Artlist-related configuration under a single
// nested struct (PR-CONFIG-SPLIT, July 2026). The acquisition_mode +
// auto_download separation makes the two policies orthogonal: an
// operator can choose authorized_api for search while keeping
// auto_download.enabled=false for manual curation.
type ArtlistConfig struct {
	// AcquisitionMode is one of "" (manual_import per Normalize) or
	// "authorized_api". The loader default is authorized_api.
	AcquisitionMode string `yaml:"acquisition_mode"`
	// AutoDownload controls whether successful searches trigger
	// automatic downloads. Operators that want manual curation set
	// AutoDownload.Enabled=false; the resolver returns
	// ErrAutomaticDownloadsDisabled.
	AutoDownload ArtlistAutoDownload `yaml:"auto_download"`
}

// ArtlistAutoDownload is the per-account automatic-download policy.
type ArtlistAutoDownload struct {
	// Enabled gates automatic downloads. Default: false (safe local).
	Enabled bool `yaml:"enabled"`
	// DailyLimit caps the per-account per-day download count. 0 disables.
	DailyLimit int `yaml:"daily_limit"`
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
