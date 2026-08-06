package jobs

import "time"

// registerMediaEntries registers all remaining media job types
// (video, catalog, content, system, AI image) into the canonical
// registry. Called by Compose() after the base registry is created.
//
// LONG-FILES-SPLIT-2026-07-06 Band A #7: extracted from registry.go's
// Compose() function per AGENTS.md Pattern 5.
func registerMediaEntries(r *Registry) {
	// ── Video ──
	// PR-COMPLETE-WORKER-BROAD-FIX Path D (July 2026): ProducesArtifacts REMOVED.
	// TypeVideoGenerate, TypeRenderVideo, TypeYouTubeUpload are orphaned
	// registry entries — no production handler is statically registered.
	r.Register(JobPolicy{Type: TypeVideoGenerate, Description: "Video generation", Timeout: 60 * time.Minute, DefaultMaxRetries: 1})
	r.Register(JobPolicy{Type: TypeRenderVideo, Description: "Video rendering", Timeout: 60 * time.Minute, DefaultMaxRetries: 1})

	// ── YouTube ──
	r.Register(JobPolicy{Type: TypeYouTubeUpload, Description: "YouTube upload", Timeout: 30 * time.Minute, DefaultMaxRetries: 2})

	// ── Catalog / sync ──
	r.Register(JobPolicy{Type: TypeCatalogSync, Description: "Catalog synchronization", Timeout: 2 * time.Minute, DefaultMaxRetries: 2})
	r.Register(JobPolicy{Type: TypeArtlistRun, Description: "Artlist run", Timeout: 30 * time.Minute, DefaultMaxRetries: 1})
	r.Register(JobPolicy{Type: TypeArtlistCacheRefresh, Description: "Refresh a stale Artlist live-search cache entry", Timeout: 2 * time.Minute, DefaultMaxRetries: 3})
	r.Register(JobPolicy{Type: TypeDriveFolderSync, Description: "Drive folder sync", Timeout: 30 * time.Minute, DefaultMaxRetries: 1})

	// ── Content processing ──
	r.Register(JobPolicy{Type: TypeBooksProcess, Description: "Book processing", Timeout: 30 * time.Minute, DefaultMaxRetries: 2, ProducesArtifacts: true})
	r.Register(JobPolicy{Type: TypeLessonsProcess, Description: "Lesson processing", Timeout: 30 * time.Minute, DefaultMaxRetries: 2, ProducesArtifacts: true})

	// ── System ──
	r.Register(JobPolicy{Type: TypeSystemCleanup, Description: "System cleanup", Timeout: 2 * time.Minute, DefaultMaxRetries: 1})

	// ── AI Image Generation ──
	// FASE 2 (June 2026): ChromeImageProvider → Playwright → slides.new → Nano Banana Pro.
	r.Register(JobPolicy{Type: TypeImageGenerateGoogle, Description: "Google Slides AI image generation (Chrome + Playwright)", Timeout: 15 * time.Minute, DefaultMaxRetries: 2, RequiredCapabilities: []string{"image_gen_chrome"}, ProducesArtifacts: true})
}
