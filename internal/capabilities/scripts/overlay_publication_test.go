package scriptgeneration

import (
	"errors"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

// TestScheduleFinalJobOverlayFramesUsesFreeGapBeforePlannedStart pins the r8
// failure: a date card planned near the scene tail was blocked by a later
// overlay, and the forward-only search rejected the whole final job even
// though the scene had free time before the card. The scheduler must keep the
// card inside its scene, as close as possible to the planned start.
func TestScheduleFinalJobOverlayFramesUsesFreeGapBeforePlannedStart(t *testing.T) {
	result := &GenerateResult{
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
			Segments: []capabilityaudio.TimelineSegment{{
				ID: "scene-1", TimelineStartUS: 0, DurationUS: 10_000_000,
			}},
		},
	}
	item := capmodel.OverlayItem{
		ID: "date-late", SceneID: "scene-1", Kind: "number",
		StartUS: 5_000_000, DurationUS: 5_000_000,
	}
	occupied := map[string]finalJobFrameWindow{
		"map-late": {start: 120, end: 240, sceneID: "scene-1"},
	}

	start, end, err := scheduleFinalJobOverlayFrames(result, item, 120, 240, 24, 1, occupied)
	if err != nil {
		t.Fatalf("scheduleFinalJobOverlayFrames: %v", err)
	}
	if start != 0 || end != 120 {
		t.Fatalf("window = [%d,%d), want the free gap [0,120) before the planned start", start, end)
	}
}

func TestScheduleFinalJobImageOverlayShrinksToFreeSceneGap(t *testing.T) {
	result := &GenerateResult{
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
			Segments: []capabilityaudio.TimelineSegment{{
				ID: "scene-1", TimelineStartUS: 0, DurationUS: 10_000_000,
			}},
		},
	}
	item := capmodel.OverlayItem{
		ID: "image-late", SceneID: "scene-1", Kind: "image",
		StartUS: 5_000_000, DurationUS: 5_000_000,
	}
	occupied := map[string]finalJobFrameWindow{
		"text-a": {start: 0, end: 96, sceneID: "scene-1"},
		"text-b": {start: 96, end: 180, sceneID: "scene-1"},
	}

	start, end, err := scheduleFinalJobOverlayFrames(result, item, 120, 240, 24, 1, occupied)
	if err != nil {
		t.Fatalf("scheduleFinalJobOverlayFrames: %v", err)
	}
	if start != 180 || end != 240 {
		t.Fatalf("image window = [%d,%d), want largest free 60-frame gap [180,240)", start, end)
	}
}

func TestScheduleFinalJobNumberOverlayShrinksToFreeSceneGap(t *testing.T) {
	result := &GenerateResult{
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
			Segments: []capabilityaudio.TimelineSegment{{
				ID: "scene-1", TimelineStartUS: 0, DurationUS: 10_000_000,
			}},
		},
	}
	item := capmodel.OverlayItem{
		ID: "date-late", SceneID: "scene-1", Kind: "number",
		StartUS: 5_000_000, DurationUS: 5_000_000,
	}
	occupied := map[string]finalJobFrameWindow{
		"text-a": {start: 0, end: 180, sceneID: "scene-1"},
	}

	start, end, err := scheduleFinalJobOverlayFrames(result, item, 120, 240, 24, 1, occupied)
	if err != nil {
		t.Fatalf("scheduleFinalJobOverlayFrames: %v", err)
	}
	if start != 180 || end != 240 {
		t.Fatalf("number window = [%d,%d), want available 60-frame gap [180,240)", start, end)
	}
}

func TestScheduleFinalJobImageOverlayCanBeOmittedWhenSceneHasNoGap(t *testing.T) {
	result := &GenerateResult{
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
			Segments: []capabilityaudio.TimelineSegment{{
				ID: "scene-1", TimelineStartUS: 0, DurationUS: 10_000_000,
			}},
		},
	}
	item := capmodel.OverlayItem{
		ID: "image-late", SceneID: "scene-1", Kind: "image",
		StartUS: 5_000_000, DurationUS: 5_000_000,
	}
	occupied := map[string]finalJobFrameWindow{
		"full-scene": {start: 0, end: 240, sceneID: "scene-1"},
	}

	_, _, err := scheduleFinalJobOverlayFrames(result, item, 120, 240, 24, 1, occupied)
	if !errors.Is(err, errFinalJobOptionalOverlayNoFreeGap) {
		t.Fatalf("scheduleFinalJobOverlayFrames error = %v, want no-free-optional-overlay sentinel", err)
	}
}
