// Package clips — AdvancedSearch endpoint (PR-A Phase 4 BULK:
// SearchHandler folded onto unified *Handler).
package clips

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
)

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
	if h.searchSvc == nil {
		apiutil.InternalError(c, fmt.Errorf("advanced search service not available"))
		return
	}

	var req asset.AdvancedSearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	result, err := h.searchSvc.Search(c.Request.Context(), req)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":     true,
		"total":  result.Total,
		"count":  len(result.Clips),
		"limit":  result.Limit,
		"offset": result.Offset,
		"clips":  result.Clips,
	})
}
