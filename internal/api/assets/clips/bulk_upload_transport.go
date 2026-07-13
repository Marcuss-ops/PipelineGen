// Package clips — BulkUploadTransport sub-handler.
//
// PR-13 (July 2026, refactor(api): drop runtime-tunable noise configs):
// the transport is a thin HTTP shell: validate the 4-field canonical
// payload, enqueue a "bulk_upload_youtube_clips" job, return the job_id.
// Scanner, dry-run preview, folder-name resolution, and runtime-tunable
// flags (skip_*, subdir_as_drive_subdir, file_patterns, skip_patterns,
// concurrency, limit, dry_run, drive_folder_name) are GONE.
//
// The 6 stable operational choices (extension list, recursion, default
// concurrency, Drive layout, indexing policy, stage-root) live in server
// config. The client says WHAT to process, not HOW.
//
// godlike/07 No-fake-availability: the transport refuses to drop into a
// "delegate to publisher to resolve folder by name" fallback. The caller
// either supplies drive_folder_id or receives a 400.
//
// Pattern 8 (transport = thin HTTP shell): heavy work happens in the
// job worker (appclips.BulkUploadWorker). The transport NEVER scans the
// filesystem, NEVER calls Publisher, NEVER creates an upload.
package clips

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/api/transport"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"

	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// BulkUploadYouTubeClipsRequest is the canonical JSON body for
// POST /:source/clips/bulk-upload-youtube-clips. The client says WHAT
// to process; runtime tuning lives in server config.
type BulkUploadYouTubeClipsRequest struct {
	LocalFolder   string `json:"local_folder"`
	DriveFolderID string `json:"drive_folder_id"`
	Source        string `json:"source,omitempty"`
	Category      string `json:"category,omitempty"`
}

// BulkUploadYouTubeClipsResponse is the immediate 202 response after
// enqueueing the job. The client polls progress through
// GET /api/jobs/{id}/full (QDRANT-002 async invariant).
type BulkUploadYouTubeClipsResponse struct {
	OK        bool   `json:"ok"`
	JobID     string `json:"job_id"`
	StatusURL string `json:"status_url"`
	Message   string `json:"message"`
}

// BulkTransportDeps is the constructor bag for BulkUploadTransport.
// PR-13 (July 2026): Publisher + DriveAdmin + Cfg dropped — Drive folder
// resolution is not a transport responsibility. The remaining fields are
// the JobsSvc (enqueue + idem), the 3 storage base paths (for
// IsLocalFolderAllowed), the BulkUploadWorker (job dispatcher), and
// Log. Pass primitives directly — no nested config abstraction
// (Card 13 explicitly bans new wrappers).
type BulkTransportDeps struct {
	JobsSvc          jobservice.Service
	MediaPath        string
	TempPath         string
	DataDir          string
	BulkUploadWorker *appclips.BulkUploadWorker
	Log              *zap.Logger
}

// BulkUploadTransport owns the HTTP + job dispatcher surface for the
// single bulk-upload-youtube-clips route. Pattern B (per-cluster
// RegisterRoutes with idem fn parameter).
type BulkUploadTransport struct {
	jobsSvc          jobservice.Service
	mediaPath        string
	tempPath         string
	dataDir          string
	bulkUploadWorker *appclips.BulkUploadWorker
	log              *zap.Logger
}

// NewBulkUploadTransport constructs a BulkUploadTransport with the
// supplied BulkTransportDeps. Logger nil → zap.NewNop().
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

// RegisterRoutes installs the single bulk-upload HTTP route on the
// supplied gin router group. Write+idem-protected per PR8.
//
//	POST /:source/clips/bulk-upload-youtube-clips
//	                            -> BulkUploadYouTubeClips (write+idem)
func (bt *BulkUploadTransport) RegisterRoutes(r *gin.RouterGroup, idem gin.HandlerFunc) {
	r.POST("/:source/clips/bulk-upload-youtube-clips", idem, bt.BulkUploadYouTubeClips)
}

// HandleBulkUploadYouTubeClipsJob is the "bulk_upload_youtube_clips"
// job dispatcher.
func (bt *BulkUploadTransport) HandleBulkUploadYouTubeClipsJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	if bt.bulkUploadWorker == nil {
		return nil, fmt.Errorf("bulk upload worker not configured")
	}
	return bt.bulkUploadWorker.HandleJob(ctx, j, tools)
}

// BulkUploadYouTubeClips handles POST /api/clips/:source/bulk-upload-youtube-clips.
//
//	202 — job enqueued; poll GET /api/jobs/{id}/full for progress.
//	400 — invalid request body, empty local_folder, empty drive_folder_id,
//	      folder not under any allowed storage base path.
//	503 — JobsSvc not configured.
func (bt *BulkUploadTransport) BulkUploadYouTubeClips(c *gin.Context) {
	// PR-DIAG-BULKUPLOAD-REGISTRATION (July 2026, diagnostic-only):
	// log bt.jobsSvc pointer at handler entry so a future "no handler
	// registered" reproduction can compare this transport-side pointer
	// against the descriptor-side svc_ptr (logged inside
	// ClipsDescriptor.RegisterJobHandlers at boot) and the
	// jobs_service_ptr (logged inside WireAssets at composition time).
	// All three should match when the canonical wiring is correct; a
	// mismatch localizes the split. Diagnostic only — no behavioural
	// change; retire in a follow-up commit once the upstream bug is fixed.
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

	// Security: local_folder must be under a configured storage base path
	// to prevent the endpoint from being used to walk arbitrary
	// directories (e.g. /etc) and upload their contents to Drive.
	if !appclips.IsLocalFolderAllowed(req.LocalFolder,
		bt.mediaPath,
		bt.tempPath,
		bt.dataDir,
	) {
		apiutil.BadRequest(c, fmt.Sprintf(
			"local_folder %q is not under any allowed base path (drive.media_dir, drive.temp_dir, drive.data_dir, or a path explicitly added via config)",
			req.LocalFolder,
		))
		return
	}

	activeKey := fmt.Sprintf("bulk_upload_yt:%s", req.LocalFolder)
	if ok := transport.EnqueueAsync(c, bt.jobsSvc, &transport.EnqueueInput{
		Type:    string(jobservice.TypeBulkUploadYouTubeClips),
		Project: "media",
		Payload: map[string]any{
			"local_folder":    req.LocalFolder,
			"drive_folder_id": req.DriveFolderID,
			"source":          req.Source,
			"category":        req.Category,
		},
		ActiveKey: activeKey,
	}, "bulk upload job enqueued"); ok {
		return
	}
}
