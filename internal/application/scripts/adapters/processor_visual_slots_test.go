package adapters

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

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
