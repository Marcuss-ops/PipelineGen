package processor

import (
	"fmt"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestNormalizeScenesForExplicitSegmentsPinsFiveScenesToFiveSegments is the
// r20minuti cardinality regression: a model answer with 26 structured scenes
// must be re-keyed to the five explicit segments, not published as 26 render
// slots. The function pads missing slots and drops the surplus deterministically.
func TestNormalizeScenesForExplicitSegmentsPinsFiveScenesToFiveSegments(t *testing.T) {
	existing := make([]scriptpkg.SpecScene, 26)
	for i := range existing {
		existing[i] = scriptpkg.SpecScene{
			ID:   fmt.Sprintf("model-%d", i),
			Text: fmt.Sprintf("model scene %d", i),
		}
	}
	segments := make([]scriptpkg.ScriptSegment, 5)
	for i := range segments {
		segments[i] = scriptpkg.ScriptSegment{
			ID:         fmt.Sprintf("segment-%d", i+1),
			SourceText: fmt.Sprintf("source %d", i),
		}
	}

	got := normalizeScenesForExplicitSegments(existing, segments)
	if len(got) != 5 {
		t.Fatalf("want 5 scenes for 5 explicit segments, got %d", len(got))
	}
	if got[0].ID != "scene-0" || got[4].ID != "scene-4" {
		t.Fatalf("scene IDs must be re-keyed by slot, got %q .. %q", got[0].ID, got[4].ID)
	}
	if got[0].SegmentID != "segment-1" || got[4].SegmentID != "segment-5" {
		t.Fatalf("segment IDs must follow the explicit segments, got %q .. %q", got[0].SegmentID, got[4].SegmentID)
	}
	if got[4].Text != "model scene 4" {
		t.Fatalf("the first five model scenes must be retained verbatim, got %q", got[4].Text)
	}
}
