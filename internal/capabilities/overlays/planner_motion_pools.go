package overlays

import (
	"fmt"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"strings"
)

// This file holds the planner's motion-pool helpers. Split from planner.go to
// stay under max_lines_per_file_strict=600 (godlike/08 forward-prevention
// gate); behaviour is unchanged.
// itemPriority reads the editorial priority the planner stamped on an admitted
// item's params. A missing or non-numeric value is 0, which never satisfies a
// positive HeavyPhrasePriority: an unweighted item can never be promoted into
// the heavy lane by accident.
func itemPriority(item capmodel.OverlayItem) float64 {
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

// PhraseEntranceFrames gives a phrase a short entrance and leaves the rest of
// its certified window settled and readable. The one-third share is capped at
// 700ms so a long display window cannot turn a simple text entrance into a
// multi-second transition.
func PhraseEntranceFrames(durationUS int64, fpsNum, fpsDen int) int64 {
	if durationUS <= 0 || fpsNum <= 0 || fpsDen <= 0 {
		return 0
	}
	numerator := durationUS * int64(fpsNum)
	denominator := 3_000_000 * int64(fpsDen)
	frames := (numerator + denominator - 1) / denominator
	maxFrames := (700_000*int64(fpsNum) + 1_000_000*int64(fpsDen) - 1) / (1_000_000 * int64(fpsDen))
	if frames > maxFrames {
		frames = maxFrames
	}
	return frames
}

// PhraseExitFrames returns the visible opacity fade-out window. A phrase must
// leave screen instead of cutting hard, but the exit is a fade ONLY: mirroring
// the entrance position/scale tracks is what drove settled text back through
// the authoring gray. The window is ~8 frames (about 0.33s at 24fps), bounded
// to a quarter of the card so a very short phrase keeps readable hold time.
func PhraseExitFrames(durationUS int64, fpsNum, fpsDen int) int64 {
	if durationUS <= 0 || fpsNum <= 0 || fpsDen <= 0 {
		return 0
	}
	totalFrames := (durationUS*int64(fpsNum) + 1_000_000*int64(fpsDen) - 1) / (1_000_000 * int64(fpsDen))
	exitFrames := int64(8)
	if maxExit := totalFrames / 4; exitFrames > maxExit {
		exitFrames = maxExit
	}
	if exitFrames < 2 {
		return 0
	}
	return exitFrames
}

// phraseMotionParams gives the entrance one third of the phrase's on-screen
// duration and reserves a short opacity-only exit. Motion catalog windows are
// frame counts, so convert both durations at the output frame rate before
// sending the plan. RenderingGen holds non-opacity tracks during the exit, so
// the card fades without replaying the entrance transforms.
func phraseMotionParams(phrase TimedAnnotation, fpsNum, fpsDen int) map[string]any {
	if fpsNum <= 0 || fpsDen <= 0 {
		return nil
	}
	phraseDurationUS := phrase.DurationUS
	if phraseDurationUS <= 0 && phrase.EndMs > phrase.StartMs {
		phraseDurationUS = (phrase.EndMs - phrase.StartMs) * 1_000
	}
	frames := PhraseEntranceFrames(phraseDurationUS, fpsNum, fpsDen)
	if frames < 1 {
		frames = 1
	}
	exitFrames := PhraseExitFrames(phraseDurationUS, fpsNum, fpsDen)
	return map[string]any{"enter_frames": int(frames), "exit_frames": int(exitFrames)}
}

// validatePhraseMotionPool fails closed on a caller-supplied motion pool that
// names an id this build cannot render, or that would make the rotation
// ambiguous (duplicates). An empty pool is the certified default and is
// always valid. The long-phrase lane additionally rotates the native
// important_phrase highlight accents, so those long-lane ids are admitted
// here too; lanes that cannot use them (short phrases) ignore them at
// intersection time instead of failing the run.
func validatePhraseMotionPool(pool []string) error {
	if len(pool) == 0 {
		return nil
	}
	certified := make(map[string]bool)
	for _, id := range CertifiedPhraseMotions() {
		certified[id] = true
	}
	for _, id := range LongPhraseMotionCandidates() {
		certified[id] = true
	}
	seen := make(map[string]bool, len(pool))
	for _, id := range pool {
		id = ResolveDocumentaryPhraseMotion(id)
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("overlay: phrase motion pool carries an empty id")
		}
		if !certified[id] {
			return fmt.Errorf("overlay: phrase motion %q is not a certified motion", id)
		}
		if IsRemovedGeneratedPhraseMotion(id) {
			return fmt.Errorf("overlay: phrase motion %q was retired from generated overlays", id)
		}
		if seen[id] {
			return fmt.Errorf("overlay: phrase motion pool repeats %q", id)
		}
		seen[id] = true
	}
	return nil
}

// normalizePinnedPhraseMotion validates and canonicalizes one lane-wide
// payload pin. A pin is an explicit caller choice, so it is not intersected
// with the phrase pool: it replaces the lane's rotation for the whole run.
// Short pins must be general short-lane entrances because one id is applied to
// phrases of different word counts; exact-cardinality short recipes cannot be
// used as a single lane-wide choice. Long pins must belong to the certified
// long-phrase lane and must not be a known long-copy crash.
func normalizePinnedPhraseMotion(id, lane string) (string, error) {
	id = ResolveDocumentaryPhraseMotion(strings.TrimSpace(id))
	if id == "" {
		return "", nil
	}
	if IsRemovedGeneratedPhraseMotion(id) {
		return "", fmt.Errorf("overlay planner: %s_phrase_motion %q was retired from generated overlays", lane, id)
	}
	switch lane {
	case "short":
		if !containsMotion(ShortPhraseVisibleMotionCandidates(), id) && !IsShortPhraseRecipe(id) {
			return "", fmt.Errorf("overlay planner: short_phrase_motion %q is not a short-phrase entrance", id)
		}
	case "long":
		if !containsMotion(LongPhraseMotionCandidates(), id) {
			return "", fmt.Errorf("overlay planner: long_phrase_motion %q is not a certified long-phrase motion", id)
		}
		if isLongCopyCrashingMotion(id) {
			return "", fmt.Errorf("overlay planner: long_phrase_motion %q is unsafe on long copy", id)
		}
	default:
		return "", fmt.Errorf("overlay planner: unknown phrase-motion lane %q", lane)
	}
	return id, nil
}

// validateShortPhraseRecipePin keeps an exact-cardinality recipe safe when it
// is used as a lane-wide pin: the same id must be valid for every short phrase
// in the run, so mixed word counts would hand the renderer a recipe authored
// for a different count. General short entrances are valid for every count.
func validateShortPhraseRecipePin(id string, scenes []SceneInput) error {
	want, recipe := ShortPhraseMotionWordCount(id)
	if !recipe {
		return nil
	}
	for _, scene := range scenes {
		for _, phrase := range scene.Phrases {
			if phrase.PresentationRole == ChapterBulletRole {
				continue
			}
			words := len(strings.Fields(phrase.Text))
			if words < 1 || words >= 6 || words == want {
				continue
			}
			return fmt.Errorf("overlay planner: short_phrase_motion %q is a %d-word recipe but phrase %q has %d words", id, want, phrase.Text, words)
		}
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

// imageWithTextPoolFor returns the caller's captioned-image pool, or the
// certified full seeded pool when the payload omits it.
func imageWithTextPoolFor(input PlanInput) []string {
	if len(input.ImageWithTextMotions) > 0 {
		return input.ImageWithTextMotions
	}
	return ImageWithTextMotionPool(laneAnimationCount(input.AnimationCounts, laneImagesWithText))
}

// validateImageWithTextMotionPool fails closed on an id outside the certified
// image-with-text catalog or on duplicates.
func validateImageWithTextMotionPool(pool []string) error {
	if len(pool) == 0 {
		return nil
	}
	certified := make(map[string]bool)
	for _, id := range ImageWithTextMotionPool(0) {
		certified[id] = true
	}
	seen := make(map[string]bool, len(pool))
	for _, id := range pool {
		if !certified[id] {
			return fmt.Errorf("overlay planner: image_with_text motion %q is not in the certified image-with-text pool", id)
		}
		if seen[id] {
			return fmt.Errorf("overlay planner: image_with_text motion pool repeats %q", id)
		}
		seen[id] = true
	}
	return nil
}
