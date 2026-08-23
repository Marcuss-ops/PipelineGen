package jobs

import "time"

func registerAssemblyEntries(r *Registry) {
	_ = r.Register(JobPolicy{
		Completion:  CompletionDeclaration{JobType: TypeAssemblyPrepare, ArtifactOwnership: ArtifactOwnershipNone, FinalizationStrategy: FinalizationStrategyLegacyComplete},
		Description: "Prefetch and validate assembly source assets on Velox", Timeout: 30 * time.Minute,
		DefaultMaxRetries: 2, Queue: DefaultQueue, Concurrency: 2,
		RequiredCapabilities: []string{"assembly.prepare", "media.render"},
	})
	_ = r.Register(JobPolicy{
		Completion:  CompletionDeclaration{JobType: TypeAssemblyFinalize, ArtifactOwnership: ArtifactOwnershipWorkerSpine, FinalizationStrategy: FinalizationStrategyCompleteWithArtifacts},
		Description: "Finalize a prepared assembly on Velox", Timeout: 30 * time.Minute,
		DefaultMaxRetries: 2, Queue: DefaultQueue, Concurrency: 2,
		RequiredCapabilities: []string{"assembly.finalize", "media.render"},
	})
}
