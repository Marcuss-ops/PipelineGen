package asset

// text_track_repository.go defines the canonical port for persisting
// and querying TextTrack records. The concrete SQLite implementation
// lives in internal/infrastructure/database/sqlite/assets/.
//
// This port is consumed by:
//   - The YouTube writer path (atomic save of media_assets + text tracks + outbox)
//   - The TextTrackResolver (lookup-before-Whisper fast path)
//   - The SearchTextBuilder (fetch all tracks for configured index_languages)
//   - The source_version computation (text_hash inclusion)

import "context"

// TextTrackRepository is the canonical port for persisting and querying
// localized text tracks per media asset.
type TextTrackRepository interface {
	// UpsertBatch atomically inserts or updates a batch of text tracks.
	// Each track is upserted on the UNIQUE(asset_id, language_code, text_kind)
	// constraint. Existing rows have their text_content, text_hash,
	// source_type, status, and updated_at refreshed.
	UpsertBatch(ctx context.Context, tracks []TextTrack) error

	// Find returns a single text track for the given (asset, language, kind)
	// triple. Returns (nil, nil) when no row exists (not-found is not an error).
	Find(ctx context.Context, assetID string, languageCode string, kind TextTrackKind) (*TextTrack, error)

	// ListByAsset returns all text tracks for the given asset, ordered by
	// language_code, text_kind. Returns an empty slice (not nil) when no
	// tracks exist.
	ListByAsset(ctx context.Context, assetID string) ([]TextTrack, error)
}
