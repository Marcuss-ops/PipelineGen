// Package storage — module.go: the single canonical Build entrypoint for
// the Storage HTTP capability (Drive folder sync operations + the
// QDRANT-001 server-to-server /internal/v1/media/* surface).
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
// This file is part of Blocco C1-Step 12 (June 2026; user calls this
// "Step 11" because the parity numbering with the 8 sibling conversions
// started at register/Step-9). Every capability in `internal/capabilities/**` and
// `internal/capabilities/**` MUST expose a Build(d) signature. Direct
// canonical-registry Calls inside a capability package are forbidden
// (godlike/07 + the canonical `internal/app/capability_registry.go`
// hoist site landed in Blocco C1-Step 2). The composition root consumes
// this Build via `internal/app/module_media.go::WireAssets` and threads
// the returned Descriptor into `assetsapi.Dependencies.Storage` (route
// module that mounts /media/sync) AND threads `storageDesc.Handler` as
// the Router.SetInternalMediaHandler binding for the QDRANT-001
// server-to-server surface at /internal/v1/media/sync.
//
// Pattern parity with `artlist/module.go` (C1-Step 3),
// `youtube/module.go` (C1-Step 4), `clips/module.go` (C1-Step 5),
// `stock/module.go` (C1-Step 6), `voiceover/module.go` (C1-Step 7),
// `soundeffect/module.go` (C1-Step 8), `register/module.go` (C1-Step 9).
//
// UNIQUE TO STORAGE (vs the 8 siblings landed at HEAD): the Descriptor
// surface EXPOSES the raw `*storage.Handler` field — this matches the
// `clips` precedent exactly (NOT the register / soundeffect / stock /
// voiceover precedent which drops the Handler field). The reason is
// the QDRANT-001 closure (June 2026) wired a narrow server-to-server
// surface /internal/v1/media/sync that is invoked by
// `internal/platform/httpserver/routes.go::Setup()` via the `api.MediaInternalRouter`
// interface:
//
//	type MediaInternalRouter interface {
//		RegisterInternalMediaRoutes(*gin.RouterGroup)
//	}
//
// The production binding for that interface is `*storage.Handler` —
// the only type that implements `RegisterInternalMediaRoutes`. After
// the Step-12 conversion the Router still needs a *storage.Handler
// (not an api.Descriptor) because `RegisterInternalMediaRoutes` is a
// Handler-level method, not a Module-level method. The Descriptor
// therefore keeps a `Handler *storage.Handler` field for the
// composition root to read and forward into Router.SetInternalMediaHandler
// (preserving the pre-Step-12 wire_services.go contract verb atim —
// the only change is the source of the binding, which moves from
// `wire_assets.NewHandler(...)` to `storage.Build(...).Handler`).
//
// UNIQUE TO STORAGE (vs register): the Handler depends on a concrete
// `*catalogsync.Service` from `internal/capabilities/assets/catalogsync`.
// The Build contract does NOT introduce a typed-port interface for this
// dep (per AGENTS.md Pattern 0 only infra-layer dependencies need
// typed-port adapters; application-layer services can pass through
// concrete references — matches the `register` precedent which threads
// `*sourcing.Service` as a flat Dependencies field too).
//
// UNIQUE TO STORAGE (vs register/soundeffect): the Handler exports TWO
// registration surfaces (RegisterRoutes for admin +
// RegisterInternalMediaRoutes for server-to-server) instead of one.
// Both are Handler-level methods captured by the Module closure for
// admin + FORWARDED via Descriptor.Handler for the server-to-server
// surface. There is no caller (composition root, tests, internal
// services) that reads `*Handler` from outside the storage package
// EXCEPT via the Descriptor.Handler field.
package storage

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/catalogsync"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	api "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Dependencies is the typed narrow input to Build. The Handler depends
// on a `job.Service` (canonical domain interface satisfied by
// *appjobs.Service in production) + a *catalogsync.Service
// (constructed at the composition root via BuildCoreBundle / the
// catalogsync wiring under deps.Core.CatalogSyncService) + an
// EnabledFunc + an optional list of route-module decorators + a
// logger.
//
// Mandatory fields return an error when nil; optional fields fall
// through to the handler's existing nil-tolerance. Logger nil →
// zap.NewNop() (composition-root-friendly default).
type Dependencies struct {
	// Jobs is the canonical `job.Service` interface used by the
	// synced-folder handlers (`SyncDriveFolder` +
	// `InternalSyncDriveFolder`). MANDATORY — Build returns
	// an error when nil. Both handlers call `h.jobsSvc`
	// unconditionally via `transport.EnqueueAsync`; a nil
	// Jobs would NPE at first request, fail at startup
	// instead.
	Jobs job.Service

	// CatalogSync is the canonical *catalogsync.Service façade
	// built by the composition root (constructed via
	// `appcatalogsync.NewService(...)` and threaded through
	// `deps.Core.CatalogSyncService` in BuildCoreBundle). The
	// storage handlers (SyncDriveFolder + InternalSyncDriveFolder)
	// read it to short-circuit composition errors (returning
	// 500 "catalog sync service not configured" when nil
	// instead of a confusing NPE). MANDATORY at Build time —
	// Build is stricter than the handler-level runtime 500
	// contract: a missing CatalogSync surfaces as a startup
	// fail-closed error (parity with soundeffect/Step-8's
	// missing-Dispatcher invariant).
	CatalogSync *catalogsync.Service

	// EnabledFunc is the closure that decides whether the
	// module's routes are mounted. The storage capability
	// has no feature flag in production (always on) — the
	// composition root wires `func() bool { return true }`
	// (or any availability-check closure the platform team
	// prefers). MANDATORY — Build returns an error when nil
	// (so this package stays free of platform/config imports).
	EnabledFunc func() bool

	// ModuleOpts are variadic `api.RouteModuleOption` decorators
	// (typically `api.WithMiddleware(...)`) applied to the
	// RouteModule at Build time. OPTIONAL — nil produces a
	// plain RouteModule.
	ModuleOpts []api.RouteModuleOption

	// Logger is the canonical structured logger. nil →
	// zap.NewNop() (composition-root-friendly default).
	Logger *zap.Logger
}

// StorageDescriptor is the concrete capability Descriptor returned
// by Build. It satisfies api.Descriptor via the explicit Module field
// (named, not embedded — no method-promotion surprises from
// api.Module) and forwarder methods.
//
// UNIQUE TO STORAGE: the Descriptor DOES expose the Handler (matches
// the `clips` precedent exactly; the register / soundeffect / stock /
// voiceover capabilities drop the Handler field because they have no
// non-HTTP consumer). The Handler read here satisfies the
// `api.MediaInternalRouter` interface (which exposes
// `RegisterInternalMediaRoutes(*gin.RouterGroup)`) used by
// `internal/platform/httpserver/routes.go::Setup()` for the QDRANT-001 server-to-
// server /internal/v1/media/sync surface. Without an exposed Handler
// the Router would have nothing to call — the Module surface
// (Name/Enabled/RegisterRoutes) does NOT include the API Router cares
// about. See godoc at top of this file for the precise rationale.
type StorageDescriptor struct {
	// Module is the route-only Module (api.NewRouteModule
	// instance) the composition root threads into
	// assetsapi.Dependencies.Storage (mounts POST /api/media/sync
	// on the parent /api/media group via Handler.RegisterRoutes).
	Module api.Module

	// Handler is the raw *storage.Handler that satisfies
	// `api.MediaInternalRouter` via its
	// `RegisterInternalMediaRoutes(*gin.RouterGroup)` method. The
	// composition root threads this into
	// `Router.SetInternalMediaHandler(storageDesc.Handler)` so the
	// /internal/v1/media/sync server-to-server surface mounts
	// cleanly. Mirrors the clips precedent (`*ClipsDescriptor.Handler`
	// field exposes the enrichment-adapter consumer target).
	//
	// Production binding source (closes the audit loop): the
	// composition root reads `registryWiring.Assets.InternalMediaHandler`
	// from `internal/app/wire_services.go` (which forwards
	// `AssetsWiring.InternalMediaHandler` set by `WireAssets` in
	// `internal/app/module_media.go`) and passes it to
	// `api.NewServerWithHealth(...)` →
	// `router.SetInternalMediaHandler(...)` →
	// `h.RegisterInternalMediaRoutes(internalGroup)` at server
	// construction time.
	Handler *Handler
}

// Name returns the module name ("storage"). The pre-Step-12 storage
// routes were registered directly on the assets parent group
// (`m.deps.Storage.RegisterRoutes(r)` in assets/module.go), so the
// new Module name "storage" + the empty prefix (routes mount directly
// on the parent) preserve the public URL /api/media/sync
// (zero-change-contract).
func (d *StorageDescriptor) Name() string {
	return d.Module.Name()
}

// Enabled forwards to the Module's closure.
func (d *StorageDescriptor) Enabled() bool {
	return d.Module.Enabled()
}

// RegisterRoutes forwards to the Module — the Handler is reachable
// only via the Module's internal RegisterRoutes closure AND via the
// Descriptor.Handler field (for the QDRANT-001 server-to-server
// consumer — see StorageDescriptor godoc).
func (d *StorageDescriptor) RegisterRoutes(rg *gin.RouterGroup) {
	d.Module.RegisterRoutes(rg)
}

// Build composes the Storage HTTP capability from the typed narrow
// dependencies. Returns a fail-closed error when any mandatory dep
// is nil. Logger nil → zap.NewNop(). ModuleOpts nil → no decorators.
//
// The returned Descriptor carries BOTH the Module (for /api/media/sync
// admin surface) AND the Handler (for /internal/v1/media/sync server-
// to-server surface). The HTTP Handler is constructed here and
// captured by the Module's RegisterRoutes closure — the Handler field
// is exposed solely for the QDRANT-001 consumer in Router.SetInternalMediaHandler.
//
// Composition-bug guard: Build is stricter than the handler-level
// runtime 500 contract for missing deps. The CatalogSync nil-check
// below mirrors soundeffect/Step-8's Dispatcher fail-closed invariant:
// if the canonical service is not wired the operator sees the wiring
// defect at startup, not as a confused 500 at first request.
func Build(deps Dependencies) (api.Descriptor, error) {
	// Mandatory-shape validation.
	if deps.Jobs == nil {
		return nil, fmt.Errorf("storage.Build: Jobs is required (composition root must thread jobs.Facade through deps; both SyncDriveFolder + InternalSyncDriveFolder EnqueueAsync unconditionally)")
	}
	if deps.CatalogSync == nil {
		return nil, fmt.Errorf("storage.Build: CatalogSync is required (composition root must wire *catalogsync.Service; Build is stricter than the handler's runtime 500 contract — the operator sees the missing service at startup, not as a confused 500 at first request)")
	}
	if deps.EnabledFunc == nil {
		return nil, fmt.Errorf("storage.Build: EnabledFunc is required (composition root must wire a closure — typically func() bool { return true } — so this package stays free of platform/config imports)")
	}

	// Logger: nil → zap.NewNop() (composition-root-friendly default).
	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}

	// Construct the canonical Handler. NewHandler has no fail-
	// closed checks (preserves the pre-Step-12 behavior for direct
	// callers that bypass Build, e.g. the handler_test.go fixture);
	// Build's checks above are the new defensive layer.
	handler := NewHandler(
		deps.Jobs,
		deps.CatalogSync,
		log,
	)

	// Construct the route Module. The closure inside
	// api.NewRouteModule calls handler.RegisterRoutes(r) — the
	// Handler is captured here AND also exposed via the Descriptor
	// .Handler field below for the QDRANT-001 /internal/v1/media/sync
	// server-to-server consumer.
	//
	// Empty prefix "" preserves the pre-Step-12 routing shape:
	// the single admin route (POST /sync) mounts directly on the
	// parent /api/media group (no r.Group("/storage") wrap, matching
	// the pre-Step-12 assets/module.go behaviour). The Module name
	// "storage" is the canonical identifier (used for logging +
	// EnabledFunc wiring).
	mod := api.NewRouteModule(
		"storage",
		deps.EnabledFunc,
		"",
		handler,
		log,
		deps.ModuleOpts...,
	)

	return &StorageDescriptor{
		Module:  mod,
		Handler: handler,
	}, nil
}
