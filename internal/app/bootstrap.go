// Package app — bootstrap public type surface (PG-006, June 2026).
package app

import (
	"context"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"

	"github.com/gin-gonic/gin"
)

// RouteRegistrar is the standard interface for HTTP handlers that mount
// routes on a gin.RouterGroup.
type RouteRegistrar interface {
	RegisterRoutes(*gin.RouterGroup)
}

// InternalMediaRegistrar is the interface for the /internal/v1/media/*
// server-to-server surface.
type InternalMediaRegistrar interface {
	RegisterInternalMediaRoutes(*gin.RouterGroup)
}

// HealthProber is the interface for liveness/readiness probes.
type HealthProber interface {
	Probe(context.Context) error
}

// AppTransportDeps owns the HTTP registry and internal route registrars.
type AppTransportDeps struct {
	Registry             *module.Registry
	WorkerHandler        RouteRegistrar
	InternalMediaHandler InternalMediaRegistrar
	OutboxHandler        RouteRegistrar
	MediasearchHandler   RouteRegistrar
}

// AppRuntimeDeps owns lifecycle and health/readiness surfaces.
type AppRuntimeDeps struct {
	QdrantProbe  HealthProber
	QdrantHealth any
	Lifecycle    module.LifecycleManager
	HealthService any
	ReadyChecker *systemhealth.ReadyChecker
}

// AppDeps holds the minimal initialized dependencies for the server. This
// transitional direct-field shape remains until both construction sites are
// migrated through newAppDeps; the follow-up commit embeds the two capability
// groups without changing consumer selectors.
type AppDeps struct {
	Registry             *module.Registry
	WorkerHandler        RouteRegistrar
	InternalMediaHandler InternalMediaRegistrar
	OutboxHandler        RouteRegistrar
	MediasearchHandler   RouteRegistrar
	QdrantProbe          HealthProber
	QdrantHealth         any
	Lifecycle            module.LifecycleManager
	HealthService        any
	ReadyChecker         *systemhealth.ReadyChecker
}

// newAppDeps is the single assembler for the public server dependency shape.
func newAppDeps(transport AppTransportDeps, runtime AppRuntimeDeps) *AppDeps {
	return &AppDeps{
		Registry:             transport.Registry,
		WorkerHandler:        transport.WorkerHandler,
		InternalMediaHandler: transport.InternalMediaHandler,
		OutboxHandler:        transport.OutboxHandler,
		MediasearchHandler:   transport.MediasearchHandler,
		QdrantProbe:          runtime.QdrantProbe,
		QdrantHealth:         runtime.QdrantHealth,
		Lifecycle:            runtime.Lifecycle,
		HealthService:        runtime.HealthService,
		ReadyChecker:         runtime.ReadyChecker,
	}
}
