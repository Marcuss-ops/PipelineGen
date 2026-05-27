package sources

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/internal/media/vectorstore"
	"velox/go-master/internal/pkg/apiutil"
)

// SemanticSearchRequest represents a semantic vector search request
type SemanticSearchRequest struct {
	Q         string  `form:"q" binding:"required"`
	Vector    string  `form:"vector"` // text, visual, audio
	Limit     int     `form:"limit,default=10"`
	MinScore  float64 `form:"min_score"`
	Source    string  `form:"source"`
	MediaType string  `form:"media_type"`
}

// SemanticSearch godoc
// @Summary Semantic vector search over media assets
// @Description Query Qdrant vector database using text, visual, or audio space
// @Tags search
// @Param q query string true "Search query text"
// @Param vector query string false "Vector space: text, visual, audio"
// @Param limit query int false "Max results"
// @Param min_score query float64 false "Cosine similarity threshold"
// @Param source query string false "Filter by source system"
// @Param media_type query string false "Filter by media type"
// @Success 200 {object} apiutil.Response
// @Router /api/media/semantic-search [get]
func (h *Handler) SemanticSearch(c *gin.Context) {
	if h.realtimeSvc == nil {
		apiutil.BadRequest(c, "Vector search / Realtime matching service is disabled or not configured.")
		return
	}

	var req SemanticSearchRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		apiutil.BadRequest(c, "invalid query parameters: "+err.Error())
		return
	}

	req.Q = strings.TrimSpace(req.Q)
	if req.Q == "" {
		apiutil.BadRequest(c, "query parameter 'q' is required")
		return
	}

	vectorName := req.Vector
	if vectorName == "" {
		vectorName = "text"
	}

	// Resolve the named vector config
	var qdrantVectorName string
	vectorName = strings.ToLower(vectorName)
	switch vectorName {
	case "text":
		qdrantVectorName = h.cfg.VectorSearch.TextVectorName
	case "visual":
		qdrantVectorName = h.cfg.VectorSearch.VisualVectorName
	case "audio":
		qdrantVectorName = h.cfg.VectorSearch.AudioVectorName
	default:
		apiutil.BadRequest(c, "invalid vector name: must be 'text', 'visual', or 'audio'")
		return
	}

	// Compute embedding for the query using the appropriate vector space
	queryVector, err := h.realtimeSvc.EmbedTextForVector(c.Request.Context(), req.Q, vectorName)
	if err != nil {
		h.log.Error("failed to generate embedding for query", 
			zap.String("query", req.Q), 
			zap.String("vector", vectorName),
			zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	minScore := req.MinScore
	if minScore <= 0 {
		minScore = h.cfg.VectorSearch.MinInstantScore
	}

	h.log.Info("executing semantic search",
		zap.String("query", req.Q),
		zap.String("vector", qdrantVectorName),
		zap.Float64("min_score", minScore),
	)

	// Perform Qdrant ANN search
	results, err := h.realtimeSvc.VectorStore().Search(c.Request.Context(), vectorstore.SearchRequest{
		QueryVector: queryVector,
		VectorName:  qdrantVectorName,
		Limit:       req.Limit,
		MinScore:    minScore,
		Source:      req.Source,
		MediaType:   req.MediaType,
	})
	if err != nil {
		h.log.Error("Qdrant search failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"query":       req.Q,
		"vector":      vectorName,
		"min_score":   minScore,
		"count":       len(results),
		"results":     results,
	})
}
