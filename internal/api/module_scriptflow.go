package api

import (
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
)

// NewScriptFlowModule creates a new /api/script module with text generation and visual planning routes.
func NewScriptFlowModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *ScriptFlowHandler,
) *RouteModule {
	return NewRouteModule(
		"script-flow",
		func(cfg *config.Config) bool { return cfg.Features.ScriptDocsEnabled },
		"/script",
		handler,
		log,
		WithMiddleware(ScriptDocsEnabled(cfg)),
	)
}
