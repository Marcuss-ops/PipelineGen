package overlays

import (
	"fmt"
	"testing"
)

// TestSelectEntityImagePresetUsesOnlyRenderSafeCandidates pins the generated
// entity-image preset contract: every selection comes from the render-safe
// candidate set (never modern_rounded_pop, whose native mask path is not
// implemented), stays bit-identical for a retry of the same job, and still
// varies across identities so one run does not render five identical motions.
func TestSelectEntityImagePresetUsesOnlyRenderSafeCandidates(t *testing.T) {
	safe := make(map[string]bool, len(imagePresetCandidates))
	for _, id := range imagePresetCandidates {
		safe[id] = true
	}
	if safe["modern_rounded_pop"] {
		t.Fatal("modern_rounded_pop must not be selectable for generated entity images")
	}

	variants := map[string]bool{}
	for i := 0; i < 64; i++ {
		jobID := fmt.Sprintf("job-%d", i)
		itemID := fmt.Sprintf("overlay-scene-0-entity-%d", i)
		preset := SelectEntityImagePreset(jobID, "scene-0", itemID)
		if !safe[preset] {
			t.Fatalf("selected preset %q is not in the render-safe image candidate set", preset)
		}
		// A retry of the same job must resolve to the same preset.
		if again := SelectEntityImagePreset(jobID, "scene-0", itemID); again != preset {
			t.Fatalf("preset selection is not deterministic: %q vs %q", preset, again)
		}
		variants[preset] = true
	}
	if len(variants) < 2 {
		t.Fatalf("entity image preset selection never varies across identities: %v", variants)
	}
}

// TestGeneratedTextOverlaysStayOnTheRenderSafeTextContract pins the only
// shape of generated text overlay the native text lane renders. Both rules
// were measured on the installed engine (vulkan backend,
// gpu-hot-path-mode=require_gpu_native), one variable at a time:
//
//   - a text layer carrying style.glow dies in the composite ("native
//     residency violation": the halo stack needs a CPU pixel-backed source),
//     and the canonical apple_v2 preset authors canaryGlow();
//   - a text layer carrying glyph/word TEXT ANIMATORS is rejected outright
//     (route=reject reason=unsupported_animation) — which is exactly what the
//     former rotation pool and apple_v2's own apple_phrase_v2 motion produce.
//
// So a generated overlay may only name the glow-free text preset and a motion
// that lowers to composition tracks, and it must name that motion explicitly:
// an empty MotionID lets the compiler fall back to the preset's own
// glyph-level motion.
func TestGeneratedTextOverlaysStayOnTheRenderSafeTextContract(t *testing.T) {
	officialTextPreset := map[string]bool{"static_text_smoke": true, "phrase_default": true}
	for _, id := range append(append([]string{}, namePresetRenderSafeCandidates...),
		append(append([]string{}, phrasePresetCandidates...), wordPresetCandidates...)...) {
		if !officialTextPreset[id] {
			t.Fatalf("text preset candidate %q is not an official RenderingGen preset", id)
		}
	}

	motions := RenderSafeTextMotions()
	if len(motions) == 0 {
		t.Fatal("no render-safe text motions; every generated text overlay would render statically")
	}
	// The animator families below cannot lower on the native kernel: naming one
	// here means the render fails closed (and, with a glow, crashes the render).
	for _, id := range motions {
		for _, banned := range []string{
			"kinetic_split_word", "masked_upward_reveal", "staggered_char_float",
			"soft_edge_spotlight_dissolve", "velocity_inertia_snap", "apple_phrase_v2",
			"character_cascade", "word_reveal", "char_wave",
		} {
			if id == banned {
				t.Fatalf("motion %q needs a text-animator stack the native lane rejects", id)
			}
		}
	}

	scenes := []SceneInput{{
		ID:       "scene-1",
		Phrases:  []TimedAnnotation{{Text: "a grounded important phrase", StartMs: 0, EndMs: 1000, StartUS: 0, DurationUS: 1_000_000, Score: 1}},
		Quotes:   []TimedAnnotation{{Text: "a grounded quote", StartMs: 0, EndMs: 1000, StartUS: 0, DurationUS: 1_000_000, Score: 1}},
		Keywords: []TimedAnnotation{{Text: "KEYWORD", StartMs: 0, EndMs: 1000, StartUS: 0, DurationUS: 1_000_000, Score: 1}},
		Numbers:  []TimedAnnotation{{Text: "42", StartMs: 0, EndMs: 1000, StartUS: 0, DurationUS: 1_000_000, Score: 1}},
	}}
	plan, err := BuildPlan(PlanInput{
		PlanID: "render-safe", VideoID: "video-1", Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1,
		Scenes: scenes,
	}, AllCandidatesPlannerConfig(scenes))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	safe := map[string]bool{}
	for _, id := range motions {
		safe[id] = true
	}
	textItems := 0
	for _, item := range plan.Items {
		if item.Text == "" {
			continue
		}
		textItems++
		if item.MotionID == "" {
			t.Fatalf("text item %q carries no explicit motion: the preset's own glyph motion would be transported", item.ID)
		}
		if !safe[item.MotionID] {
			t.Fatalf("text item %q motion %q is outside the render-safe pool %v", item.ID, item.MotionID, motions)
		}
	}
	if textItems == 0 {
		t.Fatal("no text items in the plan; the fixture stopped exercising the contract")
	}
}

func TestPresetSelectionIsDeterministicAndUsesKnownFamilies(t *testing.T) {
	first := SelectEntityNamePreset("job-1", "scene-1", "entity-1", "PERSON")
	second := SelectEntityNamePreset("job-1", "scene-1", "entity-1", "PERSON")
	if first == "" || first != second {
		t.Fatalf("entity preset selection is not deterministic: %q vs %q", first, second)
	}

	plan, err := BuildPlan(PlanInput{
		PlanID: "job-1", VideoID: "video-1", Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1,
		Scenes: []SceneInput{{
			ID:       "scene-1",
			Phrases:  []TimedAnnotation{{Text: "IMPORTANT", StartMs: 0, EndMs: 1000}},
			Keywords: []TimedAnnotation{{Text: "NOW", StartMs: 0, EndMs: 1000}},
			Images:   []ImageCandidate{{AssetID: "img-1", URL: "assets/img.png", StartMs: 0, EndMs: 1000}},
		}},
	}, PlannerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 3 {
		t.Fatalf("items = %d, want 3", len(plan.Items))
	}
	for _, item := range plan.Items {
		if item.PresetID == "" {
			t.Fatalf("item %q did not receive a preset", item.ID)
		}
	}
}
