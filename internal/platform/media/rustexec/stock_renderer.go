package rustexec

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
	"go.uber.org/zap"
)

type StockRenderer struct {
	client  *Client
	policy  mediaexec.EncoderPolicy
	profile mediaexec.VideoProfile
}

func NewStockRenderer(binaryPath, ffmpegPath string, policy mediaexec.EncoderPolicy, profile mediaexec.VideoProfile, log *zap.Logger) *StockRenderer {
	return NewStockRendererWithExecutor(NewExecutor(binaryPath, ffmpegPath, log), policy, profile, log)
}

func NewStockRendererWithExecutor(executor *Executor, policy mediaexec.EncoderPolicy, profile mediaexec.VideoProfile, log *zap.Logger) *StockRenderer {
	return &StockRenderer{client: NewClientWithExecutor(executor, log), policy: policy, profile: profile}
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
	codec, preset, crf, err := (&VideoProcessor{client: r.client, policy: r.policy, profile: r.profile}).policyFor(input.Codec, input.Preset, input.CRF)
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
	profile := r.profile
	if profile == (mediaexec.VideoProfile{}) {
		profile = mediaexec.VideoProfile{Width: input.Width, Height: input.Height, FPSNum: input.FPSNum, FPSDen: input.FPSDen, KeyframeInterval: input.KeyframeInterval}
	}
	if err := validateResolvedProfile(profile); err != nil {
		return stockpipeline.RenderResult{}, err
	}
	_, err = r.client.call(ctx, request{
		Operation: "render_stock", OutputPath: input.OutputPath, InputPaths: input.InputPaths,
		Codec: codec, Preset: preset, CRF: crf,
		Width: uint32(profile.Width), Height: uint32(profile.Height), FPSNum: uint32(profile.FPSNum), FPSDen: uint32(profile.FPSDen),
		KeyframeInterval: uint32(profile.KeyframeInterval),
		AudioCodec:       profile.AudioCodec, AudioBitrate: profile.AudioBitrate,
		SampleRate: uint32(profile.SampleRate), Channels: uint32(profile.Channels),
		KeepAudio: input.KeepAudio, NoTransitions: input.NoTransitions, ClipDurationSec: input.ClipDurationSec, Transitions: wireTransitions,
		NoEffects: input.NoEffects, EffectPaths: wireEffects, OverlayOpacity: input.OverlayOpacity,
	})
	if err != nil {
		return stockpipeline.RenderResult{}, err
	}
	return stockpipeline.RenderResult{UsedFastPath: input.NoTransitions && input.NoEffects}, nil
}

// RenderCanonicalPlan is the Velox/media-executor boundary for generation
// output. It validates the sealed plan and every manifest file before any
// Rust process is invoked, then sends the exact plan JSON as an audit field.
// The executor receives integer frame ranges and never performs timestamp
// rounding or asset selection.
func (r *StockRenderer) RenderCanonicalPlan(ctx context.Context, validated render.ValidatedRenderPlan) error {
	plan := validated.Plan()
	// Re-check physical identity immediately before invoking Velox. The
	// validator mints the typed handoff, while this final check closes the
	// replacement window between validation and process execution.
	if err := plan.ValidateManifestFiles(filesystem.NewOS()); err != nil {
		return fmt.Errorf("canonical render plan changed after validation: %w", err)
	}
	planJSON, err := json.Marshal(validated)
	if err != nil {
		return fmt.Errorf("marshal canonical render plan: %w", err)
	}
	codec, preset, crf, err := (&VideoProcessor{client: r.client, policy: r.policy, profile: r.profile}).policyFor("", "", 0)
	if err != nil {
		return err
	}
	profile := r.profile
	if profile == (mediaexec.VideoProfile{}) {
		profile = mediaexec.VideoProfile{}.WithDefaults()
	}
	inputs := make([]string, 0, len(plan.Manifest))
	for _, entry := range plan.Manifest {
		inputs = append(inputs, entry.Path)
	}
	if len(inputs) == 0 {
		return fmt.Errorf("canonical render plan rejected: no video inputs")
	}
	videoOutput := plan.OutputPath
	if plan.FinalAudio != nil {
		videoOutput = plan.OutputPath + ".video.mp4"
		// Always remove the intermediate video, including when the render
		// process fails before the mux step starts.
		defer os.Remove(videoOutput)
	}
	_, err = r.client.call(ctx, request{
		Operation:        OperationRenderStock,
		OutputPath:       videoOutput,
		InputPaths:       inputs,
		Codec:            codec,
		Preset:           preset,
		CRF:              crf,
		Width:            uint32(profile.Width),
		Height:           uint32(profile.Height),
		FPSNum:           uint32(plan.FPSNumerator),
		FPSDen:           uint32(plan.FPSDenominator),
		KeyframeInterval: uint32(profile.KeyframeInterval),
		AudioCodec:       profile.AudioCodec,
		AudioBitrate:     profile.AudioBitrate,
		SampleRate:       uint32(profile.SampleRate),
		Channels:         uint32(profile.Channels),
		KeepAudio:        false,
		NoTransitions:    true,
		NoEffects:        true,
		RenderPlan:       planJSON,
	})
	if err != nil {
		return fmt.Errorf("execute canonical render plan: %w", err)
	}
	if plan.FinalAudio != nil {
		_, err = r.client.call(ctx, request{Operation: OperationMuxAudioCopy, InputPaths: []string{videoOutput, plan.FinalAudio.Path}, OutputPath: plan.OutputPath})
		if err != nil {
			return fmt.Errorf("mux canonical final audio copy: %w", err)
		}
	}
	return nil
}

// SetObservedExecutor attaches the single measurement point decorator to
// this renderer's client (every operation it runs is then measured once).
// Nil-safe; nil disables per-operation measurement.
func (r *StockRenderer) SetObservedExecutor(observed *ObservedExecutor) {
	if r != nil {
		r.client.SetObservedExecutor(observed)
	}
}

var _ stockpipeline.StockRenderer = (*StockRenderer)(nil)

func durationFromSeconds(seconds float64) time.Duration {
	return time.Duration(seconds * float64(time.Second))
}
