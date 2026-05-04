package youtubeclip

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/internal/service/youtubeclip"
	"velox/go-master/pkg/apiutil"
)

type Handler struct {
	service *youtubeclip.Service
	log     *zap.Logger
}

func NewHandler(service *youtubeclip.Service, log *zap.Logger) *Handler {
	return &Handler{
		service: service,
		log:     log,
	}
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/extract", h.Extract)
	folders := r.Group("/folders")
	{
		folders.GET("/:id", h.GetFolder)
		folders.GET("/:id/clips", h.GetFolderClips)
		folders.GET("/search", h.SearchFolders)
		folders.GET("", h.ListFolders)
	}
}

func (h *Handler) GetFolder(c *gin.Context) {
	folderID := c.Param("id")
	if folderID == "" {
		apiutil.BadRequest(c, "folder id required")
		return
	}

	folder, err := h.service.GetFolder(c.Request.Context(), folderID)
	if err != nil {
		apiutil.NotFound(c, "folder not found")
		return
	}

	apiutil.OK(c, gin.H{"folder": folder})
}

func (h *Handler) GetFolderClips(c *gin.Context) {
	folderID := c.Param("id")
	if folderID == "" {
		apiutil.BadRequest(c, "folder id required")
		return
	}

	clips, err := h.service.ListFolderClips(c.Request.Context(), folderID)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{"folder_id": folderID, "clips": clips})
}

func (h *Handler) SearchFolders(c *gin.Context) {
	q := c.Query("q")
	if q == "" {
		apiutil.BadRequest(c, "query parameter 'q' required")
		return
	}

	folders, err := h.service.SearchFolders(c.Request.Context(), q)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{"query": q, "folders": folders})
}

func (h *Handler) ListFolders(c *gin.Context) {
	source := c.Query("source")

	folders, err := h.service.ListFolders(c.Request.Context(), source)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{"folders": folders})
}

func (h *Handler) Extract(c *gin.Context) {
	req, ok := apiutil.BindJSON[youtubeclip.ExtractRequest](c)
	if !ok {
		return
	}

	resp, err := h.service.Extract(c.Request.Context(), &req)

	// If there's a fatal error (not just item failures), return error
	if err != nil && (resp == nil || len(resp.Items) == 0) {
		// Check if it's a user error
		errMsg := err.Error()
		if strings.Contains(errMsg, "required") ||
			strings.Contains(errMsg, "invalid") ||
			strings.Contains(errMsg, "segments") {
			apiutil.BadRequest(c, errMsg)
			return
		}
		apiutil.InternalError(c, err)
		return
	}

	// If resp is nil, return error
	if resp == nil {
		apiutil.InternalError(c, fmt.Errorf("nil response"))
		return
	}

	// If all items failed, return 500; if some failed, return 207; if all succeeded, return 200
	failedCount := 0
	for _, item := range resp.Items {
		if item.Status == "failed" {
			failedCount++
		}
	}

	if failedCount == len(resp.Items) && len(resp.Items) > 0 {
		c.JSON(http.StatusInternalServerError, resp)
	} else if failedCount > 0 {
		c.JSON(http.StatusMultiStatus, resp)
	} else {
		apiutil.OK(c, resp)
	}
}
