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
	registerMediaPolicy(r, JobPolicy{Completion: CompletionDeclaration{JobType: TypeYouTubeUpload, ArtifactOwnership: ArtifactOwnershipNone, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "YouTube upload", Timeout: 30 * time.Minute, DefaultMaxRetries: 2})

	// ── Catalog / sync ──
	registerMediaPolicy(r, JobPolicy{Completion: CompletionDeclaration{JobType: TypeCatalogSync, ArtifactOwnership: ArtifactOwnershipNone, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "Catalog synchronization", Timeout: 2 * time.Minute, DefaultMaxRetries: 2})
	registerMediaPolicy(r, JobPolicy{Completion: CompletionDeclaration{JobType: TypeArtlistRun, ArtifactOwnership: ArtifactOwnershipNone, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "Artlist run", Timeout: 30 * time.Minute, DefaultMaxRetries: 1})
	registerMediaPolicy(r, JobPolicy{Completion: CompletionDeclaration{JobType: TypeArtlistCacheRefresh, ArtifactOwnership: ArtifactOwnershipNone, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "Refresh a stale Artlist live-search cache entry", Timeout: 2 * time.Minute, DefaultMaxRetries: 3})
	registerMediaPolicy(r, JobPolicy{Completion: CompletionDeclaration{JobType: TypeDriveFolderSync, ArtifactOwnership: ArtifactOwnershipNone, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "Drive folder sync", Timeout: 30 * time.Minute, DefaultMaxRetries: 1})

	// ── Clip render (canonical VeloxEditing-compatible clip post-processing) ──
	// The worker commits its own derived media_assets row + provenance in a
	// per-job tx (mirror of media.clip), so the broker's legacy Complete is
	// the canonical mark-SUCCEEDED seam. ArtifactOwnershipApplication +
	// FinalizationStrategyLegacyComplete.
	registerMediaPolicy(r, JobPolicy{Completion: CompletionDeclaration{JobType: TypeClipRender, ArtifactOwnership: ArtifactOwnershipApplication, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "Clip render (background/watermark/subtitles baked in one render pass -> VeloxEditing-compatible derived media asset + provenance)", Timeout: 30 * time.Minute, DefaultMaxRetries: 2})

	// ── Video create (durable end-to-end video workflow parent) ──
	// The workflow's finalizer commits its own Drive publication +
	// media_assets identity + content hash in the publish stage (mirror of
	// media.clip / clip.render application-owned publish), because the typed
	// VideoCreateResult must carry the published media_url / drive_file_id /
	// sha256 to the polling caller. ArtifactOwnershipApplication +
	// FinalizationStrategyLegacyComplete.
	registerMediaPolicy(r, JobPolicy{Completion: CompletionDeclaration{JobType: TypeVideoCreate, ArtifactOwnership: ArtifactOwnershipApplication, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "Video create (script/media/voiceover/audio/render/assemble/mux/verify/publish -> verified final video)", Timeout: 3 * time.Hour, DefaultMaxRetries: 2, RequiredCapabilities: []string{"video.create"}})

	// ── System ──
	registerMediaPolicy(r, JobPolicy{Completion: CompletionDeclaration{JobType: TypeSystemCleanup, ArtifactOwnership: ArtifactOwnershipNone, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "System cleanup", Timeout: 2 * time.Minute, DefaultMaxRetries: 1}) // ── AI Image Generation ──
	// FASE 2 (June 2026): ChromeImageProvider → Playwright → slides.new → Nano Banana Pro.
	registerMediaPolicy(r, JobPolicy{Completion: CompletionDeclaration{JobType: TypeImageGenerateGoogle, ArtifactOwnership: ArtifactOwnershipWorkerSpine, FinalizationStrategy: FinalizationStrategyCompleteWithArtifacts}, Description: "Google Slides AI image generation (Chrome + Playwright)", Timeout: 15 * time.Minute, DefaultMaxRetries: 2, RequiredCapabilities: []string{"image_gen_chrome"}})
}

// registerMediaPolicy makes a static registration failure loud at composition
// time. Compose() has no error return, so a duplicate or invalid policy must
// not silently disappear from the registry.
func registerMediaPolicy(r *Registry, policy JobPolicy) {
	if err := r.Register(policy); err != nil {
		panic("jobs: register media policy: " + err.Error())
	}
}
