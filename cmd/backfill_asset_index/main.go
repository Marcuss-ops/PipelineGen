package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
	"velox/go-master/internal/service/assetindex"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/logger"
)

func main() {
	cfg := config.Get()
	if err := cfg.Validate(); err != nil {
		fmt.Printf("Invalid configuration: %v\n", err)
		os.Exit(1)
	}

	logger.Init(cfg.GetLogLevel(), cfg.GetLogFormat())
	log := logger.Get()
	defer log.Sync()

	dataDir := cfg.Storage.DataDir
	log.Info("Starting asset index backfill", zap.String("data_dir", dataDir))

	ctx := context.Background()

	// Open assets DB
	assetsDBPath := filepath.Join(dataDir, "assets.db.sqlite")
	assetsDB, err := sql.Open("sqlite3", assetsDBPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		log.Fatal("Failed to open assets DB", zap.Error(err))
	}
	defer assetsDB.Close()

	assetIndexRepo := assetindex.NewRepository(assetsDB)
	assetIndexService := assetindex.NewService(assetIndexRepo)

	// Backfill sources
	backfillClips(ctx, log, dataDir, assetIndexService, "artlist.db.sqlite", "artlist", "artlist")
	backfillClips(ctx, log, dataDir, assetIndexService, "stock.db.sqlite", "stock", "stock")
	backfillClips(ctx, log, dataDir, assetIndexService, "clips.db.sqlite", "clip", "clips")
	backfillImages(ctx, log, dataDir, assetIndexService)
	backfillVoiceovers(ctx, log, dataDir, assetIndexService)

	log.Info("Asset index backfill completed")
}

func backfillClips(ctx context.Context, log *zap.Logger, dataDir string, svc *assetindex.Service, dbName, assetType, source string) {
	dbPath := filepath.Join(dataDir, dbName)
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		log.Warn("DB not found, skipping", zap.String("db", dbName))
		return
	}

	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		log.Error("Failed to open DB", zap.String("db", dbName), zap.Error(err))
		return
	}
	defer db.Close()

	// Check if status column exists
	hasStatus := checkColumnExists(db, "clips", "status")

	// Build query based on available columns
	query := `SELECT id, name, folder_path, group_name, local_path, drive_link, download_link, file_hash`
	if hasStatus {
		query += `, status`
	}
	query += ` FROM clips`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		log.Error("Failed to query clips", zap.String("db", dbName), zap.Error(err))
		return
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id, name, folderPath, groupName, localPath, driveLink, downloadLink, fileHash string
		var status string
		var scanDest []interface{}

		scanDest = append(scanDest, &id, &name, &folderPath, &groupName, &localPath, &driveLink, &downloadLink, &fileHash)
		if hasStatus {
			scanDest = append(scanDest, &status)
		}

		if err := rows.Scan(scanDest...); err != nil {
			log.Error("Failed to scan clip row", zap.Error(err))
			continue
		}

		if status == "" {
			status = "ready"
		}

		metadata := fmt.Sprintf(`{"name": "%s"}`, strings.ReplaceAll(name, `"`, `\"`))

		rec := &assetindex.AssetRecord{
			AssetID:   fmt.Sprintf("%s_%s", source, id),
			AssetType: assetType,
			Source:    source,
			SourceID:  id,
			GroupName: groupName,
			Subfolder: folderPath,
			LocalPath: localPath,
			DriveLink: driveLink,
			FileHash:  fileHash,
			Status:    status,
			Metadata:  metadata,
		}

		if err := svc.Upsert(ctx, rec); err != nil {
			log.Error("Failed to upsert clip asset", zap.String("id", id), zap.Error(err))
			continue
		}
		count++
	}

	log.Info("Backfilled clips", zap.String("source", source), zap.Int("count", count))
}

func checkColumnExists(db *sql.DB, tableName, columnName string) bool {
	rows, err := db.QueryContext(context.Background(), fmt.Sprintf("PRAGMA table_info(%s)", tableName))
	if err != nil {
		return false
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, dtype string
		var notnull int
		var dfltValue interface{}
		var pk int
		if err := rows.Scan(&cid, &name, &dtype, &notnull, &dfltValue, &pk); err != nil {
			continue
		}
		if name == columnName {
			return true
		}
	}
	return false
}

func backfillImages(ctx context.Context, log *zap.Logger, dataDir string, svc *assetindex.Service) {
	dbPath := filepath.Join(dataDir, "images.db.sqlite")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		log.Warn("images.db.sqlite not found, skipping")
		return
	}

	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		log.Error("Failed to open images DB", zap.Error(err))
		return
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `SELECT i.id, i.source, i.local_path, i.drive_link, i.file_hash, i.status, i.width, i.height, s.name as subject_name FROM images i LEFT JOIN subjects s ON i.subject_id = s.id`)
	if err != nil {
		log.Error("Failed to query images", zap.Error(err))
		return
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id, source, localPath, driveLink, fileHash, status string
		var width, height int
		var subjectName sql.NullString
		if err := rows.Scan(&id, &source, &localPath, &driveLink, &fileHash, &status, &width, &height, &subjectName); err != nil {
			log.Error("Failed to scan image row", zap.Error(err))
			continue
		}

		if status == "" {
			status = "ready"
		}

		metadata := fmt.Sprintf(`{"width": %d, "height": %d, "subject": "%s"}`, width, height, subjectName.String)

		rec := &assetindex.AssetRecord{
			AssetID:   fmt.Sprintf("image_%s", id),
			AssetType: "image",
			Source:    "images",
			SourceID:  id,
			LocalPath: localPath,
			DriveLink: driveLink,
			FileHash:  fileHash,
			Status:    status,
			Metadata:  metadata,
		}

		if err := svc.Upsert(ctx, rec); err != nil {
			log.Error("Failed to upsert image asset", zap.String("id", id), zap.Error(err))
			continue
		}
		count++
	}

	log.Info("Backfilled images", zap.Int("count", count))
}

func backfillVoiceovers(ctx context.Context, log *zap.Logger, dataDir string, svc *assetindex.Service) {
	dbPath := filepath.Join(dataDir, "voiceover.db.sqlite")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		log.Warn("voiceover.db.sqlite not found, skipping")
		return
	}

	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		log.Error("Failed to open voiceover DB", zap.Error(err))
		return
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `SELECT id, request_id, local_path, drive_file_id, drive_link, download_link, file_hash, status, language, voice, duration_seconds, strategy FROM voiceovers`)
	if err != nil {
		log.Error("Failed to query voiceovers", zap.Error(err))
		return
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id, requestID, localPath, driveFileID, driveLink, downloadLink, fileHash, status, language, voice, strategy string
		var durationSeconds float64
		if err := rows.Scan(&id, &requestID, &localPath, &driveFileID, &driveLink, &downloadLink, &fileHash, &status, &language, &voice, &durationSeconds, &strategy); err != nil {
			log.Error("Failed to scan voiceover row", zap.Error(err))
			continue
		}

		if status == "" {
			status = "ready"
		}

		actualDriveLink := driveLink
		if actualDriveLink == "" && driveFileID != "" {
			actualDriveLink = fmt.Sprintf("https://drive.google.com/file/d/%s/view", driveFileID)
		}

		metadata := fmt.Sprintf(`{"language": "%s", "voice": "%s", "duration_seconds": %.2f, "strategy": "%s"}`, language, voice, durationSeconds, strategy)

		sourceID := id
		if requestID != "" {
			sourceID = requestID
		}

		rec := &assetindex.AssetRecord{
			AssetID:      fmt.Sprintf("voiceover_%s", id),
			AssetType:    "voiceover",
			Source:       "voiceover",
			SourceID:     sourceID,
			LocalPath:    localPath,
			DriveLink:    actualDriveLink,
			DownloadLink: downloadLink,
			FileHash:     fileHash,
			Status:       status,
			Metadata:     metadata,
		}

		if err := svc.Upsert(ctx, rec); err != nil {
			log.Error("Failed to upsert voiceover asset", zap.String("id", id), zap.Error(err))
			continue
		}
		count++
	}

	log.Info("Backfilled voiceovers", zap.Int("count", count))
}
