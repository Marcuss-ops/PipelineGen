// Package clips — BulkUploadTransport sub-handler (Step 5 Split 5,
// June 2026, override ADR 0009).
//
// OVERRIDE ADR 0009 (continues from Split 1/2/3/4 chain): this commit
// extracts the bulk-upload HTTP transport + job dispatcher into a
// dedicated *BulkUploadTransport receiver. Pattern B (per-cluster
// RegisterRoutes with idem fn parameter) is used; the orchestrator
// Handler.RegisterRoutes single-calls
// bt.RegisterRoutes(r, h.idemWriter()), preserving PR8 idempotency
// semantics uniformly.
//
// DIFE SCOPE FROM OTHER RECEIVERS: BulkUploadTransport is the LAST
// capability not yet split (after Search/Ingest/Action-delayed/Ops).
// It owns TWO distinct surfaces under one receiver:
//
//   - HTTP transport (BulkUploadYouTubeClips): validates the request,
//     computes the candidate count via appclips.ScanLocalClips
//     (Pattern 8 — no scanner logic in the transport), and enqueues
//     a "bulk_upload_youtube_clips" job. All heavy work happens in
//     the job worker (appclips.BulkUploadWorker) — the transport
//     is thin HTTP shell per AGENTS.md Pattern 8.
//   - Job dispatcher (HandleBulkUploadYouTubeClipsJob): routes the
//     "bulk_upload_youtube_clips" job into the worker's HandleJob
//     without itself importing the worker internals.
//
// BulkTransportDeps (5 deps, intentionally larger than ingest/ops
// because the transport still owns Drive-folder-name resolution + Cfg
// path lookups — these will move to appclips in a follow-up PR to
// reach full Pattern 8 compliance):
//
//   - JobsSvc          (bulk-upload enqueue + idem bypass)
//   - DriveAdmin    (Drive folder-name resolution when caller
//     omits drive_folder_id but provides
//     drive_folder_name)
//   - Cfg              (storage base paths for IsLocalFolderAllowed;
//     Drive clips / root folder fallback)
//   - BulkUploadWorker (job dispatcher → BulkUploadWorker.HandleJob)
//   - Log              (logging)
//
// ROUTE TABLE (2 routes, both write+idem-installed by orchestrator):
//
//	POST /:source/clips/bulk-upload-youtube-clips
//	                           -> BulkUploadYouTubeClips (write+idem)
//	Job type "bulk_upload_youtube_clips"
//	                           -> HandleBulkUploadYouTubeClipsJob
//	                              (runs the heavy worker)
package clips

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/api/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// BulkTransportDeps is the constructor bag for BulkUploadTransport.
// The 5 fields below are exactly the deps the moved HTTP transport
// + job dispatcher touch — no more, no less. Cluster ownership
// follows the matrix in the Step 5 discovery report (June 2026).
type BulkTransportDeps struct {
	JobsSvc          jobservice.Service
	DriveAdmin       drive.Admin
	Cfg              *config.Config
	BulkUploadWorker *appclips.BulkUploadWorker
	Publisher        delivery.Publisher
	Log              *zap.Logger
}

// BulkUploadTransport owns the 2 surfaces inherited from the
// pre-Split-5 inline-on-Handler BulkUploadYouTubeClips +
// HandleBulkUploadYouTubeClipsJob. Receiver-on-pattern-B: constructed
// in NewHandler from a BulkTransportDeps shape extracted from the
// orchestrator Deps.
type BulkUploadTransport struct {
	jobsSvc          jobservice.Service
	driveAdmin       drive.Admin
	cfg              *config.Config
	bulkUploadWorker *appclips.BulkUploadWorker
	publisher        delivery.Publisher
	log              *zap.Logger
}

// NewBulkUploadTransport constructs a BulkUploadTransport with the
// supplied BulkTransportDeps. Nil fields are tolerated for test
// fixtures where the transport is not actively exercised; production
// wiring supplies all 5 via the orchestrator Deps shape.
func NewBulkUploadTransport(d BulkTransportDeps) *BulkUploadTransport {
	if d.Log == nil {
		d.Log = zap.NewNop()
	}
	return &BulkUploadTransport{
		jobsSvc:          d.JobsSvc,
		driveAdmin:       d.DriveAdmin,
		cfg:              d.Cfg,
		bulkUploadWorker: d.BulkUploadWorker,
		publisher:        d.Publisher,
		log:              d.Log,
	}
}

// RegisterRoutes installs the single bulk-upload HTTP route on the
// supplied gin router group. The route is read+idem-protected per
// PR8 because it enqueues an async worker job.
//
// Route table:
//
//	POST /:source/clips/bulk-upload-youtube-clips
//	                            -> BulkUploadYouTubeClips (write+idem)
func (bt *BulkUploadTransport) RegisterRoutes(r *gin.RouterGroup, idem gin.HandlerFunc) {
	r.POST("/:source/clips/bulk-upload-youtube-clips", idem, bt.BulkUploadYouTubeClips)
}

// ──────────────────────────────────────────────────────────────────────
// BULK UPLOAD HTTP TRANSPORT (DRIFT-CLIPS-BULK-SPLIT-5, June 2026)
// ──────────────────────────────────────────────────────────────────────

// BulkUploadYouTubeClipsRequest is declared in bulk_upload.go.

// BulkUploadYouTubeClipsResponse is declared in bulk_upload.go.

// HandleBulkUploadYouTubeClipsJob is the "bulk_upload_youtube_clips"
// job dispatcher. The orchestrator *Handler.RegisterJobHandlers
// single-calls bt.HandleBulkUploadYouTubeClipsJob when registering
// the job handler with h.bulkUploadWorker.
//
// Returns the worker's map[string]any result verbatim. nil
// BulkUploadWorker's surface a 503-equivalent error to the broker.
func (bt *BulkUploadTransport) HandleBulkUploadYouTubeClipsJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	if bt.bulkUploadWorker == nil {
		return nil, fmt.Errorf("bulk upload worker not configured")
	}
	return bt.bulkUploadWorker.HandleJob(ctx, j, tools)
}

// BulkUploadYouTubeClips handles POST /api/clips/:source/bulk-upload-youtube-clips.
//
// Validates the request, scans the local folder to count candidates
// (via appclips.ScanLocalClips — Pattern 8: scanner logic lives in
// the application tier), then enqueues a "bulk_upload_youtube_clips"
// job and returns the job_id. All heavy lifting (uploads, embeddings,
// Qdrant) happens in the job worker.
//
// Status codes:
//
//	202 — job enqueued; poll GET /api/jobs/{id}/full for progress.
//	200 — dry-run: returned preview only, no job enqueued.
//	400 — invalid request body, missing drive_folder_id / name,
//	      folder not under any allowed storage base path.
//	500 — drive uploader not configured when folder name resolution
//	      is needed, or no Drive root configured.
//	503 — JobsSvc not configured (regression from the
//	      EnqueueAsync fallback in transport package).
func (bt *BulkUploadTransport) BulkUploadYouTubeClips(c *gin.Context) {
	var req BulkUploadYouTubeClipsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	// Defaults
	subdirAsDriveSubdir := true
	if req.SubdirAsDriveSubdir != nil {
		subdirAsDriveSubdir = *req.SubdirAsDriveSubdir
	}
	recursive := true
	if req.Recursive != nil {
		recursive = *req.Recursive
	}
	if req.Source == "" {
		req.Source = "youtube-local"
	}
	if req.Concurrency <= 0 {
		req.Concurrency = 2
	}
	if req.Concurrency > 8 {
		req.Concurrency = 8 // sanity cap to avoid Drive rate limits
	}
	if len(req.FilePatterns) == 0 {
		req.FilePatterns = []string{"*.mp4"}
	}

	// Validation
	if strings.TrimSpace(req.LocalFolder) == "" {
		apiutil.BadRequest(c, "local_folder is required")
		return
	}
	abs, err := filepath.Abs(req.LocalFolder)
	if err != nil {
		apiutil.BadRequest(c, "invalid local_folder: "+err.Error())
		return
	}
	info, err := osStat(abs)
	if err != nil {
		apiutil.BadRequest(c, fmt.Sprintf("local_folder not accessible: %v", err))
		return
	}
	if !info.IsDir() {
		apiutil.BadRequest(c, "local_folder is not a directory")
		return
	}
	if req.DriveFolderID == "" && req.DriveFolderName == "" {
		apiutil.BadRequest(c, "either drive_folder_id or drive_folder_name is required")
		return
	}

	ctx := c.Request.Context()
	log := bt.log.With(
		zap.String("handler", "bulk-upload-youtube-clips"),
		zap.String("local_folder", abs),
	)

	// Scan to count candidates (so the response includes a useful preview).
	// Scanner logic lives in the application tier (Pattern 8).
	candidates, scanErr := appclips.ScanLocalClips(abs, recursive, req.FilePatterns, req.SkipPatterns, req.Limit)
	if scanErr != nil {
		apiutil.BadRequest(c, "failed to scan local_folder: "+scanErr.Error())
		return
	}
	log.Info("scanned local folder",
		zap.Int("candidates", len(candidates)),
		zap.Bool("dry_run", req.DryRun),
		zap.Bool("recursive", recursive))

	// Dry-run: return preview without enqueueing.
	if req.DryRun {
		apiutil.OK(c, BulkUploadYouTubeClipsResponse{
			OK:         true,
			DryRun:     true,
			LocalFound: len(candidates),
			Message:    "dry run: no job enqueued, candidate count returned",
		})
		return
	}

	// Resolve target Drive folder once so the worker doesn't have to.
	targetDriveFolderID := strings.TrimSpace(req.DriveFolderID)
	if targetDriveFolderID == "" {
		if bt.publisher == nil {
			apiutil.InternalError(c, fmt.Errorf("publisher not configured; drive_folder_id is required"))
			return
		}
		if req.DriveFolderName == "" {
			apiutil.BadRequest(c, "either drive_folder_id or drive_folder_name is required")
			return
		}
		dirID, err := bt.publisher.ResolveFolder(ctx, delivery.PublishRequest{
			Destination: delivery.DestinationYouTubeClip,
			Group:       req.DriveFolderName,
			Subject:     "_batch",
		})
		if err != nil {
			apiutil.InternalError(c, fmt.Errorf("failed to resolve drive_folder_name: %w", err))
			return
		}
		targetDriveFolderID = dirID
		log.Info("resolved Drive folder by name via Publisher",
			zap.String("name", req.DriveFolderName),
			zap.String("folder_id", targetDriveFolderID))
	}

	// Security: local_folder must be under a configured storage base path
	// to prevent the endpoint from being used to walk arbitrary
	// directories (e.g. /etc) and upload their contents to Drive.
	// Path resolution lives in the application tier (Pattern 8 in
	// progress; full Cfg/DriveAdmin migration to follow-up PR).
	if !appclips.IsLocalFolderAllowed(abs,
		bt.cfg.Storage.MediaPath(),
		bt.cfg.Storage.TempPath(),
		bt.cfg.Storage.DataDir,
	) {
		apiutil.BadRequest(c, fmt.Sprintf(
			"local_folder %q is not under any allowed base path (drive.media_dir, drive.temp_dir, drive.data_dir, or a path explicitly added via config)",
			abs,
		))
		return
	}

	// Enqueue the job.
	activeKey := fmt.Sprintf("bulk_upload_yt:%s", abs)
	if ok := transport.EnqueueAsync(c, bt.jobsSvc, &transport.EnqueueInput{
		Type:    "bulk_upload_youtube_clips",
		Project: "media",
		Payload: map[string]any{
			"local_folder":           abs,
			"drive_folder_id":        targetDriveFolderID,
			"source":                 req.Source,
			"category":               req.Category,
			"subdir_as_drive_subdir": subdirAsDriveSubdir,
			"recursive":              recursive,
			"limit":                  req.Limit,
			"skip_upload":            req.SkipUpload,
			"skip_embeddings":        req.SkipEmbeddings,
			"skip_qdrant":            req.SkipQdrant,
			"concurrency":            req.Concurrency,
			"file_patterns":          req.FilePatterns,
			"skip_patterns":          req.SkipPatterns,
		},
		ActiveKey: activeKey,
	}, fmt.Sprintf("bulk upload job enqueued (%d candidates)", len(candidates))); ok {
		return
	}
	// EnqueueAsync returns false if jobsSvc is nil (503) or on error.
}

// osStat is a thin os.Stat indirection so the file can be unit-tested
// without filesystem shenanigans. Production wiring uses os.Stat directly.
func osStat(path string) (osDirEntry, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	return &osStatInfo{fi: info}, nil
}

// osDirEntry is the minimal subset of os.FileInfo the transport
// uses (just IsDir). Production implements via os.Stat; tests can mock.
type osDirEntry interface {
	IsDir() bool
}

type osStatInfo struct {
	fi os.FileInfo
}

func (s *osStatInfo) IsDir() bool { return s.fi.IsDir() }
