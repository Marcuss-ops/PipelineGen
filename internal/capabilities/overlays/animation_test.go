// Package overlays — animation_test.go certifies the deterministic animation
// contract at the boundary PipelineGen owns: a layer's motion preset and its
// explicit frame window compile verbatim, and two compiles of the same input
// are byte-identical (identical re-renders).
//
// The pixel motion the presets name — fade/scale_pop/slide_up enter-hold-exit
// curves, and the kinetic_word's scale/rotation/opacity/position — is the
// Chronon renderer's job (cross-repo). PipelineGen owns the deterministic
// instruction: which preset, over which exact frame window, in a stable order.
package overlays

import (
	"reflect"
	"testing"
)

// TestCompilePhraseAnimationPresets certifies the GOLDEN 12 phrase animation
// presets: fade, scale_pop and slide_up each compile to a text layer carrying
// the exact motion preset.
func TestCompilePhraseAnimationPresets(t *testing.T) {
	plan := OverlayPlan{
		SchemaVersion: SchemaVersionPlan,
		PlanID:        "anim-phrases",
		VideoID:       "video-anim-phrases",
		ProjectID:     "golden-content",
		Width:         1280,
		Height:        720,
		FPSNum:        30, FPSDen: 1,
		RendererVersion: "chronon",
		Items: []OverlayItem{
			{ID: "p_fade", TemplateID: "IMPORTANT_PHRASE", StartMs: 0, EndMs: 2000, Text: "FADE", Params: map[string]any{"animation": map[string]any{"preset": "fade"}}},
			{ID: "p_scale_pop", TemplateID: "IMPORTANT_PHRASE", StartMs: 2000, EndMs: 4000, Text: "SCALE POP", Params: map[string]any{"animation": map[string]any{"preset": "scale_pop"}}},
			{ID: "p_slide_up", TemplateID: "IMPORTANT_PHRASE", StartMs: 4000, EndMs: 6000, Text: "SLIDE UP", Params: map[string]any{"animation": map[string]any{"preset": "slide_up"}}},
		},
	}
	got, err := CompileChrononPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"p_fade": "fade", "p_scale_pop": "scale_pop", "p_slide_up": "slide_up"}
	for _, layer := range got.Plan.Layers {
		if layer.Animation == nil {
			t.Fatalf("layer %q missing animation", layer.ID)
		}
		if layer.Animation.Preset != want[layer.ID] {
			t.Errorf("layer %q animation preset = %q, want %q", layer.ID, layer.Animation.Preset, want[layer.ID])
		}
	}
}

// TestCompileAnimationExplicitWindow certifies the explicit animation window:
// a preset with start_frame/duration_frames compiles to the exact frame
// window (the "when" the renderer applies the enter-hold-exit curve).
func TestCompileAnimationExplicitWindow(t *testing.T) {
	plan := OverlayPlan{
		SchemaVersion: SchemaVersionPlan,
		PlanID:        "anim-window",
		VideoID:       "video-anim-window",
		ProjectID:     "golden-content",
		Width:         1280,
		Height:        720,
		FPSNum:        30, FPSDen: 1,
		RendererVersion: "chronon",
		Items: []OverlayItem{{
			ID:         "p_fade",
			TemplateID: "IMPORTANT_PHRASE",
			StartMs:    200, // hidden before frame 6
			EndMs:      2100,
			Text:       "THIS CHANGES EVERYTHING",
			Params: map[string]any{"animation": map[string]any{
				"preset":          "fade",
				"start_frame":     int64(6), // 200ms @ 30fps
				"duration_frames": int64(18),
			}},
		}},
	}
	got, err := CompileChrononPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	anim := got.Plan.Layers[0].Animation
	if anim == nil || anim.Preset != "fade" || anim.StartFrame != 6 || anim.DurationFrames != 18 {
		t.Fatalf("animation window = %+v, want fade start=6 dur=18", anim)
	}
}

// TestCompileKineticWordTransformDeterministic certifies the GOLDEN 13
// transform properties PipelineGen owns as explicit instructions: opacity and
// position compile verbatim on kinetic words, and two compiles are
// byte-identical (identical re-renders).
//
// Scale and rotation are the renderer's pixel motion named by the animation
// preset (scale_drop / scale_pop) — the chronon.render-plan.v1 layer contract
// has no explicit scale/rotation fields, so PipelineGen pins the preset that
// drives them instead of inventing a contract the renderer does not consume.
func TestCompileKineticWordTransformDeterministic(t *testing.T) {
	build := func() OverlayPlan {
		return OverlayPlan{
			SchemaVersion: SchemaVersionPlan,
			PlanID:        "kinetic-transform",
			VideoID:       "video-kinetic-transform",
			ProjectID:     "golden-content",
			Width:         1280,
			Height:        720,
			FPSNum:        30, FPSDen: 1,
			RendererVersion: "chronon",
			Items: []OverlayItem{
				{ID: "w_apple", TemplateID: "IMPORTANT_WORD", StartMs: 500, EndMs: 1300, Text: "APPLE", Params: map[string]any{
					"animation": map[string]any{"preset": "scale_drop"},
					"opacity":   0.9,
					"position":  []any{float64(200), float64(100)},
				}},
				{ID: "w_lost", TemplateID: "IMPORTANT_WORD", StartMs: 1300, EndMs: 2000, Text: "LOST", Params: map[string]any{
					"animation": map[string]any{"preset": "scale_pop"},
					"opacity":   1.0,
					"position":  []any{float64(400), float64(300)},
				}},
			},
		}
	}
	a, err := CompileChrononPlan(build())
	if err != nil {
		t.Fatal(err)
	}
	b, err := CompileChrononPlan(build())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Plan, b.Plan) {
		t.Fatal("kinetic transform is not deterministic across identical compiles")
	}

	want := map[string]struct {
		preset  string
		opacity float64
		pos     []float64
	}{
		"w_apple": {preset: "scale_drop", opacity: 0.9, pos: []float64{200, 100}},
		"w_lost":  {preset: "scale_pop", opacity: 1.0, pos: []float64{400, 300}},
	}
	for _, layer := range a.Plan.Layers {
		w, ok := want[layer.ID]
		if !ok {
			t.Fatalf("unexpected layer %q", layer.ID)
		}
		if layer.Animation == nil || layer.Animation.Preset != w.preset {
			t.Errorf("layer %q animation = %+v, want preset %q", layer.ID, layer.Animation, w.preset)
		}
		if layer.Opacity != w.opacity {
			t.Errorf("layer %q opacity = %v, want %v", layer.ID, layer.Opacity, w.opacity)
		}
		if len(layer.Position) != 2 || layer.Position[0] != w.pos[0] || layer.Position[1] != w.pos[1] {
			t.Errorf("layer %q position = %v, want %v", layer.ID, layer.Position, w.pos)
		}
	}
}

// TestCompileKineticWordTimingDeterministic certifies the GOLDEN 13 kinetic
// timing: APPLE / LOST / $10 BILLION at 0.5s / 1.3s / 2.0s compile to exact
// frame windows, and two compiles of the same input are byte-identical — the
// deterministic-timing guarantee behind "two renders look visually identical".
func TestCompileKineticWordTimingDeterministic(t *testing.T) {
	build := func() OverlayPlan {
		return OverlayPlan{
			SchemaVersion: SchemaVersionPlan,
			PlanID:        "kinetic-timing",
			VideoID:       "video-kinetic-timing",
			ProjectID:     "golden-content",
			Width:         1280,
			Height:        720,
			FPSNum:        30, FPSDen: 1,
			RendererVersion: "chronon",
			Items: []OverlayItem{
				{ID: "w_apple", TemplateID: "IMPORTANT_WORD", StartMs: 500, EndMs: 1300, Text: "APPLE", Params: map[string]any{"animation": map[string]any{"preset": "scale_drop"}}},
				{ID: "w_lost", TemplateID: "IMPORTANT_WORD", StartMs: 1300, EndMs: 2000, Text: "LOST", Params: map[string]any{"animation": map[string]any{"preset": "scale_drop"}}},
				{ID: "w_billion", TemplateID: "IMPORTANT_WORD", StartMs: 2000, EndMs: 2700, Text: "$10 BILLION", Params: map[string]any{"animation": map[string]any{"preset": "scale_drop"}}},
			},
		}
	}
	a, err := CompileChrononPlan(build())
	if err != nil {
		t.Fatal(err)
	}
	b, err := CompileChrononPlan(build())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Plan, b.Plan) {
		t.Fatal("kinetic timing is not deterministic across identical compiles")
	}

	want := map[string][2]int64{
		"w_apple":   {15, 24}, // 0.5s → frame 15, dur 24 (0.8s)
		"w_lost":    {39, 21}, // 1.3s → frame 39, dur 21 (0.7s)
		"w_billion": {60, 21}, // 2.0s → frame 60, dur 21 (0.7s)
	}
	for _, layer := range a.Plan.Layers {
		w, ok := want[layer.ID]
		if !ok {
			t.Fatalf("unexpected layer %q", layer.ID)
		}
		if layer.StartFrame != w[0] || layer.DurationFrames != w[1] {
			t.Errorf("layer %q timing = [%d,%d], want start %d dur %d", layer.ID, layer.StartFrame, layer.DurationFrames, w[0], w[1])
		}
		if layer.Animation == nil || layer.Animation.Preset != "scale_drop" {
			t.Errorf("layer %q animation = %+v, want scale_drop", layer.ID, layer.Animation)
		}
	}
}
