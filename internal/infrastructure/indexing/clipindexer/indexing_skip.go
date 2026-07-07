package clipindexer

import (
	"context"
	"database/sql"
	"strings"

	"go.uber.org/zap"
)

// isSkippableAssetName reports whether the asset name represents a
// bookkeeping artifact (e.g. the cumulative `metadata.json` sidecar that
// Drive ingest uploads next to each clip) that must NOT be indexed into
// the vector store. Without this guard, every re-ingestion of a clip folder
// reinserts ~1 metadata.json point into Qdrant, polluting semantic search.
//
// "ends with .json" catches most JSON artifacts uploaded as media
// (per-file captions, transcript exports, etc.) which are not real
// searchable assets. The timestamp-manifest `metadata.json` rows are
// explicitly excluded from this skip so they can be indexed with the
// rest of the workflow metadata.
func isSkippableAssetName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	if name == "metadata.json" {
		return false
	}
	return strings.HasSuffix(strings.ToLower(name), ".json")
}

// shouldSkipByName loads the asset's `name` column and decides whether to
// short-circuit indexing. Returns (false, "") on lookup errors so the caller
// falls through to the normal index path (better to attempt than to silently
// drop a real clip on a transient SQL blip). A clean sql.ErrNoRows is
// treated as a no-op skip so we do not seed a phantom asset row by accident.
func (s *Service) shouldSkipByName(ctx context.Context, clipID string) (bool, string) {
	var name string
	err := s.db.QueryRowContext(ctx, `SELECT name FROM media_assets WHERE id = ?`, clipID).Scan(&name)
	if err != nil {
		if err == sql.ErrNoRows {
			s.log.Debug("asset row not found, nothing to index", zap.String("clip_id", clipID))
			return true, ""
		}
		s.log.Warn("could not load asset name for skip-check, will proceed",
			zap.String("clip_id", clipID), zap.Error(err))
		return false, ""
	}
	if isSkippableAssetName(name) {
		return true, name
	}
	return false, name
}
