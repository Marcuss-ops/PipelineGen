package overlays

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/animreg"

// The animation registry lives in package animreg (leaf, no overlay
// dependencies). These aliases keep every overlay caller unchanged.
type (
	AnimationEntry    = animreg.AnimationEntry
	AnimationRegistry = animreg.AnimationRegistry
)

var canonicalAnimationRegistry = animreg.Canonical()

var (
	AnimationCandidates               = animreg.AnimationCandidates
	AnimationSelectable               = animreg.AnimationSelectable
	AnimationRendererSupported        = animreg.AnimationRendererSupported
	AnimationRandomCandidates         = animreg.AnimationRandomCandidates
	AnimationIsRetired                = animreg.AnimationIsRetired
	RetiredAnimationCandidates        = animreg.RetiredAnimationCandidates
	AnimationWordCount                = animreg.AnimationWordCount
	AnimationLaneCompatible           = animreg.AnimationLaneCompatible
	AnimationTargetCandidates         = animreg.AnimationTargetCandidates
	AnimationSupportsTarget           = animreg.AnimationSupportsTarget
	AnimationMapTypeBasemap           = animreg.AnimationMapTypeBasemap
	AnimationRegistryMapTypeDefault   = animreg.AnimationRegistryMapTypeDefault
	AnimationSupportsMapType          = animreg.AnimationSupportsMapType
	AnimationMapCompositionCandidates = animreg.AnimationMapCompositionCandidates
)
