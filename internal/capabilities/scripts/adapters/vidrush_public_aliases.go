package adapters

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/vidrush"

type (
	MediaResolverImageStage         = vidrush.MediaResolverImageStage
	MediaResolver                   = vidrush.MediaResolver
	MediaManifestPins               = vidrush.MediaManifestPins
	VidRushProviderFanout           = vidrush.VidRushProviderFanout
	VidRushProviderSelection        = vidrush.VidRushProviderSelection
	ProviderPreference              = vidrush.ProviderPreference
	VidRushProviderSelector         = vidrush.VidRushProviderSelector
	VidRushAssetProviderRegistry    = vidrush.VidRushAssetProviderRegistry
	VidRushRegistryMediaResolver    = vidrush.VidRushRegistryMediaResolver
	VidRushMaterializationProcessor = vidrush.VidRushMaterializationProcessor
	VisualPlanningProcessor         = vidrush.VisualPlanningProcessor
	VidRushImagePolicy              = vidrush.VidRushImagePolicy
	VidRushCacheStats               = vidrush.VidRushCacheStats
	VidRushGenerationRequest        = vidrush.VidRushGenerationRequest
	SegmentTimingBudget             = vidrush.SegmentTimingBudget
	SegmentProviderDecision         = vidrush.SegmentProviderDecision
	OptionalEntityImageScorer       = vidrush.OptionalEntityImageScorer
	EntityImageCatalogDecision      = vidrush.EntityImageCatalogDecision
	EntityImageContentStore         = vidrush.EntityImageContentStore
)

const (
	MediaManifestProvider         = vidrush.MediaManifestProvider
	MediaManifestCacheState       = vidrush.MediaManifestCacheState
	MinInternetImageSemanticScore = vidrush.MinInternetImageSemanticScore
)

var (
	LoadMediaManifestPins                         = vidrush.LoadMediaManifestPins
	LoadMediaManifestPinsFromEnv                  = vidrush.LoadMediaManifestPinsFromEnv
	NewMediaResolverImageStageWithCatalog         = vidrush.NewMediaResolverImageStageWithCatalog
	NewVidRushProviderFanout                      = vidrush.NewVidRushProviderFanout
	NewVidRushProviderFanoutWithYouTube           = vidrush.NewVidRushProviderFanoutWithYouTube
	NewVidRushProviderFanoutWithCache             = vidrush.NewVidRushProviderFanoutWithCache
	NewVidRushProviderFanoutWithCatalog           = vidrush.NewVidRushProviderFanoutWithCatalog
	NewVidRushProviderFanoutWithResolver          = vidrush.NewVidRushProviderFanoutWithResolver
	NewVidRushProviderSelector                    = vidrush.NewVidRushProviderSelector
	NewVidRushAssetProviderRegistry               = vidrush.NewVidRushAssetProviderRegistry
	NewVidRushMaterializationProcessor            = vidrush.NewVidRushMaterializationProcessor
	NewVidRushMaterializationProcessorWithCatalog = vidrush.NewVidRushMaterializationProcessorWithCatalog
	NewVisualPlanningProcessor                    = vidrush.NewVisualPlanningProcessor
	NewOptionalEntityImageScorer                  = vidrush.NewOptionalEntityImageScorer
	NewStockResolveCache                          = vidrush.NewStockResolveCache

	ProjectEntityImageBindings          = vidrush.ProjectEntityImageBindings
	EntityImageDriveLeaf                = vidrush.EntityImageDriveLeaf
	DeduplicateVidRushSegments          = vidrush.DeduplicateVidRushSegments
	FinalizeVidRushBindings             = vidrush.FinalizeVidRushBindings
	FinalizeVidRushBindingsWithCache    = vidrush.FinalizeVidRushBindingsWithCache
	ValidateVidRushSegmentAssetBindings = vidrush.ValidateVidRushSegmentAssetBindings
	CloneVidRushSegmentResult           = vidrush.CloneVidRushSegmentResult
	AppendProviderCandidatesUnique      = vidrush.AppendProviderCandidatesUnique
	ResolveManualSegmentQueries         = vidrush.ResolveManualSegmentQueries
	ManualSearchSlots                   = vidrush.ManualSearchSlots
	VidRushCacheMetrics                 = vidrush.VidRushCacheMetrics
	VidRushGenerationCacheKey           = vidrush.VidRushGenerationCacheKey
	ResolveSegmentTimingBudget          = vidrush.ResolveSegmentTimingBudget

	DefaultVidRushImagePolicy        = vidrush.DefaultVidRushImagePolicy
	ValidateVidRushRemoteImageURL    = vidrush.ValidateVidRushRemoteImageURL
	ProviderImageHTTPClient          = vidrush.ProviderImageHTTPClient
	DownloadVidRushImageForCandidate = vidrush.DownloadVidRushImageForCandidate
	VerifyVidRushImageBytes          = vidrush.VerifyVidRushImageBytes
	VerifyVidRushImageFile           = vidrush.VerifyVidRushImageFile
	CanonicalizeVidRushImageFile     = vidrush.CanonicalizeVidRushImageFile
	PersonImageSourceIdentityFailure = vidrush.PersonImageSourceIdentityFailure
	PersonImageSourceIdentityPenalty = vidrush.PersonImageSourceIdentityPenalty
)
