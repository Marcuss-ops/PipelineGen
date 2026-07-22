package adapters

import (
	"context"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// StockBindingsProcessor applies caller-supplied stock assets to the
// already clip-bound SpecScene. Direct bindings are deterministic and take
// precedence over any legacy/semantic stock value.
type StockBindingsProcessor struct{}

func NewStockBindingsProcessor() *StockBindingsProcessor { return &StockBindingsProcessor{} }
func (p *StockBindingsProcessor) Name() ProcessorName    { return ProcessorStockBindings }
func (p *StockBindingsProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorRequired
}

func (p *StockBindingsProcessor) Process(_ context.Context, _ *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	stockEnabled := input.StockEnabled.AsBool() ||
		(input.StockEnabled != scriptpkg.ToggleDisabled && len(input.StockBindings) > 0)
	if !stockEnabled {
		for i := range input.SpecScene.Scenes {
			input.SpecScene.Scenes[i].Bindings.Stock = nil
		}
		return &PostProcessResult{Changed: true, UpdatedSpecScene: input.SpecScene}, nil
	}
	if len(input.StockBindings) == 0 {
		return nil, fmt.Errorf("%w: stock_bindings enabled but no direct stock bindings were supplied", scriptpkg.ErrPostprocessFailed)
	}
	seen := make(map[int]struct{}, len(input.StockBindings))
	for _, in := range input.StockBindings {
		if in.Index < 0 || in.Index >= len(input.SpecScene.Scenes) {
			return nil, fmt.Errorf("%w: stock binding index %d is outside SpecScene", scriptpkg.ErrPostprocessFailed, in.Index)
		}
		if _, ok := seen[in.Index]; ok {
			return nil, fmt.Errorf("%w: duplicate stock binding index %d", scriptpkg.ErrPostprocessFailed, in.Index)
		}
		seen[in.Index] = struct{}{}
		scene := &input.SpecScene.Scenes[in.Index]
		if strings.TrimSpace(in.SceneID) != "" && in.SceneID != scene.ID {
			return nil, fmt.Errorf("%w: stock binding index %d targets scene_id %q, want %q", scriptpkg.ErrPostprocessFailed, in.Index, in.SceneID, scene.ID)
		}
		if strings.TrimSpace(in.SegmentID) != "" && in.SegmentID != scene.SegmentID {
			return nil, fmt.Errorf("%w: stock binding index %d targets segment_id %q, want %q", scriptpkg.ErrPostprocessFailed, in.Index, in.SegmentID, scene.SegmentID)
		}
		if strings.TrimSpace(in.AssetID) == "" && strings.TrimSpace(in.DriveLink) == "" {
			return nil, fmt.Errorf("%w: stock binding index %d requires asset_id or drive_link", scriptpkg.ErrPostprocessFailed, in.Index)
		}
		if in.StartMs < 0 || in.EndMs <= in.StartMs {
			return nil, fmt.Errorf("%w: stock binding index %d requires end_ms > start_ms", scriptpkg.ErrPostprocessFailed, in.Index)
		}
		scene.Bindings.Stock = &scriptpkg.StockBinding{
			AssetID: in.AssetID, Name: in.Name, Source: in.Source,
			DriveLink: in.DriveLink, Score: in.Score, Fallback: in.Fallback,
			StartMs: in.StartMs, EndMs: in.EndMs, DurationMs: in.EndMs - in.StartMs,
		}
	}
	return &PostProcessResult{Changed: true, UpdatedSpecScene: input.SpecScene}, nil
}
