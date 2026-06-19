package api

import (
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
)

// NewLessonsModule creates a new /api/lessons module for lesson generation.
func NewLessonsModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *LessonsHandler,
) *RouteModule {
	return NewRouteModule(
		"lessons",
		func(cfg *config.Config) bool { return cfg.Lessons.Enabled },
		"/lessons",
		handler,
		log,
	)
}
