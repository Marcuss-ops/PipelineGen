// Package stockpipeline — step_sub_operations.go
// (PR-STOCK-SUB-OPERATIONS, October 2026).
//
// Sub-operation vocabulary for the stock jobs. Each stock job step is ONE
// span in the stage breakdown (stock.stage_sources / stock.extract /
// stock.compose): before this file a 15-source request measured as a single
// wall, and the operator could not tell download time from cut time from
// render time. These helpers decompose each span into per-unit canonical
// kernel OPERATIONS (run_operation_observations rows), so the timing
// snapshot attributes the stock wall to download vs cut vs render without a
// second timer: the enclosing stage wall is unchanged, the operations are
// the measured sub-spans of it.
//
// godlike/06 SSOT: the helpers here are the ONLY place that maps a stock
// sub-span to (stage, component, operation); call sites pass their unit
// counters and the kernel owns the timing. When no run is bound to the
// context, kernobs.MeasureOperation degrades to a plain call — measurement
// must never change behaviour.
package stockpipeline

import (
	"context"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// stockSourceComponent maps the plan's canonical SourceProvider bucket to the
// component that OWNS the staging boundary for it. YouTube is the only
// provider with its own adapter component at this boundary; Pexels/Pixabay
// and the unknown/empty buckets are attributed to the stager itself — the
// only component guaranteed to exist there. The raw bucket always travels
// the Provider field, so the snapshot can still split downloads by provider.
func stockSourceComponent(provider string) kernobs.ComponentName {
	switch provider {
	case SourceProviderYouTube:
		return kernobs.ComponentYouTube
	default:
		return kernobs.ComponentName("source_stager")
	}
}

// measureSourceDownload brackets ONE unique-source staging (the yt-dlp /
// download operation under the stock.stage_sources stage. One source is
// exactly one download: the step deduplicates by SourceID before staging, so
// the operation count IS the unique-source count.
func measureSourceDownload(ctx context.Context, provider string, fn func(context.Context) error) error {
	return kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
		Stage:     kernobs.StageStockStageSources,
		Component: stockSourceComponent(provider),
		Operation: kernobs.OperationDownload,
		Provider:  provider,
		Items:     1,
	}, fn)
}

// measureSourceCut brackets ONE source-local cut batch (VideoCutter.Cut for a
// single source group) as a canonical cut operation under the stock.extract
// stage. Items is the batch cardinality (planned clips of that source).
func measureSourceCut(ctx context.Context, clips int64, fn func(context.Context) error) error {
	return kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
		Stage:     kernobs.StageStockExtract,
		Component: kernobs.ComponentFFmpeg,
		Operation: kernobs.OperationCut,
		Items:     clips,
	}, fn)
}

// measureChunkRender brackets ONE composed chunk (StockRenderer.Render for a
// single cut path) as a canonical render operation under the stock.compose
// stage. One chunk is exactly one render pass (1:1 cardinality).
func measureChunkRender(ctx context.Context, fn func(context.Context) error) error {
	return kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
		Stage:     kernobs.StageStockCompose,
		Component: kernobs.ComponentFFmpeg,
		Operation: kernobs.OperationRender,
		Items:     1,
	}, fn)
}
