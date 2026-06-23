package sources

import (
	api "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"

	"go.uber.org/zap"
)

// NewStockPipelineModule creates the Stock Pipeline module factory.
func NewStockPipelineModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *StockHandler,
) *api.RouteModule {
	return api.NewRouteModule(
		"stock-pipeline",
		func() bool { return cfg.Features.StockPipelineEnabled },
		"/stock-pipeline",
		handler,
		log,
	)
}
