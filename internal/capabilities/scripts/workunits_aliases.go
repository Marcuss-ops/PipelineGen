// workunits_aliases.go — facade over the scene→work-unit projections.
//
// The dispatch priority of scene×language units and the fixed-section
// bindings are pure projections over the generate contract and the kernel
// script policy: they live in the leaf scripts/workunits and the root keeps
// the historical names so no call site moved.
package scriptgeneration

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/workunits"

type (
	sceneLanguageWork = workunits.SceneLanguageWork
)

var (
	applyDurableStockBindingToScene = workunits.ApplyDurableStockBindingToScene
	applyDurableStockBindings       = workunits.ApplyDurableStockBindings
	applyExplicitSegmentDuration    = workunits.ApplyExplicitSegmentDuration
	applyFixedSections              = workunits.ApplyFixedSections
	applySegmentClipBindings        = workunits.ApplySegmentClipBindings
	applySegmentNoVoiceoverScenes   = workunits.ApplySegmentNoVoiceoverScenes
	buildSceneLanguageWork          = workunits.BuildSceneLanguageWork
	buildVoiceoverLanguageWork      = workunits.BuildVoiceoverLanguageWork
	committedSegmentSourceText      = workunits.CommittedSegmentSourceText
	dispatchLanguagePriority        = workunits.DispatchLanguagePriority
	fixedMediaClipProjection        = workunits.FixedMediaClipProjection
	languageStrings                 = workunits.LanguageStrings
	newVoiceoverLanguageFilter      = workunits.NewVoiceoverLanguageFilter
	orderedSceneLanguages           = workunits.OrderedSceneLanguages
	requestedSceneLanguages         = workunits.RequestedSceneLanguages
	sceneExtractionToggle           = workunits.SceneExtractionToggle
)
