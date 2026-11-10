package jobs

import "time"

const (
	TypeIntegrityVerify = "integrity.verify"
	TypeAssetCleanup    = "asset.cleanup"
)

func registerIntegrityEntries(r *Registry) {
	// Fail-closed at composition time: a duplicate or invalid entry must
	// never vanish into a swallowed error — the "integrity" family in
	// particular cannot silently skip its own integrity.
	mustRegister(r, RegistryEntry{
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
	mustRegister(r, RegistryEntry{
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
