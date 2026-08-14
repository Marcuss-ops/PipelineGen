package overlays

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	infra "github.com/Marcuss-ops/PipelineGen/internal/platform/overlays"
)

type fakeRenderer struct{ calls int }

func (f *fakeRenderer) Render(_ context.Context, _ []byte, output string) error {
	f.calls++
	return os.WriteFile(output, []byte("chronon-overlay"), 0644)
}

func testPlan() capoverlay.OverlayPlan {
	return capoverlay.OverlayPlan{SchemaVersion: capoverlay.SchemaVersionPlan, PlanID: "plan-1", VideoID: "video-1", Width: 1920, Height: 1080, FPS: 30, Items: []capoverlay.OverlayItem{{ID: "overlay-1", TemplateID: "entity-card@1", StartMs: 100, EndMs: 1100, Text: "Ada"}}}
}

func TestRenderHandlerEmitsManifestWithDriveOverlayMetadata(t *testing.T) {
	cache, err := infra.NewCache(filepath.Join(t.TempDir(), "cache"))
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeRenderer{}
	gate, err := infra.NewGPUGate(filepath.Join(t.TempDir(), "gpu.lock"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandlerSet(cache, r, gate, "test-renderer")
	if err != nil {
		t.Fatal(err)
	}
	plan := testPlan()
	payload, _ := json.Marshal(capoverlay.RenderRequest{Plan: plan, OverlayID: "overlay-1"})
	result, err := h.Render(context.Background(), &job.Job{ID: "job-1", Type: capoverlay.JobTypeRender, Payload: payload}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.calls != 1 {
		t.Fatalf("renderer calls=%d, want 1", r.calls)
	}
	raw, ok := result[job.ManifestKey]
	if !ok {
		t.Fatal("render result missing artifact manifest")
	}
	m, ok := raw.(job.ArtifactManifest)
	if !ok {
		t.Fatalf("manifest type=%T, want job.ArtifactManifest", raw)
	}
	meta := m.Artifacts[0].ArtifactMetadata
	if meta["source"] != "overlay" {
		t.Fatalf("source=%v, want overlay", meta["source"])
	}
	if got, ok := meta["drive_subpath"].([]string); !ok || len(got) != 1 || got[0] != "overlay" {
		t.Fatalf("drive_subpath=%#v, want [overlay]", meta["drive_subpath"])
	}
}

func TestRenderHandlerUsesContentCache(t *testing.T) {
	cache, err := infra.NewCache(filepath.Join(t.TempDir(), "cache"))
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeRenderer{}
	gate, err := infra.NewGPUGate(filepath.Join(t.TempDir(), "gpu.lock"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandlerSet(cache, r, gate, "test-renderer")
	if err != nil {
		t.Fatal(err)
	}
	plan := testPlan()
	payload, _ := json.Marshal(capoverlay.RenderRequest{Plan: plan, OverlayID: "overlay-1"})
	j := &job.Job{ID: "job-cache", Type: capoverlay.JobTypeRender, Payload: payload}
	if _, err := h.Render(context.Background(), j, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Render(context.Background(), j, nil); err != nil {
		t.Fatal(err)
	}
	if r.calls != 1 {
		t.Fatalf("renderer calls=%d, want 1 after cache hit", r.calls)
	}
}
