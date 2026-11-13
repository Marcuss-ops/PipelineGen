package scriptgeneration

import (
	"fmt"
	"strings"
)

const (
	maxGenericSceneImagesPerScene = 10
	// maxWholeScenePhraseWords bounds the no-word-timing fallback that turns a
	// scene into one phrase card. Without it, a scene with phrases enabled but
	// no certified word timing (the overlay plan runs before TTS timing is
	// attached) projects its entire narration as a single phrase; a 106-word
	// Floyd Mayweather scene then fails Chronon at render time. Keep the
	// fallback inside the same 16-word ceiling as the grounded long-phrase lane.
	maxWholeScenePhraseWords = 16
)

// wholeScenePhraseText returns the verbatim scene text only when it can be
// rendered as one phrase card. Longer narration keeps its images, entities and
// maps, but contributes no whole-scene phrase instead of an unrenderable card.
func wholeScenePhraseText(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || len(strings.Fields(trimmed)) > maxWholeScenePhraseWords {
		return "", false
	}
	return trimmed, true
}

// resolveImageOverlayCeiling makes the two run-level image budgets agree.
// max_image_overlays is the caller's request; overlay_group_limits.images is
// the group cap. Before this gate the group silently overwrote the max, so a
// payload asking for 12 images could render 3 without saying so. Zero keeps
// the certified default and is not a disagreement.
func resolveImageOverlayCeiling(maxImageOverlays int, limits map[string]int) (int, error) {
	if n, ok := overlayGroupLimit(limits, overlayGroupImages); ok && n > 0 {
		if maxImageOverlays > 0 && n != maxImageOverlays {
			return 0, fmt.Errorf("overlay plan: max_image_overlays=%d and overlay_group_limits.%s=%d disagree; remove one or make them equal so the run cannot silently render a different image count", maxImageOverlays, overlayGroupImages, n)
		}
		return n, nil
	}
	return maxImageOverlays, nil
}
