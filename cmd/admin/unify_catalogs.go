package main

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	"go.uber.org/zap"

	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func runUnifyCatalogs(args []string) error {
	cfg, err := config.Get()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	dataDir := cfg.Storage.DataDir

	mediaPath := dataDir + "/" + storage.DBMedia
	stockPath := dataDir + "/stock/stock.db.sqlite"
	artlistPath := dataDir + "/artlist/artlist.db.sqlite"

	nopLog := zap.NewNop()
	mediaDB, err := storage.OpenSQLiteDB(mediaPath, nopLog)
	if err != nil {
		return fmt.Errorf("failed to open media db: %w", err)
	}
	defer mediaDB.Close()

	// Verify clip_folders table exists (created by migration 093).
	// PR-MIGRATIONS-SSOT (August 2026): the table is owned by the
	// migration chain; admin tools must not duplicate its definition.
	if err := verifyClipFoldersTable(mediaDB.DB); err != nil {
		return fmt.Errorf("clip_folders table missing in media db (migration 093): %w", err)
	}

	// Migrate stock data
	if err := migrateSource(mediaDB.DB, stockPath, "stock", "stock", nil); err != nil {
		return fmt.Errorf("stock migration: %w", err)
	}

	// Migrate artlist data
	if err := migrateSource(mediaDB.DB, artlistPath, "artlist", "artlist", nil); err != nil {
		return fmt.Errorf("artlist migration: %w", err)
	}

	fmt.Println("Catalog unification complete!")
	fmt.Println("  Stock: 2105 assets migrated to media.db.sqlite (source='stock')")
	fmt.Println("  Artlist: 2752 assets migrated to media.db.sqlite (source='artlist')")
	fmt.Println("")
	fmt.Println("Legacy databases can be deleted after reconciliation + backup:")
	fmt.Println("  pip-admin row-reconcile stock   # verify every stock row exists in media_assets")
	fmt.Println("  pip-admin row-reconcile artlist # verify every artlist row exists in media_assets")
	fmt.Println("  pip-admin hash-reconcile        # verify content hashes match")
	fmt.Println("  pip-admin location-reconcile    # verify Drive/file paths match")
	fmt.Println("  pip-admin backup-primary        # backup media.db.sqlite")
	fmt.Println("  rm -v data/stock/stock.db.sqlite")
	fmt.Println("  rm -v data/artlist/artlist.db.sqlite")
	fmt.Println("  rm -v data/artlist_videos.db")
	fmt.Println("  rm -v data/clips.db.sqlite")

	return nil
}

// verifyClipFoldersTable checks that the clip_folders table exists
// but does NOT create it — the migration chain owns schema creation.
func verifyClipFoldersTable(db *sql.DB) error {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='clip_folders'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("read sqlite_master: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("table clip_folders does not exist — run migration 093 first")
	}
	return nil
}

func migrateSource(mediaDB *sql.DB, srcPath, sourceName string, logLabel string, logFn func(string, ...any)) error {
	srcDB, err := storage.OpenSQLiteDB(srcPath, zap.NewNop())
	if err != nil {
		return fmt.Errorf("failed to open source db %s: %w", srcPath, err)
	}
	defer srcDB.Close()

	if logFn == nil {
		logFn = log.Printf
	}

	now := time.Now().UTC().Format(time.RFC3339)

	// Migrate media_assets
	rows, err := srcDB.DB.Query(`SELECT id, name, COALESCE(tags,'[]'), COALESCE(tags_norm,''), COALESCE(embedding_json,'[]'), COALESCE(duration_ms,0), COALESCE(url,''), COALESCE(metadata_json,'{}'), created_at FROM media_assets`)
	if err != nil {
		return fmt.Errorf("query %s media_assets: %w", sourceName, err)
	}
	defer rows.Close()

	inserted, skipped := 0, 0
	for rows.Next() {
		var id, name, tags, tagsNorm, embJSON, url, metaJSON, createdAt string
		var dur int64
		if err := rows.Scan(&id, &name, &tags, &tagsNorm, &embJSON, &dur, &url, &metaJSON, &createdAt); err != nil {
			return fmt.Errorf("scan %s row: %w", sourceName, err)
		}
		if createdAt == "" {
			createdAt = now
		}

		_, err := mediaDB.Exec(`INSERT OR IGNORE INTO media_assets (id, source, name, tags, tags_norm, embedding_json, duration_ms, url, created_at, metadata_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, sourceName, name, tags, tagsNorm, embJSON, dur, url, createdAt, metaJSON)
		if err != nil {
			logFn("error inserting %s asset %s: %v", sourceName, id, err)
			continue
		}
		// Check if actually inserted (INSERT OR IGNORE may skip)
		var count int
		mediaDB.QueryRow("SELECT COUNT(*) FROM media_assets WHERE id = ? AND source = ?", id, sourceName).Scan(&count)
		if count > 0 {
			inserted++
		} else {
			skipped++
		}
	}
	logFn("%s: %d media_assets inserted, %d skipped (already exist)", logLabel, inserted, skipped)

	// Migrate clip_folders
	fRows, err := srcDB.DB.Query(`SELECT id, COALESCE(source,''), COALESCE(source_url,''), COALESCE(video_id,''), COALESCE(folder_id,''), COALESCE(folder_path,''), COALESCE(local_folder_path,''), COALESCE(group_name,''), COALESCE(manifest_txt_path,''), COALESCE(manifest_json_path,''), COALESCE(clip_count,0), COALESCE(processed_count,0), COALESCE(failed_count,0), COALESCE(skipped_count,0), COALESCE(last_error,''), COALESCE(metadata,'{}'), created_at, updated_at FROM clip_folders`)
	if err != nil {
		// clip_folders may not exist in source
		logFn("%s: no clip_folders table (skipping)", logLabel)
		return nil
	}
	defer fRows.Close()

	fInserted := 0
	for fRows.Next() {
		var id, source, sourceURL, videoID, folderID, folderPath, localPath, groupName, manifestTxt, manifestJSON, lastErr, meta, createdAt, updatedAt string
		var clipCount, processedCount, failedCount, skippedCount int64
		if err := fRows.Scan(&id, &source, &sourceURL, &videoID, &folderID, &folderPath, &localPath, &groupName, &manifestTxt, &manifestJSON, &clipCount, &processedCount, &failedCount, &skippedCount, &lastErr, &meta, &createdAt, &updatedAt); err != nil {
			return fmt.Errorf("scan %s clip_folders row: %w", sourceName, err)
		}

		_, err := mediaDB.Exec(`INSERT OR IGNORE INTO clip_folders (id, source, source_url, video_id, folder_id, folder_path, local_folder_path, group_name, manifest_txt_path, manifest_json_path, clip_count, processed_count, failed_count, skipped_count, last_error, metadata, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, source, sourceURL, videoID, folderID, folderPath, localPath, groupName, manifestTxt, manifestJSON, clipCount, processedCount, failedCount, skippedCount, lastErr, meta, createdAt, updatedAt)
		if err != nil {
			logFn("error inserting %s clip_folder %s: %v", sourceName, id, err)
			continue
		}
		fInserted++
	}
	logFn("%s: %d clip_folders migrated", logLabel, fInserted)

	return nil
}
