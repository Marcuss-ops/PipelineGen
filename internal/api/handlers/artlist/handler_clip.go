package artlist

import (
	"github.com/gin-gonic/gin"
	"net/http"

	"velox/go-master/internal/service/artlist"
)

type ImportScraperDBRequest struct {
	DBPath string `json:"db_path"`
}

type ImportScraperDBResponse struct {
	OK       bool   `json:"ok"`
	Imported int    `json:"imported"`
	Error    string `json:"error,omitempty"`
}

// GetClipStatus returns the status of a clip
func (h *Handler) GetClipStatus(c *gin.Context) {
	clipID := c.Param("id")
	if clipID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "clip id is required"})
		return
	}

	resp, err := h.service.GetClipStatus(c.Request.Context(), clipID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// DownloadClip downloads a clip from Artlist
func (h *Handler) DownloadClip(c *gin.Context) {
	clipID := c.Param("id")
	if clipID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "clip id is required"})
		return
	}

	var req artlist.DownloadClipRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		req = artlist.DownloadClipRequest{}
	}

	resp, err := h.service.DownloadClip(c.Request.Context(), clipID, &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// UploadClipToDrive uploads a clip to Google Drive
func (h *Handler) UploadClipToDrive(c *gin.Context) {
	clipID := c.Param("id")
	if clipID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "clip id is required"})
		return
	}

	var req artlist.UploadClipToDriveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// Ignore bind error, use empty request
	}

	resp, err := h.service.UploadClipToDrive(c.Request.Context(), clipID, &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// ProcessClip processes a clip: search → download → upload to Drive → update DB
func (h *Handler) ProcessClip(c *gin.Context) {
	var req artlist.ProcessClipRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid request"})
		return
	}

	if req.ClipID == "" && req.Term == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "clip_id or term is required"})
		return
	}

	resp, err := h.service.ProcessClip(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *Handler) ImportScraperDB(c *gin.Context) {
	var req ImportScraperDBRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "invalid request: " + err.Error(),
		})
		return
	}

	imported, err := h.service.ImportScraperDB(c.Request.Context(), req.DBPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, ImportScraperDBResponse{
		OK:       true,
		Imported: imported,
	})
}
