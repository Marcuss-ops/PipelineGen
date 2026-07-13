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
// UNUSED-AT-BUILD (godlike/07 NO-FAKE-AVAILABILITY, July 2026): the
// HTTP handler (SearchAndRun / RunStockPipeline / DownloadStockClip)
// was retired — no route mounting site references this Build, and
// /api/stock-pipeline/* is unmounted everywhere. Per godlike/07 we
// MUST NOT advertise the handler as available; Build instead returns
// a fail-closed descriptor that exposes a nil-handler RouteModule
// (RouteModule.RegisterRoutes logs a Warn and skips route registration
// when the handler is nil, mirroring the canonical nil-tolerant
// lifecycle contract in internal/api/module_route_module.go).
//
// godlike/06 SSOT (one canonical owner per fact):
//   - The canonical StockUseCase constructor lives in
//     stockpipeline/usecase.go::NewStockUseCase.
//   - The StockDescriptor lives in this file.
//   - The composition root continues to wire the canonical stock
//     pipeline service via the symmetric publisher/finalizer gate at
//     build_bundles_stock.go::BuildStockBundle — only the HTTP
//     projection surface (handler + request validation +
//     download streaming) is retired here.
//
// Pattern parity with the artlist / youtube / clips Blocco C1-Step
// modules: Descriptor does NOT embed Module; the explicit field form
// does not promote Name / Enabled / RegisterRoutes via embedding.
package stock

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Dependencies is the typed narrow input to Build. The HTTP surface is
// retired (godlike/07), so Dependencies reduces to the canonical
// StockUseCase + the EnabledFunc closure — the minimum required to
// construct a fail-closed Descriptor that the composition root can
// type-assert as *StockDescriptor (no callers consume the handler, but
// the Descriptor shape remains stable so registry_registration.go's
// type-assertions continue to compile).
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
}

// StockDescriptor is the concrete capability Descriptor returned by
// Build. It satisfies api.Descriptor via the explicit Module field
// (named, not embedded — no method-promotion surprises from
// api.Module) and forwarder methods. Module carries a nil handler
// (godlike/07 fail-closed) so RouteModule.RegisterRoutes logs a Warn
// and skips route registration.
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

// RegisterRoutes forwards to the Module. With a nil handler
// (godlike/07 fail-closed), RouteModule.RegisterRoutes logs a Warn
// and returns without registering any routes.
func (d *StockDescriptor) RegisterRoutes(rg *gin.RouterGroup) {
	d.Module.RegisterRoutes(rg)
}

// Build composes the Stock HTTP capability from the typed narrow
// dependencies. Returns a fail-closed error when any mandatory dep
// is nil. The HTTP handler is deliberately NOT constructed — the
// capability is unmounted everywhere per godlike/07
// NO-FAKE-AVAILABILITY and the package-level doc comment above.
//
// The returned Descriptor carries the Module (routes). The Module's
// handler is nil; api.RouteModule.RegisterRoutes detects that and
// logs a Warn + returns, so a composition root that mistakenly
// invokes RegisterRoutes sees the safe no-op behaviour instead of a
// nil-deref.
func Build(deps Dependencies) (api.Descriptor, error) {
	// ── Mandatory-shape validation ────────────────────────────────
	if deps.UseCase == nil {
		return nil, fmt.Errorf("stock.Build: UseCase is required (composition root must pre-construct *stockpipeline.StockUseCase from the stock bundle before calling Build)")
	}
	if deps.EnabledFunc == nil {
		return nil, fmt.Errorf("stock.Build: EnabledFunc is required (composition root must wire cfg.Features.StockPipelineEnabled as a closure so this package stays free of platform/config imports)")
	}

	// Construct the route Module with a NIL handler
	// (godlike/07 fail-closed). api.RouteModule.RegisterRoutes has
	// the canonical `if m.handler == nil { warn + return }` guard, so
	// the no-op behaviour is safe + observable.
	mod := api.NewRouteModule(
		"stock-pipeline",
		deps.EnabledFunc,
		"/stock-pipeline",
		nil, // handler (godlike/07: nil — RegisterRoutes logs Warn + skips)
		zap.NewNop(),
	)

	return &StockDescriptor{
		Module: mod,
	}, nil
}
