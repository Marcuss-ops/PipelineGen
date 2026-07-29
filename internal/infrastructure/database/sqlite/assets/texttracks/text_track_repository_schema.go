// Package assets — text_track_repository_schema.go
//
// SQLite concrete for the TextTrackRepository port. Persists and queries
// localized text tracks (transcript, description, summary, title,
// keywords) per media asset. Used by the YouTube writer path, the
// TextTrackResolver lookup-before-Whisper fast path, and the
// SearchTextBuilder for multilingual embedding construction.
//
// The concrete TextTrackRepositorySQLite lives across several files
// in this package (all share `package assets`), each owning a single
// concern:
//
//   - text_track_repository_schema.go   — struct + constructor + interface assertion (THIS file).
//   - text_track_repository_queries.go  — write paths. UpsertBatch (atomic
//     upsert through the SQLite partial
//     UNIQUE INDEX) and
//     InsertTranslationWithAuditPredecessor
//     (begin-tx / idempotency-SELECT /
//     flip-prior / INSERT / commit).
//   - text_track_repository_lookup.go   — read paths. Find / FindReady /
//     FindCurrentForTranslation /
//     ListByAsset / ListReadyLanguages
//     plus findCuesForTrackID.
//   - text_track_repository_mapping.go  — scan helpers. textTrackScanner
//     interface, scanTextTrack,
//     scanTextTrackRows.
//
// PR-CATALOG-MULTILINGUA step 2 (July 2026): the SELECT/INSERT
// projections now carry the new source_track_id (FK back to the
// parent source-language track for audit-trail navigation) +
// source_text_hash (persisted source-text SHA-256) columns added
// by migration 156. The column-to-domain mapping lives next to
// scanTextTrack in mapping.go.
package texttracks

import (
	"database/sql"
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
)

// TextTrackRepositorySQLite is the SQLite-backed implementation of
// asset.TextTrackRepository.
type TextTrackRepositorySQLite struct {
	db  *sql.DB
	log *zap.Logger
}

// NewTextTrackRepository builds a SQLite-backed text track repository.
func NewTextTrackRepository(db *sql.DB, log *zap.Logger) (*TextTrackRepositorySQLite, error) {
	if db == nil {
		return nil, errors.New("text_track_repository: sql.DB is nil")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &TextTrackRepositorySQLite{db: db, log: log}, nil
}

// Compile-time interface assertion: *TextTrackRepositorySQLite
// must satisfy asset.TextTrackRepository. If a method signature
// drifts, this line fails to compile and surfaces the regression
// before runtime.
var _ asset.TextTrackRepository = (*TextTrackRepositorySQLite)(nil)
