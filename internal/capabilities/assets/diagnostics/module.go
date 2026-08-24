// Package diagnostics — module.go: the single canonical Build entrypoint for
// the Diagnostics HTTP capability.
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
// This file is part of Blocco C1-Step 10 (June 2026): every capability
// in `internal/api/**` and `internal/application/**` MUST expose a
// Build(d) signature. Direct canonical-registry Calls inside a
// capability package are forbidden (godlike/07 + the canonical
// `internal/app/capability_registry.go` hoist site landed in
// Blocco C1-Step 2). The composition root consumes this Build via
// `internal/app/module_media.go::WireAssets` and threads the returned
// Descriptor into `assetsapi.Dependencies.Diagnostics` (route module
// that mounts /diagnostics + /index-health + /qdrant/cleanup on the
// parent /api/media group).
//
// Pattern parity with `artlist/module.go` (C1-Step 3),
// `youtube/module.go` (C1-Step 4), `clips/module.go` (C1-Step 5),
// `stock/module.go` (C1-Step 6), `voiceover/module.go` (C1-Step 7),
// `soundeffect/module.go` (C1-Step 8), `register/module.go`
// (C1-Step 9).
//
// UNIQUE TO DIAGNOSTICS (vs stock/voiceover/soundeffect/register):
// the Handler is the thinnest in the assets tree today — it depends
// on a single *appdiag.Service (1 dep) plus a logger. The 3 routes
// (/diagnostics + /index-health + /qdrant/cleanup) all delegate to
// the Service.Check method (with the QDRANT-005 cleanup endpoint
// returning an honest status — the background cleaner was removed in
// PG-034 and is pending restoration in QDRANT-005 Fase 3).
//
// UNIQUE TO DIAGNOSTICS (vs clips): the Descriptor surface is the
// smallest in the tree today (tied with stock / voiceover /
// soundeffect / register) — only `Module` field, no `Handler` /
// `Service` field. The diagnostics capability has no non-HTTP consumer
// in the codebase (the 3 routes are the entire public surface,
// reachable only via HTTP). The Handler stays the internal worker
// captured by the Module closure; no caller (composition root,
// tests, internal services) reads a raw *Handler from outside the
// package.
//
// The composition root constructs the *appdiag.Service from 3 typed-
// port adapters (IndexHealthAdapter + AssetStatsAdapter + ZapLogAdapter)
// in `internal/app/module_media.go::WireAssets` per AGENTS.md Pattern 0
// (the api/ layer must stay thin; the composition root owns the
// typed-port adapter chain). The Service flows through Build as a
// flat Dependencies field (canonical pattern: composition root
// builds, api layer consumes).
package assets

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	appdiag "github.com/Marcuss-ops/PipelineGen/internal/application/assets/diagnostics"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Dependencies is the typed narrow input to Build. The Handler depends
// on a *appdiag.Service (constructed at the composition root), an
// EnabledFunc, an optional list of route-module decorators, and a
// logger.
//
// Mandatory fields return an error when nil; optional fields fall
// through to the handler's existing nil-tolerance (each route
// short-circuits to 503 — never panic, never NPE). Logger nil →
// zap.NewNop() (composition-root-friendly default).
type Dependencies struct {
	// Service is the canonical *appdiag.Service façade built by
	// the composition root in
	// `internal/app/module_media.go::WireAssets` from the
	// typed-port adapter chain (IndexHealthAdapter +
	// AssetStatsAdapter + ZapLogAdapter). MANDATORY — Build
	// returns an error when nil. The Handler stores h.svc and
	// the 3 routes call svc.Check unconditionally. A nil
	// Service would NPE at first request; fail at startup
	// instead.
	Service *appdiag.Service

	// EnabledFunc is the closure that decides whether the
	// module's routes are mounted. The diagnostics capability
	// has no feature flag in production (always on) — the
	// composition root wires `func() bool { return true }` (or
	// any availability-check closure the platform team prefers).
	// MANDATORY — Build returns an error when nil (so this
	// package stays free of platform/config imports).
	EnabledFunc func() bool

	// ModuleOpts are variadic `api.RouteModuleOption` decorators
	// (typically `api.WithMiddleware(...)`) applied to the
	// RouteModule at Build time. OPTIONAL — nil produces a plain
	// RouteModule.
	ModuleOpts []api.RouteModuleOption

	// Logger is the canonical structured logger. nil →
	// zap.NewNop() (composition-root-friendly default).
	Logger *zap.Logger
}

// DiagnosticsDescriptor is the concrete capability Descriptor
// returned by Build. It satisfies api.Descriptor via the explicit
// Module field (named, not embedded — no method-promotion surprises
// from api.Module) and forwarder methods.
//
// UNIQUE TO DIAGNOSTICS: the Descriptor does NOT expose the Handler
// (matches the stock / voiceover / soundeffect / register precedent
// of dropping the explicit Handler field) NOR the Service (the
// Service is a composition-root artifact — moving it into the api/
// layer would require moving the typed-port adapter chain too,
// violating AGENTS.md Pattern 0). There is no non-HTTP consumer of
// the diagnostics Handler in the codebase — /diagnostics +
// /index-health + /qdrant/cleanup are the entire public surface,
// reachable only via HTTP. The Handler stays the internal worker
// captured by the Module closure; no caller reads a raw *Handler
// from outside the package.
type DiagnosticsDescriptor struct {
	// Module is the route-only Module (api.NewRouteModule
	// instance) the composition root threads into
	// assetsapi.Dependencies.Diagnostics.
	Module api.Module
}

// Name returns the module name ("diagnostics"). The pre-Step-10
// diagnostics routes were registered directly on the assets parent
// group (`m.deps.Diagnostics.RegisterRoutes(r)` in
// assets/module.go), so the new Module name "diagnostics" + the
// empty prefix (routes mount directly on the parent) preserve the
// public URLs /api/media/diagnostics + /api/media/index-health +
// /api/media/qdrant/cleanup (zero-change-contract).
func (d *DiagnosticsDescriptor) Name() string {
	return d.Module.Name()
}

// Enabled forwards to the Module's closure.
func (d *DiagnosticsDescriptor) Enabled() bool {
	return d.Module.Enabled()
}

// RegisterRoutes forwards to the Module — the Handler is reachable
// only via the Module's internal closure.
func (d *DiagnosticsDescriptor) RegisterRoutes(rg *gin.RouterGroup) {
	d.Module.RegisterRoutes(rg)
}

// Build composes the Diagnostics HTTP capability from the typed
// narrow dependencies. Returns a fail-closed error when any
// mandatory dep is nil. Logger nil → zap.NewNop(). ModuleOpts nil →
// no decorators.
//
// The returned Descriptor carries the Module (routes). The HTTP
// Handler is constructed here and captured by the Module's
// RegisterRoutes closure — no caller (composition root, tests,
// internal services) reads the raw Handler anywhere outside this
// function.
func Build(deps Dependencies) (api.Descriptor, error) {
	// Mandatory-shape validation.
	if deps.Service == nil {
		return nil, fmt.Errorf("diagnostics.Build: Service is required (composition root must pre-construct *appdiag.Service from the 3 typed-port adapters IndexHealthAdapter + AssetStatsAdapter + ZapLogAdapter; the api/ layer never builds it)")
	}
	if deps.EnabledFunc == nil {
		return nil, fmt.Errorf("diagnostics.Build: EnabledFunc is required (composition root must wire a closure — typically func() bool { return true } — so this package stays free of platform/config imports)")
	}

	// Logger: nil → zap.NewNop() (composition-root-friendly default).
	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}

	// Construct the canonical Handler. NewHandler has no
	// fail-closed checks (preserves the pre-Step-10 behavior for
	// direct callers that bypass Build); Build's checks above
	// are the new defensive layer.
	handler := NewHandler(
		deps.Service,
		log,
	)

	// Construct the route Module. The closure inside
	// api.NewRouteModule calls handler.RegisterRoutes(r) — the
	// Handler is captured here, not exposed to the composition
	// root via the Module surface.
	//
	// Empty prefix "" preserves the pre-Step-10 routing shape:
	// the 3 routes (GET /diagnostics + GET /index-health + POST
	// /qdrant/cleanup) mount directly on the parent /api/media
	// group (no r.Group("/diagnostics") wrap, matching the
	// pre-Step-10 assets/module.go behaviour). The Module name
	// "diagnostics" is the canonical identifier (used for
	// logging + EnabledFunc wiring).
	mod := api.NewRouteModule(
		"diagnostics",
		deps.EnabledFunc,
		"",
		handler,
		log,
		deps.ModuleOpts...,
	)

	return &DiagnosticsDescriptor{
		Module: mod,
	}, nil
}
