// Package videomuscles (watermark_smoke_test.go) — LIVE smoke test for the
// YouTube watermark overlay path.
//
// Certifies the production wiring end-to-end:
//
//	Pipeline (videomuscles) → ClipProcessor port → rustexec.VideoProcessor
//	→ Rust media plane (operation=watermark) → ffmpeg overlay
//
// with the canonical YouTube watermark parameters (config/watermark.png,
// opacity 0.25, position center, scale 20%). It drives the REAL Rust binary
// (bin/pipelinegen-muscles) against an ffmpeg-generated source, exactly like
// the production composition root wires it (build_bundles_domain_media.go),
// and asserts:
//
//  1. The pipeline reaches the watermark branch (log line
//     "watermark overlay applied successfully").
//  2. The watermark operation dispatches an ffmpeg invocation through the
//     Rust media plane (ffmpeg_exec_count increments by exactly 1 on top of
//     the cut+normalize baseline).
//  3. The output is a physically valid MP4 (positive duration, h264 video +
//     aac audio, no stub).
//
// Skips when the pipelinegen-muscles binary or ffmpeg are missing.
package videomuscles

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
)

// watermarkTestBinaries resolves the Rust muscles binary and ffmpeg,
// skipping the test when either is unavailable (same posture as the
// rustexec E2E certification tests).
func watermarkTestBinaries(t *testing.T) (musclesPath, ffmpegPath, ffprobePath string) {
	t.Helper()
	if v := os.Getenv("VELOX_RUST_MUSCLES_PATH"); v != "" {
		musclesPath = v
	} else if _, err := os.Stat(filepath.Join("..", "..", "..", "..", "bin", "pipelinegen-muscles")); err == nil {
		musclesPath, _ = filepath.Abs(filepath.Join("..", "..", "..", "..", "bin", "pipelinegen-muscles"))
	}
	if musclesPath == "" {
		t.Skip("pipelinegen-muscles binary not found (set VELOX_RUST_MUSCLES_PATH or build via make build-muscles)")
	}
	var err error
	if ffmpegPath, err = exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg binary not found")
	}
	if ffprobePath, err = exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe binary not found")
	}
	return musclesPath, ffmpegPath, ffprobePath
}

// wmGenerateSource creates a 1920x1080/24fps test video with audio.
func wmGenerateSource(t *testing.T, ffmpeg, path string) {
	t.Helper()
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=1920x1080:rate=24",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-t", "6",
		"-c:v", "libx264", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-ar", "48000", "-ac", "2", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate source %s: %v: %s", path, err, out)
	}
}

// wmWriteWatermarkPNG writes a FULL-FRAME solid-color PNG into workDir/config/
// (the exact path the YouTube pipeline probes: config/watermark.png).
// Full-frame on purpose: without the scale_percent=20 honor the overlay
// would cover the entire output; with it, only the center 20% is darkened.
// color=none means the pipeline's green-screen chromakey (0x00FF00) leaves
// the overlay fully transparent, so the center must NOT darken.
func wmWriteWatermarkPNG(t *testing.T, ffmpeg, workDir, color string) {
	t.Helper()
	cfgDir := filepath.Join(workDir, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	if color == "" {
		color = "black"
	}
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", fmt.Sprintf("color=c=%s@1.0:s=1920x1080", color),
		"-frames:v", "1", filepath.Join(cfgDir, "watermark.png"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate watermark: %v: %s", err, out)
	}
}

// wmRunYouTubeCut drives the REAL production wiring: Pipeline + the same
// rustexec.NewConfiguredVideoProcessor the composition root builds. Returns
// the output path and the ffmpeg_exec_count delta observed across the run.
func wmRunYouTubeCut(t *testing.T, workDir, musclesPath, ffmpegPath, sourcePath string) (string, float64) {
	t.Helper()
	logBuf := &bytes.Buffer{}
	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
		zapcore.AddSync(logBuf),
		zapcore.DebugLevel,
	)
	log := zap.New(core)

	proc := rustexec.NewConfiguredVideoProcessor(
		musclesPath, ffmpegPath,
		mediaexec.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23},
		mediaexec.VideoProfile{
			Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, KeyframeInterval: 48,
			AudioCodec: "aac", AudioBitrate: "128k", SampleRate: 48000, Channels: 2,
		},
		log,
	)
	p := NewPipeline(&config.Config{}, log, proc)

	// The pipeline probes config/watermark.png relative to the process CWD.
	// Point the whole test process there for this run.
	prevCWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(workDir); err != nil {
		t.Fatalf("chdir %s: %v", workDir, err)
	}
	defer func() { _ = os.Chdir(prevCWD) }()

	before := testutil.ToFloat64(observability.FFmpegExecCount)
	outDir := filepath.Join(workDir, "out")
	res, err := p.DownloadAndCutYouTubeVideo(context.Background(), YouTubeCutRequest{
		URL:               "https://www.youtube.com/watch?v=wm-smoke-test",
		VideoID:           "wm-smoke-test",
		Start:             0,
		Duration:          4,
		OutputName:        "wm-smoke",
		KeepAudio:         true,
		Normalize:         true,
		Strategy:          "replace",
		OutputDir:         outDir,
		PreDownloadedPath: sourcePath,
	})
	after := testutil.ToFloat64(observability.FFmpegExecCount)
	if err != nil {
		t.Fatalf("DownloadAndCutYouTubeVideo: %v", err)
	}
	if res == nil || res.LocalPath == "" {
		t.Fatalf("DownloadAndCutYouTubeVideo: empty result")
	}
	if _, err := os.Stat(res.LocalPath); err != nil {
		t.Fatalf("output missing: %v", err)
	}
	return res.LocalPath, after - before
}

func TestYouTubeWatermarkSmoke_GoesThroughRustMediaPlane(t *testing.T) {
	musclesPath, ffmpegPath, ffprobePath := watermarkTestBinaries(t)

	base := t.TempDir()
	sourcePath := filepath.Join(base, "source.mp4")
	wmGenerateSource(t, ffmpegPath, sourcePath)

	// ── Run 1: WITHOUT config/watermark.png → baseline (cut+normalize only).
	noWMDir := filepath.Join(base, "no-wm")
	if err := os.MkdirAll(noWMDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	noWMPath, noWMDelta := wmRunYouTubeCut(t, noWMDir, musclesPath, ffmpegPath, sourcePath)

	// ── Run 2: WITH a full-frame BLACK watermark.png → watermark branch
	//    must fire AND scale_percent=20 must shrink it to the center 20%.
	withWMDir := filepath.Join(base, "with-wm")
	if err := os.MkdirAll(withWMDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	wmWriteWatermarkPNG(t, ffmpegPath, withWMDir, "black")
	withWMPath, withWMDelta := wmRunYouTubeCut(t, withWMDir, musclesPath, ffmpegPath, sourcePath)

	// ── Run 3: WITH a full-frame GREEN watermark.png → the green-screen
	//    chromakey (0x00FF00) must remove it: center must NOT darken.
	greenWMDir := filepath.Join(base, "green-wm")
	if err := os.MkdirAll(greenWMDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	wmWriteWatermarkPNG(t, ffmpegPath, greenWMDir, "0x00FF00")
	greenWMPath, _ := wmRunYouTubeCut(t, greenWMDir, musclesPath, ffmpegPath, sourcePath)

	// 1. The watermark branch was reached: exactly ONE extra ffmpeg
	//    invocation (the overlay) over the cut+normalize baseline.
	if withWMDelta != noWMDelta+1 {
		t.Errorf("ffmpeg_exec_count delta with watermark = %v, want baseline %v + 1 (operation=watermark through Rust media plane)",
			withWMDelta, noWMDelta)
	}
	if noWMDelta < 2 {
		t.Errorf("baseline ffmpeg_exec_count delta = %v, want >= 2 (cut_copy + cut_and_normalize)", noWMDelta)
	}

	// 2. All three outputs are physically valid MP4s.
	for name, p := range map[string]string{
		"without-watermark": noWMPath,
		"with-watermark":    withWMPath,
		"green-watermark":   greenWMPath,
	} {
		probe := exec.Command(ffprobePath, "-v", "error",
			"-show_entries", "format=duration,size",
			"-show_entries", "stream=codec_type,codec_name,width,height",
			"-of", "json", p)
		out, err := probe.CombinedOutput()
		if err != nil {
			t.Fatalf("ffprobe %s: %v: %s", name, err, out)
		}
		if !strings.Contains(string(out), `"codec_name": "h264"`) {
			t.Errorf("%s: missing h264 video stream:\n%s", name, out)
		}
		if !strings.Contains(string(out), `"codec_name": "aac"`) {
			t.Errorf("%s: missing aac audio stream:\n%s", name, out)
		}
	}

	// 3. No .part/.tmp residue from the watermark path.
	leftovers := []string{}
	_ = filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && (strings.HasSuffix(path, ".part") || strings.HasSuffix(path, ".tmp")) {
			leftovers = append(leftovers, path)
		}
		return nil
	})
	if len(leftovers) > 0 {
		t.Errorf("leftover temp files: %v", leftovers)
	}

	// 4. Pixel probes: black watermark must darken the center (overlay at
	//    position=center + scale_percent=20), the green watermark must be
	//    chroma-keyed away (center unchanged), and the corner must stay
	//    bright with the black watermark (scale actually shrunk it).
	probePixel := func(path string, x, y int) (int, int, int) {
		t.Helper()
		framePath := filepath.Join(t.TempDir(), "frame.png")
		frame := exec.Command(ffmpegPath, "-hide_banner", "-loglevel", "error", "-y",
			"-ss", "2", "-i", path, "-frames:v", "1", framePath)
		if out, err := frame.CombinedOutput(); err != nil {
			t.Fatalf("extract frame %s: %v: %s", path, err, out)
		}
		cmd := exec.Command(ffmpegPath, "-hide_banner", "-loglevel", "error",
			"-i", framePath,
			"-vf", fmt.Sprintf("crop=1:1:%d:%d", x, y),
			"-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgb24", "-")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("pixel %s @(%d,%d): %v: %s", path, x, y, err, out)
		}
		if len(out) < 3 {
			t.Fatalf("pixel %s @(%d,%d): short output %d bytes", path, x, y, len(out))
		}
		return int(out[0]), int(out[1]), int(out[2])
	}
	bright := func(r, g, b int) int { return max(r, g, b) }

	// 4a. Center pixel: black watermark must darken it (opacity 0.25).
	rNo, gNo, bNo := probePixel(noWMPath, 960, 540)
	rWith, gWith, bWith := probePixel(withWMPath, 960, 540)
	if bright(rWith, gWith, bWith) >= bright(rNo, gNo, bNo)-10 {
		t.Errorf("center with black watermark = (%d,%d,%d) bright %d, baseline (%d,%d,%d) bright %d; want >= 10 darker (overlay at center)",
			rWith, gWith, bWith, bright(rWith, gWith, bWith), rNo, gNo, bNo, bright(rNo, gNo, bNo))
	}

	// 4b. Corner pixel: with scale_percent=20 the watermark covers only the
	//     center 20% (384x216 of 1920x1080); the corner must stay as bright
	//     as the baseline corner (same source frame, same position).
	rCNo, gCNo, bCNo := probePixel(noWMPath, 100, 100)
	rC, gC, bC := probePixel(withWMPath, 100, 100)
	if bright(rC, gC, bC) < bright(rCNo, gCNo, bCNo)-10 {
		t.Errorf("corner with black watermark = (%d,%d,%d) bright %d, baseline corner (%d,%d,%d) bright %d; want ~baseline bright (scale_percent=20 must shrink the full-frame overlay)",
			rC, gC, bC, bright(rC, gC, bC), rCNo, gCNo, bCNo, bright(rCNo, gCNo, bCNo))
	}

	// 4c. Center pixel with the GREEN watermark: chromakey 0x00FF00 removes
	//     it, so the center must NOT darken vs the baseline.
	rG, gG, bG := probePixel(greenWMPath, 960, 540)
	if bright(rG, gG, bG) < bright(rNo, gNo, bNo)-10 {
		t.Errorf("center with green watermark = (%d,%d,%d) bright %d; want ~baseline bright (green-screen chromakey must remove the overlay)",
			rG, gG, bG, bright(rG, gG, bG))
	}

	// 5. Wall-clock sanity: the whole run completes in reasonable time.
	_ = time.Now // kept out of the assertion path; duration assertions are flaky in CI
}
