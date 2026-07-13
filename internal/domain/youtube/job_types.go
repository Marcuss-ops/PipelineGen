package youtube

// Canonical YouTube job type constants.
// Per godlike/02 capability-specific constants live in their owning domain package.
const (
	// TypeUpload is the canonical job type for YouTube upload.
	TypeUpload = "youtube.upload"

	// TypeClipExtract is the canonical job type for YouTube clip extraction.
	TypeClipExtract = "youtube_clip.extract"

	// TypeClipExtractImportant is the canonical job type for LLM-driven
	// per-segment YouTube clip extraction.
	TypeClipExtractImportant = "youtube.clip_extract_important"

	// TypeRebuildSearchText is the canonical job type for rebuilding
	// YouTube search text.
	TypeRebuildSearchText = "youtube.rebuild_search_text"
)
