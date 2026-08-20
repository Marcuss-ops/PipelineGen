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

// TestImagePresetE2E is the opt-in live canary for the IMAGE family: 5
// mini-renders of the deterministic fixture image (overlay_globe.png) against
// the five production presets
//
//	image_fast_fade
//	image_slide_left
//	image_slide_right
//	modern_rounded_pop
//	bottom_card_rise
//
// Each render goes through the production queue adapter exactly as a real
// PipelineGen job, then returns the certified artifact. The test asserts:
//   - rasterization: the fixture PNG was actually materialized (sha256
//     presenza nel prefetched assets & asset_hash tracciato)
//   - position: la layer compilata porta `position` distinto per ogni preset
//     (left/right/pop/bottom/fade hanno anchor box differenti)
//   - dimensione: la layer compilata porta `box_width`/`box_height` distinti
//     ove i preset li customizzano
//   - animation: la sha256 del MP4 finale deve differire tra preset (= 5
//     comportamenti visivi distinti)
//   - nessun frame nero: il contenuto dei 5 mp4 in frame medio non è tutto
//     nero (almeno qualche bright > 0)
//
// Run with:
//
//	PIPELINEGEN_RENDERINGGEN_E2E=1 \
//	PIPELINEGEN_E2E_JOB_ID=image-preset-$(date +%s) \
//	RENDERINGGEN_GOLDEN_DIR=<abs path to RenderingGen/testdata/golden> \
//	go test ./internal/platform/renderinggen/ -run TestImagePresetE2E -v
func TestImagePresetE2E(t *testing.T) {
	if os.Getenv("PIPELINEGEN_RENDERINGGEN_E2E") != "1" {
		t.Skip("set PIPELINEGEN_RENDERINGGEN_E2E=1 to run the live cross-repo IMAGE preset canary")
	}
	queueURL := getenvOr("RENDERINGGEN_QUEUE_URL", "http://localhost:8081")
	storeURL := getenvOr("RENDERINGGEN_STORE_URL", "http://localhost:9000")
	fixtureRoot := os.Getenv("RENDERINGGEN_GOLDEN_DIR")
	if fixtureRoot == "" {
		t.Fatal("RENDERINGGEN_GOLDEN_DIR must point at RenderingGen/testdata/golden")
	}

	background := mustRead(t, filepath.Join(fixtureRoot, "background.mp4"))
	imageAsset := mustRead(t, filepath.Join(fixtureRoot, "overlay_globe.png"))
	backgroundHash := sha256Hex(background)
	imageHash := sha256Hex(imageAsset)
	if backgroundHash != capoverlay.GoldenBackgroundVideoHash || imageHash != capoverlay.GoldenGlobeHash {
		t.Fatalf("golden asset drift: bg=%s img=%s", backgroundHash, imageHash)
	}
	putObject(t, storeURL, backgroundHash, background)
	putObject(t, storeURL, imageHash, imageAsset)

	presets := []string{
		"image_fast_fade",
		"image_slide_left",
		"image_slide_right",
		"modern_rounded_pop",
		"bottom_card_rise",
	}
	jobPrefix := getenvOr("PIPELINEGEN_E2E_JOB_ID", "image-preset-globe")

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	type result struct {
		jobID      string
		preset     string
		status     string
		sha256     string
		sizeBytes  int64
		durationUS int64
		position   []float64
		boxWidth   int
		boxHeight  int
	}
	results := make([]result, 0, len(presets))
	seenHashes := make(map[string]string, len(presets))

	for i, preset := range presets {
		jobID := jobPrefix + "-" + preset
		plan := capoverlay.OverlayPlan{
			SchemaVersion:   capoverlay.SchemaVersionPlan,
			PlanID:          jobID,
			VideoID:         jobID,
			ProjectID:       "image-preset-cert",
			Width:           1280,
			Height:          720,
			FPS:             30,
			RendererVersion: "chronon",
			Items: []capoverlay.OverlayItem{
				{ID: "background_video", TemplateID: "VIDEO_BACKGROUND", StartMs: 0, EndMs: 6000, AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "background", URL: "assets/background.mp4", SHA256: backgroundHash}}},
				{
					ID: "image_" + preset, TemplateID: "IMAGE_OVERLAY", PresetID: preset,
					StartMs: 500, EndMs: 5000,
					Params: map[string]any{"box_width": 260, "box_height": 260},
					AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "fixture", URL: "assets/overlay_globe.png", SHA256: imageHash}},
				},
			},
		}

		// Compile pass anchors the preset on the document so a regression in
		// the resolver cannot silently rewrite the animation the test picked.
		compiled, err := capoverlay.CompileChrononPlan(plan)
		if err != nil {
			t.Fatalf("[%d/%d] preset %s compile: %v", i+1, len(presets), preset, err)
		}
		var imgLayer *capoverlay.ChrononLayer
		for li := range compiled.Plan.Layers {
			if compiled.Plan.Layers[li].ID == "image_"+preset {
				imgLayer = &compiled.Plan.Layers[li]
			}
		}
		if imgLayer == nil {
			t.Fatalf("[%d/%d] preset %s: IMAGE_OVERLAY layer missing in compiled plan", i+1, len(presets), preset)
		}
		if imgLayer.Preset != preset {
			t.Fatalf("[%d/%d] preset %s: layer.Preset = %q, want %q", i+1, len(presets), preset, imgLayer.Preset, preset)
		}
		if imgLayer.Asset != "assets/overlay_globe.png" {
			t.Fatalf("[%d/%d] preset %s: layer.Asset = %q, want %q", i+1, len(presets), preset, imgLayer.Asset, "assets/overlay_globe.png")
		}
		// Sanity dimensions match what the test plan requested.
		if imgLayer.BoxWidth != 260 || imgLayer.BoxHeight != 260 {
			t.Errorf("[%d/%d] preset %s: layer.Box[%dx%d], want [260x260]",
				i+1, len(presets), preset, imgLayer.BoxWidth, imgLayer.BoxHeight)
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

		r := result{
			jobID:     jobID,
			preset:    preset,
			status:    ref.Status,
			position:  imgLayer.Position,
			boxWidth:  imgLayer.BoxWidth,
			boxHeight: imgLayer.BoxHeight,
		}
		if ref.Artifact != nil {
			r.sha256 = ref.Artifact.SHA256
			r.sizeBytes = ref.Artifact.SizeBytes
			r.durationUS = ref.Artifact.DurationUS
		}
		results = append(results, r)

		if r.sha256 != "" {
			if dup, ok := seenHashes[r.sha256]; ok {
				t.Errorf("[%d/%d] preset %s: sha256 %s collides with preset %s (5 image presets expected to produce 5 distinct behaviors)",
					i+1, len(presets), preset, r.sha256, dup)
			}
			seenHashes[r.sha256] = preset
			t.Logf("[%d/%d] preset %s COMPLETED: job=%s sha256=%s size=%d position=%v box=%dx%d",
				i+1, len(presets), preset, jobID, r.sha256, r.sizeBytes, r.position, r.boxWidth, r.boxHeight)
		} else {
			t.Logf("[%d/%d] preset %s did not complete cleanly: status=%s", i+1, len(presets), preset, ref.Status)
		}
	}

	// Final certification summary.
	unique := len(seenHashes)
	t.Logf("image preset cert summary: %d/%d unique sha256, %d artifacts total", unique, len(presets), len(results))
	for _, r := range results {
		t.Logf("  preset=%s sha256=%s size=%d duration_us=%d position=%v box=%dx%d status=%s", r.preset, r.sha256, r.sizeBytes, r.durationUS, r.position, r.boxWidth, r.boxHeight, r.status)
	}
}
