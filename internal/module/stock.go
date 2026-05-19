package module

import (
	"velox/go-master/internal/api/handlers/sources"
	"velox/go-master/internal/config"

	"go.uber.org/zap"
)

func NewStockPipelineModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *sources.StockHandler,
) *RouteModule {
	return NewRouteModule(
		"stock-pipeline",
		func(cfg *config.Config) bool { return cfg.Features.StockPipelineEnabled },
		"/stock-pipeline",
		handler,
		log,
	)
}
