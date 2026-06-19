package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
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
	}

	appDeps, err := app.WireServices(cfg, log, "test")
	if err != nil {
		return fmt.Errorf("wire services: %w", err)
	}
	defer appDeps.Cleanup()

	router := api.NewRouter(cfg)
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
	"/health":                             "Health check",
	"/api/health":                         "Health check (API prefix)",
	"/api/internal/slug":                  "Generate URL slug from text",
	"/api/artlist/run":                    "Start Artlist pipeline for a term",
	"/api/artlist/run-smart":              "Start Artlist pipeline (smart mode)",
	"/api/artlist/search":                 "Search Artlist catalog",
	"/api/artlist/stats":                  "Get Artlist statistics",
	"/api/jobs":                           "List jobs or enqueue new job",
	"/api/jobs/:id":                       "Get job by ID",
	"/api/jobs/:id/cancel":                "Cancel a job",
	"/api/jobs/:id/retry":                 "Retry a failed job",
	"/api/jobs/:id/full":                  "Get full job details",
	"/api/clips/process":                  "Download and process clips",
	"/api/clips/info":                     "Get YouTube video metadata",
	"/api/clips/search":                   "Search and rank YouTube videos by topic",
	"/api/media/voiceover/generate":       "Generate voiceover",
	"/api/media/voiceover/batch":          "Batch generate voiceovers",
	"/api/voiceover/sync":                 "Sync voiceovers from Drive",
	"/api/scripts":                        "List scripts",
	"/api/scripts/:id":                    "Get script by ID",
	"/api/scripts/:id/delete":             "Delete script",
	"/api/images/search":                  "Search images",
	"/api/images/sync":                    "Sync images",
	"/api/media/manifest/export":          "Export media manifest",
	"/api/media/:source/folders":          "List media folders",
	"/api/media/:source/clips":            "List clips",
	"/api/assets/search":                  "Search assets",
	"/api/assets/stats":                   "Get asset statistics",
	"/api/scraper/search":                 "Search using scraper",
	"/api/system/doctor":                  "System diagnostics",
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
