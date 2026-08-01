// build_stock_batches.go — Gate 5 of BuildStockBundle: the stock
// batch coordinator + /stock-batches module construction. Extracted
// so the BuildStockBundle orchestrator stays a thin gate dispatcher.
package app

import (
	"fmt"

	api "github.com/Marcuss-ops/PipelineGen/internal/api"
	stockbatches "github.com/Marcuss-ops/PipelineGen/internal/api/assets/stockbatches"
	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockplan"
)

// buildStockBatchModule constructs the stock batch coordinator and the
// /stock-batches module. Returns (nil, nil) when no BatchRepository is
// wired (backcompat/test mode — the batch surface is simply absent).
// Error wrapping follows the BuildStockBundle preamble convention
// (`stock.BuildStockBundle: stockbatches.Build: %w`).
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
