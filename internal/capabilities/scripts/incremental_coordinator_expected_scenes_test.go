package scriptgeneration

import (
	"context"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestWaitForVidRushRejectsExtraCommittedScene(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{Segments: []scriptpkg.ScriptSegment{{}}}
	coordinator := NewVidRushIncrementalCoordinator(nil, plan, 1)
	for index, id := range []string{"scene-0", "scene-1"} {
		if err := coordinator.OnSceneCommitted(context.Background(), SceneCommitted{
			RunID: "run-extra", SceneID: id, SceneIndex: index, TextHash: "hash-" + id,
			ExtractEntities: scriptpkg.ToggleDisabled,
		}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := coordinator.WaitForVidRush(context.Background(), "run-extra")
	if err == nil || !strings.Contains(err.Error(), "expected 1 committed scenes, got 2") {
		t.Fatalf("WaitForVidRush error = %v, want extra-scene diagnostic", err)
	}
	if !strings.Contains(err.Error(), "extra indexes [1]") {
		t.Fatalf("WaitForVidRush error = %v, want extra index [1]", err)
	}
}

func TestWaitForVidRushRejectsIndexGapWithMatchingCount(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{Segments: []scriptpkg.ScriptSegment{{}, {}}}
	coordinator := NewVidRushIncrementalCoordinator(nil, plan, 1)
	for index, id := range []string{"scene-0", "scene-2"} {
		actualIndex := index * 2
		if err := coordinator.OnSceneCommitted(context.Background(), SceneCommitted{
			RunID: "run-gap", SceneID: id, SceneIndex: actualIndex, TextHash: "hash",
			ExtractEntities: scriptpkg.ToggleDisabled,
		}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := coordinator.WaitForVidRush(context.Background(), "run-gap")
	if err == nil || !strings.Contains(err.Error(), "missing indexes [1]") || !strings.Contains(err.Error(), "extra indexes [2]") {
		t.Fatalf("WaitForVidRush error = %v, want gap diagnostic", err)
	}
}

// TestWaitForVidRushCountsFixedIntroAndOutro certifies that the barrier's
// expected scene count includes the fixed sections the runner injects as
// first/last SpecScenes. Counting only plan.Segments rejects the perfectly
// valid intro + body + outro run with an "extra indexes" error.
func TestWaitForVidRushCountsFixedIntroAndOutro(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		Segments: []scriptpkg.ScriptSegment{{}, {}},
		Intro:    &scriptpkg.FixedSection{ClipIDs: []string{"intro-clip"}},
		Outro:    &scriptpkg.FixedSection{ClipIDs: []string{"outro-clip"}},
	}
	coordinator := NewVidRushIncrementalCoordinator(nil, plan, 1)
	for index, id := range []string{"scene-intro", "scene-0", "scene-1", "scene-outro"} {
		if err := coordinator.OnSceneCommitted(context.Background(), SceneCommitted{
			RunID: "run-fixed", SceneID: id, SceneIndex: index, TextHash: "hash-" + id,
			ExtractEntities: scriptpkg.ToggleDisabled,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := coordinator.WaitForVidRush(context.Background(), "run-fixed"); err != nil {
		t.Fatalf("WaitForVidRush with fixed intro/outro = %v, want nil", err)
	}
}

// TestWaitForVidRushKeepsDynamicCountWithoutExplicitSegments certifies the
// other half of the contract: intro/outro alone do not invent an expected
// count when the body is generated to a dynamic scene count.
func TestWaitForVidRushKeepsDynamicCountWithoutExplicitSegments(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		Intro: &scriptpkg.FixedSection{ClipIDs: []string{"intro-clip"}},
		Outro: &scriptpkg.FixedSection{ClipIDs: []string{"outro-clip"}},
	}
	coordinator := NewVidRushIncrementalCoordinator(nil, plan, 1)
	for index := 0; index < 5; index++ {
		if err := coordinator.OnSceneCommitted(context.Background(), SceneCommitted{
			RunID: "run-dynamic", SceneID: "scene-" + string(rune('0'+index)), SceneIndex: index,
			TextHash: "hash", ExtractEntities: scriptpkg.ToggleDisabled,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := coordinator.WaitForVidRush(context.Background(), "run-dynamic"); err != nil {
		t.Fatalf("WaitForVidRush without explicit segments = %v, want nil", err)
	}
}

func TestWaitForVidRushRejectsMissingPlannedScene(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{Segments: []scriptpkg.ScriptSegment{{}, {}}}
	coordinator := NewVidRushIncrementalCoordinator(nil, plan, 1)
	err := coordinator.OnSceneCommitted(context.Background(), SceneCommitted{
		RunID: "run-missing", SceneID: "scene-1", SceneIndex: 0, TextHash: "hash-1",
		ExtractEntities: scriptpkg.ToggleDisabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = coordinator.WaitForVidRush(context.Background(), "run-missing")
	if err == nil || !strings.Contains(err.Error(), "expected 2 committed scenes, got 1") {
		t.Fatalf("WaitForVidRush error = %v, want missing-scene diagnostic", err)
	}
}
