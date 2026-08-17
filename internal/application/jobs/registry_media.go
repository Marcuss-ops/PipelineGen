package jobs

import "time"

// registerMediaEntries registers all remaining media job types
// (video, catalog, content, system, AI image) into the canonical
// registry. Called by Compose() after the base registry is created.
//
// LONG-FILES-SPLIT-2026-07-06 Band A #7: extracted from registry.go's
// Compose() function per AGENTS.md Pattern 5.
func registerMediaEntries(r *Registry) {
	// ── YouTube ──
	r.Register(JobPolicy{Completion: CompletionDeclaration{JobType: TypeYouTubeUpload, ArtifactOwnership: ArtifactOwnershipNone, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "YouTube upload", Timeout: 30 * time.Minute, DefaultMaxRetries: 2})

	// ── Catalog / sync ──
	r.Register(JobPolicy{Completion: CompletionDeclaration{JobType: TypeCatalogSync, ArtifactOwnership: ArtifactOwnershipNone, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "Catalog synchronization", Timeout: 2 * time.Minute, DefaultMaxRetries: 2})
	r.Register(JobPolicy{Completion: CompletionDeclaration{JobType: TypeArtlistRun, ArtifactOwnership: ArtifactOwnershipNone, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "Artlist run", Timeout: 30 * time.Minute, DefaultMaxRetries: 1})
	r.Register(JobPolicy{Completion: CompletionDeclaration{JobType: TypeArtlistCacheRefresh, ArtifactOwnership: ArtifactOwnershipNone, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "Refresh a stale Artlist live-search cache entry", Timeout: 2 * time.Minute, DefaultMaxRetries: 3})
	r.Register(JobPolicy{Completion: CompletionDeclaration{JobType: TypeDriveFolderSync, ArtifactOwnership: ArtifactOwnershipNone, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "Drive folder sync", Timeout: 30 * time.Minute, DefaultMaxRetries: 1})

	// ── Clip render (canonical VeloxEditing-compatible clip post-processing) ──
	// The worker commits its own derived media_assets row + provenance in a
	// per-job tx (mirror of media.clip), so the broker's legacy Complete is
	// the canonical mark-SUCCEEDED seam. ArtifactOwnershipApplication +
	// FinalizationStrategyLegacyComplete.
	r.Register(JobPolicy{Completion: CompletionDeclaration{JobType: TypeClipRender, ArtifactOwnership: ArtifactOwnershipApplication, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "Clip render (background/watermark/subtitles baked in one render pass -> VeloxEditing-compatible derived media asset + provenance)", Timeout: 30 * time.Minute, DefaultMaxRetries: 2})

	// ── Content processing ──
	r.Register(JobPolicy{Completion: CompletionDeclaration{JobType: TypeBooksProcess, ArtifactOwnership: ArtifactOwnershipWorkerSpine, FinalizationStrategy: FinalizationStrategyCompleteWithArtifacts}, Description: "Book processing", Timeout: 30 * time.Minute, DefaultMaxRetries: 2})
	r.Register(JobPolicy{Completion: CompletionDeclaration{JobType: TypeLessonsProcess, ArtifactOwnership: ArtifactOwnershipWorkerSpine, FinalizationStrategy: FinalizationStrategyCompleteWithArtifacts}, Description: "Lesson processing", Timeout: 30 * time.Minute, DefaultMaxRetries: 2})

	// ── System ──
	r.Register(JobPolicy{Completion: CompletionDeclaration{JobType: TypeSystemCleanup, ArtifactOwnership: ArtifactOwnershipNone, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "System cleanup", Timeout: 2 * time.Minute, DefaultMaxRetries: 1})

	// ── AI Image Generation ──
	// FASE 2 (June 2026): ChromeImageProvider → Playwright → slides.new → Nano Banana Pro.
	r.Register(JobPolicy{Completion: CompletionDeclaration{JobType: TypeImageGenerateGoogle, ArtifactOwnership: ArtifactOwnershipWorkerSpine, FinalizationStrategy: FinalizationStrategyCompleteWithArtifacts}, Description: "Google Slides AI image generation (Chrome + Playwright)", Timeout: 15 * time.Minute, DefaultMaxRetries: 2, RequiredCapabilities: []string{"image_gen_chrome"}})
}
