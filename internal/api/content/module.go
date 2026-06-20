package content

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
)

// NewBooksModule creates the Books sub-module for the API registry.
// Each sub-module preserves its historical URL subtree so existing
// clients (curl, frontend, scripts) keep working.
// The module is gated by cfg.Books.Enabled.
func NewBooksModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *BooksHandler,
) *api.RouteModule {
	return api.NewRouteModule(
		"books",
		func(cfg *config.Config) bool { return cfg.Books.Enabled },
		"/books",
		handler,
		log,
	)
}

// NewLessonsModule creates the Lessons sub-module for the API registry.
// The module is gated by cfg.Lessons.Enabled.
func NewLessonsModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *LessonsHandler,
) *api.RouteModule {
	return api.NewRouteModule(
		"lessons",
		func(cfg *config.Config) bool { return cfg.Lessons.Enabled },
		"/lessons",
		handler,
		log,
	)
}
