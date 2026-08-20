package renderinggen

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// TestPresetCertificationMatrixE2E is the opt-in live mini-render harness for
// the 16-preset matrix. Each subtest renders one 5s cell through the production
// queue adapter and certifies the artifact (sha256, size_bytes, 1280x720, 5s).
// The pixel and Drive columns are inspected manually/separately per cell.
//
// Run with:
//
//	PIPELINEGEN_RENDERINGGEN_E2E=1 \
//	RENDERINGGEN_GOLDEN_DIR=<abs path to RenderingGen/testdata/golden> \
//	go test ./internal/platform/renderinggen/ -run TestPresetCertificationMatrixE2E -v
func TestPresetCertificationMatrixE2E(t *testing.T) {
	if os.Getenv("PIPELINEGEN_RENDERINGGEN_E2E") != "1" {
		t.Skip("set PIPELINEGEN_RENDERINGGEN_E2E=1 to run the live cross-repo canary")
	}
	queueURL := getenvOr("RENDERINGGEN_QUEUE_URL", "http://localhost:8081")
	storeURL := getenvOr("RENDERINGGEN_STORE_URL", "http://localhost:9000")
	fixtureRoot := os.Getenv("RENDERINGGEN_GOLDEN_DIR")
	if fixtureRoot == "" {
		t.Fatal("RENDERINGGEN_GOLDEN_DIR must point at RenderingGen/testdata/golden")
	}

	background := mustRead(t, filepath.Join(fixtureRoot, "background.mp4"))
	globe := mustRead(t, filepath.Join(fixtureRoot, "overlay_globe.png"))
	backgroundHash := sha256Hex(background)
	globeHash := sha256Hex(globe)
	if backgroundHash != capoverlay.GoldenBackgroundVideoHash || globeHash != capoverlay.GoldenGlobeHash {
		t.Fatalf("golden asset drift: bg=%s globe=%s", backgroundHash, globeHash)
	}
	putObject(t, storeURL, backgroundHash, background)
	putObject(t, storeURL, globeHash, globe)

	baseJobID := getenvOr("PIPELINEGEN_E2E_JOB_ID", "preset-certification")
	enqueuer, err := scriptgen.NewQueueRenderEnqueuer(New(queueURL))
	if err != nil {
		t.Fatal(err)
	}

	for _, cell := range capoverlay.PresetCertificationMatrix() {
		cell := cell
		t.Run(cell.Family+"_"+cell.Preset, func(t *testing.T) {
			jobID := baseJobID + "-" + cell.Family + "-" + cell.Preset
			plan := capoverlay.BuildPresetCertificationPlan(cell, jobID)

			compiled, err := capoverlay.CompileChrononPlan(plan)
			if err != nil {
				t.Fatalf("compile %s/%s: %v", cell.Family, cell.Preset, err)
			}
			for i := range compiled.Plan.Layers {
				if compiled.Plan.Layers[i].ID != "background_video" && compiled.Plan.Layers[i].Preset != cell.Preset {
					t.Fatalf("compiled preset = %q, want %q", compiled.Plan.Layers[i].Preset, cell.Preset)
				}
			}

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			ref, err := enqueuer.EnqueueChrononPlan(ctx, plan)
			if err != nil {
				t.Fatalf("enqueue %s/%s: %v", cell.Family, cell.Preset, err)
			}
			if ref.JobID != jobID || ref.Status != "COMPLETED" || ref.Artifact == nil {
				t.Fatalf("unexpected live render reference: %+v", ref)
			}
			if ref.Artifact.SHA256 == "" || ref.Artifact.SizeBytes <= 0 || ref.Artifact.Width != 1280 || ref.Artifact.Height != 720 || ref.Artifact.DurationUS != 5_000_000 {
				t.Fatalf("live artifact is not certified: %+v", ref.Artifact)
			}
			t.Logf("matrix cell %s/%s PASS: sha256=%s size=%d duration_us=%d",
				cell.Family, cell.Preset, ref.Artifact.SHA256, ref.Artifact.SizeBytes, ref.Artifact.DurationUS)
		})
	}
}
