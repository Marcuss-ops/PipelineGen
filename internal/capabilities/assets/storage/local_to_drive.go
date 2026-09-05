package storage

import (
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver/transport"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// LocalToDriveRequest is the minimal canonical POST body for the
// bulk-upload-youtube-clips route. The POST is a pure enqueue: it
// validates the payload, enqueues a bulk_upload_youtube_clips job,
// and returns the job_id. The worker (BulkUploadWorker) is the sole
// owner of filesystem scanning — no pre-scan happens here.
type LocalToDriveRequest struct {
	LocalFolder   string `json:"local_folder"`
	DriveFolderID string `json:"drive_folder_id"`
	Source        string `json:"source,omitempty"`
	Category      string `json:"category,omitempty"`
	Recursive     bool   `json:"recursive,omitempty"`
	Concurrency   int    `json:"concurrency,omitempty"`
}

// LocalToDriveResponse is the immediate 202 reply: {ok, job_id, message}.
// The scan results are NOT computed here — the worker emits them when
// the job runs.
type LocalToDriveResponse struct {
	OK      bool   `json:"ok"`
	JobID   string `json:"job_id,omitempty"`
	Message string `json:"message,omitempty"`
}

func (h *Handler) LocalToDrive(c *gin.Context) {
	var req LocalToDriveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	// PR-CLIPS-ENQUEUE-ONLY (July 2026): the handler validates the
	// minimal payload and enqueues the job. No filesystem pre-scan
	// (no os.Stat, no filepath.WalkDir) happens here — the worker
	// (BulkUploadWorker) is the sole owner of filesystem scanning.
	if strings.TrimSpace(req.LocalFolder) == "" {
		apiutil.BadRequest(c, "local_folder is required")
		return
	}
	if strings.TrimSpace(req.DriveFolderID) == "" {
		apiutil.BadRequest(c, "drive_folder_id is required")
		return
	}

	source := req.Source
	if source == "" {
		source = "youtube-local"
	}

	// Recursive + concurrency + category are optional wire-shape
	// overrides; the worker uses its server-config defaults when the
	// caller omits them.
	payload := map[string]any{
		"local_folder":    req.LocalFolder,
		"drive_folder_id": strings.TrimSpace(req.DriveFolderID),
		"source":          source,
		"category":        req.Category,
		"recursive":       req.Recursive,
		"concurrency":     req.Concurrency,
	}

	h.log.Info("bulk-upload-youtube-clips: enqueue",
		zap.String("local_folder", req.LocalFolder),
		zap.String("drive_folder_id", req.DriveFolderID),
		zap.String("source", source),
		zap.String("category", req.Category),
		zap.Bool("recursive", req.Recursive),
		zap.Int("concurrency", req.Concurrency))

	if ok := transport.EnqueueAsync(c, h.jobsSvc, &transport.EnqueueInput{
		Type:    "bulk_upload_youtube_clips",
		Project: "media",
		Payload: payload,
	}, "Job enqueued."); ok {
		return
	}
	// EnqueueAsync returns false if jobsSvc is nil (503) or on error.
}
