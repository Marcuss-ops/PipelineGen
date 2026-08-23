package rustexec

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
)

// TestStockRustAppliesResolvedTransitions drives the real Rust render_stock
// legacy path with two Go-resolved transitions (clip 0 end → fadeblack,
// clip 2 end → blur). It certifies that Rust receives and applies the exact
// assignments: the output is playable H.264 with the correct resolution, frame
// rate and duration.
func TestStockRustAppliesResolvedTransitions(t *testing.T) {
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
	colors := []string{"0xFF0000", "0x00FF00", "0x0000FF", "0xFFFF00"}
	paths := make([]string, len(colors))
	for i, color := range colors {
		paths[i] = filepath.Join(t.TempDir(), fmt.Sprintf("clip-%d.mp4", i))
		generateSolidColorClip(t, ffmpegPath, paths[i], color, 60, width, height, fps)
	}
	output := filepath.Join(t.TempDir(), "transitions-output.mp4")
	stock := stockrustRenderer(newRealExecutor(t, musclesPath, ffmpegPath), width, height, fps)
	_, err := stock.Render(context.Background(), stockpipeline.RenderRequest{
		InputPaths: paths, OutputPath: output,
		Width: width, Height: height, FPSNum: fps, FPSDen: 1, KeyframeInterval: 60,
		Codec: "libx264", Preset: "veryfast", CRF: 23,
		NoTransitions: false, ClipDurationSec: 2,
		Transitions: []stockpipeline.RenderTransition{
			{ClipIndex: 0, Segment: "end", ID: "fadeblack"},
			{ClipIndex: 2, Segment: "end", ID: "blur"},
		},
		NoEffects: true,
	})
	if err != nil {
		t.Fatalf("resolved transitions render failed: %v", err)
	}

	probe := ffprobeOutput(t, ffprobePath, output)
	if len(probe.Streams) != 1 {
		t.Fatalf("expected 1 video stream, got %d", len(probe.Streams))
	}
	if s := probe.Streams[0]; s.CodecName != "h264" || s.Width != width || s.Height != height || s.RFrameRate != "30/1" {
		t.Fatalf("unexpected output profile: codec=%q %dx%d %s", s.CodecName, s.Width, s.Height, s.RFrameRate)
	}
	if frames := ffprobeFrameCount(t, ffprobePath, output); frames < 238 || frames > 242 {
		t.Fatalf("frame count = %d, want ~240 (4 clips × 2s)", frames)
	}
	if errs := fullDecode(t, ffmpegPath, output); errs != "" {
		t.Fatalf("transitions output decode errors:\n%s", errs)
	}
}

// TestStockRustAppliesResolvedEffects renders three clips and overlays a white
// effect file on clip 1 only. It certifies exact-path application: the
// affected clip is blended with the effect while the untouched clip stays
// pure, and the output decodes clean.
func TestStockRustAppliesResolvedEffects(t *testing.T) {
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
	colors := []string{"0xFF0000", "0x00FF00", "0x0000FF"}
	paths := make([]string, len(colors))
	for i, color := range colors {
		paths[i] = filepath.Join(t.TempDir(), fmt.Sprintf("clip-%d.mp4", i))
		generateSolidColorClip(t, ffmpegPath, paths[i], color, 60, width, height, fps)
	}
	effectPath := filepath.Join(t.TempDir(), "effect-white.mp4")
	generateSolidColorClip(t, ffmpegPath, effectPath, "0xFFFFFF", 60, width, height, fps)

	output := filepath.Join(t.TempDir(), "effects-output.mp4")
	stock := stockrustRenderer(newRealExecutor(t, musclesPath, ffmpegPath), width, height, fps)
	_, err := stock.Render(context.Background(), stockpipeline.RenderRequest{
		InputPaths: paths, OutputPath: output,
		Width: width, Height: height, FPSNum: fps, FPSDen: 1, KeyframeInterval: 60,
		Codec: "libx264", Preset: "veryfast", CRF: 23,
		NoTransitions:  true,
		NoEffects:      false,
		EffectPaths:    []stockpipeline.RenderEffectPath{{ClipIndex: 1, Path: effectPath}},
		OverlayOpacity: 0.5,
	})
	if err != nil {
		t.Fatalf("resolved effects render failed: %v", err)
	}

	probe := ffprobeOutput(t, ffprobePath, output)
	if s := probe.Streams[0]; s.CodecName != "h264" || s.Width != width || s.Height != height {
		t.Fatalf("unexpected output profile: codec=%q %dx%d", s.CodecName, s.Width, s.Height)
	}
	if errs := fullDecode(t, ffmpegPath, output); errs != "" {
		t.Fatalf("effects output decode errors:\n%s", errs)
	}

	// clip 0 (no effect) must stay pure red; clip 1 (white overlay at 0.5)
	// must have its green blended with white (elevated red/blue channels).
	clip0 := sampleFrameRGB(t, ffmpegPath, output, 1.0)
	assertDominantChannel(t, clip0, 0, 1.0)
	if clip0[1] > 80 || clip0[2] > 80 {
		t.Fatalf("clip 0 was affected by the overlay but must stay pure: %v", clip0)
	}
	clip1 := sampleFrameRGB(t, ffmpegPath, output, 3.0)
	assertDominantChannel(t, clip1, 1, 3.0)
	if clip1[0] < 50 || clip1[2] < 50 {
		t.Fatalf("clip 1 does not show the white overlay (red/blue not elevated): %v", clip1)
	}
}

// TestStockRustRejectsUnknownTransitionID certifies the fail-closed path: a
// Go-resolved transition with an ID outside the Rust catalog is rejected by
// Rust and no output is accepted.
func TestStockRustRejectsUnknownTransitionID(t *testing.T) {
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
	paths := make([]string, 2)
	for i, color := range []string{"0xFF0000", "0x00FF00"} {
		paths[i] = filepath.Join(t.TempDir(), fmt.Sprintf("clip-%d.mp4", i))
		generateSolidColorClip(t, ffmpegPath, paths[i], color, 60, width, height, fps)
	}
	output := filepath.Join(t.TempDir(), "bad-transition-output.mp4")
	stock := stockrustRenderer(newRealExecutor(t, musclesPath, ffmpegPath), width, height, fps)
	_, err := stock.Render(context.Background(), stockpipeline.RenderRequest{
		InputPaths: paths, OutputPath: output,
		Width: width, Height: height, FPSNum: fps, FPSDen: 1, KeyframeInterval: 60,
		Codec: "libx264", Preset: "veryfast", CRF: 23,
		NoTransitions: false, ClipDurationSec: 2,
		Transitions: []stockpipeline.RenderTransition{{ClipIndex: 0, Segment: "end", ID: "random-transition"}},
		NoEffects:   true,
	})
	if err == nil {
		t.Fatal("unknown transition ID must be rejected")
	}
	if !strings.Contains(err.Error(), "invalid resolved transition") {
		t.Fatalf("error = %v, want invalid resolved transition", err)
	}
	if _, statErr := os.Stat(output); statErr == nil {
		t.Fatal("unknown transition must not produce an output file")
	}
}

// TestStockRustRejectsMissingEffectFile certifies the fail-closed path: an
// effect assignment whose file does not exist on disk is rejected by Rust and
// no output is accepted.
func TestStockRustRejectsMissingEffectFile(t *testing.T) {
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
	paths := make([]string, 2)
	for i, color := range []string{"0xFF0000", "0x00FF00"} {
		paths[i] = filepath.Join(t.TempDir(), fmt.Sprintf("clip-%d.mp4", i))
		generateSolidColorClip(t, ffmpegPath, paths[i], color, 60, width, height, fps)
	}
	output := filepath.Join(t.TempDir(), "bad-effect-output.mp4")
	stock := stockrustRenderer(newRealExecutor(t, musclesPath, ffmpegPath), width, height, fps)
	_, err := stock.Render(context.Background(), stockpipeline.RenderRequest{
		InputPaths: paths, OutputPath: output,
		Width: width, Height: height, FPSNum: fps, FPSDen: 1, KeyframeInterval: 60,
		Codec: "libx264", Preset: "veryfast", CRF: 23,
		NoTransitions: true,
		NoEffects:     false,
		EffectPaths:   []stockpipeline.RenderEffectPath{{ClipIndex: 0, Path: "/nonexistent/effect.mp4"}},
	})
	if err == nil {
		t.Fatal("missing effect file must be rejected")
	}
	if !strings.Contains(err.Error(), "invalid resolved effect path") {
		t.Fatalf("error = %v, want invalid resolved effect path", err)
	}
	if _, statErr := os.Stat(output); statErr == nil {
		t.Fatal("missing effect file must not produce an output file")
	}
}

// TestStockRustRejectsLegacySelectionInputs certifies the no-selection
// invariant at the wire boundary: legacy selection hints (transition_every,
// effect_every, effects_dir, effect_index_hint) are rejected by Rust, which
// only ever applies IDs and paths explicitly resolved by Go.
func TestStockRustRejectsLegacySelectionInputs(t *testing.T) {
	musclesPath := resolveMusclesBinary()
	if musclesPath == "" {
		t.Skip("pipelinegen-muscles binary not found (set VELOX_RUST_MUSCLES_PATH or build via make build-muscles)")
	}
	for _, field := range []string{
		`"transition_every":4`,
		`"effect_every":2`,
		`"effects_dir":"/tmp/effects"`,
		`"effect_index_hint":1`,
	} {
		payload := fmt.Sprintf(`{"version":"mediaexec.v1","operation":"render_stock","input_paths":["/tmp/not-a-file.mp4"],"output_path":"/tmp/out.mp4",%s}`, field)
		out := runRawMuscles(t, musclesPath, payload)
		if !strings.Contains(out, "unresolved transition/effect selection is not supported") {
			t.Fatalf("legacy selection input %s was not rejected: %s", field, out)
		}
	}
}

func runRawMuscles(t *testing.T, muscles, payload string) string {
	t.Helper()
	cmd := exec.Command(muscles)
	cmd.Stdin = strings.NewReader(payload + "\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("muscles exited non-zero: %v: %s", err, out)
	}
	return string(out)
}
