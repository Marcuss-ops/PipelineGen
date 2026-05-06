package assets

import (
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/internal/service/artlist"
	"velox/go-master/internal/repository/catalog"
	"velox/go-master/pkg/apiutil"
)

// Handler handles unified asset search
type Handler struct {
	artlistSvc  *artlist.Service
	catalogRepo *catalog.Repository
	log         *zap.Logger
}

// NewHandler creates a new assets handler
func NewHandler(artlistSvc *artlist.Service, catalogRepo *catalog.Repository, log *zap.Logger) *Handler {
	return &Handler{
		artlistSvc:  artlistSvc,
		catalogRepo: catalogRepo,
		log:         log,
	}
}

// SearchRequest represents a search request
type SearchRequest struct {
	Q     string `form:"q" binding:"required"`
	Type  string `form:"type"` // video, image, audio, all
	Limit int    `form:"limit,default=20"`
}

// Search godoc
// @Summary Search assets across multiple sources
// @Description Search for clips, images, audio across Artlist, Catalog, etc.
// @Tags assets
// @Accept json
// @Produce json
// @Param q query string true "Search query"
// @Param type query string false "Asset type (video, image, audio, all)"
// @Param limit query int false "Limit results (default 20)"
// @Success 200 {object} map[string]interface{}
// @Router /assets/search [get]
func (h *Handler) Search(c *gin.Context) {
	var req SearchRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	req.Q = strings.TrimSpace(req.Q)
	if req.Q == "" {
		apiutil.BadRequest(c, "query parameter 'q' is required")
		return
	}

	if req.Limit <= 0 {
		req.Limit = 20
	}

	results := gin.H{}

	// Search Artlist clips
	if h.artlistSvc != nil && (req.Type == "" || req.Type == "video" || req.Type == "all") {
		// Use artlist service to search clips from database
		searchReq := &artlist.SearchRequest{
			Term:  req.Q,
			Limit: req.Limit,
		}
		searchResp, err := h.artlistSvc.Search(c.Request.Context(), searchReq)
		if err != nil {
			h.log.Warn("artlist search failed", zap.Error(err))
			results["artlist"] = gin.H{
				"count":   0,
				"results": []interface{}{},
				"error":   err.Error(),
			}
		} else if searchResp != nil {
			results["artlist"] = gin.H{
				"count":   len(searchResp.Clips),
				"results": searchResp.Clips,
				"source":  searchResp.Source,
			}
		}
	}

	// Search Catalog folders
	if h.catalogRepo != nil {
		catalogResults, err := h.catalogRepo.SearchAll(req.Q)
		if err != nil {
			h.log.Warn("catalog search failed", zap.Error(err))
		} else {
			results["catalog"] = gin.H{
				"count":   len(catalogResults),
				"results": catalogResults,
			}
		}
	}

	apiutil.OK(c, gin.H{
		"query":   req.Q,
		"type":    req.Type,
		"results": results,
	})
}
