package module

import (
	"velox/go-master/internal/api/handlers/script/handlers"
	"velox/go-master/internal/api/middleware"
	"velox/go-master/pkg/config"

	"go.uber.org/zap"
)

// NewScriptDocsModule creates a new ScriptDocs module using RouteModule
func NewScriptDocsModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *handlers.ScriptDocsHandler,
) *RouteModule {
	return NewRouteModule(
		"script-docs",
		func(cfg *config.Config) bool { return cfg.Features.ScriptDocsEnabled },
		"/script-docs",
		handler,
		log,
		WithMiddleware(middleware.ScriptDocsEnabled(cfg)),
	)
}
