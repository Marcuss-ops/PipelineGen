// Package clips — module.go: canonical Build entrypoint for the Clips HTTP capability.
//
// Build constructs the ClipsDescriptor (Module + Handler). The Module mounts
// 7 sub-descriptors (catalog/ingest/processing/publication/indexing/operations/bulk)
// under /clips. Description.Handler is the lone non-HTTP consumer side-channel
// (sourcingEnrichmentAdapter → EnrichAndIndexClip); production HTTP routes
// always go through the Module.
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
// decorators). Mandatory fields return error when nil; optional fields fall
// through to handler-level nil-tolerance.
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
	// EnrichUC is OPTIONAL: nil triggers enrichUCOrLocal fallback construction.
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

	// Field→sub-handler mapping: each *XxxRegistrar method on *Handler
	// consumes exactly the deps its cluster needs (search/ingest/ops/nonops/
	// bulk) — see handler.go factory functions for the per-cluster matrix.
}

// ClipsDescriptor: route surface (Module) + raw orchestrator (Handler).
// Descriptor does NOT embed *Handler — forwarder methods (Name/Enabled/
// RegisterRoutes/RegisterJobHandlers) hand-promote the surface.
type ClipsDescriptor struct {
	Module  api.Module
	Handler *Handler
}

func (d *ClipsDescriptor) Name() string  { return d.Module.Name() }
func (d *ClipsDescriptor) Enabled() bool { return d.Module.Enabled() }
func (d *ClipsDescriptor) RegisterRoutes(rg *gin.RouterGroup) {
	d.Module.RegisterRoutes(rg)
}

// RegisterJobHandlers implements api.DescriptorJobs by routing through the
// canonical 3-method chain: Descriptor → Handler → NonOpsHandler →
// jobs.Service. The svc parameter is captured in orchestrator's h.jobsSvc at
// wire-time (same instance), so fail-closed typed sentinels surface at
// nonops.RegisterJobHandlers first.
//
// Diagnostic ping (symmetric to BulkUploadYouTubeClips handler-entry snapshot)
// records descriptor+handler+jobsSvc pointers at boot so a future "no handler
// registered" reproduction can localise the wiring split. Retire once the
// upstream bug is closed.
func (d *ClipsDescriptor) RegisterJobHandlers(svc api.JobRegistrar) error {
	if d.Handler == nil {
		return nil
	}
	registerErr := d.Handler.RegisterJobHandlers()
	_ = svc // svc is structurally required by DescriptorJobs; orchestrator captures the same instance.
	if d.Handler.log != nil {
		d.Handler.log.Info("clips: registered bulk_upload_youtube_clips handler",
			zap.String("module", "clips"),
			zap.String("svc_type", fmt.Sprintf("%T", svc)),
			zap.String("svc_ptr", fmt.Sprintf("%p", svc)),
			zap.String("descriptor_ptr", fmt.Sprintf("%p", d)),
			zap.String("handler_ptr", fmt.Sprintf("%p", d.Handler)),
			zap.Bool("register_ok", registerErr == nil),
			zap.Error(registerErr),
		)
	}
	return registerErr
}

// Build composes the Clips HTTP capability. Fail-closed on mandatory nil deps.
// Returns ClipsDescriptor { Module, Handler }.
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

	return &ClipsDescriptor{Module: mod, Handler: handler}, nil
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
