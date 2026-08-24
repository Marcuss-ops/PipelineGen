package render

// AssetState value constants. This file is the canonical SOLE owner of
// the 14 AssetState values.
//
// godlike/06 SSOT: do NOT declare new `StateAssetX AssetState = "..."`
// constants anywhere else. The archcheck scanners enforce this:
//   - percheck_asset_state_canonical_14 counts declarations in this file.
//   - percheck_asset_state_no_shadow_enum bans the same shape in other
//     production files.
const (
	// StateAssetDiscovered — initial sentinel. The asset has been
	// identified but no work has been requested yet. Migration 157's
	// column DEFAULT writes this on existing rows.
	StateAssetDiscovered AssetState = "DISCOVERED"

	// StateAssetDownloaded — the asset's bytes are local; ready for
	// normalization and hashing.
	StateAssetDownloaded AssetState = "DOWNLOADED"

	// StateAssetNormalized — the asset has been normalized to the
	// canonical codec/container shape.
	StateAssetNormalized AssetState = "NORMALIZED"

	// StateAssetHashed — content-hash computed; idempotency keys for
	// downstream stages are derived from this hash.
	StateAssetHashed AssetState = "HASHED"

	// StateAssetUploaded — the asset is on remote storage (Drive);
	// ready for catalog enrichment.
	StateAssetUploaded AssetState = "UPLOADED"

	// StateAssetTranscribed — original-language transcript is present.
	StateAssetTranscribed AssetState = "TRANSCRIBED"

	// StateAssetEnriched — original-language description and visual
	// summary catalogues are present.
	StateAssetEnriched AssetState = "ENRICHED"

	// StateAssetTranslated — every enabled language has at least one
	// current text track entry.
	StateAssetTranslated AssetState = "TRANSLATED"

	// StateAssetIndexPending — Qdrant upsert is enqueued but the
	// indexer worker has not picked it up yet.
	StateAssetIndexPending AssetState = "INDEX_PENDING"

	// StateAssetIndexed — Qdrant upsert confirmed; the asset is
	// search-ready in its primary language.
	StateAssetIndexed AssetState = "INDEXED"

	// StateAssetReady — single-language readiness gate passes.
	StateAssetReady AssetState = "READY"

	// StateAssetReadyMultilingual — full multilingual gate passes.
	// This is the canonical publishable state.
	StateAssetReadyMultilingual AssetState = "READY_MULTILINGUAL"

	// StateAssetFailedRetryable — semi-terminal failure. The error was
	// classified as transient or retryable. Operator-driven re-entry
	// into any pre-terminal state is allowed.
	StateAssetFailedRetryable AssetState = "FAILED_RETRYABLE"

	// StateAssetFailedPermanent — true terminal failure. No automatic
	// out-edge; the operator must recreate the asset to leave this
	// state.
	StateAssetFailedPermanent AssetState = "FAILED_PERMANENT"
)
