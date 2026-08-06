package media

// Canonical media job type constants.
// Per godlike/02 capability-specific constants live in their owning domain package.
const (
	// TypeBulkUploadYouTubeClips is the canonical job type for
	// bulk uploading a list of YouTube clips to the operator's
	// Google Drive + upserting the corresponding media_assets rows.
	//
	// Wire-string: "media.bulk_upload_youtube_clips". The owner-side
	// identifier (job.TypeBulkUploadYouTubeClips) is a typed alias to this
	// canonical; renaming the identifier does NOT change the wire
	// value so in-flight jobs / orchestration records continue to
	// dispatch post cutover.
	TypeBulkUploadYouTubeClips = "media.bulk_upload_youtube_clips"

	// TypeClipRegister is the canonical job type for the async
	// clip-registration pipeline (the media.clip family). Each
	// clip from the batch-register endpoint becomes an
	// independent job; yt-dlp + cut + Drive upload + DB write
	// happen off the request thread. Canonical JobDefinition
	// literal lives at internal/domain/job.CanonicalClipRegister.
	//
	// Wire-string: "media.clip". The owner-side identifier
	// (domain/job.TypeClipRegister) is a typed alias to this
	// canonical; renaming the identifier does NOT change the
	// wire value so in-flight jobs / orchestration records
	// continue to dispatch post cutover.
	TypeClipRegister = "media.clip"

	// ── Commits 9.1 (PR-KERNEL-JOB-POPULATE follow-up, July 2026) ───
	// The following constants are required by the back-compat
	// alias layer in internal/domain/job/job.go (re-added by
	// PipelineGen Bot during the Commit 9 type-rename race).
	// Each constant's wire-string is the canonical package-scoped
	// discriminator used by the C3 dispatcher routing table; the
	// constants were never re-introduced after the bot swept them
	// out, so the aliases in domain/job failed to resolve.
	// PR-KERNEL-JOB-POPULATE step 1 (commit 9.1) restores them.

	// TypeExtract is the canonical job type for the media.extract
	// pipeline (yt-dlp + audio/video metadata extraction).
	TypeExtract = "media.extract"

	// TypeStock is the canonical job type for the stock-pipeline
	// orchestration (Artlist + Drive upload + indexing).
	TypeStock = "media.stock"

	// TypeArtlistRun is the canonical job type for an Artlist
	// stock-pipeline run (download + cut + Drive upload).
	TypeArtlistRun = "media.artlist"

	// TypeArtlistCacheRefresh is the canonical job type for refreshing
	// a stale Artlist live-search cache entry. The HTTP search path only
	// enqueues this durable job; the worker performs the provider request
	// and cache write.
	TypeArtlistCacheRefresh = "media.artlist_cache_refresh"

	// TypeGenerate is the canonical job type for the media
	// image-generation entry point. Wire-string recovered
	// from git history (pre-bloomed bot-sweep deletion): the
	// historical value is "media.generate_missing_asset" (NOT
	// the inferred "media.generate"). The wire string is the
	// canonical SQLite jobs.type discriminator + the C3
	// dispatcher routing key; a mismatch would silently drop
	// these jobs at dispatch.
	TypeGenerate = "media.generate_missing_asset"

	// TypeReindex is the canonical job type for the Qdrant
	// reindex pipeline (assets → Qdrant upsert).
	TypeReindex = "media.reindex"

	// TypeEnrich is the canonical job type for the media-enrich
	// pipeline (asset metadata enrichment via RLM).
	TypeEnrich = "media.enrich"

	// TypeCurate is the canonical job type for the media-curation
	// pipeline (operator-curated clip selection).
	TypeCurate = "media.curate"

	// TypeStockRLMEnrich is the canonical job type for the
	// stock-pipeline RLM enrichment step (description + tags).
	TypeStockRLMEnrich = "media.stock_rlm_enrich"
)
