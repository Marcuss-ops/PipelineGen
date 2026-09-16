// Package media — voiceover_media_reader.go: the two media_assets reads the
// voiceover capability needs, on the media SSOT.
//
// WHY THIS EXISTS. internal/app/wiring/voiceover/adapters_voiceover_repo.go
// reached media_assets through the OPERATIONAL handle it also uses for the
// `voiceovers` table, in two places:
//
//  1. findVoiceoverMediaAsset — the cross-run cache lookup. After migration 232
//     dropped the location columns from `voiceovers`, the canonical Drive and
//     local-path facts live in media_assets, so this read decides whether a
//     fingerprint match yields a usable cache HIT. Reading the operational
//     mirror meant a committed voiceover's location could look absent, turning a
//     real cache hit into a full regeneration.
//  2. CountByDriveFileIDTx — the PR-VO-B3 post-upload dedupe gate. It read
//     media_assets INSIDE the finalizer's SQLite transaction, which is the one
//     genuinely subtle case; see the tx note on CountByDriveFileID below.
//
// Both now answer from the media SSOT. PostgreSQL ONLY, no fallback.
package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// VoiceoverMediaAssetLocation is the subset of media_assets columns needed to
// hydrate a voiceover cache hit. It mirrors the retired
// voiceoverMediaAssetLocation struct field-for-field.
type VoiceoverMediaAssetLocation struct {
	DriveFileID  string
	DriveLink    string
	DownloadLink string
	LocalPath    string
	Name         string
}

// MediaVoiceoverMediaReader answers the voiceover capability's media_assets
// reads from the PostgreSQL media SSOT.
type MediaVoiceoverMediaReader struct {
	db *sql.DB
}

// NewMediaVoiceoverMediaReader returns a reader bound to the PostgreSQL media
// database. The handle is required: a nil database is a programming error, not
// a degrade path.
func NewMediaVoiceoverMediaReader(db *sql.DB) *MediaVoiceoverMediaReader {
	if db == nil {
		panic("media.NewMediaVoiceoverMediaReader: db is required")
	}
	return &MediaVoiceoverMediaReader{db: db}
}

// DB exposes the underlying handle so callers can assert the reader lives on
// the media SSOT.
func (r *MediaVoiceoverMediaReader) DB() *sql.DB {
	if r == nil {
		return nil
	}
	return r.db
}

// MediaAssetLocation returns the location columns of one media asset, with
// found=false when the row is absent.
//
// The retired statement mapped sql.ErrNoRows to (nil, nil) — "no row", which the
// cache lookup treats as a miss — so absence is reported as (zero, false, nil)
// and an actual read failure stays an error. Collapsing those two into one
// outcome would turn an unreadable catalog into a cache miss, i.e. a silent
// full regeneration instead of a retry.
func (r *MediaVoiceoverMediaReader) MediaAssetLocation(ctx context.Context, assetID string) (VoiceoverMediaAssetLocation, bool, error) {
	if r == nil || r.db == nil {
		return VoiceoverMediaAssetLocation{}, false, fmt.Errorf("media voiceover media reader: media SSOT handle is not configured")
	}
	var loc VoiceoverMediaAssetLocation
	err := r.db.QueryRowContext(ctx, `
		SELECT COALESCE(drive_file_id, ''), COALESCE(drive_link, ''),
		       COALESCE(download_link, ''), COALESCE(local_path, ''),
		       COALESCE(name, '')
		FROM media_assets WHERE id = $1
	`, assetID).Scan(&loc.DriveFileID, &loc.DriveLink, &loc.DownloadLink, &loc.LocalPath, &loc.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return VoiceoverMediaAssetLocation{}, false, nil
	}
	if err != nil {
		return VoiceoverMediaAssetLocation{}, false, fmt.Errorf("media voiceover media reader: load location for %q: %w", assetID, err)
	}
	return loc, true, nil
}

// CountByDriveFileID runs the PR-VO-B3 dedupe lookup: the id of one OTHER asset
// holding driveFileID plus the total count of such assets.
//
// WHY IT IS NOT TRANSACTION-BOUND ANY MORE. The retired form took the
// finalizer's *sql.Tx and was therefore evaluated inside a transaction over the
// OPERATIONAL database. That transaction gave snapshot isolation over the
// SQLite copy of media_assets — a table that is not authoritative — and it could
// never have covered the authoritative copy at all, because media_assets lives
// on a different engine and cannot participate in a SQLite transaction. So the
// in-tx form was not buying correctness: it was reading the wrong database with
// stronger isolation. The gate's verdict is also explicitly ADVISORY — its own
// contract projects the count into Continue/Reuse/Conflict and the caller
// handles a lost verdict by falling through — and no media_assets WRITE happens
// in that transaction (the projection step is a fail-closed stub on the retired
// legacy branch), so there is no write for the read to be atomic WITH.
//
// Consequently the read now answers from the SSOT outside any transaction, which
// is strictly better information with a marginally weaker isolation guarantee
// that was never enforceable in the first place.
//
// Semantics preserved from the retired statements: (a) a row whose drive_file_id
// matches but whose id equals currentID is excluded; (b) absence of ANY match
// yields ("", 0, nil) rather than an error; (c) a COUNT failure after a match was
// found reports count=1 — the retired graceful-degrade contract, which reports
// the match without inventing ambiguity that would trigger DedupeConflict.
func (r *MediaVoiceoverMediaReader) CountByDriveFileID(ctx context.Context, driveFileID, currentID string) (string, int, error) {
	if r == nil || r.db == nil {
		return "", 0, fmt.Errorf("media voiceover media reader: media SSOT handle is not configured")
	}
	if driveFileID == "" {
		return "", 0, nil
	}
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	var matchedID string
	err := r.db.QueryRowContext(ctx, `
		SELECT id FROM media_assets
		WHERE drive_file_id = $1 AND id <> $2
		LIMIT 1
	`, driveFileID, currentID).Scan(&matchedID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, nil
	}
	if err != nil {
		return "", 0, fmt.Errorf("media voiceover media reader: dedupe lookup: %w", err)
	}

	var count int
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_assets WHERE drive_file_id = $1 AND id <> $2`,
		driveFileID, currentID,
	).Scan(&count); err != nil {
		// Count failed but the row WAS found: report count=1 so the gate still
		// surfaces the match without inflating it into an ambiguity.
		return matchedID, 1, nil
	}
	return matchedID, count, nil
}
