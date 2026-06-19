package api

import (
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
)

// NewBooksModule creates a new /api/books module for book summarization/processing.
func NewBooksModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *BooksHandler,
) *RouteModule {
	return NewRouteModule(
		"books",
		func(cfg *config.Config) bool { return cfg.Books.Enabled },
		"/books",
		handler,
		log,
	)
}
