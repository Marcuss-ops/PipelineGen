// Package clips hosts the HTTP handlers for the clip search / discovery
// endpoints. PR-A Phase 4 sub-2: clip_search.go exits the flat
// handler_sources_clip_search_handlers.go and lands in the new clips
// subpackage so callers can hook `internal/api/sources/clips`'s
// SearchHandler instead of carrying the method on *sources.SourcesHandler.
package clips

import (
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api/sources/internal"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/clips"
)

// AllSources is the canonical list of clip source names covered by the
// multi-source AdvancedSearch fan-out. Adding a new source is single-site:
//  1. Append here.
//  2. Add the matching field on *sources.SourcesHandler in its constructors.
//  3. Add the matching entry to the map passed to NewSearchHandler at the
//     SourcesHandler wiring site.
//
// Single source of truth for the source fan-out avoids drift between the
// iteration list and the repo map.
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
	repos map[string]*clips.Repository
	log   *zap.Logger
}

// NewSearchHandler builds the SearchHandler. Pass a map keyed by every
// entry in AllSources; missing keys are skipped at fan-out time.
func NewSearchHandler(repos map[string]*clips.Repository, log *zap.Logger) *SearchHandler {
	return &SearchHandler{
		repos: repos,
		log:   log,
	}
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
//	@Success		200  {object} object
//	@Router			/api/media/search/advanced [post]
func (h *SearchHandler) AdvancedSearch(c *gin.Context) {
	var req clips.AdvancedSearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		internal.APIUtil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	// Preserve legacy behavior: only `q` is whitespace-trimmed before the
	// fan-out. Other string fields (Source, Category, SortBy, etc.) are
	// intentionally passed raw to match the pre-consolidation behavior of
	// the local AdvancedSearchRequest that did the same. Don't extend the
	// trim "for consistency" - that would be a silent wire-shape change.
	req.Q = strings.TrimSpace(req.Q)

	ctx := c.Request.Context()
	var allClips []any
	var totalCount int

	sources := AllSources
	if req.Source != "" && req.Source != "all" {
		sources = []string{req.Source}
	}

	for _, src := range sources {
		repo, ok := h.repos[src]
		if !ok || repo == nil {
			continue
		}

		srcReq := req
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
	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}
	offset := req.Offset
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
		"offset": req.Offset,
		"clips":  allClips,
	})
}

// RegisterRoutes mounts AdvancedSearch onto the supplied gin router group.
func (h *SearchHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/search/advanced", h.AdvancedSearch)
}
