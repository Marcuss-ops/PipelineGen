package api

import (
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	channelsrepo "github.com/Marcuss-ops/PipelineGen/internal/repository/channels"

	"go.uber.org/zap"
)

// NewChannelsModule creates a new module for category_channels CRUD API.
func NewChannelsModule(
	log *zap.Logger,
	repo *channelsrepo.Repository,
) *RouteModule {
	handler := NewChannelsHandler(repo, log)
	return NewRouteModule(
		"channels",
		func(cfg *config.Config) bool { return repo != nil },
		"/channels",
		handler,
		log,
	)
}
