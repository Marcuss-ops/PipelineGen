package rustexec

import (
	"context"
	"fmt"
	"time"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

type StockRenderer struct {
	client *Client
	policy config.VideoEncoderPolicy
}

func NewStockRenderer(binaryPath, ffmpegPath string, policy config.VideoEncoderPolicy, log *zap.Logger) *StockRenderer {
	return &StockRenderer{client: NewClient(binaryPath, ffmpegPath, log), policy: policy}
}

func (r *StockRenderer) Render(ctx context.Context, input stockpipeline.RenderRequest) (stockpipeline.RenderResult, error) {
	if !input.NoTransitions && len(input.Transitions) == 0 {
		return stockpipeline.RenderResult{}, fmt.Errorf("unresolved render plan: transitions must be resolved by Go")
	}
	if !input.NoEffects && len(input.EffectPaths) == 0 {
		return stockpipeline.RenderResult{}, fmt.Errorf("unresolved render plan: effect paths must be resolved by Go")
	}
	for _, transition := range input.Transitions {
		if transition.ID == "" || (transition.Segment != "start" && transition.Segment != "end") {
			return stockpipeline.RenderResult{}, fmt.Errorf("invalid resolved transition assignment")
		}
	}
	for _, effect := range input.EffectPaths {
		if effect.Path == "" {
			return stockpipeline.RenderResult{}, fmt.Errorf("invalid resolved effect path assignment")
		}
	}
	codec, preset, crf, err := (&VideoProcessor{client: r.client, policy: r.policy}).policyFor(input.Codec, input.Preset, input.CRF)
	wireTransitions := make([]renderTransition, len(input.Transitions))
	for i, transition := range input.Transitions {
		wireTransitions[i] = renderTransition{ClipIndex: transition.ClipIndex, Segment: transition.Segment, ID: transition.ID}
	}
	wireEffects := make([]renderEffectPath, len(input.EffectPaths))
	for i, effect := range input.EffectPaths {
		wireEffects[i] = renderEffectPath{ClipIndex: effect.ClipIndex, Path: effect.Path}
	}
	if err != nil {
		return stockpipeline.RenderResult{}, err
	}
	_, err = r.client.call(ctx, request{
		Operation: "render_stock", OutputPath: input.OutputPath, InputPaths: input.InputPaths,
		Codec: codec, Preset: preset, CRF: crf,
		Width: uint32(input.Width), Height: uint32(input.Height), FPS: uint32(input.FPS),
		KeepAudio: input.KeepAudio, NoTransitions: input.NoTransitions,
		ClipDurationSec: input.ClipDurationSec, Transitions: wireTransitions,
		NoEffects: input.NoEffects, EffectPaths: wireEffects, OverlayOpacity: input.OverlayOpacity,
		KeyframeInterval: uint32(input.KeyframeInterval),
	})
	if err != nil {
		return stockpipeline.RenderResult{}, err
	}
	return stockpipeline.RenderResult{UsedFastPath: input.NoTransitions && input.NoEffects}, nil
}

var _ stockpipeline.StockRenderer = (*StockRenderer)(nil)

func durationFromSeconds(seconds float64) time.Duration {
	return time.Duration(seconds * float64(time.Second))
}
