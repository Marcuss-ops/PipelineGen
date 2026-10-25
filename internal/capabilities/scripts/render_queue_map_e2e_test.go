package scriptgeneration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/overlay"
)

// TestPlanMapBackendsRoutingAndAnnotationAgree is the cutover acceptance case:
// routing and annotation consume the SAME backend decision, so the route a
// map takes and the backend stamped on it can never disagree — whether the
// plan holds one map or several.
func TestPlanMapBackendsRoutingAndAnnotationAgree(t *testing.T) {
	dynamicMap := func(id string) capoverlay.OverlayItem {
		return capoverlay.OverlayItem{ID: id, Kind: "map",
			Map:    &capoverlay.MapOverlay{},
			Params: map[string]any{"map_animation": "region_zoom"},
		}
	}
	single := capoverlay.OverlayPlan{Items: []capoverlay.OverlayItem{dynamicMap("map-1")}}
	multi := capoverlay.OverlayPlan{Items: []capoverlay.OverlayItem{dynamicMap("map-1"), dynamicMap("map-2")}}

	for name, plan := range map[string]capoverlay.OverlayPlan{"single": single, "multi": multi} {
		decisions, err := resolvePlanMapBackends(plan)
		if err != nil {
			t.Fatalf("%s: resolve: %v", name, err)
		}
		if len(decisions) != len(plan.Items) {
			t.Fatalf("%s: decisions = %d, want one per map item", name, len(decisions))
		}
		for _, decision := range decisions {
			if decision.backend != "python" {
				t.Fatalf("%s: item %q backend = %q, want python (dynamic composition)", name, decision.itemID, decision.backend)
			}
		}
		annotated := plan
		annotatePlanMapBackends(&annotated, decisions)
		for i := range annotated.Items {
			if got := annotated.Items[i].Params["map_backend"]; got != "python" {
				t.Fatalf("%s: item %q annotated backend = %v, want python", name, annotated.Items[i].ID, got)
			}
			if reason, _ := annotated.Items[i].Params["map_backend_reason"].(string); reason == "" {
				t.Fatalf("%s: item %q carries no backend reason for the decision report", name, annotated.Items[i].ID)
			}
		}
	}
	if planNeedsPerItemMapRenders(single) {
		t.Fatal("the single dynamic map keeps the certified background fold")
	}
	if !planNeedsPerItemMapRenders(multi) {
		t.Fatal("the two-map plan renders each map per item exactly like a single-map plan")
	}
}

// completingMapClient stores the submitted job and immediately completes it
// with a certified artifact, so the test observes the real submit→wait→return
// path without a live queue.
type completingMapClient struct {
	*fakeRenderQueueClient
	artifact *RenderArtifact
}

func (c *completingMapClient) Submit(ctx context.Context, job RenderQueueJob) error {
	if err := c.fakeRenderQueueClient.Submit(ctx, job); err != nil {
		return err
	}
	c.fakeRenderQueueClient.mu.Lock()
	defer c.fakeRenderQueueClient.mu.Unlock()
	stored := c.jobs[job.ID]
	stored.State = "completed"
	stored.Artifact = c.artifact
	c.jobs[job.ID] = stored
	return nil
}

// installFakeMapRenderer anchors VELOX_GEO_MAP_PLATE_GENERATOR_PATH at a
// stand-in Chronon renderer emitting the given telemetry summary.
func installFakeMapRenderer(t *testing.T, telemetry string) {
	t.Helper()
	dir := t.TempDir()
	plate := filepath.Join(dir, "plate-generator.py")
	if err := os.WriteFile(plate, []byte("# path anchor\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rendererDir := filepath.Join(dir, "map")
	if err := os.MkdirAll(rendererDir, 0o700); err != nil {
		t.Fatal(err)
	}
	program := "import argparse,pathlib\np=argparse.ArgumentParser();p.add_argument('--input');p.add_argument('--output');p.add_argument('--summary-output');a=p.parse_args()\npathlib.Path(a.output).write_bytes(b'fake-mp4')\npathlib.Path(a.summary_output).write_text(" + strconv.Quote(telemetry) + ",encoding='utf-8')\nprint('test diagnostic')\n"
	if err := os.WriteFile(filepath.Join(rendererDir, "render_dynamic_map_overlay.py"), []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VELOX_GEO_MAP_PLATE_GENERATOR_PATH", plate)
}

// TestRenderDynamicMapVideoHonorsDeclaredEncodeProfile pins the encoder
// decoupling at the render boundary: the CPU-certified path renders under an
// explicit declaration, NVENC telemetry under a CPU declaration fails instead
// of being silently accepted, and an unknown codec fails before any render.
func TestRenderDynamicMapVideoHonorsDeclaredEncodeProfile(t *testing.T) {
	cpuTelemetry := `{"schema":"chronon.dynamic-map-telemetry.v1","frames":120,"dimensions":{"width":1920,"height":1080},"fps":{"num":24,"den":1},"tile":{"tile_disk_cache_hits":7,"tile_disk_cache_misses":0,"tile_network_fetches":0,"tile_fallbacks":0,"tile_bytes_downloaded":0,"tile_fetch_ms":12.0,"prefetch_ms":3.0,"prefetch_tiles_requested":7,"prefetch_tiles_memory_hits":0,"late_tile_fetches":0},"plates":{"prepare_ms":4.0,"plate_compose_ms":1.0,"plate_count":11,"plate_bytes":42,"required_tile_count":7,"prefetch_tiles_fetched":7,"prefetch_tiles_memory_hits":0},"gate_ms":2.0,"frame_pipeline_s":3.0,"render_encode_wall_s":3.5,"post_frame_tail_s":0.5,"engine_fallback_frames":0,"output_bytes":8,"video_encoder":"libx264","gpu_encoder":false,"gpu_map_sampler":"cpu","encoder_preset":"veryfast","encoder_quality":23,"renderer_wall_s":9.0}`
	installFakeMapRenderer(t, cpuTelemetry)
	t.Setenv("PIPELINEGEN_MAP_CODEC", "libx264")
	path, err := renderDynamicMapVideoWithGate(context.Background(), newTestMapPlan("map-cpu-1"), nil)
	if err != nil {
		t.Fatalf("declared CPU profile with CPU telemetry must render: %v", err)
	}
	defer func() { _ = os.RemoveAll(filepath.Dir(path)) }()
}

func TestRenderDynamicMapVideoRefusesCrossProfileTelemetry(t *testing.T) {
	installFakeMapRenderer(t, validDynamicMapTelemetry)
	t.Setenv("PIPELINEGEN_MAP_CODEC", "libx264")
	if _, err := renderDynamicMapVideoWithGate(context.Background(), newTestMapPlan("map-cross-1"), &fakeDynamicMapGPUGate{}); err == nil {
		t.Fatal("NVENC telemetry under a declared CPU profile must fail, not certify")
	}
}

func TestRenderDynamicMapVideoRefusesUnknownCodec(t *testing.T) {
	installFakeMapRenderer(t, validDynamicMapTelemetry)
	t.Setenv("PIPELINEGEN_MAP_CODEC", "hevc_nvenc")
	if _, err := renderDynamicMapVideoWithGate(context.Background(), newTestMapPlan("map-badcodec-1"), &fakeDynamicMapGPUGate{}); err == nil {
		t.Fatal("an uncertified codec declaration must fail before any render")
	}
}

func TestDynamicMapCPUProfileRequiresAdmissionWhenSamplerOptedIntoOpenCL(t *testing.T) {
	t.Setenv("PIPELINEGEN_MAP_CODEC", "libx264")
	t.Setenv("DYNAMIC_MAP_GPU_SAMPLER", "1")
	installFakeMapRenderer(t, `{"schema":"chronon.dynamic-map-telemetry.v1"}`)
	if _, err := renderDynamicMapVideo(context.Background(), newTestMapPlan("map-cpu-opencl-no-gate")); err == nil || !strings.Contains(err.Error(), "requires the shared RenderingGen GPU gate") {
		t.Fatalf("CPU+OpenCL without GPU admission error = %v; want fail-closed GPU gate refusal", err)
	}
}

func TestDynamicMapGPUSamplerDefaultAndGatePolicy(t *testing.T) {
	production := scriptoverlay.DefaultMapEncodeProfile()
	if sampler := dynamicMapSamplerForProfile(production); sampler != "opencv_opencl" {
		t.Fatalf("production sampler = %q, want opencv_opencl", sampler)
	}
	if !mapRenderRequiresGPU(production, "opencv_opencl", "region_zoom") {
		t.Fatal("production NVENC/OpenCL render must use shared GPU admission")
	}

	cpu, err := scriptoverlay.ResolveMapEncodeProfile("libx264")
	if err != nil {
		t.Fatal(err)
	}
	if sampler := dynamicMapSamplerForProfile(cpu); sampler != "cpu" {
		t.Fatalf("CPU profile sampler = %q, want cpu", sampler)
	}
	if mapRenderRequiresGPU(cpu, "cpu", "region_zoom") {
		t.Fatal("certified CPU sampler+encoder must not require GPU admission")
	}
	if !mapRenderRequiresGPU(cpu, "opencv_opencl", "region_zoom") {
		t.Fatal("CPU encoding with OpenCL sampling still requires GPU admission")
	}
	if !mapRenderRequiresGPU(cpu, "cpu", "ed_yemen_markers") {
		t.Fatal("editorial GPU composition must require shared admission even with CPU sampler")
	}
}

func TestRenderDynamicMapVideoRequiresGPUGateForGPUWork(t *testing.T) {
	installFakeMapRenderer(t, validDynamicMapTelemetry)
	_, err := renderDynamicMapVideo(context.Background(), newTestMapPlan("map-gate-1"))
	if err == nil || !strings.Contains(err.Error(), "requires the shared RenderingGen GPU gate") {
		t.Fatalf("renderDynamicMapVideo error = %v, want the GPU gate refusal", err)
	}
}

func TestRenderDynamicMapVideoReleasesSharedGateAfterRendererFailure(t *testing.T) {
	installFakeMapRenderer(t, validDynamicMapTelemetry)
	dir := filepath.Dir(strings.TrimSpace(os.Getenv("VELOX_GEO_MAP_PLATE_GENERATOR_PATH")))
	renderer := filepath.Join(dir, "map", "render_dynamic_map_overlay.py")
	if err := os.WriteFile(renderer, []byte("raise RuntimeError('intentional renderer failure')\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gate := &observingDynamicMapGPUGate{}
	if _, err := renderDynamicMapVideoWithGate(context.Background(), newTestMapPlan("map-gate-failure"), gate); err == nil {
		t.Fatal("failing renderer unexpectedly succeeded")
	}
	if gate.acquired != 1 || gate.released != 1 || gate.active {
		t.Fatalf("GPU gate lifecycle after renderer failure = %d/%d/%t, want 1/1/false", gate.acquired, gate.released, gate.active)
	}
}

func TestRenderDynamicMapVideoAcquiresAndReleasesSharedGate(t *testing.T) {
	installFakeMapRenderer(t, validDynamicMapTelemetry)
	gate := &observingDynamicMapGPUGate{}
	path, err := renderDynamicMapVideoWithGate(context.Background(), newTestMapPlan("map-gate-held"), gate)
	if err != nil {
		t.Fatalf("render under shared GPU gate: %v", err)
	}
	defer func() { _ = os.RemoveAll(filepath.Dir(path)) }()
	if gate.acquired != 1 || gate.released != 1 || gate.active {
		t.Fatalf("GPU gate lifecycle acquired=%d released=%d active=%t; want 1/1/false", gate.acquired, gate.released, gate.active)
	}
}

func TestRenderDynamicMapVideoCacheHitSkipsGPUGate(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv(dynamicMapVideoCacheDirEnv, cacheRoot)
	installFakeMapRenderer(t, validDynamicMapTelemetry)
	plan := newTestMapPlan("map-cache-gate")
	first, err := renderDynamicMapVideoWithGate(context.Background(), plan, &observingDynamicMapGPUGate{})
	if err != nil {
		t.Fatalf("populate map cache: %v", err)
	}
	_ = os.RemoveAll(filepath.Dir(first))
	gate := &observingDynamicMapGPUGate{}
	second, err := renderDynamicMapVideoWithGate(context.Background(), plan, gate)
	if err != nil {
		t.Fatalf("serve cached map: %v", err)
	}
	defer func() { _ = os.RemoveAll(filepath.Dir(second)) }()
	if gate.acquired != 0 || gate.released != 0 {
		t.Fatalf("cache hit GPU gate lifecycle acquired=%d released=%d; want 0/0", gate.acquired, gate.released)
	}
}

// TestMapSinglePlanEndToEndSubmitsCertifiedRender drives the whole single-map
// boundary: semantic plan → capability backend decision → local certified
// render → content-addressed background fold → semantic queue submit → worker
// artifact, with the map decision report persisted. It fails if any stage
// silently substitutes content, codec or backend.
func TestMapSinglePlanEndToEndSubmitsCertifiedRender(t *testing.T) {
	installFakeMapRenderer(t, validDynamicMapTelemetry)
	t.Setenv("PIPELINEGEN_MAP_CODEC", "")
	t.Setenv("DYNAMIC_MAP_GPU_SAMPLER", "")
	gate := &observingDynamicMapGPUGate{}
	reportDir := t.TempDir()
	t.Setenv("PIPELINEGEN_MAP_REPORT_DIR", reportDir)

	client := &completingMapClient{
		fakeRenderQueueClient: newFakeRenderQueueClient(),
		artifact: &RenderArtifact{
			ID: "art-map-e2e", URL: "https://store/map.mp4",
			SHA256: "e2e", SizeBytes: 8, MimeType: "video/mp4",
		},
	}
	enqueuer, err := NewQueueRenderEnqueuer(client)
	if err != nil {
		t.Fatal(err)
	}
	enqueuer.pollInterval = 1000000
	enqueuer.SetDynamicMapGPUGate(gate)

	plan := newTestMapPlan("map-e2e-1")
	plan.SchemaVersion = capoverlay.SchemaVersionPlan
	ref, err := enqueuer.EnqueueChrononPlan(context.Background(), plan)
	if err != nil {
		t.Fatalf("enqueue single map plan: %v", err)
	}
	if ref.Status != "COMPLETED" || ref.Artifact == nil {
		t.Fatalf("reference = %+v, want COMPLETED with artifact", ref)
	}
	if gate.acquired != 1 || gate.released != 1 || gate.active {
		t.Fatalf("GPU gate lifecycle acquired=%d released=%d active=%t; want 1/1/false", gate.acquired, gate.released, gate.active)
	}

	submitted, ok := client.jobs[ref.JobID]
	if !ok {
		t.Fatalf("the folded map import job %q was not submitted to the queue", ref.JobID)
	}
	var doc dynamicMapImportRequest
	if err := json.Unmarshal(submitted.OverlaySpec, &doc); err != nil {
		t.Fatalf("decode submitted spec: %v", err)
	}
	if doc.SchemaVersion != dynamicMapImportSchema {
		t.Fatalf("submitted schema = %q, want %q", doc.SchemaVersion, dynamicMapImportSchema)
	}
	if doc.Producer != "pipelinegen-map-v2" {
		t.Fatalf("submitted producer = %q, want pipelinegen-map-v2", doc.Producer)
	}
	if doc.PlanID != "map-e2e-1" || doc.VideoID != "map-e2e-1" {
		t.Fatalf("import identity = %q/%q, want map-e2e-1/map-e2e-1", doc.PlanID, doc.VideoID)
	}
	if len(doc.ItemIDs) != 1 || doc.ItemIDs[0] != "map-1" {
		t.Fatalf("import item identity = %v, want [map-1]", doc.ItemIDs)
	}
	// Content-addressed identity crosses the boundary untouched: the import
	// attests the locally rendered bytes, not a re-derived or empty file.
	wantSHA := sha256.Sum256([]byte("fake-mp4"))
	if doc.SourceSHA256 != hex.EncodeToString(wantSHA[:]) {
		t.Fatalf("import sha = %q, want the rendered bytes %q",
			doc.SourceSHA256, hex.EncodeToString(wantSHA[:]))
	}
	if doc.SourceSizeBytes != int64(len("fake-mp4")) {
		t.Fatalf("import size = %d, want %d", doc.SourceSizeBytes, len("fake-mp4"))
	}
	if doc.Width != plan.Width || doc.Height != plan.Height || doc.FPSNum != plan.FPSNum || doc.FPSDen != plan.FPSDen {
		t.Fatalf("import media facts = %dx%d@%d/%d, want %dx%d@%d/%d",
			doc.Width, doc.Height, doc.FPSNum, doc.FPSDen, plan.Width, plan.Height, plan.FPSNum, plan.FPSDen)
	}
	if doc.FrameCount != 120 || doc.DurationUS != 5_000_000 {
		t.Fatalf("import timing = frames:%d duration:%dus, want 120/5s", doc.FrameCount, doc.DurationUS)
	}
	if len(doc.ProducerTelemetry) == 0 {
		t.Fatal("import must carry the producer telemetry")
	}
	manifested := false
	for _, asset := range submitted.Assets {
		if asset.SHA256 == hex.EncodeToString(wantSHA[:]) {
			manifested = true
		}
	}
	if !manifested {
		t.Fatal("the rendered map bytes are missing from the queue asset manifest")
	}

	entries, err := os.ReadDir(reportDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("decision report dir = %v, %v; want exactly one report", entries, err)
	}
	raw, err := os.ReadFile(filepath.Join(reportDir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Schema      string `json:"schema"`
		Seed        string `json:"seed"`
		Backend     string `json:"backend"`
		Composition string `json:"composition"`
		Places      []struct {
			Presented bool `json:"presented"`
		} `json:"places"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("decode decision report: %v", err)
	}
	if report.Schema != "pipelinegen.map-decision.v1" || report.Seed != "map-e2e-1" {
		t.Fatalf("report identity = %q/%q, want pipelinegen.map-decision.v1/map-e2e-1", report.Schema, report.Seed)
	}
	if report.Backend != "python" || report.Composition == "" {
		t.Fatalf("report decisions missing: backend=%q composition=%q", report.Backend, report.Composition)
	}
	if len(report.Places) != 1 || !report.Places[0].Presented {
		t.Fatalf("report places = %+v, want the one presented pin", report.Places)
	}
}

// installCountingMapRenderer is installFakeMapRenderer plus an invocation
// counter: the stand-in renderer appends one byte per process start, so tests
// can pin how many Chronon map renders actually ran.
func installCountingMapRenderer(t *testing.T, telemetry string) func() int {
	t.Helper()
	dir := t.TempDir()
	plate := filepath.Join(dir, "plate-generator.py")
	if err := os.WriteFile(plate, []byte("# path anchor\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rendererDir := filepath.Join(dir, "map")
	if err := os.MkdirAll(rendererDir, 0o700); err != nil {
		t.Fatal(err)
	}
	countFile := filepath.Join(dir, "render-count")
	program := "import argparse,pathlib\np=argparse.ArgumentParser();p.add_argument('--input');p.add_argument('--output');p.add_argument('--summary-output');a=p.parse_args()\n" +
		"pathlib.Path(" + strconv.Quote(countFile) + ").open('ab').write(b'x')\n" +
		"pathlib.Path(a.output).write_bytes(b'fake-mp4')\n" +
		"pathlib.Path(a.summary_output).write_text(" + strconv.Quote(telemetry) + ",encoding='utf-8')\nprint('test diagnostic')\n"
	if err := os.WriteFile(filepath.Join(rendererDir, "render_dynamic_map_overlay.py"), []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VELOX_GEO_MAP_PLATE_GENERATOR_PATH", plate)
	return func() int {
		data, err := os.ReadFile(countFile)
		if err != nil {
			return 0
		}
		return len(data)
	}
}

// TestDynamicMapVideoConcurrentRendersShareOneProcess pins the prepare/
// submission de-duplication (TICKET-OVERLAY-THROUGHPUT (c)): concurrent
// callers for the same map share ONE Chronon map render instead of each
// starting their own — a second render wastes tens of seconds of CPU/GPU and
// doubles the shared GPU-gate hold. Joiners materialize from the durable
// cache.
func TestDynamicMapVideoConcurrentRendersShareOneProcess(t *testing.T) {
	t.Setenv(dynamicMapVideoCacheDirEnv, t.TempDir())
	count := installCountingMapRenderer(t, validDynamicMapTelemetry)
	plan := newTestMapPlan("map-shared-1")

	const callers = 2
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	paths := make(chan string, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			path, err := renderDynamicMapVideoWithGate(context.Background(), plan, &observingDynamicMapGPUGate{})
			if err != nil {
				errs <- err
				return
			}
			paths <- path
		}()
	}
	wg.Wait()
	close(errs)
	close(paths)
	for err := range errs {
		t.Fatalf("concurrent map render: %v", err)
	}
	for path := range paths {
		_ = os.RemoveAll(filepath.Dir(path))
	}
	if got := count(); got != 1 {
		t.Fatalf("Chronon map renders = %d, want 1 (a joiner must take the cache, not a second render)", got)
	}
}

// TestPreMaterializedMapVideoMakesTheSubmissionThin pins TICKET-OVERLAY-
// THROUGHPUT (c) — «le mappe vanno pre-materializzate fuori dal percorso di
// sottomissione»: the heavy map render happens in the PREPARE step, and the
// enqueue's background fold consumes the durable cache entry. The certified
// artifact is produced with exactly ONE Chronon map render, and that render
// happened before the queue submission, not inside it.
func TestPreMaterializedMapVideoMakesTheSubmissionThin(t *testing.T) {
	t.Setenv(dynamicMapVideoCacheDirEnv, t.TempDir())
	count := installCountingMapRenderer(t, validDynamicMapTelemetry)
	t.Setenv("PIPELINEGEN_MAP_CODEC", "")
	t.Setenv("DYNAMIC_MAP_GPU_SAMPLER", "")

	client := &completingMapClient{
		fakeRenderQueueClient: newFakeRenderQueueClient(),
		artifact: &RenderArtifact{
			ID: "art-map-prepare", URL: "https://store/map.mp4",
			SHA256: "prepare", SizeBytes: 8, MimeType: "video/mp4",
		},
	}
	enqueuer, err := NewQueueRenderEnqueuer(client)
	if err != nil {
		t.Fatal(err)
	}
	enqueuer.pollInterval = 1000000
	enqueuer.SetDynamicMapGPUGate(&observingDynamicMapGPUGate{})

	plan := newTestMapPlan("map-prepare-1")
	plan.SchemaVersion = capoverlay.SchemaVersionPlan

	// PREPARE: the heavy render runs here, outside the submission path.
	if err := enqueuer.PreMaterializeDynamicMapVideo(context.Background(), plan); err != nil {
		t.Fatalf("pre-materialize dynamic map video: %v", err)
	}
	if got := count(); got != 1 {
		t.Fatalf("pre-materialization renders = %d, want 1", got)
	}
	// SUBMIT: the fold consumes the pre-materialized cache entry.
	ref, err := enqueuer.EnqueueChrononPlan(context.Background(), plan)
	if err != nil {
		t.Fatalf("enqueue pre-materialized map plan: %v", err)
	}
	if ref.Artifact == nil {
		t.Fatal("enqueue produced no certified artifact")
	}
	if got := count(); got != 1 {
		t.Fatalf("Chronon map renders = %d after submission, want 1 (the fold must consume the pre-materialized cache entry)", got)
	}
}

// newTestMultiMapPlan is a two-map plan with disjoint timed windows — the
// multi-map shape the per-item route separates into one foldable child per
// map. The item shape mirrors the semantic planner (Kind map, TemplateID MAP,
// microsecond timing projected to milliseconds).
func newTestMultiMapPlan(planID string) capoverlay.OverlayPlan {
	mapItem := func(id string, startUS, durationUS int64, pin capoverlay.MapOverlayPin) capoverlay.OverlayItem {
		return capoverlay.OverlayItem{
			ID: id, SceneID: "scene-0", Kind: "map", TemplateID: "MAP",
			StartUS: startUS, DurationUS: durationUS,
			StartMs: startUS / 1000, EndMs: (startUS + durationUS + 999) / 1000,
			Map: &capoverlay.MapOverlay{
				Provider: "local", SourceID: "plate:test", SourceLicense: "test-only",
				Attribution: "test basemap",
				Center:      capoverlay.MapCenter{Latitude: pin.Latitude, Longitude: pin.Longitude},
				Zoom:        6, Width: 1920, Height: 1080,
				// The production dynamic-map shape: the raster is deferred to the
				// renderer, so the plan carries no basemap asset.
				DynamicRendererOnly: true,
				Pins:                []capoverlay.MapOverlayPin{pin},
			},
		}
	}
	pin := func(id, label string, latitude, longitude float64) capoverlay.MapOverlayPin {
		return capoverlay.MapOverlayPin{
			ID: id, Label: label, Latitude: latitude, Longitude: longitude,
			Color: "#E4572E", RadiusPX: 24,
		}
	}
	return capoverlay.OverlayPlan{
		PlanID: planID, VideoID: planID, SchemaVersion: capoverlay.SchemaVersionPlan,
		Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, DurationMS: 10000,
		Items: []capoverlay.OverlayItem{
			mapItem("map-a", 0, 5_000_000, pin("place:rome", "Rome", 41.89, 12.49)),
			mapItem("map-b", 5_000_000, 5_000_000, pin("place:paris", "Paris", 48.86, 2.35)),
		},
	}
}

// TestMultiMapPlanPreMaterializesEveryMapAndSubmitsFromTheDurableCache pins
// the multi-map prepare contract (TICKET-OVERLAY-THROUGHPUT (c)): a plan with
// two maps prepares BOTH outside the submission path with the exact per-item
// identity the fold consumes, the per-item route folds each map from the
// durable cache (one Chronon map render per map, never two), and a rerun of
// the same plan is served entirely from the cache with zero renders.
func TestMultiMapPlanPreMaterializesEveryMapAndSubmitsFromTheDurableCache(t *testing.T) {
	t.Setenv(dynamicMapVideoCacheDirEnv, t.TempDir())
	count := installCountingMapRenderer(t, validDynamicMapTelemetry)
	t.Setenv("PIPELINEGEN_MAP_CODEC", "")
	t.Setenv("DYNAMIC_MAP_GPU_SAMPLER", "")

	client := &completingMapClient{
		fakeRenderQueueClient: newFakeRenderQueueClient(),
		artifact: &RenderArtifact{
			ID: "art-map-multi", URL: "https://store/maps.mp4",
			SHA256: "multi", SizeBytes: 8, MimeType: "video/mp4",
		},
	}
	enqueuer, err := NewQueueRenderEnqueuer(client)
	if err != nil {
		t.Fatal(err)
	}
	enqueuer.pollInterval = 1000000
	enqueuer.SetDynamicMapGPUGate(&observingDynamicMapGPUGate{})

	plan := newTestMultiMapPlan("map-multi-1")

	// PREPARE: a multi-map plan prepares EVERY map, not just single-map plans.
	if err := enqueuer.PreMaterializeDynamicMapVideo(context.Background(), plan); err != nil {
		t.Fatalf("pre-materialize multi-map plan: %v", err)
	}
	if got := count(); got != 2 {
		t.Fatalf("pre-materialization renders = %d, want 2 (one Chronon map render per map)", got)
	}

	// SUBMIT: the per-item route folds each map from the durable cache.
	ref, err := enqueuer.EnqueueChrononPlan(context.Background(), plan)
	if err != nil {
		t.Fatalf("enqueue multi-map plan: %v", err)
	}
	if got := count(); got != 2 {
		t.Fatalf("Chronon map renders = %d after submission, want 2 (each fold must consume its pre-materialized cache entry)", got)
	}
	if len(ref.Items) != 2 {
		t.Fatalf("published items = %d, want 2", len(ref.Items))
	}
	wantOrder := []string{"map-a", "map-b"}
	for index, item := range ref.Items {
		if item.ItemID != wantOrder[index] {
			t.Fatalf("published item[%d] = %q, want %q", index, item.ItemID, wantOrder[index])
		}
		if item.Artifact == nil {
			t.Fatalf("published item %q carries no certified artifact", item.ItemID)
		}
	}
	// Each map was folded as its own producer import naming its own item.
	imported := make(map[string]bool, 2)
	for _, job := range client.jobs {
		var doc dynamicMapImportRequest
		if err := json.Unmarshal(job.OverlaySpec, &doc); err != nil {
			t.Fatalf("decode submitted import spec: %v", err)
		}
		if doc.SchemaVersion != dynamicMapImportSchema {
			t.Fatalf("submitted schema = %q, want %q", doc.SchemaVersion, dynamicMapImportSchema)
		}
		if len(doc.ItemIDs) != 1 {
			t.Fatalf("import %q names %d items, want exactly one map per import", doc.PlanID, len(doc.ItemIDs))
		}
		imported[doc.ItemIDs[0]] = true
	}
	if !imported["map-a"] || !imported["map-b"] {
		t.Fatalf("map imports = %v, want one import naming map-a and one naming map-b", imported)
	}

	// RERUN: the durable cache serves the identical plan with zero renders.
	if _, err := enqueuer.EnqueueChrononPlan(context.Background(), plan); err != nil {
		t.Fatalf("rerun multi-map plan: %v", err)
	}
	if got := count(); got != 2 {
		t.Fatalf("Chronon map renders = %d after rerun, want 2 (the durable cache must serve the rerun)", got)
	}
}

// TestPreMaterializeDynamicMapVideoReleasesItsRenderWorkspaces pins the
// workspace ownership contract: the PREPARE owns the render workspace the map
// renderer hands it and releases it once the durable cache entry is stored
// (each fold materializes its own private workspace from the cache). Before
// this contract every prepare leaked a pipelinegen-dynamic-map-* tree into
// TMPDIR (706 leaked workspaces measured on the production host).
func TestPreMaterializeDynamicMapVideoReleasesItsRenderWorkspaces(t *testing.T) {
	workspaces := t.TempDir()
	t.Setenv("TMPDIR", workspaces)
	t.Setenv(dynamicMapVideoCacheDirEnv, t.TempDir())
	installCountingMapRenderer(t, validDynamicMapTelemetry)
	t.Setenv("PIPELINEGEN_MAP_CODEC", "")
	t.Setenv("DYNAMIC_MAP_GPU_SAMPLER", "")

	enqueuer, err := NewQueueRenderEnqueuer(newFakeRenderQueueClient())
	if err != nil {
		t.Fatal(err)
	}
	enqueuer.SetDynamicMapGPUGate(&observingDynamicMapGPUGate{})

	for name, plan := range map[string]capoverlay.OverlayPlan{
		"single-map": newTestMapPlan("map-ws-single"),
		"multi-map":  newTestMultiMapPlan("map-ws-multi"),
	} {
		if err := enqueuer.PreMaterializeDynamicMapVideo(context.Background(), plan); err != nil {
			t.Fatalf("%s: pre-materialize: %v", name, err)
		}
		entries, err := os.ReadDir(workspaces)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "pipelinegen-dynamic-map-") {
				t.Fatalf("%s: prepare leaked render workspace %s", name, entry.Name())
			}
		}
	}
}
