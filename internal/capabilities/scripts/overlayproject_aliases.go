// overlayproject_aliases.go — facade over the overlay projection leaf.
//
// Numeric grounding, SFX projection and the scene-annotation helpers are pure
// functions over the generate contract and the kernel script policy: they live
// in scripts/overlayproject and the root keeps the historical names.
package scriptgeneration

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/overlayproject"

var (
	annotationsForLanguage        = overlayproject.AnnotationsForLanguage
	imageOverlayItemsBySceneWhere = overlayproject.ImageOverlayItemsBySceneWhere
	isNumericOverlayValue         = overlayproject.IsNumericOverlayValue
	overlaySFXIntentsForPlan      = overlayproject.OverlaySFXIntentsForPlan
	recoverNumericSuffix          = overlayproject.RecoverNumericSuffix
	overlaySFXCueGainDB           = overlayproject.OverlaySFXCueGainDB
	overlaySFXCueDurationMS       = overlayproject.OverlaySFXCueDurationMS
	cleanNumericOverlayValue      = overlayproject.CleanNumericOverlayValue
)
