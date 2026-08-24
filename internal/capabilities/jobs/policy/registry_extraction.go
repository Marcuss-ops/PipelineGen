package policy

import (
	"time"

	youtubejob "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube"
)

// registerExtractionEntries registers all extraction/YouTube job types
// into the canonical registry. Called by Compose() after the base
// registry is created.
//
// LONG-FILES-SPLIT-2026-07-06 Band A #7: extracted from registry.go's
// Compose() function per AGENTS.md Pattern 5.
func registerExtractionEntries(r *Registry) {
	// PR-COMPLETE-WORKER-BROAD-FIX Path D (July 2026): ProducesArtifacts REMOVED.
	// TypeMediaExtract is an orphaned registry entry — no production handler
	// is statically registered. Flipping to false ensures the SQL-layer guard
	// doesn't block the legacy Complete path if a handler is later registered.
	r.Register(JobPolicy{Completion: CompletionDeclaration{JobType: TypeMediaExtract, ArtifactOwnership: ArtifactOwnershipNone, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "Media extraction", Timeout: 30 * time.Minute, DefaultMaxRetries: 2})

	// PR-YOUTUBE-EXTRACT-REGISTRY (July 2026): youtube.extract is the
	// canonical registry entry for the YouTube clip extraction path
	// wired by internal/app/c3ValidateRuntimeGraph. The worker path
	// persists its own media_assets row + outbox event in the caller-owned tx,
	// so the broker's legacy Complete is the canonical mark-SUCCEEDED seam.
	r.Register(JobPolicy{Completion: CompletionDeclaration{JobType: youtubejob.TypeExtract, ArtifactOwnership: ArtifactOwnershipApplication, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "youtube clip extraction (URL -> media_assets row + outbox)", Timeout: 60 * time.Minute, DefaultMaxRetries: 2})
	// The canonical extractor commits each clip's media_assets row and
	// outbox event before returning; the broker only marks the job complete.
	r.Register(JobPolicy{Completion: CompletionDeclaration{JobType: youtubejob.TypeStock, ArtifactOwnership: ArtifactOwnershipNone, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "YouTube transcript-first stock clips", Timeout: 60 * time.Minute, DefaultMaxRetries: 2, Concurrency: 2})

	// PR-COMPLETE-WORKER-YT-FIX (July 2026): ProducesArtifacts REMOVED.
	r.Register(JobPolicy{Completion: CompletionDeclaration{JobType: TypeYouTubeClipExtract, ArtifactOwnership: ArtifactOwnershipApplication, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "YouTube clip extraction (per-segment artifacts persisted inside the per-segment caller-owned tx via process_segment + ClipAtomicWriter; broker's legacy Complete is the canonical mark-SUCCEEDED seam)", Timeout: 60 * time.Minute, DefaultMaxRetries: 2})
	r.Register(JobPolicy{Completion: CompletionDeclaration{JobType: TypeYouTubeRebuildST, ArtifactOwnership: ArtifactOwnershipNone, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "Rebuild YouTube search text", Timeout: 10 * time.Minute, DefaultMaxRetries: 1})

	// ── Clip registration (async batch, PR-BATCH-REGISTER-ASYNC) ──
	r.Register(JobPolicy{Completion: CompletionDeclaration{JobType: TypeClipRegister, ArtifactOwnership: ArtifactOwnershipApplication, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "Async clip registration (PR-BATCH-REGISTER-ASYNC: yt-dlp download + cut + Drive upload + DB write off the request thread)", Timeout: 30 * time.Minute, DefaultMaxRetries: 2})

}
