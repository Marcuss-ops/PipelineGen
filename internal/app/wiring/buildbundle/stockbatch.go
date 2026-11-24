package buildbundle

import (
	"fmt"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	stockplan "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockplan"
	stockbatches "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/stockbatches"
	api "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
)

func buildStockBatchModule(deps StockBundleDeps, svc *stockpipeline.Service) (api.Module, error) {
	var batchModule api.Module
	if deps.Acquisition.BatchRepository != nil {
		coordinator := stockplan.NewCoordinator(stockplan.CoordinatorDeps{
			Repo:     deps.Acquisition.BatchRepository,
			Enqueuer: deps.Orchestration.Jobs,
			Resolver: nil,
			Stager:   svc,
			Log:      deps.Runtime.Log,
		})
		batchDescriptor, batchErr := stockbatches.Build(stockbatches.Dependencies{
			Coordinator: coordinator,
			EnabledFunc: deps.Feature.StockPipelineEnabled,
			Logger:      deps.Runtime.Log,
		})
		if batchErr != nil {
			return nil, fmt.Errorf("stock.BuildStockBundle: stockbatches.Build: %w", batchErr)
		}
		if d, ok := batchDescriptor.(*stockbatches.StockBatchesDescriptor); ok && d != nil {
			batchModule = d.Module
		}
	}
	return batchModule, nil
}
