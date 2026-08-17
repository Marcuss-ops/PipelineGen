// cmd/admin/qdrant_readiness_checks_routes.go — routes readiness check
// + router helper + cfg-derived ports + compile-time assertions
// extracted from qdrant_readiness_checks.go (LONG-FILES-DECOMPOSITION-2026-07-06 Band C #1).
//
// Owns: checkRoutesReal, buildRouterWithProductionWiring, cfgAuthPort,
// cfgRatePort, cfgFeaturesPort, engineHasPath, compile-time assertions,
// legacyauditClassify marker.
package main

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"

	api "github.com/Marcuss-ops/PipelineGen/internal/api"
	middlewareports "github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/application/qdrant/legacyaudit"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ── Routes check ──────────────────────────────────────────────────────

// checkRoutesReal: production-shaped. Replaces the stub-router
// pattern with a real router built from production handlers.
func checkRoutesReal(_ context.Context, deps readinessDeps) checkStatus {
	if deps.Root == nil {
		return checkStatus{Err: "production composition root is nil — routes check requires real handler wiring"}
	}
	if deps.Root.OutboxHandler == nil {
		return checkStatus{Err: "outbox handler is nil — production ServerDeps wiring is missing"}
	}
	if deps.Root.MediasearchHandler == nil {
		return checkStatus{Err: "mediasearch handler is nil — production ServerDeps wiring is missing"}
	}
	engine := buildRouterWithProductionWiring(deps)
	if !engineHasPath(engine, "GET", "/internal/v1/outbox/") {
		return checkStatus{Err: "/internal/v1/outbox/* route not registered in production-shaped router"}
	}
	if !engineHasPath(engine, "POST", "/internal/v1/media/search") {
		return checkStatus{Err: "/internal/v1/media/search route not registered in production-shaped router"}
	}
	return checkStatus{Pass: true}
}

// ── Router helper ─────────────────────────────────────────────────────

// buildRouterWithProductionWiring: production-shaped. Builds the
// canonical api.Router from cfg.Security + cfg.FeatureFlags (no stub
// ports) and injects the production outbox + mediasearch handler
// instances from root.
func buildRouterWithProductionWiring(deps readinessDeps) *gin.Engine {
	gin.SetMode(gin.TestMode)
	server := api.NewServerWithHealth(api.ServerDeps{
		Config: deps.Cfg,
		Handlers: api.InternalHandlers{
			Outbox:      deps.Root.OutboxHandler,
			MediaSearch: deps.Root.MediasearchHandler,
		},
	})
	return server.GetRouter()
}

// ── cfg-derived ports (no stubs) ──────────────────────────────────────

type (
	cfgAuthPort     struct{ Cfg *config.Config }
	cfgRatePort     struct{ Cfg *config.Config }
	cfgFeaturesPort struct{ Cfg *config.Config }
)

func (p *cfgAuthPort) EnableAuth() bool {
	if p.Cfg == nil {
		return false
	}
	return strings.TrimSpace(p.Cfg.Security.AdminToken) != "" ||
		strings.TrimSpace(p.Cfg.Security.WorkerToken) != ""
}
func (p *cfgAuthPort) AdminToken() string {
	if p.Cfg == nil {
		return ""
	}
	return p.Cfg.Security.AdminToken
}
func (p *cfgAuthPort) WorkerToken() string {
	if p.Cfg == nil {
		return ""
	}
	return p.Cfg.Security.WorkerToken
}

func (p *cfgRatePort) RateLimitEnabled() bool {
	if p.Cfg == nil {
		return false
	}
	return p.Cfg.Security.RateLimitEnabled
}
func (p *cfgRatePort) RateLimitRequests() int {
	if p.Cfg == nil {
		return 0
	}
	return p.Cfg.Security.RateLimitRequests
}

func (p *cfgFeaturesPort) ArtlistEnabled() bool {
	if p.Cfg == nil {
		return false
	}
	return p.Cfg.Features.ArtlistEnabled
}
func (p *cfgFeaturesPort) ScriptClipsEnabled() bool {
	if p.Cfg == nil {
		return false
	}
	return p.Cfg.Features.ScriptClipsEnabled
}

// Compile-time guards: cfg-derived ports satisfy the canonical middleware ports.
var (
	_ middlewareports.AuthSecurityPort = (*cfgAuthPort)(nil)
	_ middlewareports.RateLimitPort    = (*cfgRatePort)(nil)
	_ middlewareports.FeatureFlagsPort = (*cfgFeaturesPort)(nil)
)

// ── Helpers ────────────────────────────────────────────────────────────

func engineHasPath(engine *gin.Engine, method, prefix string) bool {
	if engine == nil {
		return false
	}
	for _, r := range engine.Routes() {
		if strings.EqualFold(r.Method, method) && strings.HasPrefix(r.Path, prefix) {
			return true
		}
	}
	return false
}

// legacyauditClassify is a compile-time reference ensuring the
// legacyaudit package import stays live (PR 14 ↔ PR 15 consistency).
var _ = legacyaudit.Classify
