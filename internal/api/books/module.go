package books

import (
	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"

	"go.uber.org/zap"
)

// NewModule creates the Books module for the API registry.
func NewModule(
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
