package overlays

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	if meta["source"] != "chronon" {
		t.Fatalf("source=%v, want chronon", meta["source"])
	}
	// Parent video identity threads through so the Sender can resolve the
	// overlay's already-resolved video folder (/video/.../overlay/).
	if meta["video_id"] != "video-1" {
		t.Fatalf("video_id=%v, want video-1", meta["video_id"])
	}
	if meta["project_id"] != "" {
		t.Fatalf("project_id=%v, want empty", meta["project_id"])
	}
	if got, ok := meta["drive_subpath"].([]string); !ok || len(got) != 1 || got[0] != "overlay" {
		t.Fatalf("drive_subpath=%#v, want [overlay]", meta["drive_subpath"])
	}
	// SHA256 + Drive + duration/renderer provenance all live on the manifest.
	if meta["renderer_version"] != "test-renderer" {
		t.Fatalf("renderer_version=%v, want test-renderer", meta["renderer_version"])
	}
	// plan item 100→1100ms ⇒ 1000ms ⇒ 1_000_000us.
	if meta["duration_us"] != int64(1_000_000) {
		t.Fatalf("duration_us=%v, want 1000000", meta["duration_us"])
	}
	if meta["duration_ms"] != int64(1000) {
		t.Fatalf("duration_ms=%v, want 1000", meta["duration_ms"])
	}
	if a := m.Artifacts[0]; a.SHA256 == "" || a.SizeBytes <= 0 {
		t.Fatalf("manifest artifact must carry sha256 + size_bytes: sha256=%q size=%d", a.SHA256, a.SizeBytes)
	}
}

// TestRenderHandler_ProbeSHA256AndSizeMatchFileBytes pins the first half of
// the probe→SHA256→manifest→publisher flow: the handler probes the rendered
// file (SHA-256 + size) and that probe must be byte-exact — the manifest's
// sha256/size_bytes equal an independent re-hash of the on-disk bytes.
func TestRenderHandler_ProbeSHA256AndSizeMatchFileBytes(t *testing.T) {
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
	result, err := h.Render(context.Background(), &job.Job{ID: "job-probe", Type: capoverlay.JobTypeRender, Payload: payload}, nil)
	if err != nil {
		t.Fatal(err)
	}

	raw, ok := result[job.ManifestKey]
	if !ok {
		t.Fatal("render result missing artifact manifest")
	}
	m, ok := raw.(job.ArtifactManifest)
	if !ok {
		t.Fatalf("manifest type=%T, want job.ArtifactManifest", raw)
	}
	a := m.Artifacts[0]

	b, err := os.ReadFile(a.Path)
	if err != nil {
		t.Fatalf("read rendered output: %v", err)
	}
	sum := sha256.Sum256(b)
	wantSHA := hex.EncodeToString(sum[:])
	if a.SHA256 != wantSHA {
		t.Fatalf("manifest SHA256 = %q, want %q (probe must be byte-exact)", a.SHA256, wantSHA)
	}
	if a.SizeBytes != int64(len(b)) {
		t.Fatalf("manifest SizeBytes = %d, want %d", a.SizeBytes, len(b))
	}
	if a.SHA256 == "" || a.SizeBytes <= 0 {
		t.Fatalf("probe must populate sha256 + size_bytes: sha256=%q size=%d", a.SHA256, a.SizeBytes)
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
