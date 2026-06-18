package vectorstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ClipIndexerAdapter implements clipindexer.VectorStoreIndexer.
// It reads the clip's data from SQLite and upserts it into the vector store.
type ClipIndexerAdapter struct {
	db    *sql.DB
	store *Service
	log   *zap.Logger
	cfg   Config
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
	if a.store == nil {
		a.log.Debug("vector store service is nil, skipping upsert",
			zap.String("clip_id", clipID))
		return nil
	}
	if !a.store.Enabled() {
		a.log.Debug("vector store disabled, skipping upsert",
			zap.String("clip_id", clipID))
		return nil
	}

	asset, err := a.readClipFromDB(ctx, clipID)
	if err != nil {
		return fmt.Errorf("read clip %s: %w", clipID, err)
	}

	if !a.hasEmbeddings(asset) {
		a.log.Warn("clip has no embeddings to index in vector store",
			zap.String("clip_id", clipID),
			zap.Int("text_emb_len", len(asset.TextEmbedding)),
			zap.Int("visual_emb_len", len(asset.VisualEmbedding)),
			zap.Int("audio_emb_len", len(asset.AudioEmbedding)))
		return nil
	}

	return a.store.UpsertAsset(ctx, *asset)
}

// UpsertFromClips reads multiple clips from SQLite and pushes them in a single batch upsert.
// Skips clips without embeddings. Much faster than N individual UpsertFromClip calls
// for bulk operations like IndexRunItems or backfill/import scripts.
func (a *ClipIndexerAdapter) UpsertFromClips(ctx context.Context, clipIDs []string) error {
	if a.store == nil || !a.store.Enabled() || len(clipIDs) == 0 {
		return nil
	}

	assets := make([]VectorAsset, 0, len(clipIDs))
	for _, clipID := range clipIDs {
		asset, err := a.readClipFromDB(ctx, clipID)
		if err != nil {
			a.log.Warn("skipping clip in batch upsert",
				zap.String("clip_id", clipID),
				zap.Error(err))
			continue
		}
		if a.hasEmbeddings(asset) {
			assets = append(assets, *asset)
		} else {
			a.log.Debug("skip clip with no embeddings", zap.String("clip_id", clipID))
		}
	}

	if len(assets) == 0 {
		return nil
	}

	a.log.Info("batch upserting clips to vector store",
		zap.Int("total_requested", len(clipIDs)),
		zap.Int("with_embeddings", len(assets)))

	return a.store.UpsertAssets(ctx, assets)
}

// hasEmbeddings returns true if the asset has at least one embedding type.
func (a *ClipIndexerAdapter) hasEmbeddings(asset *VectorAsset) bool {
	return len(asset.TextEmbedding) > 0 || len(asset.TranscriptEmbedding) > 0 ||
		len(asset.VisualEmbedding) > 0 || len(asset.AudioEmbedding) > 0
}

// readClipFromDB fetches clip metadata + embeddings from the media database.
// Canonical columns (drive_link, local_path, category, media_type,
// search_text) are read directly per migration 059. Source-specific
// provider metadata stays in metadata_json (audio_embedding_json, style,
// language, youtube_video_id, youtube_url, start, end, duration_ms) —
// those are clipindexer source annotations, not canonical columns.
func (a *ClipIndexerAdapter) readClipFromDB(ctx context.Context, clipID string) (*VectorAsset, error) {
	query := `
		SELECT id, name, source, tags,
			COALESCE(embedding_json, '[]') as embedding_json,
			COALESCE(visual_embedding, '[]') as visual_embedding,
			COALESCE(transcript_embedding, '[]') as transcript_embedding,
			COALESCE(json_extract(metadata_json, '$.audio_embedding_json'), '') as audio_embedding_json,
			COALESCE(drive_link, '') as drive_link,
			COALESCE(local_path, '') as local_path,
			COALESCE(category, '') as category,
			COALESCE(json_extract(metadata_json, '$.style'), '') as style,
			COALESCE(media_type, '') as media_type,
			COALESCE(duration_ms, 0) as duration_ms,
			COALESCE(json_extract(metadata_json, '$.language'), '') as language,
			COALESCE(search_text, '') as search_text,
			COALESCE(json_extract(metadata_json, '$.youtube_video_id'), '') as youtube_video_id,
			COALESCE(json_extract(metadata_json, '$.youtube_url'), '') as youtube_url,
			COALESCE(json_extract(metadata_json, '$.start'), '') as start_time,
			COALESCE(json_extract(metadata_json, '$.end'), '') as end_time
		FROM media_assets WHERE id = ?
	`

	var id, name, source string
	var tagsStr, embeddingJSON, visualEmbeddingJSON, transcriptEmbeddingJSON, audioEmbeddingJSON string
	var driveLink, localPath, category, style, mediaType string
	var durationMs int
	var language, searchText, youtubeVideoID, youtubeURL, startTime, endTime string

	err := a.db.QueryRowContext(ctx, query, clipID).Scan(
		&id, &name, &source, &tagsStr,
		&embeddingJSON, &visualEmbeddingJSON, &transcriptEmbeddingJSON, &audioEmbeddingJSON,
		&driveLink, &localPath, &category, &style, &mediaType, &durationMs,
		&language, &searchText, &youtubeVideoID, &youtubeURL, &startTime, &endTime,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("clip not found: %s", clipID)
		}
		return nil, fmt.Errorf("query clip: %w", err)
	}

	asset := &VectorAsset{
		AssetID:        id,
		Source:         source,
		Name:           name,
		LocalPath:      localPath,
		DriveLink:      driveLink,
		Category:       category,
		Style:          style,
		MediaType:      mediaType,
		DurationMs:     durationMs,
		Language:       language,
		SearchText:     searchText,
		YouTubeVideoID: youtubeVideoID,
		YouTubeURL:     youtubeURL,
		StartTime:      startTime,
		EndTime:        endTime,
	}

	// Parse text embedding
	if embeddingJSON != "" && embeddingJSON != "[]" {
		var emb []float64
		if err := json.Unmarshal([]byte(embeddingJSON), &emb); err == nil && len(emb) > 0 {
			asset.TextEmbedding = textutil.Float64To32(emb)
		}
	}

	// Parse visual embedding
	if visualEmbeddingJSON != "" && visualEmbeddingJSON != "[]" {
		var visualEmb []float64
		if err := json.Unmarshal([]byte(visualEmbeddingJSON), &visualEmb); err == nil && len(visualEmb) > 0 {
			asset.VisualEmbedding = textutil.Float64To32(visualEmb)
		}
	}

	// Parse transcript embedding
	if transcriptEmbeddingJSON != "" && transcriptEmbeddingJSON != "[]" {
		var transcriptEmb []float64
		if err := json.Unmarshal([]byte(transcriptEmbeddingJSON), &transcriptEmb); err == nil && len(transcriptEmb) > 0 {
			asset.TranscriptEmbedding = textutil.Float64To32(transcriptEmb)
		}
	}

	// Parse audio embedding
	if audioEmbeddingJSON != "" {
		var audioEmb []float64
		if err := json.Unmarshal([]byte(audioEmbeddingJSON), &audioEmb); err == nil && len(audioEmb) > 0 {
			asset.AudioEmbedding = textutil.Float64To32(audioEmb)
		}
	}

	// Parse tags
	if tagsStr != "" && tagsStr != "[]" {
		json.Unmarshal([]byte(tagsStr), &asset.Tags)
	}

	return asset, nil
}
