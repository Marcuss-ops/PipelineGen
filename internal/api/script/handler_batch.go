// Package script (api/script) — handler_batch.go holds the
// /api/script/generate-batch endpoint and the batch progress lookup.
//
// PR4.F2 (June 2026): GenerateBatch is now a thin transport that
// delegates the entire orchestration to scripts.GenerateBatchUseCase
// (default-coercion, validation, async/sync dispatch, response shaping).
// The handler is responsible only for:
//
//   - nil-checking that the use case is wired
//   - binding JSON from the request body
//   - extracting the Idempotency-Key header
//   - calling the use case
//   - mapping domain errors to HTTP status codes
//   - serialising the typed AsyncJobRef or BatchGenerateResponse to JSON
//
// Add business logic ONLY in scripts.GenerateBatchUseCase. Anything
// added here is a code smell (the PR4.F reviewer flagged this layer as
// a back-slide risk).
//
// GetBatchProgress stays as a thin handler-level adaptor because its
// function is essentially "translate a job-event log stream into the
// public progress schema". It does not need its own use case.
package script

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/api"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// GetBatchProgress handles GET /api/script/generate-batch/progress.
//
// Parses the job's recent events to surface a friendly per-language
// translation map and a chapter-progress counter alongside the
// canonical job fields. Caller polls until status is Succeeded or
// Failed (or until terminal status arrives through the events stream).
func (h *ScriptFlowHandler) GetBatchProgress(c *gin.Context) {
	if h.jobsSvc == nil {
		api.Error(c, http.StatusServiceUnavailable, "jobs service not initialized")
		return
	}

	jobID := strings.TrimSpace(c.Query("job_id"))
	if jobID == "" {
		api.BadRequest(c, "job_id query parameter is required")
		return
	}

	job, err := h.jobsSvc.Get(c.Request.Context(), jobID)
	if err != nil {
		api.NotFound(c, fmt.Sprintf("job not found: %v", err))
		return
	}

	events, err := h.jobsSvc.ListEvents(c.Request.Context(), jobID)
	if err != nil {
		h.log.Warn("failed to list job events for batch progress", zap.String("job_id", jobID), zap.Error(err))
		events = nil
	}

	var currentPhase string
	translationProgress := make(map[string]string)
	var chaptersTotal, chaptersDone int

	if len(events) > 0 {
		lastEvent := events[len(events)-1]
		currentPhase = lastEvent.Message

		for _, ev := range events {
			msg := ev.Message
			if strings.Contains(msg, "Translating to") {
				parts := strings.Split(msg, "Translating to ")
				if len(parts) > 1 {
					langCode := strings.TrimSuffix(strings.TrimSpace(parts[1]), "...")
					if langCode != "" {
						translationProgress[langCode] = "in_progress"
					}
				}
			}
			if strings.Contains(msg, "Completed!") {
				for k := range translationProgress {
					translationProgress[k] = "completed"
				}
			}
			if strings.Contains(msg, "Processing chapter") {
				if _, errFmt := fmt.Sscanf(msg, "Processing chapter %d of %d", &chaptersDone, &chaptersTotal); errFmt == nil && chaptersTotal > 0 {
					if chaptersDone > 0 {
						chaptersDone = chaptersDone - 1
					}
				}
			}
		}
	}

	resp := gin.H{
		"job_id":         job.ID,
		"status":         job.Status,
		"progress":       job.Progress,
		"current_phase":  currentPhase,
		"translations":   translationProgress,
		"chapters_total": chaptersTotal,
		"chapters_done":  chaptersDone,
	}

	if job.Status == jobservice.StatusFailed && job.Error != "" {
		resp["error"] = job.Error
	}

	if job.Status == jobservice.StatusSucceeded && len(job.Result) > 0 {
		var resultObj map[string]any
		if err := json.Unmarshal(job.Result, &resultObj); err == nil {
			summary := gin.H{}
			if docURL, ok := resultObj["doc_url"]; ok {
				summary["doc_url"] = docURL
			}
			if trans, ok := resultObj["translations"]; ok {
				summary["translations"] = trans
			}
			if fc, ok := resultObj["failed_chapter_count"]; ok {
				summary["failed_chapter_count"] = fc
			}
			resp["result_summary"] = summary
		}
	}

	api.OK(c, resp)
}

// GenerateBatch handles POST /api/script/generate-batch.
//
// Thin transport that delegates to scripts.GenerateBatchUseCase.
// Domain errors map to HTTP via mapGenerateBatchError below.
func (h *ScriptFlowHandler) GenerateBatch(c *gin.Context) {
	if h.generateBatch == nil {
		api.Error(c, http.StatusServiceUnavailable, "generate-batch use case not initialized")
		return
	}

	req, ok := api.BindJSON[scripts.GenerateBatchRequest](c)
	if !ok {
		return
	}

	out, err := h.generateBatch.Run(c.Request.Context(), scripts.GenerateBatchInput{
		Request:        &req,
		IdempotencyKey: c.GetHeader("Idempotency-Key"),
	})
	if err != nil {
		h.mapGenerateBatchError(c, &req, err)
		return
	}

	if out.Async != nil {
		c.JSON(http.StatusOK, gin.H{
			"ok":         true,
			"async":      true,
			"job_id":     out.Async.JobID,
			"status":     out.Async.Status,
			"message":    "Batch script generation enqueued. Poll /api/jobs/" + out.Async.JobID + "/full for status.",
			"status_url": out.Async.StatusURL,
		})
		return
	}

	c.JSON(http.StatusOK, out.Response)
}

// mapGenerateBatchError translates a use-case error to an HTTP response.
// Validation errors carry structured details via GenerateBatchValidationErrors;
// the original handler exposed that slice verbatim, so we preserve the
// shape {"error":"invalid_request", "details":[...]}.
//
//   - ErrGenerateBatchInvalid → 400
//   - GenerateBatchValidationErrors → 400 with details (errors.As)
//   - ErrGenerateBatchMissing → 503
//   - ErrGenerateBatchAsyncFailed / SyncFailed → 500 with structured log
//   - default → 500 with structured log
func (h *ScriptFlowHandler) mapGenerateBatchError(c *gin.Context, req *scripts.GenerateBatchRequest, err error) {
	var verr *scripts.GenerateBatchValidationErrors
	if errors.As(err, &verr) {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"error":   "invalid_request",
			"details": verr.Details,
		})
		return
	}
	// Best-effort log title — empty if the request never reached the
	// use case (e.g. JSON-binding failed before parsing).
	var title string
	if req != nil {
		title = strings.TrimSpace(req.DocTitle)
	}
	switch {
	case errors.Is(err, scripts.ErrGenerateBatchMissing):
		api.Error(c, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, scripts.ErrGenerateBatchInvalid):
		api.Error(c, http.StatusBadRequest, err.Error())
	case errors.Is(err, scripts.ErrGenerateBatchAsyncFailed),
		errors.Is(err, scripts.ErrGenerateBatchSyncFailed):
		h.log.Error("generate-batch dispatch failed",
			zap.String("title", title),
			zap.Bool("async", req != nil && req.Async),
			zap.Error(err))
		api.InternalError(c, err)
	default:
		h.log.Error("generate-batch use case failed",
			zap.Error(err))
		api.InternalError(c, err)
	}
}
