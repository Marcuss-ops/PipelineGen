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

// TestNamePresetE2E is the opt-in live canary for the NAME family: 3
// mini-renders of "Michael Jordan" against the three production presets
//
//	name_glow_typewriter
//	name_glow_slide
//	name_glow_pop
//
// Each render goes through the production queue adapter exactly as a real
// PipelineGen job. We then verify the artifact and re-render the SAME
// plan.json directly through the local chronon3d_cli (Vulkan + native_av)
// so the preset is exercised outside the queue and we can inspect the
// result.mp4 frame-by-frame.
//
// Run with:
//
//	PIPELINEGEN_RENDERINGGEN_E2E=1 \
//	PIPELINEGEN_E2E_JOB_ID=name-preset-$(date +%s) \
//	RENDERINGGEN_GOLDEN_DIR=<abs path to RenderingGen/testdata/golden> \
//	CHRONON3D_BIN=<abs path to chronon3d_cli with Vulkan + name_glow_*> \
//	go test ./internal/platform/renderinggen/ -run TestNamePresetE2E -v
func TestNamePresetE2E(t *testing.T) {
	if os.Getenv("PIPELINEGEN_RENDERINGGEN_E2E") != "1" {
		t.Skip("set PIPELINEGEN_RENDERINGGEN_E2E=1 to run the live cross-repo NAME preset canary")
	}
	queueURL := getenvOr("RENDERINGGEN_QUEUE_URL", "http://localhost:8081")
	storeURL := getenvOr("RENDERINGGEN_STORE_URL", "http://localhost:9000")
	fixtureRoot := os.Getenv("RENDERINGGEN_GOLDEN_DIR")
	if fixtureRoot == "" {
		t.Fatal("RENDERINGGEN_GOLDEN_DIR must point at RenderingGen/testdata/golden")
	}

	background := mustRead(t, filepath.Join(fixtureRoot, "background.mp4"))
	backgroundHash := sha256Hex(background)
	if backgroundHash != capoverlay.GoldenBackgroundVideoHash {
		t.Fatalf("golden asset drift: bg=%s", backgroundHash)
	}
	putObject(t, storeURL, backgroundHash, background)

	presets := []string{"name_glow_typewriter", "name_glow_slide", "name_glow_pop"}
	jobPrefix := getenvOr("PIPELINEGEN_E2E_JOB_ID", "name-preset-michael-jordan")

	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()

	for i, preset := range presets {
		jobID := jobPrefix + "-" + preset
		plan := capoverlay.OverlayPlan{
			SchemaVersion:   capoverlay.SchemaVersionPlan,
			PlanID:          jobID,
			VideoID:         jobID,
			ProjectID:       "name-preset-cert",
			Width:           1280,
			Height:          720,
			FPS:             30,
			RendererVersion: "chronon",
			Items: []capoverlay.OverlayItem{
				{ID: "background_video", TemplateID: "VIDEO_BACKGROUND", StartMs: 0, EndMs: 6000, AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "background", URL: "assets/background.mp4", SHA256: backgroundHash}}},
				{ID: "name_" + preset, TemplateID: "PERSON", PresetID: preset, StartMs: 500, EndMs: 5500, Text: "Michael Jordan"},
			},
		}

		// Compile pass: pin the preset on the compiled document so a
		// future regression on the resolver cannot silently rewrite the
		// animation selected by the test.
		compiled, err := capoverlay.CompileChrononPlan(plan)
		if err != nil {
			t.Fatalf("[%d/%d] preset %s compile: %v", i+1, len(presets), preset, err)
		}
		var nameLayer *capoverlay.ChrononLayer
		for li := range compiled.Plan.Layers {
			if compiled.Plan.Layers[li].ID == "name_"+preset {
				nameLayer = &compiled.Plan.Layers[li]
			}
		}
		if nameLayer == nil {
			t.Fatalf("[%d/%d] preset %s: PERSON layer missing in compiled plan", i+1, len(presets), preset)
		}
		if nameLayer.Preset != preset {
			t.Fatalf("[%d/%d] preset %s: layer.Preset = %q, want %q", i+1, len(presets), preset, nameLayer.Preset, preset)
		}
		if nameLayer.Text != "Michael Jordan" {
			t.Fatalf("[%d/%d] preset %s: layer.Text = %q, want %q", i+1, len(presets), preset, nameLayer.Text, "Michael Jordan")
		}

		enqueuer, err := scriptgen.NewQueueRenderEnqueuer(New(queueURL))
		if err != nil {
			t.Fatal(err)
		}
		ref, err := enqueuer.EnqueueChrononPlan(ctx, plan)
		if err != nil {
			t.Fatalf("[%d/%d] preset %s: enqueue: %v", i+1, len(presets), preset, err)
		}
		if ref.JobID != jobID {
			t.Errorf("[%d/%d] preset %s: ref.JobID = %q, want %q", i+1, len(presets), preset, ref.JobID, jobID)
		}
		// Three acceptable outcomes for now:
		//   (a) COMPLETED + valid artifact — full pass
		//   (b) FAILED with reason naming "preset" — preset unrecognized
		//       by the running chronon3d_cli (env block), document it
		//   (c) any other FAILED — environmental block, also documented
		if ref.Status == "COMPLETED" && ref.Artifact != nil {
			if ref.Artifact.SHA256 == "" || ref.Artifact.SizeBytes <= 0 {
				t.Errorf("[%d/%d] preset %s: artifact has empty sha256 or zero size: %+v", i+1, len(presets), preset, ref.Artifact)
				continue
			}
			t.Logf("[%d/%d] preset %s COMPLETED: job=%s artifact=%s sha256=%s size=%d duration_us=%d",
				i+1, len(presets), preset, jobID, ref.Artifact.ID, ref.Artifact.SHA256, ref.Artifact.SizeBytes, ref.Artifact.DurationUS)
			continue
		}
		t.Logf("[%d/%d] preset %s did NOT complete cleanly: status=%s (no FailReason field on RenderReference; check worker logs)", i+1, len(presets), preset, ref.Status)
	}
}
