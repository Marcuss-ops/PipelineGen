package clipindexer

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"go.uber.org/zap"
)

// isSkippableAssetName reports whether the asset name represents a
// bookkeeping artifact (e.g. the cumulative `metadata.json` sidecar that
// Drive ingest uploads next to each clip) that must NOT be indexed into
// the vector store. Without this guard, every re-ingestion of a clip folder
// reinserts ~1 metadata.json point into Qdrant, polluting semantic search.
//
// "ends with .json" catches any JSON artifact uploaded as media (per-file
// captions, transcript exports, etc.) which is not a real searchable asset.
func isSkippableAssetName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	if name == "metadata.json" {
		return true
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

// filterSkippableClipIDs removes any ID whose media_assets.name matches a
// metadata-only pattern, so bulk indexing paths do not waste an embedding
// request on them. Errors fall through to the original slice (safe default).
func (s *Service) filterSkippableClipIDs(ctx context.Context, clipIDs []string) []string {
	if len(clipIDs) == 0 {
		return clipIDs
	}
	placeholders := make([]string, len(clipIDs))
	args := make([]any, len(clipIDs))
	for i, id := range clipIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	query := fmt.Sprintf(
		// Mirror isSkippableAssetName() exactly (case-insensitive on both branches)
		// so a row like `Metadata.JSON` is caught here just as it is by the
		// single-row shouldSkipByName() path.
		"SELECT id FROM media_assets WHERE id IN (%s) AND (LOWER(name) = 'metadata.json' OR LOWER(name) LIKE '%%.json')",
		strings.Join(placeholders, ","),
	)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		s.log.Warn("could not pre-filter skippable clip IDs, proceeding with original batch",
			zap.Int("count", len(clipIDs)), zap.Error(err))
		return clipIDs
	}
	defer rows.Close()

	skippable := make(map[string]struct{}, len(clipIDs))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		skippable[id] = struct{}{}
	}

	if len(skippable) == 0 {
		return clipIDs
	}
	filtered := make([]string, 0, len(clipIDs)-len(skippable))
	for _, id := range clipIDs {
		if _, drop := skippable[id]; !drop {
			filtered = append(filtered, id)
		}
	}
	s.log.Info("filtered skippable JSON-metadata ids from batch",
		zap.Int("total", len(clipIDs)),
		zap.Int("skipped", len(skippable)),
		zap.Int("kept", len(filtered)))
	return filtered
}
