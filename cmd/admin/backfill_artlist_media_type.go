package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"
	"github.com/Marcuss-ops/PipelineGen/internal/config"
	"github.com/Marcuss-ops/PipelineGen/internal/media/vectorstore"
	"github.com/Marcuss-ops/PipelineGen/internal/storage"
)

// runBackfillArtlistMediaType updates Artlist clips that have media_type != 'video'
// in metadata_json and optionally upserts them to Qdrant.
//
// Usage:
//
//	admin backfill-artlist-media-type              # dry-run: count only
//	admin backfill-artlist-media-type --apply      # apply: update DB
//	admin backfill-artlist-media-type --apply --qdrant  # apply + upsert to Qdrant
func runBackfillArtlistMediaType(args []string) error {
	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	apply := false
	toQdrant := false
	for _, a := range args {
		switch strings.TrimSpace(a) {
		case "--apply":
			apply = true
		case "--qdrant":
			toQdrant = true
		}
	}

	ctx := cmdContext()
	dataDir := cfg.Storage.DataDir
	log.Info("opening media database", zap.String("data_dir", dataDir))

	// Use the same DB path as the production code: storage.NewSQLiteDB(dataDir, storage.DBMedia, log)
	// DBMedia = "media/media.db.sqlite"
	sqliteDB, err := storage.NewSQLiteDB(dataDir, storage.DBMedia, log)
	if err != nil {
		return fmt.Errorf("failed to open media DB: %w", err)
	}
	defer sqliteDB.Close()
	db := sqliteDB.DB

	// Step 1: Find all Artlist clips with media_type != 'video' in metadata_json
	rows, err := db.QueryContext(ctx, `
		SELECT id, metadata_json
		FROM media_assets
		WHERE source = 'artlist'
		  AND (json_extract(metadata_json, '$.media_type') IS NULL
		       OR json_extract(metadata_json, '$.media_type') = ''
		       OR json_extract(metadata_json, '$.media_type') = '"artlist"'
		       OR json_extract(metadata_json, '$.media_type') IS NOT NULL
		          AND json_extract(metadata_json, '$.media_type') != 'video')
	`)
	if err != nil {
		return fmt.Errorf("failed to query artlist clips: %w", err)
	}
	defer rows.Close()

	type clipRecord struct {
		id           string
		metadataJSON string
	}

	var clips []clipRecord
	for rows.Next() {
		var id, metaJSON string
		if err := rows.Scan(&id, &metaJSON); err != nil {
			log.Warn("failed to scan row", zap.Error(err))
			continue
		}
		clips = append(clips, clipRecord{id: id, metadataJSON: metaJSON})
	}

	if len(clips) == 0 {
		log.Info("no Artlist clips need media_type backfill — all already have media_type='video'")
		return nil
	}

	log.Info("found Artlist clips that need media_type backfill",
		zap.Int("count", len(clips)))

	if !apply {
		log.Info("DRY-RUN: pass --apply to update, --apply --qdrant to also upsert to Qdrant")
		return nil
	}

	// Step 2: Update metadata_json to include media_type: "video"
	updated := 0
	for _, clip := range clips {
		var meta map[string]any
		if err := json.Unmarshal([]byte(clip.metadataJSON), &meta); err != nil {
			meta = make(map[string]any)
		}
		meta["media_type"] = "video"
		updatedJSON, err := json.Marshal(meta)
		if err != nil {
			log.Warn("failed to marshal metadata for clip", zap.String("id", clip.id), zap.Error(err))
			continue
		}

		if _, err := db.ExecContext(ctx, `UPDATE media_assets SET metadata_json = ?, updated_at = datetime('now') WHERE id = ?`,
			string(updatedJSON), clip.id); err != nil {
			log.Warn("failed to update clip", zap.String("id", clip.id), zap.Error(err))
			continue
		}
		updated++
	}

	log.Info("updated clips in DB",
		zap.Int("updated", updated),
		zap.Int("total_found", len(clips)))

	if !toQdrant {
		log.Info("pass --qdrant to also upsert to Qdrant")
		return nil
	}

	// Step 3: Upsert to Qdrant via vectorstore adapter
	log.Info("starting Qdrant upsert for updated clips")
	clipIDs := make([]string, 0, len(clips))
	for _, clip := range clips {
		clipIDs = append(clipIDs, clip.id)
	}

	if err := upsertArtlistClipsToQdrant(ctx, db, cfg, log, clipIDs); err != nil {
		return fmt.Errorf("Qdrant upsert failed: %w", err)
	}

	log.Info("backfill complete",
		zap.Int("total_updated", updated),
		zap.Int("qdrant_upserted", len(clipIDs)))

	return nil
}

// upsertArtlistClipsToQdrant reads the given clip IDs from DB and upserts them
// to Qdrant via the vectorstore adapter (same path as production indexer).
func upsertArtlistClipsToQdrant(ctx context.Context, db *sql.DB, cfg *config.Config, log *zap.Logger, clipIDs []string) error {
	// Build vectorstore service
	vsCfg := vectorstore.Config{
		URL:                  cfg.VectorSearch.URL,
		Collection:           cfg.VectorSearch.Collection,
		TextVectorName:       cfg.VectorSearch.TextVectorName,       // "text"
		VisualVectorName:     cfg.VectorSearch.VisualVectorName,     // "visual"
		AudioVectorName:      cfg.VectorSearch.AudioVectorName,      // "audio"
		TranscriptVectorName: cfg.VectorSearch.TranscriptVectorName, // "transcript"
		SparseVectorName:     cfg.VectorSearch.SparseVectorName,     // "bm25_text"
		TextDimensions:       cfg.VectorSearch.TextDimensions,       // 768
		VisualDimensions:     cfg.VectorSearch.VisualDimensions,     // 512
		AudioDimensions:      cfg.VectorSearch.AudioDimensions,      // 512
		TranscriptDimensions: cfg.VectorSearch.TranscriptDimensions, // 768
		MinInstantScore:      cfg.VectorSearch.MinInstantScore,
		TimeoutMs:            cfg.VectorSearch.TimeoutMs,
		BatchSize:            500,
	}

	qdrantClient := vectorstore.NewQdrantClient(vsCfg)
	vs := vectorstore.NewService(qdrantClient, vsCfg, log)

	adapter := vectorstore.NewClipIndexerAdapter(db, vs, vsCfg, log)
	if err := adapter.UpsertFromClips(ctx, clipIDs); err != nil {
		return fmt.Errorf("batch upsert to Qdrant: %w", err)
	}

	return nil
}
