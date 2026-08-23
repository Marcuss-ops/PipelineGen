package rustexec

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediaexec"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
)

// clipRenderPlanWithFiles builds a sealed plan whose artifacts are real
// files (the adapter and the transport verify them), covering source +
// asset background + watermark + burn subtitles.
func clipRenderPlanWithFiles(t *testing.T) (cliprender.ClipRenderPlanV1, string) {
	t.Helper()
	dir := t.TempDir()
	write := func(name string) (string, string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("artifact-"+name), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		// Content hashes are verified by Rust (render_clip re-audits every
		// artifact with sha256sum); the Go adapter only checks existence.
		return path, strings.Repeat("a", 64)
	}
	sourcePath, sourceSHA := write("source.mp4")
	bgPath, bgSHA := write("bg.png")
	wmPath, wmSHA := write("watermark.png")
	subPath, subSHA := write("subtitles.ass")
	outputPath := filepath.Join(dir, "rendered-clip.mp4")
	if err := os.WriteFile(outputPath, []byte("rendered"), 0o600); err != nil {
		t.Fatalf("write output: %v", err)
	}

	plan, err := cliprender.Compile(cliprender.CompileInput{
		RunID: "job-1",
		Source: &cliprender.MaterializedAsset{
			AssetID:   "asset-src",
			LocalPath: sourcePath,
			SHA256:    sourceSHA,
		},
		Background: &cliprender.MaterializedAsset{
			AssetID:   "asset-bg",
			LocalPath: bgPath,
			SHA256:    bgSHA,
		},
		Watermark: &cliprender.MaterializedAsset{
			AssetID:   "asset-wm",
			LocalPath: wmPath,
			SHA256:    wmSHA,
		},
		WatermarkSpec: &cliprender.WatermarkSpec{
			Position: cliprender.PositionTopRight,
			Opacity:  0.85,
			MarginPX: 40,
		},
		Subtitles: &cliprender.SubtitleArtifact{
			LocalPath: subPath,
			SHA256:    subSHA,
			Mode:      cliprender.SubtitlesModeBurn,
			StyleID:   "shorts-v1",
		},
		Contract: &cliprender.ResolvedContract{
			ContractID:   cliprender.OutputContractVeloxAssemblyReadyV1,
			Container:    "mp4",
			VideoCodec:   "h264",
			VideoProfile: "high",
			PixelFormat:  "yuv420p",
			Width:        1080,
			Height:       1920,
			FPSNum:       60,
			FPSDen:       1,
			AudioCodec:   "aac",
			SampleRate:   48000,
			Channels:     2,
		},
		AudioMode:  cliprender.AudioModeCopyIfCompatible,
		OutputPath: outputPath,
	})
	if err != nil {
		t.Fatalf("compile plan: %v", err)
	}
	return plan, sourcePath
}

func newTestClipRenderer(runner commandRunner) *ClipRenderer {
	client := NewClient("muscles", "ffmpeg", nil)
	client.runner = runner
	return &ClipRenderer{
		client: client,
		policy: mediaexec.EncoderPolicy{Codec: "h264_nvenc", Preset: "p1", CRF: 23},
		profile: mediaexec.VideoProfile{
			Width: 1080, Height: 1920, FPSNum: 60, FPSDen: 1, KeyframeInterval: 120,
			AudioCodec: "aac", AudioBitrate: "128k", SampleRate: 48000, Channels: 2,
		},
	}
}

func TestClipRenderer_TransportsSealedPlanWithPolicy(t *testing.T) {
	plan, sourcePath := clipRenderPlanWithFiles(t)
	runner := &fakeRunner{stdout: []byte(`{"ok":true,"operation":"render_clip","metadata":{"duration_sec":30,"width":1080,"height":1920,"fps":60,"fps_num":60,"fps_den":1,"ffmpeg_ms":1250,"audio_copy_eligible":true,"audio_encode_passes":0,"subtitle_raster_cpu":true}}`)}
	renderer := newTestClipRenderer(runner)

	result, err := renderer.RenderClip(context.Background(), plan, cliprender.BackendFFmpegFallback)
	if err != nil {
		t.Fatalf("RenderClip: %v", err)
	}
	if runner.input == nil {
		t.Fatal("render_clip was not invoked")
	}
	var sent request
	if err := json.Unmarshal(runner.input, &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Operation != OperationRenderClip {
		t.Fatalf("operation = %q, want render_clip", sent.Operation)
	}
	if sent.SourcePath != sourcePath || sent.OutputPath != plan.OutputPath {
		t.Fatalf("paths: source=%q output=%q", sent.SourcePath, sent.OutputPath)
	}
	// Encoder policy comes from the composition-root config, never the plan.
	if sent.Codec != "h264_nvenc" || sent.Preset != "p1" || sent.CRF != 23 {
		t.Fatalf("encoder policy not transported: %+v", sent)
	}
	// Geometry is transported from the plan so Rust can detect drift.
	if sent.Width != 1080 || sent.Height != 1920 || sent.FPSNum != 60 || sent.FPSDen != 1 || sent.KeyframeInterval != 120 {
		t.Fatalf("geometry: %dx%d@%d/%d ki=%d", sent.Width, sent.Height, sent.FPSNum, sent.FPSDen, sent.KeyframeInterval)
	}
	if sent.RenderBackend != string(cliprender.BackendFFmpegFallback) {
		t.Fatalf("render_backend = %q, want ffmpeg_fallback", sent.RenderBackend)
	}
	if len(sent.ClipPlan) == 0 || sent.ClipPlan[0] != '{' {
		t.Fatalf("clip_plan not transported")
	}
	var wire struct {
		PlanSHA256 string `json:"plan_sha256"`
		RunID      string `json:"run_id"`
	}
	if err := json.Unmarshal(sent.ClipPlan, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.PlanSHA256 != plan.PlanSHA256 || wire.RunID != "job-1" {
		t.Fatalf("unexpected clip plan wire contract: %+v", wire)
	}
	// Typed result: copy-eligible + zero encode passes + CPU subtitle stage.
	if result.AudioCopyEligible == nil || !*result.AudioCopyEligible {
		t.Fatalf("AudioCopyEligible = %v, want true", result.AudioCopyEligible)
	}
	if result.AudioEncodePasses == nil || *result.AudioEncodePasses != 0 {
		t.Fatalf("AudioEncodePasses = %v, want 0", result.AudioEncodePasses)
	}
	if result.SubtitleRasterCPU == nil || !*result.SubtitleRasterCPU {
		t.Fatalf("SubtitleRasterCPU = %v, want true", result.SubtitleRasterCPU)
	}
	if result.OutputPath != plan.OutputPath || result.SizeBytes == 0 || result.FFmpegMS != 1250 {
		t.Fatalf("result output facts: %+v", result)
	}
}

// TestClipRenderer_RejectsMissingArtifact verifies the adapter fails closed
// BEFORE the Rust call when a referenced artifact is missing from disk.
func TestClipRenderer_RejectsMissingArtifact(t *testing.T) {
	plan, sourcePath := clipRenderPlanWithFiles(t)
	// Remove the source AFTER sealing: the plan stays valid (hash intact)
	// but the artifact is gone — the adapter's existence gate must fire.
	if err := os.Remove(sourcePath); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	renderer := newTestClipRenderer(runner)

	_, err := renderer.RenderClip(context.Background(), plan, cliprender.BackendFFmpegFallback)
	if err == nil || !strings.Contains(err.Error(), "source") {
		t.Fatalf("expected artifact failure, got %v", err)
	}
	if runner.input != nil {
		t.Fatal("Rust must not be invoked with missing artifacts")
	}
}

// TestClipRenderer_RejectsTamperedPlan verifies a plan mutated after sealing
// is rejected before any Rust process starts (drift gate at the adapter).
func TestClipRenderer_RejectsTamperedPlan(t *testing.T) {
	plan, _ := clipRenderPlanWithFiles(t)
	plan.Output.Height = 1080 // mutate after seal
	runner := &fakeRunner{}
	renderer := newTestClipRenderer(runner)

	_, err := renderer.RenderClip(context.Background(), plan, cliprender.BackendFFmpegFallback)
	if err == nil || !strings.Contains(err.Error(), "validation failed") {
		t.Fatalf("expected drift rejection, got %v", err)
	}
	if runner.input != nil {
		t.Fatal("Rust must not be invoked with a tampered plan")
	}
}

// TestClipRenderer_FailsClosedOnEmptyPolicy verifies the encoder policy gate:
// no codec/preset/quality from the composition root → typed error, no call.
func TestClipRenderer_FailsClosedOnEmptyPolicy(t *testing.T) {
	plan, _ := clipRenderPlanWithFiles(t)
	client := NewClient("muscles", "ffmpeg", nil)
	client.runner = &fakeRunner{}
	renderer := &ClipRenderer{client: client, profile: mediaexec.VideoProfile{}.WithDefaults()}

	_, err := renderer.RenderClip(context.Background(), plan, cliprender.BackendFFmpegFallback)
	if err == nil || !strings.Contains(err.Error(), "ENCODER_POLICY_REQUIRED") {
		t.Fatalf("expected encoder policy failure, got %v", err)
	}
}

// TestClipRenderer_FailsClosedOnMissingOutput verifies a successful Rust
// response without a non-empty output file is still a failure (never a
// silent success with a stub artifact).
func TestClipRenderer_FailsClosedOnMissingOutput(t *testing.T) {
	plan, _ := clipRenderPlanWithFiles(t)
	if err := os.Remove(plan.OutputPath); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{stdout: []byte(`{"ok":true,"operation":"render_clip","metadata":{"duration_sec":30,"width":1080,"height":1920,"fps":60,"fps_num":60,"fps_den":1,"ffmpeg_ms":10}}`)}
	renderer := newTestClipRenderer(runner)

	_, err := renderer.RenderClip(context.Background(), plan, cliprender.BackendFFmpegFallback)
	if err == nil || !strings.Contains(err.Error(), "output missing or empty") {
		t.Fatalf("expected output gate failure, got %v", err)
	}
}
