package artlist

import (
	"fmt"
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

	req.Limit = apiutil.ClampLimit(req.Limit, 8, 50)

	resp, err := h.service.Search(c.Request.Context(), &req)
	if err != nil {
		apiutil.InternalError(c, fmt.Errorf("search failed: %v", err))
		return
	}

	apiutil.OK(c, resp)
}

// SearchLive performs a live search using the Node.js scraper
func (h *Handler) SearchLive(c *gin.Context) {
	req, ok := apiutil.BindJSON[struct {
		Term  string `json:"term" binding:"required"`
		Limit int    `json:"limit"`
	}](c)
	if !ok {
		return
	}

	req.Limit = apiutil.ClampLimit(req.Limit, 8, 50)

	clips, err := h.service.SearchLive(c.Request.Context(), req.Term, req.Limit)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{"clips": clips})
}

// Diagnostics returns a lightweight snapshot of Artlist wiring and counts
func (h *Handler) Diagnostics(c *gin.Context) {
	term := strings.TrimSpace(c.Query("term"))
	resp, err := h.service.Diagnostics(c.Request.Context(), term)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, resp)
}

// SyncDriveFolder syncs a Google Drive folder to the database
func (h *Handler) SyncDriveFolder(c *gin.Context) {
	req, ok := apiutil.BindJSON[struct {
		FolderID  string `json:"folder_id" binding:"required"`
		MediaType string `json:"media_type" default:"stock"`
	}](c)
	if !ok {
		return
	}

	resp, err := h.service.SyncDriveFolder(c.Request.Context(), req.FolderID, req.MediaType)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, resp)
}
