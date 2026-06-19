package api

import (
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
)

func NewStockPipelineModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *StockHandler,
) *RouteModule {
	return NewRouteModule(
		"stock-pipeline",
		func(cfg *config.Config) bool { return cfg.Features.StockPipelineEnabled },
		"/stock-pipeline",
		handler,
		log,
	)
}
