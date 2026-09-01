package rustexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
	filesystem "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
)

// TestStockRustRenderPreservesClipOrder renders three visually distinct solid
// color clips (red, green, blue) through the real Rust canonical renderer and
// proves the manifest order equals the render order by sampling the midpoint
// frame of each clip in the output.
func TestStockRustRenderPreservesClipOrder(t *testing.T) {
	musclesPath := resolveMusclesBinary()
	if musclesPath == "" {
		t.Skip("pipelinegen-muscles binary not found (set VELOX_RUST_MUSCLES_PATH or build via make build-muscles)")
	}
	ffmpegPath := resolveFFmpegBinary()
	if ffmpegPath == "" {
		t.Skip("ffmpeg binary not found")
	}

	const (
		width  = 1280
		height = 720
		fps    = 30
	)
	specs := []struct {
		assetID string
		color   string
		channel int // 0=red, 1=green, 2=blue
	}{
		{assetID: "clip-red", color: "0xFF0000", channel: 0},
		{assetID: "clip-green", color: "0x00FF00", channel: 1},
		{assetID: "clip-blue", color: "0x0000FF", channel: 2},
	}

	assetsDir := t.TempDir()
	clipPaths := make([]string, len(specs))
	planSpec := make([]stockrustClip, len(specs))
	for i, s := range specs {
		clipPaths[i] = filepath.Join(assetsDir, s.assetID+".mp4")
		generateSolidColorClip(t, ffmpegPath, clipPaths[i], s.color, 60, width, height, fps)
		planSpec[i] = stockrustClip{assetID: s.assetID, durationUS: 2_000_000, frameCount: 60}
	}

	plan := compileThreeClipPlan(t, planSpec, clipPaths, 6_000_000, fps)
	validated, err := render.ValidateRenderPlan(plan, filesystem.NewOS())
	if err != nil {
		t.Fatalf("validate canonical render plan: %v", err)
	}
	executor := newRealExecutor(t, musclesPath, ffmpegPath)
	stock := stockrustRenderer(executor, width, height, fps)
	if err := stock.RenderCanonicalPlan(context.Background(), validated); err != nil {
		t.Fatalf("stockrust render failed: %v", err)
	}

	// Each clip is 2s; sample 1s/3s/5s = midpoint of red/green/blue.
	sample := []struct {
		seconds float64
		want    int
	}{
		{seconds: 1.0, want: specs[0].channel},
		{seconds: 3.0, want: specs[1].channel},
		{seconds: 5.0, want: specs[2].channel},
	}
	for _, s := range sample {
		rgb := sampleFrameRGB(t, ffmpegPath, plan.OutputPath, s.seconds)
		assertDominantChannel(t, rgb, s.want, s.seconds)
	}
}

// TestStockRustRenderUsesExactSourceRanges certifies that the canonical
// renderer consumes exactly the requested source windows, not the whole file.
// A single 20s source is split into four grey zones; the plan asks for
// source[2..5] and source[10..14] only. The 7s output must reproduce the
// source colour at the corresponding source times, proving source_in offsets
// are honoured for both scenes.
func TestStockRustRenderUsesExactSourceRanges(t *testing.T) {
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
		width  = 1280
		height = 720
		fps    = 30
	)
	sourcePath := filepath.Join(t.TempDir(), "source-20s.mp4")
	generateZoneSource(t, ffmpegPath, sourcePath, width, height, fps)
	if got := ffprobeFrameCount(t, ffprobePath, sourcePath); got != 600 {
		t.Fatalf("zone source frame count = %d, want 600", got)
	}

	// One manifest asset, two timeline scenes with explicit source windows.
	timeline := audio.CanonicalTimeline{
		Version:    audio.TimelineVersion,
		DurationUS: 7_000_000,
		Segments: []audio.TimelineSegment{
			{
				ID: "scene-0", Index: 0, TimelineStartUS: 0, DurationUS: 3_000_000,
				Video: audio.VideoSegment{AssetID: "src", SourceInUS: 2_000_000, SourceDurationUS: 3_000_000},
				Audio: audio.AudioIntent{Mode: audio.AudioSilence},
			},
			{
				ID: "scene-1", Index: 1, TimelineStartUS: 3_000_000, DurationUS: 4_000_000,
				Video: audio.VideoSegment{AssetID: "src", SourceInUS: 10_000_000, SourceDurationUS: 4_000_000},
				Audio: audio.AudioIntent{Mode: audio.AudioSilence},
			},
		},
	}
	contents, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(contents)
	plan, err := render.Compile(render.CompileInput{
		JobID: "stockrust-source-ranges", Revision: "generation.v1",
		OutputPath: filepath.Join(t.TempDir(), "stockrust-ranges-output.mp4"),
		FrameRate:  audio.IntegerFrameRate(fps), Timeline: timeline,
		Manifest: []render.AssetManifestEntry{{AssetID: "src", Path: sourcePath, SHA256: hex.EncodeToString(sum[:]), FrameCount: 600}},
	})
	if err != nil {
		t.Fatal(err)
	}
	validated, err := render.ValidateRenderPlan(plan, filesystem.NewOS())
	if err != nil {
		t.Fatalf("validate canonical render plan: %v", err)
	}
	executor := newRealExecutor(t, musclesPath, ffmpegPath)
	stock := stockrustRenderer(executor, width, height, fps)
	if err := stock.RenderCanonicalPlan(context.Background(), validated); err != nil {
		t.Fatalf("stockrust render failed: %v", err)
	}

	// The output must be 7s (210 frames), not the 20s source.
	if got := ffprobeFrameCount(t, ffprobePath, plan.OutputPath); got != 210 {
		t.Fatalf("output frame count = %d, want 210 (7s at 30fps)", got)
	}

	// Reference colours from the source at the expected source times.
	scene1Ref := sampleFrameRGB(t, ffmpegPath, sourcePath, 3.5)  // source 2..5 window
	scene2Ref := sampleFrameRGB(t, ffmpegPath, sourcePath, 12.0) // source 10..14 window
	wrong1 := sampleFrameRGB(t, ffmpegPath, sourcePath, 0.5)     // what source_in=0 would show
	wrong2 := sampleFrameRGB(t, ffmpegPath, sourcePath, 5.0)     // what source_in=0 for scene2 would show

	scene1Out := sampleFrameRGB(t, ffmpegPath, plan.OutputPath, 1.5)
	scene2Out := sampleFrameRGB(t, ffmpegPath, plan.OutputPath, 5.0)

	assertRGBClose(t, "scene 1 (source 2..5)", scene1Out, scene1Ref, 25)
	assertRGBClose(t, "scene 2 (source 10..14)", scene2Out, scene2Ref, 25)
	if rgbDistance(scene1Out, wrong1) < 50 {
		t.Fatalf("scene 1 used the wrong source window: output %v ≈ source[0.5s] %v (source_in must be 2s, not 0s)", scene1Out, wrong1)
	}
	if rgbDistance(scene2Out, wrong2) < 50 {
		t.Fatalf("scene 2 used the wrong source window: output %v ≈ source[5.0s] %v (source_in must be 10s, not 0s)", scene2Out, wrong2)
	}
}

func stockrustRenderer(executor *Executor, width, height, fps int) *StockRenderer {
	return &StockRenderer{
		client: NewClientWithExecutor(executor, nil),
		policy: mediaexec.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23},
		profile: mediaexec.VideoProfile{
			Width: width, Height: height, FPSNum: fps, FPSDen: 1, KeyframeInterval: 60,
			AudioCodec: "aac", AudioBitrate: "128k", SampleRate: 48000, Channels: 2,
		},
	}
}

func generateSolidColorClip(t *testing.T, ffmpeg, path, color string, frames, width, height, fps int) {
	t.Helper()
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", fmt.Sprintf("color=c=%s:size=%dx%d:rate=%d", color, width, height, fps),
		"-frames:v", strconv.Itoa(frames),
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-an", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate solid color clip %s: %v: %s", path, err, out)
	}
}

// generateZoneSource produces a 20s (600-frame) source whose grey level steps
// per second so each source window is visually distinguishable:
//
//	0–2s   lum 0    (black)
//	2–10s  lum 80
//	10–14s lum 160
//	14–20s lum 240
func generateZoneSource(t *testing.T, ffmpeg, path string, width, height, fps int) {
	t.Helper()
	expr := "geq=lum='if(lt(N,60),0,if(lt(N,300),80,if(lt(N,420),160,240)))':cb='128':cr='128'"
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", fmt.Sprintf("color=black:size=%dx%d:rate=%d", width, height, fps),
		"-frames:v", "600",
		"-vf", expr,
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-g", "60", "-an", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate zone source %s: %v: %s", path, err, out)
	}
}

// sampleFrameRGB decodes a single frame at the requested timestamp, downsamples
// the whole frame to 1×1, and returns its averaged RGB triple.
func sampleFrameRGB(t *testing.T, ffmpeg, path string, seconds float64) [3]int {
	t.Helper()
	cmd := exec.Command(ffmpeg, "-v", "error", "-ss", fmt.Sprintf("%.3f", seconds), "-i", path,
		"-frames:v", "1", "-vf", "scale=1:1", "-f", "rawvideo", "-pix_fmt", "rgb24", "-")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("sample frame at %.3fs from %s: %v", seconds, path, err)
	}
	if len(out) != 3 {
		t.Fatalf("sample frame at %.3fs from %s: got %d bytes, want 3", seconds, path, len(out))
	}
	return [3]int{int(out[0]), int(out[1]), int(out[2])}
}

func rgbDistance(a, b [3]int) int {
	d := 0
	for i := range a {
		diff := a[i] - b[i]
		if diff < 0 {
			diff = -diff
		}
		if diff > d {
			d = diff
		}
	}
	return d
}

func assertRGBClose(t *testing.T, label string, got, want [3]int, tolerance int) {
	t.Helper()
	if d := rgbDistance(got, want); d > tolerance {
		t.Fatalf("%s: colour %v not within %d of expected %v (distance %d)", label, got, tolerance, want, d)
	}
}

func assertDominantChannel(t *testing.T, c [3]int, want int, at float64) {
	t.Helper()
	names := []string{"red", "green", "blue"}
	dominant := 0
	for i := 1; i < 3; i++ {
		if c[i] > c[dominant] {
			dominant = i
		}
	}
	if dominant != want {
		t.Fatalf("frame at %.1fs: colour %v dominant channel is %s, want %s", at, c, names[dominant], names[want])
	}
	for i := 0; i < 3; i++ {
		if i == want {
			continue
		}
		if c[want]-c[i] < 40 {
			t.Fatalf("frame at %.1fs: colour %v channel %s not clearly dominant over %s", at, c, names[want], names[i])
		}
	}
}
