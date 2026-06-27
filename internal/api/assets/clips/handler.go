// Package clips hosts the unified HTTP handler that owns every clip-related
// endpoint. PR-A Phase 4 BULK consolidation: a single Handler struct carries
// the full 14-dep surface and exposes every method previously scattered
// across handler_sources_clip_*.go in the flat sources package.
//
// Sub-handler fan-out (DeleteHandler, SearchHandler) is replaced by
// receivers on *Handler — there is no longer a need for nested structs.
// SourcesHandler keeps a single *clips.Handler field and delegates each
// clip-route registration to clips.Handler.{CreateClip, GetClip, ...}.
package clips

import (
	"context"
	"fmt"
	"io"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	appclipssearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/clipssearch"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"

	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
)

// Deps is the constructor bag for Handler. Keeping deps in a struct
// rather than 14 positional arguments makes wiring sites readable and
// future dep additions non-breaking.
// VectorStore field removed from handler deps.
type Deps struct {
	SourceResolver *artifacts.SourceResolver
	AssetRepo      asset.Repository
	ClipsRepo      *assets.ClipsRepository
	StockRepo      *assets.ClipsRepository
	ArtlistRepo    *assets.ClipsRepository
	DeletionSvc    *deletion.DeletionService
	DriveUploader  *drive.Uploader
	MediaProcessor asset.Processor
	AssetTreeSvc   *assettree.Service
	MetaWriter     *semantic.MetadataWriter
	ClipIndexer    *clipindexer.Service
	JobsSvc        jobservice.Service
	Cfg            *config.Config
	Log            *zap.Logger
	// VoiceoverRepo enables the voiceover-source branch in DownloadClip.
	// Nil-tolerated so absence of voiceover wiring never crashes the chain.
	VoiceoverRepo *assets.VoiceoversRepository
	// ImagesRepo enables the "source=images" branch in ListClips.
	// Nil-tolerated; nil means GET /:source/clips for that source returns 400.
	ImagesRepo *assets.ImagesRepository
	// ArtifactSvc streams uploaded files through content-addressed drive.
	// Used by UploadVideoClip. Nil means POST /upload-video returns 500.
	ArtifactSvc *artifacts.Service
	// FolderMemSvc supports manifest regeneration heuristics.
	FolderMemSvc *foldermemory.Service
	// SearchSvc owns advanced multi-source clip search.
	SearchSvc *appclipssearch.Service
	// ProcessRunner executes external subprocesses (ffprobe, mediainfo, etc.).
	ProcessRunner appassets.ProcessRunner
	// Dispatcher is the application port (NOT the concrete
	// *outbox.Dispatcher) for QDRANT-002 routing. When non-nil,
	// UpdateClip routes through port.EnqueueAndIndex instead of raw
	// repo.UpsertClip. Nil-tolerated for test fixtures.
	//
	// Depends on appclips.ClipIndexDispatcherPort to keep this
	// handler as thin transport per AGENTS.md Pattern 8 (API must
	// not import concrete infrastructure). The composition root
	// (`internal/app`) wires a clipsDispatcherAdapter that wraps
	// the concrete *outbox.Dispatcher.
	Dispatcher appclips.ClipIndexDispatcherPort

	// BulkUploadWorker is the canonical port-based worker for the
	// "bulk_upload_youtube_clips" job. W14 PR2 slice 3 (June 2026):
	// replaces the in-handler HandleBulkUploadYouTubeClipsJob method
	// that previously lived in api/assets/clips/bulk_upload_worker.go.
	// Nil-tolerated so test fixtures can opt out.
	BulkUploadWorker *appclips.BulkUploadWorker
}

// Handler owns every clip-related HTTP method. One receiver per method;
// no nested struct fan-out.
type Handler struct {
	// PR8 (June 2026): Idempotency is the reusable Gin idempotency
	// middleware (constructed once at server boot via NewHandler →
	// WireAssets → BuildRepoBundle.IdempotencyStore). Nil-tolerated so
	// test fixtures can opt out. Only WRITE routes (POST/PUT/PATCH/DELETE
	// on /clips/* and the upload/bulk routes) install it — READ routes
	// fall through unchanged.
	Idempotency gin.HandlerFunc

	sourceResolver *artifacts.SourceResolver
	assetRepo      asset.Repository
	clipsRepo      *assets.ClipsRepository
	stockRepo      *assets.ClipsRepository
	artlistRepo    *assets.ClipsRepository
	deletionSvc    *deletion.DeletionService
	driveUploader  *drive.Uploader
	mediaProcessor asset.Processor
	assetTreeSvc   *assettree.Service
	metaWriter     *semantic.MetadataWriter
	clipIndexer    *clipindexer.Service
	jobsSvc        jobservice.Service
	cfg            *config.Config
	log            *zap.Logger
	// voiceoverRepo is mirrored from Deps.VoiceoverRepo via NewHandler
	voiceoverRepo *assets.VoiceoversRepository
	// imagesRepo mirrors Deps.ImagesRepo. Same late-binding semantics.
	imagesRepo *assets.ImagesRepository
	// artifactSvc mirrors Deps.ArtifactSvc. Same late-binding semantics.
	artifactSvc  *artifacts.Service
	folderMemSvc *foldermemory.Service
	// searchSvc mirrors Deps.SearchSvc.
	searchSvc *appclipssearch.Service
	// processRunner mirrors Deps.ProcessRunner.
	processRunner appassets.ProcessRunner
	// dispatcher mirrors Deps.Dispatcher (now the application port
	// type, see ClipIndexDispatcherPort for the rationale). Nil-
	// tolerated for test fixtures and partial deployments.
	dispatcher appclips.ClipIndexDispatcherPort

	// Use cases — business logic extracted from handlers
	reprocessUC     *appclips.ReprocessUseCase
	downloadUC      *appclips.DownloadUseCase
	bulkTagsUC      *appclips.BulkTagsUseCase
	enrichUC        *appclips.EnrichUseCase
	bulkUploadWorker *appclips.BulkUploadWorker
}

// NewHandler constructs the unified Handler. May be called before every
// dependency is wired — individual methods that need a missing dep will
// internal-error handle it (preserved legacy behavior).
//
// PR8: idempotencyMiddleware is the reusable Gin idempotency middleware
// instance; a nil value disables idempotency (test fixtures / dry-run
// CLI invocations). Production wiring passes the canonical *middleware.Idempotency
// value constructed from BuildRepoBundle.IdempotencyStore.
func NewHandler(d Deps, idempotencyMiddleware gin.HandlerFunc) *Handler {
	var idem gin.HandlerFunc = func(c *gin.Context) { c.Next() }
	if idempotencyMiddleware != nil {
		idem = idempotencyMiddleware
	}
	return &Handler{
		Idempotency:    idem,
		sourceResolver: d.SourceResolver,
		assetRepo:      d.AssetRepo,
		clipsRepo:      d.ClipsRepo,
		stockRepo:      d.StockRepo,
		artlistRepo:    d.ArtlistRepo,
		deletionSvc:    d.DeletionSvc,
		driveUploader:  d.DriveUploader,
		mediaProcessor: d.MediaProcessor,
		assetTreeSvc:   d.AssetTreeSvc,
		metaWriter:     d.MetaWriter,
		clipIndexer:    d.ClipIndexer,
		jobsSvc:        d.JobsSvc,
		cfg:            d.Cfg,
		log:            d.Log,
		voiceoverRepo:  d.VoiceoverRepo,
		imagesRepo:     d.ImagesRepo,
		artifactSvc:    d.ArtifactSvc,
		folderMemSvc:   d.FolderMemSvc,
		searchSvc:      d.SearchSvc,
		processRunner:  d.ProcessRunner,
		dispatcher:     d.Dispatcher,
		bulkUploadWorker: d.BulkUploadWorker,

		// Initialize use cases
		reprocessUC: appclips.NewReprocessUseCase(d.AssetRepo, d.MediaProcessor),
		downloadUC:  appclips.NewDownloadUseCase(d.AssetRepo, d.VoiceoverRepo),
		bulkTagsUC:  appclips.NewBulkTagsUseCase(d.SourceResolver, d.AssetTreeSvc),
		enrichUC:    appclips.NewEnrichUseCase(d.AssetRepo, d.ClipIndexer, d.MetaWriter, d.Log),
	}
}

// repoForSource resolves a clip source to its canonical repository.
// Standard clip sources are resolved through the shared source resolver.
func (h *Handler) repoForSource(source string) *assets.ClipsRepository {
	if h.sourceResolver == nil {
		return nil
	}
	return h.sourceResolver.ResolveRepo(source)
}

func (h *Handler) driveRootForSource(source string) (string, string) {
	spec, ok := map[string]struct {
		root   func(*config.Config) string
		marker string
	}{
		"clips": {
			root:   func(cfg *config.Config) string { return cfg.Drive.ClipsFolder() },
			marker: "/clips/",
		},
		"artlist": {
			root:   func(cfg *config.Config) string { return cfg.Drive.ArtlistFolder() },
			marker: "/artlist/",
		},
		"stock": {
			root:   func(cfg *config.Config) string { return cfg.Drive.StockFolder() },
			marker: "/stock/",
		},
	}[artifacts.CanonicalSource(source)]
	if !ok {
		return "", ""
	}
	return spec.root(h.cfg), spec.marker
}

// RegisterJobHandlers wires up the bulk-upload worker via the
// application-layer BulkUploadWorker (W14 PR2 slice 3, June 2026).
// The previous in-handler HandleBulkUploadYouTubeClipsJob method
// (api/assets/clips/bulk_upload_worker.go) was deleted; the worker
// is now constructed at the composition root with typed ports.
func (h *Handler) RegisterJobHandlers() error {
	if h.jobsSvc == nil {
		return nil
	}
	if h.bulkUploadWorker == nil {
		return nil
	}
	return h.jobsSvc.RegisterHandler("bulk_upload_youtube_clips", h.bulkUploadWorker.HandleJob)
}

// ─── W14 PR2 slice 4 bridge: cumulative metadata adapter ──────────────────
// cumulativeDriveAdapter wraps *drive.Uploader into appclips.ClipDriveUploaderPort
// so updateCumulativeMetadataJSON can delegate to the canonical application-layer
// appclips.UpdateCumulativeMetadataJSON without clip_upload.go importing infra.
// Only the 4 methods actually called by UpdateCumulativeMetadataJSON are
// delegated; adding new call patterns requires expanding the adapter.
type cumulativeDriveAdapter struct {
	up *drive.Uploader
}

func (a *cumulativeDriveAdapter) ListFiles(ctx context.Context, query string) ([]appclips.ClipDriveFileDTO, error) {
	if a.up == nil || a.up.Service == nil {
		return nil, fmt.Errorf("cumulativeDriveAdapter: uploader not wired")
	}
	list, err := a.up.Service.Files.List().Q(query).Fields("files(id, name)").Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	if list == nil {
		return nil, nil
	}
	out := make([]appclips.ClipDriveFileDTO, len(list.Files))
	for i, f := range list.Files {
		out[i] = appclips.ClipDriveFileDTO{ID: f.Id, Name: f.Name}
	}
	return out, nil
}

func (a *cumulativeDriveAdapter) DownloadFile(ctx context.Context, fileID string) (io.ReadCloser, string, error) {
	if a.up == nil {
		return nil, "", fmt.Errorf("cumulativeDriveAdapter: uploader not wired")
	}
	return a.up.DownloadFile(ctx, fileID)
}

func (a *cumulativeDriveAdapter) TrashFile(ctx context.Context, fileID string) error {
	if a.up == nil {
		return fmt.Errorf("cumulativeDriveAdapter: uploader not wired")
	}
	return a.up.TrashFile(ctx, fileID)
}

func (a *cumulativeDriveAdapter) UploadFile(ctx context.Context, localPath, folderID, filename string) (*appclips.ClipUploadResultDTO, error) {
	if a.up == nil {
		return nil, fmt.Errorf("cumulativeDriveAdapter: uploader not wired")
	}
	res, err := a.up.UploadFile(ctx, localPath, folderID, filename)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return &appclips.ClipUploadResultDTO{}, nil
	}
	return &appclips.ClipUploadResultDTO{
		FileID:       res.FileID,
		WebViewLink:  res.WebViewLink,
		DownloadLink: res.DownloadLink,
		MD5Checksum:  res.MD5Checksum,
	}, nil
}

// The remaining ClipDriveUploaderPort methods are not called by
// UpdateCumulativeMetadataJSON. If a future caller exercises them,
// expand this adapter.

func (a *cumulativeDriveAdapter) GetOrCreateFolder(ctx context.Context, name, parent string) (string, error) {
	return "", fmt.Errorf("cumulativeDriveAdapter: GetOrCreateFolder not implemented")
}
func (a *cumulativeDriveAdapter) GetFolderName(ctx context.Context, id string) (string, error) {
	return "", fmt.Errorf("cumulativeDriveAdapter: GetFolderName not implemented")
}
func (a *cumulativeDriveAdapter) TrashFolder(ctx context.Context, id string) error {
	return fmt.Errorf("cumulativeDriveAdapter: TrashFolder not implemented")
}
func (a *cumulativeDriveAdapter) DeleteFolder(ctx context.Context, id string) error {
	return fmt.Errorf("cumulativeDriveAdapter: DeleteFolder not implemented")
}
func (a *cumulativeDriveAdapter) UploadFileWithDescription(ctx context.Context, localPath, folderID, filename, desc string) (*appclips.ClipUploadResultDTO, error) {
	return nil, fmt.Errorf("cumulativeDriveAdapter: UploadFileWithDescription not implemented")
}
func (a *cumulativeDriveAdapter) GetFileMD5(ctx context.Context, id string) (string, error) {
	return "", fmt.Errorf("cumulativeDriveAdapter: GetFileMD5 not implemented")
}
func (a *cumulativeDriveAdapter) GetFileMeta(ctx context.Context, id string) (*appclips.ClipDriveFileMetaDTO, error) {
	return nil, fmt.Errorf("cumulativeDriveAdapter: GetFileMeta not implemented")
}

// updateCumulativeMetadataJSON is a thin bridge that wraps the concrete
// *drive.Uploader into a ClipDriveUploaderPort and delegates to the
// canonical application-layer UpdateCumulativeMetadataJSON. W14 PR2
// slice 4 (June 2026): the previous UpdateCumulativeMetadataJSON in
// upload_helpers.go took *drive.Uploader directly; this wrapper exists
// so clip_upload.go can use the port-based app-layer version without
// importing internal/infrastructure/drive.
func (h *Handler) updateCumulativeMetadataJSON(
	ctx context.Context,
	tempPath string,
	folderID string,
	clipID string,
	newEntry map[string]interface{},
	log *zap.Logger,
) {
	if h.driveUploader == nil {
		return
	}
	appclips.UpdateCumulativeMetadataJSON(ctx, &cumulativeDriveAdapter{up: h.driveUploader}, tempPath, folderID, clipID, newEntry, log)
}

// PR8 helper: idemWriter returns h.Idempotency if set, else a no-op
// pass-through handler. Used only for Write routes (POST/PUT/PATCH/DELETE);
// read routes never need idempotency.
func (h *Handler) idemWriter() gin.HandlerFunc {
	if h.Idempotency == nil {
		return func(c *gin.Context) { c.Next() }
	}
	return h.Idempotency
}

// RegisterRoutes mounts the entire clip-route surface on the supplied
// gin router group. SourcesHandler keeps the Voiceover, SoundEffect,
// diagnostics, and Drive-move/fold/sync-route families and delegates
// everything else to h.clips.
//
// PR8 (June 2026): write routes (POST/PUT/PATCH/DELETE) install
// h.Idempotency BEFORE the handler — when present — so Idempotency-Key
// replay, body-hash conflict (422), and in-flight (409) semantics
// apply uniformly. Read routes are unchanged.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	idem := h.idemWriter()
	// Clip-level endpoints
	r.POST("/:source/clips", idem, h.CreateClip)
	r.GET("/:source/clips", h.ListClips)
	r.GET("/:source/clips/:id", h.GetClip)
	r.PATCH("/:source/clips/:id", idem, h.UpdateClip)
	r.POST("/:source/clips/:id/status", idem, h.ClipStatus)
	r.POST("/:source/clips/:id/verify", idem, h.VerifyClip)
	r.POST("/:source/clips/:id/trash", idem, h.TrashClip)
	r.POST("/:source/clips/:id/delete", idem, h.DeleteClip)
	r.POST("/:source/clips/:id/download", idem, h.DownloadClip)
	r.POST("/:source/clips/:id/duplicates", idem, h.FindDuplicates)
	r.POST("/:source/clips/:id/reupload", idem, h.ReuploadClip)
	r.POST("/:source/clips/:id/reprocess", idem, h.ReprocessClip)
	r.POST("/:source/clips/:id/reindex", idem, h.ReindexClip)

	// Source-level bulk actions
	r.POST("/:source/bulk/tags/add", idem, h.BulkAddTags)
	r.POST("/:source/bulk/tags/remove", idem, h.BulkRemoveTags)
	r.POST("/:source/reconcile", idem, h.Reconcile)
	r.POST("/:source/cleanup", idem, h.Cleanup)

	// Folders + tree (writes only)
	r.GET("/:source/folders", h.ListFolders)
	r.GET("/:source/folders/:id", h.FolderStatus)
	r.POST("/:source/folders/:id/manifest", idem, h.RegenerateManifest)
	r.POST("/:source/folders/:id/trash", idem, h.TrashFolder)
	r.POST("/:source/folders/:id/delete", idem, h.DeleteFolder)
	r.GET("/:source/folders/:id/children", h.GetFolderChildren)
	r.GET("/:source/tree", h.GetTree)
	r.GET("/:source/breadcrumb", h.GetBreadcrumb)

	// Cross-cutting actions on existing clip contexts
	r.POST("/enrich", idem, h.EnrichMedia)
	r.POST("/enrich/batch", idem, h.BatchReindex)

	// Upload endpoints (multipart body bypasses body-hash; idempotency still
	// observes in-flight 409 + completed replay).
	r.POST("/upload-video", idem, h.UploadVideoClip)

	// Search endpoint
	r.POST("/search/advanced", idem, h.AdvancedSearch)
}
