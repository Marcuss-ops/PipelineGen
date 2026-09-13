package rustexec

import (
	"context"
	"fmt"
	"math"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// stockComposeRevision is the Revision stamped on every stock-compose
// render plan. The canonical render contract requires a non-empty identity;
// the stock compose step re-encodes a single cut clip per plan.
const stockComposeRevision = "stock.compose.v1"

// stockInputFacts is the probe result for one compose input: the duration
// that drives the canonical timeline and whether the asset carries audio (so
// the caller can decide whether a copy-only mux can preserve it).
type stockInputFacts struct {
	Path       string
	DurationUS int64
	HasAudio   bool
}

// probeInput probes one input file through the shared Rust media executor.
func (r *StockRenderer) probeInput(ctx context.Context, path string) (stockInputFacts, error) {
	if r.client == nil {
		return stockInputFacts{}, fmt.Errorf("stock render: media client is not configured")
	}
	resp, err := r.client.call(ctx, request{Operation: OperationProbe, SourcePath: path})
	if err != nil {
		return stockInputFacts{}, fmt.Errorf("stock render: probe %s: %w", path, err)
	}
	if resp.Metadata == nil || resp.Metadata.DurationSec <= 0 {
		return stockInputFacts{}, fmt.Errorf("stock render: probe %s returned no duration", path)
	}
	return stockInputFacts{
		Path:       path,
		DurationUS: int64(math.Round(resp.Metadata.DurationSec * 1_000_000)),
		HasAudio:   resp.Metadata.HasAudio,
	}, nil
}

// compileStockRenderPlan builds and seals the canonical render.RenderPlan for
// the stock compose step.
//
// The plan is one primary video track whose segments are the compose inputs
// in order, each consumed from its full source window (SourceInUS=0), with a
// manifest entry carrying the REAL on-disk SHA-256 and frame count. This is
// the canonical contract Rust render_stock consumes; the legacy
// transitions/effect_paths envelope is no longer accepted by the executor.
func (r *StockRenderer) compileStockRenderPlan(input stockpipeline.RenderRequest, facts []stockInputFacts, outputPath string, rate audio.FrameRate) (render.RenderPlan, error) {
	if len(facts) == 0 {
		return render.RenderPlan{}, fmt.Errorf("stock render: at least one input path is required")
	}
	resolver, err := audio.NewFrameResolver(rate)
	if err != nil {
		return render.RenderPlan{}, fmt.Errorf("stock render: %w", err)
	}
	manifest := make([]render.AssetManifestEntry, 0, len(facts))
	segments := make([]audio.TimelineSegment, 0, len(facts))
	var timelineStartUS int64
	for index, fact := range facts {
		hash, _, err := digest.SHA256File(fact.Path)
		if err != nil {
			return render.RenderPlan{}, fmt.Errorf("stock render: hash input %s: %w", fact.Path, err)
		}
		_, frameCount, err := resolver.FrameRange(timelineStartUS, fact.DurationUS)
		if err != nil {
			return render.RenderPlan{}, fmt.Errorf("stock render: frame range for %s: %w", fact.Path, err)
		}
		assetID := fmt.Sprintf("stock-input-%d", index)
		manifest = append(manifest, render.AssetManifestEntry{
			AssetID: assetID, Path: fact.Path, SHA256: hash, FrameCount: frameCount,
		})
		segments = append(segments, audio.TimelineSegment{
			ID: assetID, Index: index, TimelineStartUS: timelineStartUS, DurationUS: fact.DurationUS,
			Video: audio.VideoSegment{
				AssetID: assetID, SourceInUS: 0, SourceDurationUS: fact.DurationUS,
				TimelineOffsetUS: 0, TimelineDurationUS: fact.DurationUS,
			},
			// The canonical video executor is video-only; audio is preserved by
			// a separate copy-only mux. The timeline intent is SILENCE so the
			// video plan never claims an audio mix it does not perform.
			Audio: audio.AudioIntent{Mode: audio.AudioSilence},
		})
		timelineStartUS += fact.DurationUS
	}
	plan, err := render.Compile(render.CompileInput{
		JobID:      fmt.Sprintf("stock-compose-%d", input.ChunkIndex),
		Revision:   stockComposeRevision,
		OutputPath: outputPath,
		FrameRate:  rate,
		Timeline:   audio.CanonicalTimeline{Version: audio.TimelineVersion, DurationUS: timelineStartUS, Segments: segments},
		Manifest:   manifest,
	})
	if err != nil {
		return render.RenderPlan{}, fmt.Errorf("stock render: compile canonical plan: %w", err)
	}
	return plan, nil
}
