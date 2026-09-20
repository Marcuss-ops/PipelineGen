package rustexec

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
	pathutil "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
	"go.uber.org/zap"
)

type StockRenderer struct {
	client  *Client
	policy  mediaexec.EncoderPolicy
	profile mediaexec.VideoProfile
}

func NewStockRendererWithExecutor(executor *Executor, policy mediaexec.EncoderPolicy, profile mediaexec.VideoProfile, log *zap.Logger) *StockRenderer {
	return &StockRenderer{client: NewClientWithExecutor(executor, log), policy: policy, profile: profile}
}

// Render executes one stock compose chunk through the canonical
// render_stock contract.
//
// PR-STOCK-CANONICAL-RENDER-PLAN: the executor no longer accepts the legacy
// transitions/effect_paths envelope (it fails closed with "render_stock
// requires a canonical render_plan"), so this adapter compiles a sealed
// render.RenderPlan from the resolved request and transports it. The
// canonical executor is video-only; when the caller asked to keep audio it is
// preserved by a copy-only mux of the input's original audio track.
//
// Transitions and effect overlays have no representation in the canonical
// plan (trim/scale/fps/concat only), so a request carrying them fails closed
// with a descriptive error instead of silently dropping the operator's
// selection.
func (r *StockRenderer) Render(ctx context.Context, input stockpipeline.RenderRequest) (stockpipeline.RenderResult, error) {
	if !input.NoTransitions && len(input.Transitions) == 0 {
		return stockpipeline.RenderResult{}, fmt.Errorf("unresolved render plan: transitions must be resolved by Go")
	}
	if !input.NoEffects && len(input.EffectPaths) == 0 {
		return stockpipeline.RenderResult{}, fmt.Errorf("unresolved render plan: effect paths must be resolved by Go")
	}
	if len(input.Transitions) > 0 {
		return stockpipeline.RenderResult{}, fmt.Errorf("render_stock canonical plan cannot express transitions (resolved %d); compose must resolve to no-transitions", len(input.Transitions))
	}
	if len(input.EffectPaths) > 0 {
		return stockpipeline.RenderResult{}, fmt.Errorf("render_stock canonical plan cannot express effect overlays (resolved %d); compose must resolve to no-effects", len(input.EffectPaths))
	}
	if len(input.InputPaths) == 0 {
		return stockpipeline.RenderResult{}, fmt.Errorf("render_stock requires at least one input path")
	}
	if input.KeepAudio && len(input.InputPaths) > 1 {
		return stockpipeline.RenderResult{}, fmt.Errorf("render_stock canonical plan cannot preserve audio across %d concatenated inputs", len(input.InputPaths))
	}
	if input.OutputPath == "" {
		return stockpipeline.RenderResult{}, fmt.Errorf("render_stock output_path is required")
	}
	codec, preset, crf, err := (&VideoProcessor{client: r.client, policy: r.policy, profile: r.profile}).policyFor(input.Codec, input.Preset, input.CRF)
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
	rate := audio.FrameRate{Numerator: int64(profile.FPSNum), Denominator: int64(profile.FPSDen)}
	if _, err := audio.NewFrameResolver(rate); err != nil {
		return stockpipeline.RenderResult{}, fmt.Errorf("stock render: %w", err)
	}

	facts := make([]stockInputFacts, 0, len(input.InputPaths))
	for _, path := range input.InputPaths {
		fact, err := r.probeInput(ctx, path)
		if err != nil {
			return stockpipeline.RenderResult{}, err
		}
		facts = append(facts, fact)
	}
	// The canonical video executor always strips audio; keep it via a copy-only
	// mux when the operator requested it and the single source actually has an
	// audio stream (a silent source has nothing to preserve).
	muxAudio := input.KeepAudio && len(facts) == 1 && facts[0].HasAudio
	renderOutput := input.OutputPath
	if muxAudio {
		renderOutput = input.OutputPath + ".video.mp4"
	}

	started := time.Now()
	plan, err := r.compileStockRenderPlan(input, facts, renderOutput, rate)
	if err != nil {
		return stockpipeline.RenderResult{}, err
	}
	validated, err := render.ValidateRenderPlan(plan, pathutil.NewOS())
	if err != nil {
		return stockpipeline.RenderResult{}, fmt.Errorf("stock render: validate canonical plan: %w", err)
	}
	planJSON, err := json.Marshal(validated)
	if err != nil {
		return stockpipeline.RenderResult{}, fmt.Errorf("stock render: marshal canonical plan: %w", err)
	}
	inputPaths := make([]string, 0, len(facts))
	for _, fact := range facts {
		inputPaths = append(inputPaths, fact.Path)
	}

	if muxAudio {
		// Always remove the intermediate video, including when the render
		// process fails before the mux step starts.
		defer os.Remove(renderOutput)
	}
	_, err = r.client.call(ctx, request{
		Operation: OperationRenderStock, OutputPath: renderOutput, InputPaths: inputPaths,
		Codec: codec, Preset: preset, CRF: crf,
		Width: uint32(profile.Width), Height: uint32(profile.Height), FPSNum: uint32(profile.FPSNum), FPSDen: uint32(profile.FPSDen),
		KeyframeInterval: uint32(profile.KeyframeInterval),
		AudioCodec:       profile.AudioCodec, AudioBitrate: profile.AudioBitrate,
		SampleRate: uint32(profile.SampleRate), Channels: uint32(profile.Channels),
		KeepAudio: false, NoTransitions: true, NoEffects: true, RenderPlan: planJSON,
	})
	if err != nil {
		return stockpipeline.RenderResult{}, err
	}
	if muxAudio {
		if _, err := r.client.call(ctx, request{
			Operation:  OperationMuxAudioCopy,
			InputPaths: []string{renderOutput, facts[0].Path},
			OutputPath: input.OutputPath,
		}); err != nil {
			return stockpipeline.RenderResult{}, fmt.Errorf("stock render: preserve original audio: %w", err)
		}
	}
	// The canonical executor runs a trimmed/scaled/fps-converted
	// filter_complex concat, so this is not the concat-demuxer fast path.
	return stockpipeline.RenderResult{UsedFastPath: false, DurationMS: time.Since(started).Milliseconds()}, nil
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
	if err := plan.ValidateManifestFiles(pathutil.NewOS()); err != nil {
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
