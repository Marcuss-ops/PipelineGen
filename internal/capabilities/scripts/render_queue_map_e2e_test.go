package scriptgeneration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
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
	stored := c.fakeRenderQueueClient.jobs[job.ID]
	stored.State = "completed"
	stored.Artifact = c.artifact
	c.fakeRenderQueueClient.jobs[job.ID] = stored
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
	cpuTelemetry := `{"schema":"chronon.dynamic-map-telemetry.v1","frames":120,"dimensions":{"width":1920,"height":1080},"fps":{"num":24,"den":1},"tile":{"tile_disk_cache_hits":7,"tile_disk_cache_misses":0,"tile_network_fetches":0,"tile_fallbacks":0,"tile_bytes_downloaded":0,"tile_fetch_ms":12.0,"prefetch_ms":3.0,"prefetch_tiles_requested":7,"prefetch_tiles_memory_hits":0,"late_tile_fetches":0},"plates":{"prepare_ms":4.0,"plate_compose_ms":1.0,"plate_count":11,"plate_bytes":42,"required_tile_count":7,"prefetch_tiles_fetched":7,"prefetch_tiles_memory_hits":0},"gate_ms":2.0,"frame_pipeline_s":3.0,"render_encode_wall_s":3.5,"post_frame_tail_s":0.5,"engine_fallback_frames":0,"output_bytes":8,"video_encoder":"libx264","gpu_encoder":false,"renderer_wall_s":9.0}`
	installFakeMapRenderer(t, cpuTelemetry)
	t.Setenv("PIPELINEGEN_MAP_CODEC", "libx264")
	path, err := renderDynamicMapVideo(context.Background(), newTestMapPlan("map-cpu-1"))
	if err != nil {
		t.Fatalf("declared CPU profile with CPU telemetry must render: %v", err)
	}
	defer os.RemoveAll(filepath.Dir(path))
}

func TestRenderDynamicMapVideoRefusesCrossProfileTelemetry(t *testing.T) {
	installFakeMapRenderer(t, validDynamicMapTelemetry)
	t.Setenv("PIPELINEGEN_MAP_CODEC", "libx264")
	if _, err := renderDynamicMapVideo(context.Background(), newTestMapPlan("map-cross-1")); err == nil {
		t.Fatal("NVENC telemetry under a declared CPU profile must fail, not certify")
	}
}

func TestRenderDynamicMapVideoRefusesUnknownCodec(t *testing.T) {
	installFakeMapRenderer(t, validDynamicMapTelemetry)
	t.Setenv("PIPELINEGEN_MAP_CODEC", "hevc_nvenc")
	if _, err := renderDynamicMapVideo(context.Background(), newTestMapPlan("map-badcodec-1")); err == nil {
		t.Fatal("an uncertified codec declaration must fail before any render")
	}
}

// TestMapSinglePlanEndToEndSubmitsCertifiedRender drives the whole single-map
// boundary: semantic plan → capability backend decision → local certified
// render → content-addressed background fold → semantic queue submit → worker
// artifact, with the map decision report persisted. It fails if any stage
// silently substitutes content, codec or backend.
func TestMapSinglePlanEndToEndSubmitsCertifiedRender(t *testing.T) {
	installFakeMapRenderer(t, validDynamicMapTelemetry)
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

	plan := newTestMapPlan("map-e2e-1")
	plan.SchemaVersion = capoverlay.SchemaVersionPlan
	ref, err := enqueuer.EnqueueChrononPlan(context.Background(), plan)
	if err != nil {
		t.Fatalf("enqueue single map plan: %v", err)
	}
	if ref.Status != "COMPLETED" || ref.Artifact == nil {
		t.Fatalf("reference = %+v, want COMPLETED with artifact", ref)
	}

	submitted, ok := client.jobs["map-e2e-1"]
	if !ok {
		t.Fatal("the folded map job was not submitted to the queue")
	}
	var doc struct {
		SchemaVersion string `json:"schema_version"`
		Items         []any  `json:"items"`
		Background    *struct {
			Kind      string `json:"kind"`
			AssetRefs []struct {
				SHA256 string `json:"sha256"`
			} `json:"asset_refs"`
		} `json:"background"`
	}
	if err := json.Unmarshal(submitted.OverlaySpec, &doc); err != nil {
		t.Fatalf("decode submitted spec: %v", err)
	}
	if doc.SchemaVersion != capoverlay.SchemaVersionPlan {
		t.Fatalf("submitted schema = %q, want %q", doc.SchemaVersion, capoverlay.SchemaVersionPlan)
	}
	if len(doc.Items) != 0 {
		t.Fatalf("folded single-map spec carries %d items, want none (background fold)", len(doc.Items))
	}
	if doc.Background == nil || doc.Background.Kind != "video" || len(doc.Background.AssetRefs) != 1 {
		t.Fatalf("folded spec background = %+v, want one video asset", doc.Background)
	}
	// Content-addressed identity crosses the boundary untouched: the queued
	// background IS the locally rendered bytes.
	wantSHA := sha256.Sum256([]byte("fake-mp4"))
	if doc.Background.AssetRefs[0].SHA256 != hex.EncodeToString(wantSHA[:]) {
		t.Fatalf("background sha = %q, want the rendered bytes %q",
			doc.Background.AssetRefs[0].SHA256, hex.EncodeToString(wantSHA[:]))
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
