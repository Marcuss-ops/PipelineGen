// Package clips exposes the canonical Clips HTTP module.
package clips

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/clips/processing"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/clips/operations"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/clips/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/clips/catalog"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/clips/bulk"
	"fmt"

	api "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/clips/publication"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	jobmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// TransportDeps contains only module-level HTTP concerns. Repositories,
// delivery adapters and media processors are intentionally absent.
type TransportDeps struct {
	Idempotency gin.HandlerFunc
	EnabledFunc func() bool
	Logger      *zap.Logger
	ModuleOpts  []api.RouteModuleOption
}

// Dependencies is the immutable Build contract. Handlers is split by real
// route capability in Deps; Transport owns only common HTTP infrastructure.
type Dependencies struct {
	Handlers  Deps
	Transport TransportDeps
}

// clipJobRegistrar is the narrow boot-time bridge for the bulk upload worker.
type clipJobRegistrar struct {
	bulkUploadWorker *appclips.BulkUploadWorker
	jobsSvc          job.Service
	log              *zap.Logger
}

func (r *clipJobRegistrar) RegisterBulkUpload(svc api.JobRegistrar, descriptorPtr interface{}) error {
	if r.bulkUploadWorker == nil || r.jobsSvc == nil {
		return fmt.Errorf("%w: clipJobRegistrar: BulkUploadWorker or JobsSvc nil at registration", appjobs.ErrJobsSvcRequiredAtRegistration)
	}
	registerErr := svc.RegisterHandler(jobmedia.TypeBulkUploadYouTubeClips, appjobs.HandlerFunc(r.bulkUploadWorker.HandleJob))
	if r.log != nil {
		r.log.Info("clips: registered bulk_upload_youtube_clips handler",
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

// ClipsModule is the upper descriptor. Only sub-descriptors with real
// cross-package consumers are exported.
type ClipsModule struct {
	Module api.Module

	Catalog    *catalog.Descriptor
	Ingest     *ingest.Descriptor
	Processing *processing.Descriptor
	Operations *operations.Descriptor

	bulk   *bulk.Descriptor
	jobReg *clipJobRegistrar
}

func (m *ClipsModule) Name() string  { return m.Module.Name() }
func (m *ClipsModule) Enabled() bool { return m.Module.Enabled() }

func (m *ClipsModule) RegisterRoutes(rg *gin.RouterGroup) {
	m.Module.RegisterRoutes(rg)
}

func (m *ClipsModule) RegisterJobHandlers(svc api.JobRegistrar) error {
	if m.jobReg == nil {
		return nil
	}
	return m.jobReg.RegisterBulkUpload(svc, m)
}

// JobHandlers exposes the clips-owned worker handlers as runtime descriptors.
// The composition root publishes this list through the canonical runtime
// descriptor path; RegisterJobHandlers remains as a compatibility slot for
// older callers and tests.
func (m *ClipsModule) JobHandlers() []api.JobHandlerDescriptor {
	if m == nil || m.jobReg == nil || m.jobReg.bulkUploadWorker == nil {
		return nil
	}
	return []api.JobHandlerDescriptor{{
		Type:    jobmedia.TypeBulkUploadYouTubeClips,
		Handler: appjobs.HandlerFunc(m.jobReg.bulkUploadWorker.HandleJob),
	}}
}

// Build constructs transport handlers from already-built application use
// cases. No repository, processing or delivery orchestration occurs here.
func Build(deps Dependencies) (*ClipsModule, error) {
	if deps.Handlers.Search.ClipsRepo == nil {
		return nil, fmt.Errorf("Build: Search.ClipsRepo is required")
	}
	if deps.Handlers.NonOps.JobsSvc == nil {
		return nil, fmt.Errorf("Build: NonOps.JobsSvc is required")
	}
	if deps.Transport.EnabledFunc == nil {
		return nil, fmt.Errorf("Build: Transport.EnabledFunc is required")
	}
	if deps.Handlers.Ingest.EnrichUC == nil {
		return nil, fmt.Errorf("Build: Ingest.EnrichUC is required")
	}

	log := deps.Transport.Logger
	if log == nil {
		log = zap.NewNop()
	}
	idem := deps.Transport.Idempotency
	if idem == nil {
		idem = func(c *gin.Context) { c.Next() }
	}

	handler, err := NewHandlerStrict(deps.Handlers)
	if err != nil {
		return nil, fmt.Errorf("Build: %w", err)
	}

	catalogDesc, err := catalog.Build(catalog.Dependencies{
		Search:         handler.search,
		Folders:        handler.ops,
		FindDuplicates: catalog.FindDuplicatesHandler(handler.actions.FindDuplicates),
		EnabledFunc:    deps.Transport.EnabledFunc,
		Idempotency:    idem,
		Logger:         log,
		ModuleOpts:     deps.Transport.ModuleOpts,
	})
	if err != nil {
		return nil, fmt.Errorf("Build: catalog sub-descriptor: %w", err)
	}
	ingestDesc, err := ingest.Build(ingest.Dependencies{
		Ingest:      handler.ingest,
		EnabledFunc: deps.Transport.EnabledFunc,
		Idempotency: idem,
		Logger:      log,
		ModuleOpts:  deps.Transport.ModuleOpts,
	})
	if err != nil {
		return nil, fmt.Errorf("Build: ingest sub-descriptor: %w", err)
	}
	processingDesc, err := processing.Build(processing.Dependencies{
		Processing:  handler.nonops,
		EnabledFunc: deps.Transport.EnabledFunc,
		Idempotency: idem,
		Logger:      log,
		ModuleOpts:  deps.Transport.ModuleOpts,
	})
	if err != nil {
		return nil, fmt.Errorf("Build: processing sub-descriptor: %w", err)
	}
	publicationDesc, err := publication.Build(publication.Dependencies{
		Publication: handler.actions,
		EnabledFunc: deps.Transport.EnabledFunc,
		Idempotency: idem,
		Logger:      log,
		ModuleOpts:  deps.Transport.ModuleOpts,
	})
	if err != nil {
		return nil, fmt.Errorf("Build: publication sub-descriptor: %w", err)
	}
	operationsDesc, err := operations.Build(operations.Dependencies{
		Ops:         handler.ops,
		EnabledFunc: deps.Transport.EnabledFunc,
		Idempotency: idem,
		Logger:      log,
		ModuleOpts:  deps.Transport.ModuleOpts,
	})
	if err != nil {
		return nil, fmt.Errorf("Build: operations sub-descriptor: %w", err)
	}
	bulkDesc, err := bulk.Build(bulk.Dependencies{
		Transport:   handler.bulkTransport,
		EnabledFunc: deps.Transport.EnabledFunc,
		Idempotency: idem,
		Logger:      log,
		ModuleOpts:  deps.Transport.ModuleOpts,
	})
	if err != nil {
		return nil, fmt.Errorf("Build: bulk sub-descriptor: %w", err)
	}

	mod := api.NewRouteModule(
		"clips",
		deps.Transport.EnabledFunc,
		"/clips",
		&subModuleAdapter{subModules: []api.Descriptor{
			catalogDesc, ingestDesc, processingDesc,
			operationsDesc, bulkDesc, publicationDesc,
		}},
		log,
		deps.Transport.ModuleOpts...,
	)

	return &ClipsModule{
		Module:     mod,
		Catalog:    catalogDesc,
		Ingest:     ingestDesc,
		Processing: processingDesc,
		Operations: operationsDesc,
		bulk:       bulkDesc,
		jobReg: &clipJobRegistrar{
			bulkUploadWorker: deps.Handlers.NonOps.BulkUploadWorker,
			jobsSvc:          deps.Handlers.NonOps.JobsSvc,
			log:              log,
		},
	}, nil
}

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

var (
	_ api.Descriptor            = (*ClipsModule)(nil)
	_ api.DescriptorJobs        = (*ClipsModule)(nil)
	_ api.DescriptorJobHandlers = (*ClipsModule)(nil)
)
