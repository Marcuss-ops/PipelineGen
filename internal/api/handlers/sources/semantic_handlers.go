package sources

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/internal/media/vectorstore"
	"velox/go-master/pkg/apiutil"
)

// SemanticSearchRequest represents a semantic vector search request
type SemanticSearchRequest struct {
	Q         string  `form:"q" binding:"required"`
	Vector    string  `form:"vector"` // text, visual, audio
	Mode      string  `form:"mode"`   // ann (default), hybrid
	Limit     int     `form:"limit,default=10"`
	MinScore  float64 `form:"min_score"`
	Source    string  `form:"source"`
	MediaType string  `form:"media_type"`
}

// SemanticSearch godoc
// @Summary Semantic vector search over media assets
// @Description Query Qdrant vector database using text, visual, or audio space. Supports ANN and hybrid (dense+sparse) modes.
// @Tags search
// @Param q query string true "Search query text"
// @Param vector query string false "Vector space: text, visual, audio"
// @Param mode query string false "Search mode: ann (default), hybrid (dense+BM25 sparse)"
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

	minScore := req.MinScore
	if minScore <= 0 {
		minScore = h.cfg.VectorSearch.MinInstantScore
	}

	mode := strings.ToLower(req.Mode)
	if mode == "" {
		mode = "ann"
	}

	ctx := c.Request.Context()

	h.log.Info("executing semantic search",
		zap.String("query", req.Q),
		zap.String("vector", qdrantVectorName),
		zap.String("mode", mode),
		zap.Float64("min_score", minScore),
	)

	var results []vectorstore.SearchResult
	var err error

	switch mode {
	case "hybrid":
		// #6: Hybrid search — dense E5 + sparse BM25 via RRF fusion
		queryVector, vecErr := h.realtimeSvc.EmbedTextForVector(ctx, req.Q, vectorName)
		if vecErr != nil {
			h.log.Error("failed to generate embedding for hybrid query",
				zap.String("query", req.Q), zap.Error(vecErr))
			apiutil.InternalError(c, vecErr)
			return
		}

		results, err = h.realtimeSvc.VectorStore().HybridSearch(ctx, vectorstore.HybridSearchRequest{
			QueryText:        req.Q,
			DenseVector:      queryVector,
			DenseVectorName:  qdrantVectorName,
			SparseVectorName: h.cfg.VectorSearch.SparseVectorName,
			Limit:            req.Limit,
			MinScore:         minScore,
			Source:           req.Source,
			MediaType:        req.MediaType,
		})

	default: // "ann"
		// Standard ANN search
		queryVector, vecErr := h.realtimeSvc.EmbedTextForVector(ctx, req.Q, vectorName)
		if vecErr != nil {
			h.log.Error("failed to generate embedding for query",
				zap.String("query", req.Q), zap.Error(vecErr))
			apiutil.InternalError(c, vecErr)
			return
		}

		results, err = h.realtimeSvc.VectorStore().Search(ctx, vectorstore.SearchRequest{
			QueryVector: queryVector,
			VectorName:  qdrantVectorName,
			Limit:       req.Limit,
			MinScore:    minScore,
			Source:      req.Source,
			MediaType:   req.MediaType,
		})
	}

	if err != nil {
		h.log.Error("Qdrant search failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	// #10: Auto-generate reason for each result
	for i := range results {
		results[i].Reason = buildSearchReason(results[i], req.Q)
	}

	c.JSON(http.StatusOK, gin.H{
		"query":     req.Q,
		"vector":    vectorName,
		"mode":      mode,
		"min_score": minScore,
		"count":     len(results),
		"results":   results,
	})
}

// buildSearchReason generates a human-readable reason for why a result matches the query.
func buildSearchReason(r vectorstore.SearchResult, query string) string {
	parts := []string{}

	// Score-based assessment
	if r.Score >= 0.85 {
		parts = append(parts, "very high semantic similarity")
	} else if r.Score >= 0.75 {
		parts = append(parts, "high semantic similarity")
	} else if r.Score >= 0.6 {
		parts = append(parts, "good thematic match")
	} else {
		parts = append(parts, "partial semantic match")
	}

	// Source context
	if r.Source != "" {
		parts = append(parts, "source: "+r.Source)
	}

	// Language match
	if r.Language != "" {
		parts = append(parts, "lang: "+r.Language)
	}

	// Top tags
	if len(r.Tags) > 0 {
		tags := r.Tags
		if len(tags) > 3 {
			tags = tags[:3]
		}
		parts = append(parts, "tags: "+strings.Join(tags, ", "))
	}

	// Content match heuristic: check query words against name/search_text
	queryWords := strings.Fields(strings.ToLower(query))
	nameLower := strings.ToLower(r.Name)
	matchCount := 0
	for _, w := range queryWords {
		if len(w) > 2 && strings.Contains(nameLower, w) {
			matchCount++
		}
	}
	if matchCount > 0 {
		parts = append(parts, fmt.Sprintf("name matches %d query terms", matchCount))
	}

	return strings.Join(parts, " | ")
}
