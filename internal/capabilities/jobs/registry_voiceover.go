package jobs

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

import "time"

// registerVoiceoverEntries registers all voiceover/subtitle job types
// into the canonical registry. Called by Compose() after the base
// registry is created.
//
// LONG-FILES-SPLIT-2026-07-06 Band A #7: extracted from registry.go's
// Compose() function per AGENTS.md Pattern 5.
func registerVoiceoverEntries(r *Registry) {
	// PR-COMPLETE-WORKER-FIX-TYPE-VOICEOVER-BATCH (July 2026):
	// ProducesArtifacts REMOVED. The voiceover batch pipeline persists ALL
	// voiceover artifacts (voiceovers row + media_assets projection +
	// asset.index outbox event + voiceover.cleanup outbox event) atomically
	// inside the per-item caller-owned tx through
	// VoiceoverFinalizer.Finalize (internal/application/voiceover/finalizer.go)
	// — distinct from the JobFinalizer.CompleteWithArtifacts spine that
	// script.generate uses. Marking ProducesArtifacts=false re-routes the
	// broker's "mark SUCCEEDED" path through the legacy SQLiteStore.Complete
	// which is the CANONICAL terminal-flip seam for this job type today.
	r.Register(JobPolicy{Completion: CompletionDeclaration{JobType: TypeVoiceoverBatch, ArtifactOwnership: ArtifactOwnershipApplication, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "Voiceover batch generation (per-item artifacts persisted inside the per-item caller-owned tx via Service.GenerateBatch → finalizeStage → voiceover.Finalizer.Finalize → tx.Commit; broker's legacy Complete is the canonical mark-SUCCEEDED seam)", Timeout: 30 * time.Minute, DefaultMaxRetries: 2})
	// PR-COMPLETE-WORKER-FIX-TYPE-VOICEOVER-PROMO (July 2026):
	// ProducesArtifacts REMOVED. Mirrors the voiceover batch fix above.
	r.Register(JobPolicy{Completion: CompletionDeclaration{JobType: TypeVoiceoverPromo, ArtifactOwnership: ArtifactOwnershipApplication, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "Voiceover promo generation (translate + generate) (per-item artifacts persisted inside the per-item caller-owned tx via Service.GeneratePromo → promo.NewGenerator → promoVoiceoverAdapter → ProcessVoiceoverItemUseCase.Execute → ProcessSegmentUseCase.Execute → voiceover.Finalizer.Finalize → tx.Commit; broker's legacy Complete is the canonical mark-SUCCEEDED seam)", Timeout: 30 * time.Minute, DefaultMaxRetries: 2})
	// PR-VO-COMPLETEPATH-FIX (July 2026): ProducesArtifacts REMOVED.
	r.Register(JobPolicy{Completion: CompletionDeclaration{JobType: TypeVoiceoverGenerate, ArtifactOwnership: ArtifactOwnershipApplication, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "Voiceover single generation (per-batch command, Blocco 4 typed-port cutover); artifact persistence delegated to voiceover.Finalizer inside the per-item tx — broker's legacy Complete is the canonical mark-SUCCEEDED seam", Timeout: 30 * time.Minute, DefaultMaxRetries: 2})
	// PR-VO-COMPLETEPATH-FIX (July 2026): ProducesArtifacts REMOVED.
	r.Register(JobPolicy{Completion: CompletionDeclaration{JobType: TypeVoiceoverGenerateItem, ArtifactOwnership: ArtifactOwnershipApplication, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "Voiceover per-language child (P0.3 fan-out: parent voiceover.generate schedules 1 job per (language, voice) pair; concurrency 4 = sibling throttle); artifact persistence delegated to voiceover.Finalizer inside the per-item tx — broker's legacy Complete is the canonical mark-SUCCEEDED seam", Timeout: 10 * time.Minute, DefaultMaxRetries: 2, Concurrency: 4})
	r.Register(JobPolicy{Completion: CompletionDeclaration{JobType: TypeSubtitleGenerate, ArtifactOwnership: ArtifactOwnershipNone, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "Subtitle generation", Timeout: 10 * time.Minute, DefaultMaxRetries: 2})
}
