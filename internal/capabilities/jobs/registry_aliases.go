// Package jobs — registry_aliases.go: compatibility facade for the job-type
// policy registry, whose canonical implementation now lives in
// internal/capabilities/jobs/scheduling.
//
// The registry owns scheduling policy (timeout, retries, queue, concurrency,
// completion declarations), so it belongs beside the other scheduling policy
// owners. Keeping these aliases preserves every existing jobs.Registry /
// jobs.Compose / jobs.Type* call site while the root package contracts toward
// a thin facade.
package jobs

import jobscheduling "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/scheduling"

type (
	// Registry is the canonical job-type policy registry.
	Registry = jobscheduling.Registry
	// RegistryEntry is the per-job-type policy record.
	RegistryEntry = jobscheduling.RegistryEntry
	// JobPolicy is the canonical alias for RegistryEntry.
	JobPolicy = jobscheduling.JobPolicy
	// CompletionDeclaration declares artifact ownership and finalization.
	CompletionDeclaration = jobscheduling.CompletionDeclaration
	// ArtifactOwnership identifies the artifact owner for a job type.
	ArtifactOwnership = jobscheduling.ArtifactOwnership
	// FinalizationStrategy identifies the terminal completion API.
	FinalizationStrategy = jobscheduling.FinalizationStrategy
	// TimeoutMap is the type-keyed timeout snapshot.
	TimeoutMap = jobscheduling.TimeoutMap
	// TimeoutResolver is the typed per-job timeout port.
	TimeoutResolver = jobscheduling.TimeoutResolver
)

const (
	ArtifactOwnershipNone        = jobscheduling.ArtifactOwnershipNone
	ArtifactOwnershipWorkerSpine = jobscheduling.ArtifactOwnershipWorkerSpine
	ArtifactOwnershipApplication = jobscheduling.ArtifactOwnershipApplication

	FinalizationStrategyLegacyComplete                 = jobscheduling.FinalizationStrategyLegacyComplete
	FinalizationStrategyCompleteWithArtifacts          = jobscheduling.FinalizationStrategyCompleteWithArtifacts
	FinalizationStrategyCompleteWithArtifactsThenDefer = jobscheduling.FinalizationStrategyCompleteWithArtifactsThenDefer

	DefaultQueue       = jobscheduling.DefaultQueue
	DefaultConcurrency = jobscheduling.DefaultConcurrency

	TypeMediaExtract           = jobscheduling.TypeMediaExtract
	TypeMediaStock             = jobscheduling.TypeMediaStock
	TypeVoiceoverBatch         = jobscheduling.TypeVoiceoverBatch
	TypeSubtitleGenerate       = jobscheduling.TypeSubtitleGenerate
	TypeYouTubeUpload          = jobscheduling.TypeYouTubeUpload
	TypeYouTubeStock           = jobscheduling.TypeYouTubeStock
	TypeCatalogSync            = jobscheduling.TypeCatalogSync
	TypeArtlistRun             = jobscheduling.TypeArtlistRun
	TypeArtlistCacheRefresh    = jobscheduling.TypeArtlistCacheRefresh
	TypeSystemCleanup          = jobscheduling.TypeSystemCleanup
	TypeMediaGenerate          = jobscheduling.TypeMediaGenerate
	TypeMediaEnrich            = jobscheduling.TypeMediaEnrich
	TypeYouTubeRebuildST       = jobscheduling.TypeYouTubeRebuildST
	TypeDriveFolderSync        = jobscheduling.TypeDriveFolderSync
	TypeMediaCurate            = jobscheduling.TypeMediaCurate
	TypeVoiceoverPromo         = jobscheduling.TypeVoiceoverPromo
	TypeVoiceoverGenerateItem  = jobscheduling.TypeVoiceoverGenerateItem
	TypeImageGenerateGoogle    = jobscheduling.TypeImageGenerateGoogle
	TypeAssetsResolve          = jobscheduling.TypeAssetsResolve
	TypeScriptVoiceoverSibling = jobscheduling.TypeScriptVoiceoverSibling
	TypeScriptImageSibling     = jobscheduling.TypeScriptImageSibling
	TypeScriptGenerateItem     = jobscheduling.TypeScriptGenerateItem
	TypeClipRegister           = jobscheduling.TypeClipRegister
	TypeMediaStockRLMEnrich    = jobscheduling.TypeMediaStockRLMEnrich
	TypeVoiceoverGenerate      = jobscheduling.TypeVoiceoverGenerate
	TypeYouTubeClipExtract     = jobscheduling.TypeYouTubeClipExtract
	TypeScriptGenerate         = jobscheduling.TypeScriptGenerate
	TypeImagesGenerate         = jobscheduling.TypeImagesGenerate
	TypeBulkUploadYouTubeClips = jobscheduling.TypeBulkUploadYouTubeClips
	TypeAssetTextMaterialize   = jobscheduling.TypeAssetTextMaterialize
	TypeYouTubeExtract         = jobscheduling.TypeYouTubeExtract
	TypeClipRender             = jobscheduling.TypeClipRender
	TypeAssemblyPrepare        = jobscheduling.TypeAssemblyPrepare
	TypeAssemblyFinalize       = jobscheduling.TypeAssemblyFinalize
)

var ErrInvalidCompletionDeclaration = jobscheduling.ErrInvalidCompletionDeclaration

// NewRegistry creates an empty unfrozen registry.
func NewRegistry() *Registry {
	return jobscheduling.NewRegistry()
}

// Compose builds the canonical registry with every registered job type.
func Compose() *Registry {
	return jobscheduling.Compose()
}

// SpineJobTypes returns the registered types that require worker-spine
// artifact finalization.
func SpineJobTypes() []string {
	return jobscheduling.SpineJobTypes()
}
