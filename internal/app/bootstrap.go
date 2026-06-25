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
//	resolveDynamicDriveFolders + migrateLegacyScriptDocs → drive_folders_resolve.go
//	InitComposition + initCompositionMinimal* + WireServices + WireMinimal → wire_services.go
//	Build*Bundle / NewComposition → composition.go + build_*_bundle.go
//	startBackgroundJobs + lifecycle.NewServerLifecycleWithProbes → lifecycle.go
//	buildCleanup (LIFO teardown) → shutdown.go
//	InitWorkerComposition → worker_composition.go (cmd/worker entry-point)
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
	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"

	"github.com/gin-gonic/gin"
)

// AppDeps holds the minimal initialized dependencies for the server.
//
// PG-006 (June 2026): the concrete composition entry point
// (WireServices) lives in wire_services.go; this file holds only the
// return-shape type so it can stay strictly transport-shaped. Every
// concrete infra type ("Drive", "Qdrant", "JobBroker") is hidden
// behind the *ComposeRoot tree in wire_services.go — the API runtime
// only ever sees the AppDeps, Registry, module.LifecycleManager and
// the typed GetDroppedLogs() / RegisterRoutes interfaces.
//
// Fields:
//
//   - Registry: optional module registry (nil for WireMinimal-only
//     callers). InitComposition + WireServices populate this; cmd-level
//     wiring consumers read it via the AppDeps return value.
//   - WorkerHandler: internal broker handler for the /internal/v1/*
//     surface. The shape is an inline interface so api/jobs can grow
//     without forcing bootstrap.go to depend on its concrete type.
//   - Lifecycle: server lifecycle manager (startup plan + readiness
//     probes + LIFO stop). Resolves to *ServerLifecycle via the
//     composition root but typed as the api-module interface so this
//     file stays free of infra dependencies.
//   - HealthService: typed as interface{} to allow the downstream
//     router handler to perform safe type assertion. The concrete
//     type is *systemhealth.Service; see internal/api/routes.go's
//     SetHealthService for the safe-assertion pattern.
//   - ReadyChecker: concrete *systemhealth.ReadyChecker. Kept as a
//     concrete pointer so the router can call CheckReady without an
//     extra method-set assertion.
//   - Cleanup: REMOVED in PG-020 (June 2026). All teardown now goes
//     through Lifecycle.Stop. Callers that previously referenced
//     deps.Cleanup() now call deps.Lifecycle.Stop(ctx).
type AppDeps struct {
	Registry      *module.Registry
	WorkerHandler interface{ RegisterRoutes(*gin.RouterGroup) }
	// InternalMediaHandler is the QDRANT-001 server-to-server surface
	// for /internal/v1/media/* (currently just /sync-drive-folder).
	// Same shape as WorkerHandler — narrow interface, no infra imports
	// leak back into bootstrap.go. Wire-time: cmd/server/main.go calls
	// server.SetInternalMediaHandler(deps.InternalMediaHandler) so the
	// /internal/v1/media/sync-drive-folder route registers on the
	// WorkerAuth-protected group.
	InternalMediaHandler interface{ RegisterInternalMediaRoutes(*gin.RouterGroup) }
	Lifecycle            module.LifecycleManager
	HealthService        interface{}
	ReadyChecker         *systemhealth.ReadyChecker
}
