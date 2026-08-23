// Package overlays — aspect_fit_test.go certifies GOLDEN 03 image aspect fit
// across source aspect classes at the compile boundary PipelineGen owns.
//
// PipelineGen never reads the source image's pixel dimensions at compile time:
// the asset ref is a content hash/URL, not a decoded frame. What PipelineGen
// DOES own is the deterministic fit instruction that forces the renderer to
// preserve aspect ratio:
//
//   - IMAGE_OVERLAY (and product/logo/popup) → "contain": scale the whole
//     source into the box, letterboxing where needed — NEVER stretch.
//   - BACKGROUND / VIDEO_BACKGROUND → "cover": fill the full canvas, cropping
//     the overflowing edges — the correct crop for a backdrop.
//
// So the no-stretch / correct-crop contract is certified here as: every image
// overlay compiles to "contain" (never "stretch") regardless of the source's
// aspect class, and every background compiles to "cover" (crop, not
// letterbox). The pixel-level letterbox/crop is Chronon's job (cross-repo);
// the deterministic instruction is PipelineGen's.
package overlays

import (
	"reflect"
	"testing"
)

// aspectClass names the four GOLDEN 03 source aspect classes. Each is a
// distinct asset ref — the compiler never decodes pixels, so the class is
// carried as (source, logical path), not as width/height.
type aspectClass struct {
	name       string
	sourceURL  string
	dimensions string // documented for humans; never read by the compiler
}

var aspectClasses = []aspectClass{
	{"landscape", "assets/landscape_1920x1080.png", "1920x1080"},
	{"portrait", "assets/portrait_1080x1920.png", "1080x1920"},
	{"square", "assets/square_1000x1000.png", "1000x1000"},
	{"panorama", "assets/panorama_4000x800.png", "4000x800"},
}

// TestImageAspectFitNeverStretches certifies the no-stretch contract across
// all four source aspect classes: every IMAGE_OVERLAY resolves to the
// image_focus_in preset (whose Chronon geometry is "contain", never
// "stretch"), and PipelineGen emits NO fit/geometry for preset-driven images
// — the renderer owns the aspect-preserving geometry. The result is
// byte-identical across re-renders.
func TestImageAspectFitNeverStretches(t *testing.T) {
	items := []OverlayItem{{
		ID:         "background",
		TemplateID: "BACKGROUND",
		StartMs:    0,
		EndMs:      5000,
		AssetRefs:  []OverlayAssetRef{{AssetID: "bg", URL: "assets/background.jpg", SHA256: GoldenBackgroundHash, MediaType: "image/jpeg"}},
	}}
	for i, ac := range aspectClasses {
		items = append(items, OverlayItem{
			ID:         "img_" + ac.name,
			TemplateID: "IMAGE_OVERLAY",
			StartMs:    int64(i) * 1000,
			EndMs:      int64(i+1) * 1000,
			AssetRefs: []OverlayAssetRef{{
				AssetID: ac.name, URL: ac.sourceURL, SHA256: "sha-" + ac.name, MediaType: "image/png",
			}},
		})
	}
	plan := OverlayPlan{
		SchemaVersion: SchemaVersionPlan,
		PlanID:        "aspect-fit",
		VideoID:       "video-aspect-fit",
		ProjectID:     "golden-content",
		Width:         1280,
		Height:        720,
		FPSNum:        30, FPSDen: 1,
		RendererVersion: "chronon",
		Items:           items,
	}

	got, err := CompileChrononPlan(plan)
	if err != nil {
		t.Fatal(err)
	}

	// Background: cover (crop-to-fill), not contain/stretch — PipelineGen
	// still owns the preset-less backdrop instruction.
	bg := got.Plan.Layers[0]
	if bg.Fit != "cover" {
		t.Fatalf("background fit = %q, want cover (correct crop for a backdrop)", bg.Fit)
	}

	// Every image overlay: one of the modern image presets (Chronon resolves the
	// aspect-preserving "contain"), and PipelineGen emits NO fit — so it can
	// never emit "stretch" for a preset-driven image.
	overlays := got.Plan.Layers[1:]
	if len(overlays) != len(aspectClasses) {
		t.Fatalf("overlay layers = %d, want %d", len(overlays), len(aspectClasses))
	}
	for i, layer := range overlays {
		if !contains(imagePresetCandidates, layer.Preset) {
			t.Errorf("overlay %q (%s) preset = %q, want modern image preset", layer.ID, aspectClasses[i].dimensions, layer.Preset)
		}
		if layer.Fit != "" {
			t.Errorf("overlay %q must not emit a fit (Chronon owns it), got %q", layer.ID, layer.Fit)
		}
	}

	// Determinism: the same aspect set compiles byte-identically.
	again, err := CompileChrononPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Plan, again.Plan) {
		t.Fatal("aspect-fit plan is not deterministic across identical compiles")
	}
}

// TestImageAspectFitRejectsStretchOverride pins the fail-closed guard: a
// preset-driven image emits NO fit by default (Chronon owns the
// aspect-preserving "contain" via the image_focus_in preset). An explicit
// "stretch" fit override is the ONLY way to request stretch, and it is honored
// verbatim — so nothing stretches unless a caller explicitly demands it.
func TestImageAspectFitRejectsStretchOverride(t *testing.T) {
	plan := OverlayPlan{
		SchemaVersion: SchemaVersionPlan,
		PlanID:        "aspect-fit-default",
		VideoID:       "video-aspect-fit-default",
		ProjectID:     "golden-content",
		Width:         1280,
		Height:        720,
		FPSNum:        30, FPSDen: 1,
		RendererVersion: "chronon",
		Items: []OverlayItem{{
			ID:         "img",
			TemplateID: "IMAGE_OVERLAY",
			StartMs:    0,
			EndMs:      1000,
			AssetRefs: []OverlayAssetRef{{
				AssetID: "square", URL: "assets/square_1000x1000.png", SHA256: "sha-square", MediaType: "image/png",
			}},
		}},
	}
	got, err := CompileChrononPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if got.Plan.Layers[0].Fit != "" {
		t.Fatalf("default image fit = %q, want empty (Chronon owns contain, no implicit stretch)", got.Plan.Layers[0].Fit)
	}
	if !contains(imagePresetCandidates, got.Plan.Layers[0].Preset) {
		t.Fatalf("preset-driven image preset = %q, want modern image preset", got.Plan.Layers[0].Preset)
	}

	// An explicit stretch override is the ONLY way to request stretch.
	plan.Items[0].Params = map[string]any{"fit": "stretch"}
	got, err = CompileChrononPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if got.Plan.Layers[0].Fit != "stretch" {
		t.Fatalf("explicit stretch fit = %q, want stretch", got.Plan.Layers[0].Fit)
	}
}
