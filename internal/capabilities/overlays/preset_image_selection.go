package overlays

import (
	"crypto/rand"
	"math/big"
)

func selectWordPreset(jobID, sceneID, itemID string) string {
	return selectPreset(jobID, sceneID, itemID, "important_word", wordPresetCandidates)
}

func selectImagePreset(jobID, sceneID, itemID string) string {
	return selectPreset(jobID, sceneID, itemID, "entity_image", imagePresetCandidates)
}

// SelectEntityImagePreset chooses the image motion independently from the
// entity name treatment. Both choices are made by the shared sampler so the
// renderer never has to infer or invent a preset.
func SelectEntityImagePreset(jobID, sceneID, itemID string) string {
	return selectImagePreset(jobID, sceneID, itemID)
}

// SelectEntityImageAnimation chooses a stable-but-varied Chronon entry
// animation for an entity image. Retries of the same job remain bit-identical.
func SelectEntityImageAnimation(jobID, sceneID, itemID string) string {
	return selectPreset(jobID, sceneID, itemID, "entity_image_animation", imageAnimationCandidates)
}

// SelectImageAnimation is the generic image/product/logo planner selector.
func SelectImageAnimation(jobID, sceneID, itemID string) string {
	return SelectEntityImageAnimation(jobID, sceneID, itemID)
}

// CertifiedImageMotions returns the complete certified image motion pool.
// Callers receive a copy.
func CertifiedImageMotions() []string {
	return append([]string(nil), imageMotionCandidates...)
}

// SelectImageMotion chooses a stable catalog image motion for one image
// overlay. Retries of the same job, scene and item resolve identically.
// Generated overlays rotate ONLY the centered subset: a portrait or scene
// image must stay pinned to the canvas center while it reveals (the same
// editorial rule maps already follow); slide/25d motions that carry the card
// across the canvas remain certified for explicit editorial plans but are
// never selected here.
func SelectImageMotion(jobID, sceneID, itemID string) string {
	return selectPreset(jobID, sceneID, itemID, "image_motion", centeredImageMotionCandidates)
}

// SelectImageMotionAt rotates through the certified CENTERED image pool
// from a deterministic per-job starting point. This gives each image in a run
// a different motion while allowing later jobs to start at a different point.
func SelectImageMotionAt(jobID, sceneID string, ordinal int) string {
	return selectImageMotion(jobID, sceneID, ordinal, centeredImageMotionCandidates)
}

// RandomImageMotionOffset chooses a fresh cryptographically random starting
// offset for one render plan. The caller samples it once, then rotates through
// the centered pool so no two images in the same run repeat before all are used.
func RandomImageMotionOffset() (int, error) {
	if len(centeredImageMotionCandidates) == 0 {
		return 0, nil
	}
	start, err := rand.Int(rand.Reader, big.NewInt(int64(len(centeredImageMotionCandidates))))
	if err != nil {
		return 0, err
	}
	return int(start.Int64()), nil
}

// ImageMotionAtOffset returns the image motion at a position in the randomly
// rotated CENTERED pool — the pool generated image overlays rotate over.
func ImageMotionAtOffset(offset, ordinal int) string {
	if len(centeredImageMotionCandidates) == 0 {
		return ""
	}
	index := ((offset % len(centeredImageMotionCandidates)) + ordinal) % len(centeredImageMotionCandidates)
	return centeredImageMotionCandidates[index]
}

// EntityImageParams returns the entity-image card geometry, scaled to the
// output canvas while keeping enough margin for image motion. On 1920×1080 the
// card is 960×864 (50% width, capped at 80% height): a readable portrait, not
// a corner stamp.
func EntityImageParams(width, height int) map[string]any {
	if width <= 0 || height <= 0 {
		return map[string]any{"box_width": 480, "box_height": 480}
	}
	size := width * 50 / 100
	if maxHeight := height * 80 / 100; maxHeight < size {
		size = maxHeight
	}
	if size < 1 {
		size = 1
	}
	// "width"/"height" are the keys RenderingGen's imageLayer() reads;
	// "box_width"/"box_height" stay for consumers of the legacy spelling.
	return map[string]any{"box_width": size, "box_height": size, "width": size, "height": size}
}

func selectImageMotion(jobID, sceneID string, ordinal int, pool []string) string {
	candidates := imageMotionCandidates
	if len(pool) > 0 {
		candidates = pool
	}
	if len(candidates) == 0 {
		return ""
	}
	seeded := selectPreset(jobID, sceneID, sceneID, "image_motion", candidates)
	start := 0
	for i, candidate := range candidates {
		if candidate == seeded {
			start = i
			break
		}
	}
	return candidates[(start+ordinal)%len(candidates)]
}
