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

// TestLiveContentShowcaseE2E is an opt-in cross-repository canary for the
// overlay-content upgrades: the full semantic vocabulary, the auto-fit text
// budget (a long phrase must carry a font_size override) and the
// collision-avoiding slot layout (two image overlays with string positions
// and different priorities must not stack). It goes through the production
// queue adapter exactly like a real PipelineGen job.
//
// Run with:
//
//	PIPELINEGEN_RENDERINGGEN_E2E=1 \
//	RENDERINGGEN_GOLDEN_DIR=../RenderingGen/testdata/golden \
//	go test ./internal/platform/renderinggen/ -run TestLiveContentShowcaseE2E -v
func TestLiveContentShowcaseE2E(t *testing.T) {
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
	chart := mustRead(t, filepath.Join(fixtureRoot, "overlay_chart.png"))
	logo := mustRead(t, filepath.Join(fixtureRoot, "logo_pulse.png"))
	backgroundHash := sha256Hex(background)
	globeHash := sha256Hex(globe)
	chartHash := sha256Hex(chart)
	logoHash := sha256Hex(logo)
	if backgroundHash != capoverlay.GoldenBackgroundVideoHash || globeHash != capoverlay.GoldenGlobeHash ||
		chartHash != capoverlay.GoldenChartHash || logoHash != capoverlay.GoldenLogoHash {
		t.Fatalf("golden asset drift: bg=%s globe=%s chart=%s logo=%s", backgroundHash, globeHash, chartHash, logoHash)
	}
	for hash, data := range map[string][]byte{
		backgroundHash: background, globeHash: globe, chartHash: chart, logoHash: logo,
	} {
		putObject(t, storeURL, hash, data)
	}

	jobID := getenvOr("PIPELINEGEN_E2E_JOB_ID", "pipelinegen-live-content")
	// The long phrase (48 runes) exercises the auto-fit font_size override;
	// the two colliding right-slot images (same window, different priorities)
	// exercise the collision-avoiding slot layout. Full vocabulary included.
	plan := capoverlay.OverlayPlan{
		SchemaVersion:   capoverlay.SchemaVersionPlan,
		PlanID:          jobID,
		VideoID:         jobID,
		ProjectID:       "pipelinegen-live",
		Width:           1280,
		Height:          720,
		FPS:             30,
		RendererVersion: "chronon",
		Items: []capoverlay.OverlayItem{
			{ID: "background_video", TemplateID: "VIDEO_BACKGROUND", StartMs: 0, EndMs: 6000, AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "bg", URL: "assets/background.mp4", SHA256: backgroundHash}}},
			{ID: "phrase_long", TemplateID: "IMPORTANT_PHRASE", Text: "UNA FRASE MOLTO LUNGA CHE SUPERA AMPIAMENTE IL BUDGET DI VISUALIZZAZIONE", StartMs: 500, EndMs: 3500, Params: map[string]any{"animation": map[string]any{"preset": "fade_in"}}},
			{ID: "word", TemplateID: "IMPORTANT_WORD", Text: "VELOCITÀ", StartMs: 1500, EndMs: 3500},
			{ID: "number", TemplateID: "NUMBER", Text: "42%", StartMs: 2000, EndMs: 3500},
			{ID: "quote", TemplateID: "QUOTE", Text: "Il futuro è adesso", StartMs: 3500, EndMs: 5000},
			{ID: "image_a", TemplateID: "IMAGE_OVERLAY", StartMs: 800, EndMs: 4500, Params: map[string]any{"position": "right", "priority": 0.9, "box_width": 260, "box_height": 260}, AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "globe", URL: "assets/overlay_globe.png", SHA256: globeHash}}},
			{ID: "image_b", TemplateID: "IMAGE_OVERLAY", StartMs: 1000, EndMs: 4500, Params: map[string]any{"position": "right", "priority": 0.5, "box_width": 260, "box_height": 260}, AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "chart", URL: "assets/overlay_chart.png", SHA256: chartHash}}},
			{ID: "logo", TemplateID: "LOGO", StartMs: 0, EndMs: 6000, Params: map[string]any{"position": "corner", "box_width": 160, "box_height": 160}, AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "logo", URL: "assets/logo_pulse.png", SHA256: logoHash}}},
		},
	}

	// Compile through the production compiler and lock the content upgrades
	// on the compiled document before enqueueing.
	compiled, err := capoverlay.CompileChrononPlan(plan)
	if err != nil {
		t.Fatalf("compile content plan: %v", err)
	}
	fontSizeOK := false
	var imgA, imgB *capoverlay.ChrononLayer
	for i := range compiled.Plan.Layers {
		layer := &compiled.Plan.Layers[i]
		switch layer.ID {
		case "phrase_long":
			if layer.FontSize == 0 {
				t.Fatalf("long phrase compiled without auto-fit font_size: %+v", layer)
			}
			fontSizeOK = true
		case "image_a":
			imgA = layer
		case "image_b":
			imgB = layer
		}
	}
	if !fontSizeOK {
		t.Fatal("missing phrase_long layer")
	}
	if imgA == nil || imgB == nil {
		t.Fatal("missing image layers")
	}
	if len(imgA.Position) != 2 || len(imgB.Position) != 2 {
		t.Fatalf("image layers must carry resolved positions: a=%v b=%v", imgA.Position, imgB.Position)
	}
	if imgA.Position[0] == imgB.Position[0] && imgA.Position[1] == imgB.Position[1] {
		t.Fatalf("collision layout failed: image_a and image_b share %v", imgA.Position)
	}
	t.Logf("content plan compiled: phrase font_size=%.0f image_a=%v image_b=%v",
		compiled.Plan.Layers[1].FontSize, imgA.Position, imgB.Position)

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
	t.Logf("live content showcase PASS: job=%s artifact=%s sha256=%s size=%d duration_us=%d",
		jobID, ref.Artifact.ID, ref.Artifact.SHA256, ref.Artifact.SizeBytes, ref.Artifact.DurationUS)
}
