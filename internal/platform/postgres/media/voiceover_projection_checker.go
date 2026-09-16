// Package media — voiceover_projection_checker.go: the narrow PostgreSQL read
// behind the voiceover post-commit verifier.
//
// WHY THIS EXISTS. VoiceoverPostCommitVerifierAdapter.Verify confirmed that the
// voiceover's media_assets projection exists by issuing
//
//	SELECT source FROM media_assets WHERE id = ? AND source = 'voiceover'
//
// on the OPERATIONAL SQLite handle, in the same adapter that also (correctly)
// reads the operational `voiceovers` table. media_assets is owned by PostgreSQL,
// so the projection half of the check graded a database that holds no committed
// media rows — the verifier would have reported a missing projection for every
// asset the canonical writer had just committed.
//
// The adapter now takes this checker alongside the operational handle, so each
// half of the check reads the engine that owns its table. That is the general
// shape for a "two tables, two engines" verification: split the reads rather
// than choosing one engine for both.
//
// The boolean return deliberately collapses "row absent" into (false, nil): the
// retired statement mapped sql.ErrNoRows to a missing-projection report, and the
// caller needs to distinguish "absent" (a divergence report, severity
// StateCompletedUnverified) from "unreadable" (an error report, same severity
// but a different diagnosis). Both inputs are preserved here.
//
// PostgreSQL ONLY. No SQLite branch, no fallback.
package media

import (
	"context"
	"database/sql"
	"fmt"
)

// MediaVoiceoverProjectionChecker answers whether the canonical voiceover
// projection of an asset exists on the media SSOT.
type MediaVoiceoverProjectionChecker struct {
	db *sql.DB
}

// NewMediaVoiceoverProjectionChecker returns a checker bound to the PostgreSQL
// media database. The handle is required: a nil database is a programming
// error, not a degrade path.
func NewMediaVoiceoverProjectionChecker(db *sql.DB) *MediaVoiceoverProjectionChecker {
	if db == nil {
		panic("media.NewMediaVoiceoverProjectionChecker: db is required")
	}
	return &MediaVoiceoverProjectionChecker{db: db}
}

// DB exposes the underlying handle so callers can assert the checker lives on
// the media SSOT.
func (c *MediaVoiceoverProjectionChecker) DB() *sql.DB {
	if c == nil {
		return nil
	}
	return c.db
}

// VoiceoverProjectionExists reports whether a media_assets row exists for the id
// with source='voiceover'.
//
// source is compared to the literal 'voiceover' exactly as the retired statement
// did. A NULL source cannot match, so an unclassified projection is reported as
// missing — the same verdict the retired statement produced, and the safe one for
// a verification surface.
func (c *MediaVoiceoverProjectionChecker) VoiceoverProjectionExists(ctx context.Context, assetID string) (bool, error) {
	if c == nil || c.db == nil {
		return false, fmt.Errorf("media voiceover projection checker: media SSOT handle is not configured")
	}
	var exists bool
	err := c.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM media_assets WHERE id = $1 AND source = 'voiceover')`,
		assetID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("media voiceover projection checker: check %q: %w", assetID, err)
	}
	return exists, nil
}
