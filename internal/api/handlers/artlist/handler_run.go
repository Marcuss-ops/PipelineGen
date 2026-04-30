package artlist

import (
	"strings"

	"github.com/gin-gonic/gin"
	"net/http"

	"velox/go-master/internal/service/artlist"
	"go.uber.org/zap"
)

// RunTagPipeline executes the full Artlist flow for a tag
func (h *Handler) RunTagPipeline(c *gin.Context) {
	var req artlist.RunTagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid request: " + err.Error()})
		return
	}

	if strings.TrimSpace(req.Term) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "term is required"})
		return
	}
	if req.Limit <= 0 {
		req.Limit = 1
	}

	h.log.Info("artlist run requested",
		zap.String("term", req.Term),
		zap.Int("limit", req.Limit),
		zap.String("root_folder_id", req.RootFolderID),
		zap.String("strategy", req.Strategy),
		zap.Bool("dry_run", req.DryRun),
		zap.Bool("force_reupload", req.ForceReupload),
	)

	resp, err := h.service.StartRunTag(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, resp)
		return
	}

	c.JSON(http.StatusAccepted, resp)
}

// RunStatus returns the tracked status for a background artlist run
func (h *Handler) RunStatus(c *gin.Context) {
	runID := strings.TrimSpace(c.Param("run_id"))
	if runID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "run_id is required"})
		return
	}

	resp, err := h.service.GetRunTag(c.Request.Context(), runID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}
