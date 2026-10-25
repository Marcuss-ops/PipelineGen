package overlays

import capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"

// Model-side limits re-exported under their planner-facing unexported names.
const (
	maxMapRasterWidth  = capmodel.MaxMapRasterWidth
	maxMapRasterHeight = capmodel.MaxMapRasterHeight
)

// intentID aliases the model-level id derivation (exported IntentID).
var intentID = capmodel.IntentID

// overlay_model_alias.go is the GENERATED facade over the model leaf.
// It re-exports exactly the model identifiers referenced outside the model
// package (overlays planning layer, scripts, platform, plus overlay tests).
// Regenerate by rescanning, never by hand-editing types.
// Regenerate with: go run ./cmd/overlay-model-alias-gen --generate

type (
	AnimationPolicy         = capmodel.AnimationPolicy
	ChainSegment            = capmodel.ChainSegment
	ChrononOverlayRegistry  = capmodel.ChrononOverlayRegistry
	EntityBinding           = capmodel.EntityBinding
	EntityImageOverlayInput = capmodel.EntityImageOverlayInput
	EntityOverlayInput      = capmodel.EntityOverlayInput
	IntentPayload           = capmodel.IntentPayload
	MapCameraMove           = capmodel.MapCameraMove
	MapCenter               = capmodel.MapCenter
	MapOverlay              = capmodel.MapOverlay
	MapOverlayLOD           = capmodel.MapOverlayLOD
	MapOverlayPin           = capmodel.MapOverlayPin
	MediaProber             = capmodel.MediaProber
	OverlayAnnotationInput  = capmodel.OverlayAnnotationInput
	OverlayAssetRef         = capmodel.OverlayAssetRef
	OverlayBackground       = capmodel.OverlayBackground
	OverlayEntityRef        = capmodel.OverlayEntityRef
	OverlayImageLayer       = capmodel.OverlayImageLayer
	OverlayIntakeBudget     = capmodel.OverlayIntakeBudget
	OverlayIntent           = capmodel.OverlayIntent
	OverlayItem             = capmodel.OverlayItem
	OverlayItemFrame        = capmodel.OverlayItemFrame
	OverlayItemFrameBorder  = capmodel.OverlayItemFrameBorder
	OverlayItemFrameShadow  = capmodel.OverlayItemFrameShadow
	OverlayItemFrameStroke  = capmodel.OverlayItemFrameStroke
	OverlayKind             = capmodel.OverlayKind
	OverlayMediaContract    = capmodel.OverlayMediaContract
	OverlayPlan             = capmodel.OverlayPlan
	OverlayPlanIntake       = capmodel.OverlayPlanIntake
	OverlayProbeResult      = capmodel.OverlayProbeResult
	OverlaySoundEffect      = capmodel.OverlaySoundEffect
	OverlaySource           = capmodel.OverlaySource
	RenderRequest           = capmodel.RenderRequest
	RenderResult            = capmodel.RenderResult
	SceneEntityInput        = capmodel.SceneEntityInput
	TimingState             = capmodel.TimingState
)

const (
	IntentSourceEntity           = capmodel.IntentSourceEntity
	IntentSourceEntityImage      = capmodel.IntentSourceEntityImage
	JobTypePrepare               = capmodel.JobTypePrepare
	JobTypeRender                = capmodel.JobTypeRender
	KindBrandText                = capmodel.KindBrandText
	KindChapterCard              = capmodel.KindChapterCard
	KindConcept                  = capmodel.KindConcept
	KindEntityCard               = capmodel.KindEntityCard
	KindEntityImage              = capmodel.KindEntityImage
	KindImagePopup               = capmodel.KindImagePopup
	KindImportantPhrase          = capmodel.KindImportantPhrase
	KindImportantWord            = capmodel.KindImportantWord
	KindLocation                 = capmodel.KindLocation
	KindLogo                     = capmodel.KindLogo
	KindLowerThird               = capmodel.KindLowerThird
	KindMap                      = capmodel.KindMap
	KindMixedChain               = capmodel.KindMixedChain
	KindNumber                   = capmodel.KindNumber
	KindOrganization             = capmodel.KindOrganization
	KindPhraseChain              = capmodel.KindPhraseChain
	KindProduct                  = capmodel.KindProduct
	KindQuote                    = capmodel.KindQuote
	OverlayDropCategoryDisabled  = capmodel.OverlayDropCategoryDisabled
	OverlayDropCompositionMerged = capmodel.OverlayDropCompositionMerged
	OverlayDropEditorialBudget   = capmodel.OverlayDropEditorialBudget
	OverlayDropEntityImageBudget = capmodel.OverlayDropEntityImageBudget
	OverlayDropGroupLimit        = capmodel.OverlayDropGroupLimit
	OverlayDropMapsOnly          = capmodel.OverlayDropMapsOnly
	OverlayDropPerSceneCap       = capmodel.OverlayDropPerSceneCap
	OverlayDropUnmaterialized    = capmodel.OverlayDropUnmaterialized
	OverlayIntentVersion         = capmodel.OverlayIntentVersion
	OverlayStatusReady           = capmodel.OverlayStatusReady
	SchemaVersionPlan            = capmodel.SchemaVersionPlan
	SchemaVersionResult          = capmodel.SchemaVersionResult
	TimingStateFrozen            = capmodel.TimingStateFrozen
	TimingStatePending           = capmodel.TimingStatePending
)

var (
	CoalesceChainedOverlays       = capmodel.CoalesceChainedOverlays
	ComputeRenderKey              = capmodel.ComputeRenderKey
	ContractIDForCanvas           = capmodel.ContractIDForCanvas
	DecodeChainSegments           = capmodel.DecodeChainSegments
	DefaultChrononOverlayRegistry = capmodel.DefaultChrononOverlayRegistry
	DefaultOverlayContractNoAlpha = capmodel.DefaultOverlayContractNoAlpha
	DefaultOverlayContractV1      = capmodel.DefaultOverlayContractV1
	EntityTypeToKind              = capmodel.EntityTypeToKind
	IntentID                      = capmodel.IntentID
	IsChainItemKind               = capmodel.IsChainItemKind
	NewOverlayAssetRef            = capmodel.NewOverlayAssetRef
	OverlayContractForCanvas      = capmodel.OverlayContractForCanvas
	PlanOverlayIntents            = capmodel.PlanOverlayIntents
	RebaseChainSegments           = capmodel.RebaseChainSegments
	RenderKey                     = capmodel.RenderKey
	ResolveMediaContract          = capmodel.ResolveMediaContract
	ValidateResultForPlan         = capmodel.ValidateResultForPlan
)
