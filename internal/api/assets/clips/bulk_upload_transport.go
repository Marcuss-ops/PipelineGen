// Package clips — BulkUploadTransport: thin HTTP shell for the single
// bulk-upload-youtube-clips route. Heavy work runs in appclips.BulkUploadWorker.
// The 6 runtime choices (recursion, filter, concurrency, layout, indexing,
// stage-root) live in server config; the transport ONLY validates the 4-field
// payload and enqueues. Per godlike/07, drive_folder_id is mandatory (no
// "delegate to publisher" fallback).
package clips

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/api/transport"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"

	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// BulkUploadYouTubeClipsRequest: canonical POST body.
type BulkUploadYouTubeClipsRequest struct {
	LocalFolder   string `json:"local_folder"`
	DriveFolderID string `json:"drive_folder_id"`
	Source        string `json:"source,omitempty"`
	Category      string `json:"category,omitempty"`
	Recursive     bool   `json:"recursive,omitempty"`
	Concurrency   int    `json:"concurrency,omitempty"`
}

// BulkUploadYouTubeClipsResponse: immediate 202 with job_id + status URL.
type BulkUploadYouTubeClipsResponse struct {
	OK        bool   `json:"ok"`
	JobID     string `json:"job_id"`
	StatusURL string `json:"status_url"`
	Message   string `json:"message"`
}

// BulkTransportDeps: JobsSvc + 3 allowed storage base paths + worker + log.
type BulkTransportDeps struct {
	JobsSvc          job.Service
	MediaPath        string
	TempPath         string
	DataDir          string
	BulkUploadWorker *appclips.BulkUploadWorker
	Log              *zap.Logger
}

// BulkUploadTransport owns the HTTP + job-dispatcher surface for the single
// bulk-upload-youtube-clips route.
type BulkUploadTransport struct {
	jobsSvc          job.Service
	mediaPath        string
	tempPath         string
	dataDir          string
	bulkUploadWorker *appclips.BulkUploadWorker
	log              *zap.Logger
}

func NewBulkUploadTransport(d BulkTransportDeps) *BulkUploadTransport {
	if d.Log == nil {
		d.Log = zap.NewNop()
	}
	return &BulkUploadTransport{
		jobsSvc:          d.JobsSvc,
		mediaPath:        d.MediaPath,
		tempPath:         d.TempPath,
		dataDir:          d.DataDir,
		bulkUploadWorker: d.BulkUploadWorker,
		log:              d.Log,
	}
}

// RegisterRoutes installs the single bulk-upload HTTP route (write+idem protected).
func (bt *BulkUploadTransport) RegisterRoutes(r *gin.RouterGroup, idem gin.HandlerFunc) {
	r.POST("/:source/clips/bulk-upload-youtube-clips", idem, bt.BulkUploadYouTubeClips)
}

// HandleBulkUploadYouTubeClipsJob is the job-handler dispatcher.
func (bt *BulkUploadTransport) HandleBulkUploadYouTubeClipsJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	if bt.bulkUploadWorker == nil {
		return nil, fmt.Errorf("bulk upload worker not configured")
	}
	return bt.bulkUploadWorker.HandleJob(ctx, j, tools)
}

// BulkUploadYouTubeClips handles POST /api/clips/:source/bulk-upload-youtube-clips.
//
//	202 — job enqueued; poll /api/jobs/{id}/full for progress.
//	400 — invalid body, empty local_folder/drive_folder_id, folder not under allowed base path.
//	503 — JobsSvc not configured.
func (bt *BulkUploadTransport) BulkUploadYouTubeClips(c *gin.Context) {
	// Diagnostic ping (1 per request): jobsSvc pointer for upstream wiring isolation.
	if bt.log != nil {
		bt.log.Info("bulk-upload: handler-entry jobs-service snapshot",
			zap.String("bt_jobsSvc_type", fmt.Sprintf("%T", bt.jobsSvc)),
			zap.String("bt_jobsSvc_ptr", fmt.Sprintf("%p", bt.jobsSvc)),
		)
	}
	var req BulkUploadYouTubeClipsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	if req.LocalFolder == "" {
		apiutil.BadRequest(c, "local_folder is required")
		return
	}
	if req.DriveFolderID == "" {
		apiutil.BadRequest(c, "drive_folder_id is required")
		return
	}
	if req.Source == "" {
		req.Source = "youtube-local"
	}

	// Security: local_folder must be under a configured storage base path.
	if !appclips.IsLocalFolderAllowed(req.LocalFolder,
		bt.mediaPath, bt.tempPath, bt.dataDir,
	) {
		apiutil.BadRequest(c, fmt.Sprintf(
			"local_folder %q is not under any allowed base path (drive.media_dir, drive.temp_dir, drive.data_dir, or a path explicitly added via config)",
			req.LocalFolder,
		))
		return
	}

	activeKey := fmt.Sprintf("bulk_upload_yt:%s", req.LocalFolder)
	if ok := transport.EnqueueAsync(c, bt.jobsSvc, &transport.EnqueueInput{
		Type:    string(media.TypeBulkUploadYouTubeClips),
		Project: "media",
		Payload: map[string]any{
			"local_folder":    req.LocalFolder,
			"drive_folder_id": req.DriveFolderID,
			"source":          req.Source,
			"category":        req.Category,
			"recursive":       req.Recursive,
			"concurrency":     req.Concurrency,
		},
		ActiveKey: activeKey,
	}, "bulk upload job enqueued"); ok {
		return
	}
}
