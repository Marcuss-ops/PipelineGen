package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	jobservice "github.com/Marcuss-ops/PipelineGen/internal/jobs"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// GenerateFromCatalog is the async-only endpoint for catalog-first script generation.
//
// Flow:
//  1. Validate request
//  2. Run CatalogScanner to find + cluster clips for the topic
//  3. If coverage is below min_coverage, return error
//  4. Enqueue a background job with selected clip IDs + catalog report
//
// POST /api/script/generate-from-catalog
func (h *ScriptFlowHandler) GenerateFromCatalog(c *gin.Context) {
	var req GenerateFromCatalogRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	if req.Topic == "" {
		apiutil.BadRequest(c, "topic is required")
		return
	}
	if req.MaxClips <= 0 {
		req.MaxClips = 10
	}
	if req.MinCoverage <= 0 {
		req.MinCoverage = 0.3
	}

	if h.clipSourceBuilder == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "clip source builder not initialized")
		return
	}
	if h.jobsSvc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "job service not initialized")
		return
	}

	// Run catalog scan synchronously so we can report coverage to the user
	// before enqueuing the (potentially long) script generation job.
	selectedIDs, catalogReport, err := h.clipSourceBuilder.SelectClipsForTopic(c.Request.Context(), req.Topic, req.MaxClips)
	if err != nil {
		h.log.Error("catalog scan failed", zap.String("topic", req.Topic), zap.Error(err))
		apiutil.Error(c, http.StatusInternalServerError, "catalog scan failed: "+err.Error())
		return
	}

	if len(selectedIDs) == 0 {
		apiutil.Error(c, http.StatusBadRequest, "no usable clips found for topic: "+req.Topic)
		return
	}

	// Check minimum coverage
	if catalogReport.CoverageScore < req.MinCoverage {
		apiutil.Error(c, http.StatusBadRequest,
			"insufficient catalog coverage for: "+req.Topic)
		return
	}

	// Build job payload — same structure as clip_source payload
	payload := jobPayloadCatalogScript{
		ClipIDs:            selectedIDs,
		Title:              req.Title,
		OutputName:         req.OutputName,
		Language:           req.Language,
		Tone:               req.Tone,
		Model:              req.Model,
		TargetWords:        req.TargetWords,
		Duration:           req.Duration,
		TranscriptPolicy:   req.TranscriptPolicy,
		OrderingStrategy:   req.OrderingStrategy,
		CreateDoc:          req.CreateDoc,
		SaveToDB:           req.SaveToDB,
		ForceRefresh:       req.ForceRefresh,
		MinQualityScore:    req.MinQualityScore,
		MinTranscriptWords: req.MinTranscriptWords,
	}

	// EnqueueRequest.Payload expects map[string]any, not a struct
	payloadBytes, _ := json.Marshal(payload)
	var payloadMap map[string]any
	json.Unmarshal(payloadBytes, &payloadMap)

	h.log.Info("enqueuing catalog script generation",
		zap.String("topic", req.Topic),
		zap.Int("clips_selected", len(selectedIDs)),
		zap.Float64("coverage", catalogReport.CoverageScore),
	)

	job, err := h.jobsSvc.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
		Type:    "script.generate_from_catalog",
		Payload: payloadMap,
	})
	if err != nil {
		h.log.Error("failed to enqueue catalog script job", zap.Error(err))
		apiutil.Error(c, http.StatusInternalServerError, "failed to enqueue job")
		return
	}

	// Build response with async job details + catalog report
	c.JSON(http.StatusAccepted, gin.H{
		"ok":             true,
		"job_id":         job.ID,
		"status":         "queued",
		"catalog_report": catalogReport,
	})
}
