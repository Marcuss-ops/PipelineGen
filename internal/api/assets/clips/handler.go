// Package clips — handler.go: fat orchestrator that mounts every clip route.
//
// Composition: 5 sub-handlers (search/ingest/ops/nonops/bulk) registered via
// per-cluster RegisterRoutes; 3 non-HTTP delegators (EnrichAndIndexClip +
// RegisterJobHandlers + HandleBulkUploadYouTubeClipsJob) keep external
// non-HTTP consumers stable. NewHandlerStrict validates the JOB-SVC +
// bulk-upload-worker chain at construction (godlike/07 no-fake-availability)
// — a partial wiring crashes at boot instead of silently succeeding on first
// enqueue. The legacy NewHandler (nil-tolerant) remains for test fixtures.
package clips

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips/nonops"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/duplicates"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	appupload "github.com/Marcuss-ops/PipelineGen/internal/application/clips/upload"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Deps is the constructor bag for Handler.
type Deps struct {
	ClipsRepo        *assets.ClipsRepository
	AssetRepo        asset.Repository
	DeletionSvc      *deletion.DeletionService
	DriveAdmin       drive.Admin
	MediaProcessor   asset.Processor
	AssetTreeSvc     *assettree.Service
	MetaWriter       semantic.MetadataWriterPort
	ClipIndexer      *clipindexer.Service
	JobsSvc          jobservice.Service
	Cfg              *config.Config
	Log              *zap.Logger
	VoiceoverRepo    *assets.VoiceoversRepository
	ImagesRepo       *assets.ImagesRepository
	ArtifactSvc      *artifacts.Service
	FolderMemSvc     *foldermemory.Service
	SearchSvc        *search.Aggregator
	ProcessRunner    appassets.ProcessRunner
	Dispatcher       appclips.ClipIndexDispatcherPort
	DuplicateFinder  *duplicates.Finder
	ReuploadUC       *appclips.ReuploadUseCase
	EnrichUC         *appclips.EnrichUseCase
	BulkUploadWorker *appclips.BulkUploadWorker
	ClipOpsService   *appclips.ClipOpsService
	UploadUC         *appupload.UseCase
	Publisher        delivery.Publisher
}

// Handler is the fat HTTP orchestrator. Sub-handlers are constructed eagerly
// in NewHandler; non-HTTP delegators stay on *Handler for external consumers.
type Handler struct {
	Idempotency gin.HandlerFunc

	assetRepo       asset.Repository
	driveAdmin      drive.Admin
	duplicateFinder *duplicates.Finder
	downloadUC      *appclips.DownloadUseCase
	reuploadUC      *appclips.ReuploadUseCase
	publisher       delivery.Publisher
	log             *zap.Logger
	jobsSvc         jobservice.Service
	cfg             *config.Config

	search        *SearchHandler
	ingest        *IngestHandler
	ops           *OpsHandler
	bulkTransport *BulkUploadTransport
	nonops        *nonops.NonOpsHandler
}

func NewHandler(d Deps, idempotencyMiddleware gin.HandlerFunc) *Handler {
	var idem gin.HandlerFunc = func(c *gin.Context) { c.Next() }
	if idempotencyMiddleware != nil {
		idem = idempotencyMiddleware
	}
	enrichUC := enrichUCOrLocal(d.EnrichUC, d.AssetRepo, d.MetaWriter, d.Log)
	bulkTagsUC := appclips.NewBulkTagsUseCase(d.ClipsRepo, d.AssetTreeSvc)
	downloadUC := appclips.NewDownloadUseCase(d.AssetRepo, d.VoiceoverRepo)
	reprocessUC := appclips.NewReprocessUseCase(d.AssetRepo, d.MediaProcessor, nil)

	h := &Handler{
		Idempotency:     idem,
		assetRepo:       d.AssetRepo,
		duplicateFinder: d.DuplicateFinder,
		driveAdmin:      d.DriveAdmin,
		downloadUC:      downloadUC,
		reuploadUC:      d.ReuploadUC,
		publisher:       d.Publisher,
		log:             d.Log,
		jobsSvc:         d.JobsSvc,
		cfg:             d.Cfg,

		search: NewSearchHandler(SearchDeps{
			ClipsRepo:     d.ClipsRepo,
			AssetRepo:     d.AssetRepo,
			VoiceoverRepo: d.VoiceoverRepo,
			ImagesRepo:    d.ImagesRepo,
			SearchSvc:     d.SearchSvc,
		}),
		ingest: NewIngestHandler(IngestDeps{
			Dispatcher:   d.Dispatcher,
			AssetTreeSvc: d.AssetTreeSvc,
			JobsSvc:      d.JobsSvc,
			ClipsRepo:    d.ClipsRepo,
			EnrichUC:     enrichUC,
			UploadUC:     d.UploadUC,
			Log:          d.Log,
		}),
		ops: NewOpsHandler(OpsDeps{
			ClipOpsService: d.ClipOpsService,
			DeletionSvc:    d.DeletionSvc,
			FolderMemSvc:   d.FolderMemSvc,
			ClipsRepo:      d.ClipsRepo,
			DriveAdmin:     d.DriveAdmin,
			AssetTreeSvc:   d.AssetTreeSvc,
			Log:            d.Log,
		}),
		bulkTransport: NewBulkUploadTransport(BulkTransportDeps{
			JobsSvc:          d.JobsSvc,
			MediaPath:        d.Cfg.Storage.MediaPath(),
			TempPath:         d.Cfg.Storage.TempPath(),
			DataDir:          d.Cfg.Storage.AbsDataDir(),
			BulkUploadWorker: d.BulkUploadWorker,
			Log:              d.Log,
		}),
	}

	h.nonops = nonops.NewNonOpsHandler(nonops.Deps{
		BulkTagsUC:       bulkTagsUC,
		ReprocessUC:      reprocessUC,
		EnrichUC:         enrichUC,
		ClipIndexer:      d.ClipIndexer,
		JobsSvc:          d.JobsSvc,
		BulkUploadWorker: d.BulkUploadWorker,
		RepoForSource:    h.repoForSource,
		Log:              d.Log,
	})

	return h
}

// NewHandlerStrict: same construction as NewHandler but validates the
// nonops.NewNonOps required deps (JobsSvc + BulkUploadWorker non-nil) up front.
// godlike/07 no-fake-availability: a partial wiring that would fail at first
// enqueue is a 500 at boot instead.
func NewHandlerStrict(d Deps, idempotencyMiddleware gin.HandlerFunc) (*Handler, error) {
	if err := nonops.ValidateNonOpsDeps(nonops.Deps{
		JobsSvc:          d.JobsSvc,
		BulkUploadWorker: d.BulkUploadWorker,
	}); err != nil {
		return nil, err
	}
	return NewHandler(d, idempotencyMiddleware), nil
}

// enrichUCOrLocal: returns shared when non-nil; otherwise constructs a local
// fallback with no dispatcher (orchestrator only sees the narrower
// ClipIndexDispatcherPort; enrichment runs but re-index enqueueing is skipped).
func enrichUCOrLocal(
	shared *appclips.EnrichUseCase,
	repo asset.Repository,
	mw semantic.MetadataWriterPort,
	log *zap.Logger,
) *appclips.EnrichUseCase {
	if shared != nil {
		return shared
	}
	return appclips.NewEnrichUseCase(repo, mw, nil, log)
}

// repoForSource: resolves clip source via the Search sub-handler; used as
// method-value callback by the nonops sub-handler.
func (h *Handler) repoForSource(source string) *assets.ClipsRepository {
	if h.search == nil {
		return nil
	}
	return h.search.repoForSource(source)
}

// EnrichAndIndexClip: 1-line delegator to nonops.EnrichAndIndexClip.
func (h *Handler) EnrichAndIndexClip(ctx context.Context, clip *asset.Asset, source string) {
	if h.nonops == nil {
		return
	}
	h.nonops.EnrichAndIndexClip(ctx, clip, source)
}

// RegisterJobHandlers: 1-line delegator to nonops.RegisterJobHandlers.
func (h *Handler) RegisterJobHandlers() error {
	if h.nonops == nil {
		return nil
	}
	return h.nonops.RegisterJobHandlers()
}

// HandleBulkUploadYouTubeClipsJob: 1-line delegator to nonops; typed error
// when nonops is nil so the dispatcher fails closed.
func (h *Handler) HandleBulkUploadYouTubeClipsJob(ctx context.Context, j *jobservice.Job, tools *appjobs.JobTools) (map[string]any, error) {
	if h.nonops == nil {
		return nil, fmt.Errorf("nonops sub-handler not wired (clips.Handler constructed without NewHandler)")
	}
	return h.nonops.HandleBulkUploadYouTubeClipsJob(ctx, j, tools)
}

// RegisterRoutes mounts the entire clip-route surface.
//
// NOTE: clips.Build composition paths register routes through sub-descriptors,
// NOT through this method, so production registration does not double-mount.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	idem := h.idemWriter()

	h.ingest.RegisterRoutes(r, idem)
	h.search.RegisterRoutes(r, idem)
	h.ops.RegisterRoutes(r, idem)
	h.nonops.RegisterRoutes(r, idem)
	h.bulkTransport.RegisterRoutes(r, idem)

	r.POST("/:source/clips/:id/download", idem, h.DownloadClip)
	r.POST("/:source/clips/:id/duplicates", idem, h.FindDuplicates)
	r.POST("/:source/clips/:id/reupload", idem, h.ReuploadClip)
}

func (h *Handler) catalogRegistrar(idem gin.HandlerFunc) *catalogRegistrar {
	return &catalogRegistrar{search: h.search, ops: h.ops, h: h, idem: idem}
}

func (h *Handler) ingestRegistrar(idem gin.HandlerFunc) *ingestRegistrar {
	return &ingestRegistrar{ingest: h.ingest, idem: idem}
}

func (h *Handler) processingRegistrar(idem gin.HandlerFunc) *processingRegistrar {
	return &processingRegistrar{nonops: h.nonops, idem: idem}
}

func (h *Handler) publicationRegistrar(idem gin.HandlerFunc) *publicationRegistrar {
	return &publicationRegistrar{h: h, idem: idem}
}

func (h *Handler) indexingRegistrar(idem gin.HandlerFunc) *indexingRegistrar {
	return &indexingRegistrar{nonops: h.nonops, idem: idem}
}

func (h *Handler) operationsRegistrar(idem gin.HandlerFunc) *operationsRegistrar {
	return &operationsRegistrar{ops: h.ops, nonops: h.nonops, idem: idem}
}

func (h *Handler) bulkRegistrar(idem gin.HandlerFunc) *bulkRegistrar {
	return &bulkRegistrar{bulk: h.bulkTransport, idem: idem}
}
