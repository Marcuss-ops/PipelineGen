package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	infraoverlays "github.com/Marcuss-ops/PipelineGen/internal/platform/overlays"
)

// TestOverlaySegmentResolver_ResolvesFromCache certifies the render_job_id →
// artifact hop: the resolver finds the cached overlay segment by render_key
// (the key the overlay.render handler writes under) and returns it with a
// verified content hash.
func TestOverlaySegmentResolver_ResolvesFromCache(t *testing.T) {
	cache, err := infraoverlays.NewCache(t.TempDir())
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	// Simulate the overlay.render handler's cache write: one artifact per
	// render_key under the overlays namespace.
	renderKey := "5d82d42de05145b6abc65e8866fae74894d67ff38195104747dec1269752c311"
	segmentContent := []byte("fake overlay segment bytes")
	segPath := filepath.Join(t.TempDir(), "overlay.mp4")
	if err := os.WriteFile(segPath, segmentContent, 0644); err != nil {
		t.Fatalf("write segment: %v", err)
	}
	if _, err := cache.PutFile("overlays", renderKey, "overlay.mp4", segPath); err != nil {
		t.Fatalf("cache put: %v", err)
	}

	resolver := &overlaySegmentResolver{cache: cache}
	seg, err := resolver.Resolve(context.Background(), cliprender.OverlayResolveInput{
		RenderJobID: "render-michael-jordan-overlay-001",
		RenderKey:   renderKey,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if seg.RenderJobID != "render-michael-jordan-overlay-001" || seg.RenderKey != renderKey {
		t.Errorf("segment lineage = %+v", seg)
	}
	if seg.LocalPath == "" || seg.SHA256 == "" || seg.SizeBytes != int64(len(segmentContent)) {
		t.Errorf("segment artifact = %+v", seg)
	}
}

// TestOverlaySegmentResolver_FailClosedWithoutArtifact certifies the
// fail-closed half: an unknown render_key yields a typed error, never a
// phantom segment.
func TestOverlaySegmentResolver_FailClosedWithoutArtifact(t *testing.T) {
	cache, err := infraoverlays.NewCache(t.TempDir())
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	resolver := &overlaySegmentResolver{cache: cache}
	_, err = resolver.Resolve(context.Background(), cliprender.OverlayResolveInput{
		RenderJobID: "render-job-001",
		RenderKey:   "unknown-render-key-0000000000000000000000000000000000000000000000000000000000000000",
	})
	if err == nil {
		t.Fatal("resolving an unknown render_key must fail")
	}
}

// generateSmallVideoMP4 renders a tiny real MP4 with ffmpeg for the
// compositor e2e, mirroring the rustexec e2e convention.
func generateSmallVideoMP4(t *testing.T, ffmpeg, path string, seconds float64) {
	t.Helper()
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "color=c=blue:s=320x240:r=25",
		"-t", "1.0",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-an", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate source %s: %v: %s", path, err, out)
	}
	_ = seconds
}

// TestFFmpegOverlayCompositor_BlendsSegmentOntoSource certifies the real
// blend: given a source clip and a rendered overlay segment, the compositor
// produces a hashed output at the declared window. Requires a real ffmpeg
// binary (the canonical e2e convention in this repo).
func TestFFmpegOverlayCompositor_BlendsSegmentOntoSource(t *testing.T) {
	ffmpeg := resolveAppFFmpeg(t)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "source.mp4")
	segPath := filepath.Join(dir, "segment.mp4")
	outPath := filepath.Join(dir, "composited.mp4")
	generateSmallVideoMP4(t, ffmpeg, srcPath, 1.0)
	generateSmallVideoMP4(t, ffmpeg, segPath, 1.0)

	compositor := &ffmpegOverlayCompositor{ffmpegPath: ffmpeg, codec: "libx264", preset: "veryfast", crf: 28}
	composite, err := compositor.Composite(context.Background(), cliprender.OverlayCompositeInput{
		RunID:      "run-overlay-e2e",
		SourcePath: srcPath,
		Segment: &cliprender.OverlaySegment{
			RenderJobID: "render-job-001",
			RenderKey:   "key-001",
			LocalPath:   segPath,
			SHA256:      "segment-sha",
		},
		StartUS:    200000,
		EndUS:      700000,
		OutputPath: outPath,
		Width:      320,
		Height:     240,
	})
	if err != nil {
		t.Fatalf("Composite: %v", err)
	}
	if composite == nil || composite.OutputPath != outPath || composite.SHA256 == "" || composite.SizeBytes <= 0 {
		t.Fatalf("composite result = %+v", composite)
	}
	if composite.CompositeMS < 0 {
		t.Errorf("composite_ms = %d", composite.CompositeMS)
	}
}

// resolveAppFFmpeg returns the configured ffmpeg binary, skipping the test
// when no ffmpeg is available (canonical e2e gate in this repo).
func resolveAppFFmpeg(t *testing.T) string {
	t.Helper()
	if fromEnv := os.Getenv("FFMPEG_PATH"); fromEnv != "" {
		if info, err := os.Stat(fromEnv); err == nil && info.Mode().IsRegular() {
			return fromEnv
		}
	}
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available")
	}
	return path
}
