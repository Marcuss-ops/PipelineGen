package httpserver

import (
	"github.com/gin-gonic/gin"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver/middleware"
)

// registerInternalRoutes wires the WorkerAuth-protected /internal/v1
// group: worker/jobs routes, the QDRANT-001 internal media sync,
// the QDRANT-002 outbox monitoring and the QDRANT-004 media search.
// All are server-to-server surfaces and MUST
// stay on this group — anti-regression test
// TestRoutes_NoApiInternalV1Prefix forbids any of them leaking under /api.
func (r *Router) registerInternalRoutes(engine *gin.Engine) {
	// QDRANT-002 + QDRANT-004 (June 2026): the internal-worker-broker
	// prefix is "/internal/v1" — historically `remoteshared.InternalPathPrefix`.
	// The Wave 14 PR5 cleanup hardcodes it here so internal/api stops
	// importing internal/platform/remote/shared (a transport concern,
	// not a capability concern). Anti-regression test
	// internal/api/routes_test.go::TestRoutes_NoApiInternalV1Prefix enforces
	// no /api/internal/v1/* route should ever leak.
	internalGroup := engine.Group("/internal/v1")
	internalGroup.Use(middleware.WorkerAuth(r.cfg.Auth, r.cfg.Log))
	{
		if r.workerHandler != nil {
			r.workerHandler.RegisterRoutes(internalGroup)
		}
		// QDRANT-001 /internal/v1/media/* surface — server-to-server.
		// WorkerAuth above enforces Bearer token (rejects admin tokens —
		// see middleware_worker_auth_test.go). nil-tolerant if not wired.
		if r.internalMediaHandler != nil {
			r.internalMediaHandler.RegisterInternalMediaRoutes(internalGroup)
		}
		// QDRANT-002 /internal/v1/outbox/* surface — server-to-server
		// outbox monitoring (GET /status, GET /events). Mounted on the
		// SAME WorkerAuth internalGroup as worker routes; anti-regression
		// test TestRoutes_NoApiInternalV1Prefix forbids ever moving this
		// under /api.
		if r.outboxHandler != nil {
			outboxGroup := internalGroup.Group("/outbox")
			r.outboxHandler.RegisterRoutes(outboxGroup)
		}
		// QDRANT-004 /internal/v1/media/search — server-to-server
		// semantic search. Mounted on the SAME WorkerAuth internalGroup.
		if r.mediasearchHandler != nil {
			mediaSearchGroup := internalGroup.Group("/media")
			r.mediasearchHandler.RegisterRoutes(mediaSearchGroup)
		}
	}
}
