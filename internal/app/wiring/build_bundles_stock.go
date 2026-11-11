// Package wiring retains the compatibility builder and typed aliases for
// Stock. The subsystem implementation lives in wiring/stock; this root
// supplies only the batch-module factory, whose dependencies belong to the
// application composition layer. The wrapper remains the frozen canonical
// construction site and intentionally starts no goroutines.
package wiring

import stockwiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/stock"

// Stock dependency contracts are owned by the stock wiring module. These
// aliases keep the composition package's existing typed builder surface
// source-compatible while its assembly logic migrates by subsystem.
type StockBundleDeps = stockwiring.StockBundleDeps
type StockSourceCacheDeps = stockwiring.StockSourceCacheDeps
type StockRuntimeDeps = stockwiring.StockRuntimeDeps
type StockDeliveryDeps = stockwiring.StockDeliveryDeps
type StockDriveReader = stockwiring.StockDriveReader
type StockAcquisitionDeps = stockwiring.StockAcquisitionDeps
type StockMediaDeps = stockwiring.StockMediaDeps
type StockOrchestrationDeps = stockwiring.StockOrchestrationDeps
type StockFeatureGate = stockwiring.StockFeatureGate
type StockEnrichmentDeps = stockwiring.StockEnrichmentDeps

// BuildStockBundle preserves the canonical composition-root entry point while
// delegating stock service/API assembly to the stock wiring module. Batch
// module construction stays injected because it uses application-root helpers.
func BuildStockBundle(deps StockBundleDeps) (*stockwiring.StockPipelineWiring, error) {
	return stockwiring.BuildBundle(deps, buildStockBatchModule)
}
