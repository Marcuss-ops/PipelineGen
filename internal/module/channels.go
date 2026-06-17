package module

import (
	channelshandler "velox/go-master/internal/api/handlers/channels"
	"velox/go-master/internal/config"
	channelsrepo "velox/go-master/internal/repository/channels"

	"go.uber.org/zap"
)

// NewChannelsModule creates a new module for category_channels CRUD API.
func NewChannelsModule(
	log *zap.Logger,
	repo *channelsrepo.Repository,
) *RouteModule {
	handler := channelshandler.NewHandler(repo, log)
	return NewRouteModule(
		"channels",
		func(cfg *config.Config) bool { return repo != nil },
		"/channels",
		handler,
		log,
	)
}
