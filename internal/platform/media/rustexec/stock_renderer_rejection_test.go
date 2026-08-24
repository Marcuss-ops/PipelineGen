package rustexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
)

// compileStockPlan builds a valid, sealed canonical render plan whose single
// manifest asset is a real on-disk file, plus the path of that file so tests
// can tamper with or remove it after validation.
func compileStockPlan(t *testing.T) (render.RenderPlan, string) {
	t.Helper()
	clipPath := t.TempDir() + "/clip.mp4"
	contents := []byte("canonical clip bytes")
	if err := os.WriteFile(clipPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(contents)
	timeline := audio.CanonicalTimeline{
		Version:    audio.TimelineVersion,
		DurationUS: 1_000_000,
		Segments: []audio.TimelineSegment{{
			ID: "scene", Index: 0, DurationUS: 1_000_000,
			Video: audio.VideoSegment{AssetID: "clip", SourceInUS: 0, SourceDurationUS: 1_000_000},
			Audio: audio.AudioIntent{Mode: audio.AudioSilence},
		}},
	}
	plan, err := render.Compile(render.CompileInput{
		JobID: "job-1", Revision: "generation.v1", OutputPath: t.TempDir() + "/final.mp4", FrameRate: audio.IntegerFrameRate(30),
		Timeline: timeline,
		Manifest: []render.AssetManifestEntry{{AssetID: "clip", Path: clipPath, SHA256: hex.EncodeToString(sum[:]), FrameCount: 30}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan, clipPath
}

// compileStockPlanWithFinalAudio builds the same plan but with a certified
// copy-eligible final audio asset, returning the audio path for assertions.
func compileStockPlanWithFinalAudio(t *testing.T) (render.RenderPlan, string) {
	t.Helper()
	videoPath := t.TempDir() + "/clip.mp4"
	audioPath := t.TempDir() + "/final_audio.m4a"
	videoBytes := []byte("canonical clip bytes")
	audioBytes := []byte("canonical final audio")
	if err := os.WriteFile(videoPath, videoBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(audioPath, audioBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	videoHash := sha256.Sum256(videoBytes)
	audioHash := sha256.Sum256(audioBytes)
	timeline := audio.CanonicalTimeline{
		Version:    audio.TimelineVersion,
		DurationUS: 1_000_000,
		Segments: []audio.TimelineSegment{{
			ID: "scene", Index: 0, DurationUS: 1_000_000,
			Video: audio.VideoSegment{AssetID: "clip", SourceInUS: 0, SourceDurationUS: 1_000_000},
			Audio: audio.AudioIntent{Mode: audio.AudioSilence},
		}},
	}
	plan, err := render.Compile(render.CompileInput{
		JobID: "job-audio", Revision: "generation.v1", OutputPath: t.TempDir() + "/final.mp4", FrameRate: audio.IntegerFrameRate(30),
		Timeline: timeline,
		FinalAudio: &render.FinalAudioAsset{
			AssetID: "final-audio", AssetKind: "final_audio", Strategy: string(audio.FinalAudioCopy),
			Path: audioPath, SHA256: hex.EncodeToString(audioHash[:]), PlanSHA256: strings.Repeat("a", 64),
			AudioContractVersion: audio.AudioContractVersion, AudioPlanVersion: audio.AudioPlanVersion,
			Codec: "aac", Profile: "LC", SampleRate: 48000, Channels: 2, ChannelLayout: "stereo",
			DurationMS: 1000, StartPTS: 0, SizeBytes: int64(len(audioBytes)), FinalMix: true, CopyEligible: true,
		},
		Manifest: []render.AssetManifestEntry{{AssetID: "clip", Path: videoPath, SHA256: hex.EncodeToString(videoHash[:]), FrameCount: 30}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan, audioPath
}

func stockRendererWith(runner commandRunner) *StockRenderer {
	client := NewClient("muscles", "ffmpeg", nil)
	client.runner = runner
	return &StockRenderer{client: client, policy: mediaexec.EncoderPolicy{Codec: "h264_nvenc", Preset: "p1", CRF: 23}, profile: mediaexec.VideoProfile{}.WithDefaults()}
}

func validatedStockPlan(t *testing.T) render.ValidatedRenderPlan {
	t.Helper()
	plan, _ := compileStockPlan(t)
	validated, err := render.ValidateRenderPlan(plan, filesystem.NewOS())
	if err != nil {
		t.Fatal(err)
	}
	return validated
}

func TestStockRenderer_RejectsTamperedManifestBeforeRust(t *testing.T) {
	plan, clipPath := compileStockPlan(t)
	validated, err := render.ValidateRenderPlan(plan, filesystem.NewOS())
	if err != nil {
		t.Fatal(err)
	}
	// Tamper the physical manifest asset AFTER validation closes the
	// replacement window the final re-check inside RenderCanonicalPlan guards.
	if err := os.WriteFile(clipPath, []byte("tampered bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	if err := stockRendererWith(runner).RenderCanonicalPlan(context.Background(), validated); err == nil {
		t.Fatal("tampered manifest must be rejected before Rust execution")
	}
	if len(runner.inputs) != 0 {
		t.Fatalf("Rust must not be invoked for a tampered manifest, got %d calls", len(runner.inputs))
	}
}

func TestStockRenderer_RejectsTamperedPlanHashBeforeRust(t *testing.T) {
	plan, clipPath := compileStockPlan(t)
	plan.PlanSHA256 = strings.Repeat("f", 64)
	if _, err := render.ValidateRenderPlan(plan, filesystem.NewOS()); err == nil {
		t.Fatal("tampered plan hash must be rejected before handoff")
	} else if !errors.Is(err, render.ErrPlanDrift) {
		t.Fatalf("tampered plan hash error = %v, want ErrPlanDrift", err)
	}
	// The transport envelope re-validates the sealed render_plan, so a
	// tampered hash is also rejected at the last Go boundary before Rust.
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	req := request{Version: ProtocolVersion, Operation: OperationRenderStock, OutputPath: "/tmp/final.mp4", InputPaths: []string{clipPath}, RenderPlan: planJSON}
	if err := req.Validate(); err == nil {
		t.Fatal("transport must reject a render_stock request carrying a tampered plan hash")
	}
}

func TestStockRenderer_RejectsTamperedManifestHashBeforeRust(t *testing.T) {
	plan, clipPath := compileStockPlan(t)
	plan.ManifestSHA256 = strings.Repeat("e", 64)
	if _, err := render.ValidateRenderPlan(plan, filesystem.NewOS()); err == nil {
		t.Fatal("tampered manifest hash must be rejected before handoff")
	} else if !errors.Is(err, render.ErrManifestDrift) {
		t.Fatalf("tampered manifest hash error = %v, want ErrManifestDrift", err)
	}
	// The transport envelope re-validates the sealed render_plan, so the
	// tampered manifest hash is also rejected at the last Go boundary.
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	req := request{Version: ProtocolVersion, Operation: OperationRenderStock, OutputPath: "/tmp/final.mp4", InputPaths: []string{clipPath}, RenderPlan: planJSON}
	if err := req.Validate(); err == nil {
		t.Fatal("transport must reject a render_stock request carrying a tampered manifest hash")
	}
}

func TestStockRenderer_RejectsTamperedAssetHashBeforeRust(t *testing.T) {
	plan, clipPath := compileStockPlan(t)
	// Tamper the per-asset SHA256 inside the manifest; the sealed manifest hash
	// no longer matches, so the plan is rejected before any executor runs.
	plan.Manifest[0].SHA256 = strings.Repeat("d", 64)
	if _, err := render.ValidateRenderPlan(plan, filesystem.NewOS()); err == nil {
		t.Fatal("tampered manifest asset hash must be rejected before handoff")
	} else if !errors.Is(err, render.ErrManifestDrift) {
		t.Fatalf("tampered asset hash error = %v, want ErrManifestDrift", err)
	}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	req := request{Version: ProtocolVersion, Operation: OperationRenderStock, OutputPath: "/tmp/final.mp4", InputPaths: []string{clipPath}, RenderPlan: planJSON}
	if err := req.Validate(); err == nil {
		t.Fatal("transport must reject a render_stock request carrying a tampered manifest asset hash")
	}
}

func TestStockRenderer_RejectsMissingInputFileBeforeRust(t *testing.T) {
	plan, clipPath := compileStockPlan(t)
	validated, err := render.ValidateRenderPlan(plan, filesystem.NewOS())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(clipPath); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	if err := stockRendererWith(runner).RenderCanonicalPlan(context.Background(), validated); err == nil {
		t.Fatal("missing manifest input file must be rejected before Rust execution")
	}
	if len(runner.inputs) != 0 {
		t.Fatalf("Rust must not be invoked when an input file is missing, got %d calls", len(runner.inputs))
	}
}

func TestStockRenderer_RejectsEmptyManifest(t *testing.T) {
	// An audio-only timeline compiles to a plan with an empty manifest and no
	// video segments; the validator must reject it before any executor runs.
	timeline := audio.CanonicalTimeline{
		Version:    audio.TimelineVersion,
		DurationUS: 1_000_000,
		Segments: []audio.TimelineSegment{{
			ID: "scene", Index: 0, DurationUS: 1_000_000,
			Audio: audio.AudioIntent{Mode: audio.AudioSilence},
		}},
	}
	plan, err := render.Compile(render.CompileInput{
		JobID: "job-empty", Revision: "rev-1", OutputPath: t.TempDir() + "/final.mp4", FrameRate: audio.IntegerFrameRate(30),
		Timeline: timeline,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Manifest) != 0 {
		t.Fatalf("expected empty manifest, got %d entries", len(plan.Manifest))
	}
	if _, err := render.ValidateRenderPlan(plan, filesystem.NewOS()); err == nil {
		t.Fatal("empty manifest render plan must be rejected before handoff")
	}
}

func TestStockRenderer_RejectsUnresolvedTransition(t *testing.T) {
	runner := &recordingRunner{}
	_, err := stockRendererWith(runner).Render(context.Background(), stockpipeline.RenderRequest{
		InputPaths: []string{"a.mp4", "b.mp4"}, OutputPath: "out.mp4",
		NoTransitions: false, Transitions: nil,
		NoEffects: true,
	})
	if err == nil || !strings.Contains(err.Error(), "transitions must be resolved by Go") {
		t.Fatalf("Render() error = %v, want unresolved transitions rejection", err)
	}
	if len(runner.inputs) != 0 {
		t.Fatalf("Rust must not be invoked for unresolved transitions, got %d calls", len(runner.inputs))
	}
}

func TestStockRenderer_RejectsUnresolvedEffect(t *testing.T) {
	runner := &recordingRunner{}
	_, err := stockRendererWith(runner).Render(context.Background(), stockpipeline.RenderRequest{
		InputPaths: []string{"a.mp4"}, OutputPath: "out.mp4",
		NoTransitions: true,
		NoEffects:     false, EffectPaths: nil,
	})
	if err == nil || !strings.Contains(err.Error(), "effect paths must be resolved by Go") {
		t.Fatalf("Render() error = %v, want unresolved effects rejection", err)
	}
	if len(runner.inputs) != 0 {
		t.Fatalf("Rust must not be invoked for unresolved effects, got %d calls", len(runner.inputs))
	}
}

func TestStockRenderer_UsesCanonicalEncoderPolicy(t *testing.T) {
	validated := validatedStockPlan(t)
	runner := &fakeRunner{stdout: []byte(`{"ok":true,"operation":"render_stock"}`)}
	client := NewClient("muscles", "ffmpeg", nil)
	client.runner = runner
	stock := &StockRenderer{client: client, policy: mediaexec.EncoderPolicy{Codec: "h264_nvenc", Preset: "p1", CRF: 21}, profile: mediaexec.VideoProfile{}.WithDefaults()}
	if err := stock.RenderCanonicalPlan(context.Background(), validated); err != nil {
		t.Fatal(err)
	}
	var sent request
	if err := json.Unmarshal(runner.input, &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Codec != "h264_nvenc" || sent.Preset != "p1" || sent.CRF != 21 {
		t.Fatalf("canonical encoder policy not transported: codec=%q preset=%q crf=%d", sent.Codec, sent.Preset, sent.CRF)
	}
}

func TestStockRenderer_FinalAudioUsesMuxAudioCopy(t *testing.T) {
	plan, audioPath := compileStockPlanWithFinalAudio(t)
	validated, err := render.ValidateRenderPlan(plan, filesystem.NewOS())
	if err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	if err := stockRendererWith(runner).RenderCanonicalPlan(context.Background(), validated); err != nil {
		t.Fatal(err)
	}
	if len(runner.inputs) != 2 {
		t.Fatalf("expected render_stock + mux_audio_copy, got %d calls", len(runner.inputs))
	}
	var second request
	if err := json.Unmarshal(runner.inputs[1], &second); err != nil {
		t.Fatal(err)
	}
	if second.Operation != OperationMuxAudioCopy || len(second.InputPaths) != 2 || second.InputPaths[1] != audioPath {
		t.Fatalf("final audio must be muxed via mux_audio_copy: %+v", second)
	}
}

func TestStockRenderer_FinalAudioNeverReencodesAudio(t *testing.T) {
	plan, _ := compileStockPlanWithFinalAudio(t)
	validated, err := render.ValidateRenderPlan(plan, filesystem.NewOS())
	if err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	if err := stockRendererWith(runner).RenderCanonicalPlan(context.Background(), validated); err != nil {
		t.Fatal(err)
	}
	if len(runner.inputs) != 2 {
		t.Fatalf("expected exactly render_stock then mux_audio_copy, got %d calls", len(runner.inputs))
	}
	var first request
	if err := json.Unmarshal(runner.inputs[0], &first); err != nil {
		t.Fatal(err)
	}
	if first.Operation != OperationRenderStock || first.KeepAudio {
		t.Fatalf("render_stock must strip audio (KeepAudio=false), got %+v", first)
	}
	for i, raw := range runner.inputs {
		var req request
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Fatal(err)
		}
		if req.Operation == OperationRenderAudioPlan {
			t.Fatalf("audio re-encode operation must never appear (call %d)", i)
		}
	}
}

// intermediateRemovalRunner simulates a real render_stock that writes the
// intermediate video file, then fails the mux step so the deferred cleanup in
// RenderCanonicalPlan must remove the intermediate.
type intermediateRemovalRunner struct {
	inputs  [][]byte
	created string
	muxFail bool
}

func (r *intermediateRemovalRunner) Run(_ context.Context, _ string, input []byte) ([]byte, []byte, error) {
	r.inputs = append(r.inputs, append([]byte(nil), input...))
	var req request
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, nil, err
	}
	if req.Operation == OperationRenderStock {
		if err := os.WriteFile(req.OutputPath, []byte("intermediate video"), 0o600); err != nil {
			return nil, nil, err
		}
		r.created = req.OutputPath
		return []byte(`{"ok":true,"operation":"render_stock"}`), nil, nil
	}
	if r.muxFail {
		return nil, nil, errors.New("mux failed")
	}
	return []byte(`{"ok":true,"operation":"mux_audio_copy"}`), nil, nil
}

func TestStockRenderer_RemovesIntermediateVideoOnFailure(t *testing.T) {
	plan, _ := compileStockPlanWithFinalAudio(t)
	validated, err := render.ValidateRenderPlan(plan, filesystem.NewOS())
	if err != nil {
		t.Fatal(err)
	}
	runner := &intermediateRemovalRunner{muxFail: true}
	if err := stockRendererWith(runner).RenderCanonicalPlan(context.Background(), validated); err == nil {
		t.Fatal("mux failure must propagate from RenderCanonicalPlan")
	}
	if runner.created == "" {
		t.Fatal("render_stock step did not produce an intermediate file")
	}
	if _, statErr := os.Stat(runner.created); !os.IsNotExist(statErr) {
		t.Fatalf("intermediate video %q must be removed on failure (stat err=%v)", runner.created, statErr)
	}
}
