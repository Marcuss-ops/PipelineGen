package httpserver

import (
	"github.com/gin-gonic/gin"

	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/capabilities/system/health"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver/transport"
)

// registerHealthRoutes wires the unified health surface: /health with
// ?deep=true aggregation, /ready, /models (E5 + SigLIP probes) and the
// /qdrant/{live,ready} endpoints. Returns the *transport.HealthHandler so
// Setup() can attach the WireRegistry once every route is mounted.
//
// PR1 + codex/health-ready-contract (June 2026): the health service and
// ReadyChecker are wired via SetHealthService / SetReadyChecker before
// Setup() runs — /ready no longer receives nil in production.
func (r *Router) registerHealthRoutes(engine *gin.Engine, log *zap.Logger) *transport.HealthHandler {
	// Unified health check (PR1, June 2026): single /health with ?deep=true
	// for aggregated DB+Drive+Qdrant+JobBroker checks. The health service
	// lives in ComposeRoot.Utility.HealthService and is wired via
	// SetHealthService before Setup() runs.
	// codex/health-ready-contract (June 2026): ReadyChecker is now wired
	// via SetReadyChecker — /ready no longer receives nil in production.
	var healthHandler *transport.HealthHandler
	if r.healthSvc != nil {
		if svc, svcOk := r.healthSvc.(*systemhealth.Service); svcOk {
			var rc *systemhealth.ReadyChecker
			if r.readyChecker != nil {
				rc, _ = r.readyChecker.(*systemhealth.ReadyChecker)
			}
			healthHandler = transport.NewHealthHandler(svc, rc)
		}
	}
	if healthHandler == nil {
		log.Warn("health service not wired, health endpoints will return 503")
		healthHandler = transport.NewHealthHandler(nil, nil /* nil-by-design; integration stub only */)
	}
	engine.GET("/health", healthHandler.Health)
	engine.GET("/ready", healthHandler.Ready)

	// /models — E5 + SigLIP model health probes (Task 10, July 2026).

	// /models — E5 + SigLIP model health probes (Task 10, July 2026).
	// nil-safe: returns 503 when the handler is not wired.
	modelsHandler := r.modelsHandler
	if modelsHandler == nil {
		log.Warn("models handler not wired, /models will return 503")
		modelsHandler = transport.NewModelsHandler("") // empty URL -> 503 responses
	}
	engine.GET("/models", modelsHandler.Models)

	// Qdrant health endpoints — /qdrant/live (liveness) and
	// /qdrant/ready (deep readiness with alias + collection + schema
	// + semantic canary). HIGH #7, July 2026.
	if r.qdrantHealth != nil {
		if qh, ok := r.qdrantHealth.(interface {
			Live(*gin.Context)
			Ready(*gin.Context)
		}); ok {
			engine.GET("/qdrant/live", qh.Live)
			engine.GET("/qdrant/ready", qh.Ready)
		} else {
			log.Warn("qdrantHealth handler does not satisfy Live/Ready interface, routes not registered")
		}
	}

	return healthHandler
}
