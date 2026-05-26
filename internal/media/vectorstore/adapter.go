package vectorstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"
)

// ClipIndexerAdapter implements clipindexer.VectorStoreIndexer.
// It reads the clip's data from SQLite and upserts it into the vector store.
type ClipIndexerAdapter struct {
	db           *sql.DB
	store        *Service
	log          *zap.Logger
	cfg          Config
}

// NewClipIndexerAdapter creates a new adapter that bridges clipindexer → vectorstore.
func NewClipIndexerAdapter(db *sql.DB, store *Service, cfg Config, log *zap.Logger) *ClipIndexerAdapter {
	return &ClipIndexerAdapter{
		db:    db,
		store: store,
		log:   log,
		cfg:   cfg,
	}
}

// UpsertFromClip reads the clip from SQLite and upserts it into the vector store.
func (a *ClipIndexerAdapter) UpsertFromClip(ctx context.Context, clipID string) error {
	if a.store == nil || !a.store.Enabled() {
		return nil
	}

	// Read clip data from DB
	asset, err := a.readClipFromDB(ctx, clipID)
	if err != nil {
		return fmt.Errorf("read clip %s: %w", clipID, err)
	}

	if len(asset.TextEmbedding) == 0 && len(asset.VisualEmbedding) == 0 {
		a.log.Debug("clip has no embeddings to index in vector store",
			zap.String("clip_id", clipID))
		return nil
	}

	return a.store.UpsertAsset(ctx, *asset)
}

// readClipFromDB fetches clip metadata + embeddings from the media database.
func (a *ClipIndexerAdapter) readClipFromDB(ctx context.Context, clipID string) (*VectorAsset, error) {
	query := `
		SELECT id, name, source, tags,
			COALESCE(embedding_json, '[]') as embedding_json,
			COALESCE(json_extract(metadata_json, '$.visual_embedding_json'), '') as visual_embedding_json,
			COALESCE(json_extract(metadata_json, '$.drive_link'), '') as drive_link,
			COALESCE(json_extract(metadata_json, '$.local_path'), '') as local_path,
			COALESCE(json_extract(metadata_json, '$.category'), '') as category,
			COALESCE(json_extract(metadata_json, '$.style'), '') as style,
			COALESCE(json_extract(metadata_json, '$.media_type'), '') as media_type,
			COALESCE(CAST(json_extract(metadata_json, '$.duration_ms') AS INTEGER), 0) as duration_ms
		FROM media_assets WHERE id = ?
	`

	var id, name, source string
	var tagsStr, embeddingJSON, visualEmbeddingJSON string
	var driveLink, localPath, category, style, mediaType string
	var durationMs int

	err := a.db.QueryRowContext(ctx, query, clipID).Scan(
		&id, &name, &source, &tagsStr,
		&embeddingJSON, &visualEmbeddingJSON,
		&driveLink, &localPath, &category, &style, &mediaType, &durationMs,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("clip not found: %s", clipID)
		}
		return nil, fmt.Errorf("query clip: %w", err)
	}

	asset := &VectorAsset{
		AssetID:    id,
		Source:     source,
		Name:       name,
		LocalPath:  localPath,
		DriveLink:  driveLink,
		Category:   category,
		Style:      style,
		MediaType:  mediaType,
		DurationMs: durationMs,
	}

	// Parse text embedding
	if embeddingJSON != "" && embeddingJSON != "[]" {
		var emb []float64
		if err := json.Unmarshal([]byte(embeddingJSON), &emb); err == nil && len(emb) > 0 {
			asset.TextEmbedding = float64To32(emb)
		}
	}

	// Parse visual embedding
	if visualEmbeddingJSON != "" {
		var visualEmb []float64
		if err := json.Unmarshal([]byte(visualEmbeddingJSON), &visualEmb); err == nil && len(visualEmb) > 0 {
			asset.VisualEmbedding = float64To32(visualEmb)
		}
	}

	// Parse tags
	if tagsStr != "" && tagsStr != "[]" {
		json.Unmarshal([]byte(tagsStr), &asset.Tags)
	}

	return asset, nil
}

// float64To32 converts a []float64 slice to []float32.
func float64To32(in []float64) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v)
	}
	return out
}
