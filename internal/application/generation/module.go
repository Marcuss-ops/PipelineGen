// Package generation — module.go: the single canonical Build
// entrypoint for the Generation capability.
//
// Capability Standard module.go contract:
//
//	func Build(deps Dependencies) (api.Descriptor, error)
//
// The result is complete: missing mandatory dependencies return
// an error during composition; they must not create partially
// initialized services. Once Build returns, the descriptor is
// ready to be registered into the api.Registry and (via the
// optional DescriptorJobs slot) publish worker handlers into the
// canonical jobs service.
//
// Used by the composition root at internal/app/registry.go::WireRegistry.
// External CLIs (none today) could use Service directly via
// NewService + BuildDefaultRegistry, just like the Channels
// precedent.
//
// The narrow ports JobService and AssetStore are defined in
// service.go (where they are consumed by NewService) and re-used
// here to avoid duplicate declarations in the same package.
package generation

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Dependencies is the typed narrow input to Build.
//
// Jobs is mandatory (no fallback; the capability cannot operate
// without the dispatcher).
//
// Books / Lessons are the worker-side handler function values.
// Both are typed as HandlerFunc (alias for appjobs.HandlerFunc) so
// this package does NOT import internal/application/books or
// internal/application/lessons — those packages transitively
// import internal/application/generation (for generation.Response[T]
// envelope types used by their use cases), which would create an
// import cycle.
//
// A nil handler value means "do not register that worker type";
// composition-time nil is tolerated (the optional-interface slot
// pattern: feature flag off → nil dep → skipped registration).
//
// BooksEnabled / LessonsEnabled / ScriptEnabled drive the in-process
// Definition registration flags.
//
// Logger nil → zap.NewNop().
type Dependencies struct {
	// Jobs is the canonical jobs service used by the dispatcher
	// path (Service.NewService). Mandatory.
	Jobs JobService
	// Assets resolves persisted source assets for book-Generate
	// requests. Optional — Books still works when nil.
	Assets AssetStore
	// Books is the worker-side handler for books.process jobs.
	// nil = skip registration (BooksEnabled=false or wired
	// sub-service absent).
	Books appjobs.HandlerFunc
	// Lessons is the worker-side handler for lessons.process jobs.
	// nil = skip registration.
	Lessons appjobs.HandlerFunc
	// BooksEnabled drives the TypeBookGenerate registration flag.
	BooksEnabled bool
	// LessonsEnabled drives the TypeLessonGenerate registration flag.
	LessonsEnabled bool
	// ScriptEnabled drives the three script Definitions' flag.
	ScriptEnabled bool
	// Logger is the canonical structured logger. nil → zap.NewNop().
	Logger *zap.Logger
}

// GenerationDescriptor is the concrete capability Descriptor
// returned by Build. It exposes the api.Descriptor interface via
// explicit Module field + delegation methods (mirrors Channels'
// round-3 pattern to avoid method-promotion surprises) AND the
// api.DescriptorJobs optional interface directly via a typed field.
type GenerationDescriptor struct {
	// Module is the route-only Module (api.NewRouteModule instance)
	// the composition root registers for HTTP traffic.
	Module api.Module
	// Service is exposed so non-HTTP callers (admin tools, tests)
	// can drive the capability without re-constructing the use-case
	// layer.
	Service *Service
	// JobHandlers is the worker-side publication. The composition
	// root type-asserts on DescriptorJobs and calls RegisterJobHandlers
	// with root.Jobs.Service (passed via the typed JobRegistrar port).
	JobHandlers JobHandlers
}

// ── Module satisfaction (api.Descriptor) ──────────────────────
// Descriptor embeds Module; the explicit field form does not
// promote Name / Enabled / RegisterRoutes via embedding, so we
// forward them by hand. (Matches the Channels precedent.)

func (d *GenerationDescriptor) Name() string {
	return d.Module.Name()
}
func (d *GenerationDescriptor) Enabled() bool {
	return d.Module.Enabled()
}
func (d *GenerationDescriptor) RegisterRoutes(rg *gin.RouterGroup) {
	d.Module.RegisterRoutes(rg)
}

// ── DescriptorJobs satisfaction ────────────────────────────────

// RegisterJobHandlers delegates to JobHandlers.RegisterJobHandlers.
// Implements api.DescriptorJobs.
func (d *GenerationDescriptor) RegisterJobHandlers(svc api.JobRegistrar) error {
	return d.JobHandlers.RegisterJobHandlers(svc)
}

// Build constructs the Generation capability: Jobs → Service →
// HTTP Handler → Module. The returned Descriptor carries the
// Service and JobHandlers slots so:
//   - the registry path consumes Module for routes + DescriptorJobs
//     for worker handlers,
//   - non-HTTP callers consume Service for Create/Status/Cancel.
//
// Returns an error when Jobs is nil: the capability cannot operate
// without the canonical jobs service. Logger is replaced with
// zap.NewNop() when nil.
func Build(deps Dependencies) (api.Descriptor, error) {
	if deps.Jobs == nil {
		return nil, fmt.Errorf("generation.Build: Jobs is required")
	}
	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}

	reg := BuildDefaultRegistry(deps.BooksEnabled, deps.LessonsEnabled, deps.ScriptEnabled)
	svc := NewService(deps.Jobs, deps.Assets, reg)
	handler := NewHandler(svc, log)

	return &GenerationDescriptor{
		Module: api.NewRouteModule(
			"generation",
			func() bool { return true },
			"/generations",
			handler,
			log,
		),
		Service: svc,
		JobHandlers: JobHandlers{
			Books:   deps.Books,
			Lessons: deps.Lessons,
			Log:     log,
		},
	}, nil
}
