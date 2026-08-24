// Package app — bootstrap public type surface.
//
// bootstrap.go holds AppDeps + sub-bundle types only. Composition-root
// wiring lives in wire_services.go + composition.go + per-bundle files.
// bootstrap.go makes zero `internal/infrastructure/*` imports so callers
// compose via the AppDeps return value only.
package wiring

import (
	"context"

	module "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/capabilities/system/health"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"

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

// AppDeps holds the composition-root output for the server runtime.
// Surface nests into 4 purpose-grouped sub-bundles (Handlers, Runtime,
// Health, Images) so the struct stays ≤ 8 fields. Concrete infra types
// (Drive, Qdrant, JobBroker) live behind *ComposeRoot in
// wire_services.go; the API runtime only sees AppDeps.
//
// Each sub-bundle holds:
//
//   - Handlers: HTTP route registrars (5 fields)
//   - Runtime: lifecycle manager (1 field)
//   - Health: probes + readiness + health services (4 fields)
//   - Images: image-domain routing singleton (1 field)
//
// Migration: callers that previously read `deps.WorkerHandler` now read
// `deps.Handlers.WorkerHandler`; teardown goes through Lifecycle.Stop.
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

	// EmbeddingContractProbe is the boot-time embedding-contract handshake
	// (internal/app/embedding_contract_probe.go). nil when the Qdrant +
	// ClipIndexer indexing chain is not fully configured. build_server.go
	// registers it on the lifecycle readiness barrier as
	// "embedding-contract".
	EmbeddingContractProbe HealthProber
}

// AppImage groups the image-domain routing surface (FASE 7, July 2026).
// Exposes the canonical ImageSearchResolver — held on Server.imageSearchResolver
// via ServerDeps.ImageSearchResolver for future /api/images/search handler
// consumption. Kept as a typed sub-bundle (not a single AppDeps field) so
// future image-domain ports land in the same purpose-grouped slot without
// bumping AppDeps past the 8-field cap.
type AppImage struct {
	ImageSearchResolver images.ImageSearchResolver
}
