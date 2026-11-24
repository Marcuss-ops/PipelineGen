// renderunits_aliases.go — facade for the render-unit projection.
//
// The decomposition of a scene into atomic localized-render units is a
// pure projection over the generate contract: it depends on contract, the
// kernel job/script value types and nothing else, so it lives in the leaf
// scripts/renderunits. The fan-out that consumes it stays in the root
// (runner_fixed_render.go) because it spawns work on the run. The bridges
// below keep the historical unexported names so no call site moved.
package scriptgeneration

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/renderunits"

// SceneRenderUnit is one atomic localized-render fan-out item.
type SceneRenderUnit = renderunits.SceneRenderUnit

// Exported projection surface used by other packages.
var (
	RenderUnitsForScene = renderunits.RenderUnitsForScene
	RenderUnitCount     = renderunits.RenderUnitCount
)

// Internal seams kept under their historical names.
var (
	sceneClips                         = renderunits.SceneClips
	sceneStockClipIDs                  = renderunits.SceneStockClipIDs
	primaryVisualClip                  = renderunits.PrimaryVisualClip
	sceneVisualClipsAreStockOnly       = renderunits.SceneVisualClipsAreStockOnly
	sceneRenderSpec                    = renderunits.SceneRenderSpec
	sceneClipOwnsCaption               = renderunits.SceneClipOwnsCaption
	localizedRenderClipFields          = renderunits.LocalizedRenderClipFields
	localizedRenderUnitClipFields      = renderunits.LocalizedRenderUnitClipFields
	localizedRenderCaptionText         = renderunits.LocalizedRenderCaptionText
	renderLanguages                    = renderunits.RenderLanguages
	fixedRenderLanguages               = renderunits.FixedRenderLanguages
	localizedRenderErrorCapacity       = renderunits.LocalizedRenderErrorCapacity
	fixedCaptionText                   = renderunits.FixedCaptionText
	fixedMediaNeedsLocalizedRender     = renderunits.FixedMediaNeedsLocalizedRender
	fixedMediaVisualTreatmentRequested = renderunits.FixedMediaVisualTreatmentRequested
	expectedRenderUnits                = renderunits.ExpectedRenderUnits
	fixedMediaRenderUnits              = renderunits.FixedMediaRenderUnits
	recordRenderStageProgress          = renderunits.RecordRenderStageProgress
	recordRenderStageFailure           = renderunits.RecordRenderStageFailure
	recordRenderStageStatus            = renderunits.RecordRenderStageStatus
	renderUnitKey                      = renderunits.RenderUnitKey
)
