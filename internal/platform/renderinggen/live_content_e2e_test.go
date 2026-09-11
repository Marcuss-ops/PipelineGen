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

// goldenContentFontHashes are the SHA-256 of the deterministic font fixtures
// the worker materializes into the rendering job workspace. The compile emits
// DejaVuSans only; the test ALSO pushes Poppins-Bold up front because Chronon3d's
// VisualPresetRegistry resolves its `font_asset` against either default, and
// the canary must satisfy whichever default the host's build resolves to.
const (
	goldenPoppinsBoldHash = "983676516167748b74de6f4771fb384c664fd913acb8b471122ecacf5da5ea6c"
	goldenPoppinsBoldPath = "assets/fonts/Poppins-Bold.ttf"
)

// TestLiveContentShowcaseE2E is an opt-in cross-repository canary for the
// overlay-content upgrades: the full semantic vocabulary, the resolved
// semantic preset + per-item animation override, and the collision-avoiding
// slot layout (two image overlays with string positions and different
// priorities must not stack). Font sizing is intentionally NOT asserted here:
// Chronon's VisualPresetRegistry + StyleResolver own the font asset and size.
// It goes through the production queue adapter exactly like a real
// PipelineGen job.
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
	poppinsBold := mustRead(t, filepath.Join(fixtureRoot, "Poppins-Bold.ttf"))
	backgroundHash := sha256Hex(background)
	globeHash := sha256Hex(globe)
	chartHash := sha256Hex(chart)
	logoHash := sha256Hex(logo)
	poppinsHash := sha256Hex(poppinsBold)
	if backgroundHash != capoverlay.GoldenBackgroundVideoHash || globeHash != capoverlay.GoldenGlobeHash ||
		chartHash != capoverlay.GoldenChartHash || logoHash != capoverlay.GoldenLogoHash {
		t.Fatalf("golden asset drift: bg=%s globe=%s chart=%s logo=%s", backgroundHash, globeHash, chartHash, logoHash)
	}
	if poppinsHash != goldenPoppinsBoldHash {
		t.Fatalf("font drift: poppins=%s want=%s", poppinsHash, goldenPoppinsBoldHash)
	}
	// Image fixtures AND the Chronon3d default font (Poppins-Bold) all land
	// in the same content-addressed store so the worker materialize step
	// can resolve them by hash.
	for hash, data := range map[string][]byte{
		backgroundHash: background, globeHash: globe, chartHash: chart, logoHash: logo,
		poppinsHash: poppinsBold,
	} {
		putObject(t, storeURL, hash, data)
	}

	jobID := getenvOr("PIPELINEGEN_E2E_JOB_ID", "pipelinegen-live-content")
	// The long phrase exercises the resolved semantic preset + the per-item
	// animation override; the two colliding right-slot images (same window,
	// different priorities) exercise the collision-avoiding slot layout.
	// Full vocabulary included. Text layers carry explicit position +
	// box_width + box_height because Chronon's preset registry otherwise
	// cannot resolve a "collision-free safe-area anchor" for them.
	plan := capoverlay.OverlayPlan{
		SchemaVersion: capoverlay.SchemaVersionPlan,
		PlanID:        jobID,
		VideoID:       jobID,
		ProjectID:     "pipelinegen-live",
		Width:         1280,
		Height:        720,
		FPSNum:        30, FPSDen: 1,
		RendererVersion: "chronon",
		Items: []capoverlay.OverlayItem{
			{ID: "background_video", TemplateID: "VIDEO_BACKGROUND", StartMs: 0, EndMs: 6000, AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "bg", URL: "assets/background.mp4", SHA256: backgroundHash}}},
			{ID: "phrase_long", TemplateID: "IMPORTANT_PHRASE", Text: "QUESTO CAMBIA TUTTO", StartMs: 500, EndMs: 1400, Params: map[string]any{"animation": map[string]any{"preset": "fade_in"}, "position": []any{640, 280}, "box_width": 800, "box_height": 120}},
			{ID: "word", TemplateID: "IMPORTANT_WORD", Text: "APPLE", StartMs: 1500, EndMs: 1900, Params: map[string]any{"position": []any{640, 360}, "box_width": 400, "box_height": 100}},
			{ID: "number", TemplateID: "NUMBER", Text: "42%", StartMs: 1950, EndMs: 3450, Params: map[string]any{"position": []any{640, 480}, "box_width": 200, "box_height": 80}},
			{ID: "quote", TemplateID: "QUOTE", Text: "FUTURO ADESSO", StartMs: 3500, EndMs: 5000, Params: map[string]any{"position": []any{640, 240}, "box_width": 600, "box_height": 120}},
			{ID: "image_a", TemplateID: "IMAGE_OVERLAY", StartMs: 800, EndMs: 4500, Params: map[string]any{"position": []any{912, 230}, "priority": 0.9, "box_width": 200, "box_height": 200}, AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "globe", URL: "assets/overlay_globe.png", SHA256: globeHash}}},
			{ID: "image_b", TemplateID: "IMAGE_OVERLAY", StartMs: 800, EndMs: 4500, Params: map[string]any{"position": []any{168, 230}, "priority": 0.5, "box_width": 200, "box_height": 200}, AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "chart", URL: "assets/overlay_chart.png", SHA256: chartHash}}},
			{ID: "logo", TemplateID: "LOGO", StartMs: 0, EndMs: 6000, Params: map[string]any{"position": "corner", "box_width": 160, "box_height": 160}, AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "logo", URL: "assets/logo_pulse.png", SHA256: logoHash}}},
		},
	}

	// Lock the content upgrades on the SEMANTIC plan before enqueueing; the
	// visual lowering (layer geometry, collision slot layout, animation
	// projection) is RenderingGen's compiler and is asserted by its tests.
	itemByID := map[string]capoverlay.OverlayItem{}
	for _, item := range plan.Items {
		itemByID[item.ID] = item
	}
	phraseItem, ok := itemByID["phrase_long"]
	if !ok {
		t.Fatal("missing phrase_long item")
	}
	if phraseItem.PresetID == "" {
		t.Fatalf("long phrase compiled without a semantic preset: %+v", phraseItem)
	}
	if itemByID["image_a"].PresetID == "" || itemByID["image_b"].PresetID == "" {
		t.Fatalf("image overlays must carry image presets: a=%q b=%q", itemByID["image_a"].PresetID, itemByID["image_b"].PresetID)
	}
	t.Logf("semantic content plan: phrase preset=%s animation=%v", phraseItem.PresetID, phraseItem.Params["animation"])

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	enqueuer, err := scriptgen.NewQueueRenderEnqueuer(New(queueURL))
	if err != nil {
		t.Fatal(err)
	}
	ref, err := enqueuer.EnqueueChrononPlan(ctx, plan)
	if err != nil {
		t.Fatalf("enqueue render job: %v", err)
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
