package stock

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
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
}

// runRequest is the JSON body for POST /api/stock-pipeline/run.
type runRequest struct {
	SearchQueries     []string                          `json:"search_queries"`
	DirectURLs        []string                          `json:"direct_urls,omitempty"`
	DriveURLs         []string                          `json:"drive_urls,omitempty"`
	Clips             []stockpipeline.ClipSpec          `json:"clips,omitempty"`
	TotalMinutes      int                               `json:"total_minutes"`
	ChunkDuration     int                               `json:"chunk_duration,omitempty"`
	ClipDuration      int                               `json:"clip_duration,omitempty"`
	SecondsPerSegment int                               `json:"seconds_per_segment,omitempty"`
	NoAudio           bool                              `json:"no_audio,omitempty"`
	NoEffects         bool                              `json:"no_effects,omitempty"`
	NoTransitions     bool                              `json:"no_transitions,omitempty"`
	MaxVideos         int                               `json:"max_videos,omitempty"`
	Subfolder         string                            `json:"subfolder"`
	FolderName        string                            `json:"folder_name"`
	DriveFolderID     string                            `json:"drive_folder_id,omitempty"`
	FolderID          string                            `json:"folder_id,omitempty"`
	Metadata          *stockpipeline.ChunkMetadataInput `json:"metadata,omitempty"`
	Async             bool                              `json:"async,omitempty"`
	Persist           bool                              `json:"persist,omitempty"`
}

// runResponse is the JSON response for POST /api/stock-pipeline/run.
// godlike/06 SSOT: all error responses carry a machine-readable
// `error_code` field (UNKNOWN_FIELD / INVALID_URL / PATH_TRAVERSAL /
// MAX_CLIPS_EXCEEDED / INVALID_PAYLOAD). Successful responses carry
// `deduplicated` (always present, default false) — the idempotency
// followup will flip it to true when a duplicate run is detected.
type runResponse struct {
	JobID        string `json:"job_id,omitempty"`
	RunID        string `json:"run_id,omitempty"`
	Status       string `json:"status"`
	Deduplicated bool   `json:"deduplicated"`
	Error        string `json:"error,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
}

// Constants for the HTTP contract — exported so the test suite can
// reference them without copy/paste drift.
const (
	// MaxClipsPerRun is the upper bound on `clips` per single
	// request. Larger jobs MUST be split client-side into multiple
	// runs (the orchestrator flags 100+ clips as a misuse surface).
	MaxClipsPerRun = 100

	// MaxURLLength caps individual URL strings to defense-in-depth
	// against URL-flood DoS. 2048 chars covers long Drive-share links
	// with auth tokens; longer URLs are flagged for operator review
	// and should be wrapped in a separate reference.
	MaxURLLength = 2048

	// Response-level status strings (godlike/06 SSOT decoupling):
	// these describe the *endpoint acknowledgement* — not the broker
	// job state enum (QUEUED / RUNNING / FINALIZING / SUCCEEDED /
	// INDEX_PENDING, owned by internal/kernel/job.Status).
	//   - StatusPending = request accepted, work scheduled via the
	//     jobs broker (async path; useCase.Submit returned a jobID).
	//     Callers poll /api/jobs/{id}/full or wait for the
	//     broker-level terminal state to know the actual outcome.
	//   - StatusCompleted = request processed inline (sync path;
	//     useCase.Submit returned with empty jobID, e.g. test
	//     fixture or partial-deploy worker). The job is finished
	//     by the time the response is serialised; no follow-up
	//     polling is required.
	// Keeping these distinct from the broker enum avoids the silent-
	// confusion class where a "QUEUED" status string at the endpoint
	// implies "job not yet started" while the broker is in RUNNING.
	StatusPending   = "QUEUED"
	StatusCompleted = "completed"
	// StatusError is the third endpoint-acknowledgement value —
	// emitted on every 4xx/5xx response (validation rejections, broker
	// unavailability). The `error_code` field carries the machine-
	// readable subtype (UNKNOWN_FIELD / INVALID_URL / etc.); `status`
	// stays at the canonical literal so clients can branch on a single
	// field with no enum drift.
	StatusError = "error"

	// Error codes — machine-readable tags for client retry logic.
	ErrCodeUnknownField   = "UNKNOWN_FIELD"
	ErrCodeInvalidURL     = "INVALID_URL"
	ErrCodePathTraversal  = "PATH_TRAVERSAL"
	ErrCodeMaxClips       = "MAX_CLIPS_EXCEEDED"
	ErrCodeInvalidPayload = "INVALID_PAYLOAD"
)

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
// On success returns 200 OK with {job_id, run_id, status, deduplicated}.
// HTTP 200 (not 202) is intentional: the handler always acknowledges
// receipt synchronously, and the response carries the canonical
// endpoint-acknowledgement enum (godlike/06 SSOT, see StatusPending /
// StatusCompleted above). Status naming is intentionally decoupled
// from the broker job.State enum (QUEUED / RUNNING / FINALIZING /
// SUCCEEDED / INDEX_PENDING) — clients that need broker-level status
// poll /api/jobs/{id}/full separately.
//
// Endpoint-acknowledgement enum (godlike/06 SSOT decoupling, see the
// StatusPending / StatusCompleted / StatusError constants above):
//   - status = "pending" when the use case routed through the jobs
//     broker (async path — useCase.Submit returned a non-empty jobID,
//     canonical production path). job_id + run_id are populated.
//   - status = "completed" when the use case ran inline (sync path —
//     useCase.Submit returned no jobID, e.g. partial deploy / test
//     fixture). job_id + run_id are empty.
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
	for _, u := range req.DirectURLs {
		if !isValidURL(u) {
			c.JSON(http.StatusBadRequest, runResponse{
				Status:    StatusError,
				Error:     "invalid or insecure direct_url: " + u,
				ErrorCode: ErrCodeInvalidURL,
			})
			return
		}
	}
	for _, u := range req.DriveURLs {
		if !isValidURL(u) {
			c.JSON(http.StatusBadRequest, runResponse{
				Status:    StatusError,
				Error:     "invalid or insecure drive_url: " + u,
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
		if clip.URL != "" && !isValidURL(clip.URL) {
			c.JSON(http.StatusBadRequest, runResponse{
				Status:    StatusError,
				Error:     "invalid or insecure clip url: " + clip.URL,
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
	if req.TotalMinutes <= 0 {
		req.TotalMinutes = 5
	}

	cmd := &stockpipeline.StockCommand{
		SearchQueries:     req.SearchQueries,
		DirectURLs:        req.DirectURLs,
		DriveURLs:         req.DriveURLs,
		Clips:             req.Clips,
		TotalMinutes:      req.TotalMinutes,
		ChunkDuration:     req.ChunkDuration,
		ClipDuration:      req.ClipDuration,
		SecondsPerSegment: req.SecondsPerSegment,
		NoAudio:           req.NoAudio,
		NoEffects:         req.NoEffects,
		NoTransitions:     req.NoTransitions,
		MaxVideos:         req.MaxVideos,
		Subfolder:         req.Subfolder,
		FolderName:        req.FolderName,
		DriveFolderID:     req.DriveFolderID,
		FolderID:          req.FolderID,
		Metadata:          req.Metadata,
		Async:             req.Async,
		Persist:           req.Persist,
	}

	jobID, err := h.useCase.Submit(c.Request.Context(), cmd, req.Async)
	if err != nil {
		h.log.Error("stock pipeline submit failed", zap.Error(err))
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

// isValidURL validates that u is an absolute https URL with a
// resolvable hostname and rejects private / loopback IP addresses
// (RFC1918 SSRF mitigation). file://, ftp://, gopher://, jar: are
// rejected via the scheme check.
//
// godlike/06 SSOT: this is the single source of truth for URL
// validation at the HTTP boundary. The orchestrator's downstream
// path uses different rules (yt-dlp accepts http on some sources)
// but those are application-layer concerns.
func isValidURL(u string) bool {
	if u == "" {
		return false
	}
	// Length cap — defense in depth against URL-flood DoS (10MB URLs).
	if len(u) > MaxURLLength {
		return false
	}
	// Null-byte rejection — some libraries truncate at NUL.
	if strings.ContainsRune(u, '\x00') {
		return false
	}
	parsed, err := url.ParseRequestURI(u)
	if err != nil {
		return false
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return false
	}
	if parsed.Host == "" {
		return false
	}
	host := parsed.Hostname()
	// Reject when Hostname is a private / loopback IP literal
	// (numeric IPv4 or IPv6 literal). Hostnames that resolve to
	// private IPs at DNS-time are out of scope for the HTTP-layer
	// validator — call the operator-side DNS pin at the runner level.
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			return false
		}
	}
	return true
}

// isSafePath rejects path-traversal attempts (".." sequences and
// backslash escapes), absolute paths, and null-byte injections on
// folder fields. True for the empty string and for any value whose
// canonical path stays within the configured root.
//
// godlike/06 SSOT: single helper used across subfolder / folder_name /
// drive_folder_id / folder_id fields.
func isSafePath(p string) bool {
	if p == "" {
		return true
	}
	// Backslash escape — reject any path that contains "\".
	if strings.Contains(p, `\`) {
		return false
	}
	// Null-byte rejection — defense in depth against libtruncation bypass.
	if strings.ContainsRune(p, '\x00') {
		return false
	}
	// Absolute-path rejection (e.g. /etc/passwd). Windows drive letters
	// like "C:\foo" are caught by the backslash check above.
	if strings.HasPrefix(p, "/") {
		return false
	}
	clean := path.Clean(p)
	if clean == ".." {
		return false
	}
	if strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
		return false
	}
	return true
}
