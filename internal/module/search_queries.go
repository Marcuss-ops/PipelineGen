package module

import (
	searchquerieshandler "velox/go-master/internal/api/handlers/searchqueries"
	"velox/go-master/internal/config"
	searchqueriesrepo "velox/go-master/internal/repository/searchqueries"

	"go.uber.org/zap"
)

// NewSearchQueriesModule creates a new module for search_queries CRUD API.
func NewSearchQueriesModule(
	log *zap.Logger,
	repo *searchqueriesrepo.Repository,
) *RouteModule {
	handler := searchquerieshandler.NewHandler(repo, log)
	return NewRouteModule(
		"search_queries",
		func(cfg *config.Config) bool { return repo != nil },
		"/search-queries",
		handler,
		log,
	)
}
