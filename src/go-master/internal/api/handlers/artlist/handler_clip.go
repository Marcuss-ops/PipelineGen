package artlist

import (
	"io"

	"github.com/gin-gonic/gin"

	"velox/go-master/internal/service/artlist"
	"velox/go-master/pkg/apiutil"
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
		apiutil.BadRequest(c, "clip id is required")
		return
	}

	resp, err := h.service.GetClipStatus(c.Request.Context(), clipID)
	if err != nil {
		apiutil.NotFound(c, err.Error())
		return
	}

	apiutil.OK(c, resp)
}

// DownloadClip downloads a clip from Artlist
func (h *Handler) DownloadClip(c *gin.Context) {
	clipID := c.Param("id")
	if clipID == "" {
		apiutil.BadRequest(c, "clip id is required")
		return
	}

	var req artlist.DownloadClipRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		req = artlist.DownloadClipRequest{}
	}

	resp, err := h.service.DownloadClip(c.Request.Context(), clipID, &req)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, resp)
}

// UploadClipToDrive uploads a clip to Google Drive
func (h *Handler) UploadClipToDrive(c *gin.Context) {
	clipID := c.Param("id")
	if clipID == "" {
		apiutil.BadRequest(c, "clip id is required")
		return
	}

	var req artlist.UploadClipToDriveRequest
	if err := c.ShouldBindJSON(&req); err != nil && err != io.EOF {
		apiutil.BadRequest(c, "invalid json body")
		return
	}

	resp, err := h.service.UploadClipToDrive(c.Request.Context(), clipID, &req)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, resp)
}

// ProcessClip processes a clip: search → download → upload to Drive → update DB
func (h *Handler) ProcessClip(c *gin.Context) {
	req, ok := apiutil.BindJSON[artlist.ProcessClipRequest](c)
	if !ok {
		return
	}

	if req.ClipID == "" && req.Term == "" {
		apiutil.BadRequest(c, "clip_id or term is required")
		return
	}

	resp, err := h.service.ProcessClip(c.Request.Context(), &req)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, resp)
}

func (h *Handler) ImportScraperDB(c *gin.Context) {
	req, ok := apiutil.BindJSON[ImportScraperDBRequest](c)
	if !ok {
		return
	}

	imported, err := h.service.ImportScraperDB(c.Request.Context(), req.DBPath)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, ImportScraperDBResponse{
		OK:       true,
		Imported: imported,
	})
}
