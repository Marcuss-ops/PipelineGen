package module

import (
	"velox/go-master/internal/api/handlers/script/handlers"
	"velox/go-master/internal/api/middleware"
	"velox/go-master/internal/config"

	"go.uber.org/zap"
)

// NewScriptFlowModule creates a new /api/script module with text generation and visual planning routes.
func NewScriptFlowModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *handlers.ScriptFlowHandler,
) *RouteModule {
	return NewRouteModule(
		"script-flow",
		func(cfg *config.Config) bool { return cfg.Features.ScriptDocsEnabled },
		"/script",
		handler,
		log,
		WithMiddleware(middleware.ScriptDocsEnabled(cfg)),
	)
}
