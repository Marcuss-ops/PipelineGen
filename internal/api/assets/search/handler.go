// Package search provides HTTP transport for search operations
// (cross-provider search, semantic search, clip recommendation).
// All business logic is delegated to application/assets/search.Service.
package search

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	appsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	defaults "github.com/Marcuss-ops/PipelineGen/pkg/defaults"
)

// Handler is the thin HTTP transport for search operations.
type Handler struct {
	svc *appsearch.Service
	log *zap.Logger
}

// NewHandler creates a SearchHandler.
func NewHandler(svc *appsearch.Service, log *zap.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes registers search routes under the given group.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/search", h.Search)
	r.GET("/semantic-search", h.SemanticSearch)
	r.POST("/recommend", h.Recommend)
}

// ── Search (GET /search) ──────────────────────────────────────────

type searchRequest struct {
	Q     string `form:"q" binding:"required"`
	Type  string `form:"type"`
	Limit int    `form:"limit,default=20"`
	Sort  string `form:"sort"`
}

func (h *Handler) Search(c *gin.Context) {
	if h.svc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "search service not wired")
		return
	}
	var req searchRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		apiutil.BadRequest(c, "invalid query: "+err.Error())
		return
	}
	req.Q = strings.TrimSpace(req.Q)
	if req.Q == "" {
		apiutil.BadRequest(c, "query parameter 'q' is required")
		return
	}
	limit := defaults.Int(req.Limit, 20)

	result, err := h.svc.Search(c.Request.Context(), appsearch.SearchRequest{
		Query:     req.Q,
		MediaType: req.Type,
		Limit:     limit,
	})
	if err != nil {
		h.log.Error("search failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// ── SemanticSearch (GET /semantic-search) ─────────────────────────

type semanticSearchRequest struct {
	Q         string  `form:"q" binding:"required"`
	Vector    string  `form:"vector"`
	Mode      string  `form:"mode"`
	Limit     int     `form:"limit,default=10"`
	MinScore  float64 `form:"min_score"`
	Source    string  `form:"source"`
	MediaType string  `form:"media_type"`
}

func (h *Handler) SemanticSearch(c *gin.Context) {
	if h.svc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "search service not wired")
		return
	}
	var req semanticSearchRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		apiutil.BadRequest(c, "invalid query: "+err.Error())
		return
	}
	req.Q = strings.TrimSpace(req.Q)
	if req.Q == "" {
		apiutil.BadRequest(c, "query parameter 'q' is required")
		return
	}

	result, err := h.svc.SemanticSearch(c.Request.Context(), appsearch.SemanticSearchRequest{
		Query:      req.Q,
		VectorName: defaults.String(req.Vector, "text"),
		Mode:       strings.ToLower(req.Mode),
		Limit:      defaults.Int(req.Limit, 10),
		MinScore:   req.MinScore,
		Source:     req.Source,
		MediaType:  req.MediaType,
	})
	if err != nil {
		h.log.Error("semantic-search failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"query":     result.Query,
		"vector":    result.Vector,
		"mode":      result.Mode,
		"min_score": result.MinScore,
		"count":     result.Count,
		"results":   result.Results,
	})
}

// ── Recommend (POST /recommend) ───────────────────────────────────

type recommendRequest struct {
	ScriptText string  `json:"script_text" binding:"required"`
	Language   string  `json:"language,omitempty"`
	Source     string  `json:"source,omitempty"`
	MediaType  string  `json:"media_type,omitempty"`
	TopK       int     `json:"top_k_per_scene,omitempty"`
	MinScore   float64 `json:"min_score,omitempty"`
}

func (h *Handler) Recommend(c *gin.Context) {
	if h.svc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "search service not wired")
		return
	}
	req, ok := apiutil.BindJSON[recommendRequest](c)
	if !ok {
		return
	}
	req.ScriptText = strings.TrimSpace(req.ScriptText)
	if req.ScriptText == "" {
		apiutil.BadRequest(c, "script_text is required")
		return
	}

	result, err := h.svc.Recommend(c.Request.Context(), appsearch.RecommendRequest{
		ScriptText: req.ScriptText,
		Language:   req.Language,
		Source:     req.Source,
		MediaType:  req.MediaType,
		TopK:       defaults.Int(req.TopK, 5),
		MinScore:   req.MinScore,
	})
	if err != nil {
		h.log.Error("recommend failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	type clipItem struct {
		AssetID   string   `json:"asset_id"`
		Title     string   `json:"title"`
		Score     float64  `json:"score"`
		Source    string   `json:"source,omitempty"`
		MediaType string   `json:"media_type,omitempty"`
		DriveLink string   `json:"drive_link,omitempty"`
		Tags      []string `json:"tags,omitempty"`
		Reason    string   `json:"reason,omitempty"`
	}
	type sceneItem struct {
		Scene           string     `json:"scene"`
		SceneIndex      int        `json:"scene_index"`
		Query           string     `json:"query"`
		Recommendations []clipItem `json:"recommendations"`
	}
	scenes := make([]sceneItem, len(result.Scenes))
	for i, s := range result.Scenes {
		recs := make([]clipItem, len(s.Recommendations))
		for j, r := range s.Recommendations {
			recs[j] = clipItem{
				AssetID:   r.AssetID,
				Title:     r.Title,
				Score:     r.Score,
				Source:    r.Source,
				MediaType: r.MediaType,
				DriveLink: r.DriveLink,
				Tags:      r.Tags,
				Reason:    r.Reason,
			}
		}
		scenes[i] = sceneItem{
			Scene:           s.Scene,
			SceneIndex:      s.SceneIndex,
			Query:           s.Query,
			Recommendations: recs,
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":             true,
		"script_preview": result.ScriptPreview,
		"scene_count":    result.SceneCount,
		"scenes":         scenes,
		"total_clips":    result.TotalClips,
		"language":       result.Language,
	})
}
