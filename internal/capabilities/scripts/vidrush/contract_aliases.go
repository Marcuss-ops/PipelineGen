package vidrush

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"

type (
	ProcessInput        = contract.ProcessInput
	PostProcessResult   = contract.PostProcessResult
	PostProcessor       = contract.PostProcessor
	SceneVoiceover      = contract.SceneVoiceover
	SceneImage          = contract.SceneImage
	ProcessorName       = contract.ProcessorName
	ProcessorPolicy     = contract.ProcessorPolicy
	ProcessorDescriptor = contract.ProcessorDescriptor

	InternetImageSearchRequest = contract.InternetImageSearchRequest
	InternetImageSearcher      = contract.InternetImageSearcher
	VidRushTimingMetrics       = contract.VidRushTimingMetrics
	VidRushMetrics             = contract.VidRushMetrics
)

const (
	ProcessorRequired   ProcessorPolicy = contract.ProcessorRequired
	ProcessorBestEffort ProcessorPolicy = contract.ProcessorBestEffort

	ProcessorClipSearch                  = contract.ProcessorClipSearch
	ProcessorMetadata                    = contract.ProcessorMetadata
	ProcessorTranslation                 = contract.ProcessorTranslation
	ProcessorClipBindings                = contract.ProcessorClipBindings
	ProcessorStockBindings               = contract.ProcessorStockBindings
	ProcessorAssetLocationReconciliation = contract.ProcessorAssetLocationReconciliation
	ProcessorVisualPlanning              = contract.ProcessorVisualPlanning
	ProcessorVisualSlots                 = contract.ProcessorVisualSlots
	ProcessorVoiceover                   = contract.ProcessorVoiceover
	ProcessorImages                      = contract.ProcessorImages
	ProcessorInternetImages              = contract.ProcessorInternetImages
	ProcessorVidRushMaterialization      = contract.ProcessorVidRushMaterialization
	ProcessorPersistence                 = contract.ProcessorPersistence
	ProcessorNarrationSanitizer          = contract.ProcessorNarrationSanitizer
)

var (
	DefaultPolicyFor          = contract.DefaultPolicyFor
	CanonicalProcessorNames   = contract.CanonicalProcessorNames
	RequiredProcessorNames    = contract.RequiredProcessorNames
	ProcessorNamesToStrings   = contract.ProcessorNamesToStrings
	ProcessorNamesFromStrings = contract.ProcessorNamesFromStrings

	SceneImageDriveLink    = contract.SceneImageDriveLink
	CloneSceneBindings     = contract.CloneSceneBindings
	cloneSpecSceneOutput   = contract.CloneSpecSceneOutput
	cloneAnnotatedEntities = contract.CloneAnnotatedEntities
)
