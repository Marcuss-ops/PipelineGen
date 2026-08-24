// Package stock — module.go: the single canonical Build entrypoint for
// the Stock HTTP capability.
//
// Capability Standard module.go contract:
//
//	func Build(deps Dependencies) (api.Descriptor, error)
//
// The returned Descriptor is complete: missing mandatory dependencies
// return an error during composition; the capability does not create
// partially-initialized services. Once Build returns, the descriptor is
// ready to be registered into the api.Registry by the composition root.
//
// godlike/06 SSOT (one canonical owner per fact):
//   - The canonical StockUseCase constructor lives in
//     stockpipeline/usecase.go::NewStockUseCase.
//   - The StockDescriptor lives in this file.
//   - The composition root wires the canonical stock pipeline service
//     via the symmetric publisher/finalizer gate at
//     build_bundles_stock.go::BuildStockBundle.
//
// Pattern parity with the artlist / youtube / clips Blocco C1-Step
// modules: Descriptor does NOT embed Module; the explicit field form
// does not promote Name / Enabled / RegisterRoutes via embedding.
package stock

import (
	"fmt"

	api "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Dependencies is the typed narrow input to Build.
type Dependencies struct {
	// UseCase is the canonical *stockpipeline.StockUseCase
	// constructed by the composition root (WireStockPipeline) from
	// the stock bundle (cfg + log + drive + storage + media +
	// jobs). MANDATORY — Build returns an error when nil.
	UseCase *stockpipeline.StockUseCase

	// EnabledFunc is the closure that decides whether the
	// module's routes are mounted. MANDATORY — Build returns an
	// error when nil (so this package stays free of platform/config
	// imports).
	EnabledFunc func() bool

	// Logger is the structured logger. Optional (nil → zap.NewNop()).
	Logger *zap.Logger

	// ModuleOpts are optional RouteModule options (e.g. middleware).
	ModuleOpts []api.RouteModuleOption
}

// StockDescriptor is the concrete capability Descriptor returned by
// Build. It satisfies api.Descriptor via the explicit Module field
// (named, not embedded — no method-promotion surprises from
// api.Module) and forwarder methods.
type StockDescriptor struct {
	// Module is the route-only Module (api.NewRouteModule instance)
	// the composition root threads into the capability_registry.
	Module api.Module
}

// ── Module satisfaction (api.Descriptor) ────────────────────────────
// Descriptor does NOT embed Module. The explicit field form does not
// promote Name / Enabled / RegisterRoutes via embedding, so we
// forward them by hand. (Matches the Artlist / YouTube / Clips
// precedent.)

// Name returns the module name ("stock-pipeline"). Preserved verbatim
// from the pre-§-retirement wiring so any future re-introduction of
// the route keeps the same MountWithRoot name.
func (d *StockDescriptor) Name() string {
	return d.Module.Name()
}

// Enabled forwards to the Module's closure.
func (d *StockDescriptor) Enabled() bool {
	return d.Module.Enabled()
}

// RegisterRoutes forwards to the Module.
func (d *StockDescriptor) RegisterRoutes(rg *gin.RouterGroup) {
	d.Module.RegisterRoutes(rg)
}

// Build composes the Stock HTTP capability from the typed narrow
// dependencies. Returns a fail-closed error when any mandatory dep
// is nil.
func Build(deps Dependencies) (api.Descriptor, error) {
	// ── Mandatory-shape validation ────────────────────────────────
	if deps.UseCase == nil {
		return nil, fmt.Errorf("stock.Build: UseCase is required (composition root must pre-construct *stockpipeline.StockUseCase from the stock bundle before calling Build)")
	}
	if deps.EnabledFunc == nil {
		return nil, fmt.Errorf("stock.Build: EnabledFunc is required (composition root must wire cfg.Features.StockPipelineEnabled as a closure so this package stays free of platform/config imports)")
	}

	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}

	handler := NewStockHandler(deps.UseCase, log)

	mod := api.NewRouteModule(
		"stock-pipeline",
		deps.EnabledFunc,
		"/stock-pipeline",
		handler,
		log,
		deps.ModuleOpts...,
	)

	return &StockDescriptor{
		Module: mod,
	}, nil
}
