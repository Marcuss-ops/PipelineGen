// Package app — bootstrap public type surface (PG-006, June 2026).
//
// PG-006 (June 2026): bootstrap.go now contains ONLY the AppDeps
// public type plus this package doc. All composition-root logic moved
// out to per-bundle / per-capability files so this file can stay
// strictly free of `internal/infrastructure/*` imports:
//
//   - AppDeps struct + module.LifecycleManager field — public surface
//     for the runtime entry-point. Caller types include:
//   - cmd/server/main.go (the canonical HTTP server bootstrap)
//   - cmd/worker/main.go (the remote worker bundle instantiation)
//   - cmd/admin/* tools (one-shot admin CLI entry-points)
//
// Concrete wiring — file map after PG-006:
//
//	initDatabases + runAllMigrations → databases_helpers.go
//	resolveDynamicFoldersPaths (Drive folder validation, pre-creation, storage dirs)
//	    → build_drive_startup.go (PR9-A deferred-start closure from BuildDriveBundle)
//	InitComposition + initCompositionMinimal* + WireServices + WireMinimal → wire_services.go
//	Build*Bundle / NewComposition → composition.go + build_*_bundle.go
//	startBackgroundJobs + lifecycle.NewServerLifecycleWithProbes → lifecycle.go
//	buildJobRunner + buildJobRunnerStep (PR4.8 job-runner typed lifecycle) → lifecycle_job_runner.go
//	buildCleanup (LIFO teardown) → shutdown.go
//	InitWorkerComposition → worker_composition.go (cmd/worker entry-point)
//
// Wave A Item 1 (June 2026): the legacy `resolveDynamicDriveFolders` +
// `migrateLegacyScriptDocs` helpers (drive_folders_resolve.go) were
// dead code — only doc-comment references in cmd/admin/* + bootstrap,
// no production callers — and have been removed.
//
// PR4d-final (June 2026): the legacy *CoreDeps projection was removed.
// `type services struct` (in dependencies.go) was removed in the same
// wave — it duplicated logic that now lives in composition.go's
// Build*Bundle()s.
//
// Rule (AGENTS.md §13 + PG-006 stricture): every file in the package
// that imports anything from `internal/infrastructure/*` must justify
// the import in its package doc header. bootstrap.go's contract is that
// it makes zero such imports — callers compose via the AppDeps return
// value only.
package app

import (
	"context"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/routing"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"

	"github.com/gin-gonic/gin"
)

// ── Shared structural interfaces for composition-root handler fields ──────
//
// These interfaces deduplicate the identical inline interface literals
// that were repeated in both AppDeps (bootstrap.go) and RegistryWiring
// (registry.go). The concrete handlers satisfy these structurally via
// Go's implicit-interface rules — no adapter wrappers needed.

// RouteRegistrar is the standard interface for HTTP handlers that mount
// routes on a gin.RouterGroup. Used by WorkerHandler, OutboxHandler,
// and MediasearchHandler across AppDeps and RegistryWiring.
type RouteRegistrar interface {
	RegisterRoutes(*gin.RouterGroup)
}

// InternalMediaRegistrar is the interface for the /internal/v1/media/*
// server-to-server surface (currently /sync).
type InternalMediaRegistrar interface {
	RegisterInternalMediaRoutes(*gin.RouterGroup)
}

// HealthProber is the interface for liveness/readiness probes.
type HealthProber interface {
	Probe(context.Context) error
}

// AppDeps holds the minimal initialized dependencies for the server.
//
// PG-006 (June 2026) + PR-NEST-FLAT-DEPS (July 2026): the concrete
// composition entry point (WireServices) lives in wire_services.go;
// this file holds only the return-shape type so it can stay strictly
// transport-shaped. Every concrete infra type ("Drive", "Qdrant",
// "JobBroker") is hidden behind the *ComposeRoot tree in
// wire_services.go — the API runtime only ever sees the AppDeps
// (Handlers + Runtime + Health sub-bundles), Registry,
// module.LifecycleManager and the typed GetDroppedLogs() /
// RouteRegistrar interfaces.
//
// PR-NEST-FLAT-DEPS (July 2026): the previous flat-list shape had
// 10 mandatory fields, tripping the `max_struct_deps=8` archcheck
// gate (struct_deps warn-violation). The struct now nests the
// surface into 3 purpose-grouped sub-bundles: AppHandlers
// (HTTP route registrars — 5 fields, max 5), AppRuntime (lifecycle
// manager — 1 field), AppHealth (probes + health services — 4
// fields). AppDeps itself carries 3 sub-bundles → 3 fields, well
// below the 8-field cap. The nesting is the canonical pattern
// established by PR-NEST-FLAT-DEPS-ARLIST (build_bundles_artlist.go:
// ServicePorts + ServiceDependencies{Infra, Ports, Domain, Repos,
// Finalizer}).
//
// Field groups (cross-references wire_services.go::WireServices):
//
//   - Handlers.Registry: optional module registry (nil for
//     WireMinimal-only callers). InitComposition + WireServices
//     populate this; cmd-level wiring consumers read it via the
//     AppDeps return value.
//   - Handlers.WorkerHandler: internal broker handler for the
//     /internal/v1/* surface.
//   - Handlers.InternalMediaHandler: QDRANT-001 server-to-server
//     surface for /internal/v1/media/* (currently just /sync).
//   - Handlers.OutboxHandler: QDRANT-002 surface for
//     /internal/v1/outbox/status and /events.
//   - Handlers.MediasearchHandler: QDRANT-004 surface for
//     /internal/v1/media/search.
//   - Runtime.Lifecycle: server lifecycle manager (startup plan +
//     readiness probes + LIFO stop).
//   - Health.QdrantProbe: QDRANT-005 health probe used by /ready.
//   - Health.QdrantHealth: HIGH #7 handler for /qdrant/live and
//     /qdrant/ready.
//   - Health.HealthService: *systemhealth.Service.
//   - Images.ImageSearchResolver: FASE 7 routing singleton reached from
//     app.DomainBundle.ImageSearchResolver. nil-typed-port safe.
//
// Migration note: callers that previously read field names like
// `deps.WorkerHandler` now read `deps.Handlers.WorkerHandler`,
// `deps.Lifecycle` becomes `deps.Runtime.Lifecycle`, etc. cmd/server/main.go
// and cmd/worker/main.go are updated in the same commit. The Cleanup
// field is REMOVED in PG-020 (June 2026) — all teardown goes through
// Lifecycle.Stop.
type AppDeps struct {
	Handlers AppHandlers
	Runtime  AppRuntime
	Health   AppHealth
	Images   AppImage
}

// AppHandlers groups the HTTP route registrars exposed by the
// server composition root. Each field is a narrow interface
// (RouteRegistrar / InternalMediaRegistrar) so api/* packages can
// grow without the bootstrap surface having to depend on concrete
// api/jobs / internal/types.
//
// godlike/06 SSOT: this is the single canonical sub-bundle for the
// handlers group. New HTTP handler fields belong HERE (not as
// top-level AppDeps fields) so AppDeps stays ≤8-field compliant.
type AppHandlers struct {
	Registry             *module.Registry
	WorkerHandler        RouteRegistrar
	InternalMediaHandler InternalMediaRegistrar
	OutboxHandler        RouteRegistrar
	MediasearchHandler   RouteRegistrar
}

// AppRuntime groups the lifecycle / server-control surface. Kept as
// a typed sub-bundle (not a single field-on-AppDeps) so future
// runtime primitives (startup hooks, shutdown barriers, runtime
// metrics) can land in the same purpose-grouped slot without
// bumping AppDeps past the 8-field cap.
type AppRuntime struct {
	Lifecycle module.LifecycleManager
}

// AppHealth groups the readiness + liveness probe surface. Mirrors
// the AppHandlers wiring-shape convention so call sites stay
// consistent (`deps.Health.X`, `deps.Handlers.X`).
type AppHealth struct {
	QdrantProbe   HealthProber
	QdrantHealth  any
	HealthService any
	ReadyChecker  *systemhealth.ReadyChecker
}

// AppImage groups the image-domain routing surface (FASE 7, July 2026).
// Exposes the canonical ImageSearchResolver — held on Server.imageSearchResolver
// via ServerDeps.ImageSearchResolver for future /api/images/search handler
// consumption. Kept as a typed sub-bundle (not a single AppDeps field) so
// future image-domain ports land in the same purpose-grouped slot without
// bumping AppDeps past the 8-field cap.
type AppImage struct {
	ImageSearchResolver routing.ImageSearchResolver
}
