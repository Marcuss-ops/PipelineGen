package overlays

import (
	"fmt"
	"strings"
)

// This file holds the planner's motion-pool helpers. Split from planner.go to
// stay under max_lines_per_file_strict=600 (godlike/08 forward-prevention
// gate); behaviour is unchanged.
// itemPriority reads the editorial priority the planner stamped on an admitted
// item's params. A missing or non-numeric value is 0, which never satisfies a
// positive HeavyPhrasePriority: an unweighted item can never be promoted into
// the heavy lane by accident.
func itemPriority(item OverlayItem) float64 {
	if item.Params == nil {
		return 0
	}
	switch value := item.Params["priority"].(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	default:
		return 0
	}
}

// phraseMotionParams gives the entrance one third of the phrase's on-screen
// duration. Motion catalog windows are frame counts, so convert that duration
// at the output frame rate before sending the plan.
func phraseMotionParams(phrase TimedAnnotation, fpsNum, fpsDen int) map[string]any {
	if fpsNum <= 0 || fpsDen <= 0 {
		return nil
	}
	phraseDurationUS := phrase.DurationUS
	if phraseDurationUS <= 0 && phrase.EndMs > phrase.StartMs {
		phraseDurationUS = (phrase.EndMs - phrase.StartMs) * 1_000
	}
	framesNumerator := phraseDurationUS * int64(fpsNum)
	framesDenominator := 3_000_000 * int64(fpsDen)
	frames := (framesNumerator + framesDenominator - 1) / framesDenominator
	if frames < 1 {
		frames = 1
	}
	return map[string]any{"enter_frames": int(frames)}
}

// validatePhraseMotionPool fails closed on a caller-supplied motion pool that
// names an id this build cannot render, or that would make the rotation
// ambiguous (duplicates). An empty pool is the certified default and is
// always valid.
func validatePhraseMotionPool(pool []string) error {
	if len(pool) == 0 {
		return nil
	}
	certified := make(map[string]bool)
	for _, id := range CertifiedPhraseMotions() {
		certified[id] = true
	}
	seen := make(map[string]bool, len(pool))
	for _, id := range pool {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("overlay: phrase motion pool carries an empty id")
		}
		if !certified[id] {
			return fmt.Errorf("overlay: phrase motion %q is not a certified motion", id)
		}
		if seen[id] {
			return fmt.Errorf("overlay: phrase motion pool repeats %q", id)
		}
		seen[id] = true
	}
	return nil
}

func validateImageMotionPool(pool []string) error {
	if len(pool) == 0 {
		return nil
	}
	certified := make(map[string]bool)
	for _, id := range CertifiedSingleImageMotions() {
		certified[id] = true
	}
	seen := make(map[string]bool, len(pool))
	for _, id := range pool {
		if !certified[id] {
			return fmt.Errorf("overlay planner: image motion %q is not in the certified single-image pool", id)
		}
		if seen[id] {
			return fmt.Errorf("overlay planner: image motion pool repeats %q", id)
		}
		seen[id] = true
	}
	return nil
}
