package adapters

// cliprender_overlay.go wires the overlay-resolution hop of the clip.render
// worker:
//
//	OverlayRefSpec (render_job_id + render_key)
//	    → OverlaySegmentResolver   (overlays cache lookup by render_key)
//	    → OverlaySegment           (the rendered overlay artifact)
//	    → sealed into ClipRenderPlanV1.Overlay
//	    → composited by Chronon INSIDE the single render pass
//
// ── Demolished: the post-render FFmpeg compositor ────────────────────
//
// This file used to also own the post-render FFmpeg overlay compositor, which
// blended the segment onto the already-encoded clip and re-encoded the whole
// file — a SECOND full transcode per overlay clip. It was deleted with the
// single-pass overlay cutover: Chronon now composites the segment as a timed
// video layer in the same render pass (the segment's first frame lands on the
// declared start frame because Chronon's VideoNode samples at
// frame - layer_start). The CI cutover gate forbids its return.
//
// The resolver is the only remaining overlay adapter. It is fail-closed: an
// unknown render_key or a missing/unreadable artifact is a typed error — a
// phantom segment never reaches the sealed plan.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	infraoverlays "github.com/Marcuss-ops/PipelineGen/internal/platform/overlays"
)

// OverlaySegmentResolver resolves the rendered overlay segment from the
// overlays content cache by the declared render_key — the content-addressed
// key the overlay.render handler writes the certified artifact under. The
// render_job_id is the lineage identity; the render_key is the cache key, so
// the resolved segment is provably the artifact that job produced (the plan
// fingerprint and render key travel on the request's lineage). Fail-closed:
// an unknown key or a missing/unreadable artifact is a typed error.
type OverlaySegmentResolver struct {
	cache *infraoverlays.Cache
	// verifier memoizes the segment digest (size+modtime keyed): the cached
	// overlay artifact is immutable for a given render_key, so a batch of
	// clips that reuse the same segment hashes it once instead of once per
	// render. A nil verifier falls back to the canonical one-shot hash.
	verifier *cliprender.ContentVerifier
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
	verifier := r.verifier
	if verifier == nil {
		verifier = cliprender.NewContentVerifier(nil)
	}
	sha, size, err := verifier.Verify(path)
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
