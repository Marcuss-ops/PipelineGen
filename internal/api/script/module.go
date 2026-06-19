package script

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	middleware "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// NewModule creates the script-flow module for the API registry.
// It wraps the thin Handler in a RouteModule that registers under /api/script.
func NewModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *Handler,
) api.Module {
	return api.NewRouteModule(
		"script-flow",
		func(cfg *config.Config) bool { return cfg.Features.ScriptDocsEnabled },
		"/script",
		handler,
		log,
		api.WithMiddleware(middleware.ScriptDocsEnabled(cfg)),
	)
}
