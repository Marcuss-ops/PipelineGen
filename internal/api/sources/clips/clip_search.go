// Package clips hosts the HTTP handlers for the clip search / discovery
// endpoints. PR-A Phase 4 sub-2: clip_search.go exits the flat
// handler_sources_clip_search_handlers.go and lands in the new clips
// subpackage so callers can `internal/api/sources/clips`'s register the
// route instead of `*sources.SourcesHandler` carrying the method.
package clips

import (
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api/sources/internal"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/clips"
)

// AllSources is the canonical list of clip source names covered by the
// multi-source AdvancedSearch fan-out. Adding a new source? Append once
// here, then add the matching repository field on SearchHandler.
var AllSources = []string{"youtube", "artlist", "stock"}

// SearchHandler owns the clip search / discovery endpoints. The single
// endpoint currently mounted is AdvancedSearch (POST /search/advanced),
// which dispatches a multi-source filter query across the YouTube,
// Artlist, and Stock clip repositories.
//
// Methods are extracted from the legacy flat
// handler_sources_clip_search_handlers.go — same behavior, same wire
// shape, just a fresh receiver and subpackage.
type SearchHandler struct {
	clipsRepo   *clips.Repository
	artlistRepo *clips.Repository
	stockRepo   *clips.Repository
	log         *zap.Logger
}

// NewSearchHandler builds the SearchHandler.
func NewSearchHandler(clipsRepo, artlistRepo, stockRepo *clips.Repository, log *zap.Logger) *SearchHandler {
	return &SearchHandler{
		clipsRepo:   clipsRepo,
		artlistRepo: artlistRepo,
		stockRepo:   stockRepo,
		log:         log,
	}
}

// AdvancedSearchRequest is the JSON body for POST /api/media/search/advanced.
type AdvancedSearchRequest struct {
	Q             string `json:"q"`
	Source        string `json:"source"`
	Category      string `json:"category"`
	MinDuration   int    `json:"min_duration"` // seconds
	MaxDuration   int    `json:"max_duration"` // seconds
	HasTranscript bool   `json:"has_transcript"`
	HasDriveLink  bool   `json:"has_drive_link"`
	CreatedAfter  string `json:"created_after"`  // RFC3339
	CreatedBefore string `json:"created_before"` // RFC3339
	SortBy        string `json:"sort_by"`        // created_at, duration, name, source
	SortAsc       bool   `json:"sort_asc"`
	Limit         int    `json:"limit"`
	Offset        int    `json:"offset"`
}

// AdvancedSearch performs an advanced multi-source clip search with
// structured filters.
//
//	@Summary		Advanced clip search with filters
//	@Description	Search media assets with structured filters (category, date range,
//	@Description	duration, transcript, source, Drive link).
//	@Tags			search
//	@Accept			json
//	@Produce		json
//	@Param			body body AdvancedSearchRequest true "Filter request"
//	@Success		200  {object} object
//	@Router			/api/media/search/advanced [post]
func (h *SearchHandler) AdvancedSearch(c *gin.Context) {
	var req AdvancedSearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		internal.APIUtil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	// Map to repo-level request
	repoReq := clips.AdvancedSearchRequest{
		Q:             strings.TrimSpace(req.Q),
		Source:        req.Source,
		Category:      req.Category,
		MinDuration:   req.MinDuration,
		MaxDuration:   req.MaxDuration,
		HasTranscript: req.HasTranscript,
		HasDriveLink:  req.HasDriveLink,
		CreatedAfter:  req.CreatedAfter,
		CreatedBefore: req.CreatedBefore,
		SortBy:        req.SortBy,
		SortAsc:       req.SortAsc,
		Limit:         req.Limit,
		Offset:        req.Offset,
	}

	ctx := c.Request.Context()
	var allClips []any
	var totalCount int

	// Search across all available repos
	repos := map[string]*clips.Repository{
		"youtube": h.clipsRepo,
		"artlist": h.artlistRepo,
		"stock":   h.stockRepo,
	}

	sources := AllSources
	if repoReq.Source != "" && repoReq.Source != "all" {
		sources = []string{repoReq.Source}
	}

	for _, src := range sources {
		repo, ok := repos[src]
		if !ok || repo == nil {
			continue
		}

		srcReq := repoReq
		srcReq.Source = src
		srcReq.Limit = 0 // no per-source limit; merge first

		result, err := repo.SearchClipsAdvanced(ctx, srcReq)
		if err != nil {
			h.log.Warn("search failed", zap.String("source", src), zap.Error(err))
			continue
		}
		if result != nil {
			for _, clip := range result.Clips {
				allClips = append(allClips, clip)
			}
			totalCount += result.Total
		}
	}

	// Apply global limit/offset
	limit := repoReq.Limit
	if limit <= 0 {
		limit = 50
	}
	offset := repoReq.Offset
	if offset > 0 && offset < len(allClips) {
		allClips = allClips[offset:]
	} else if offset >= len(allClips) {
		allClips = nil
	}
	if len(allClips) > limit {
		allClips = allClips[:limit]
	}

	internal.APIUtil.OK(c, gin.H{
		"ok":     true,
		"total":  totalCount,
		"count":  len(allClips),
		"limit":  limit,
		"offset": repoReq.Offset,
		"clips":  allClips,
	})
}

// RegisterRoutes mounts AdvancedSearch onto the supplied gin router group.
func (h *SearchHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/search/advanced", h.AdvancedSearch)
}
