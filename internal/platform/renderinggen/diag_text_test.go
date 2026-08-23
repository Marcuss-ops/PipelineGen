package renderinggen

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// TestDiagSnapScaleText is a diagnostic: only background.jpg + 1 IMPORTANT_WORD
// with preset snap_scale. If the rendered text shows up, fonts are materialized
// by the worker; if it renders as a uniform rectangle, font/asset resolution
// is broken.
func TestDiagSnapScaleText(t *testing.T) {
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
	backgroundHash := sha256Hex(background)
	putObject(t, storeURL, backgroundHash, background)

	jobID := getenvOr("PIPELINEGEN_E2E_JOB_ID", "diag-text-snap-scale")
	plan := capoverlay.OverlayPlan{
		SchemaVersion: capoverlay.SchemaVersionPlan,
		PlanID:        jobID,
		VideoID:       jobID,
		ProjectID:     "pipelinegen-diag",
		Width:         1280,
		Height:        720,
		FPSNum:        30, FPSDen: 1,
		RendererVersion: "chronon",
		Items: []capoverlay.OverlayItem{
			{ID: "background", TemplateID: "BACKGROUND", StartMs: 0, EndMs: 4000, AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "background", URL: "assets/background.jpg", SHA256: backgroundHash}}},
			{ID: "word", TemplateID: "IMPORTANT_WORD", Text: "LEGEND", PresetID: "snap_scale", StartMs: 500, EndMs: 3500, Params: map[string]any{"position": []any{640, 360}, "box_width": 600, "box_height": 200}},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	enqueuer, err := scriptgen.NewQueueRenderEnqueuer(New(queueURL))
	if err != nil {
		t.Fatal(err)
	}
	ref, err := enqueuer.EnqueueChrononPlan(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("job=%s status=%s", ref.JobID, ref.Status)
	if ref.Artifact != nil {
		out := "/tmp/diag-text.mp4"
		req, _ := http.NewRequest(http.MethodGet, storeURL+"/objects/"+ref.Artifact.SHA256, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Logf("download artifact: %v", err)
		} else {
			defer resp.Body.Close()
			buf := &bytes.Buffer{}
			io.Copy(buf, resp.Body)
			os.WriteFile(out, buf.Bytes(), 0o644)
			t.Logf("artifact saved to %s (%d bytes)", out, buf.Len())
		}
	}
}
