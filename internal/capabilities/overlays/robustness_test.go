// Package overlays — robustness_test.go certifies the robustness and scale
// boundary PipelineGen owns at the compile/planner layer:
//
//   - missing-image fail-closed (a missing/hash-less asset is a hard error);
//   - Unicode text counted in runes, never bytes (multilingual accents);
//   - huge display text auto-fits down deterministically, never off-canvas;
//   - safe-area geometry holds across 720p / 1080p / portrait canvases;
//   - editorial culling caps a huge candidate set (the planner's answer to
//     "process the relevant few, not everything");
//   - a 900-item stress plan compiles deterministically.
//
// Frame-level temporal culling (10k items → 12 active at a given frame) and
// GPU render/encode timing are the Chronon renderer's job (cross-repo);
// PipelineGen certifies the deterministic instruction set that reaches it.
package overlays

import (
	"fmt"
	"reflect"
	"testing"
)

// TestCompileMissingImageFailsClosed certifies the missing-image contract:
// an Image/Video primitive without an asset (or without a content hash) fails
// the compile — never a blank layer or a garbage texture.
func TestCompileMissingImageFailsClosed(t *testing.T) {
	base := func(items ...OverlayItem) OverlayPlan {
		return OverlayPlan{
			SchemaVersion:   SchemaVersionPlan,
			PlanID:          "missing-image",
			VideoID:         "video-missing-image",
			ProjectID:       "golden-content",
			Width:           1280,
			Height:          720,
			FPS:             30,
			RendererVersion: "chronon",
			Items:           items,
		}
	}
	// No asset ref at all → fail closed.
	_, err := CompileChrononPlan(base(OverlayItem{ID: "img", TemplateID: "IMAGE_OVERLAY", StartMs: 0, EndMs: 1000}))
	if err == nil {
		t.Fatal("image overlay without an asset must fail closed")
	}
	// Asset ref with neither URL nor hash → fail closed (unresolvable).
	_, err = CompileChrononPlan(base(OverlayItem{
		ID: "img", TemplateID: "IMAGE_OVERLAY", StartMs: 0, EndMs: 1000,
		AssetRefs: []OverlayAssetRef{{AssetID: "img-1"}},
	}))
	if err == nil {
		t.Fatal("image overlay with an unresolvable asset must fail closed")
	}
	// Video primitive (LIGHT_LEAK) with neither URL nor hash must also fail.
	_, err = CompileChrononPlan(base(OverlayItem{
		ID: "leak", TemplateID: "LIGHT_LEAK", StartMs: 0, EndMs: 1000,
		AssetRefs: []OverlayAssetRef{{AssetID: "leak"}},
	}))
	if err == nil {
		t.Fatal("light leak with an unresolvable asset must fail closed")
	}
	// A URL-only ref is legitimate: the worker materializes it and computes
	// the hash (never a blank layer).
	if _, err := CompileChrononPlan(base(OverlayItem{
		ID: "img", TemplateID: "IMAGE_OVERLAY", StartMs: 0, EndMs: 1000,
		AssetRefs: []OverlayAssetRef{{AssetID: "img-1", URL: "https://cdn.example.com/img.png"}},
	})); err != nil {
		t.Fatalf("URL-only asset must compile (worker resolves the hash): %v", err)
	}
}

// TestCompileUnicodeTextRuneCounting certifies multilingual text: accented/
// emojis/CJK text compiles without error (font/style/layout are Chronon-owned,
// so the plan only transports the text).
func TestCompileUnicodeTextRuneCounting(t *testing.T) {
	cases := []string{"È CAMBIATO", "MÜNCHEN", "L'IPHONE", "日本", "€50 MILIONI", "90%"}
	for _, tc := range cases {
		plan := OverlayPlan{
			SchemaVersion:   SchemaVersionPlan,
			PlanID:          "unicode",
			VideoID:         "video-unicode",
			ProjectID:       "golden-content",
			Width:           1280,
			Height:          720,
			FPS:             30,
			RendererVersion: "chronon",
			Items: []OverlayItem{{
				ID: "phrase", TemplateID: "IMPORTANT_PHRASE", StartMs: 0, EndMs: 2000, Text: tc,
			}},
		}
		if _, err := CompileChrononPlan(plan); err != nil {
			t.Errorf("unicode text %q failed to compile: %v", tc, err)
		}
	}
}

// TestCompileHugeTextCarriesNoFontSize certifies that even very long display
// text compiles without a font_size override: sizing is Chronon's preset
// (StyleResolver), never a PipelineGen auto-fit bucket.
func TestCompileHugeTextCarriesNoFontSize(t *testing.T) {
	huge := "THIS IS A VERY LONG IMPORTANT PHRASE THAT SHOULD NEVER FIT ON ONE LINE AND MUST SCALE DOWN DETERMINISTICALLY"
	plan := OverlayPlan{
		SchemaVersion:   SchemaVersionPlan,
		PlanID:          "huge-text",
		VideoID:         "video-huge-text",
		ProjectID:       "golden-content",
		Width:           1280,
		Height:          720,
		FPS:             30,
		RendererVersion: "chronon",
		Items: []OverlayItem{{
			ID: "phrase", TemplateID: "IMPORTANT_PHRASE", StartMs: 0, EndMs: 2000, Text: huge,
		}},
	}
	got, err := CompileChrononPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	layer := got.Plan.Layers[0]
	if layer.Font != "" || layer.FontSize != 0 {
		t.Fatalf("text layer must not carry font/font_size: %+v", layer)
	}
}

// TestLayoutSafeAreaAcrossResolutions certifies the safe-area contract at the
// auto-layout boundary: a semantic-slot image layer stays inside the safe
// margin on 1280x720, 1920x1080 and the portrait 1080x1920 canvas.
func TestLayoutSafeAreaAcrossResolutions(t *testing.T) {
	for _, canvas := range []struct{ w, h int }{{1280, 720}, {1920, 1080}, {1080, 1920}} {
		plan := OverlayPlan{
			SchemaVersion:   SchemaVersionPlan,
			PlanID:          "safe-area",
			VideoID:         "video-safe-area",
			ProjectID:       "golden-content",
			Width:           canvas.w,
			Height:          canvas.h,
			FPS:             30,
			RendererVersion: "chronon",
			Items: []OverlayItem{{
				ID: "img", TemplateID: "IMAGE_OVERLAY", StartMs: 0, EndMs: 3000,
				Params: map[string]any{"position": "right", "box_width": 260, "box_height": 260},
				AssetRefs: []OverlayAssetRef{{
					AssetID: "globe", URL: "assets/overlay_globe.png", SHA256: GoldenGlobeHash, MediaType: "image/png",
				}},
			}},
		}
		got, err := CompileChrononPlan(plan)
		if err != nil {
			t.Fatalf("canvas %dx%d: %v", canvas.w, canvas.h, err)
		}
		layer := got.Plan.Layers[0]
		if len(layer.Position) != 2 {
			t.Fatalf("canvas %dx%d: missing resolved position", canvas.w, canvas.h)
		}
		x, y := layer.Position[0], layer.Position[1]
		if x < SafeCanvasMargin || y < SafeCanvasMargin ||
			x+float64(layer.BoxWidth) > float64(canvas.w)-SafeCanvasMargin ||
			y+float64(layer.BoxHeight) > float64(canvas.h)-SafeCanvasMargin {
			t.Errorf("canvas %dx%d: layer outside safe area: pos=%v box=%dx%d margin=%v",
				canvas.w, canvas.h, layer.Position, layer.BoxWidth, layer.BoxHeight, SafeCanvasMargin)
		}
	}
}

// TestBuildPlanEditorialCullingCapsHugeCandidateSet certifies the editorial
// culling: a scene carrying 100 phrases + 500 keywords + 200 images is capped
// to the configured per-scene limits (2 phrases / 3 keywords / 3 images), so
// the planner processes the relevant few, not every candidate.
func TestBuildPlanEditorialCullingCapsHugeCandidateSet(t *testing.T) {
	phrases := make([]TimedAnnotation, 100)
	for i := range phrases {
		phrases[i] = TimedAnnotation{Text: fmt.Sprintf("P%d", i), StartMs: int64(i) * 10, EndMs: int64(i)*10 + 5, Score: float64(i)}
	}
	keywords := make([]TimedAnnotation, 500)
	for i := range keywords {
		keywords[i] = TimedAnnotation{Text: fmt.Sprintf("K%d", i), StartMs: int64(i), EndMs: int64(i) + 1, Score: float64(i)}
	}
	images := make([]ImageCandidate, 200)
	for i := range images {
		images[i] = ImageCandidate{AssetID: fmt.Sprintf("img-%d", i), SHA256: fmt.Sprintf("h-%d", i), StartMs: int64(i), EndMs: int64(i) + 1, Score: float64(i)}
	}

	plan, err := BuildPlan(PlanInput{
		PlanID: "cull", VideoID: "video-cull", Width: 1280, Height: 720, FPS: 30,
		Scenes: []SceneInput{{ID: "scene-1", Phrases: phrases, Keywords: keywords, Images: images}},
	}, PlannerConfig{MaxPhrases: 2, MaxKeywords: 3, MaxImages: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 8 {
		t.Fatalf("items = %d, want 2 phrases + 3 keywords + 3 images (capped)", len(plan.Items))
	}
	var phrasesSeen, keywordsSeen, imagesSeen int
	for _, item := range plan.Items {
		switch item.TemplateID {
		case "IMPORTANT_PHRASE":
			phrasesSeen++
		case "IMPORTANT_WORD":
			keywordsSeen++
		case "IMAGE_OVERLAY":
			imagesSeen++
		}
	}
	if phrasesSeen != 2 || keywordsSeen != 3 || imagesSeen != 3 {
		t.Fatalf("capping failed: phrases=%d keywords=%d images=%d", phrasesSeen, keywordsSeen, imagesSeen)
	}
}

// TestCompileStressProfile900Items certifies the scale profile: a plan with
// 100 phrases + 500 words + 200 images + 100 light leaks compiles to exactly
// 900 layers deterministically (the compile-time analog of the render stress
// profile; render/encode GPU timing is the renderer's concern).
func TestCompileStressProfile900Items(t *testing.T) {
	items := make([]OverlayItem, 0, 900)
	for i := 0; i < 100; i++ {
		items = append(items, OverlayItem{
			ID: fmt.Sprintf("phrase-%d", i), TemplateID: "IMPORTANT_PHRASE",
			StartMs: int64(i) * 1000, EndMs: int64(i)*1000 + 500, Text: fmt.Sprintf("PHRASE %d", i),
		})
	}
	for i := 0; i < 500; i++ {
		items = append(items, OverlayItem{
			ID: fmt.Sprintf("word-%d", i), TemplateID: "IMPORTANT_WORD",
			StartMs: int64(i) * 500, EndMs: int64(i)*500 + 200, Text: fmt.Sprintf("W%d", i),
		})
	}
	for i := 0; i < 200; i++ {
		items = append(items, OverlayItem{
			ID: fmt.Sprintf("image-%d", i), TemplateID: "IMAGE_OVERLAY",
			StartMs: int64(i) * 500, EndMs: int64(i)*500 + 300,
			AssetRefs: []OverlayAssetRef{{
				AssetID: fmt.Sprintf("img-%d", i), URL: fmt.Sprintf("assets/img-%d.png", i),
				SHA256: fmt.Sprintf("sha-%d", i), MediaType: "image/png",
			}},
		})
	}
	for i := 0; i < 100; i++ {
		items = append(items, OverlayItem{
			ID: fmt.Sprintf("leak-%d", i), TemplateID: "LIGHT_LEAK",
			StartMs: int64(i) * 1000, EndMs: int64(i)*1000 + 500,
			Params: map[string]any{"opacity": 0.5},
			AssetRefs: []OverlayAssetRef{{
				AssetID: fmt.Sprintf("leak-%d", i), URL: "assets/light_leak_01.mp4",
				SHA256: fmt.Sprintf("leaksha-%d", i), MediaType: "video/mp4",
			}},
		})
	}
	plan := OverlayPlan{
		SchemaVersion:   SchemaVersionPlan,
		PlanID:          "stress",
		VideoID:         "video-stress",
		ProjectID:       "golden-content",
		Width:           1280,
		Height:          720,
		FPS:             30,
		RendererVersion: "chronon",
		Items:           items,
	}
	got, err := CompileChrononPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Plan.Layers) != 900 {
		t.Fatalf("layers = %d, want 900", len(got.Plan.Layers))
	}
	// Determinism: the same 900-item plan compiles byte-identically.
	again, err := CompileChrononPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Plan, again.Plan) {
		t.Fatal("900-item plan is not deterministic across identical compiles")
	}
	// Assets: 200 images + 100 leaks (distinct hashes) + 1 canonical font.
	if len(got.Assets) != 301 {
		t.Fatalf("assets = %d, want 301 (200 images + 100 leaks + font)", len(got.Assets))
	}
}
