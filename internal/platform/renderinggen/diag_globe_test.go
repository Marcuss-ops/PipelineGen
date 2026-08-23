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

// TestDiagGlobeOverlay is a one-off diagnostic that mirrors
// TestLivePipelineGenToRenderingGen but uses overlay_globe.png instead of
// apple.png, to confirm whether image overlays other than apple also render.
func TestDiagGlobeOverlay(t *testing.T) {
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

	jobID := getenvOr("PIPELINEGEN_E2E_JOB_ID", "diag-globe-overlay")
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
			{ID: "background", TemplateID: "BACKGROUND", StartMs: 0, EndMs: 5000, AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "background", URL: "assets/background.jpg", SHA256: backgroundHash}}},
			{ID: "image_overlay", TemplateID: "IMAGE_OVERLAY", StartMs: 500, EndMs: 4500, Params: map[string]any{"position": []any{640, 360}, "box_width": 400, "box_height": 400}, AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "globe", URL: "assets/overlay_globe.png", SHA256: globeHash}}},
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
	t.Logf("job=%s status=%s artifact=%+v", ref.JobID, ref.Status, ref.Artifact)
	if ref.Artifact != nil {
		// Download the artifact locally for visual inspection by the caller
		out := "/tmp/diag-globe.mp4"
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
