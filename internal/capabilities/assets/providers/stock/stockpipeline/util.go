package stockpipeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	urlutil "github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// F2.10: resolveFolderTarget RETIRED (override brutal). Folder
// resolution now goes through `s.publisher.ResolveFolder(ctx,
// delivery.PublishRequest{Group: seg, DestinationFolderID: currentID})`
// in stockpipeline.run.go, which uses DestinationStock's PathBuilder
// to compute the canonical folder hierarchy. The legacy
// driveutil.EnsureFolderPath walking is gone; the legacy
// `s.driveAdmin driveup.Admin` surface is gone (see
// `internal/platform/drive` import).

// formatDuration converts a float64 seconds value to HH:MM:SS.mmm format.
func formatDuration(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	d := time.Duration(sec * float64(time.Second))
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	ms := (d - s*time.Second) / time.Millisecond
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
}

// extractVideoID extracts the video ID from a YouTube URL.
func extractVideoID(url string) string {
	if id, err := urlutil.ExtractVideoID(url); err == nil {
		return id
	}
	if strings.Contains(url, "v=") {
		for _, part := range strings.Split(url, "&") {
			if strings.HasPrefix(part, "v=") {
				return strings.TrimPrefix(part, "v=")
			}
		}
	}
	parts := strings.Split(strings.TrimSpace(url), "/")
	if len(parts) == 0 {
		return url
	}
	return strings.TrimSpace(parts[len(parts)-1])
}

// ─── Sub-operation vocabulary (PR-STOCK-SUB-OPERATIONS, October 2026) ───
//
// Each stock job step is ONE span in the stage breakdown (stock.stage_sources
// / stock.extract / stock.compose): before these helpers a 15-source request
// measured as a single wall, and the operator could not tell download time
// from cut time from render time. They decompose each span into per-unit
// canonical kernel OPERATIONS (run_operation_observations rows), so the
// timing snapshot attributes the stock wall to download vs cut vs render
// without a second timer: the enclosing stage wall is unchanged, the
// operations are the measured sub-spans of it.
//
// godlike/06 SSOT: the helpers here are the ONLY place that maps a stock
// sub-span to (stage, component, operation); call sites pass their unit
// counters and the kernel owns the timing. When no run is bound to the
// context, kernobs.MeasureOperation degrades to a plain call — measurement
// must never change behaviour.
//
// (Landed in util.go rather than a dedicated step_sub_operations.go because
// the stockpipeline package is a registered hotspot whose file baseline
// ratchet — architecture/package_hotspots.json, percheck_legacy_hotspot_growth
// — allows no new production files while the decomposition toward
// internal/capabilities/assets/stock/* is active.)

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
