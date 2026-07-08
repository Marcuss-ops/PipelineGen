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
// This file is part of Blocco C1-Step 6 (June 2026): every capability
// in `internal/api/**` and `internal/application/**` MUST expose a
// Build(d) signature. Direct canonical-registry Calls inside a
// capability package are forbidden (godlike/07 + the canonical
// `internal/app/capability_registry.go` hoist site landed in
// Blocco C1-Step 2). The composition root consumes this Build via
// `internal/app/module_sources.go::WireStockPipeline` and threads
// the returned Descriptor into the capability_registry.
//
// Pattern parity with `artlist/module.go` (C1-Step 3),
// `youtube/module.go` (C1-Step 4), `clips/module.go` (C1-Step 5).
//
// UNIQUE TO STOCK: the package owns a thin *Handler that exposes only
// 2 routes (POST /run + POST /search-and-run) over a single use case
// (stockpipeline.StockUseCase). The handler has no sub-handlers, no
// mirror fields, no NON-Ops inline methods — the smallest handler in
// the assets tree today. The Build contract therefore mirrors the
// artlist shape exactly (no `Handler` field on the Descriptor; only
// `Module` + forwarder methods). The use case is internal to the
// stockpipeline package and is NOT exposed via the Descriptor
// (matches the artlist precedent of NOT exposing the service in the
// Descriptor for consumers that don't need it).
package stock

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Dependencies is the typed narrow input to Build. Stock's handler is
// the smallest in the assets tree (1 use case + 1 logger), so the
// Dependency surface is correspondingly narrow.
//
// Mandatory fields return an error when nil; optional fields fall
// through to the handler's existing nil-tolerance (Handler.NewHandler
// already defaults a nil log to zap.NewNop(); use case is forwarded
// as-is and the route handlers nil-check the use case at request time).
//
// Logger nil → zap.NewNop() (composition-root-friendly default).
type Dependencies struct {
	// UseCase is the canonical *stockpipeline.StockUseCase
	// constructed by the composition root (WireStockPipeline) from
	// the stock bundle (cfg + log + drive + storage + media +
	// youtube + jobs). The use case owns the dispatch decision
	// (async-vs-sync, jobs-required 503) for both /run and
	// /search-and-run. MANDATORY — Build returns an error when
	// nil.
	UseCase *stockpipeline.StockUseCase

	// EnabledFunc is the closure that decides whether the
	// module's routes are mounted. The composition root wires the
	// canonical `cfg.Features.StockPipelineEnabled` closure.
	// MANDATORY — Build returns an error when nil (so this
	// package stays free of platform/config imports).
	EnabledFunc func() bool

	// ModuleOpts are variadic `api.RouteModuleOption` decorators
	// (typically `api.WithMiddleware(...)`) applied to the
	// RouteModule at Build time. OPTIONAL — nil produces a plain
	// RouteModule.
	ModuleOpts []api.RouteModuleOption

	// AssetLookup is the narrow port for looking up a media asset
	// by ID (stock download endpoint). Satisfied by
	// *assets.ClipsRepository.Get. OPTIONAL — nil produces a 503
	// on /clips/:id/download (godlike/07 fail-closed).
	AssetLookup StockAssetLookup

	// DriveReader is the narrow port for streaming files from
	// Google Drive (stock download endpoint). Satisfied by
	// drive.Reader. OPTIONAL — nil produces a 503 on
	// /clips/:id/download.
	DriveReader StockDriveReader

	// Logger is the canonical structured logger. nil →
	// zap.NewNop() (composition-root-friendly default).
	Logger *zap.Logger
}

// StockDescriptor is the concrete capability Descriptor returned
// by Build. It satisfies api.Descriptor via the explicit Module
// field (named, not embedded — no method-promotion surprises from
// api.Module) and forwarder methods.
//
// UNIQUE TO STOCK: the Descriptor does NOT expose the handler
// (matches the artlist precedent of dropping the explicit Handler
// field). There is no non-HTTP consumer of the stock handler in the
// codebase — /run + /search-and-run are the entire public surface,
// both reachable via HTTP. The handler stays the internal worker
// captured by the Module closure; no caller reads a raw *Handler
// from outside the package.
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

// Name returns the module name ("stock-pipeline"). Preserved
// verbatim from the pre-Step-6 wiring so the public route prefix
// `/api/stock-pipeline/*` stays unchanged (zero-change-contract).
func (d *StockDescriptor) Name() string {
	return d.Module.Name()
}

// Enabled forwards to the Module's closure.
func (d *StockDescriptor) Enabled() bool {
	return d.Module.Enabled()
}

// RegisterRoutes forwards to the Module — the Handler is reachable
// only via the Module's internal closure.
func (d *StockDescriptor) RegisterRoutes(rg *gin.RouterGroup) {
	d.Module.RegisterRoutes(rg)
}

// Build composes the Stock HTTP capability from the typed narrow
// dependencies. Returns a fail-closed error when any mandatory dep
// is nil. Logger nil → zap.NewNop(). ModuleOpts nil → no decorators.
//
// The returned Descriptor carries the Module (routes). The HTTP
// Handler is constructed here and captured by the Module's
// RegisterRoutes closure — no caller (composition root, tests,
// internal services) reads the raw Handler anywhere outside this
// function.
func Build(deps Dependencies) (api.Descriptor, error) {
	// ── Mandatory-shape validation ────────────────────────────────
	if deps.UseCase == nil {
		return nil, fmt.Errorf("stock.Build: UseCase is required (composition root must pre-construct *stockpipeline.StockUseCase from the stock bundle before calling Build)")
	}
	if deps.EnabledFunc == nil {
		return nil, fmt.Errorf("stock.Build: EnabledFunc is required (composition root must wire cfg.Features.StockPipelineEnabled as a closure so this package stays free of platform/config imports)")
	}

	// Logger: nil → zap.NewNop() (composition-root-friendly default).
	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}

	// Construct the canonical Handler. NewHandler has its own
	// nil-tolerance for log (defaults to zap.NewNop()).
	handler := NewHandler(deps.UseCase, log, deps.AssetLookup, deps.DriveReader)

	// Construct the route Module. The closure inside
	// api.NewRouteModule calls handler.RegisterRoutes(r) — the
	// Handler is captured here, not exposed to the composition
	// root via the Module surface.
	mod := api.NewRouteModule(
		"stock-pipeline",
		deps.EnabledFunc,
		"/stock-pipeline",
		handler,
		log,
		deps.ModuleOpts..., // typically []ModuleOption{api.WithMiddleware(...)}
	)

	return &StockDescriptor{
		Module: mod,
	}, nil
}
