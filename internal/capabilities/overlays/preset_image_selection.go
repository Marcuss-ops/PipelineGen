package overlays

import (
	"encoding/binary"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

func selectImagePreset(jobID, sceneID, itemID string) string {
	return selectPreset(jobID, sceneID, itemID, "entity_image", imagePresetCandidates)
}

// SelectEntityImagePreset chooses the image motion independently from the
// entity name treatment. Both choices are made by the shared sampler so the
// renderer never has to infer or invent a preset.
func SelectEntityImagePreset(jobID, sceneID, itemID string) string {
	return selectImagePreset(jobID, sceneID, itemID)
}

// ImagePresetCandidates returns the certified `preset.image` vocabulary the
// seeded entity-image sampler selects from. It travels in the v2 request so the
// Rust planner can promote a card with the same entrance preset the Go sampler
// used to choose, without hardcoding a Chronon-owned id.
func ImagePresetCandidates() []string {
	return append([]string(nil), imagePresetCandidates...)
}

// CertifiedSingleImageMotions returns the complete certified single-image
// catalog, including the recipe families whose recipe lowers on one image.
// Multi-image stacks stay in the group lane and are not part of this pool.
func CertifiedSingleImageMotions() []string {
	return uniqueMotionPool(append(append([]string(nil), renderSafeImageMotions...), imageRecipeMotionCandidates...))
}

// ImageMotionOffsetForSeed returns a stable pool rotation for one immutable
// plan identity. Each newly assigned plan ID varies the rotation; replaying the
// same plan identity produces the same offset, as required for retries.
func ImageMotionOffsetForSeed(seed string) int {
	span := max(len(generatedEntityImageMotionCandidates), len(generatedEntityCaptionMotionCandidates), len(centeredImageMotionCandidates))
	if span == 0 {
		return 0
	}
	sum := digest.SHA256Sum([]byte("image-motion-offset\x00" + seed))
	return int(binary.BigEndian.Uint64(sum[:8]) % uint64(span))
}

// CertifiedEntityImageMotions returns the complete certified image-motion
// pool used by GENERATED entity-image overlays. Callers
// receive a copy.
func CertifiedEntityImageMotions() []string {
	return append([]string(nil), generatedEntityImageMotionCandidates...)
}

// CertifiedEntityCaptionMotions returns the complete certified caption-motion
// pool used by GENERATED entity-image overlays when a caption is attached.
// Callers receive a copy.
func CertifiedEntityCaptionMotions() []string {
	return append([]string(nil), generatedEntityCaptionMotionCandidates...)
}
