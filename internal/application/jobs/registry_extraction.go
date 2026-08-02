package jobs

import (
	"time"

	youtubejob "github.com/Marcuss-ops/PipelineGen/internal/domain/youtube"
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
	r.Register(JobPolicy{Type: TypeMediaExtract, Description: "Media extraction", Timeout: 30 * time.Minute, DefaultMaxRetries: 2})

	// PR-YOUTUBE-EXTRACT-REGISTRY (July 2026): youtube.extract is the
	// canonical registry entry for the YouTube clip extraction path
	// wired by internal/app/c3ValidateRuntimeGraph. The worker path
	// persists its own media_assets row + outbox event in the caller-owned tx,
	// so the broker's legacy Complete is the canonical mark-SUCCEEDED seam.
	r.Register(JobPolicy{Type: youtubejob.TypeExtract, Description: "youtube clip extraction (URL -> media_assets row + outbox)", Timeout: 60 * time.Minute, DefaultMaxRetries: 2})
	// The canonical extractor commits each clip's media_assets row and
	// outbox event before returning; the broker only marks the job complete.
	r.Register(JobPolicy{Type: youtubejob.TypeStock, Description: "YouTube transcript-first stock clips", Timeout: 60 * time.Minute, DefaultMaxRetries: 2, Concurrency: 2})

	// PR-COMPLETE-WORKER-YT-FIX (July 2026): ProducesArtifacts REMOVED.
	r.Register(JobPolicy{Type: TypeYouTubeClipExtract, Description: "YouTube clip extraction (per-segment artifacts persisted inside the per-segment caller-owned tx via process_segment + ClipAtomicWriter; broker's legacy Complete is the canonical mark-SUCCEEDED seam)", Timeout: 60 * time.Minute, DefaultMaxRetries: 2})
	r.Register(JobPolicy{Type: TypeYouTubeRebuildST, Description: "Rebuild YouTube search text", Timeout: 10 * time.Minute, DefaultMaxRetries: 1})

	// ── Clip registration (async batch, PR-BATCH-REGISTER-ASYNC) ──
	r.Register(JobPolicy{Type: TypeClipRegister, Description: "Async clip registration (PR-BATCH-REGISTER-ASYNC: yt-dlp download + cut + Drive upload + DB write off the request thread)", Timeout: 30 * time.Minute, DefaultMaxRetries: 2})

	// ── PR-GEMMA-EXTRACT-IMPORTANT (July 2026): LLM-driven per-segment
	// extract-important pipeline. POST /api/clips/extract-important submits
	// a batch via the jobs broker; the handler fans out per segment in
	// parallel, each segment running DownloadSection → Drive UploadFile →
	// FileHash → ClipAtomicWriter.CommitClipAndIndexEvent in a per-clip
	// atomic tx. ProducesArtifacts=true per the canonical pattern:
	// artifacts (media_assets row + outbox event) are persisted INSIDE
	// the per-clip tx, the broker's legacy Complete marks SUCCEEDED.
	//
	// Timeout is generous (60min) to accommodate long YouTube videos with
	// many LLM-identified segments + per-clip yt-dlp download + Drive upload
	// + DB write. DefaultMaxRetries=2 mirrors the per-clip clip-register
	// envelope (transient network/drive failures retry once before going
	// to the broker's dead-letter path).
	r.Register(JobPolicy{Type: TypeYouTubeClipExtractImportant, Description: "YouTube extract-important clips (PR-GEMMA-EXTRACT-IMPORTANT: LLM-driven per-segment fan-out, each segment clip published via the canonical ClipAtomicWriter in a per-clip atomic tx)", Timeout: 60 * time.Minute, DefaultMaxRetries: 2})
}
