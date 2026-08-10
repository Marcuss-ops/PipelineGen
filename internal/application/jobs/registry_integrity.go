package jobs

import "time"

const (
	TypeIntegrityVerify = "integrity.verify"
	TypeAssetCleanup    = "asset.cleanup"
)

func registerIntegrityEntries(r *Registry) {
	_ = r.Register(RegistryEntry{
		Completion: CompletionDeclaration{
			JobType:              TypeIntegrityVerify,
			ArtifactOwnership:    ArtifactOwnershipNone,
			FinalizationStrategy: FinalizationStrategyLegacyComplete,
		},
		Description:       "Integrity verification",
		Timeout:           10 * time.Minute,
		DefaultMaxRetries: 3,
		Queue:             DefaultQueue,
		Concurrency:       DefaultConcurrency,
	})
	_ = r.Register(RegistryEntry{
		Completion: CompletionDeclaration{
			JobType:              TypeAssetCleanup,
			ArtifactOwnership:    ArtifactOwnershipNone,
			FinalizationStrategy: FinalizationStrategyLegacyComplete,
		},
		Description:       "Asset cleanup",
		Timeout:           10 * time.Minute,
		DefaultMaxRetries: 3,
		Queue:             DefaultQueue,
		Concurrency:       DefaultConcurrency,
	})
}
