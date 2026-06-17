package module

import (
	"velox/go-master/internal/api/handlers/lessons"
	"velox/go-master/internal/config"

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
