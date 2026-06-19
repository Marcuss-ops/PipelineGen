package searchqueries

import (
	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	searchqueriesrepo "github.com/Marcuss-ops/PipelineGen/internal/repository/searchqueries"

	"go.uber.org/zap"
)

// NewModule creates the SearchQueries module for the API registry.
func NewModule(
	log *zap.Logger,
	repo *searchqueriesrepo.Repository,
) *api.RouteModule {
	return api.NewRouteModule(
		"search_queries",
		func(cfg *config.Config) bool { return repo != nil },
		"/search-queries",
		NewSearchqueriesHandler(repo, log),
		log,
	)
}
