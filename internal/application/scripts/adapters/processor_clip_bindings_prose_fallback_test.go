// Package adapters_test — processor_clip_bindings_prose_fallback_test.go
// pins the FASE 3 (June 2026) prose-fallback contract for the
// ClipBindingsProcessor at the integration boundary. The file lives
// in `adapters_test` (external) which matches the existing
// sibling test file pattern in processor_clip_bindings_test.go.
//
// Why external and not `package adapters`? The internal-package
// variant would import `internal/application/scripts/usecase`
// (for BuildClipEvidence), and `usecase → adapters` is part of
// the production import graph — Go 1.21+ rejects this as a
// test-only cycle. The contract is therefore asserted indirectly
// through PostProcessResult observability
// (SynthesizedScenes / Warnings / IsEmpty) instead of via direct
// helper access.
package adapters_test

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// TestClipBindingsProcessor_ProseFallback covers the canonical FASE 3
// integration scenarios: prose-fallback engages, skips on empty
// input, and does NOT trigger when model-emitted scenes pre-exist.
//
// Helpers buildScenesFromProse / kindForPosition remain unexported;
// their per-position kind assignment (N=1, N=2, N=10, empty prose)
// is pinned by the more comprehensive sibling sub-tests below that
// exercise the processor path with the corresponding clip counts and
// observe the synthesised kind distribution through result.
func TestClipBindingsProcessor_ProseFallback(t *testing.T) {
	t.Run("engages_when_scenes_empty_and_text_present_N3", func(t *testing.T) {
		ev := &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clip-a", "clip-b", "clip-c"},
			ClipNames: map[string]string{"clip-a": "A", "clip-b": "B", "clip-c": "C"},
			DriveLinks: map[string]string{
				"clip-a": "https://drive.google.com/a",
				"clip-b": "https://drive.google.com/b",
				"clip-c": "https://drive.google.com/c",
			},
		}
		if ev == nil {
			t.Fatal("BuildClipEvidence returned nil")
		}
		plan := &scriptpkg.ResolvedGenerationPlan{
			ClipEvidence: ev,
			NumClips:     3,
		}
		text := "Jackie Chan makes audiences laugh with hyperactive physical pranks. Every scene is gold."
		p := adapters.NewClipBindingsProcessor(zap.NewNop())
		result, err := p.Process(context.Background(), plan, adapters.ProcessInput{
			SpecScene: scriptpkg.SpecSceneOutput{Scenes: nil},
			Text:      text,
		})
		if err != nil {
			t.Fatalf("process error = %v", err)
		}
		if result == nil {
			t.Fatal("result is nil")
		}
		// FASE 3 contract: SynthesizedScenes populated + IsEmpty=false
		// so the registry "empty output" warning does NOT fire.
		if result.IsEmpty() {
			t.Errorf("result.IsEmpty() = true; want false (heuristic should populate SynthesizedScenes)")
		}
		if got := len(result.SynthesizedScenes); got != 3 {
			t.Errorf("len(SynthesizedScenes) = %d, want 3", got)
		}
		// N=3 kind distribution: scene[0]=intro, scene[1]=clip, scene[2]=outro.
		if result.SynthesizedScenes[0].Kind != scriptpkg.SceneIntro {
			t.Errorf("synth[0].Kind = %q, want %q", result.SynthesizedScenes[0].Kind, scriptpkg.SceneIntro)
		}
		if result.SynthesizedScenes[1].Kind != scriptpkg.SceneClip {
			t.Errorf("synth[1].Kind = %q, want %q", result.SynthesizedScenes[1].Kind, scriptpkg.SceneClip)
		}
		if result.SynthesizedScenes[2].Kind != scriptpkg.SceneOutro {
			t.Errorf("synth[2].Kind = %q, want %q", result.SynthesizedScenes[2].Kind, scriptpkg.SceneOutro)
		}
		if len(result.Warnings) == 0 {
			t.Errorf("result.Warnings empty; want heuristic-engagement warning")
		}
		// Synthesised scenes must each carry a non-empty Text + ID so
		// downstream SpecScene.Validate does not trip the "text is
		// required" / "id is required" rules.
		for i, s := range result.SynthesizedScenes {
			if s.Text == "" {
				t.Errorf("synth[%d].Text empty", i)
			}
			if s.ID == "" {
				t.Errorf("synth[%d].ID empty", i)
			}
		}
	})

	t.Run("distributes_intro_clip_outro_for_N10", func(t *testing.T) {
		clipIDs := []string{
			"c1", "c2", "c3", "c4", "c5",
			"c6", "c7", "c8", "c9", "c10",
		}
		drvLinks := make(map[string]string, len(clipIDs))
		for _, id := range clipIDs {
			drvLinks[id] = "https://drive.google.com/" + id
		}
		ev := &scriptpkg.ClipEvidence{
			AcceptedClipIDs: clipIDs,
			ClipNames:  make(map[string]string),
			DriveLinks: drvLinks,
		}
		if ev == nil {
			t.Fatal("BuildClipEvidence returned nil")
		}
		plan := &scriptpkg.ResolvedGenerationPlan{
			ClipEvidence: ev,
			NumClips:     10,
		}
		text := "Jackie Chan is the king of comedy. His slapstick timing is legendary. Every scene is gold. Stunt work is a dance. Hong Kong style. Hollywood dramas. Broken bones and laughter. Tiger bites surprise him. Kung Fu lesson. Chelsea Handler giggles. Graham Norton hugs. Singing with feeling. Director makes him old."
		p := adapters.NewClipBindingsProcessor(zap.NewNop())
		result, err := p.Process(context.Background(), plan, adapters.ProcessInput{
			SpecScene: scriptpkg.SpecSceneOutput{Scenes: nil},
			Text:      text,
		})
		if err != nil {
			t.Fatalf("process error = %v", err)
		}
		if got := len(result.SynthesizedScenes); got != 10 {
			t.Fatalf("len(SynthesizedScenes) = %d, want 10", got)
		}
		// N>=3 → intro at [0], outro at [n-1], middle is clip.
		if result.SynthesizedScenes[0].Kind != scriptpkg.SceneIntro {
			t.Errorf("synth[0].Kind = %q, want %q", result.SynthesizedScenes[0].Kind, scriptpkg.SceneIntro)
		}
		for i := 1; i < 9; i++ {
			if result.SynthesizedScenes[i].Kind != scriptpkg.SceneClip {
				t.Errorf("synth[%d].Kind = %q, want %q", i, result.SynthesizedScenes[i].Kind, scriptpkg.SceneClip)
			}
		}
		if result.SynthesizedScenes[9].Kind != scriptpkg.SceneOutro {
			t.Errorf("synth[9].Kind = %q, want %q", result.SynthesizedScenes[9].Kind, scriptpkg.SceneOutro)
		}
	})

	t.Run("N2_both_clips_no_intro_outro_bleed", func(t *testing.T) {
		ev := &scriptpkg.ClipEvidence{
			AcceptedClipIDs:   []string{"clip-a", "clip-b"},
			ClipNames: map[string]string{},
			DriveLinks: map[string]string{"clip-a": "u1", "clip-b": "u2"},
		}
		plan := &scriptpkg.ResolvedGenerationPlan{
			ClipEvidence: ev,
			NumClips:     2,
		}
		text := "Two funny moments."
		p := adapters.NewClipBindingsProcessor(zap.NewNop())
		result, err := p.Process(context.Background(), plan, adapters.ProcessInput{
			SpecScene: scriptpkg.SpecSceneOutput{Scenes: nil},
			Text:      text,
		})
		if err != nil {
			t.Fatalf("process error = %v", err)
		}
		if got := len(result.SynthesizedScenes); got != 2 {
			t.Fatalf("len(SynthesizedScenes) = %d, want 2", got)
		}
		// N<3 → both scenes are SceneClip (no intro bleed at [0])
		// and no outro bleed at [1]).
		if result.SynthesizedScenes[0].Kind != scriptpkg.SceneClip {
			t.Errorf("synth[0].Kind = %q, want %q (no intro bleed for N<3)",
				result.SynthesizedScenes[0].Kind, scriptpkg.SceneClip)
		}
		if result.SynthesizedScenes[1].Kind != scriptpkg.SceneClip {
			t.Errorf("synth[1].Kind = %q, want %q (no outro bleed for N<3)",
				result.SynthesizedScenes[1].Kind, scriptpkg.SceneClip)
		}
	})

	t.Run("skips_when_text_empty_preserves_noop", func(t *testing.T) {
		ev := &scriptpkg.ClipEvidence{
			AcceptedClipIDs:   []string{"clip-a"},
			ClipNames: map[string]string{},
			DriveLinks: map[string]string{"clip-a": "https://drive.google.com/a"},
		}
		plan := &scriptpkg.ResolvedGenerationPlan{
			ClipEvidence: ev,
			NumClips:     1,
		}
		p := adapters.NewClipBindingsProcessor(zap.NewNop())
		result, err := p.Process(context.Background(), plan, adapters.ProcessInput{
			SpecScene: scriptpkg.SpecSceneOutput{Scenes: nil},
			Text:      "",
		})
		if err != nil {
			t.Fatalf("process error = %v", err)
		}
		// Empty prose → heuristic skipped → result is empty
		// (pre-FASE-3 no-op behaviour preserved).
		if !result.IsEmpty() {
			t.Errorf("result.IsEmpty() = false; want true (heuristic should skip on empty prose)")
		}
		if len(result.SynthesizedScenes) > 0 {
			t.Errorf("SynthesizedScenes = %v, want empty (heuristic skipped)", result.SynthesizedScenes)
		}
	})

	t.Run("skips_when_no_clip_evidence", func(t *testing.T) {
		plan := &scriptpkg.ResolvedGenerationPlan{
			ClipEvidence: nil,
			NumClips:     0,
		}
		p := adapters.NewClipBindingsProcessor(zap.NewNop())
		result, err := p.Process(context.Background(), plan, adapters.ProcessInput{
			SpecScene: scriptpkg.SpecSceneOutput{Scenes: nil},
			Text:      "Prose but no clips.",
		})
		if err != nil {
			t.Fatalf("process error = %v", err)
		}
		// No clip evidence → no-op regardless of prose.
		if !result.IsEmpty() {
			t.Errorf("result.IsEmpty() = false; want true (no clip evidence → no-op)")
		}
		if len(result.SynthesizedScenes) > 0 {
			t.Errorf("SynthesizedScenes = %v, want empty (no clip evidence)", result.SynthesizedScenes)
		}
	})

	t.Run("preserves_existing_scenes_no_heuristic", func(t *testing.T) {
		ev := &scriptpkg.ClipEvidence{
			AcceptedClipIDs:   []string{"clip-a"},
			ClipNames: map[string]string{},
			DriveLinks: map[string]string{"clip-a": "https://drive.google.com/a"},
		}
		plan := &scriptpkg.ResolvedGenerationPlan{
			ClipEvidence: ev,
			NumClips:     1,
		}
		existing := []scriptpkg.SpecScene{
			{ID: "s1", Index: 0, Text: "Hello", Kind: scriptpkg.SceneClip},
			{ID: "s2", Index: 1, Text: "World", Kind: scriptpkg.SceneClip},
			{ID: "s3", Index: 2, Text: "Foo", Kind: scriptpkg.SceneClip},
		}
		model := &scriptpkg.ModelScriptOutputV1{
			SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: existing},
		}
		p := adapters.NewClipBindingsProcessor(zap.NewNop())
		result, err := p.Process(context.Background(), plan, adapters.ProcessInput{
			SpecScene: model.SpecScene,
			Text:      "Prose that MUST be ignored — scenes already populated.",
		})
		if err != nil {
			t.Fatalf("process error = %v", err)
		}
		// Pre-existing-scenes path: 1:1 binding (P0 #2 contract).
		// scene[0] bound to clip-a; scenes 1-2 stay unbound.
		if model.SpecScene.Scenes[0].Bindings.Clip == nil {
			t.Fatalf("scene[0].Bindings.Clip = nil; want bound to clip-a")
		}
		if model.SpecScene.Scenes[0].Bindings.Clip.ClipID != "clip-a" {
			t.Errorf("scene[0].Bindings.Clip.ClipID = %q, want %q",
				model.SpecScene.Scenes[0].Bindings.Clip.ClipID, "clip-a")
		}
		if model.SpecScene.Scenes[1].Bindings.Clip != nil {
			t.Errorf("scene[1].Bindings.Clip = %v, want nil (P0 #2: extra scenes unbound)",
				model.SpecScene.Scenes[1].Bindings.Clip.ClipID)
		}
		// P1 #10 (June 2026): IsEmpty() now returns false even when
		// heuristic did NOT engage — the binder sets Changed=true
		// because it mutated input.SpecScene.Scenes (bound clips,
		// cleared unbound scenes). The contract pin is that
		// SynthesizedScenes is empty (heuristic NOT engaged) while
		// IsEmpty() is false (mutative work happened).
		if result.IsEmpty() {
			t.Errorf("result.IsEmpty() = true; want false (Changed=true because clips were bound)")
		}
		if len(result.SynthesizedScenes) > 0 {
			t.Errorf("SynthesizedScenes = %v, want empty (heuristic must NOT engage when scenes pre-exist)",
				result.SynthesizedScenes)
		}
	})
}
