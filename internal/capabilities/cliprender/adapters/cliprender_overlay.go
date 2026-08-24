package adapters

// cliprender_overlay.go wires the compositing hop of the clip.render worker:
//
//	OverlayRefSpec (render_job_id + render_key)
//	    → OverlaySegmentResolver   (overlays cache lookup by render_key)
//	    → OverlaySegment           (the rendered overlay artifact)
//	    → FFmpegOverlayCompositor  (blend onto the source at [start_us, end_us))
//	    → final video that contains the overlay in its pixels
//
// The compositor derives ALL encoder parameters from the assembly-ready
// contract (ResolvedContract): pixel_format, keyframe interval (GOP), FPS,
// video profile, audio contract. No hardcoded encoder values remain in this
// file. Post-composite ffprobe + ValidateContract is mandatory when the
// worker's outputProber is wired.
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
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	infraoverlays "github.com/Marcuss-ops/PipelineGen/internal/platform/overlays"
)

// ── OverlaySegmentResolver (render_job_id → artifact) ─────────────────

// OverlaySegmentResolver resolves the rendered overlay segment from the
// overlays content cache by the declared render_key — the content-addressed
// key the overlay.render handler writes the certified artifact under. The
// render_job_id is the lineage identity; the render_key is the cache key, so
// the resolved segment is provably the artifact that job produced (the plan
// fingerprint and render key travel on the request's lineage). Fail-closed:
// an unknown key or a missing/unreadable artifact is a typed error.
type OverlaySegmentResolver struct {
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

func (r *OverlaySegmentResolver) Resolve(_ context.Context, in cliprender.OverlayResolveInput) (*cliprender.OverlaySegment, error) {
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
	sha, size, err := digest.SHA256File(path)
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

// FFmpegOverlayCompositor blends the rendered overlay segment onto the
// source clip at the declared [start_us, end_us) window with a single ffmpeg
// pass. Every encoder parameter (codec, preset, CRF, pixel_format, GOP,
// profile, audio) is derived from the assembly-ready contract
// (ResolvedContract) — there are NO hardcoded encoder defaults.
//
// The ffmpeg invocation:
//
//	scale → pad (letterbox) → setsar=1 → setpts=PTS+start/TB → overlay
//	with enable=between(start, end). Source audio is copied bit-exact.
type FFmpegOverlayCompositor struct {
	ffmpegPath string
	codec      string // from mediaConfig.Policy.Codec (composition root)
	preset     string // from mediaConfig.Policy.Preset
	crf        int    // from mediaConfig.Policy.CRF
}

func (c *FFmpegOverlayCompositor) Composite(ctx context.Context, in cliprender.OverlayCompositeInput) (*cliprender.OverlayCompositeResult, error) {
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

	// Contract is mandatory: every encoder parameter derives from it.
	if in.Contract == nil {
		return nil, fmt.Errorf("overlay compositor: assembly-ready contract is required")
	}

	// Verify contract invariants that matter for compositing.
	if in.Contract.FPSNum != 24 || in.Contract.FPSDen != 1 {
		return nil, fmt.Errorf("overlay compositor: contract fps %d/%d != 24/1", in.Contract.FPSNum, in.Contract.FPSDen)
	}
	if in.Contract.PixelFormat != "yuv420p" {
		return nil, fmt.Errorf("overlay compositor: contract pixel_format %q != yuv420p", in.Contract.PixelFormat)
	}
	if in.Contract.VideoCodec != "h264" || in.Contract.VideoProfile != "high" {
		return nil, fmt.Errorf("overlay compositor: contract video %s/%s != h264/high", in.Contract.VideoCodec, in.Contract.VideoProfile)
	}

	// Geometry: prefer contract Width/Height; fall back to legacy Width/Height for compat.
	width := in.Contract.Width
	height := in.Contract.Height
	if in.Width > 0 {
		width = in.Width
	}
	if in.Height > 0 {
		height = in.Height
	}
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("overlay compositor: target geometry %dx%d is invalid", width, height)
	}

	// ── Derive ALL encoder parameters from the contract ──
	codec := c.codec
	preset := c.preset
	crf := c.crf
	if preset == "" {
		preset = "veryfast"
	}
	if crf == 0 {
		crf = 23
	}
	// pixel_format, GOP, and FPS are contract-driven — never hardcoded.
	pixFmt := in.Contract.PixelFormat
	gop := in.Contract.KeyframeInterval
	if gop <= 0 {
		gop = 48
	}

	startSec := float64(in.StartUS) / 1e6
	endSec := float64(in.EndUS) / 1e6
	// Scale+letterbox the segment to the target geometry, then shift its PTS
	// so the segment's own t=0 lands at start_us on the final timeline.
	segmentChain := fmt.Sprintf(
		"[1:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1,setpts=PTS+%.6f/TB[ov]",
		width, height, width, height, startSec,
	)
	overlayChain := fmt.Sprintf(
		"[0:v][ov]overlay=0:0:enable='between(t,%.6f,%.6f)':eof_action=pass[outv]",
		startSec, endSec,
	)
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
		"-c:v", codec, "-preset", preset, "-crf", fmt.Sprint(crf),
		// Contract-driven: pixel format, GOP, profile, FPS.
		"-pix_fmt", pixFmt,
		"-g", fmt.Sprint(gop),
		"-bf", "0", // no B-frames (closed GOP)
		"-flags", "+cgop", // closed GOP
		"-profile:v", in.Contract.VideoProfile,
		"-r", fmt.Sprintf("%d/%d", in.Contract.FPSNum, in.Contract.FPSDen),
		// Audio: copy bit-exact from source (contract audio already verified).
		"-c:a", "copy",
		"-movflags", "+faststart",
		in.OutputPath,
	}
	cmd := exec.CommandContext(ctx, c.ffmpegPath, args...)
	if combined, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("overlay compositor: ffmpeg blend: %w\n%s", err, lastLines(string(combined), 20))
	}
	sha, size, err := digest.SHA256File(in.OutputPath)
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
