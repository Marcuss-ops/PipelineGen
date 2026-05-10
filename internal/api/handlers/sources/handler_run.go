package sources

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"

	"go.uber.org/zap"
	"velox/go-master/internal/service/artlist"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/pkg/apiutil"
	"velox/go-master/pkg/models"
)

// RunTagPipeline executes the full Artlist flow for a tag
func (h *ArtlistHandler) RunTagPipeline(c *gin.Context) {
	req, ok := apiutil.BindJSON[artlist.RunTagRequest](c)
	if !ok {
		return
	}

	if strings.TrimSpace(req.Term) == "" {
		apiutil.BadRequest(c, "term is required")
		return
	}

	// Normalize request before enqueue
	req = artlist.NormalizeRunTagRequest(req, artlist.RunDefaults{
		DefaultRootFolderID: h.cfg.Harvester.DriveFolderID,
		MaxLimit:            500,
	})

	h.log.Info("artlist run requested",
		zap.String("term", req.Term),
		zap.Int("limit", req.Limit),
		zap.String("root_folder_id", req.RootFolderID),
		zap.String("strategy", req.Strategy),
		zap.Bool("dry_run", req.DryRun),
	)

	h.enqueueArtlistRun(c, req)
}

// RunSmartPipeline executes the Artlist flow with preset support
func (h *ArtlistHandler) RunSmartPipeline(c *gin.Context) {
	req, ok := apiutil.BindJSON[artlist.RunSmartRequest](c)
	if !ok {
		return
	}

	if strings.TrimSpace(req.Term) == "" {
		apiutil.BadRequest(c, "term is required")
		return
	}

	// Convert to RunTagRequest using preset
	runReq := req.ToRunTagRequest(h.presetsConfig)

	// Normalize request
	normalized := artlist.NormalizeRunTagRequest(*runReq, artlist.RunDefaults{
		DefaultRootFolderID: h.cfg.Harvester.DriveFolderID,
		MaxLimit:            500,
	})
	runReq = &normalized

	h.log.Info("artlist smart run requested",
		zap.String("term", req.Term),
		zap.String("preset", req.Preset),
		zap.Int("limit", runReq.Limit),
	)

	h.enqueueArtlistRun(c, *runReq)
}

// enqueueArtlistRun is the single enqueue path for all Artlist runs
func (h *ArtlistHandler) enqueueArtlistRun(c *gin.Context, req artlist.RunTagRequest) {
	if h.jobsService == nil {
		apiutil.InternalError(c, fmt.Errorf("jobs service not configured"))
		return
	}

	// Use common jobs system exclusively
	job, err := h.jobsService.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
		Type:       models.JobTypeArtlistRun,
		Payload:    req.ToMap(),
		MaxRetries: 3,
		ActiveKey:  artlist.RunDedupKey(req.Term, req.RootFolderID, req.Strategy, req.DryRun),
	})
	if err != nil {
		h.log.Error("failed to enqueue artlist job", zap.Error(err))
		apiutil.InternalError(c, fmt.Errorf("failed to enqueue job: %w", err))
		return
	}
	apiutil.Accepted(c, artlist.JobToRunTagResponse(job))
}

// RunStatus returns the tracked status for a background artlist run
func (h *ArtlistHandler) RunStatus(c *gin.Context) {
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
