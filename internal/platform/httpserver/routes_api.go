package httpserver

import (
	"github.com/gin-gonic/gin"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver/middleware"
) // registerAPIRoutes wires the public /api surface: the protected subtree
// (Auth + RateLimit + WorkspaceScope) that hosts the module registry for
// the media, jobs and other modules. Returns the /api group so Setup()
// can mount /api/capabilities AFTER the WireRegistry is built (the wire
// surface must reflect the full routed surface).
//
// PG-M2M (Aug 2026): the /api/v1/jobs group is mounted SEPARATELY from
// the protected (admin) subtree. It carries JobClientAuthMiddleware
// (per-client Bearer VELOX_M2M_SECRET + scope checks) instead of the
// shared admin/worker Auth, so a remote submitter (PipelineGen /
// Agent / second PC) can POST a job and poll its status WITHOUT
// knowing VELOX_ADMIN_TOKEN. The admin /api/jobs surface (List, Stats,
// GetFull, Cancel, Retry, Events, Replay, History) stays on the
// protected group under Auth.
func (r *Router) registerAPIRoutes(engine *gin.Engine, log *zap.Logger) *gin.RouterGroup {
	// API routes
	api := engine.Group("/api")
	{
		// Protected routes — Auth + RateLimit + WorkspaceScope
		protected := api.Group("")
		protected.Use(middleware.Auth(r.cfg.Auth, r.cfg.Log))
		r.rateLimitMiddleware = middleware.RateLimit(r.cfg.Rate, middleware.NewOSEnvReader())
		protected.Use(r.rateLimitMiddleware.Handler)
		protected.Use(middleware.WorkspaceScopeMiddleware())
		{
			// Use module registry for route registration
			if r.registry != nil {
				log.Info("using module registry for route registration")
				r.registry.RegisterAllRoutes(protected)
			} else {
				log.Warn("no module registry available, no routes registered")
			}
		}

		// M2M (machine-to-machine) job surface — /api/v1/jobs.
		// Distinct principal from admin/worker: a remote submitter with
		// a per-client secret + jobs.submit/jobs.read scopes. Mounted on
		// its own group so it does NOT inherit the admin Auth guard
		// (otherwise a leaked admin token could reach this surface via
		// header confusion). Nil-safe on the handler: skip when no M2M
		// jobs handler is wired (dev/test/E2E fixtures). The M2MSecurityPort
		// (per-client secret store) is OPTIONAL at this layer — when nil,
		// JobClientAuthMiddleware receives nil and its EnableM2M()==false
		// path short-circuits to pass-through (admin context); once the
		// store lands the guard activates without re-wiring the routes.
		if r.m2mJobsHandler != nil {
			m2mJobs := api.Group("/v1/jobs")
			m2mJobs.Use(middleware.JobClientAuthMiddleware(r.cfg.M2M, r.cfg.Log))
			// Per-route RequireScope (jobs.submit / jobs.read) is applied
			// inside the module's RegisterRoutes so the scope gate sits
			// immediately before the handler.
			r.m2mJobsHandler.RegisterRoutes(m2mJobs)
			m2mEnabled := false
			if r.cfg.M2M != nil {
				m2mEnabled = r.cfg.M2M.EnableM2M()
			}
			log.Info("M2M job surface mounted",
				zap.String("prefix", "/api/v1/jobs"),
				zap.Bool("m2m_enabled", m2mEnabled))
		} else {
			log.Info("M2M job surface not mounted (no M2M jobs handler wired)")
		}

		// M2M media-read surface — /api/v1/media. Same principal as the
		// M2M job surface (Bearer VELOX_M2M_SECRET) but a distinct scope
		// (media.read). It lets a remote submitter enumerate the media
		// SSOT elements (filters + pagination + facets) and read a single
		// asset, so it can build a job payload from real rows instead of
		// reading the Master's database out of band. Read-only by
		// construction: the module mounts only GET /assets, /assets/:id
		// and /facets — no mutation reaches this principal.
		// Nil-safe on the handler: skip when no M2M media handler is wired
		// (dev/test/E2E fixtures). Mounted on its own group so it does NOT
		// inherit the admin Auth guard.
		if r.m2mMediaHandler != nil {
			m2mMedia := api.Group("/v1/media")
			m2mMedia.Use(middleware.JobClientAuthMiddleware(r.cfg.M2M, r.cfg.Log))
			r.m2mMediaHandler.RegisterRoutes(m2mMedia)
			log.Info("M2M media read surface mounted", zap.String("prefix", "/api/v1/media"))
		} else {
			log.Info("M2M media read surface not mounted (no M2M media handler wired)")
		}
	}
	return api
}
