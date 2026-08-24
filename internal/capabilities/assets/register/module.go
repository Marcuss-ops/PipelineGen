// Package register — module.go: the single canonical Build entrypoint for
// the Register HTTP capability.
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
// This file is part of Blocco C1-Step 9 (June 2026): every capability
// in `internal/api/**` and `internal/application/**` MUST expose a
// Build(d) signature. Direct canonical-registry Calls inside a
// capability package are forbidden (godlike/07 + the canonical
// `internal/app/capability_registry.go` hoist site landed in
// Blocco C1-Step 2). The composition root consumes this Build via
// `internal/app/module_media.go::WireAssets` and threads the returned
// Descriptor into `assetsapi.Dependencies.Register` (route module that
// mounts /media/register-from-youtube + /media/register-batch).
//
// Pattern parity with `artlist/module.go` (C1-Step 3),
// `youtube/module.go` (C1-Step 4), `clips/module.go` (C1-Step 5),
// `stock/module.go` (C1-Step 6), `voiceover/module.go` (C1-Step 7),
// `soundeffect/module.go` (C1-Step 8).
//
// UNIQUE TO REGISTER (vs stock/voiceover/soundeffect): the Handler
// depends on a *sourcing.Service that is constructed at the
// composition root (in `internal/app/assets_register_sourcing.go::newAssetRegisterService`)
// from a 9-arg positional signature (cfg / log / clipsRepo /
// driveUploader / assetTreeSvc / providerRegistry / clipsHandler /
// dispatcher / publisher). The Build contract does NOT move this
// construction into the api/ layer (per AGENTS.md Pattern 0 — the api/
// layer must stay thin; the composition root owns the typed-port
// adapter chain). The Service flows through Build as a flat
// Dependencies field (canonical pattern: composition root builds, api
// layer consumes).
//
// UNIQUE TO REGISTER (vs clips): the Descriptor surface is the
// smallest in the tree today (tied with stock / voiceover /
// soundeffect) — only `Module` field, no `Handler` / `Service` field.
// The register capability has no non-HTTP consumer in the codebase
// that needs the raw Handler or Service (the YouTubeRegistrar's
// non-HTTP surface is the `sourcingEnrichmentAdapter` which calls
// `clipsHandler.EnrichAndIndexClip` — that consumer is satisfied by
// `clipsDesc.Handler`, not by the register Handler). The Handler stays
// the internal worker captured by the Module closure; no caller
// (composition root, tests, internal services) reads a raw *Handler
// from outside the package.
//
// The PR8 (June 2026) Idempotency middleware stays an optional
// Dependencies field (nil → no-op pass-through, preserves the
// test-fixture / dry-run path). The only mandatory deps are
// Service + EnabledFunc.
//
// PR-DRIVE-AVAILABILITY-GATE (2026-07-04): a NEW optional
// DriveChecker Dependencies field threads a probe closure from
// the composition root into the Handler so BatchRegisterFromYouTube
// can fail-closed at request time (HTTP 503) when folder_id is
// non-empty AND *drive.Uploader.Service is nil (the canonical
// silent-failure mode before the gate). nil → defensive
// always-fail checker (no Service==nil silent-success path).
package assets

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Dependencies is the typed narrow input to Build. The Handler depends
// on a *sourcing.Service (constructed at the composition root), a
// reusable Gin idempotency middleware instance, an EnabledFunc, an
// optional list of route-module decorators, and a logger.
//
// Mandatory fields return an error when nil; optional fields fall
// through to the handler's existing nil-tolerance (each route
// short-circuits to 503 or to the appropriate sentinel response —
// never panic, never NPE). Logger nil → zap.NewNop() (composition-
// root-friendly default). Idempotency nil → no-op pass-through.
type Dependencies struct {
	// Service is the canonical *sourcing.Service façade built by
	// the composition root in
	// `internal/app/assets_register_sourcing.go::newAssetRegisterService`
	// from the typed-port adapter chain (the YouTubeRegistrar
	// sub-service + BatchRegistrar + DriveFolderSynchronizer +
	// LocalImporter sub-services are stitched together there).
	// MANDATORY — Build returns an error when nil. The Handler
	// stores h.svc and the two routes (/register-from-youtube +
	// /register-batch) call svc.RegisterFromYouTube +
	// svc.BatchRegisterFromYouTube unconditionally. A nil
	// Service would NPE at first request; fail at startup
	// instead.
	Service *sourcing.Service

	// Idempotency is the reusable Gin idempotency middleware
	// instance from WireRegistry (PR8, June 2026). Constructed
	// once at server boot via WireRegistry →
	// BuildRepoBundle.IdempotencyStore. nil → Build installs a
	// no-op pass-through (preserves the test-fixture / dry-run
	// path). Installed on POST /register-from-youtube
	// (write route); /register-batch intentionally falls
	// through (its semantics are inherently batch-shaped — the
	// per-clip dedup is owned by sourcing.Service's
	// FindByExternalRef logic).
	Idempotency gin.HandlerFunc

	// EnabledFunc is the closure that decides whether the
	// module's routes are mounted. The register capability has
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

	// DriveChecker is the canonical probe closure that
	// BatchRegisterFromYouTube fires BEFORE any folder_id
	// routing — returns nil iff *drive.Uploader.Service is
	// wired (godlike/06 SSOT one-canonical-owner-per-fact:
	// the composition root owns the wiring; the api/ layer
	// owns the probe call). PR-DRIVE-AVAILABILITY-GATE (2026-07-04)
	// fail-closes the silent-success mode where the
	// composition root logs `driveClient not initialized`
	// while the handler attempts Drive publication anyway
	// (would 500-panic on first folder_id request). nil →
	// defensive always-fail checker (never accepts folder_id
	// traffic). The composition root wires a closure that
	// probes both runtime (deps.DriveUploader != nil) AND
	// config-level (cfg.Paths.CredentialsFile + cfg.Paths.TokenFile
	// stat-OK) state.
	DriveChecker func() error
}

// RegisterDescriptor is the concrete capability Descriptor returned
// by Build. It satisfies api.Descriptor via the explicit Module field
// (named, not embedded — no method-promotion surprises from
// api.Module) and forwarder methods.
//
// UNIQUE TO REGISTER: the Descriptor does NOT expose the Handler
// (matches the stock / voiceover / soundeffect precedent of dropping
// the explicit Handler field) NOR the Service (the Service is a
// composition-root artifact — moving it into the api/ layer would
// require moving the typed-port adapter chain too, violating
// AGENTS.md Pattern 0). There is no non-HTTP consumer of the
// register Handler in the codebase — /register-from-youtube +
// /register-batch are the entire public surface, reachable only via
// HTTP. The handler stays the internal worker captured by the
// Module closure; no caller reads a raw *Handler from outside the
// package.
type RegisterDescriptor struct {
	// Module is the route-only Module (api.NewRouteModule
	// instance) the composition root threads into
	// assetsapi.Dependencies.Register.
	Module api.Module
}

// Name returns the module name ("register"). The pre-Step-9
// register routes were registered directly on the assets parent
// group (`m.deps.Register.RegisterRoutes(r)` in
// assets/module.go), so the new Module name "register" + the empty
// prefix (routes mount directly on the parent) preserve the public
// URLs /api/media/register-from-youtube + /api/media/register-batch
// (zero-change-contract).
func (d *RegisterDescriptor) Name() string {
	return d.Module.Name()
}

// Enabled forwards to the Module's closure.
func (d *RegisterDescriptor) Enabled() bool {
	return d.Module.Enabled()
}

// RegisterRoutes forwards to the Module — the Handler is reachable
// only via the Module's internal closure.
func (d *RegisterDescriptor) RegisterRoutes(rg *gin.RouterGroup) {
	d.Module.RegisterRoutes(rg)
}

// Build composes the Register HTTP capability from the typed narrow
// dependencies. Returns a fail-closed error when any mandatory dep
// is nil. Logger nil → zap.NewNop(). Idempotency nil → no-op
// pass-through. ModuleOpts nil → no decorators.
//
// The returned Descriptor carries the Module (routes). The HTTP
// Handler is constructed here and captured by the Module's
// RegisterRoutes closure — no caller (composition root, tests,
// internal services) reads the raw Handler anywhere outside this
// function.
func Build(deps Dependencies) (api.Descriptor, error) {
	// Mandatory-shape validation.
	if deps.Service == nil {
		return nil, fmt.Errorf("register.Build: Service is required (composition root must pre-construct *sourcing.Service via newAssetRegisterService; the api/ layer never builds it)")
	}
	if deps.EnabledFunc == nil {
		return nil, fmt.Errorf("register.Build: EnabledFunc is required (composition root must wire a closure — typically func() bool { return true } — so this package stays free of platform/config imports)")
	}

	// Logger: nil → zap.NewNop() (composition-root-friendly default).
	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}

	// Construct the canonical Handler. NewHandler has no
	// fail-closed checks (preserves the pre-Step-9 behavior for
	// direct callers that bypass Build); Build's checks above
	// are the new defensive layer. The 4th-arg DriveChecker is
	// nil-tolerant inside NewHandler — passing nil here
	// preserves the pre-PR-DRIVE-AVAILABILITY-GATE behaviour
	// for direct-NewHandler callers; the composition root
	// wires a real closure via Dependencies.DriveChecker.
	handler := NewHandler(
		deps.Service,
		log,
		deps.Idempotency,  // nil-tolerant (no-op pass-through inside NewHandler)
		deps.DriveChecker, // nil-tolerant in NewHandler (returns "driveChecker not wired" error)
	)

	// Construct the route Module. The closure inside
	// api.NewRouteModule calls handler.RegisterRoutes(r) — the
	// Handler is captured here, not exposed to the composition
	// root via the Module surface.
	//
	// Empty prefix "" preserves the pre-Step-9 routing shape:
	// the two routes (POST /register-from-youtube + POST
	// /register-batch) mount directly on the parent /api/media
	// group (no r.Group("/register") wrap, matching the
	// pre-Step-9 assets/module.go behaviour). The Module name
	// "register" is the canonical identifier (used for logging
	// + EnabledFunc wiring).
	mod := api.NewRouteModule(
		"register",
		deps.EnabledFunc,
		"",
		handler,
		log,
		deps.ModuleOpts...,
	)

	return &RegisterDescriptor{
		Module: mod,
	}, nil
}
