package rustexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
)

// TestStockRustExactFrameCount certifies that the canonical renderer emits
// exactly the plan's resolved duration_frames for both an integer frame rate
// (30/1) and a rational frame rate (30000/1001 = 29.97). The Go FrameResolver
// owns the timestamp→frame conversion; Rust must reproduce that count verbatim
// and never reinterpret timing, so expected == plan.DurationFrames == rendered.
func TestStockRustExactFrameCount(t *testing.T) {
	musclesPath := resolveMusclesBinary()
	if musclesPath == "" {
		t.Skip("pipelinegen-muscles binary not found (set VELOX_RUST_MUSCLES_PATH or build via make build-muscles)")
	}
	ffmpegPath := resolveFFmpegBinary()
	if ffmpegPath == "" {
		t.Skip("ffmpeg binary not found")
	}
	ffprobePath := resolveFFprobeBinary(ffmpegPath)
	if ffprobePath == "" {
		t.Skip("ffprobe binary not found")
	}

	const (
		width      = 1280
		height     = 720
		durationUS = 10_000_000 // 10s
	)
	cases := []struct {
		name         string
		rate         audio.FrameRate
		expectedRate string
	}{
		{"30fps", audio.IntegerFrameRate(30), "30/1"},
		{"29.97fps", audio.FrameRate{Numerator: 30000, Denominator: 1001}, "30000/1001"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The Go resolver is the single source of truth for the expected
			// frame count. The canonical plan's duration_frames must equal it.
			resolver, err := audio.NewFrameResolver(tc.rate)
			if err != nil {
				t.Fatal(err)
			}
			expectedFrames, err := resolver.FrameAt(durationUS)
			if err != nil {
				t.Fatal(err)
			}

			dir := t.TempDir()
			clipPath := filepath.Join(dir, "clip.mp4")
			generateTestsrcClipAtRate(t, ffmpegPath, clipPath, int(expectedFrames), width, height, tc.rate.Numerator, tc.rate.Denominator)

			clipBytes, err := os.ReadFile(clipPath)
			if err != nil {
				t.Fatal(err)
			}
			clipSum := sha256.Sum256(clipBytes)

			timeline := audio.CanonicalTimeline{
				Version:    audio.TimelineVersion,
				DurationUS: durationUS,
				Segments: []audio.TimelineSegment{{
					ID: "scene", Index: 0, TimelineStartUS: 0, DurationUS: durationUS,
					Video: audio.VideoSegment{AssetID: "clip", SourceInUS: 0, SourceDurationUS: durationUS},
					Audio: audio.AudioIntent{Mode: audio.AudioSilence},
				}},
			}
			plan, err := render.Compile(render.CompileInput{
				JobID: "stockrust-frame-accurate-" + tc.name, Revision: "generation.v1",
				OutputPath: filepath.Join(dir, "out.mp4"),
				FrameRate:  tc.rate,
				Timeline:   timeline,
				Manifest:   []render.AssetManifestEntry{{AssetID: "clip", Path: clipPath, SHA256: hex.EncodeToString(clipSum[:]), FrameCount: expectedFrames}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if plan.DurationFrames != expectedFrames {
				t.Fatalf("plan duration_frames = %d, want resolver output %d", plan.DurationFrames, expectedFrames)
			}
			if plan.FPSNumerator != tc.rate.Numerator || plan.FPSDenominator != tc.rate.Denominator {
				t.Fatalf("plan fps = %d/%d, want %d/%d", plan.FPSNumerator, plan.FPSDenominator, tc.rate.Numerator, tc.rate.Denominator)
			}

			validated, err := render.ValidateRenderPlan(plan, filesystem.NewOS())
			if err != nil {
				t.Fatalf("validate canonical render plan: %v", err)
			}

			executor := newRealExecutor(t, musclesPath, ffmpegPath)
			stock := &StockRenderer{
				client: NewClientWithExecutor(executor, nil),
				policy: mediaexec.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23},
				profile: mediaexec.VideoProfile{
					Width: width, Height: height, FPSNum: 30, FPSDen: 1, KeyframeInterval: 60,
					AudioCodec: "aac", AudioBitrate: "128k", SampleRate: 48000, Channels: 2,
				},
			}
			if err := stock.RenderCanonicalPlan(context.Background(), validated); err != nil {
				t.Fatalf("stockrust render failed: %v", err)
			}

			// The decisive assertion: rendered frame count must equal the
			// plan's resolved duration_frames exactly (never ≈).
			renderedFrames := ffprobeFrameCount(t, ffprobePath, plan.OutputPath)
			if int64(renderedFrames) != plan.DurationFrames {
				t.Fatalf("rendered frame count = %d, want exactly %d (plan duration_frames)", renderedFrames, plan.DurationFrames)
			}

			// The rational rate must survive to the container, not be coerced
			// to the nominal integer fps.
			report := ffprobeOutput(t, ffprobePath, plan.OutputPath)
			if len(report.Streams) != 1 || report.Streams[0].RFrameRate != tc.expectedRate {
				t.Fatalf("r_frame_rate = %v, want %q (streams=%d)", report.Streams, tc.expectedRate, len(report.Streams))
			}

			if decodeErrors := fullDecode(t, ffmpegPath, plan.OutputPath); decodeErrors != "" {
				t.Fatalf("full decode produced errors:\n%s", decodeErrors)
			}
		})
	}
}

// generateTestsrcClipAtRate renders an exact frame count at a rational frame
// rate so the manifest's frame_count matches the resolver's output for 30/1
// and 30000/1001 alike.
func generateTestsrcClipAtRate(t *testing.T, ffmpeg, path string, frames, width, height int, num, den int64) {
	t.Helper()
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size="+strconv.Itoa(width)+"x"+strconv.Itoa(height)+":rate="+strconv.FormatInt(num, 10)+"/"+strconv.FormatInt(den, 10),
		"-frames:v", strconv.Itoa(frames),
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-an", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate testsrc clip %s: %v: %s", path, err, out)
	}
}
