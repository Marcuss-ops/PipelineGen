package renderinggen

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

var httpClient = &http.Client{Timeout: 10 * time.Second}

func fetchBrokerJob(t *testing.T, queueURL, jobID string) []byte {
	t.Helper()
	resp, err := httpClient.Get(queueURL + "/jobs/" + jobID)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// TestRenderMetricsDiagnostic measures the PipelineGen-side completion wait
// against the broker wall time for the SAME job. The two must add up so that
// render_ms + completion_wait_ms correctly attribute time: the former is
// worker-measured Chronon time, the latter is PipelineGen queue polling.
//
// In this run the worker remoto does NOT populate render_ms/encode_ms, so we
// expect artifact.render_ms == 0. The completion_wait_ms is reconstructed by
// subtracting started_at from completed_at in the broker payload.
func TestRenderMetricsDiagnostic(t *testing.T) {
	if os.Getenv("PIPELINEGEN_RENDERINGGEN_E2E") != "1" {
		t.Skip("set PIPELINEGEN_RENDERINGGEN_E2E=1 to run")
	}
	queueURL := getenvOr("RENDERINGGEN_QUEUE_URL", "http://localhost:8081")
	storeURL := getenvOr("RENDERINGGEN_STORE_URL", "http://localhost:9000")
	fixtureRoot := os.Getenv("RENDERINGGEN_GOLDEN_DIR")
	if fixtureRoot == "" {
		t.Fatal("RENDERINGGEN_GOLDEN_DIR required")
	}
	background := mustRead(t, filepath.Join(fixtureRoot, "background.jpg"))
	globe := mustRead(t, filepath.Join(fixtureRoot, "overlay_globe.png"))
	backgroundHash := sha256Hex(background)
	globeHash := sha256Hex(globe)
	putObject(t, storeURL, backgroundHash, background)
	putObject(t, storeURL, globeHash, globe)

	jobID := "metrics-diag-" + time.Now().Format("20060102-150405")
	plan := capoverlay.OverlayPlan{
		SchemaVersion: capoverlay.SchemaVersionPlan,
		PlanID:        jobID,
		VideoID:       jobID,
		ProjectID:     "metrics-diag",
		Width:         1280, Height: 720, FPS: 30,
		RendererVersion: "chronon",
		Items: []capoverlay.OverlayItem{
			{ID: "background", TemplateID: "BACKGROUND", StartMs: 0, EndMs: 4000, AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "background", URL: "assets/background.jpg", SHA256: backgroundHash}}},
			{ID: "image_mj", TemplateID: "IMAGE_OVERLAY", StartMs: 500, EndMs: 3500, Params: map[string]any{"position": []any{640, 360}, "box_width": 400, "box_height": 400}, AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "mj_photo", URL: "assets/overlay_globe.png", SHA256: globeHash}}},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	enqueuer, err := scriptgen.NewQueueRenderEnqueuer(New(queueURL))
	if err != nil {
		t.Fatal(err)
	}

	t0 := time.Now()
	rref, err := enqueuer.EnqueueChrononPlan(ctx, plan)
	pipelinegenWallMS := time.Since(t0).Milliseconds()
	if err != nil {
		t.Fatalf("EnqueueChrononPlan: %v", err)
	}
	if rref.JobID != jobID {
		t.Errorf("jobID mismatch: %q != %q", rref.JobID, jobID)
	}

	time.Sleep(500 * time.Millisecond)
	brokerJSON := fetchBrokerJob(t, queueURL, jobID)
	var brokerJob struct {
		StartedAt   string                    `json:"started_at"`
		CompletedAt string                    `json:"completed_at"`
		State       string                    `json:"state"`
		Attempts    int                       `json:"attempts"`
		Artifact    *scriptgen.RenderArtifact `json:"artifact,omitempty"`
	}
	if err := json.Unmarshal(brokerJSON, &brokerJob); err != nil {
		t.Fatal(err)
	}
	startedT, _ := time.Parse(time.RFC3339Nano, brokerJob.StartedAt)
	completedT, _ := time.Parse(time.RFC3339Nano, brokerJob.CompletedAt)
	brokerWallMS := completedT.Sub(startedT).Milliseconds()

	art := brokerJob.Artifact
	t.Logf("=== METRICS DIAGNOSTIC ===")
	t.Logf("job                = %s", jobID)
	t.Logf("attempts           = %d", brokerJob.Attempts)
	t.Logf("--- PipelineGen side ---")
	t.Logf("pipelinegen_wall_ms (EnqueueChrononPlan total) = %d", pipelinegenWallMS)
	t.Logf("--- Broker side ---")
	t.Logf("broker_started_at  = %s", brokerJob.StartedAt)
	t.Logf("broker_completed_at= %s", brokerJob.CompletedAt)
	t.Logf("broker_wall_ms     = (completed - started) = %d", brokerWallMS)
	t.Logf("polling_overhead   = (pipelinegen_wall - broker_wall) = %d ms", pipelinegenWallMS-brokerWallMS)
	t.Logf("--- Worker-reported Chronon phases (artifact) ---")
	if art != nil {
		t.Logf("render_ms          = %d   (Chronon render phase)", art.RenderMS)
		t.Logf("encode_ms          = %d   (encode phase)", art.EncodeMS)
		t.Logf("frame_count        = %d", art.FrameCount)
		t.Logf("size_bytes         = %d", art.SizeBytes)
	} else {
		t.Logf("artifact is NIL")
	}

	t.Logf("--- SEPARATION CHECK ---")
	t.Logf("PipelineGen completion_wait_ms ~= broker_wall_ms = %d", brokerWallMS)
	t.Logf("Worker render_ms = %d  (0 means worker did NOT populate)", art.RenderMS)
	if art.RenderMS == 0 {
		t.Logf("WARN: artifact.render_ms is 0 -> worker remoto not reporting Chronon render duration.")
		t.Logf("      The broker-reported broker_wall_ms includes BOTH Chronon render AND any worker-internal work.")
		t.Logf("      To correctly separate, the worker must populate render_ms and encode_ms.")
	}
}
