// Package search — module.go: the single canonical Build entrypoint for
// the Search HTTP capability.
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
// This file is part of Blocco C1-Step 11 (June 2026): every capability
// in `internal/capabilities/**` and `internal/capabilities/**` MUST expose a
// Build(d) signature. Direct canonical-registry Calls inside a
// capability package are forbidden (godlike/07 + the canonical
// `internal/app/capability_registry.go` hoist site landed in
// Blocco C1-Step 2). The composition root consumes this Build via
// `internal/app/module_media.go::WireAssets` and threads the returned
// Descriptor into `assetsapi.Dependencies.Search` (route module that
// mounts /search on the parent /api/media group).
//
// Pattern parity with `artlist/module.go` (C1-Step 3),
// `youtube/module.go` (C1-Step 4), `clips/module.go` (C1-Step 5),
// `stock/module.go` (C1-Step 6), `voiceover/module.go` (C1-Step 7),
// `soundeffect/module.go` (C1-Step 8), `register/module.go`
// (C1-Step 9), `diagnostics/module.go` (C1-Step 10).
//
// UNIQUE TO SEARCH: the Handler is the THINNEST in the assets tree
// today (1 dep + log, 1 route: POST /search). The Handler is already
// Aggregator-backed per the Blocco A2 consolidation (June 2026): all
// independent search endpoints (Artlist /search + /search/live,
// YouTube /search, clips /search/advanced, scraper /search,
// /api/assets/search) have been absorbed into the single
// POST /api/media/search endpoint that delegates to the canonical
// Aggregator. The Build contract simply wraps the existing
// thin transport with the canonical Build/Descriptor surface.
//
// UNIQUE TO SEARCH (vs clips): the Descriptor surface is the smallest
// in the tree today (tied with stock / voiceover / soundeffect /
// register / diagnostics) — only `Module` field, no `Handler` /
// `Service` field. The search capability has no non-HTTP consumer in
// the codebase that needs the raw Handler (the cross-provider search
// surface reaches the canonical Aggregator directly, not via
// the api/search Handler). The Handler stays the internal worker
// captured by the Module closure; no caller (composition root,
// tests, internal services) reads a raw *Handler from outside the
// package.
//
// The *Aggregator is constructed at the composition root in
// `internal/app/module_media.go::WireAssets` from the SearchBackends
// + ZapLogAdapter. The Build contract does NOT move this construction
// into the api/ layer (per AGENTS.md Pattern 0 — the api/ layer must
// stay thin; the composition root owns the typed-port adapter chain).
// The Aggregator flows through Build as a flat Dependencies field
// (canonical pattern: composition root builds, api layer consumes).
package search

import (
	"fmt"

	assetresolver "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/resolver"
	api "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Dependencies is the typed narrow input to Build. The Handler depends
// on a *Aggregator (constructed at the composition root), an
// EnabledFunc, an optional list of route-module decorators, and a
// logger.
//
// Mandatory fields return an error when nil; optional fields fall
// through to the handler's existing nil-tolerance (the single route
// short-circuits to 503 when the Aggregator is nil — never panic,
// never NPE). Logger nil → zap.NewNop() (composition-root-friendly
// default).
type Dependencies struct {
	// Aggregator is the canonical *Aggregator built by
	// the composition root in
	// `internal/app/module_media.go::WireAssets` from the
	// pre-built SearchBackends + ZapLogAdapter. MANDATORY —
	// Build returns an error when nil. The Handler stores
	// h.aggreg and the single route /search calls
	// aggreg.Search unconditionally. A nil Aggregator would
	// NPE at first request; fail at startup instead.
	Aggregator *Aggregator
	Resolver   *assetresolver.Service

	// EnabledFunc is the closure that decides whether the
	// module's routes are mounted. The search capability has
	// no feature flag in production (always on) — the
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

// SearchDescriptor is the concrete capability Descriptor returned
// by Build. It satisfies api.Descriptor via the explicit Module
// field (named, not embedded — no method-promotion surprises from
// api.Module) and forwarder methods.
//
// UNIQUE TO SEARCH: the Descriptor does NOT expose the Handler
// (matches the stock / voiceover / soundeffect / register /
// diagnostics precedent of dropping the explicit Handler field) NOR
// the Aggregator (the Aggregator is a composition-root artifact —
// moving it into the api/ layer would require moving the
// SearchBackends + ZapLogAdapter chain too, violating AGENTS.md
// Pattern 0). There is no non-HTTP consumer of the search Handler
// in the codebase — POST /search is the entire public surface,
// reachable only via HTTP. The Handler stays the internal worker
// captured by the Module closure; no caller reads a raw *Handler
// from outside the package.
type SearchDescriptor struct {
	// Module is the route-only Module (api.NewRouteModule
	// instance) the composition root threads into
	// assetsapi.Dependencies.Search.
	Module api.Module
}

// Name returns the module name ("search"). The pre-Step-11 search
// route was registered directly on the assets parent group
// (`m.deps.Search.RegisterRoutes(r)` in assets/module.go), so the
// new Module name "search" + the empty prefix (route mounts directly
// on the parent) preserve the public URL /api/media/search
// (zero-change-contract).
func (d *SearchDescriptor) Name() string {
	return d.Module.Name()
}

// Enabled forwards to the Module's closure.
func (d *SearchDescriptor) Enabled() bool {
	return d.Module.Enabled()
}

// RegisterRoutes forwards to the Module — the Handler is reachable
// only via the Module's internal closure.
func (d *SearchDescriptor) RegisterRoutes(rg *gin.RouterGroup) {
	d.Module.RegisterRoutes(rg)
}

// Build composes the Search HTTP capability from the typed narrow
// dependencies. Returns a fail-closed error when any mandatory dep
// is nil. Logger nil → zap.NewNop(). ModuleOpts nil → no decorators.
//
// The returned Descriptor carries the Module (routes). The HTTP
// Handler is constructed here and captured by the Module's
// RegisterRoutes closure — no caller (composition root, tests,
// internal services) reads the raw Handler anywhere outside this
// function.
func Build(deps Dependencies) (api.Descriptor, error) {
	// Mandatory-shape validation.
	if deps.Aggregator == nil {
		return nil, fmt.Errorf("Build: Aggregator is required (composition root must pre-construct *Aggregator from the SearchBackends + ZapLogAdapter; the api/ layer never builds it)")
	}
	if deps.EnabledFunc == nil {
		return nil, fmt.Errorf("Build: EnabledFunc is required (composition root must wire a closure — typically func() bool { return true } — so this package stays free of platform/config imports)")
	}

	// Logger: nil → zap.NewNop() (composition-root-friendly default).
	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}

	// Construct the canonical Handler. NewHandler has no
	// fail-closed checks (preserves the pre-Step-11 behavior for
	// direct callers that bypass Build); Build's checks above
	// are the new defensive layer.
	handler := NewHandler(
		deps.Aggregator,
		deps.Resolver,
		log,
	)

	// Construct the route Module. The closure inside
	// api.NewRouteModule calls handler.RegisterRoutes(r) — the
	// Handler is captured here, not exposed to the composition
	// root via the Module surface.
	//
	// Empty prefix "" preserves the pre-Step-11 routing shape:
	// the single route (POST /search) mounts directly on the
	// parent /api/media group (no r.Group("/search") wrap,
	// matching the pre-Step-11 assets/module.go behaviour).
	// The Module name "search" is the canonical identifier
	// (used for logging + EnabledFunc wiring).
	mod := api.NewRouteModule(
		"search",
		deps.EnabledFunc,
		"",
		handler,
		log,
		deps.ModuleOpts...,
	)

	return &SearchDescriptor{
		Module: mod,
	}, nil
}
