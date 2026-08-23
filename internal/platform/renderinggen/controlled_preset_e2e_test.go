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

// TestControlledPresetE2E is the opt-in live canary for the controlled
// certification scenario: one image + one phrase + one word, each with an
// explicit preset (image_slide_left / clean_slide_up / snap_scale). It goes
// through the production queue adapter exactly like a real PipelineGen job.
//
// Run with:
//
//	PIPELINEGEN_RENDERINGGEN_E2E=1 \
//	PIPELINEGEN_E2E_JOB_ID=controlled-preset-image-phrase-word-<unique> \
//	RENDERINGGEN_GOLDEN_DIR=<abs path to RenderingGen/testdata/golden> \
//	go test ./internal/platform/renderinggen/ -run TestControlledPresetE2E -v
func TestControlledPresetE2E(t *testing.T) {
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
	fixture := mustRead(t, filepath.Join(fixtureRoot, "overlay_globe.png"))
	backgroundHash := sha256Hex(background)
	fixtureHash := sha256Hex(fixture)
	if backgroundHash != capoverlay.GoldenBackgroundVideoHash || fixtureHash != capoverlay.GoldenGlobeHash {
		t.Fatalf("golden asset drift: bg=%s fixture=%s", backgroundHash, fixtureHash)
	}
	putObject(t, storeURL, backgroundHash, background)
	putObject(t, storeURL, fixtureHash, fixture)

	jobID := getenvOr("PIPELINEGEN_E2E_JOB_ID", "controlled-preset-image-phrase-word")
	plan := capoverlay.OverlayPlan{
		SchemaVersion: capoverlay.SchemaVersionPlan,
		PlanID:        jobID,
		VideoID:       jobID,
		ProjectID:     "controlled-preset-cert",
		Width:         1280,
		Height:        720,
		FPSNum:        30, FPSDen: 1,
		RendererVersion: "chronon",
		Items: []capoverlay.OverlayItem{
			{ID: "background_video", TemplateID: "VIDEO_BACKGROUND", StartMs: 0, EndMs: 6000, AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "background", URL: "assets/background.mp4", SHA256: backgroundHash}}},
			{ID: "image", TemplateID: "IMAGE_OVERLAY", PresetID: "image_slide_left", StartMs: 500, EndMs: 3500, Params: map[string]any{"box_width": 260, "box_height": 260}, AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "fixture", URL: "assets/overlay_globe.png", SHA256: fixtureHash}}},
			{ID: "phrase", TemplateID: "IMPORTANT_PHRASE", PresetID: "clean_slide_up", StartMs: 1000, EndMs: 4000, Text: "MICHAEL JORDAN CHANGED BASKETBALL"},
			{ID: "word", TemplateID: "IMPORTANT_WORD", PresetID: "snap_scale", StartMs: 2000, EndMs: 4000, Text: "LEGEND"},
		},
	}

	// Lock the controlled presets on the compiled document before enqueueing.
	compiled, err := capoverlay.CompileChrononPlan(plan)
	if err != nil {
		t.Fatalf("compile controlled preset plan: %v", err)
	}
	wantPresets := map[string]string{
		"image":  "image_slide_left",
		"phrase": "clean_slide_up",
		"word":   "snap_scale",
	}
	for _, layer := range compiled.Plan.Layers {
		if w, ok := wantPresets[layer.ID]; ok && layer.Preset != w {
			t.Fatalf("layer %s preset = %q, want %q", layer.ID, layer.Preset, w)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	enqueuer, err := scriptgen.NewQueueRenderEnqueuer(New(queueURL))
	if err != nil {
		t.Fatal(err)
	}
	ref, err := enqueuer.EnqueueChrononPlan(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	if ref.JobID != jobID || ref.Status != "COMPLETED" || ref.Artifact == nil {
		t.Fatalf("unexpected live render reference: %+v", ref)
	}
	if ref.Artifact.SHA256 == "" || ref.Artifact.SizeBytes <= 0 || ref.Artifact.Width != 1280 || ref.Artifact.Height != 720 || ref.Artifact.DurationUS != 6_000_000 {
		t.Fatalf("live artifact is not certified: %+v", ref.Artifact)
	}
	t.Logf("controlled preset E2E PASS: job=%s artifact=%s sha256=%s size=%d duration_us=%d",
		jobID, ref.Artifact.ID, ref.Artifact.SHA256, ref.Artifact.SizeBytes, ref.Artifact.DurationUS)
}
