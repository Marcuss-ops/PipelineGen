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

func TestWaitForVidRushAllowsFixedIntroAndOutroWithVerbatimScenes(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		SourceTextVerbatim: true,
		Segments:           []scriptpkg.ScriptSegment{{}, {}, {}, {}, {}},
		Intro:              &scriptpkg.FixedSection{ClipIDs: []string{"intro-clip"}},
		Outro:              &scriptpkg.FixedSection{ClipIDs: []string{"outro-clip"}},
	}
	coordinator := NewVidRushIncrementalCoordinator(nil, plan, 1)
	coordinator.optionalFixedScenes = 2
	for index, id := range []string{"scene-0", "scene-1", "scene-2", "scene-3", "scene-4", "scene-intro", "scene-outro"} {
		if err := coordinator.OnSceneCommitted(context.Background(), SceneCommitted{
			RunID: "run-verbatim-fixed", SceneID: id, SceneIndex: index, TextHash: "hash-" + id,
			ExtractEntities: scriptpkg.ToggleDisabled,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := coordinator.WaitForVidRush(context.Background(), "run-verbatim-fixed"); err != nil {
		t.Fatalf("WaitForVidRush with verbatim fixed intro/outro = %v, want nil", err)
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

// TestWaitForVidRushAcceptsOptionalFixedScenes pins the r10 live shape: the
// non-verbatim streamer has already committed the fixed intro/outro scenes
// when the barrier runs. They are optional (the later phase may add them), but
// the barrier must accept them instead of reporting them as extra indexes.
func TestWaitForVidRushAcceptsOptionalFixedScenes(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{Segments: []scriptpkg.ScriptSegment{{}, {}}}
	coordinator := NewVidRushIncrementalCoordinator(nil, plan, 1)
	coordinator.optionalFixedScenes = 2
	for index, id := range []string{"scene-intro", "scene-0", "scene-1", "scene-outro"} {
		if err := coordinator.OnSceneCommitted(context.Background(), SceneCommitted{
			RunID: "run-optional-fixed", SceneID: id, SceneIndex: index, TextHash: "hash-" + id,
			ExtractEntities: scriptpkg.ToggleDisabled,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := coordinator.WaitForVidRush(context.Background(), "run-optional-fixed"); err != nil {
		t.Fatalf("WaitForVidRush with committed optional fixed scenes = %v, want nil", err)
	}
}

// TestWaitForVidRushRejectsBeyondOptionalFixedScenes keeps the fail-closed
// half: the optional allowance is exactly the fixed intro/outro count, so a
// real extra scene still fails the barrier.
func TestWaitForVidRushRejectsBeyondOptionalFixedScenes(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{Segments: []scriptpkg.ScriptSegment{{}, {}}}
	coordinator := NewVidRushIncrementalCoordinator(nil, plan, 1)
	coordinator.optionalFixedScenes = 2
	for index := 0; index < 5; index++ {
		if err := coordinator.OnSceneCommitted(context.Background(), SceneCommitted{
			RunID: "run-extra-beyond-fixed", SceneID: "scene-" + string(rune('0'+index)), SceneIndex: index,
			TextHash: "hash", ExtractEntities: scriptpkg.ToggleDisabled,
		}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := coordinator.WaitForVidRush(context.Background(), "run-extra-beyond-fixed")
	if err == nil || !strings.Contains(err.Error(), "extra indexes [4]") {
		t.Fatalf("WaitForVidRush error = %v, want extra index [4] beyond the optional fixed allowance", err)
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
