package module

import (
	channelshandler "github.com/Marcuss-ops/PipelineGen/internal/api/handlers/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	channelsrepo "github.com/Marcuss-ops/PipelineGen/internal/repository/channels"

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
