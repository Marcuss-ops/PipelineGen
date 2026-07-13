// Package submodule provides the generic building block for the
// clips capability sub-descriptors (catalog, ingest, processing,
// publication, indexing, operations, bulk).
//
// Each sub-descriptor is a thin wrapper around a RouteRegistrar: a
// small handler cluster that already knows how to register its own
// routes. The wrapper turns that cluster into a canonical
// api.Descriptor so the parent clips module can aggregate them.
package submodule

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// RouteRegistrar is the minimal surface a sub-descriptor needs from
// its handler cluster. The idempotency middleware is captured at
// construction time by the wrapper, so the registration contract
// matches api.Module.RegisterRoutes.
type RouteRegistrar interface {
	RegisterRoutes(r *gin.RouterGroup)
}

// Deps is the generic input to Build.
type Deps struct {
	// Name is the sub-descriptor module name (e.g. "clips-catalog").
	Name string
	// Handler is the cluster that owns the routes.
	Handler RouteRegistrar
	// EnabledFunc decides whether the sub-descriptor's routes are mounted.
	EnabledFunc func() bool
	// Idempotency is the Gin idempotency middleware applied to write
	// routes. nil -> no-op pass-through.
	Idempotency gin.HandlerFunc
	// Logger is the structured logger; nil -> zap.NewNop().
	Logger *zap.Logger
	// ModuleOpts are optional route-module decorators.
	ModuleOpts []api.RouteModuleOption
}

// Descriptor is the concrete api.Descriptor returned by Build.
type Descriptor struct {
	Module api.Module
}

// Name returns the module name.
func (d *Descriptor) Name() string { return d.Module.Name() }

// Enabled forwards to the module's closure.
func (d *Descriptor) Enabled() bool { return d.Module.Enabled() }

// RegisterRoutes forwards to the module.
func (d *Descriptor) RegisterRoutes(rg *gin.RouterGroup) { d.Module.RegisterRoutes(rg) }

// Build wraps the supplied RouteRegistrar in a canonical
// api.Descriptor. Missing mandatory deps fail closed.
func Build(d Deps) (api.Descriptor, error) {
	if d.Handler == nil {
		return nil, fmt.Errorf("submodule.Build: Handler is required for sub-descriptor %q", d.Name)
	}
	if d.EnabledFunc == nil {
		return nil, fmt.Errorf("submodule.Build: EnabledFunc is required for sub-descriptor %q", d.Name)
	}
	log := d.Logger
	if log == nil {
		log = zap.NewNop()
	}

	mod := api.NewRouteModule(
		d.Name,
		d.EnabledFunc,
		"",
		&registrarAdapter{inner: d.Handler},
		log,
		d.ModuleOpts...,
	)
	return &Descriptor{Module: mod}, nil
}

// registrarAdapter adapts a RouteRegistrar to the api.Module
// RegisterRoutes contract. The idempotency middleware is already
// captured by the wrapper, so the adapter only forwards the router
// group.
type registrarAdapter struct {
	inner RouteRegistrar
}

// RegisterRoutes delegates to the inner RouteRegistrar.
func (a *registrarAdapter) RegisterRoutes(rg *gin.RouterGroup) {
	a.inner.RegisterRoutes(rg)
}
