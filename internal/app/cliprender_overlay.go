package app

// cliprender_overlay.go wires the compositing hop of the clip.render worker:
//
//	OverlayRefSpec (render_job_id + render_key)
//	    → overlaySegmentResolver   (overlays cache lookup by render_key)
//	    → OverlaySegment           (the rendered overlay artifact)
//	    → ffmpegOverlayCompositor  (blend onto the source at [start_us, end_us))
//	    → final video that contains the overlay in its pixels
//
// Both adapters are fail-closed: an unresolvable segment or a failed blend is
// a typed error — the published video never claims an overlay it does not
// carry.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	infraoverlays "github.com/Marcuss-ops/PipelineGen/internal/platform/overlays"
)

// ── OverlaySegmentResolver (render_job_id → artifact) ─────────────────

// overlaySegmentResolver resolves the rendered overlay segment from the
// overlays content cache by the declared render_key — the content-addressed
// key the overlay.render handler writes the certified artifact under. The
// render_job_id is the lineage identity; the render_key is the cache key, so
// the resolved segment is provably the artifact that job produced (the plan
// fingerprint and render key travel on the request's lineage). Fail-closed:
// an unknown key or a missing/unreadable artifact is a typed error.
type overlaySegmentResolver struct {
	cache *infraoverlays.Cache
}

// resolveOverlaySegmentInCache locates the cached overlay artifact for a
// render_key. The cache stores one file per render key under
// <root>/overlays/<key[:2]>/<key>/<filename>; the filename derives from the
// item id (unknown to the resolver), so the key directory is scanned for its
// single artifact.
func resolveOverlaySegmentInCache(cache *infraoverlays.Cache, renderKey string) (string, error) {
	if cache == nil || len(renderKey) < 2 {
		return "", fmt.Errorf("overlay segment resolver: invalid cache or render_key %q", renderKey)
	}
	dir := filepath.Join(cache.Root, "overlays", renderKey[:2], renderKey)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("overlay segment resolver: cache lookup %q: %w", renderKey, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		return filepath.Join(dir, e.Name()), nil
	}
	return "", fmt.Errorf("overlay segment resolver: no cached artifact for render_key %q", renderKey)
}

func (r *overlaySegmentResolver) Resolve(_ context.Context, in cliprender.OverlayResolveInput) (*cliprender.OverlaySegment, error) {
	if r == nil || r.cache == nil {
		return nil, fmt.Errorf("overlay segment resolver: cache is required")
	}
	if strings.TrimSpace(in.RenderJobID) == "" || strings.TrimSpace(in.RenderKey) == "" {
		return nil, fmt.Errorf("overlay segment resolver: render_job_id and render_key are required")
	}
	path, err := resolveOverlaySegmentInCache(r.cache, in.RenderKey)
	if err != nil {
		return nil, err
	}
	sha, size, err := sha256File(path)
	if err != nil {
		return nil, fmt.Errorf("overlay segment resolver: hash artifact: %w", err)
	}
	return &cliprender.OverlaySegment{
		RenderJobID: in.RenderJobID,
		RenderKey:   in.RenderKey,
		LocalPath:   path,
		SHA256:      sha,
		SizeBytes:   size,
	}, nil
}

// ── OverlayCompositor (ffmpeg blend at [start_us, end_us)) ───────────

// ffmpegOverlayCompositor blends the rendered overlay segment onto the
// source clip at the declared [start_us, end_us) window with a single ffmpeg
// pass: the segment is scaled+letterboxed to the target geometry, its PTS is
// shifted so its own t=0 lands at start_us, and it is overlaid on the source
// only inside the window (enable=between). The source audio is copied
// bit-exact. The output is content-hashed before the result is returned.
type ffmpegOverlayCompositor struct {
	ffmpegPath string
	codec      string
	preset     string
	crf        int
}

func (c *ffmpegOverlayCompositor) Composite(ctx context.Context, in cliprender.OverlayCompositeInput) (*cliprender.OverlayCompositeResult, error) {
	if c == nil || strings.TrimSpace(c.ffmpegPath) == "" {
		return nil, fmt.Errorf("overlay compositor: ffmpeg path is required")
	}
	if in.Segment == nil || in.Segment.LocalPath == "" || in.Segment.SHA256 == "" {
		return nil, fmt.Errorf("overlay compositor: a resolved overlay segment is required")
	}
	if in.SourcePath == "" || in.OutputPath == "" {
		return nil, fmt.Errorf("overlay compositor: source and output paths are required")
	}
	if in.StartUS < 0 || in.EndUS <= in.StartUS {
		return nil, fmt.Errorf("overlay compositor: invalid window [%d, %d)", in.StartUS, in.EndUS)
	}
	if in.Width <= 0 || in.Height <= 0 {
		return nil, fmt.Errorf("overlay compositor: target geometry %dx%d is invalid", in.Width, in.Height)
	}

	startSec := float64(in.StartUS) / 1e6
	endSec := float64(in.EndUS) / 1e6
	// Scale+letterbox the segment to the target geometry, then shift its PTS
	// so the segment's own t=0 lands at start_us on the final timeline.
	segmentChain := fmt.Sprintf(
		"[1:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1,setpts=PTS+%.6f/TB[ov]",
		in.Width, in.Height, in.Width, in.Height, startSec,
	)
	overlayChain := fmt.Sprintf(
		"[0:v][ov]overlay=0:0:enable='between(t,%.6f,%.6f)':eof_action=pass[outv]",
		startSec, endSec,
	)
	codec := c.codec
	preset := c.preset
	if preset == "" {
		preset = "medium"
	}
	crf := c.crf
	if crf == 0 {
		crf = 23
	}
	if err := os.MkdirAll(filepath.Dir(in.OutputPath), 0755); err != nil {
		return nil, fmt.Errorf("overlay compositor: create output dir: %w", err)
	}

	start := time.Now()
	args := []string{
		"-y", "-hide_banner", "-loglevel", "error",
		"-i", in.SourcePath,
		"-i", in.Segment.LocalPath,
		"-filter_complex", segmentChain + ";" + overlayChain,
		"-map", "[outv]",
		"-map", "0:a?",
		"-c:v", codec, "-preset", preset, "-crf", fmt.Sprint(crf), "-pix_fmt", "yuv420p",
		"-c:a", "copy",
		"-movflags", "+faststart",
		in.OutputPath,
	}
	cmd := exec.CommandContext(ctx, c.ffmpegPath, args...)
	if combined, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("overlay compositor: ffmpeg blend: %w\n%s", err, lastLines(string(combined), 20))
	}
	sha, size, err := sha256File(in.OutputPath)
	if err != nil {
		return nil, fmt.Errorf("overlay compositor: hash output: %w", err)
	}
	return &cliprender.OverlayCompositeResult{
		OutputPath:  in.OutputPath,
		SHA256:      sha,
		SizeBytes:   size,
		CompositeMS: time.Since(start).Milliseconds(),
	}, nil
}

// lastLines returns the last n lines of s, for trimming ffmpeg stderr in
// error messages.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
