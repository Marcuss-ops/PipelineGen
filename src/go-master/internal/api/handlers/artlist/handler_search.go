package artlist

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"velox/go-master/internal/service/artlist"
	"velox/go-master/pkg/apiutil"
)

// Stats returns statistics about Artlist clips and search terms
func (h *Handler) Stats(c *gin.Context) {
	stats, err := h.service.GetStats(c.Request.Context())
	if err != nil {
		apiutil.InternalError(c, fmt.Errorf("failed to get stats: %v", err))
		return
	}

	apiutil.OK(c, stats)
}

// Search searches for Artlist clips in the database
func (h *Handler) Search(c *gin.Context) {
	req, ok := apiutil.BindJSON[artlist.SearchRequest](c)
	if !ok {
		return
	}

	if strings.TrimSpace(req.Term) == "" {
		apiutil.BadRequest(c, "term is required")
		return
	}

	resp, err := h.service.Search(c.Request.Context(), &req)
	if err != nil {
		apiutil.InternalError(c, fmt.Errorf("search failed: %v", err))
		return
	}

	apiutil.OK(c, resp)
}

// Diagnostics returns Artlist system diagnostics
func (h *Handler) Diagnostics(c *gin.Context) {
	term := strings.TrimSpace(c.Query("term"))
	if term == "" {
		term = "test"
	}

	resp, err := h.service.Diagnostics(c.Request.Context(), term)
	if err != nil {
		apiutil.InternalError(c, fmt.Errorf("diagnostics failed: %v", err))
		return
	}

	apiutil.OK(c, resp)
}

// SearchLive performs a live search using the Node.js scraper
func (h *Handler) SearchLive(c *gin.Context) {
	term := strings.TrimSpace(c.Query("term"))
	limitStr := c.DefaultQuery("limit", "20")
	limit := 8
	if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
		limit = l
	}
	if limit > 50 {
		limit = 50
	}

	if term == "" {
		apiutil.BadRequest(c, "term is required")
		return
	}

	clips, err := h.service.SearchLive(c.Request.Context(), term, limit)
	if err != nil {
		apiutil.InternalError(c, fmt.Errorf("live search failed: %v", err))
		return
	}

	apiutil.OK(c, gin.H{"clips": clips})
}
