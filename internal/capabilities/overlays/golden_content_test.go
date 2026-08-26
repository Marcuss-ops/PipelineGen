// Package overlays — golden_content_test.go pins the Golden Content Suite
// compile invariants: each semantic content plan compiles to exactly its
// expected chronon.render-plan.v1 document and content-addressed assets.
// This is the "content, not just MP4 output" regression net: if the compiler
// or the template table drifts, a content scenario fails loudly.
package overlays

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"encoding/json"
	"reflect"
	"testing"
)

// goldenContentCase binds one content scenario's semantic plan to its
// expected compiled document and assets.
type goldenContentCase struct {
	name   string
	plan   func() OverlayPlan
	want   ChrononPlan
	assets []ChrononAsset
}

// goldenContentCases is the canonical suite. Content01–03 and Content05 are
// the scenarios buildable with the certified template vocabulary; GOLDEN 04
// (light leak) and GOLDEN 06 (full script scene) are separate milestones.
func goldenContentCases() []goldenContentCase {
	return []goldenContentCase{
		{
			name:   "Content01-phrases",
			plan:   GoldenOverlayPlanContent01Phrases,
			want:   GoldenChrononPlanContent01Phrases,
			assets: GoldenChrononAssetsContent01Phrases,
		},
		{
			name:   "Content02-words",
			plan:   GoldenOverlayPlanContent02Words,
			want:   GoldenChrononPlanContent02Words,
			assets: GoldenChrononAssetsContent02Words,
		},
		{
			name:   "Content03-images",
			plan:   GoldenOverlayPlanContent03Images,
			want:   GoldenChrononPlanContent03Images,
			assets: GoldenChrononAssetsContent03Images,
		},
		{
			name:   "Content05-mixed",
			plan:   GoldenOverlayPlanContent05Mixed,
			want:   GoldenChrononPlanContent05Mixed,
			assets: GoldenChrononAssetsContent05Mixed,
		},
	}
}

// TestCompileChrononPlanGoldenContent pins the struct-level compile invariant
// for every content scenario: the semantic plan compiles to exactly the
// expected document (layers, canvas, output) and content-addressed assets.
func TestCompileChrononPlanGoldenContent(t *testing.T) {
	for _, tc := range goldenContentCases() {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CompileChrononPlan(tc.plan())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Plan, tc.want) {
				gotJSON, _ := json.MarshalIndent(got.Plan, "", "  ")
				wantJSON, _ := json.MarshalIndent(tc.want, "", "  ")
				t.Fatalf("compiled %s plan diverges:\n--- got ---\n%s\n--- want ---\n%s", tc.name, gotJSON, wantJSON)
			}
			if !reflect.DeepEqual(got.Assets, tc.assets) {
				t.Fatalf("compiled %s assets diverge:\n got %+v\nwant %+v", tc.name, got.Assets, tc.assets)
			}
		})
	}
}

// TestCompileChrononPlanGoldenContentDeterministic pins the determinism
// contract: compiling the same semantic plan twice yields the same document
// and assets (the layout engine is a pure function of plan + order).
func TestCompileChrononPlanGoldenContentDeterministic(t *testing.T) {
	for _, tc := range goldenContentCases() {
		t.Run(tc.name, func(t *testing.T) {
			a, err := CompileChrononPlan(tc.plan())
			if err != nil {
				t.Fatal(err)
			}
			b, err := CompileChrononPlan(tc.plan())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(a.Plan, b.Plan) {
				t.Fatal("plan is not deterministic across identical compiles")
			}
			if !reflect.DeepEqual(a.Assets, b.Assets) {
				t.Fatal("assets are not deterministic across identical compiles")
			}
		})
	}
}

// TestCompileChrononPlanGoldenContent01JSON matches the compiled Content01
// plan against the literal render_plan document carried by RenderingGen's
// golden-content-01 job. Both sides are canonicalized through JSON so field
// order and whitespace differences are ignored while every value must match.
func TestCompileChrononPlanGoldenContent01JSON(t *testing.T) {
	const content01RenderPlan = `{
    "schema": "chronon.render-plan",
    "version": 1,
    "job_id": "golden-content-01-phrases",
    "canvas": { "width": 1280, "height": 720, "fps_num": 30, "fps_den": 1, "duration_frames": 150 },
    "layers": [
      {
        "id": "background",
        "type": "image",
        "asset": "assets/background.jpg",
        "box_width": 1280,
        "box_height": 720,
        "fit": "cover",
        "start_frame": 0,
        "duration_frames": 150
      },
      {
        "id": "important_phrase_1",
        "type": "text",
        "font_asset": { "asset": "assets/fonts/DejaVuSans.ttf", "family": "DejaVu Sans", "weight": 700 },
        "text": "A MAJOR CHANGE",
        "preset": "slide_lateral",
        "start_frame": 24,
        "duration_frames": 48
      },
      {
        "id": "important_phrase_2",
        "type": "text",
        "font_asset": { "asset": "assets/fonts/DejaVuSans.ttf", "family": "DejaVu Sans", "weight": 700 },
        "text": "THIS CHANGES EVERYTHING",
        "preset": "clean_slide_up",
        "start_frame": 90,
        "duration_frames": 54
      }
    ],
    "output": { "path": "result.mp4", "format": "mp4", "codec": "h264" }
  }`

	got, err := CompileChrononPlan(GoldenOverlayPlanContent01Phrases())
	if err != nil {
		t.Fatal(err)
	}
	gotJSON, err := got.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	var gotDoc, wantDoc any
	if err := json.Unmarshal(gotJSON, &gotDoc); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(content01RenderPlan), &wantDoc); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotDoc, wantDoc) {
		t.Fatalf("compiled Content01 plan does not match the RenderingGen golden document:\n got %s", gotJSON)
	}
}

// TestCompileChrononPlanGoldenContent02Animations pins that the kinetic-word
// animation presets survive compilation as layer animations (the motion is
// part of the content, not a decoration).
func TestCompileChrononPlanGoldenContent02Animations(t *testing.T) {
	got, err := CompileChrononPlan(GoldenOverlayPlanContent02Words())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"important_word_1": "scale_drop",
		"important_word_2": "fade_in",
		"important_word_3": "fade_shift_vertical",
		"important_word_4": "scale_drop",
	}
	seen := map[string]string{}
	for _, layer := range got.Plan.Layers {
		if layer.Animation != nil {
			seen[layer.ID] = layer.Animation.Preset
		}
	}
	for id, preset := range want {
		if seen[id] != preset {
			t.Errorf("layer %q animation = %q, want %q", id, seen[id], preset)
		}
	}
}

// TestGoldenContent03ImagesFitAndBounds certifies GOLDEN 03 image geometry:
// every image overlay resolves to a modern image preset (Chronon owns the
// canonical 260×260 "contain" geometry). Explicit Params box+position stay
// fully inside the 1280x720 canvas (no overflow, no off-canvas ink); an
// overlay without an explicit position carries no geometry and is placed by
// Chronon via the preset anchor. The background ("cover", full canvas) is
// excluded.
func TestGoldenContent03ImagesFitAndBounds(t *testing.T) {
	got, err := CompileChrononPlan(GoldenOverlayPlanContent03Images())
	if err != nil {
		t.Fatal(err)
	}
	imageLayers := 0
	for _, layer := range got.Plan.Layers {
		if !contains(imagePresetCandidates, layer.Preset) {
			continue // background is "cover"; only overlay images carry the preset
		}
		imageLayers++
		if len(layer.Position) != 2 {
			// No explicit position → Chronon owns placement (preset anchor).
			if layer.BoxWidth != 0 || layer.BoxHeight != 0 {
				t.Errorf("image overlay %q carries a box without a position: %dx%d",
					layer.ID, layer.BoxWidth, layer.BoxHeight)
			}
			continue
		}
		x, y := layer.Position[0], layer.Position[1]
		if x < 0 || y < 0 ||
			x+float64(layer.BoxWidth) > float64(got.Plan.Canvas.Width) ||
			y+float64(layer.BoxHeight) > float64(got.Plan.Canvas.Height) {
			t.Errorf("image overlay %q overflows canvas: pos=%v box=%dx%d canvas=%dx%d",
				layer.ID, layer.Position, layer.BoxWidth, layer.BoxHeight, got.Plan.Canvas.Width, got.Plan.Canvas.Height)
		}
	}
	if imageLayers != 3 {
		t.Fatalf("image overlay layers = %d, want 3", imageLayers)
	}
}

// TestCompileChrononPlanGoldenContent03NoFontAsset pins that an image-only
// plan does not carry the canonical font: there are no text layers, so the
// font is never projected into the queue assets.
func TestCompileChrononPlanGoldenContent03NoFontAsset(t *testing.T) {
	got, err := CompileChrononPlan(GoldenOverlayPlanContent03Images())
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range got.Assets {
		if asset.Hash == GoldenFontHash {
			t.Fatal("image-only plan must not carry the canonical font asset")
		}
	}
}

// TestGoldenContent05MixedZOrder certifies the GOLDEN 05 z-index contract:
// the layer order IS the stacking order (bottom → top), and it is defined and
// deterministic — never dependent on map/array iteration order. The canonical
// mixed plan pins background < image < light leak < word < phrase.
func TestGoldenContent05MixedZOrder(t *testing.T) {
	got, err := CompileChrononPlan(GoldenOverlayPlanContent05Mixed())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"background", "image_overlay", "light_leak", "important_word", "important_phrase"}
	if len(got.Plan.Layers) != len(want) {
		t.Fatalf("layers = %d, want %d", len(got.Plan.Layers), len(want))
	}
	for i, id := range want {
		if got.Plan.Layers[i].ID != id {
			t.Errorf("layer[%d] = %q, want %q (defined z-index)", i, got.Plan.Layers[i].ID, id)
		}
	}
	// The light leak sits above the image and below the text (z=40).
	leak := got.Plan.Layers[2]
	if leak.Type != "video" || leak.BlendMode != "screen" || leak.Opacity != 0.5 {
		t.Fatalf("light leak layer = %+v, want video/screen/0.5", leak)
	}
}

// TestGoldenContent05MixedSafeArea certifies the safe-area contract: every
// explicitly positioned layer stays fully inside the canvas — nothing is cut
// off at the compile boundary (the renderer then applies the same geometry).
func TestGoldenContent05MixedSafeArea(t *testing.T) {
	got, err := CompileChrononPlan(GoldenOverlayPlanContent05Mixed())
	if err != nil {
		t.Fatal(err)
	}
	for _, layer := range got.Plan.Layers {
		if len(layer.Position) != 2 {
			continue // text/video layers are preset/fit-driven, no explicit box
		}
		x, y := layer.Position[0], layer.Position[1]
		if x < 0 || y < 0 ||
			x+float64(layer.BoxWidth) > float64(got.Plan.Canvas.Width) ||
			y+float64(layer.BoxHeight) > float64(got.Plan.Canvas.Height) {
			t.Errorf("layer %q overflows canvas: pos=%v box=%dx%d canvas=%dx%d",
				layer.ID, layer.Position, layer.BoxWidth, layer.BoxHeight, got.Plan.Canvas.Width, got.Plan.Canvas.Height)
		}
	}
}
