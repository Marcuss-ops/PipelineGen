// Package clips — AdvancedSearch endpoint (PR-A Phase 4 BULK:
// SearchHandler folded onto unified *Handler).
package assets

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// AllSources is the canonical list of clip source names covered by the
// multi-source AdvancedSearch fan-out. Adding a new source is single-site:
// append here and the resolver loop picks it up automatically (the repos
// map is built in AdvancedSearch from clipsRepo/artlistRepo/stockRepo).
var AllSources = []string{"youtube", "artlist", "stock"}

// AdvancedSearch performs a multi-source clip search with structured filters.
//
//	@Summary		Advanced clip search with filters
//	@Description	Search media assets with structured filters (category, date range,
//	@Description	duration, transcript, source, Drive link).
//	@Tags			search
//	@Accept			json
//	@Produce		json
//	@Success		200  {object} object
//	@Router			/api/media/search/advanced [post]
func (h *Handler) AdvancedSearch(c *gin.Context) {
	var req assets.AdvancedSearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	// Preserve legacy behavior: only `q` is whitespace-trimmed before fan-out.
	req.Q = strings.TrimSpace(req.Q)

	// Build the per-source repo lookup. nil repos are skipped (no error).
	repos := map[string]*assets.ClipsRepository{
		"youtube": h.clipsRepo,
		"artlist": h.artlistRepo,
		"stock":   h.stockRepo,
	}

	ctx := c.Request.Context()
	var allClips []any
	var totalCount int

	sources := AllSources
	if req.Source != "" && req.Source != "all" {
		sources = []string{req.Source}
	}

	for _, src := range sources {
		repo, ok := repos[src]
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

	apiutil.OK(c, gin.H{
		"ok":     true,
		"total":  totalCount,
		"count":  len(allClips),
		"limit":  limit,
		"offset": req.Offset,
		"clips":  allClips,
	})
}
