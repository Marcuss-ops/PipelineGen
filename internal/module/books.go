package module

import (
	"github.com/Marcuss-ops/PipelineGen/internal/api/handlers/books"
	"github.com/Marcuss-ops/PipelineGen/internal/config"

	"go.uber.org/zap"
)

// NewBooksModule creates a new /api/books module for book summarization/processing.
func NewBooksModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *books.Handler,
) *RouteModule {
	return NewRouteModule(
		"books",
		func(cfg *config.Config) bool { return cfg.Books.Enabled },
		"/books",
		handler,
		log,
	)
}
