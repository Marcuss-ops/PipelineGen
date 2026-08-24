// Package cliprender — module.go: the single canonical Build entrypoint
// for the clip.render HTTP capability.
//
// It is a NEW capability, separate from /api/clips/process (YouTube
// ingest/extraction) and /api/clips/stock (stock acquisition). It
// enqueues a canonical Master job (clip.render) on the same queue and
// worker model as every other capability — no second renderer, no
// second queue.
//
// This package owns BOTH the business contract (request.go), the job
// handler binding (worker.go) and the HTTP transport (handler.go +
// contract.go). Transport lives in the capability (a target root) per
// the architecture migration policy: internal/api is migration-only and
// forbids new files, so the capability owns its own thin gin surface
// (mirror of internal/capabilities/jobs/transport).
//
// Capability Standard module.go contract:
//
//	func Build(deps Dependencies) (api.Descriptor, error)
//
// The returned Descriptor is complete: missing mandatory dependencies
// return an error during composition; the capability does not create
// partially-initialized services. Once Build returns, the descriptor is
// ready to be registered into the api.Registry by the composition root.
package cliprender

import (
	"fmt"

	api "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Dependencies is the typed narrow input to Build.
type Dependencies struct {
	// Jobs is the canonical Master job service. MANDATORY — Build
	// returns an error when nil (the render endpoint is a pure
	// enqueue surface; there is no inline execution path).
	Jobs job.Service

	// EnabledFunc is the closure that decides whether the module's
	// routes are mounted. MANDATORY — Build returns an error when nil
	// (so this package stays free of platform/config imports).
	EnabledFunc func() bool

	// Idempotency is the reusable Gin idempotency middleware applied
	// to POST /clips/render. Optional (nil disables).
	Idempotency gin.HandlerFunc

	// Logger is the structured logger. Optional (nil → zap.NewNop()).
	Logger *zap.Logger

	// ModuleOpts are optional RouteModule options (e.g. middleware).
	ModuleOpts []api.RouteModuleOption
}

// Descriptor is the concrete capability Descriptor returned by Build.
// It satisfies api.Descriptor via the explicit Module field (named, not
// embedded — no method-promotion surprises from api.Module) and
// forwarder methods.
type Descriptor struct {
	Module api.Module
}

// Name returns the module name ("clip-render"). Distinct from the
// youtube-owned "clips" module name so both can mount routes under the
// /api/clips prefix without colliding in the strict-uniqueness registry.
func (d *Descriptor) Name() string {
	return d.Module.Name()
}

// Enabled forwards to the Module's closure.
func (d *Descriptor) Enabled() bool {
	return d.Module.Enabled()
}

// RegisterRoutes forwards to the Module.
func (d *Descriptor) RegisterRoutes(rg *gin.RouterGroup) {
	d.Module.RegisterRoutes(rg)
}

// Build composes the clip.render HTTP capability from the typed narrow
// dependencies. Returns a fail-closed error when any mandatory dep is
// nil.
func Build(deps Dependencies) (api.Descriptor, error) {
	if deps.Jobs == nil {
		return nil, fmt.Errorf("cliprender.Build: Jobs is required (the POST /clips/render enqueue path is unreachable without job.Service)")
	}
	if deps.EnabledFunc == nil {
		return nil, fmt.Errorf("cliprender.Build: EnabledFunc is required (composition root must wire cfg.Features.ClipRenderEnabled as a closure so this package stays free of platform/config imports)")
	}

	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}

	handler := NewHandler(deps.Jobs, log)
	handler.Idempotency = deps.Idempotency

	mod := api.NewRouteModule(
		"clip-render",
		deps.EnabledFunc,
		"/clips",
		handler,
		log,
		deps.ModuleOpts...,
	)

	return &Descriptor{Module: mod}, nil
}
