package artlist

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"net/http"

	"velox/go-master/internal/service/artlist"
)

// Stats returns statistics about Artlist clips and search terms
func (h *Handler) Stats(c *gin.Context) {
	stats, err := h.service.GetStats(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": fmt.Sprintf("failed to get stats: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, stats)
}

// Search searches for Artlist clips in the database
func (h *Handler) Search(c *gin.Context) {
	var req artlist.SearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": fmt.Sprintf("invalid request: %v", err),
		})
		return
	}

	if strings.TrimSpace(req.Term) == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "term is required",
		})
		return
	}

	if req.Limit <= 0 {
		req.Limit = 8
	}
	if req.Limit > 50 {
		req.Limit = 50
	}

	resp, err := h.service.Search(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": fmt.Sprintf("search failed: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// SearchLive performs a live search using the Node.js scraper
func (h *Handler) SearchLive(c *gin.Context) {
	var req struct {
		Term  string `json:"term" binding:"required"`
		Limit int    `json:"limit"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid request: " + err.Error()})
		return
	}

	if req.Limit <= 0 {
		req.Limit = 8
	}
	if req.Limit > 50 {
		req.Limit = 50
	}

	clips, err := h.service.SearchLive(c.Request.Context(), req.Term, req.Limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "clips": clips})
}

// Diagnostics returns a lightweight snapshot of Artlist wiring and counts
func (h *Handler) Diagnostics(c *gin.Context) {
	term := strings.TrimSpace(c.Query("term"))
	resp, err := h.service.Diagnostics(c.Request.Context(), term)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// SyncDriveFolder syncs a Google Drive folder to the database
func (h *Handler) SyncDriveFolder(c *gin.Context) {
	var req struct {
		FolderID  string `json:"folder_id" binding:"required"`
		MediaType string `json:"media_type" default:"stock"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid request: " + err.Error()})
		return
	}

	if req.MediaType == "" {
		req.MediaType = "stock"
	}

	resp, err := h.service.SyncDriveFolder(c.Request.Context(), req.FolderID, req.MediaType)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}
