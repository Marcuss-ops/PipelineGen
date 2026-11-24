package scriptgeneration

import (
	"errors"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

// TestScheduleFinalJobOverlayFramesAcceptsAnItemEndingAtTheSceneTail pins the
// r18 failure: the last phrase of the video ended exactly at the scene tail,
// but the scene end was floored while the item end was ceiled, so the item
// looked one frame longer than an empty scene and could never be placed.
func TestScheduleFinalJobOverlayFramesAcceptsAnItemEndingAtTheSceneTail(t *testing.T) {
	const sceneEndUS = int64(1_119_456_000) // 26_866.944 frames at 24 fps
	result := &GenerateResult{AudioOutput: AudioOutput{CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
		Segments: []capabilityaudio.TimelineSegment{{
			ID: "scene-05", TimelineStartUS: 1_116_521_000, DurationUS: sceneEndUS - 1_116_521_000,
		}},
	}},
	}
	item := capmodel.OverlayItem{
		ID: "last-phrase", SceneID: "scene-05", Kind: "text_phrase",
		StartUS: 1_116_521_000, DurationUS: sceneEndUS - 1_116_521_000,
	}

	start, end, err := scheduleFinalJobOverlayFrames(
		result, item, 26_797, 26_867, 24, 1, map[string]finalJobFrameWindow{},
	)
	if err != nil {
		t.Fatalf("a phrase that ends with its scene must fit: %v", err)
	}
	if start != 26_797 || end != 26_867 {
		t.Fatalf("window = [%d,%d), want the planned [26797,26867)", start, end)
	}
}

// TestScheduleFinalJobOverlayFramesUsesFreeGapBeforePlannedStart pins the r8
// failure: a date card planned near the scene tail was blocked by a later
// overlay, and the forward-only search rejected the whole final job even
// though the scene had free time before the card. The scheduler must keep the
// card inside its scene, as close as possible to the planned start.
func TestScheduleFinalJobOverlayFramesUsesFreeGapBeforePlannedStart(t *testing.T) {
	result := &GenerateResult{AudioOutput: AudioOutput{CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
		Segments: []capabilityaudio.TimelineSegment{{
			ID: "scene-1", TimelineStartUS: 0, DurationUS: 10_000_000,
		}},
	}},
	}
	item := capmodel.OverlayItem{
		ID: "date-late", SceneID: "scene-1", Kind: "number",
		StartUS: 5_000_000, DurationUS: 5_000_000,
	}
	occupied := map[string]finalJobFrameWindow{
		"map-late": {Start: 120, End: 240, SceneID: "scene-1"},
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
	result := &GenerateResult{AudioOutput: AudioOutput{CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
		Segments: []capabilityaudio.TimelineSegment{{
			ID: "scene-1", TimelineStartUS: 0, DurationUS: 10_000_000,
		}},
	}},
	}
	item := capmodel.OverlayItem{
		ID: "image-late", SceneID: "scene-1", Kind: "image",
		StartUS: 5_000_000, DurationUS: 5_000_000,
	}
	occupied := map[string]finalJobFrameWindow{
		"text-a": {Start: 0, End: 96, SceneID: "scene-1"},
		"text-b": {Start: 96, End: 180, SceneID: "scene-1"},
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
	result := &GenerateResult{AudioOutput: AudioOutput{CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
		Segments: []capabilityaudio.TimelineSegment{{
			ID: "scene-1", TimelineStartUS: 0, DurationUS: 10_000_000,
		}},
	}},
	}
	item := capmodel.OverlayItem{
		ID: "date-late", SceneID: "scene-1", Kind: "number",
		StartUS: 5_000_000, DurationUS: 5_000_000,
	}
	occupied := map[string]finalJobFrameWindow{
		"text-a": {Start: 0, End: 180, SceneID: "scene-1"},
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
	result := &GenerateResult{AudioOutput: AudioOutput{CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
		Segments: []capabilityaudio.TimelineSegment{{
			ID: "scene-1", TimelineStartUS: 0, DurationUS: 10_000_000,
		}},
	}},
	}
	item := capmodel.OverlayItem{
		ID: "image-late", SceneID: "scene-1", Kind: "image",
		StartUS: 5_000_000, DurationUS: 5_000_000,
	}
	occupied := map[string]finalJobFrameWindow{
		"full-scene": {Start: 0, End: 240, SceneID: "scene-1"},
	}

	_, _, err := scheduleFinalJobOverlayFrames(result, item, 120, 240, 24, 1, occupied)
	if !errors.Is(err, errFinalJobOptionalOverlayNoFreeGap) {
		t.Fatalf("scheduleFinalJobOverlayFrames error = %v, want no-free-optional-overlay sentinel", err)
	}
}
