package rustexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
)

// TestStockRustRenderTenClips drives the REAL Rust canonical renderer over 10
// visually distinct 7s clips (70s total) and certifies the realistic scale:
// exact frame count (2100), exact duration (~70s), manifest/render order
// (each clip's grey level appears at its own midpoint), zero missing/duplicate
// clips, and a clean full decode.
func TestStockRustRenderTenClips(t *testing.T) {
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
		fps        = 30
		clipCount  = 10
		clipSec    = 7
		frameCount = clipSec * fps // 210 per clip
	)

	// 10 well-separated grey levels make each clip visually distinct so the
	// order check can name the expected luminance at every midpoint.
	levels := make([]int, clipCount)
	for i := range levels {
		levels[i] = i * 25 // 0, 25, 50, ..., 225
	}

	assetsDir := t.TempDir()
	clipPaths := make([]string, clipCount)
	planSpec := make([]stockrustClip, clipCount)
	for i := range clipCount {
		assetID := fmt.Sprintf("clip-%02d", i)
		clipPaths[i] = filepath.Join(assetsDir, assetID+".mp4")
		colour := fmt.Sprintf("0x%02X%02X%02X", levels[i], levels[i], levels[i])
		generateSolidColorClip(t, ffmpegPath, clipPaths[i], colour, frameCount, width, height, fps)
		planSpec[i] = stockrustClip{assetID: assetID, durationUS: clipSec * 1_000_000, frameCount: frameCount}
	}

	plan := compileTenClipPlan(t, planSpec, clipPaths, clipCount*clipSec*1_000_000, fps)
	validated, err := render.ValidateRenderPlan(plan, filesystem.NewOS())
	if err != nil {
		t.Fatalf("validate canonical render plan: %v", err)
	}
	executor := newRealExecutor(t, musclesPath, ffmpegPath)
	stock := stockrustRenderer(executor, width, height, fps)
	if err := stock.RenderCanonicalPlan(context.Background(), validated); err != nil {
		t.Fatalf("stockrust render failed: %v", err)
	}

	// 1. Exact frame count: 10 × 210 = 2100 (never ≈).
	if got := ffprobeFrameCount(t, ffprobePath, plan.OutputPath); got != clipCount*frameCount {
		t.Fatalf("output frame count = %d, want %d", got, clipCount*frameCount)
	}

	// 2. Duration ≈ 70s, single h264 1280x720 30fps video stream.
	report := ffprobeOutput(t, ffprobePath, plan.OutputPath)
	if len(report.Streams) != 1 {
		t.Fatalf("expected exactly 1 video stream (no audio), got %d", len(report.Streams))
	}
	if s := report.Streams[0]; s.CodecName != "h264" || s.Width != width || s.Height != height || s.RFrameRate != "30/1" {
		t.Fatalf("unexpected output stream: %+v", report.Streams[0])
	}
	dur, err := strconv.ParseFloat(report.Format.Duration, 64)
	if err != nil {
		t.Fatalf("parse output duration %q: %v", report.Format.Duration, err)
	}
	if dur < 69.8 || dur > 70.2 {
		t.Fatalf("output duration = %.3fs, want ~70.000s", dur)
	}

	// 3. Order: each clip's midpoint (7i+3.5s) must show its own grey level.
	// This also proves no clip is missing or duplicated.
	for i := range clipCount {
		midpoint := float64(i*clipSec) + float64(clipSec)/2.0
		rgb := sampleFrameRGB(t, ffmpegPath, plan.OutputPath, midpoint)
		want := levels[i]
		assertRGBClose(t, fmt.Sprintf("clip-%02d midpoint %.1fs", i, midpoint), rgb, [3]int{want, want, want}, 15)
	}

	// 4. Full decode with zero ffmpeg errors.
	if decodeErrors := fullDecode(t, ffmpegPath, plan.OutputPath); decodeErrors != "" {
		t.Fatalf("full decode produced errors:\n%s", decodeErrors)
	}
}

// compileTenClipPlan mirrors the canonical plan compiler for N solid clips:
// one timeline segment and one manifest entry per clip, with contiguous
// timeline placement and real SHA256 identities.
func compileTenClipPlan(t *testing.T, clips []stockrustClip, clipPaths []string, totalUS int64, fps int) render.RenderPlan {
	t.Helper()
	segments := make([]audio.TimelineSegment, len(clips))
	manifest := make([]render.AssetManifestEntry, len(clips))
	var startUS int64
	for i, c := range clips {
		contents, err := os.ReadFile(clipPaths[i])
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(contents)
		segments[i] = audio.TimelineSegment{
			ID:              fmt.Sprintf("scene-%d", i),
			Index:           i,
			TimelineStartUS: startUS,
			DurationUS:      c.durationUS,
			Video:           audio.VideoSegment{AssetID: c.assetID, SourceInUS: 0, SourceDurationUS: c.durationUS},
			Audio:           audio.AudioIntent{Mode: audio.AudioSilence},
		}
		manifest[i] = render.AssetManifestEntry{
			AssetID: c.assetID, Path: clipPaths[i],
			SHA256: hex.EncodeToString(sum[:]), FrameCount: int64(c.frameCount),
		}
		startUS += c.durationUS
	}
	timeline := audio.CanonicalTimeline{Version: audio.TimelineVersion, DurationUS: totalUS, Segments: segments}
	plan, err := render.Compile(render.CompileInput{
		JobID: "stockrust-live-10clip", Revision: "generation.v1",
		OutputPath: filepath.Join(t.TempDir(), "stockrust-10clip-output.mp4"),
		FrameRate:  audio.IntegerFrameRate(fps), Timeline: timeline, Manifest: manifest,
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
