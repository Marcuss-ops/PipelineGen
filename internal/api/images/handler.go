// Package images (api/images) — handler.go is the thin route map.
// Per PR-IMG-SPLIT-2 (July 2026), every handler method lives in its
// own capability file; this file owns ONLY the struct, constructor,
// and route registration. No business logic lives here.
//
// Golden rule: generated = AI, retrieved = stock, all = aggregator.
// Each handler file documents which territory it belongs to.
package images

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/workflow"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/gin-gonic/gin"
)

// ImagesHandler is the HTTP transport for the /api/images route group.
// Fields are nil-safe at construction time; nil-tolerant handlers
// return 503 ServiceUnavailable for unwired dependencies.
type ImagesHandler struct {
	service   *imgservice.Service
	ingestSvc *ingest.Service
	jobsSvc   jobs.Service
}

// NewImagesHandler constructs the handler with the application-layer
// services it delegates to.
func NewImagesHandler(service *imgservice.Service, ingestSvc *ingest.Service, jobsSvc jobs.Service) *ImagesHandler {
	return &ImagesHandler{service: service, ingestSvc: ingestSvc, jobsSvc: jobsSvc}
}

// RegisterRoutes mounts every /api/images route on the given router
// group. Territory-separated search + generation endpoints delegate
// to the territory router and per-territory handler files.
//
//	GET  /search                 → TerritorySearch (defaults to retrieved)
//	GET  /retrieved/search       → RetrievedSearch
//	GET  /generated/search       → GeneratedSearch
//	POST /generated/generate     → GeneratedGenerate
//	GET  /generated/styles       → GeneratedStyles
//	GET  /diagnostics            → Diagnostics
//	POST /upload                 → Upload
//	POST /sync                   → Sync
//	POST /batch-generate         → GenerateBatch (async job system)
func (h *ImagesHandler) RegisterRoutes(r *gin.RouterGroup) {
	// Territory-separated search + generation endpoints.
	// /search defaults to territory=retrieved for callers that do not
	// select a territory explicitly.
	r.GET("/search", h.TerritorySearch)
	r.GET("/retrieved/search", h.RetrievedSearch)
	r.GET("/generated/search", h.GeneratedSearch)
	r.POST("/generated/generate", h.GeneratedGenerate)
	r.GET("/generated/styles", h.GeneratedStyles)

	r.GET("/diagnostics", h.Diagnostics)
	r.POST("/upload", h.Upload)
	r.POST("/sync", h.Sync)
	r.POST("/batch-generate", h.GenerateBatch)
}
