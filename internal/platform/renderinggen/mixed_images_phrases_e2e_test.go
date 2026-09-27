package renderinggen

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// TestFiveImagesTenPhrasesRuntimeE2E exercises the local render path with five
// distinct photos and ten animated phrase overlays. It never submits a Master
// job or requests final worker rendering.
func TestFiveImagesTenPhrasesRuntimeE2E(t *testing.T) {
	if os.Getenv("PIPELINEGEN_RENDERINGGEN_E2E") != "1" {
		t.Skip("set PIPELINEGEN_RENDERINGGEN_E2E=1 to run the local mixed-content canary")
	}
	queueURL := getenvOr("RENDERINGGEN_QUEUE_URL", "http://localhost:8081")
	storeURL := getenvOr("RENDERINGGEN_STORE_URL", "http://localhost:9000")
	fixtureRoot := os.Getenv("RENDERINGGEN_GOLDEN_DIR")
	if fixtureRoot == "" {
		t.Fatal("RENDERINGGEN_GOLDEN_DIR must point at RenderingGen/testdata/golden")
	}
	background := mustRead(t, filepath.Join(fixtureRoot, "Pale-Olive.mp4"))
	backgroundHash := sha256Hex(background)
	putObject(t, storeURL, backgroundHash, background)

	jobID := getenvOr("PIPELINEGEN_E2E_JOB_ID", "five-images-ten-phrases-"+time.Now().UTC().Format("20060102T150405Z"))
	imageNames := []string{
		"mike_tyson.png",
		"joe_frazier.jpg",
		"cus_damato.png",
		"sugar_ray_robinson.jpg",
		"muhammad_ali.jpg",
	}
	renderingGenRoot := filepath.Dir(filepath.Dir(fixtureRoot))
	imageRoot := filepath.Join(renderingGenRoot, "mike_tyson_overlay_test", "preset_overlays_v1", "assets", "entities")
	images := make([]capoverlay.OverlayItem, 0, len(imageNames))
	for i, name := range imageNames {
		data := mustRead(t, filepath.Join(imageRoot, name))
		hash := sha256Hex(data)
		putObject(t, storeURL, hash, data)
		start := int64(i) * 4000
		params := capoverlay.EntityImageParams(1920, 1080)
		params["position"] = []any{100, 250}
		images = append(images, capoverlay.OverlayItem{
			ID: "image-" + name, Kind: "image", TemplateID: "IMAGE_OVERLAY",
			PresetID: "image_slide_left", MotionID: capoverlay.SelectImageMotionAt(jobID, "canary-scene", i),
			StartMs: start, EndMs: start + 4000, Params: params,
			AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: hash, URL: "assets/entities/" + name, SHA256: hash, MediaType: imageMediaType(name)}},
		})
	}

	phraseTexts := []string{
		"A NEW CHAMPION EMERGES", "THE CROWD HOLDS ITS BREATH",
		"TRAINING CHANGES EVERYTHING", "ONE MOMENT DEFINES A CAREER",
		"THE RIVALRY GROWS", "EVERY ROUND RAISES THE STAKES",
		"COURAGE MEETS EXPERIENCE", "THE TURNING POINT ARRIVES",
		"A LEGACY TAKES SHAPE", "THE FINAL BELL RINGS",
	}
	phraseMotions := capoverlay.CertifiedPhraseMotions()
	if len(phraseMotions) < len(phraseTexts) {
		t.Fatalf("certified phrase motion pool has %d entries, need at least %d", len(phraseMotions), len(phraseTexts))
	}
	phrases := make([]capoverlay.OverlayItem, 0, len(phraseTexts))
	for i, phrase := range phraseTexts {
		start := int64(i) * 2000
		phrases = append(phrases, capoverlay.OverlayItem{
			ID: fmt.Sprintf("phrase-%02d", i+1), TemplateID: "IMPORTANT_PHRASE",
			PresetID: "phrase_default", MotionID: phraseMotions[i], Text: phrase,
			StartMs: start, EndMs: start + 1800,
			MotionParams: map[string]any{"enter_frames": 22},
			Params:       map[string]any{"position_x": 960, "position_y": 900},
		})
	}
	items := append(images, phrases...)
	plan := capoverlay.OverlayPlan{
		SchemaVersion: capoverlay.SchemaVersionPlan, PlanID: jobID, VideoID: jobID,
		ProjectID: "five-images-ten-phrases-canary", Width: 1920, Height: 1080,
		FPSNum: 24, FPSDen: 1, DurationMS: 20000, RendererVersion: "chronon",
		Background: &capoverlay.OverlayBackground{Kind: "video", Fit: "cover", Loop: true,
			AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: backgroundHash, URL: "assets/Pale-Olive.mp4", SHA256: backgroundHash, MediaType: "video/mp4"}}},
		Items: items,
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("validate mixed-content canary: %v", err)
	}
	if gotImages, gotPhrases := len(images), len(phrases); gotImages != 5 || gotPhrases != 10 {
		t.Fatalf("canary contents = %d images/%d phrases, want 5/10", gotImages, gotPhrases)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	enqueuer, err := scriptgen.NewQueueRenderEnqueuer(New(queueURL))
	if err != nil {
		t.Fatal(err)
	}
	ref, err := enqueuer.EnqueueChrononPlan(ctx, plan)
	if err != nil {
		t.Fatalf("render five images and ten phrases locally: %v", err)
	}
	if ref.Status != "COMPLETED" || ref.Artifact == nil || ref.Artifact.SHA256 == "" || ref.Artifact.SizeBytes <= 0 {
		t.Fatalf("mixed-content render did not return a certified artifact: status=%s artifact=%+v", ref.Status, ref.Artifact)
	}
	if ref.Artifact.Width != 1920 || ref.Artifact.Height != 1080 || ref.Artifact.DurationUS != 20_000_000 {
		t.Fatalf("mixed-content artifact has wrong dimensions/duration: %+v", ref.Artifact)
	}
	t.Logf("rendered 5 distinct images + 10 phrases locally: job=%s status=%s artifact=%s sha256=%s size=%d; no Master job submitted",
		ref.JobID, ref.Status, ref.Artifact.URL, ref.Artifact.SHA256, ref.Artifact.SizeBytes)
}

func imageMediaType(name string) string {
	if filepath.Ext(name) == ".png" {
		return "image/png"
	}
	return "image/jpeg"
}
