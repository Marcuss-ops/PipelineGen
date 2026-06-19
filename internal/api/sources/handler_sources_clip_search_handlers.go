package sources

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/clips"
)

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

// AdvancedSearch godoc
// @Summary      Advanced clip search with filters
// @Description  Search media assets with structured filters (category, date range, duration, transcript, source, Drive link).
// @Tags         search
// @Accept       json
// @Produce      json
// @Param        body body AdvancedSearchRequest true "Filter request"
// @Success      200  {object} object
// @Router       /api/media/search/advanced [post]
func (h *Handler) AdvancedSearch(c *gin.Context) {
	var req AdvancedSearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
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

	sources := []string{"youtube", "artlist", "stock"}
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

	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"total":  totalCount,
		"count":  len(allClips),
		"limit":  limit,
		"offset": repoReq.Offset,
		"clips":  allClips,
	})
}
