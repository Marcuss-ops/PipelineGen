package adapters

import (
	"testing"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestProjectPostSegmentClipBindingsKeepsTimelineAssignments(t *testing.T) {
	scenes := []scriptpkg.SpecScene{
		{ID: "scene-0", SegmentID: "intro-hook"},
		{ID: "scene-1", SegmentID: "boxer-mike-tyson"},
		{ID: "scene-2", SegmentID: "boxer-sugar-ray-robinson"},
	}
	assignments := []mediadomain.VisualAssignment{
		{SegmentID: "boxer-mike-tyson", Slot: mediadomain.VisualSlotPostSegment, AssetID: "tyson-clip", Position: 0, DurationMs: 7000},
		{SegmentID: "boxer-sugar-ray-robinson", Slot: mediadomain.VisualSlotPostSegment, AssetID: "robinson-clip-1", Position: 0, DurationMs: 6000},
		{SegmentID: "boxer-sugar-ray-robinson", Slot: mediadomain.VisualSlotPostSegment, AssetID: "robinson-clip-2", Position: 1, DurationMs: 6000},
	}

	projectPostSegmentClipBindings(scenes, assignments)

	if got := scenes[1].Bindings.Clip; got == nil || got.ClipID != "tyson-clip" || got.DurationMs != 7000 {
		t.Fatalf("Tyson clip binding = %#v", got)
	}
	if got := scenes[2].Bindings.Clip; got == nil || got.ClipID != "robinson-clip-1" {
		t.Fatalf("Robinson clip binding = %#v", got)
	}
	if scenes[0].Bindings.Clip != nil {
		t.Fatal("intro scene must not receive a post-segment clip binding")
	}
	if len(assignments) != 3 || assignments[2].Position != 1 {
		t.Fatal("timeline assignments must remain unchanged")
	}
}

func TestSceneIDForSegmentUsesExplicitSegmentOrderBeforeNormalization(t *testing.T) {
	scenes := []scriptpkg.SpecScene{
		{ID: "scene-0"},
		{ID: "scene-1"},
		{ID: "scene-2"},
	}
	segments := []scriptpkg.ScriptSegment{
		{ID: "intro-hook"},
		{ID: "boxer-mike-tyson"},
		{ID: "boxer-muhammad-ali"},
	}

	if got := sceneIDForSegment(scenes, segments, "boxer-muhammad-ali"); got != "scene-2" {
		t.Fatalf("scene id = %q, want scene-2", got)
	}
}

func TestSceneIDForSegmentPrefersExistingSceneBinding(t *testing.T) {
	scenes := []scriptpkg.SpecScene{
		{ID: "scene-generated", SegmentID: "boxer-mike-tyson"},
		{ID: "scene-1"},
	}
	segments := []scriptpkg.ScriptSegment{
		{ID: "intro-hook"},
		{ID: "boxer-mike-tyson"},
	}

	if got := sceneIDForSegment(scenes, segments, "boxer-mike-tyson"); got != "scene-generated" {
		t.Fatalf("scene id = %q, want scene-generated", got)
	}
}

func TestSceneIDForSegmentCreatesCanonicalIDForMissingGeneratedSlot(t *testing.T) {
	scenes := []scriptpkg.SpecScene{
		{ID: "scene-0"},
		{ID: "scene-1"},
	}
	segments := []scriptpkg.ScriptSegment{
		{ID: "intro-hook"},
		{ID: "boxer-mike-tyson"},
		{ID: "boxer-muhammad-ali"},
		{ID: "boxer-evander-holyfield"},
		{ID: "boxer-floyd-mayweather"},
	}

	if got := sceneIDForSegment(scenes, segments, "boxer-floyd-mayweather"); got != "scene-4" {
		t.Fatalf("scene id = %q, want scene-4", got)
	}
}
