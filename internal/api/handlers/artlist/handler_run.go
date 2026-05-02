package artlist

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"net/http"

	"go.uber.org/zap"
	"velox/go-master/internal/service/artlist"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/pkg/apiutil"
	"velox/go-master/pkg/models"
)

// RunTagPipeline executes the full Artlist flow for a tag
func (h *Handler) RunTagPipeline(c *gin.Context) {
	req, ok := apiutil.BindJSON[artlist.RunTagRequest](c)
	if !ok {
		return
	}

	if strings.TrimSpace(req.Term) == "" {
		apiutil.BadRequest(c, "term is required")
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

	if h.jobsService != nil {
		// Use common jobs system
		job, err := h.jobsService.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
			Type:      models.JobTypeArtlistRun,
			Payload:   req.ToMap(),
			MaxRetries: 3,
			ActiveKey: artlist.RunDedupKey(req.Term, req.RootFolderID, req.Strategy, req.DryRun),
		})
		if err != nil {
			h.log.Error("failed to enqueue artlist job", zap.Error(err))
			apiutil.InternalError(c, fmt.Errorf("failed to enqueue job: %w", err))
			return
		}
		c.JSON(http.StatusAccepted, artlist.JobToRunTagResponse(job))
		return
	}

	// Fallback to legacy system if jobs service not available
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
		apiutil.BadRequest(c, "run_id is required")
		return
	}

	resp, err := h.service.GetRunTag(c.Request.Context(), runID)
	if err != nil {
		apiutil.NotFound(c, err.Error())
		return
	}

	apiutil.OK(c, resp)
}
