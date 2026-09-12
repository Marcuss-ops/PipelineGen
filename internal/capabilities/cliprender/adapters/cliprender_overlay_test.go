package adapters

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	infraoverlays "github.com/Marcuss-ops/PipelineGen/internal/platform/overlays"
)

// TestOverlaySegmentResolver_ResolvesFromCache certifies the render_job_id →
// artifact hop: the resolver finds the cached overlay segment by render_key
// (the key the overlay.render handler writes under) and returns it with a
// verified content hash. The resolved segment is what the worker seals into
// the plan so Chronon composites it inside the single render pass.
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

	resolver := &OverlaySegmentResolver{cache: cache}
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
// phantom segment (and therefore never a plan claiming an overlay it cannot
// composite).
func TestOverlaySegmentResolver_FailClosedWithoutArtifact(t *testing.T) {
	cache, err := infraoverlays.NewCache(t.TempDir())
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	resolver := &OverlaySegmentResolver{cache: cache}
	_, err = resolver.Resolve(context.Background(), cliprender.OverlayResolveInput{
		RenderJobID: "render-job-001",
		RenderKey:   "unknown-render-key-0000000000000000000000000000000000000000000000000000000000000000",
	})
	if err == nil {
		t.Fatal("resolving an unknown render_key must fail")
	}
}
