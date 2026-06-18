package module

import (
	"github.com/Marcuss-ops/PipelineGen/internal/api/handlers/lessons"
	"github.com/Marcuss-ops/PipelineGen/internal/config"

	"go.uber.org/zap"
)

// NewLessonsModule creates a new /api/lessons module for lesson generation.
func NewLessonsModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *lessons.Handler,
) *RouteModule {
	return NewRouteModule(
		"lessons",
		func(cfg *config.Config) bool { return cfg.Lessons.Enabled },
		"/lessons",
		handler,
		log,
	)
}
