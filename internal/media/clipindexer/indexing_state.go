package clipindexer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	metrics "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
)

// setIndexState atomically updates index_state in metadata_json.
// Increments Prometheus counters for terminal states and StaleAssets
// gauges for stuck-failure transitions (failed / retrying).
func (s *Service) setIndexState(ctx context.Context, clipID, state, lastError string) {
	source := sourceFromClipID(clipID)

	now := time.Now().UTC().Format(time.RFC3339)
	if lastError != "" {
		_, err := s.db.ExecContext(ctx,
			`UPDATE media_assets SET metadata_json = json_set(json_set(json_set(COALESCE(metadata_json,'{}'), '$.index_state', ?), '$.last_index_error', ?), '$.index_state_updated_at', ?) WHERE id = ?`,
			state, lastError, now, clipID)
		if err != nil {
			s.log.Warn("failed to set index state", zap.String("clip_id", clipID), zap.Error(err))
		}
	} else {
		_, err := s.db.ExecContext(ctx,
			`UPDATE media_assets SET metadata_json = json_set(json_set(COALESCE(metadata_json,'{}'), '$.index_state', ?), '$.index_state_updated_at', ?) WHERE id = ?`,
			state, now, clipID)
		if err != nil {
			s.log.Warn("failed to set index state", zap.String("clip_id", clipID), zap.Error(err))
		}
	}

	switch state {
	case "indexed":
		metrics.MediaIndexSuccessTotal.WithLabelValues(source).Inc()
	case "failed":
		metrics.MediaIndexFailureTotal.WithLabelValues(source).Inc()
		metrics.StaleAssets.WithLabelValues(source, "failed").Inc()
	case "retrying":
		metrics.MediaIndexRetryTotal.WithLabelValues(source).Inc()
		metrics.StaleAssets.WithLabelValues(source, "retrying").Inc()
	}

	s.log.Debug("index state transition",
		zap.String("clip_id", clipID),
		zap.String("state", state))
}

// sourceFromClipID returns the canonical source label used by Prometheus
// counters, derived from the asset ID prefix convention.
func sourceFromClipID(clipID string) string {
	switch {
	case strings.HasPrefix(clipID, "yt_"):
		return "youtube"
	case strings.HasPrefix(clipID, "artlist_"):
		return "artlist"
	case strings.HasPrefix(clipID, "stock_"):
		return "stock"
	case strings.HasPrefix(clipID, "img_"):
		return "image"
	default:
		return "other"
	}
}

// setIndexedAt persists the indexed_at timestamp, content hash, and
// embedding model identity on the asset's metadata_json. Called once a
// clip has been fully embedded and upserted into vectorstore.
func (s *Service) setIndexedAt(ctx context.Context, clipID, contentHash string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		`UPDATE media_assets SET metadata_json = json_set(json_set(json_set(json_set(json_set(COALESCE(metadata_json,'{}'), '$.index_state', 'indexed'), '$.indexed_at', ?), '$.indexed_content_hash', ?), '$.embedding_model', ?), '$.embedding_model_version', ?) WHERE id = ?`,
		now, contentHash, embeddingModel, embeddingModelVersion, clipID)
	if err != nil {
		return fmt.Errorf("set indexed_at for %s: %w", clipID, err)
	}
	metrics.MediaIndexSuccessTotal.WithLabelValues(sourceFromClipID(clipID)).Inc()
	return nil
}
