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

// CertifiedSingleImageMotions returns the curated animation pool used by
// automatic single-image overlays and channel-profile overrides.
func CertifiedSingleImageMotions() []string {
	return append([]string(nil), singleImageMotionCandidates...)
}

// CertifiedEntityCaptionMotions returns all registered entity-caption motions.
// Callers receive a copy so the producer's vocabulary cannot be mutated.
func CertifiedEntityCaptionMotions() []string {
	return append([]string(nil), generatedEntityCaptionMotionCandidates...)
}

// EntityCaptionMotionAtOffset rotates captions deterministically without
// repeating a motion until the curated pool has been exhausted.
func EntityCaptionMotionAtOffset(offset, ordinal int) string {
	return rotateMotionAtOffset(offset, ordinal, generatedEntityCaptionMotionCandidates)
}

func entityCaptionMotionAtOffset(ordinal, limit int) string {
	return rotateMotionAtOffset(0, ordinal, limitedMotionPool(generatedEntityCaptionMotionCandidates, limit))
}

func entityImageMotionAtOffset(ordinal, limit int) string {
	return rotateMotionAtOffset(0, ordinal, limitedMotionPool(generatedEntityImageMotionCandidates, limit))
}

// SelectEntityCaptionMotionAt provides a stable per-job selector for semantic
// render bundles that do not carry the full plan's sampled motion offset.
func SelectEntityCaptionMotionAt(jobID, sceneID string, ordinal int) string {
	pool := generatedEntityCaptionMotionCandidates
	if len(pool) == 0 {
		return ""
	}
	seeded := selectPreset(jobID, sceneID, "run", "entity_caption_motion", pool)
	start := 0
	for index, candidate := range pool {
		if candidate == seeded {
			start = index
			break
		}
	}
	return pool[(start+ordinal)%len(pool)]
}

// SelectImageMotion chooses a stable catalog image motion for one image
// overlay. Retries of the same job, scene and item resolve identically. It
// stays on the CENTERED subset for map-compatible callers (MapOverlay.Validate
// accepts exactly those ids). Generated overlay images use
// EntityImageMotionAtOffset instead.
func SelectImageMotion(jobID, sceneID, itemID string) string {
	return selectPreset(jobID, sceneID, itemID, "image_motion", centeredImageMotionCandidates)
}

// SelectImageMotionAt rotates through the certified CENTERED image pool
// from a deterministic per-job starting point. It is reserved for
// map-compatible callers; generated overlays rotate the wider image catalog.
func SelectImageMotionAt(jobID, sceneID string, ordinal int) string {
	return selectImageMotion(jobID, sceneID, ordinal, centeredImageMotionCandidates)
}

// RandomImageMotionOffset chooses a fresh cryptographically random starting
// offset for one render plan. The caller samples it ONCE per plan and each
// visual lane reduces it modulo its own pool size, keeping image and caption
// rotations independent but stable for the compiled plan.
func RandomImageMotionOffset() (int, error) {
	span := max(len(generatedEntityImageMotionCandidates), len(generatedEntityCaptionMotionCandidates), len(centeredImageMotionCandidates))
	if span == 0 {
		return 0, nil
	}
	start, err := rand.Int(rand.Reader, big.NewInt(int64(span)))
	if err != nil {
		return 0, err
	}
	return int(start.Int64()), nil
}

// ImageMotionAtOffset returns the image motion at a position in the randomly
// rotated CENTERED pool, reserved for basemap-compatible callers.
func ImageMotionAtOffset(offset, ordinal int) string {
	return rotateMotionAtOffset(offset, ordinal, centeredImageMotionCandidates)
}

// EntityImageMotionAtOffset returns the image motion at a position in the
// randomly rotated certified catalog for generated overlays. The
// offset is the plan-level entropy value sampled by RandomImageMotionOffset;
// the pool reduces it modulo its own size.
func EntityImageMotionAtOffset(offset, ordinal int) string {
	return rotateMotionAtOffset(offset, ordinal, generatedEntityImageMotionCandidates)
}

// ImageWithTextMotionAtOffset rotates the separate image entrance pool used
// when an image carries an entity label. The five-style cap matches the
// runtime default; callers may supply a lower configured cap.
func ImageWithTextMotionAtOffset(offset, ordinal, limit int) string {
	return rotateMotionAtOffset(offset, ordinal, limitedMotionPool(generatedImageWithTextMotionCandidates, limit))
}

// SelectEntityImageMotionAt rotates generated-image motions from a
// deterministic per-job starting point. It is for stable semantic bundle
// projections that cannot carry the compilation-time random offset.
func SelectEntityImageMotionAt(jobID, sceneID string, ordinal int) string {
	if len(generatedEntityImageMotionCandidates) == 0 {
		return ""
	}
	seeded := selectPreset(jobID, sceneID, "run", "entity_image_motion", generatedEntityImageMotionCandidates)
	start := 0
	for i, candidate := range generatedEntityImageMotionCandidates {
		if candidate == seeded {
			start = i
			break
		}
	}
	return generatedEntityImageMotionCandidates[(start+ordinal)%len(generatedEntityImageMotionCandidates)]
}

// rotateMotionAtOffset is the shared modular rotation of one pool from a
// per-plan random starting offset.
func rotateMotionAtOffset(offset, ordinal int, pool []string) string {
	if len(pool) == 0 {
		return ""
	}
	index := ((offset % len(pool)) + ordinal) % len(pool)
	return pool[index]
}

// CertifiedEntityImageMotions returns the complete certified image-motion
// pool used by GENERATED entity-image overlays. Callers
// receive a copy.
func CertifiedEntityImageMotions() []string {
	return append([]string(nil), generatedEntityImageMotionCandidates...)
}

// CertifiedImageWithTextMotions returns the dedicated certified image
// entrances used by image overlays with an entity caption.
func CertifiedImageWithTextMotions() []string {
	return append([]string(nil), generatedImageWithTextMotionCandidates...)
}

// EntityImageParams returns the larger, raised hero box used by entity images.
// Entity captions anchor below it, so the portrait stays in the center-safe area.

func EntityImageParams(width, height int) map[string]any {
	if width <= 0 || height <= 0 {
		return map[string]any{"box_width": 800, "box_height": 650, "width": 800, "height": 650, "position_y": -60}
	}
	boxWidth, boxHeight := width*68/100, height*70/100
	if boxWidth < 1 {
		boxWidth = 1
	}
	if boxHeight < 1 {
		boxHeight = 1
	}
	return map[string]any{"box_width": boxWidth, "box_height": boxHeight, "width": boxWidth, "height": boxHeight, "position_y": -float64(height) * 0.10}
}

// ImageOverlayParams gives a standalone photo one consistent bounded box.
// Explicit cover fit fills it without contain's dark matte on mismatched
// source aspect ratios.
func ImageOverlayParams(width, height int) map[string]any {
	if width <= 0 || height <= 0 {
		return map[string]any{"box_width": 800, "box_height": 450, "width": 800, "height": 450, "position_y": 0, "fit": "cover"}
	}
	boxWidth, boxHeight := width*68/100, height*70/100
	if boxWidth < 1 {
		boxWidth = 1
	}
	if boxHeight < 1 {
		boxHeight = 1
	}
	return map[string]any{"box_width": boxWidth, "box_height": boxHeight, "width": boxWidth, "height": boxHeight, "position_y": 0, "fit": "cover"}
}

func selectImageMotion(jobID, sceneID string, ordinal int, pool []string) string {
	candidates := singleImageMotionCandidates
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
