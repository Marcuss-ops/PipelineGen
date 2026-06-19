package sources

import (
	api "github.com/Marcuss-ops/PipelineGen/internal/api"
	searchqueriesapi "github.com/Marcuss-ops/PipelineGen/internal/api/searchqueries"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	searchqueriesrepo "github.com/Marcuss-ops/PipelineGen/internal/repository/searchqueries"

	"go.uber.org/zap"
)

// NewSearchQueriesModule creates the SearchQueries module for the API registry.
// The handler lives in internal/api/searchqueries/; this factory wraps it as a RouteModule.
func NewSearchQueriesModule(
	log *zap.Logger,
	repo *searchqueriesrepo.Repository,
) *api.RouteModule {
	return api.NewRouteModule(
		"search_queries",
		func(cfg *config.Config) bool { return repo != nil },
		"/search-queries",
		searchqueriesapi.NewSearchqueriesHandler(repo, log),
		log,
	)
}
