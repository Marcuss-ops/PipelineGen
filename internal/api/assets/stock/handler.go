package stock

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	stockapp "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock"
	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/primitives"
)

// StockHandler is the HTTP projection of the stock pipeline UseCase.
// It owns request validation, JSON binding, and response shaping.
// All business logic lives in StockUseCase.
type StockHandler struct {
	useCase *stockpipeline.StockUseCase
	log     *zap.Logger
}

// NewStockHandler constructs the handler. Both deps are mandatory.
func NewStockHandler(uc *stockpipeline.StockUseCase, log *zap.Logger) *StockHandler {
	if log == nil {
		log = zap.NewNop()
	}
	return &StockHandler{useCase: uc, log: log}
}

// RegisterRoutes mounts the stock-pipeline HTTP routes.
func (h *StockHandler) RegisterRoutes(r *gin.RouterGroup) {
	h.log.Info("registering stock-pipeline routes")
	r.POST("/run", h.Run)
	r.POST("/search-and-run", h.SearchAndRun)
}

// SearchAndRun handles POST /api/stock-pipeline/search-and-run.
//
// This endpoint keeps the historical search request shape (`queries`)
// separate from the legacy `/run` shape (`search_queries`). The
// application converter remains the single owner of request-to-command
// mapping and duration defaults. Search probe metadata is intentionally
// bound permissively because operators may attach provider-specific
// fields (for example `test` and `request_tag`) without changing the
// execution command.
func (h *StockHandler) SearchAndRun(c *gin.Context) {
	var req stockapp.StockSearchAndRunRequest
	decoder := json.NewDecoder(c.Request.Body)
	if err := decoder.Decode(&req); err != nil {
		c.JSON(http.StatusBadRequest, runResponse{
			Status:    StatusError,
			Error:     "invalid JSON payload: " + err.Error(),
			ErrorCode: ErrCodeInvalidPayload,
		})
		return
	}

	if len(req.Queries) == 0 && len(req.DirectURLs) == 0 && len(req.DriveURLs) == 0 && len(req.Clips) == 0 {
		c.JSON(http.StatusBadRequest, runResponse{
			Status:    StatusError,
			Error:     "at least one of queries, direct_urls, drive_urls, or clips is required",
			ErrorCode: ErrCodeInvalidPayload,
		})
		return
	}
	if len(req.Clips) > MaxClipsPerRun {
		c.JSON(http.StatusBadRequest, runResponse{
			Status:    StatusError,
			Error:     fmt.Sprintf("too many clips requested (max %d)", MaxClipsPerRun),
			ErrorCode: ErrCodeMaxClips,
		})
		return
	}
	for _, u := range req.DirectURLs {
		if !isValidURL(primitives.NewURL(u)) {
			c.JSON(http.StatusBadRequest, runResponse{
				Status:    StatusError,
				Error:     "invalid or insecure direct_url: " + redactURL(u),
				ErrorCode: ErrCodeInvalidURL,
			})
			return
		}
	}
	for _, u := range req.DriveURLs {
		if !isValidURL(primitives.NewURL(u)) {
			c.JSON(http.StatusBadRequest, runResponse{
				Status:    StatusError,
				Error:     "invalid or insecure drive_url: " + redactURL(u),
				ErrorCode: ErrCodeInvalidURL,
			})
			return
		}
	}
	for _, clip := range req.Clips {
		if clip.URL != "" && !isValidURL(primitives.NewURL(clip.URL)) {
			c.JSON(http.StatusBadRequest, runResponse{
				Status:    StatusError,
				Error:     "invalid or insecure clip url: " + redactURL(clip.URL),
				ErrorCode: ErrCodeInvalidURL,
			})
			return
		}
	}
	if !isSafePath(req.Subfolder) || !isSafePath(req.FolderName) || !isSafePath(req.DriveFolderID) || !isSafePath(req.FolderID) {
		c.JSON(http.StatusBadRequest, runResponse{
			Status:    StatusError,
			Error:     "path traversal characters detected in folder configuration",
			ErrorCode: ErrCodePathTraversal,
		})
		return
	}

	cmd, err := stockapp.FromAPIRequest(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, runResponse{
			Status:    StatusError,
			Error:     err.Error(),
			ErrorCode: ErrCodeInvalidPayload,
		})
		return
	}
	jobID, err := h.useCase.Submit(c.Request.Context(), cmd, req.Async)
	if err != nil {
		h.log.Error("stock search-and-run submit failed", zap.Error(err))
		status := http.StatusInternalServerError
		if err == stockpipeline.ErrJobsServiceRequired {
			status = http.StatusServiceUnavailable
		}
		c.JSON(status, runResponse{
			Status:    StatusError,
			Error:     err.Error(),
			ErrorCode: ErrCodeInvalidPayload,
		})
		return
	}

	resp := runResponse{Deduplicated: false}
	if jobID != "" {
		resp.Status = StatusPending
		resp.JobID = jobID
		resp.RunID = jobID
		c.JSON(http.StatusAccepted, resp)
		return
	}
	resp.Status = StatusCompleted
	c.JSON(http.StatusOK, resp)
}

// Run handles POST /api/stock-pipeline/run.
//
// Validation chain (godlike/07 fail-fast):
//  1. JSON decode with DisallowUnknownFields → UNKNOWN_FIELD or
//     generic INVALID_PAYLOAD on syntax error.
//  2. Source-presence check → INVALID_PAYLOAD.
//  3. Max-clip cap → MAX_CLIPS_EXCEEDED.
//  4. URL scheme + RFC1918 IP check on direct_urls + drive_urls →
//     INVALID_URL (rejects file://, private IPs, malformed URLs).
//  5. Path-traversal check on folder fields → PATH_TRAVERSAL.
//  6. clip_duration range check (existing, 3 ≤ d ≤ 30).
//
// On success returns 202 with {job_id, run_id, status, deduplicated}
// when async=true, or 200 with {status, deduplicated} when async is
// false or omitted. The response carries the canonical endpoint-
// acknowledgement enum (godlike/06 SSOT, see StatusPending /
// StatusCompleted above). Status naming is intentionally decoupled from
// the broker job.State enum (QUEUED / RUNNING / FINALIZING / SUCCEEDED /
// INDEX_PENDING) — clients that need broker-level status poll
// /api/jobs/{id}/full separately.
//
// Endpoint-acknowledgement enum (godlike/06 SSOT decoupling, see the
// StatusPending / StatusCompleted / StatusError constants above):
//   - status = "QUEUED" when the use case routed through the jobs
//     broker (async=true; useCase.Submit returned a non-empty jobID,
//     canonical production path). job_id + run_id are populated and
//     the HTTP response is 202.
//   - status = "completed" when the use case ran inline (async=false
//     or the async field was omitted; useCase.Submit returned no jobID).
//     job_id + run_id are empty and the HTTP response is 200.
//   - status = "error" on any 4xx/5xx response from the validation
//     chain or the use case (the `error_code` field carries the
//     machine-readable subtype).
//
// For broker-level state progression (QUEUED → LEASED → RUNNING →
// WAITING_CHILDREN → FINALIZING → SUCCEEDED | INDEX_PENDING | FAILED
// | CANCELLED) clients poll /api/jobs/{id}/full — that endpoint is
// the canonical broker-state surface (see internal/api/jobs/impl.go
// ::buildJobResponse).
//
// deduplicated is always false for the first submission; the
// idempotency followup flips it to true on a duplicate hash match.
func (h *StockHandler) Run(c *gin.Context) {
	var req runRequest

	// (1) Strict JSON decode. DisallowUnknownFields catches fields
	// the request struct doesn't declare (UNKNOWN_FIELD contract).
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		code := ErrCodeInvalidPayload
		if strings.HasPrefix(err.Error(), "json: unknown field ") {
			code = ErrCodeUnknownField
		}
		c.JSON(http.StatusBadRequest, runResponse{
			Status:    StatusError,
			Error:     "invalid JSON payload: " + err.Error(),
			ErrorCode: code,
		})
		return
	}
	// (2) Source-presence check.
	if len(req.SearchQueries) == 0 && len(req.DirectURLs) == 0 && len(req.DriveURLs) == 0 && len(req.Clips) == 0 {
		c.JSON(http.StatusBadRequest, runResponse{
			Status:    StatusError,
			Error:     "at least one of search_queries, direct_urls, drive_urls, or clips is required",
			ErrorCode: ErrCodeInvalidPayload,
		})
		return
	}

	// (3) Max-clip cap.
	if len(req.Clips) > MaxClipsPerRun {
		c.JSON(http.StatusBadRequest, runResponse{
			Status:    StatusError,
			Error:     fmt.Sprintf("too many clips requested (max %d)", MaxClipsPerRun),
			ErrorCode: ErrCodeMaxClips,
		})
		return
	}

	// (4) URL validation (scheme + private IP rejection).
	// PR-DOMAIN-PRIMITIVES-NOMINAL (July 2026): boundary wraps raw
	// string fields via primitives.NewURL so the validator signature
	// is typed end-to-end. Error messages keep the raw string for
	// operator readability.
	for _, u := range req.DirectURLs {
		if !isValidURL(primitives.NewURL(u)) {
			c.JSON(http.StatusBadRequest, runResponse{
				Status:    StatusError,
				Error:     "invalid or insecure direct_url: " + redactURL(u),
				ErrorCode: ErrCodeInvalidURL,
			})
			return
		}
	}
	for _, u := range req.DriveURLs {
		if !isValidURL(primitives.NewURL(u)) {
			c.JSON(http.StatusBadRequest, runResponse{
				Status:    StatusError,
				Error:     "invalid or insecure drive_url: " + redactURL(u),
				ErrorCode: ErrCodeInvalidURL,
			})
			return
		}
	}
	// Clip URLs undergo the same gate — a ClipSpec with source
	// `file:///...` would otherwise reach the orchestrator downstream.
	// Variable named `clip` (not `c`) to avoid shadowing the gin
	// context `c *gin.Context` used for the response.
	for _, clip := range req.Clips {
		if clip.URL != "" && !isValidURL(primitives.NewURL(clip.URL)) {
			c.JSON(http.StatusBadRequest, runResponse{
				Status:    StatusError,
				Error:     "invalid or insecure clip url: " + redactURL(clip.URL),
				ErrorCode: ErrCodeInvalidURL,
			})
			return
		}
	}
	// (5) Path traversal on folder fields.
	if !isSafePath(req.Subfolder) || !isSafePath(req.FolderName) || !isSafePath(req.DriveFolderID) || !isSafePath(req.FolderID) {
		c.JSON(http.StatusBadRequest, runResponse{
			Status:    StatusError,
			Error:     "path traversal characters detected in folder configuration",
			ErrorCode: ErrCodePathTraversal,
		})
		return
	}

	// (6) clip_duration range (3 ≤ d ≤ 30).
	if req.ClipDuration != 0 && (req.ClipDuration < 3 || req.ClipDuration > 30) {
		c.JSON(http.StatusBadRequest, runResponse{
			Status:    StatusError,
			Error:     "clip_duration must be between 3 and 30 seconds",
			ErrorCode: ErrCodeInvalidPayload,
		})
		return
	}
	if err := stockpipeline.ValidateDurationContract(req.TargetTotalDurationSeconds, req.TargetDurationPerSourceSeconds, req.ClipsPerSource, req.ClipDurationSeconds, req.DownloadMode); err != nil {
		c.JSON(http.StatusBadRequest, runResponse{Status: StatusError, Error: err.Error(), ErrorCode: ErrCodeInvalidPayload})
		return
	}
	if req.TotalMinutes <= 0 {
		req.TotalMinutes = 5
	}

	cmd := &stockpipeline.StockCommand{
		SearchQueries:                  req.SearchQueries,
		DirectURLs:                     req.DirectURLs,
		DriveURLs:                      req.DriveURLs,
		Clips:                          req.Clips,
		TotalMinutes:                   req.TotalMinutes,
		TargetTotalDurationSeconds:     req.TargetTotalDurationSeconds,
		TargetDurationPerSourceSeconds: req.TargetDurationPerSourceSeconds,
		ClipsPerSource:                 req.ClipsPerSource,
		ClipDurationSeconds:            req.ClipDurationSeconds,
		DownloadMode:                   req.DownloadMode,
		ChunkDuration:                  req.ChunkDuration,
		ClipDuration:                   req.ClipDuration,
		SecondsPerSegment:              req.SecondsPerSegment,
		NoAudio:                        req.NoAudio,
		NoEffects:                      req.NoEffects,
		NoTransitions:                  req.NoTransitions,
		MaxVideos:                      req.MaxVideos,
		Subfolder:                      req.Subfolder,
		FolderName:                     req.FolderName,
		DriveFolderID:                  req.DriveFolderID,
		FolderID:                       req.FolderID,
		Metadata:                       req.Metadata,
		Async:                          req.Async,
		Persist:                        req.Persist,
	}

	jobID, err := h.useCase.Submit(c.Request.Context(), cmd, req.Async)
	if err != nil {
		h.log.Error("stock pipeline submit failed", zap.Error(err))
		status := http.StatusInternalServerError
		if err == stockpipeline.ErrJobsServiceRequired {
			status = http.StatusServiceUnavailable
		}
		// Forward-pointer (PR-STOCK-ERROR-LEAKS-TOKEN audit): this error
		// originates in the use case, not the handler, so the URL-echo
		// redaction above cannot protect it. If a future use-case error
		// embeds source URLs, audit it here before echoing err.Error().
		c.JSON(status, runResponse{
			Status:    StatusError,
			Error:     err.Error(),
			ErrorCode: ErrCodeInvalidPayload,
		})
		return
	}

	// Endpoint-acknowledgement status (godlike/06 SSOT, decoupling
	// from broker job state). Single-write assignment: jobID != '' →
	// async path → "pending"; else sync path → "completed".
	resp := runResponse{Deduplicated: false}
	if jobID != "" {
		resp.Status = StatusPending
		resp.JobID = jobID
		resp.RunID = jobID
	} else {
		resp.Status = StatusCompleted
	}

	if jobID != "" {
		c.JSON(http.StatusAccepted, resp)
		return
	}
	c.JSON(http.StatusOK, resp)
}
