// Package clips — module.go: canonical Build entrypoint for the Clips HTTP capability.
//
// Build constructs the ClipsDescriptor which exposes ONLY routes (Module)
// + job handlers (RegisterJobHandlers). The raw orchestrator *Handler
// is no longer exposed on the descriptor — godlike/06 SSOT: the
// descriptor's public surface is the minimum needed by the
// composition root and external callers; the orchestrator is a
// private construction artifact.
//
// Card 10 (July 2026): ClipsDescriptor.Handler is REMOVED. The single
// non-HTTP caller (sourcingEnrichmentAdapter in internal/app) is now
// wired through the canonical appclips.ClipEnricher typed port
// instead of reaching through clips.Handler.
package clips

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips/bulk"
	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips/catalog"
	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips/indexing"
	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips/operations"
	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips/processing"
	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips/publication"
	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
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
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Dependencies is the typed narrow input to Build. Mirror of Handler.Deps plus
// Build-time fields (Idempotency middleware, EnabledFunc closure, ModuleOpts
// decorators). Mandatory fields return error when nil; EnrichUC is REQUIRED
// (post-Card 10) — the legacy enrichUCOrLocal fallback is retired.
type Dependencies struct {
	ClipsRepo       *assets.ClipsRepository
	AssetRepo       asset.Repository
	DeletionSvc     *deletion.DeletionService
	DriveAdmin      drive.Admin
	MediaProcessor  asset.Processor
	AssetTreeSvc    *assettree.Service
	MetaWriter      semantic.MetadataWriterPort
	ClipIndexer     *clipindexer.Service
	JobsSvc         jobservice.Service
	Cfg             *config.Config
	Log             *zap.Logger
	VoiceoverRepo   *assets.VoiceoversRepository
	ImagesRepo      *assets.ImagesRepository
	ArtifactSvc     *artifacts.Service
	FolderMemSvc    *foldermemory.Service
	SearchSvc       *search.Aggregator
	ProcessRunner   appassets.ProcessRunner
	Dispatcher      appclips.ClipIndexDispatcherPort
	DuplicateFinder *duplicates.Finder
	ReuploadUC      *appclips.ReuploadUseCase
	// EnrichUC is REQUIRED post-Card 10. nil → Build returns an error
	// (godlike/07 fail-closed). The pre-Card-10 enrichUCOrLocal fallback
	// is fully retired (see commits log: card 10 closed the silent-success
	// assetRepo bypass path).
	EnrichUC         *appclips.EnrichUseCase
	BulkUploadWorker *appclips.BulkUploadWorker
	ClipOpsService   *appclips.ClipOpsService
	UploadUC         *appupload.UseCase
	Publisher        delivery.Publisher

	// Idempotency: nil → no-op pass-through (preserves test fixtures / dry-run CLI).
	Idempotency gin.HandlerFunc
	// EnabledFunc: MANDATORY closure (typically func() bool { return true }).
	EnabledFunc func() bool
	// ModuleOpts: nil → plain RouteModule.
	ModuleOpts []api.RouteModuleOption
}

// clipJobRegistrar is the canonical bridge from ClipsDescriptor to the
// jobs service. Card 10: replaces the prior chain
// Descriptor.RegisterJobHandlers → Handler.RegisterJobHandlers →
// NonOpsHandler.RegisterJobHandlers. The Descriptor holds a slim
// registrar (no Handler dependency); the boot-time diagnostic
// pointer snapshot is preserved.
type clipJobRegistrar struct {
	bulkUploadWorker *appclips.BulkUploadWorker
	jobsSvc          jobservice.Service
	log              *zap.Logger
}

// RegisterBulkUpload writes the bulk_upload_youtube_clips handler
// into the supplied jobs service. Returns the typed error from
// jobsSvc.RegisterHandler so callers can branch on no-handler-
// registered scenarios via errors.Is(err, jobs.ErrJobsSvcRequiredAtRegistration).
//
// godlike/07 fail-closed: nil BulkUploadWorker or nil JobsSvc →
// surface a typed sentinel rather than silently succeeding on the
// next enqueue.
func (r *clipJobRegistrar) RegisterBulkUpload(svc api.JobRegistrar, descriptorPtr interface{}) error {
	if r.bulkUploadWorker == nil || r.jobsSvc == nil {
		return fmt.Errorf("%w: clipJobRegistrar: BulkUploadWorker or JobsSvc nil at registration (godlike/07 fail-closed)", appjobs.ErrJobsSvcRequiredAtRegistration)
	}
	registerErr := svc.RegisterHandler(jobservice.TypeBulkUploadYouTubeClips, appjobs.HandlerFunc(r.bulkUploadWorker.HandleJob))
	if r.log != nil {
		r.log.Info("clips: registered bulk_upload_youtube_clips handler (descriptor-side)",
			zap.String("module", "clips"),
			zap.String("svc_type", fmt.Sprintf("%T", svc)),
			zap.String("svc_ptr", fmt.Sprintf("%p", svc)),
			zap.String("descriptor_type", fmt.Sprintf("%T", descriptorPtr)),
			zap.String("descriptor_ptr", fmt.Sprintf("%p", descriptorPtr)),
			zap.Bool("register_ok", registerErr == nil),
			zap.Error(registerErr),
		)
	}
	return registerErr
}

// ClipsDescriptor: route surface (Module) + job-handler registrar.
// godlike/06 SSOT one-canonical-owner-per-fact: the descriptor's
// exposed surface is the minimum the composition root + external
// callers need. The orchestrator *Handler is now a private
// construction artifact (advanced inside Build); no cross-package
// caller reaches into the Handler.
type ClipsDescriptor struct {
	Module api.Module
	jobReg *clipJobRegistrar
}

func (d *ClipsDescriptor) Name() string  { return d.Module.Name() }
func (d *ClipsDescriptor) Enabled() bool { return d.Module.Enabled() }
func (d *ClipsDescriptor) RegisterRoutes(rg *gin.RouterGroup) {
	d.Module.RegisterRoutes(rg)
}

// RegisterJobHandlers implements api.DescriptorJobs by routing through
// the slim clipJobRegistrar (godlike/06 SSOT: the canonical 3-method
// chain's LEFTMOST entry). The svc parameter is the jobs service
// injected by the composition root (same instance the orchestrator
// Handler captures internally for the nonops sub-handler).
func (d *ClipsDescriptor) RegisterJobHandlers(svc api.JobRegistrar) error {
	if d.jobReg == nil {
		return nil
	}
	return d.jobReg.RegisterBulkUpload(svc, d)
}

// Build composes the Clips HTTP capability. Fail-closed on mandatory
// nil deps (post-Card 10: EnrichUC is now in the mandatory set).
//
// Returns ClipsDescriptor { Module, jobReg }. The raw orchestrator
// *Handler stays as a private construction artifact consumed only
// by the per-cluster sub-registrar method-value callbacks.
func Build(deps Dependencies) (api.Descriptor, error) {
	if deps.ClipsRepo == nil {
		return nil, fmt.Errorf("clips.Build: ClipsRepo is required")
	}
	if deps.JobsSvc == nil {
		return nil, fmt.Errorf("clips.Build: JobsSvc is required (BulkUpload / EnrichMedia / ReindexClip routes)")
	}
	if deps.Cfg == nil {
		return nil, fmt.Errorf("clips.Build: Cfg is required (driveRootForSource helper)")
	}
	if deps.EnabledFunc == nil {
		return nil, fmt.Errorf("clips.Build: EnabledFunc is required (composition root wires the closure)")
	}
	if deps.EnrichUC == nil {
		return nil, fmt.Errorf("clips.Build: EnrichUC is required (card 10 retired the assetRepo local-fallback; partial deployments must fail-closed)")
	}

	log := deps.Log
	if log == nil {
		log = zap.NewNop()
	}
	idem := deps.Idempotency
	if idem == nil {
		idem = func(c *gin.Context) { c.Next() }
	}

	handler, err := NewHandlerStrict(Deps{
		ClipsRepo:        deps.ClipsRepo,
		AssetRepo:        deps.AssetRepo,
		DeletionSvc:      deps.DeletionSvc,
		DriveAdmin:       deps.DriveAdmin,
		MediaProcessor:   deps.MediaProcessor,
		AssetTreeSvc:     deps.AssetTreeSvc,
		MetaWriter:       deps.MetaWriter,
		ClipIndexer:      deps.ClipIndexer,
		JobsSvc:          deps.JobsSvc,
		Cfg:              deps.Cfg,
		Log:              log,
		VoiceoverRepo:    deps.VoiceoverRepo,
		ImagesRepo:       deps.ImagesRepo,
		ArtifactSvc:      deps.ArtifactSvc,
		FolderMemSvc:     deps.FolderMemSvc,
		SearchSvc:        deps.SearchSvc,
		ProcessRunner:    deps.ProcessRunner,
		Dispatcher:       deps.Dispatcher,
		DuplicateFinder:  deps.DuplicateFinder,
		ReuploadUC:       deps.ReuploadUC,
		EnrichUC:         deps.EnrichUC,
		BulkUploadWorker: deps.BulkUploadWorker,
		ClipOpsService:   deps.ClipOpsService,
		UploadUC:         deps.UploadUC,
		Publisher:        deps.Publisher,
	}, idem)
	if err != nil {
		return nil, fmt.Errorf("clips.Build: %w", err)
	}

	// 7 sub-descriptors under /clips; idempotency closed at construction.
	catalogDesc, err := catalog.Build(catalog.Dependencies{
		Handler: handler.catalogRegistrar(idem), EnabledFunc: deps.EnabledFunc,
		Idempotency: idem, Logger: log,
	})
	if err != nil {
		return nil, fmt.Errorf("clips.Build: catalog sub-descriptor: %w", err)
	}
	ingestDesc, err := ingest.Build(ingest.Dependencies{
		Handler: handler.ingestRegistrar(idem), EnabledFunc: deps.EnabledFunc,
		Idempotency: idem, Logger: log,
	})
	if err != nil {
		return nil, fmt.Errorf("clips.Build: ingest sub-descriptor: %w", err)
	}
	processingDesc, err := processing.Build(processing.Dependencies{
		Handler: handler.processingRegistrar(idem), EnabledFunc: deps.EnabledFunc,
		Idempotency: idem, Logger: log,
	})
	if err != nil {
		return nil, fmt.Errorf("clips.Build: processing sub-descriptor: %w", err)
	}
	publicationDesc, err := publication.Build(publication.Dependencies{
		Handler: handler.publicationRegistrar(idem), EnabledFunc: deps.EnabledFunc,
		Idempotency: idem, Logger: log,
	})
	if err != nil {
		return nil, fmt.Errorf("clips.Build: publication sub-descriptor: %w", err)
	}
	indexingDesc, err := indexing.Build(indexing.Dependencies{
		Handler: handler.indexingRegistrar(idem), EnabledFunc: deps.EnabledFunc,
		Idempotency: idem, Logger: log,
	})
	if err != nil {
		return nil, fmt.Errorf("clips.Build: indexing sub-descriptor: %w", err)
	}
	operationsDesc, err := operations.Build(operations.Dependencies{
		Handler: handler.operationsRegistrar(idem), EnabledFunc: deps.EnabledFunc,
		Idempotency: idem, Logger: log,
	})
	if err != nil {
		return nil, fmt.Errorf("clips.Build: operations sub-descriptor: %w", err)
	}
	bulkDesc, err := bulk.Build(bulk.Dependencies{
		Handler: handler.bulkRegistrar(idem), EnabledFunc: deps.EnabledFunc,
		Idempotency: idem, Logger: log,
	})
	if err != nil {
		return nil, fmt.Errorf("clips.Build: bulk sub-descriptor: %w", err)
	}

	mod := api.NewRouteModule(
		"clips",
		deps.EnabledFunc,
		"/clips",
		&subModuleAdapter{subModules: []api.Descriptor{
			catalogDesc, ingestDesc, processingDesc, publicationDesc,
			indexingDesc, operationsDesc, bulkDesc,
		}},
		log,
		deps.ModuleOpts...,
	)

	return &ClipsDescriptor{
		Module: mod,
		jobReg: &clipJobRegistrar{
			bulkUploadWorker: deps.BulkUploadWorker,
			jobsSvc:          deps.JobsSvc,
			log:              log,
		},
	}, nil
}

// subModuleAdapter mounts each enabled sub-descriptor under the supplied router group.
type subModuleAdapter struct {
	subModules []api.Descriptor
}

func (a *subModuleAdapter) RegisterRoutes(rg *gin.RouterGroup) {
	for _, sub := range a.subModules {
		if sub.Enabled() {
			sub.RegisterRoutes(rg)
		}
	}
}
