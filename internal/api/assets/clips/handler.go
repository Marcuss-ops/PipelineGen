// Package clips — handler.go: fat orchestrator that mounts every clip route.
//
// Composition: 5 sub-handlers (search/ingest/ops/nonops/bulk) registered via
// per-cluster RegisterRoutes. NewHandlerStrict validates the JOB-SVC +
// bulk-upload-worker chain at construction (godlike/07 no-fake-availability)
// — a partial wiring crashes at boot instead of silently succeeding on first
// enqueue. The legacy NewHandler (nil-tolerant) remains for test fixtures.
//
// Card 10 (July 2026): the 3 non-HTTP delegators
// (EnrichAndIndexClip + RegisterJobHandlers + HandleBulkUploadYouTubeClipsJob)
// are REMOVED from *Handler — the slim units have migrated either to the
// canonical typed port (appclips.ClipEnricher for enrich) or to a private
// sub-handler registrar (ClipsDescriptor.clipJobRegistrar for bulk_upload).
// The exposed transport surface is now strictly HTTP routes.
package clips

import (
	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips/nonops"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/duplicates"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	appupload "github.com/Marcuss-ops/PipelineGen/internal/application/clips/upload"
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
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
	JobsSvc          kerneljob.Service
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
	jobsSvc         kerneljob.Service
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
	// Card 10 (July 2026): enrichUC is REQUIRED. The composition root
	// (app/wire_assets_clips.go) constructs it via NewEnrichUseCase with
	// the canonical dispatcher wired; nil at this constructor only
	// happens via the legacy NewHandler nil-tolerant path used by test
	// fixtures (godlike/07 back-compat). The previous enrichUCOrLocal
	// fallback into repo.Upsert is RETIRED — partial deployments must
	// fail-closed at the composition root, not silently bypass the
	// outbox here.
	enrichUC := d.EnrichUC
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

// NewHandlerStrict constructs the unified Handler with fail-closed
// validation of the canonical 3-method job-handler registration chain
// deps at construction time. Threaded through the composition root
// (clips/module.go::Build -> NewHandlerStrict) so a partial wiring
// that would fail at first enqueue crashes loudly at boot instead —
// godlike/07 no-fake-availability.
//
// godlike/06 SSOT: the canonical path is
//
//	clips.Build (module.go)
//	  -> NewHandlerStrict (this function)
//	      -> nonops.ValidateNonOpsDeps pre-check on the required deps
//	      -> NewHandler construction (with the validated locator deps)
//	  -> ClipsDescriptor.RegisterJobHandlers (module.go)
//	      -> Handler.RegisterJobHandlers (handler.go)
//	          -> NonOpsHandler.RegisterJobHandlers (nonops/handler_jobs.go)
//	              -> jobs.Service.RegisterHandler
//
// If the pre-check fails (JobsSvc or BulkUploadWorker is nil), the
// construction returns an error instead of constructing a Handler
// with a partially-wired nonops sub-handler. The legacy NewHandler
// (nil-tolerant at construction) remains for test fixtures that
// opt out of the fail-closed contract.
func NewHandlerStrict(d Deps, idempotencyMiddleware gin.HandlerFunc) (*Handler, error) {
	if err := nonops.ValidateNonOpsDeps(nonops.Deps{
		JobsSvc:          d.JobsSvc,
		BulkUploadWorker: d.BulkUploadWorker,
	}); err != nil {
		return nil, err
	}
	if d.EnrichUC == nil {
		return nil, appclips.ErrEnrichDispatcherRequired
	}
	return NewHandler(d, idempotencyMiddleware), nil
}

// repoForSource: resolves clip source via the Search sub-handler; used as
// method-value callback by the nonops sub-handler.
func (h *Handler) repoForSource(source string) *assets.ClipsRepository {
	if h.search == nil {
		return nil
	}
	return h.search.repoForSource(source)
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
