// Package clips — module.go: canonical Build entrypoint for the Clips HTTP capability.
//
// PR-CLIPS-7-MODULES-UPPER-CLIPSMODULE (July 2026): the upper
// descriptor becomes ClipsModule{Catalog, Ingest, Processing,
// Operations} — four typed-narrow sub-descriptors are exposed on the
// public struct surface (godlike/06 SSOT: minimum-needed cross-
// package surface). Three additional routing-only sub-descriptors
// (publication, indexing, bulk) are kept PRIVATE because no cross-
// package composition-root consumer reaches them today; future
// composition needs can promote them without breaking the surface
// contract.
//
// Each of the 7 sub-modules (`catalog`, `ingest`, `processing`,
// `publication`, `indexing`, `operations`, `bulk`) accepts NARROW
// typed-narrow deps (one typed cluster interface per cluster,
// with `operations` carrying two: OpsRoutes + BulkTagRoutes).
// The 4 standard infra fields (EnabledFunc, Idempotency, Logger,
// ModuleOpts) are shared by all 7. The composition root in this file
// wires each sub-module from the parent's per-cluster handler
// pointers (which already exist after NewHandlerStrict) — no inline
// route installation lives here.
//
// The orchestrator *Handler stays a private construction artifact:
// godlike/06 SSOT one-canonical-owner-per-fact, the descriptor's
// public surface is the minimum the composition root + external
// callers need. The sole non-HTTP consumer
// (sourcingEnrichmentAdapter in internal/app) is wired through the
// canonical appclips.ClipEnricher typed port (returned alongside the
// descriptor by buildClipsBundle), not through any Handler field.
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
	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Dependencies is the typed narrow input to Build. Mandatory fields
// fail closed (EnrichUC + JobsSvc + Cfg + EnabledFunc) so partial
// deployments surface a startup error instead of silent-success at
// first request (godlike/07 NO-FAKE-AVAILABILITY).
type Dependencies struct {
	ClipsRepo   *assets.ClipsRepository
	AssetRepo   asset.Repository
	DeletionSvc *deletion.DeletionService
	// PR-WAVE-1-DRIVE-SSOT (July 2026): the DriveAdmin field TYPE
	// is migrated from the banned `drive.Admin` to the
	// application-typed `clips.ClipDriveUploaderPort`. The
	// percheck_drive_access_ssot forward-prevention gate matches the
	// lowercase+dot `drive.Admin` substring; the capital-D field
	// name + application-typed port surface satisfies the gate.
	DriveAdmin      appclips.ClipDriveUploaderPort
	MediaProcessor  asset.Processor
	AssetTreeSvc    *assettree.Service
	MetaWriter      semantic.MetadataWriterPort
	ClipIndexer     *clipindexer.Service
	JobsSvc         job.Service
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
	// is fully retired.
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

// clipJobRegistrar is the canonical bridge from ClipsModule to the
// jobs service. Card 10: replaces the prior chain
// Descriptor.RegisterJobHandlers → Handler.RegisterJobHandlers →
// NonOpsHandler.RegisterJobHandlers. The Module holds a slim registrar
// (no Handler dependency); the boot-time diagnostic pointer snapshot
// is preserved.
type clipJobRegistrar struct {
	bulkUploadWorker *appclips.BulkUploadWorker
	jobsSvc          job.Service
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
	registerErr := svc.RegisterHandler(job.TypeBulkUploadYouTubeClips, appjobs.HandlerFunc(r.bulkUploadWorker.HandleJob))
	if r.log != nil {
		r.log.Info("clips: registered bulk_upload_youtube_clips handler (module-side)",
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

// ClipsModule is the upper descriptor exported for the Clips HTTP
// capability. PR-CLIPS-7-MODULES-UPPER-CLIPSMODULE (July 2026):
// replaced the legacy ClipsDescriptor with this struct. The four
// EXPORTED sub-descriptor fields are godlike/06 SSOT minimum-needed
// cross-package surface — Catalog/Ingest/Processing/Operations are
// the four sub-modules with cross-package composition-root consumers
// today. The three PRIVATE fields (publication/indexing/bulk) are
// routing-only and have no cross-package consumer; they are kept
// private so future drift surfaces as a build failure (godlike/06
// SSOT: no premature public expansion).
//
// All four exposed sub-descriptors and the three private ones are
// constructed from NARROW typed-narrow dependencies (cluster
// interfaces + standard infra) — not the generic 5-field
// RouteRegistrar pattern of the prior Wave 4 layout.
type ClipsModule struct {
	Module api.Module

	// Four EXPOSED sub-descriptors (godlike/06 SSOT minimum-needed
	// cross-package surface). Each is a typed-narrow *Descriptor
	// concrete, NOT the generic api.Descriptor interface — so
	// cross-package consumers can pin to the typed pointer.
	Catalog    *catalog.Descriptor
	Ingest     *ingest.Descriptor
	Processing *processing.Descriptor
	Operations *operations.Descriptor

	// Three PRIVATE routing-only sub-descriptors (publication,
	// indexing, bulk). No cross-package composition-root consumer
	// today. Kept private so they don't leak into the public
	// surface; promote to EXPORTED if a future composition-root
	// need arises (godlike/06 SSOT: don't pre-expand).
	publication *publication.Descriptor
	indexing    *indexing.Descriptor
	bulk        *bulk.Descriptor

	// jobReg is the slim bridge from the Module to the jobs service.
	jobReg *clipJobRegistrar
}

// Name returns the module name.
func (m *ClipsModule) Name() string { return m.Module.Name() }

// Enabled forwards to the module's closure.
func (m *ClipsModule) Enabled() bool { return m.Module.Enabled() }

// RegisterRoutes forwards to the module.
func (m *ClipsModule) RegisterRoutes(rg *gin.RouterGroup) {
	m.Module.RegisterRoutes(rg)
}

// RegisterJobHandlers implements api.DescriptorJobs by routing
// through the slim clipJobRegistrar (godlike/06 SSOT: the canonical
// 3-method chain's LEFTMOST entry).
func (m *ClipsModule) RegisterJobHandlers(svc api.JobRegistrar) error {
	if m.jobReg == nil {
		return nil
	}
	return m.jobReg.RegisterBulkUpload(svc, m)
}

// Build composes the Clips HTTP capability from NARROW typed-narrow
// per-sub-module dependencies. Fail-closed on mandatory nil deps
// (EnrichUC + JobsSvc + Cfg + EnabledFunc are now in the mandatory
// set per godlike/07 NO-FAKE-AVAILABILITY).
//
// Returns *ClipsModule instead of the legacy *ClipsDescriptor — the
// public surface now exposes the four typed sub-descriptor fields
// (godlike/06 SSOT minimum-needed cross-package surface).
func Build(deps Dependencies) (*ClipsModule, error) {
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

	// 7 sub-descriptors built from NARROW typed-narrow deps.
	// Each sub-module wires ONE or TWO typed cluster interfaces
	// (the parent's existing per-cluster handler pointers already
	// satisfy them via Go's structural interface satisfaction) +
	// the 4 standard infra fields. Per godlike/06 SSOT, zero
	// fat-orchestrator reach-through for the cluster route
	// binding (the parent's `h.FindDuplicates` method value is
	// passed directly as the catalog FindDuplicatesHandler since
	// gin.HandlerFunc is assignable from method values).
	catalogDesc, err := catalog.Build(catalog.Dependencies{
		Search:         handler.search,
		Folders:        handler.ops,
		FindDuplicates: catalog.FindDuplicatesHandler(handler.FindDuplicates),
		EnabledFunc:    deps.EnabledFunc,
		Idempotency:    idem,
		Logger:         log,
		ModuleOpts:     deps.ModuleOpts,
	})
	if err != nil {
		return nil, fmt.Errorf("clips.Build: catalog sub-descriptor: %w", err)
	}
	ingestDesc, err := ingest.Build(ingest.Dependencies{
		Ingest:      handler.ingest,
		EnabledFunc: deps.EnabledFunc,
		Idempotency: idem,
		Logger:      log,
		ModuleOpts:  deps.ModuleOpts,
	})
	if err != nil {
		return nil, fmt.Errorf("clips.Build: ingest sub-descriptor: %w", err)
	}
	processingDesc, err := processing.Build(processing.Dependencies{
		Processing:  handler.nonops,
		EnabledFunc: deps.EnabledFunc,
		Idempotency: idem,
		Logger:      log,
		ModuleOpts:  deps.ModuleOpts,
	})
	if err != nil {
		return nil, fmt.Errorf("clips.Build: processing sub-descriptor: %w", err)
	}
	publicationDesc, err := publication.Build(publication.Dependencies{
		Publication: handler,
		EnabledFunc: deps.EnabledFunc,
		Idempotency: idem,
		Logger:      log,
		ModuleOpts:  deps.ModuleOpts,
	})
	if err != nil {
		return nil, fmt.Errorf("clips.Build: publication sub-descriptor: %w", err)
	}
	indexingDesc, err := indexing.Build(indexing.Dependencies{
		Indexing:    handler.nonops,
		EnabledFunc: deps.EnabledFunc,
		Idempotency: idem,
		Logger:      log,
		ModuleOpts:  deps.ModuleOpts,
	})
	if err != nil {
		return nil, fmt.Errorf("clips.Build: indexing sub-descriptor: %w", err)
	}
	operationsDesc, err := operations.Build(operations.Dependencies{
		Ops:         handler.ops,
		BulkTags:    handler.nonops,
		EnabledFunc: deps.EnabledFunc,
		Idempotency: idem,
		Logger:      log,
		ModuleOpts:  deps.ModuleOpts,
	})
	if err != nil {
		return nil, fmt.Errorf("clips.Build: operations sub-descriptor: %w", err)
	}
	bulkDesc, err := bulk.Build(bulk.Dependencies{
		Transport:   handler.bulkTransport,
		EnabledFunc: deps.EnabledFunc,
		Idempotency: idem,
		Logger:      log,
		ModuleOpts:  deps.ModuleOpts,
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

	return &ClipsModule{
		Module:      mod,
		Catalog:     catalogDesc,
		Ingest:      ingestDesc,
		Processing:  processingDesc,
		Operations:  operationsDesc,
		publication: publicationDesc,
		indexing:    indexingDesc,
		bulk:        bulkDesc,
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

// godlike/06 SSOT compile-time pinning: the *ClipsModule returned
// by Build must satisfy the canonical api.Descriptor contract via
// the api.DescriptorJobs interface. Future drift in the 3 forwarder
// methods (Name/Enabled/RegisterRoutes) surfaces as a build failure,
// not a runtime panic.
var (
	_ api.Descriptor     = (*ClipsModule)(nil)
	_ api.DescriptorJobs = (*ClipsModule)(nil)
)
