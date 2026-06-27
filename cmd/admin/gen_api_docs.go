package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	pkgmw "github.com/Marcuss-ops/PipelineGen/pkg/middleware"
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
	}

	appDeps, err := app.WireServices(cfg, log, "test")
	if err != nil {
		return fmt.Errorf("wire services: %w", err)
	}
	defer appDeps.Lifecycle.Stop(context.Background())

	// PG-006.1 (June 2026): inline genDocsSecurityAdapter was deleted —
	// cfg.Security is now snapshotted into the canonical pkg/middleware
	// leaf struct (TokenSecurityAdapter) directly. Enable carries
	// cfg.Security.EnableAuth (preserves the pre-PG-006.1
	// genDocsSecurityAdapter.EnableAuth() semantics; round-2 fix
	// makes EnableAuth a passthrough, not an Admin-content derivation).
	// The rate-limit + feature-flags inline adapters remain in this
	// file (out of scope for PG-006.1; candidate for a separate
	// consolidation).
	authAdapter := &pkgmw.TokenSecurityAdapter{
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
	router.SetRegistry(appDeps.Registry)
	engine := router.Setup()

	routes := engine.Routes()
	md := generateMarkdown(routes)

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

var routeDescriptions = map[string]string{
	"/health":                       "Unified health check (?deep=true for component checks)",
	"/api/internal/slug":            "Generate URL slug from text",
	"/api/artlist/run":              "Start Artlist pipeline for a term",
	"/api/artlist/run-smart":        "Start Artlist pipeline (smart mode)",
	"/api/artlist/search":           "Search Artlist catalog",
	"/api/artlist/stats":            "Get Artlist statistics",
	"/api/jobs":                     "List jobs or enqueue new job",
	"/api/jobs/:id":                 "Get job by ID",
	"/api/jobs/:id/cancel":          "Cancel a job",
	"/api/jobs/:id/retry":           "Retry a failed job",
	"/api/jobs/:id/full":            "Get full job details",
	"/api/clips/process":            "Download and process clips",
	"/api/clips/info":               "Get YouTube video metadata",
	"/api/clips/search":             "Search and rank YouTube videos by topic",
	"/api/media/voiceover/generate": "Generate voiceover",
	"/api/media/voiceover/batch":    "Batch generate voiceovers",
	"/api/voiceover/sync":           "Sync voiceovers from Drive",
	"/api/scripts":                  "List scripts",
	"/api/scripts/:id":              "Get script by ID",
	"/api/scripts/:id/delete":       "Delete script",
	"/api/images/search":            "Search images",
	"/api/images/sync":              "Sync images",
	"/api/media/manifest/export":    "Export media manifest",
	"/api/media/:source/folders":    "List media folders",
	"/api/media/:source/clips":      "List clips",
	"/api/assets/search":            "Search assets",
	"/api/assets/stats":             "Get asset statistics",
	"/api/scraper/search":           "Search using scraper",
	"/api/system/doctor":            "System diagnostics",
}

func generateMarkdown(routes []gin.RouteInfo) string {
	var sb strings.Builder

	sb.WriteString("# PipelineGen API Documentation (Auto-Generated)\n\n")
	sb.WriteString("**Status:** GENERATED - Auto-generated from live router.\n")
	sb.WriteString("**Base URL:** `http://127.0.0.1:8080`\n\n")

	groups := make(map[string][]gin.RouteInfo)
	for _, r := range routes {
		group := extractGroup(r.Path)
		groups[group] = append(groups[group], r)
	}

	for group, rt := range groups {
		sb.WriteString(fmt.Sprintf("## %s\n\n", group))
		sb.WriteString("| Method | Path | Description |\n")
		sb.WriteString("|--------|------|-------------|\n")
		for _, r := range rt {
			desc := getDescription(r.Path, r.Method)
			sb.WriteString(fmt.Sprintf("| %s | `%s` | %s |\n", r.Method, r.Path, desc))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func extractGroup(path string) string {
	parts := strings.SplitN(path, "/", 4)
	if len(parts) >= 3 {
		return "/" + parts[1] + "/" + parts[2]
	}
	return "/"
}

func getDescription(path, method string) string {
	if desc, ok := routeDescriptions[path]; ok {
		return desc
	}
	for routePattern, desc := range routeDescriptions {
		if matchRoutePattern(routePattern, path) {
			return desc
		}
	}
	return "endpoint"
}

func matchRoutePattern(pattern, path string) bool {
	patternParts := strings.Split(pattern, "/")
	pathParts := strings.Split(path, "/")
	if len(patternParts) != len(pathParts) {
		return false
	}
	for i, pp := range patternParts {
		if strings.HasPrefix(pp, ":") {
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
// deleted — the canonical concrete is pkg/middleware.TokenSecurityAdapter
// (a leaf struct reachable from internal/api, cmd/admin, and internal/app
// without crossing layering boundaries); cfg.Security is snapshot-fed
// into the canonical at the call-site. Only the rate-limit and
// feature-flags inline adapters remain below (their canonical equivalents
// are NOT yet tracked under pkg/middleware; a separate consolidation would
// promote them — out of scope for PG-006.1).

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
