package script

import mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"

// MediaRequirement is the resolved acquisition policy for one optional
// media capability. It is deliberately separate from a planner budget:
//
//	disabled — do not acquire, do not certify
//	optional — acquire when possible; a miss is a valid degraded outcome
//	required — a miss fails the run
type MediaRequirement string

const (
	MediaRequirementDisabled MediaRequirement = "disabled"
	MediaRequirementOptional MediaRequirement = "optional"
	MediaRequirementRequired MediaRequirement = "required"
)

// MediaRequirements is the canonical output contract for image/media
// acquisition. It is resolved once from the request at plan-build time so
// downstream certification does not reinterpret a planner budget
// (ImagesPerScene) as a hard requirement.
type MediaRequirements struct {
	// SceneImages reports whether the run requested generic scene imagery.
	// Entity-only image requests leave this false: entity portraits are
	// overlays, not the scene visual lane.
	SceneImages bool `json:"scene_images"`
	// EntityImages records whether the entity-image catalog is disabled,
	// optional, or required.
	EntityImages MediaRequirement `json:"entity_images,omitempty"`
	// ImageFanout is the number of generic scene images the run requires.
	// It is zero when SceneImages is false, regardless of the planner budget.
	ImageFanout int `json:"image_fanout,omitempty"`
}

// Clone returns a defensive copy. Nil stays nil.
func (r *MediaRequirements) Clone() *MediaRequirements {
	if r == nil {
		return nil
	}
	copy := *r
	return &copy
}

// ResolveMediaRequirements is the single conversion from request toggles to
// acquisition requirements. Both plan builders (the canonical generation
// builder and the live SceneTextGenerator) call it, so certification cannot
// see a different image contract from the executor.
func ResolveMediaRequirements(
	mediaMode MediaMode,
	generateSceneImages Toggle,
	imagesPerScene int,
	overlayCategories map[OverlayCategory]Toggle,
	entityImages mediadomain.EntityImagePolicy,
) *MediaRequirements {
	sceneImages := generateSceneImages != ToggleDisabled &&
		imagesPerScene > 0 &&
		mediaMode != MediaModeStockOnly && mediaMode != MediaModeClipOnly
	if toggle, ok := overlayCategories[OverlayCategoryImages]; ok && toggle == ToggleDisabled {
		sceneImages = false
	}
	entityRequirement := MediaRequirementDisabled
	if entityImages.Enabled {
		entityRequirement = MediaRequirementOptional
		if entityImages.Required {
			entityRequirement = MediaRequirementRequired
		}
	}
	fanout := 0
	if sceneImages {
		fanout = imagesPerScene
	}
	return &MediaRequirements{
		SceneImages:  sceneImages,
		EntityImages: entityRequirement,
		ImageFanout:  fanout,
	}
}

// RequestsImageAcquisition reports whether any image lane is enabled by the
// resolved contract. Nil requirements preserve legacy behavior at call sites
// that have not been migrated yet.
func (p *ResolvedGenerationPlan) RequestsImageAcquisition() bool {
	if p == nil || p.MediaRequirements == nil {
		return true
	}
	return p.MediaRequirements.SceneImages || p.MediaRequirements.EntityImages != MediaRequirementDisabled
}
