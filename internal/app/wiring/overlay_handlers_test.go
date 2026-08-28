package wiring

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	infra "github.com/Marcuss-ops/PipelineGen/internal/platform/overlays"
)

type fakeRenderer struct{ calls int }

func (f *fakeRenderer) Render(_ context.Context, _ []byte, output string) error {
	f.calls++
	return os.WriteFile(output, []byte("chronon-overlay"), 0644)
}

// fakeProber returns contract-valid facts (DefaultOverlayContractV1:
// 1920x1080@30fps prores/yuva444p/mov, zero audio streams) and hashes the
// on-disk bytes so the manifest's sha256/size stay byte-exact. It never
// inspects actual media — it stands in for the canonical rustexec probe.
type fakeProber struct{ err error }

func (f *fakeProber) ProbeOverlay(_ context.Context, path string) (capoverlay.OverlayProbeResult, error) {
	if f.err != nil {
		return capoverlay.OverlayProbeResult{}, f.err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return capoverlay.OverlayProbeResult{}, err
	}
	sum := sha256.Sum256(b)
	return capoverlay.OverlayProbeResult{
		Width:        1920,
		Height:       1080,
		DurationUS:   1_000_000,
		FPSNum:       30,
		FPSDen:       1,
		AudioStreams: 0,
		Codec:        "prores",
		PixelFormat:  "yuva444p",
		Container:    "mov",
		SizeBytes:    int64(len(b)),
		SHA256:       hex.EncodeToString(sum[:]),
	}, nil
}

func testPlan() capoverlay.OverlayPlan {
	return capoverlay.OverlayPlan{SchemaVersion: capoverlay.SchemaVersionPlan, PlanID: "plan-1", VideoID: "video-1", Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1, Items: []capoverlay.OverlayItem{{ID: "overlay-1", TemplateID: "entity-card@1", StartMs: 100, EndMs: 1100, Text: "Ada"}}}
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
	h, err := NewHandlerSet(cache, r, gate, &fakeProber{}, "test-renderer")
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
	// Probed media-contract facts + READY certification must be durable on
	// the manifest: the overlay is READY only after render + probe + contract
	// validation + hash (upload + persist complete on the Sender side).
	if meta["status"] != capoverlay.OverlayStatusReady {
		t.Fatalf("status=%v, want %q", meta["status"], capoverlay.OverlayStatusReady)
	}
	if meta["container"] != "mov" || meta["codec"] != "prores" || meta["pixel_format"] != "yuva444p" {
		t.Fatalf("probed contract facts = container=%v codec=%v pixel_format=%v, want mov/prores/yuva444p", meta["container"], meta["codec"], meta["pixel_format"])
	}
	if meta["audio_streams"] != 0 {
		t.Fatalf("audio_streams=%v, want 0 (video-only overlay)", meta["audio_streams"])
	}
	if meta["media_contract"] != capoverlay.DefaultOverlayContractV1.ID {
		t.Fatalf("media_contract=%v, want %q", meta["media_contract"], capoverlay.DefaultOverlayContractV1.ID)
	}
	if meta["template_id"] != "entity-card@1" {
		t.Fatalf("template_id=%v, want entity-card@1", meta["template_id"])
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
	h, err := NewHandlerSet(cache, r, gate, &fakeProber{}, "test-renderer")
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

// TestPrepareHandlerConsumesPrepareRequest locks the overlay.prepare payload
// contract: the handler consumes the pre-timing PrepareRequest (OverlayIntents
// with PENDING timing state), warms the entity-image assets they reference,
// and reports the prepared count. It never requires a timed OverlayPlan —
// prepare runs before any timing exists.
func TestPrepareHandlerConsumesPrepareRequest(t *testing.T) {
	cache, err := infra.NewCache(filepath.Join(t.TempDir(), "cache"))
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeRenderer{}
	gate, err := infra.NewGPUGate(filepath.Join(t.TempDir(), "gpu.lock"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandlerSet(cache, r, gate, &fakeProber{}, "test-renderer")
	if err != nil {
		t.Fatal(err)
	}
	req := capoverlay.PrepareRequest{
		SchemaVersion: capoverlay.SchemaVersionPrepare,
		PlanID:        "run-001",
		VideoID:       "run-001",
		Width:         1280, Height: 720, FPSNum: 30, FPSDen: 1,
		Intents: []capoverlay.OverlayIntent{{
			Version: capoverlay.OverlayIntentVersion, IntentID: "intent-scene-0-tom",
			SceneID: "scene-0", SceneIndex: 0, Source: capoverlay.IntentSourceEntity,
			Entity: capoverlay.EntityBinding{Type: "PERSON", CanonicalName: "Tom Hanks"},
			Kind:   string(capoverlay.KindEntityCard), TemplateID: "person_default",
			Payload: capoverlay.IntentPayload{Name: "Tom Hanks"}, TimingState: capoverlay.TimingStatePending,
		}},
	}
	payload, _ := json.Marshal(req)
	result, err := h.Prepare(context.Background(), &job.Job{ID: "prepare-run-001", Type: capoverlay.JobTypePrepare, Payload: payload}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result["schema_version"] != capoverlay.SchemaVersionPrepare {
		t.Fatalf("schema_version = %v, want %q", result["schema_version"], capoverlay.SchemaVersionPrepare)
	}
	if result["plan_id"] != "run-001" || result["prepared"] != 1 {
		t.Fatalf("prepare result = %+v", result)
	}
	if r.calls != 0 {
		t.Fatalf("prepare must not render (renderer calls = %d)", r.calls)
	}
}

// TestPrepareHandlerRejectsFrozenIntent locks the fail-closed contract: a
// prepare request carrying a FROZEN intent is rejected before any asset work.
func TestPrepareHandlerRejectsFrozenIntent(t *testing.T) {
	cache, err := infra.NewCache(filepath.Join(t.TempDir(), "cache"))
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeRenderer{}
	gate, err := infra.NewGPUGate(filepath.Join(t.TempDir(), "gpu.lock"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandlerSet(cache, r, gate, &fakeProber{}, "test-renderer")
	if err != nil {
		t.Fatal(err)
	}
	req := capoverlay.PrepareRequest{
		SchemaVersion: capoverlay.SchemaVersionPrepare,
		PlanID:        "run-001", VideoID: "run-001", Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1,
		Intents: []capoverlay.OverlayIntent{{
			Version: capoverlay.OverlayIntentVersion, IntentID: "intent-scene-0-tom",
			SceneID: "scene-0", SceneIndex: 0, Source: capoverlay.IntentSourceEntity,
			Entity: capoverlay.EntityBinding{Type: "PERSON", CanonicalName: "Tom Hanks"},
			Kind:   string(capoverlay.KindEntityCard), TemplateID: "person_default",
			Payload: capoverlay.IntentPayload{Name: "Tom Hanks"}, TimingState: capoverlay.TimingStateFrozen,
		}},
	}
	payload, _ := json.Marshal(req)
	if _, err := h.Prepare(context.Background(), &job.Job{ID: "prepare-run-001", Type: capoverlay.JobTypePrepare, Payload: payload}, nil); err == nil {
		t.Fatal("a FROZEN intent must be rejected")
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
	h, err := NewHandlerSet(cache, r, gate, &fakeProber{}, "test-renderer")
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

// TestRenderHandler_RejectsMediaContractViolation pins the fail-closed probe
// gate: a render that exited 0 but whose probed facts violate the
// OverlayMediaContract (here, an audio stream in a video-only overlay) must
// NOT publish an artifact — the renderer's exit code is never a validity
// criterion.
func TestRenderHandler_RejectsMediaContractViolation(t *testing.T) {
	cache, err := infra.NewCache(filepath.Join(t.TempDir(), "cache"))
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeRenderer{}
	gate, err := infra.NewGPUGate(filepath.Join(t.TempDir(), "gpu.lock"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandlerSet(cache, r, gate, &audioViolatingProber{}, "test-renderer")
	if err != nil {
		t.Fatal(err)
	}
	plan := testPlan()
	payload, _ := json.Marshal(capoverlay.RenderRequest{Plan: plan, OverlayID: "overlay-1"})
	if _, err := h.Render(context.Background(), &job.Job{ID: "job-invalid", Type: capoverlay.JobTypeRender, Payload: payload}, nil); err == nil {
		t.Fatal("a contract-violating render must fail closed")
	}
}

// TestRenderHandler_ProbeFailureFailsClosed pins that a probe error (the
// canonical ffprobe capability is unavailable) fails the render instead of
// being treated as a successful no-op.
func TestRenderHandler_ProbeFailureFailsClosed(t *testing.T) {
	cache, err := infra.NewCache(filepath.Join(t.TempDir(), "cache"))
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeRenderer{}
	gate, err := infra.NewGPUGate(filepath.Join(t.TempDir(), "gpu.lock"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandlerSet(cache, r, gate, &fakeProber{err: errors.New("probe unavailable")}, "test-renderer")
	if err != nil {
		t.Fatal(err)
	}
	plan := testPlan()
	payload, _ := json.Marshal(capoverlay.RenderRequest{Plan: plan, OverlayID: "overlay-1"})
	if _, err := h.Render(context.Background(), &job.Job{ID: "job-probe-fail", Type: capoverlay.JobTypeRender, Payload: payload}, nil); err == nil {
		t.Fatal("a probe failure must fail closed")
	}
}

// audioViolatingProber returns an otherwise-valid probe that carries one
// audio stream, which violates the video-only OverlayMediaContract.
type audioViolatingProber struct{}

func (audioViolatingProber) ProbeOverlay(_ context.Context, path string) (capoverlay.OverlayProbeResult, error) {
	b, _ := os.ReadFile(path)
	sum := sha256.Sum256(b)
	return capoverlay.OverlayProbeResult{
		Width:        1920,
		Height:       1080,
		DurationUS:   1_000_000,
		FPSNum:       30,
		FPSDen:       1,
		AudioStreams: 1, // violation: overlays must be video-only
		Codec:        "prores",
		PixelFormat:  "yuva444p",
		Container:    "mov",
		SizeBytes:    int64(len(b)),
		SHA256:       hex.EncodeToString(sum[:]),
	}, nil
}

// TestOverlayHandlersMapRenderingGenPhasesToCanonicalRun pins the
// RenderingGen → canonical mapping for the in-process overlay path: the
// handler records each RenderingGen phase (plan/materialize/render/
// objectstore_upload/hash) as one canonical operation on the run bound to
// ctx (component renderinggen, stage process) instead of a new timing
// family. Without a bound run the handlers degrade to plain pass-throughs.
func TestOverlayHandlersMapRenderingGenPhasesToCanonicalRun(t *testing.T) {
	cache, err := infra.NewCache(filepath.Join(t.TempDir(), "cache"))
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeRenderer{}
	gate, err := infra.NewGPUGate(filepath.Join(t.TempDir(), "gpu.lock"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandlerSet(cache, r, gate, &fakeProber{}, "test-renderer")
	if err != nil {
		t.Fatal(err)
	}

	obs := kernobs.NewRunObserver(nil)
	run := obs.StartRun(context.Background(), kernobs.RunInfo{JobID: "job-overlay", JobType: capoverlay.JobTypeRender, AttemptID: "attempt-overlay"})
	ctx := kernobs.WithRun(context.Background(), run)

	plan := testPlan()
	payload, _ := json.Marshal(capoverlay.RenderRequest{Plan: plan, OverlayID: "overlay-1"})
	if _, err := h.Render(ctx, &job.Job{ID: "job-overlay", Type: capoverlay.JobTypeRender, Payload: payload}, nil); err != nil {
		t.Fatal(err)
	}
	if r.calls != 1 {
		t.Fatalf("renderer calls=%d, want 1", r.calls)
	}

	report := run.Finish()
	got := map[string]bool{}
	for _, op := range report.Operations {
		if op.Component != string(kernobs.ComponentRenderingGen) {
			t.Fatalf("operation %s component=%q, want renderinggen", op.Operation, op.Component)
		}
		if op.Stage != string(kernobs.StageProcess) {
			t.Fatalf("operation %s stage=%q, want process", op.Operation, op.Stage)
		}
		if op.Status != string(kernobs.StageStatusCompleted) {
			t.Fatalf("operation %s status=%q, want completed", op.Operation, op.Status)
		}
		got[op.Operation] = true
	}
	for _, want := range []string{"plan", "materialize", "render", "objectstore_upload", "hash"} {
		if !got[want] {
			t.Fatalf("missing canonical operation %q (got %v)", want, got)
		}
	}
}

// TestOverlayPrepareMapsMaterializeToCanonicalRun covers the prepare
// handler's materialize phase.
func TestOverlayPrepareMapsMaterializeToCanonicalRun(t *testing.T) {
	cache, err := infra.NewCache(filepath.Join(t.TempDir(), "cache"))
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeRenderer{}
	gate, err := infra.NewGPUGate(filepath.Join(t.TempDir(), "gpu.lock"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandlerSet(cache, r, gate, &fakeProber{}, "test-renderer")
	if err != nil {
		t.Fatal(err)
	}

	obs := kernobs.NewRunObserver(nil)
	run := obs.StartRun(context.Background(), kernobs.RunInfo{JobID: "job-prepare", JobType: capoverlay.JobTypePrepare, AttemptID: "attempt-prepare"})
	ctx := kernobs.WithRun(context.Background(), run)

	req := capoverlay.PrepareRequest{
		SchemaVersion: capoverlay.SchemaVersionPrepare, PlanID: "run-001", VideoID: "run-001",
		Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1,
		Intents: []capoverlay.OverlayIntent{{Version: capoverlay.OverlayIntentVersion, IntentID: "intent-1", SceneID: "scene-0", SceneIndex: 0, Source: capoverlay.IntentSourceEntity, Entity: capoverlay.EntityBinding{Type: "PERSON", CanonicalName: "Tom Hanks"}, Kind: string(capoverlay.KindEntityCard), TemplateID: "person_default", Payload: capoverlay.IntentPayload{Name: "Tom"}, TimingState: capoverlay.TimingStatePending}},
	}
	payload, _ := json.Marshal(req)
	if _, err := h.Prepare(ctx, &job.Job{ID: "prepare-run-001", Type: capoverlay.JobTypePrepare, Payload: payload}, nil); err != nil {
		t.Fatal(err)
	}

	report := run.Finish()
	for _, op := range report.Operations {
		if op.Operation == string(kernobs.OperationMaterialize) && op.Component == string(kernobs.ComponentRenderingGen) {
			return
		}
	}
	t.Fatalf("prepare did not record the materialize operation: %+v", report.Operations)
}
