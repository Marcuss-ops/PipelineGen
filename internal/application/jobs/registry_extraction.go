package jobs

import "time"

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
	r.Register(JobPolicy{Type: TypeMediaExtract, Description: "Media extraction", Timeout: 30 * time.Minute, DefaultMaxRetries: 2})

	// PR-COMPLETE-WORKER-YT-FIX (July 2026): ProducesArtifacts REMOVED.
	r.Register(JobPolicy{Type: TypeYouTubeClipExtract, Description: "YouTube clip extraction (per-segment artifacts persisted inside the per-segment caller-owned tx via process_segment + ClipAtomicWriter; broker's legacy Complete is the canonical mark-SUCCEEDED seam)", Timeout: 60 * time.Minute, DefaultMaxRetries: 2})
	r.Register(JobPolicy{Type: TypeYouTubeRebuildST, Description: "Rebuild YouTube search text", Timeout: 10 * time.Minute, DefaultMaxRetries: 1})

	// ── Clip registration (async batch, PR-BATCH-REGISTER-ASYNC) ──
	r.Register(JobPolicy{Type: TypeClipRegister, Description: "Async clip registration (PR-BATCH-REGISTER-ASYNC: yt-dlp download + cut + Drive upload + DB write off the request thread)", Timeout: 30 * time.Minute, DefaultMaxRetries: 2})
}
