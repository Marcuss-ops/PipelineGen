// Package submodule provides the generic building block for the
// clips capability sub-descriptors (catalog, ingest, processing,
// publication, indexing, operations, bulk).
//
// Each sub-descriptor is a thin wrapper around a RouteRegistrar: a
// small handler cluster that already knows how to register its own
// routes. The wrapper turns that cluster into a canonical
// *Descriptor so the parent clips module can aggregate them AND
// expose the typed sub-descriptor pointer on the upper ClipsModule
// struct (godlike/06 minimum-needed cross-package surface).
//
// PR-CLIPS-7-MODULES-UPPER-CLIPSMODULE (July 2026): Build returns
// the *Descriptor concrete (not the generic api.Descriptor
// interface) so callers can do `module.Catalog.RegisterRoutes(rg)`
// against the typed concrete — godlike/06 SSOT one-canonical-owner
// per fact: the typed contract IS the per-sub-module contract, no
// generic bridge allocation.
package assets

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
// Exposed typed so the parent ClipsModule struct can hold
// `*catalog.Descriptor` / `*ingest.Descriptor` / etc. fields with
// the per-cluster concrete type (godlike/06 SSOT: minimum-needed
// cross-package surface, no premature interface widening).
type Descriptor struct {
	module api.Module
}

// Name returns the module name.
func (d *Descriptor) Name() string { return d.module.Name() }

// Enabled forwards to the module's closure.
func (d *Descriptor) Enabled() bool { return d.module.Enabled() }

// RegisterRoutes forwards to the module.
func (d *Descriptor) RegisterRoutes(rg *gin.RouterGroup) { d.module.RegisterRoutes(rg) }

// godlike/06 SSOT: the Descriptor's `module` field is exposed
// read-only through the Module accessor for construction-sites
// (api.NewRouteModule calls) that need the underlying Module
// value. Cross-package consumers stay agnostic and use the typed
// *Descriptor pointer.
func (d *Descriptor) Module() api.Module { return d.module }

// Build wraps the supplied RouteRegistrar in a canonical *Descriptor.
// Missing mandatory deps fail closed (godlike/07 NO-FAKE-AVAILABILITY).
func Build(d Deps) (*Descriptor, error) {
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
	return &Descriptor{module: mod}, nil
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
