package channels

import (
	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	sqlite "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite"

	"go.uber.org/zap"
)

// NewModule creates the Channels module for the API registry.
func NewModule(
	log *zap.Logger,
	repo *sqlite.ChannelsRepository,
) *api.RouteModule {
	return api.NewRouteModule(
		"channels",
		func(cfg *config.Config) bool { return repo != nil },
		"/channels",
		NewChannelsHandler(repo, log),
		log,
	)
}
