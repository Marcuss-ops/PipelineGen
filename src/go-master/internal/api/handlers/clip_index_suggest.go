package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"velox/go-master/internal/clip"
)

// SuggestForSentence godoc
// @Summary Get clip suggestions for a sentence
// @Description Get intelligent clip suggestions for a sentence from your script. This is the main endpoint for semantic matching.
// @Tags clip-suggestions
// @Accept json
// @Produce json
// @Param request body clip.SentenceSuggestRequest true "Sentence suggest request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Router /clip/index/suggest/sentence [post]
func (h *ClipIndexHandler) SuggestForSentence(c *gin.Context) {
	if h.suggester == nil {
		c.JSON(httpStatusServiceUnavailable, gin.H{
			"ok":    false,
			"error": "Semantic suggester not initialized",
		})
		return
	}

	var req clip.SentenceSuggestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Invalid request: " + err.Error(),
		})
		return
	}

	// Apply defaults
	if req.MaxResults == 0 {
		req.MaxResults = 10
	}
	if req.MinScore < 0 {
		req.MinScore = 20 // Minimum threshold
	}

	// Get suggestions
	suggestions := h.suggester.SuggestForSentence(
		c.Request.Context(),
		req.Sentence,
		req.MaxResults,
		req.MinScore,
		req.MediaType,
	)

	c.JSON(http.StatusOK, gin.H{
		"ok":          true,
		"sentence":    req.Sentence,
		"suggestions": suggestions,
		"total":       len(suggestions),
		"best_score":  getBestScore(suggestions),
	})
}

// SuggestForScript godoc
// @Summary Get clip suggestions for an entire script
// @Description Process an entire script and get clip suggestions for each sentence. Accessible from remote machines.
// @Tags clip-suggestions
// @Accept json
// @Produce json
// @Param request body clip.ScriptSuggestRequest true "Script suggest request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Router /clip/index/suggest/script [post]
func (h *ClipIndexHandler) SuggestForScript(c *gin.Context) {
	if h.suggester == nil {
		c.JSON(httpStatusServiceUnavailable, gin.H{
			"ok":    false,
			"error": "Semantic suggester not initialized",
		})
		return
	}

	var req clip.ScriptSuggestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Invalid request: " + err.Error(),
		})
		return
	}

	// Apply defaults
	if req.MaxResultsPerSentence == 0 {
		req.MaxResultsPerSentence = 5
	}
	if req.MinScore < 0 {
		req.MinScore = 20
	}

	// Get suggestions for entire script
	suggestions := h.suggester.SuggestForScript(
		c.Request.Context(),
		req.Script,
		req.MaxResultsPerSentence,
		req.MinScore,
		req.MediaType,
	)

	// Calculate summary
	totalSentences := len(suggestions)
	totalClips := 0
	for _, s := range suggestions {
		totalClips += len(s.Suggestions)
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":                     true,
		"script_length":          len(req.Script),
		"sentences_with_clips":   totalSentences,
		"total_clip_suggestions": totalClips,
		"suggestions":            suggestions,
	})
}

// SimilarClips finds clips similar to a given clip
// @Summary Find similar clips
// @Description Find clips similar to a given clip by ID
// @Tags Clip Index
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /clip/index/similar [post]
func (h *ClipIndexHandler) SimilarClips(c *gin.Context) {
	if h.indexer == nil {
		c.JSON(httpStatusServiceUnavailable, gin.H{
			"ok":    false,
			"error": "Clip indexer not initialized",
		})
		return
	}

	var req clip.SimilarClipsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Invalid request: " + err.Error(),
		})
		return
	}

	if req.MaxResults == 0 {
		req.MaxResults = 10
	}

	results, err := h.indexer.FindSimilarClips(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Failed to find similar clips: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":            true,
		"clip_id":       req.ClipID,
		"total_found":   len(results),
		"similar_clips": results,
	})
}

// Helper function to get best score from suggestions
func getBestScore(suggestions []clip.SuggestionResult) float64 {
	if len(suggestions) == 0 {
		return 0
	}
	return suggestions[0].Score
}
