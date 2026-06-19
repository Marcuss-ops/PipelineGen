package channels

import (
	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	channelsrepo "github.com/Marcuss-ops/PipelineGen/internal/repository/channels"

	"go.uber.org/zap"
)

// NewModule creates the Channels module for the API registry.
func NewModule(
	log *zap.Logger,
	repo *channelsrepo.Repository,
) *api.RouteModule {
	return api.NewRouteModule(
		"channels",
		func(cfg *config.Config) bool { return repo != nil },
		"/channels",
		NewChannelsHandler(repo, log),
		log,
	)
}
