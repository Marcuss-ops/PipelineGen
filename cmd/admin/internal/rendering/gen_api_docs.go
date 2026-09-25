package rendering

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	middleware "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver/middleware"
)

func RunGenAPIDocs(args []string) error {
	log, _ := zap.NewDevelopment()
	defer log.Sync()

	// Media PostgreSQL handle for this run (see the feature-gate note
	// below): read from the canonical env var so the generator composes
	// the same way the server does, without parsing config.yaml.
	mediaDSN := strings.TrimSpace(os.Getenv("PIPELINEGEN_MEDIA_POSTGRES_DSN"))
	mediaPG := mediaDSN != ""

	cfg := &config.Config{
		Server: config.ServerConfig{
			GinMode: "test",
		},
		Security: config.SecurityConfig{
			CORSOrigins: []string{},
		},
		// PRE-EXISTING GENERATOR GAP, resolved dynamically: Artlist AND
		// clip.render both fail closed on a nil AssetTxFinalizer committer
		// (godlike/07), and that committer exists only with a media
		// PostgreSQL handle — which a DB-less docs snapshot cannot open.
		// Boot used to abort at registerArtlist before any route was
		// collected, so the manifest could not be regenerated at all.
		// Both features are therefore mounted ONLY when the canonical
		// PIPELINEGEN_MEDIA_POSTGRES_DSN env var is present (any host
		// running the server has it); without it the generator produces
		// the rest of the manifest and their description keys live in
		// routeDescriptionsGated ("absence is gating, not drift").
		Features: config.FeaturesConfig{
			ArtlistEnabled:     mediaPG,
			YouTubeEnabled:     true,
			VoiceoverEnabled:   true,
			ImagesEnabled:      true,
			ScriptClipsEnabled: true,
			ClipRenderEnabled:  mediaPG,
			// StockPipelineEnabled: the /api/stock-pipeline/* and
			// /api/stock-batches/* routes are mounted live with the
			// production default (stock_pipeline_enabled: true) and are
			// therefore part of the committed manifest. They ride the same
			// media-PostgreSQL gate as Artlist/ClipRender because
			// WireStockPipeline needs the media committer; without the DSN
			// their description keys live in routeDescriptionsGated
			// ("absence is gating, not drift"), WITH the DSN they are
			// mounted and the keys match a real route again.
			StockPipelineEnabled: mediaPG,
		},
		// Drive credentials: the docs snapshot must be able to mount every
		// route the PRODUCTION default enables, and WireStockPipeline
		// requires root.Drive.Admin — which exists only when the Drive SDK
		// handle could be built. Leaving Paths empty (pre-fix) made
		// GetCredentialsPath() return "", the Drive client fail, Admin stay
		// nil, and the stock-pipeline routes vanish from the committed
		// manifest while the live server served them (stock_pipeline_enabled
		// defaults to true) — drift, not gating. Pinning the canonical
		// repo-relative names keeps this a construction input: a host WITH
		// credentials generates the production manifest, a credential-less
		// host still boots (soft mode) and the stock keys fall back to
		// routeDescriptionsGated.
		Paths: config.PathsConfig{
			CredentialsFile: "credentials.json",
			TokenFile:       "token.json",
		},
		Storage: config.StorageConfig{
			DataDir: "/tmp/test-data",
		},
		Multilingual: config.MultilingualConfig{
			SourceLanguage: "en",
		},
		Linguistics: config.LinguisticsConfig{
			LexiconRoot:       "config/lexicons",
			RequiredLanguages: []string{"en", "it"},
		},
		External: config.ExternalConfig{
			ArtlistScraperServerURL: "http://localhost:0",
			// BuildClipRenderRuntime fail-closes when the RenderingGen queue
			// URL is empty. Docs runs never submit a render job (the queue
			// client is constructed, never dialed at boot), so the canonical
			// config.yaml value is pinned here; like the ClipIndexer URL
			// above it is a construction input, not an I/O dependency.
			RenderingGenQueueURL: "http://127.0.0.1:8081",
		},
		MediaPostgreSQL: config.PostgreSQLMediaConfig{
			Enabled:                mediaPG,
			DSN:                    mediaDSN,
			MaxOpenConnections:     5,
			MaxIdleConnections:     2,
			ConnMaxLifetimeSeconds: 60,
			EmbeddingModel:         "intfloat/multilingual-e5-base",
		},
		// BuildOutboxBundle fail-closes when media PG is on and the E5
		// embedding sidecar URL is missing. Docs runs point at the
		// canonical sidecar address from config.yaml but never call it:
		// the docs-only media DB (see the DSN above) has an empty outbox.
		ClipIndexer: config.ClipIndexerConfig{
			Enabled:               true,
			ServerURL:             "http://127.0.0.1:8001",
			MaxConcurrentIndexing: 1,
			EmbedTimeoutSeconds:   60,
		},
	}

	appDeps, err := wiring.WireServices(cfg, log, "test")
	if err != nil {
		return fmt.Errorf("wire services: %w", err)
	}
	defer appDeps.Runtime.Lifecycle.Stop(context.Background())

	// PG-006.1 (June 2026): inline genDocsSecurityAdapter was deleted —
	// cfg.Security is now snapshotted into the canonical
	// internal/api/middleware.TokenSecurityAdapter concrete adapter
	// directly. Enable carries cfg.Security.EnableAuth (preserves the
	// pre-PG-006.1 genDocsSecurityAdapter.EnableAuth() semantics;
	// round-2 fix makes EnableAuth a passthrough, not an
	// Admin-content derivation). The rate-limit + feature-flags inline
	// adapters remain in this file (out of scope for PG-006.1;
	// candidate for a separate consolidation).
	authAdapter := &middleware.TokenSecurityAdapter{
		Enable: cfg.Security.EnableAuth,
		Admin:  cfg.Security.AdminToken,
		Worker: cfg.Security.WorkerToken,
	}
	// CLEANUP (September 2026): genDocsRateLimitAdapter +
	// genDocsFeatureFlagsAdapter inline structs were removed. The
	// canonical concretes are middleware.RateLimitAdapter +
	// middleware.FeatureFlagsAdapter — snapshot-literal wiring like
	// TokenSecurityAdapter above (godlike/06 one owner per fact).
	rateAdapter := &middleware.RateLimitAdapter{
		Enabled:  cfg.Security.RateLimitEnabled,
		Requests: cfg.Security.RateLimitRequests,
	}
	featuresAdapter := &middleware.FeatureFlagsAdapter{
		Artlist:     cfg.Features.ArtlistEnabled,
		ScriptClips: cfg.Features.ScriptClipsEnabled,
	}
	routerCfg := &httpserver.RouterConfig{
		ServerGinMode: cfg.Server.GinMode,
		// The production server binds an explicit loopback address, which
		// is what PR-METRICS-FAILCLOSED keys the /metrics mount on
		// (token-less dev mount requires a loopback listener). Without
		// this flag the docs snapshot would silently drop /metrics from
		// the manifest while the live server serves it — drift, not
		// gating. METRICS_AUTH_TOKEN (if set) still wins in every mode.
		ServerLoopbackOnly: true,
		DataDir:            cfg.Storage.DataDir,
		DownloadDir:        cfg.GoogleAccounting.DownloadDir,
		CORSOrigins:        cfg.Security.CORSOrigins,
		Log:                log,
		Auth:               authAdapter,
		Rate:               rateAdapter,
		Features:           featuresAdapter,
	}
	router := httpserver.NewRouter(routerCfg)
	router.SetRegistry(appDeps.Handlers.Registry)
	engine := router.Setup()

	routes := engine.Routes()
	if stale := staleDescriptionKeys(routeDescriptions, routes, routeDescriptionsGated); len(stale) > 0 {
		return fmt.Errorf("%d routeDescriptions key(s) have no matching registered route — remove or retarget them so the docs never drift from the live router:\n  - %s", len(stale), strings.Join(stale, "\n  - "))
	}
	md, missing := generateMarkdown(routes)
	if missing > 0 {
		fmt.Printf("⚠️  %d route(s) have no description — add them to routeDescriptions in cmd/admin/gen_api_docs.go\n", missing)
	}

	outputPath := "docs/api/ACTIVE_API_GENERATED.md"
	if len(args) > 0 {
		outputPath = args[0]
	}

	markdown := []byte(strings.TrimRight(md, "\n") + "\n")
	if outputPath == "docs/api/ACTIVE_API_GENERATED.md" {
		if err := publishRuntimeRouteArtifacts(outputPath, "architecture/routes.yaml", markdown, routes); err != nil {
			return fmt.Errorf("publish route artifacts: %w", err)
		}
	} else if err := writeAtomicFile(outputPath, markdown, 0644); err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	fmt.Printf("API documentation generated: %s\n", outputPath)
	return nil
}

// descMissing is the sentinel rendered when a route has no entry in
// routeDescriptions.
const descMissing = "\u26a0\ufe0f MISSING DESCRIPTION"

// routeDescriptions maps "METHOD path" (or "METHOD path/with/:param") to
// a human-readable description.  The key format is exactly
// METHOD + SPACE + PATH — one space, no alignment padding — because
// getDescription splits on the first space to separate method from pattern.
// Pattern segments (e.g. `:id`) are matched via matchRoutePattern.
//
// Every public route MUST have an entry here.  Routes without a
// description render as descMissing in the generated output and the
// generator prints a count to stderr.
var routeDescriptions = map[string]string{
	// ── Health / metrics / readiness ──────────────────────────
	"GET /health":  "Unified health check (?deep=true for component checks)",
	"GET /ready":   "Readiness probe",
	"GET /metrics": "Prometheus metrics endpoint",

	// ── Root ──────────────────────────────────────────────────
	"GET /": "API root (redirects or 404)",

	// ── Artlist ───────────────────────────────────────────────
	"POST /api/artlist/run":           "Start Artlist pipeline for a term",
	"POST /api/artlist/search":        "Search Artlist catalog (cached)",
	"GET /api/artlist/search/live":    "Search Artlist catalog (live, no cache)",
	"GET /api/artlist/stats":          "Get Artlist statistics",
	"GET /api/artlist/runs/:run_id":   "Get Artlist pipeline run status",
	"GET /api/artlist/diagnostics":    "Artlist diagnostics",
	"POST /api/artlist/sync-catalogs": "Sync Artlist catalogs to media DB",
	"POST /api/artlist/recommend":     "Get Artlist recommendations for a term",

	// ── Stock pipeline / stock batches ───────────────────────
	// StockPipelineEnabled (same media-PostgreSQL gate as Artlist above;
	// keys mirrored in routeDescriptionsGated).
	"POST /api/stock-pipeline/run":            "Run the stock pipeline (search_queries / direct_urls / drive_urls / clips)",
	"POST /api/stock-pipeline/search-and-run": "Search-and-run the stock pipeline (queries: [{q, limit}])",
	"POST /api/stock-batches/run":             "Run a stock batch (source_url + destination + sampling + groups)",
	"GET /api/stock-batches/:id":              "Get stock batch status",
	"POST /api/media/import":                  "Ingest media (image / voiceover / clip / stock) by request kind",

	// ── Jobs ──────────────────────────────────────────────────
	"GET /api/jobs":             "List jobs",
	"POST /api/jobs":            "Enqueue a new job",
	"GET /api/jobs/stats":       "Get job statistics",
	"GET /api/jobs/:id":         "Get job by ID",
	"GET /api/jobs/:id/full":    "Get full job details",
	"GET /api/jobs/:id/events":  "Get job event stream",
	"POST /api/jobs/:id/cancel": "Cancel a job",
	"POST /api/jobs/:id/retry":  "Retry a failed job",

	// ── Clips ─────────────────────────────────────────────────
	"POST /api/clips/process":      "Download and process clips",
	"POST /api/clips/render":       "Render a canonical clip into a VeloxEditing-compatible derived clip (background/watermark/subtitles, async clip.render job)",
	"POST /api/clips/render/batch": "Render up to 50 canonical clips with fingerprint deduplication and async clip.render jobs",
	"GET /api/clips/info":          "Get YouTube video metadata",
	"GET /api/clips/search":        "Search and rank YouTube videos by topic",
	"GET /api/clips/exists":        "Pre-extraction dedup probe: answers {exists, clip_id} for a YouTube URL so callers skip /api/clips/process for known candidates",
	"GET /api/clips/transcript":    "Transcript-as-a-service: readable text + per-cue timings for a YouTube URL without downloading the video (yt-dlp --skip-download + canonical VTT parser)",
	"GET /api/clips/diagnostics":   "Clips diagnostics",

	// ── Media / Clips ─────────────────────────────────────────
	"POST /api/media/clips/ingest/ai-stock": "Ingest an AI-generated stock clip from visual analysis + Drive video",

	// ── Images ────────────────────────────────────────────────
	"GET /api/images/search":              "Search images by territory",
	"GET /api/images/retrieved/search":    "Search retrieved images",
	"GET /api/images/generated/search":    "Search generated images",
	"GET /api/images/generated/styles":    "List generated image styles",
	"POST /api/images/generated/generate": "Generate an AI image",
	"POST /api/images/batch-generate":     "Batch generate AI images asynchronously (items or mode=sections)",
	"POST /api/images/sync":               "Sync images to Drive",
	"POST /api/images/upload":             "Upload an image",
	"GET /api/images/diagnostics":         "Images diagnostics",

	// ── Scripts ───────────────────────────────────────────────
	"GET /api/scripts":     "List scripts",
	"GET /api/scripts/:id": "Get script by ID",

	// ── Script generation ─────────────────────────────────────
	"GET /api/script/jobs/:id":     "Get script job status",
	"GET /api/script/clips/search": "Search script clips by name",
	"POST /api/script/generate":    "Generate scripts from text, clips, catalog or search sources",

	// ── Media — voiceover ────────────────────────────────────
	"POST /api/media/voiceover/generate": "Generate voiceover",

	// ── Media — sound effect ─────────────────────────────────
	"POST /api/media/sound_effect/generate": "Generate sound effect",

	// ── Media — general ──────────────────────────────────────
	"POST /api/media/search":                "Search media assets",
	"GET /api/media/diagnostics":            "Media diagnostics",
	"GET /api/media/index-health":           "Media index health check",
	"POST /api/media/sync":                  "Sync a Drive folder into media index",
	"POST /api/media/clips/enrich":          "Enrich a media asset with AI metadata",
	"POST /api/media/qdrant/cleanup":        "Clean up stale Qdrant points",
	"POST /api/media/clips/upload-video":    "Upload video clip",
	"POST /api/media/register-from-youtube": "Register asset from YouTube URL",
	"POST /api/media/register-batch":        "Batch register assets",

	// ── Media — source-scoped (YouTube, stock, artlist) ──────
	"GET /api/media/clips/:source/folders":               "List media folders by source",
	"GET /api/media/clips/:source/folders/:id":           "Get media folder by ID",
	"GET /api/media/clips/:source/folders/:id/children":  "List child folders",
	"GET /api/media/clips/:source/clips":                 "List clips by source",
	"GET /api/media/clips/:source/clips/:id":             "Get clip by ID",
	"GET /api/media/clips/:source/tree":                  "Get folder tree by source",
	"GET /api/media/clips/:source/breadcrumb":            "Get breadcrumb path to folder",
	"POST /api/media/clips/:source/clips":                "Create clip under source",
	"POST /api/media/clips/:source/clips/:id/download":   "Download clip",
	"POST /api/media/clips/:source/clips/:id/duplicates": "Find duplicate clips",
	"POST /api/media/clips/:source/clips/:id/reupload":   "Re-upload clip to Drive",
	"POST /api/media/clips/:source/clips/:id/reprocess":  "Re-process clip",
	"POST /api/media/clips/:source/clips/:id/status":     "Get clip processing status",
	"POST /api/media/clips/:source/clips/:id/verify":     "Verify clip integrity",
	"DELETE /api/media/clips/:source/clips/:id":          "Trash clip",
	"PATCH /api/media/clips/:source/clips/:id":           "Update clip metadata",
	"POST /api/media/clips/:source/cleanup":              "Clean up source artifacts",
	"POST /api/media/clips/:source/folders/:id/manifest": "Get folder manifest",
	"DELETE /api/media/clips/:source/folders/:id":        "Trash folder",
	"POST /api/media/clips/:source/reconcile":            "Reconcile source metadata",

	// ── System ───────────────────────────────────────────────
	"GET /api/system/doctor": "System diagnostics",

	// ── Drive ────────────────────────────────────────────────
	"POST /api/drive/reconcile":     "Reconcile Drive metadata",
	"POST /api/drive/resolve-by-id": "Resolve Drive folder by ID",
	"POST /api/drive/cleanup":       "Clean up empty Drive folders",
	"GET /api/drive/files":          "List a Drive folder's files",
	"POST /api/drive/folders":       "Create Drive folders under a parent (idempotent get-or-create)",
	"POST /api/drive/move":          "Move Drive files",
	"POST /api/drive/rename":        "Rename a Drive file or folder",
	"POST /api/drive/canary-upload": "Upload a readiness canary file into a Drive folder (folder_id or folder_alias)",

	// ── Fullimages ───────────────────────────────────────────
	// POST /api/fullimages/image/generate was retired and merged into
	// POST /api/images/batch-generate mode=sections (IMAGES-LEGACY-CLEANUP,
	// August 2026).

	// ── Static file serving ──────────────────────────────────
	"GET /assets/*filepath":                   "Serve static assets from data dir",
	"HEAD /assets/*filepath":                  "HEAD check for static assets",
	"GET /media/google-accounting/*filepath":  "Serve Google Accounting media files",
	"HEAD /media/google-accounting/*filepath": "HEAD check for Google Accounting media",
}

func generateMarkdown(routes []gin.RouteInfo) (string, int) {
	var sb strings.Builder
	missing := 0

	sb.WriteString("# PipelineGen API Documentation (Auto-Generated)\n\n")
	sb.WriteString("**Status:** GENERATED — auto-generated from live router.\n")
	sb.WriteString("**Base URL:** `{BASE_URL}` (overridable via `VELOX_PORT` env var)\n\n")

	groups := make(map[string][]gin.RouteInfo)
	for _, r := range routes {
		group := extractGroup(r.Path)
		groups[group] = append(groups[group], r)
	}

	// Sort group names for deterministic output.
	groupNames := make([]string, 0, len(groups))
	for g := range groups {
		groupNames = append(groupNames, g)
	}
	sort.Strings(groupNames)

	for _, group := range groupNames {
		rt := groups[group]
		// Sort routes within group by (Method, Path) for deterministic output.
		sort.Slice(rt, func(i, j int) bool {
			if rt[i].Method != rt[j].Method {
				return rt[i].Method < rt[j].Method
			}
			return rt[i].Path < rt[j].Path
		})

		sb.WriteString(fmt.Sprintf("## %s\n\n", group))
		sb.WriteString("| Method | Path | Description |\n")
		sb.WriteString("|--------|------|-------------|\n")
		for _, r := range rt {
			desc := getDescription(r.Path, r.Method)
			if desc == descMissing {
				missing++
			}
			sb.WriteString(fmt.Sprintf("| %s | `%s` | %s |\n", r.Method, r.Path, desc))
		}
		sb.WriteString("\n")
	}

	return sb.String(), missing
}

func extractGroup(path string) string {
	parts := strings.SplitN(path, "/", 4)
	if len(parts) >= 3 {
		return "/" + parts[1] + "/" + parts[2]
	}
	return "/"
}

// getDescription looks up a human-readable description for a route.
// It checks the METHOD+PATH exact match first, then falls back to
// pattern matching (where the map key contains `:param` segments).
// Routes without any match return the sentinel string
// "⚠️ MISSING DESCRIPTION".
func getDescription(path, method string) string {
	key := method + " " + path

	// 1. Exact match on METHOD PATH
	if desc, ok := routeDescriptions[key]; ok {
		return desc
	}

	// 2. Pattern match with method
	for routeKey, desc := range routeDescriptions {
		// routeKey is "METHOD path/pattern"
		parts := strings.SplitN(routeKey, " ", 2)
		if len(parts) != 2 {
			continue
		}
		keyMethod, keyPattern := parts[0], parts[1]
		if keyMethod != method {
			continue
		}
		if matchRoutePattern(keyPattern, path) {
			return desc
		}
	}

	return descMissing
}

func matchRoutePattern(pattern, path string) bool {
	patternParts := strings.Split(pattern, "/")
	pathParts := strings.Split(path, "/")
	if len(patternParts) != len(pathParts) {
		return false
	}
	for i, pp := range patternParts {
		if strings.HasPrefix(pp, ":") || strings.HasPrefix(pp, "*") {
			continue
		}
		if pp != pathParts[i] {
			return false
		}
	}
	return true
}

// descriptionKeyMatchesRoute reports whether a "METHOD path" description
// key matches any registered route (exact path, or :param/*wildcard pattern).
func descriptionKeyMatchesRoute(key string, routes []gin.RouteInfo) bool {
	parts := strings.SplitN(key, " ", 2)
	if len(parts) != 2 {
		return false
	}
	method, pattern := parts[0], parts[1]
	for _, r := range routes {
		if r.Method != method {
			continue
		}
		if pattern == r.Path || matchRoutePattern(pattern, r.Path) {
			return true
		}
	}
	return false
}

// routeDescriptionsGated lists description keys whose routes are live in
// production but gated behind a feature bundle (AI/DB) that the docs
// generator's minimal snapshot does not wire. They are expected to have no
// matching registered route in that snapshot, so the stale-description gate
// skips them — their absence is gating, not drift. Keep in sync: when a gated
// route is re-registered unconditionally, remove its key here so the gate
// covers it again.
var routeDescriptionsGated = map[string]bool{
	"GET /api/script/clips/search": true,
	"GET /api/script/jobs/:id":     true,
	"POST /api/script/generate":    true,
	// Artlist (see the ArtlistEnabled note in the cfg above): the routes
	// are live in production, but the docs snapshot cannot wire the
	// capability without a media PostgreSQL committer, so their absence
	// from the manifest is gating, not drift.
	"POST /api/artlist/run":           true,
	"POST /api/artlist/search":        true,
	"GET /api/artlist/search/live":    true,
	"GET /api/artlist/stats":          true,
	"GET /api/artlist/runs/:run_id":   true,
	"GET /api/artlist/diagnostics":    true,
	"POST /api/artlist/sync-catalogs": true,
	"POST /api/artlist/recommend":     true,
	// clip.render (ClipRenderEnabled, same media PostgreSQL gate as
	// Artlist above): mounted only when the docs snapshot has a media
	// SSOT handle, otherwise gating not drift.
	"POST /api/clips/render":       true,
	"POST /api/clips/render/batch": true,
	// stock-pipeline + stock-batches (StockPipelineEnabled, same
	// media PostgreSQL gate as Artlist / clip.render above): mounted only
	// when the docs snapshot has a media SSOT handle, otherwise gating
	// not drift.
	"POST /api/stock-pipeline/run":            true,
	"POST /api/stock-pipeline/search-and-run": true,
	"POST /api/stock-batches/run":             true,
	"GET /api/stock-batches/:id":              true,
	// POST /api/media/import is mounted only when the media ingest
	// pipeline could be built — which needs the Drive admin handle (the
	// ingest path resolves/creates Drive folders). Gated for the same
	// reason as stock above: absence on a credential-less host is
	// gating, not drift.
	"POST /api/media/import": true,
}

// staleDescriptionKeys returns the description keys that match no registered
// route and are not in the gated allow-list. This is the reverse direction of
// the "missing description" warning: when a route is removed or renamed, its
// description key survives and the committed docs silently rot. runGenAPIDocs
// fails closed on any such key so routeDescriptions stays a 1:1 mirror of the
// live router.
func staleDescriptionKeys(descs map[string]string, routes []gin.RouteInfo, gated map[string]bool) []string {
	var stale []string
	for key := range descs {
		if gated[key] {
			continue
		}
		if !descriptionKeyMatchesRoute(key, routes) {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	return stale
}

// ── Typed-port adapters (DELETED September 2026) ───────────────────────────
//
// genDocsRateLimitAdapter + genDocsFeatureFlagsAdapter were removed in the
// September 2026 cleanup. The canonical concretes are
// internal/platform/httpserver/middleware.RateLimitAdapter +
// middleware.FeatureFlagsAdapter — snapshot-literal wiring at the single
// construction site above (godlike/06 one owner per fact).

// generateMarkdown is also callable from golden-file tests via
// the gen_api_docs_test.go file in the same package.
