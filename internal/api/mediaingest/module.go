// Package mediaingest provides the MediaIngest module factory.
package mediaingest

import (
	api "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
)

// NewMediaIngestModule creates the MediaIngest module factory for the API registry.
// The handler is the canonical MediaingestHandler defined in this same package
// (impl.go). It is NOT an api.MediaingestHandler alias because that would create
// an import cycle (api → mediaingest → api).
func NewMediaIngestModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *MediaingestHandler,
) *api.RouteModule {
	return api.NewRouteModule(
		"media-ingest",
		func(cfg *config.Config) bool { return handler != nil },
		"/media",
		handler,
		log,
	)
}
