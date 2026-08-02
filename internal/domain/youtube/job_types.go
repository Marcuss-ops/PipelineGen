package youtube

// Canonical youtube job type constants.
// Per godlike/02 capability-specific constants live in their owning domain package.
const (
	// TypeExtract is the canonical job type for youtube clip extraction
	// (URL -> media_assets row + outbox).
	TypeExtract = "youtube.extract"

	// TypeStock is the transcript-first YouTube → stock clip workflow.
	TypeStock = "youtube.stock"

	// ── Commit 9.2 (PR-KERNEL-JOB-POPULATE follow-up, July 2026) ────
	// The following constants are required by the back-compat
	// alias layer in internal/domain/job/job.go (re-added by
	// PipelineGen Bot during the Commit 9 type-rename race).
	// PR-KERNEL-JOB-POPULATE step 1.6 (commit 9.2) restores them
	// so the domain/job aliases resolve.

	// TypeUpload is the canonical job type for YouTube upload
	// (clip → YouTube Studio publish).
	TypeUpload = "youtube.upload"

	// TypeClipExtract is the canonical job type for the
	// youtube_clip.extract pipeline (LBC segments → clip
	// registration). The historical wire string is
	// "youtube_clip.extract" (with the underscore separator
	// between youtube and clip — preserved for back-compat
	// with in-flight SQLite jobs.type rows).
	TypeClipExtract = "youtube_clip.extract"

	// TypeClipExtractImportant is the canonical job type for
	// the per-LLM-segment fan-out clip extractor
	// (PR-GEMMA-EXTRACT-IMPORTANT, July 2026). Mirrors
	// TypeClipExtract but batch-fans out per LLM-identified
	// segment instead of per video OR clip ID.
	TypeClipExtractImportant = "youtube_clip.extract_important"

	// TypeRebuildSearchText is the canonical job type for the
	// YouTube search-text rebuild pipeline (rebuild the
	// canonical Qdrant search-text column from raw captions).
	TypeRebuildSearchText = "youtube.rebuild_search_text"
)
