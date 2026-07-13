package media

// Canonical media job type constants.
// Per godlike/02 capability-specific constants live in their owning domain package.
const (
	// TypeExtract is the canonical job type for media extraction.
	TypeExtract = "media.extract"

	// TypeStock is the canonical job type for the stock media pipeline.
	TypeStock = "media.stock"

	// TypeArtlistRun is the canonical job type for an Artlist run.
	TypeArtlistRun = "media.artlist"

	// TypeGenerate is the canonical job type for generating missing media assets.
	TypeGenerate = "media.generate_missing_asset"

	// TypeReindex is the canonical job type for reindexing media assets.
	TypeReindex = "media.reindex"

	// TypeEnrich is the canonical job type for single-asset semantic enrichment.
	TypeEnrich = "media.enrich"

	// TypeBulkUploadYouTubeClips is the canonical job type for bulk uploading
	// YouTube clips.
	TypeBulkUploadYouTubeClips = "media.bulk_upload_youtube_clips"

	// TypeCurate is the canonical job type for media curation.
	TypeCurate = "media.curate"

	// TypeClipRegister is the canonical job type for async clip registration.
	TypeClipRegister = "media.clip"

	// TypeStockRLMEnrich is the canonical job type for post-publish RLM/LLM
	// enrichment pass.
	TypeStockRLMEnrich = "media.stock_rlm_enrich"
)
