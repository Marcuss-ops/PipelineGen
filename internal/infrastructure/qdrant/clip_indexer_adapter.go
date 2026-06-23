package qdrant

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"
)

// ClipIndexerAdapter implements clipindexer.VectorStoreIndexer by reading
// embedding data from the media_assets table and upserting into Qdrant
// via the canonical *Service.
type ClipIndexerAdapter struct {
	db        *sql.DB
	vectorSvc *Service
	log       *zap.Logger
}

// NewClipIndexerAdapter creates a ClipIndexerAdapter that satisfies
// clipindexer.VectorStoreIndexer. It reads embedding_json, visual_embedding,
// transcript_embedding, and search_text from media_assets for the given
// clip IDs and pushes the resulting VectorAsset into Qdrant.
func NewClipIndexerAdapter(db *sql.DB, vectorSvc *Service, _ Config, log *zap.Logger) *ClipIndexerAdapter {
	return &ClipIndexerAdapter{
		db:        db,
		vectorSvc: vectorSvc,
		log:       log,
	}
}

// UpsertFromClip reads a single clip's embeddings from the DB and upserts
// to Qdrant. Returns nil if the clip has no embeddings or doesn't exist.
func (a *ClipIndexerAdapter) UpsertFromClip(ctx context.Context, clipID string) error {
	asset, err := a.buildVectorAsset(ctx, clipID)
	if err != nil {
		return fmt.Errorf("clip_indexer_adapter: build vector asset for %s: %w", clipID, err)
	}
	if asset == nil {
		return nil
	}
	return a.vectorSvc.UpsertAsset(ctx, *asset)
}

// UpsertFromClips reads multiple clips' embeddings from the DB and batch-upserts
// to Qdrant. Skips clips with no embeddings.
func (a *ClipIndexerAdapter) UpsertFromClips(ctx context.Context, clipIDs []string) error {
	if len(clipIDs) == 0 {
		return nil
	}
	assets := make([]VectorAsset, 0, len(clipIDs))
	for _, id := range clipIDs {
		asset, err := a.buildVectorAsset(ctx, id)
		if err != nil {
			a.log.Warn("clip_indexer_adapter: skipping clip due to build error",
				zap.String("clip_id", id), zap.Error(err))
			continue
		}
		if asset != nil {
			assets = append(assets, *asset)
		}
	}
	if len(assets) == 0 {
		return nil
	}
	return a.vectorSvc.UpsertAssets(ctx, assets)
}

// buildVectorAsset reads a single row from media_assets and constructs a
// VectorAsset. Returns nil if the row has no embedding data.
func (a *ClipIndexerAdapter) buildVectorAsset(ctx context.Context, clipID string) (*VectorAsset, error) {
	var (
		name               string
		source             string
		category           string
		searchText         string
		driveLink          string
		localPath          string
		tagsJSON           string
		embeddingJSON      string
		visualEmbeddingStr string
		transcriptEmbStr   string
		durationMs         int
		language           string
		youtubeVideoID     string
		youtubeURL         string
		startTime          string
		endTime            string
	)

	err := a.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(name, ''),
			COALESCE(source, ''),
			COALESCE(category, ''),
			COALESCE(search_text, ''),
			COALESCE(drive_link, ''),
			COALESCE(local_path, ''),
			COALESCE(tags, '[]'),
			COALESCE(embedding_json, '[]'),
			COALESCE(visual_embedding, '[]'),
			COALESCE(transcript_embedding, '[]'),
			COALESCE(duration_ms, 0),
			COALESCE(language, ''),
			COALESCE(json_extract(metadata_json, '$.youtube_video_id'), ''),
			COALESCE(url, ''),
			COALESCE(json_extract(metadata_json, '$.start_time'), ''),
			COALESCE(json_extract(metadata_json, '$.end_time'), '')
		FROM media_assets
		WHERE id = ?
	`, clipID).Scan(
		&name, &source, &category, &searchText, &driveLink, &localPath,
		&tagsJSON, &embeddingJSON, &visualEmbeddingStr, &transcriptEmbStr,
		&durationMs, &language, &youtubeVideoID, &youtubeURL,
		&startTime, &endTime,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query media_assets for %s: %w", clipID, err)
	}

	textEmb := parseEmbeddingJSON(embeddingJSON)
	visualEmb := parseEmbeddingJSON(visualEmbeddingStr)
	transcriptEmb := parseEmbeddingJSON(transcriptEmbStr)

	if len(textEmb) == 0 && len(visualEmb) == 0 && len(transcriptEmb) == 0 {
		return nil, nil
	}

	var tags []string
	if tagsJSON != "" && tagsJSON != "[]" {
		_ = json.Unmarshal([]byte(tagsJSON), &tags)
	}

	return &VectorAsset{
		AssetID:            clipID,
		Name:               name,
		Source:             source,
		Category:           category,
		SearchText:         searchText,
		DriveLink:          driveLink,
		LocalPath:          localPath,
		Tags:               tags,
		TextEmbedding:      textEmb,
		VisualEmbedding:    visualEmb,
		TranscriptEmbedding: transcriptEmb,
		DurationMs:         durationMs,
		Language:           language,
		YouTubeVideoID:     youtubeVideoID,
		YouTubeURL:         youtubeURL,
		StartTime:          startTime,
		EndTime:            endTime,
		EmbeddingVersion:   CurrentEmbeddingVersion,
		SearchTextVersion:  CurrentSearchTextVersion,
	}, nil
}

// parseEmbeddingJSON parses a JSON array of floats (e.g. "[0.1, 0.2, ...]")
// into a []float32 slice. Returns nil on empty/parse-failure input.
func parseEmbeddingJSON(raw string) []float32 {
	if raw == "" || raw == "[]" {
		return nil
	}
	var f64 []float64
	if err := json.Unmarshal([]byte(raw), &f64); err != nil {
		return nil
	}
	f32 := make([]float32, len(f64))
	for i, v := range f64 {
		f32[i] = float32(v)
	}
	return f32
}
