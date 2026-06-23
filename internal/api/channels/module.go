package channels

import (
	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"

	"go.uber.org/zap"
)

// NewModule creates the Channels module for the API registry.
func NewModule(
	log *zap.Logger,
	repo *assets.ChannelsRepository,
) *api.RouteModule {
	return api.NewRouteModule(
		"channels",
		func() bool { return repo != nil },
		"/channels",
		NewChannelsHandler(repo, log),
		log,
	)
}
