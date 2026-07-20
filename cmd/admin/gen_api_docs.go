package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	middleware "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func runGenAPIDocs(args []string) error {
	log, _ := zap.NewDevelopment()
	defer log.Sync()

	cfg := &config.Config{
		Server: config.ServerConfig{
			GinMode: "test",
		},
		Security: config.SecurityConfig{
			CORSOrigins: []string{},
		},
		Features: config.FeaturesConfig{
			ArtlistEnabled:     true,
			YouTubeEnabled:     true,
			ScriptDocsEnabled:  true,
			VoiceoverEnabled:   true,
			ImagesEnabled:      true,
			ScriptClipsEnabled: true,
		},
		Storage: config.StorageConfig{
			DataDir: "/tmp/test-data",
		},
		Multilingual: config.MultilingualConfig{
			SourceLanguage: "en",
		},
		External: config.ExternalConfig{
			ArtlistScraperServerURL: "http://localhost:0",
		},
	}

	appDeps, err := app.WireServices(cfg, log, "test")
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
	rateAdapter := &genDocsRateLimitAdapter{cfg: cfg}
	featuresAdapter := &genDocsFeatureFlagsAdapter{cfg: cfg}
	routerCfg := &api.RouterConfig{
		ServerGinMode: cfg.Server.GinMode,
		DataDir:       cfg.Storage.DataDir,
		DownloadDir:   cfg.GoogleAccounting.DownloadDir,
		CORSOrigins:   cfg.Security.CORSOrigins,
		Log:           log,
		Auth:          authAdapter,
		Rate:          rateAdapter,
		Features:      featuresAdapter,
	}
	router := api.NewRouter(routerCfg)
	router.SetRegistry(appDeps.Handlers.Registry)
	engine := router.Setup()

	routes := engine.Routes()
	md, missing := generateMarkdown(routes)
	if missing > 0 {
		fmt.Printf("⚠️  %d route(s) have no description — add them to routeDescriptions in cmd/admin/gen_api_docs.go\n", missing)
	}

	outputPath := "docs/api/ACTIVE_API_GENERATED.md"
	if len(args) > 0 {
		outputPath = args[0]
	}

	if err := os.WriteFile(outputPath, []byte(md), 0644); err != nil {
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

	// ── Internal ──────────────────────────────────────────────
	"GET /api/internal/slug": "Generate URL slug from text",

	// ── Artlist ───────────────────────────────────────────────
	"POST /api/artlist/run":           "Start Artlist pipeline for a term",
	"POST /api/artlist/search":        "Search Artlist catalog (cached)",
	"POST /api/artlist/search/live":   "Search Artlist catalog (live, no cache)",
	"GET /api/artlist/stats":          "Get Artlist statistics",
	"GET /api/artlist/runs/:run_id":   "Get Artlist pipeline run status",
	"GET /api/artlist/diagnostics":    "Artlist diagnostics",
	"POST /api/artlist/sync-catalogs": "Sync Artlist catalogs to media DB",
	"POST /api/artlist/recommend":     "Get Artlist recommendations for a term",

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
	"POST /api/clips/process":    "Download and process clips",
	"GET /api/clips/info":        "Get YouTube video metadata",
	"GET /api/clips/search":      "Search and rank YouTube videos by topic",
	"POST /api/clips/search":     "Search and rank YouTube videos by topic (POST variant)",
	"GET /api/clips/stats":       "Get clips statistics",
	"GET /api/clips/diagnostics": "Clips diagnostics",

	// ── Media / Clips ─────────────────────────────────────────
	"POST /api/media/clips/ingest/ai-stock": "Ingest an AI-generated stock clip from visual analysis + Drive video",

	// ── Images ────────────────────────────────────────────────
	"GET /api/images/search":              "Search images by territory",
	"GET /api/images/retrieved/search":    "Search retrieved images",
	"GET /api/images/generated/search":    "Search generated images",
	"GET /api/images/generated/styles":    "List generated image styles",
	"POST /api/images/generated/generate": "Generate an AI image",
	"POST /api/images/batch-generate":     "Batch generate AI images asynchronously",
	"POST /api/images/sync":               "Sync images to Drive",
	"POST /api/images/upload":             "Upload an image",
	"GET /api/images/diagnostics":         "Images diagnostics",

	// ── Scripts ───────────────────────────────────────────────
	"GET /api/scripts":             "List scripts",
	"GET /api/scripts/:id":         "Get script by ID",
	"POST /api/scripts/:id/delete": "Delete script",

	// ── Script generation ─────────────────────────────────────
	"GET /api/script/jobs/:job_id":         "Get script job status",
	"GET /api/script/jobs/:job_id/full":    "Get full script job details",
	"GET /api/script/clips/search":         "Search script clips by name","POST /api/script/generate": "Generate scripts from text, clips, catalog or search sources",
	"POST /api/script/shorts/generate":     "Generate a Remotion Shorts video",
	"POST /api/script/shorts/render":       "Render a Remotion Shorts video synchronously",
	"POST /api/script/shorts/render/async": "Enqueue a Remotion Shorts render job",

	// ── Media — voiceover ────────────────────────────────────
	"POST /api/media/voiceover/generate":            "Generate voiceover",
	"POST /api/media/voiceover/generate-with-group": "Generate voiceover with style group",
	"POST /api/media/voiceover/batch":               "Batch generate voiceovers",
	"POST /api/media/voiceover/promo":               "Generate voiceover promo",
	"POST /api/media/voiceover/sync":                "Sync voiceover state",
	"GET /api/media/voiceover/groups":               "List voiceover style groups",

	// ── Media — sound effect ─────────────────────────────────
	"POST /api/media/sound_effect/generate": "Generate sound effect",

	// ── Media — general ──────────────────────────────────────
	"GET /api/media/search":                 "Search media assets",
	"GET /api/media/semantic-search":        "Semantic search across media assets",
	"GET /api/media/diagnostics":            "Media diagnostics",
	"GET /api/media/index-health":           "Media index health check",
	"POST /api/media/search/advanced":       "Advanced media search",
	"POST /api/media/sync-drive-folder":     "Sync a Drive folder into media index",
	"POST /api/media/recommend":             "Get media recommendations",
	"POST /api/media/enrich":                "Enrich a media asset with AI metadata",
	"POST /api/media/enrich/batch":          "Batch enrich media assets",
	"POST /api/media/local-to-drive":        "Upload local media to Drive",
	"POST /api/media/qdrant/cleanup":        "Clean up stale Qdrant points",
	"POST /api/media/upload-video":          "Upload video clip",
	"POST /api/media/register-from-youtube": "Register asset from YouTube URL",
	"POST /api/media/register-batch":        "Batch register assets",
	"POST /api/media/manifest/export":       "Export media manifest",

	// ── Media — Drive operations ─────────────────────────────
	"POST /api/media/drive/move-files":     "Move files within Drive",
	"POST /api/media/drive/create-folders": "Create Drive folders",

	// ── Media — source-scoped (YouTube, stock, artlist) ──────
	"GET /api/media/:source/folders":               "List media folders by source",
	"GET /api/media/:source/folders/:id":           "Get media folder by ID",
	"GET /api/media/:source/folders/:id/children":  "List child folders",
	"GET /api/media/:source/clips":                 "List clips by source",
	"GET /api/media/:source/clips/:id":             "Get clip by ID",
	"GET /api/media/:source/tree":                  "Get folder tree by source",
	"GET /api/media/:source/breadcrumb":            "Get breadcrumb path to folder",
	"POST /api/media/:source/clips":                "Create clip under source",
	"POST /api/media/:source/clips/:id/delete":     "Delete clip",
	"POST /api/media/:source/clips/:id/download":   "Download clip",
	"POST /api/media/:source/clips/:id/duplicates": "Find duplicate clips",
	"POST /api/media/:source/clips/:id/reupload":   "Re-upload clip to Drive",
	"POST /api/media/:source/clips/:id/reprocess":  "Re-process clip",
	"POST /api/media/:source/clips/:id/reindex":    "Re-index clip in Qdrant",
	"POST /api/media/:source/clips/:id/status":     "Get clip processing status",
	"POST /api/media/:source/clips/:id/verify":     "Verify clip integrity",
	"POST /api/media/:source/clips/:id/trash":      "Trash clip",
	"PATCH /api/media/:source/clips/:id":           "Update clip metadata",
	"POST /api/media/:source/cleanup":              "Clean up source artifacts",
	"POST /api/media/:source/folders/:id/manifest": "Get folder manifest",
	"POST /api/media/:source/folders/:id/trash":    "Trash folder",
	"POST /api/media/:source/folders/:id/delete":   "Delete folder",
	"POST /api/media/:source/bulk/tags/add":        "Bulk-add tags",
	"POST /api/media/:source/bulk/tags/remove":     "Bulk-remove tags",
	"POST /api/media/:source/reconcile":            "Reconcile source metadata",

	// ── Assets ───────────────────────────────────────────────
	"GET /api/assets/search": "Search assets",
	"GET /api/assets/stats":  "Get asset statistics",

	// ── Scraper ──────────────────────────────────────────────
	"POST /api/scraper/search": "Search using scraper",

	// ── System ───────────────────────────────────────────────
	"GET /api/system/doctor": "System diagnostics",

	// ── Search queries ───────────────────────────────────────
	"GET /api/search-queries":             "List search queries",
	"POST /api/search-queries":            "Create a search query",
	"GET /api/search-queries/active":      "List active search queries",
	"GET /api/search-queries/:id":         "Get search query by ID",
	"DELETE /api/search-queries/:id":      "Delete search query",
	"GET /api/search-queries/:id/results": "Get search query results",

	// ── Channels ─────────────────────────────────────────────
	"GET /api/channels":              "List channels",
	"POST /api/channels":             "Create channel",
	"GET /api/channels/categories":   "List channel categories",
	"GET /api/channels/:id":          "Get channel by ID",
	"DELETE /api/channels/:id":       "Delete channel",
	"POST /api/channels/bulk-upsert": "Bulk upsert channels",

	// ── Drive ────────────────────────────────────────────────
	"POST /api/drive/reconcile":     "Reconcile Drive metadata",
	"POST /api/drive/resolve-by-id": "Resolve Drive folder by ID",
	"POST /api/drive/cleanup":       "Clean up empty Drive folders",
	"POST /api/drive/folders":       "List Drive folders",
	"POST /api/drive/move":          "Move Drive files",

	// ── Fullimages ───────────────────────────────────────────
	//
	// PR-IMAGES-FULLIMAGES-IMAGE-ONLY (2026-07-10, CUTOVER phase):
	// the pre-CUTOVER route was /api/fullimages/video/generate; the
	// route is RENAMED to /api/fullimages/image/generate. Wire-shape
	// breaking change per Option B.
	"POST /api/fullimages/image/generate": "Generate one image per section (fullimages image-only pipeline)",

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

// ── Typed-port adapters (PG-006 bridge: cmd/admin → api/middleware) ────────
//
// PG-006.1 (June 2026): the genDocsSecurityAdapter inline struct was
// deleted — the canonical concrete is
// internal/api/middleware.TokenSecurityAdapter (re-located from
// pkg/middleware round-2; pkg/ is leaf-only and HTTP-middleware
// concrete adapters cannot legitimately live there). The struct is
// reachable from internal/api, cmd/admin, and internal/app without
// crossing layering boundaries; cfg.Security is snapshot-fed into
// the canonical at the call-site. Only the rate-limit and
// feature-flags inline adapters remain below (their canonical
// equivalents are NOT yet tracked under internal/api/middleware;
// a separate consolidation would promote them — out of scope
// for PG-006.1).

type genDocsRateLimitAdapter struct{ cfg *config.Config }

func (a *genDocsRateLimitAdapter) RateLimitEnabled() bool { return a.cfg.Security.RateLimitEnabled }
func (a *genDocsRateLimitAdapter) RateLimitRequests() int { return a.cfg.Security.RateLimitRequests }

type genDocsFeatureFlagsAdapter struct{ cfg *config.Config }

func (a *genDocsFeatureFlagsAdapter) ArtlistEnabled() bool { return a.cfg.Features.ArtlistEnabled }
func (a *genDocsFeatureFlagsAdapter) ScriptDocsEnabled() bool {
	return a.cfg.Features.ScriptDocsEnabled
}
func (a *genDocsFeatureFlagsAdapter) ScriptClipsEnabled() bool {
	return a.cfg.Features.ScriptClipsEnabled
}

// generateMarkdown is also callable from golden-file tests via
// the gen_api_docs_test.go file in the same package.
