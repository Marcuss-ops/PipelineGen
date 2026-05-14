// Command gen-api-docs generates API documentation from the live router.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/internal/api"
	"velox/go-master/internal/bootstrap"
	"velox/go-master/pkg/config"
)

func main() {
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
			WorkflowEnabled:    false,
		},
		Storage: config.StorageConfig{
			DataDir: "/tmp/test-data",
		},
	}

	appDeps, err := bootstrap.WireServices(cfg, log, "test")
	if err != nil {
		log.Fatal("failed to wire services", zap.Error(err))
	}
	defer appDeps.Cleanup()

	router := api.NewRouter(cfg)
	router.SetRegistry(appDeps.Registry)
	router.SetContext(nil)
	engine := router.Setup()

	routes := engine.Routes()

	md := generateMarkdown(routes)

	outputPath := "docs/api/ACTIVE_API_GENERATED.md"
	if len(os.Args) > 1 {
		outputPath = os.Args[1]
	}

	if err := os.WriteFile(outputPath, []byte(md), 0644); err != nil {
		log.Fatal("failed to write output file", zap.Error(err))
	}

	fmt.Printf("API documentation generated: %s\n", outputPath)
}

// routeDescriptions maps route paths to human-readable descriptions
var routeDescriptions = map[string]string{
	"/health":                                            "Health check",
	"/api/health":                                        "Health check (API prefix)",
	"/api/internal/slug":                                 "Generate URL slug from text",
	"/api/catalog/folders":                               "Search folders in catalog",
	"/api/artlist/run":                                   "Start Artlist pipeline for a term",
	"/api/artlist/run-smart":                             "Start Artlist pipeline (smart mode)",
	"/api/artlist/runs/:run_id":                          "Get run status by ID",
	"/api/artlist/diagnostics":                           "Check system diagnostics",
	"/api/artlist/search":                                "Search Artlist catalog",
	"/api/artlist/search/live":                           "Perform live Artlist search",
	"/api/artlist/stats":                                 "Get Artlist statistics",
	"/api/artlist/sync-catalogs":                         "Sync catalogs",
	"/api/artlist/clips/:id/status":                      "Get clip status",
	"/api/artlist/clips/:id/download":                    "Download clip",
	"/api/artlist/clips/:id/upload-drive":                "Upload clip to Drive",
	"/api/artlist/clips/process":                         "Process clip",
	"/api/artlist/sync-drive-folder":                     "Sync Drive folder",
	"/api/artlist/import-scraper-db":                     "Import scraper database",
	"/api/jobs":                                          "List jobs or enqueue new job",
	"/api/jobs/:id":                                      "Get job by ID",
	"/api/jobs/:id/cancel":                               "Cancel a job",
	"/api/jobs/:id/retry":                                "Retry a failed job",
	"/api/jobs/:id/events":                               "Get job events",
	"/api/jobs/:id/full":                                 "Get full job details",
	"/api/jobs/:id/action":                               "Perform action on job",
	"/api/youtube-clips/extract":                         "Extract YouTube clip",
	"/api/youtube-clips/folders":                         "List folders",
	"/api/youtube-clips/folders/:id":                     "Get folder details",
	"/api/youtube-clips/folders/:id/clips":               "Get folder clips",
	"/api/youtube-clips/folders/search":                  "Search folders",
	"/api/voiceover/generate":                            "Generate voiceover",
	"/api/voiceover/batch":                               "Batch generate voiceovers",
	"/api/voiceover/sync":                                "Sync voiceovers from Drive",
	"/api/script-docs/generate":                          "Generate script",
	"/api/script-docs/preview":                           "Preview script generation",
	"/api/script-docs/modes":                             "Get script generation modes",
	"/api/script-docs/association-candidates":            "Get association candidates",
	"/api/scripts":                                       "List scripts",
	"/api/scripts/:id":                                   "Get script by ID",
	"/api/scripts/:id/delete":                            "Delete script",
	"/api/images/search":                                 "Search images",
	"/api/images/sync":                                   "Sync images",
	"/api/media/manifest/export":                         "Export media manifest",
	"/api/media/:source/folders":                         "List media folders",
	"/api/media/:source/folders/:id/status":              "Get folder status",
	"/api/media/:source/folders/:id/regenerate-manifest": "Regenerate folder manifest",
	"/api/media/:source/folders/:id/trash":               "Trash folder",
	"/api/media/:source/folders/:id/delete":              "Delete folder",
	"/api/media/:source/clips":                           "List clips",
	"/api/media/:source/clips/:id/reupload":              "Reupload clip",
	"/api/media/:source/clips/:id/reprocess":             "Reprocess clip",
	"/api/media/:source/clips/:id/status":                "Get clip status",
	"/api/media/:source/clips/:id/verify":                "Verify clip",
	"/api/media/:source/clips/:id/trash":                 "Trash clip",
	"/api/media/:source/clips/:id/delete":                "Delete clip",
	"/api/media/:source/cleanup-orphans":                 "Cleanup orphaned files",
	"/api/media/:source/reconcile":                       "Reconcile media",
	"/api/media/:source/drive-file/trash":                "Trash Drive file",
	"/api/media/:source/drive-file/delete":               "Delete Drive file",
	"/api/assets/search":                                 "Search assets",
	"/api/assets/stats":                                  "Get asset statistics",
	"/api/scraper/search":                                "Search using scraper",
	"/api/system/doctor":                                 "System diagnostics",
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
	// Try exact match first
	if desc, ok := routeDescriptions[path]; ok {
		return desc
	}
	// Try matching with :param patterns
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
