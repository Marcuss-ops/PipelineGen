// Package verification — SQLite implementation of the assets verification
// port (capabilities/assets/verification.VoiceoverProjectionReader).
//
// The capability owns the port; this adapter owns the concrete SQL and the
// *sql.DB handle. The handle is injected at construction (fail-closed on nil)
// so the capability never imports database/sql.
package verification

import (
	"context"
	"database/sql"
	"fmt"

	capverify "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/verification"
)

// SQLiteProjectionReader implements capverify.VoiceoverProjectionReader
// against the canonical media.db.sqlite handle.
type SQLiteProjectionReader struct {
	db *sql.DB
}

// Compile-time assertion: the adapter satisfies the capability port.
var _ capverify.VoiceoverProjectionReader = (*SQLiteProjectionReader)(nil)

// NewProjectionReader constructs the SQLite adapter. Fail-closed on a nil
// database handle so a wiring gap surfaces at construction, not at query time.
func NewProjectionReader(db *sql.DB) (*SQLiteProjectionReader, error) {
	if db == nil {
		return nil, fmt.Errorf("verification: nil *sql.DB (caller forgot to supply the canonical media.db.sqlite handle)")
	}
	return &SQLiteProjectionReader{db: db}, nil
}

// HasVoiceoverProjection verifies that a media_assets row exists with
// source='voiceover' and id=voiceoverID. An empty voiceoverID short-circuits
// to (false, nil) so callers can safely forward an empty id.
func (r *SQLiteProjectionReader) HasVoiceoverProjection(ctx context.Context, voiceoverID string) (bool, error) {
	if voiceoverID == "" {
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("voiceover.HasVoiceoverProjection: caller context cancelled: %w", err)
	}

	const query = `
		SELECT 1
		  FROM media_assets
		 WHERE id = ?
		   AND source = 'voiceover'
		 LIMIT 1
	`
	var hit int
	if scanErr := r.db.QueryRowContext(ctx, query, voiceoverID).Scan(&hit); scanErr != nil {
		if scanErr == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("voiceover.HasVoiceoverProjection: SELECT 1 FROM media_assets WHERE id=? AND source='voiceover' LIMIT 1: %w", scanErr)
	}
	return true, nil
}

// CountVoiceoverProjections returns the number of media_assets rows with
// source='voiceover'.
func (r *SQLiteProjectionReader) CountVoiceoverProjections(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("voiceover.CountVoiceoverProjections: caller context cancelled: %w", err)
	}

	const query = `
		SELECT COUNT(*)
		  FROM media_assets
		 WHERE source = 'voiceover'
	`
	var count int
	if err := r.db.QueryRowContext(ctx, query).Scan(&count); err != nil {
		return 0, fmt.Errorf("voiceover.CountVoiceoverProjections: SELECT COUNT(*) FROM media_assets WHERE source='voiceover': %w", err)
	}
	return count, nil
}
