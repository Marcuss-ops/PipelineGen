package main

import (
	"context"
	"crypto/md5"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
	"golang.org/x/oauth2/google"
	driveapi "google.golang.org/api/drive/v3"
	"velox/go-master/internal/service/assetindex"
	"velox/go-master/internal/repository/assettree"
	"velox/go-master/internal/storage"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/logger"
)

func runBackfillHash(args []string) error {
	fs := flag.NewFlagSet("backfill-hash", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbPath := fs.String("db", "", "Path to SQLite database (absolute)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		return fmt.Errorf("usage: admin backfill-hash --db <absolute-path-to-sqlite>")
	}
	if !filepath.IsAbs(*dbPath) {
		return fmt.Errorf("db path must be absolute, got: %s", *dbPath)
	}

	log, cleanup, err := productionLogger()
	if err != nil {
		return err
	}
	defer cleanup()
	slog := log.Sugar()

	sqliteDB, err := storage.OpenSQLiteDB(*dbPath, log)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer sqliteDB.Close()
	db := sqliteDB.DB

	rows, err := db.Query("SELECT id, drive_link FROM clips WHERE file_hash='' AND drive_link!=''")
	if err != nil {
		return fmt.Errorf("failed to query clips: %w", err)
	}
	defer rows.Close()

	updated := 0
	for rows.Next() {
		var id, driveLink string
		if err := rows.Scan(&id, &driveLink); err != nil {
			continue
		}

		fileID := extractFileID(driveLink)
		if fileID == "" {
			continue
		}

		hash, err := fetchAndHash(fileID)
		if err != nil {
			slog.Errorf("failed to fetch file %s: %v", id, err)
			continue
		}

		if _, err := db.Exec("UPDATE clips SET file_hash=? WHERE id=?", hash, id); err != nil {
			slog.Errorf("failed to update hash for %s: %v", id, err)
			continue
		}

		updated++
		if updated%10 == 0 {
			fmt.Printf("Updated %d clips\n", updated)
		}
	}

	fmt.Printf("Done. Updated %d clips with file_hash\n", updated)
	return nil
}

func runBackfillHashV2(args []string) error {
	fs := flag.NewFlagSet("backfill-hash-v2", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbPath := fs.String("db", "", "Path to SQLite database (absolute)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		return fmt.Errorf("usage: admin backfill-hash-v2 --db <absolute-path-to-sqlite>")
	}
	if !filepath.IsAbs(*dbPath) {
		return fmt.Errorf("db path must be absolute, got: %s", *dbPath)
	}

	log, cleanup, err := productionLogger()
	if err != nil {
		return err
	}
	defer cleanup()
	slog := log.Sugar()

	sqliteDB, err := storage.OpenSQLiteDB(*dbPath, log)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer sqliteDB.Close()
	db := sqliteDB.DB

	ctx := context.Background()
	client, err := google.DefaultClient(ctx, driveapi.DriveScope)
	if err != nil {
		return fmt.Errorf("failed to create drive client: %w", err)
	}

	driveService, err := driveapi.New(client)
	if err != nil {
		return fmt.Errorf("failed to create drive service: %w", err)
	}

	rows, err := db.Query("SELECT id, drive_link FROM clips WHERE file_hash='' AND drive_link!=''")
	if err != nil {
		return fmt.Errorf("failed to query clips: %w", err)
	}
	defer rows.Close()

	updated := 0
	for rows.Next() {
		var id, driveLink string
		if err := rows.Scan(&id, &driveLink); err != nil {
			continue
		}

		fileID := extractFileID(driveLink)
		if fileID == "" {
			continue
		}

		file, err := driveService.Files.Get(fileID).Fields("md5Checksum").Context(ctx).Do()
		if err != nil {
			slog.Errorf("failed to get checksum for %s: %v", id, err)
			continue
		}
		if file.Md5Checksum == "" {
			continue
		}

		if _, err := db.Exec("UPDATE clips SET file_hash=? WHERE id=?", file.Md5Checksum, id); err != nil {
			slog.Errorf("failed to update hash for %s: %v", id, err)
			continue
		}

		updated++
		if updated%10 == 0 {
			fmt.Printf("Updated %d clips\n", updated)
		}
	}

	fmt.Printf("Done. Updated %d clips with file_hash\n", updated)
	return nil
}

func runBackfillAssetIndex(args []string) error {
	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	dataDir := cfg.Storage.DataDir
	log.Info("Starting asset index backfill", zap.String("data_dir", dataDir))

	ctx := context.Background()

	assetsDBPath := filepath.Join(dataDir, "assets.db.sqlite")
	assetsSQLiteDB, err := storage.OpenSQLiteDB(assetsDBPath, log)
	if err != nil {
		return fmt.Errorf("failed to open assets DB: %w", err)
	}
	defer assetsSQLiteDB.Close()

	assetIndexRepo := assetindex.NewRepository(assetsSQLiteDB.DB)
	assetIndexService := assetindex.NewService(assetIndexRepo)

	backfillClips(ctx, log, dataDir, assetIndexService, "artlist.db.sqlite", "artlist", "artlist")
	backfillClips(ctx, log, dataDir, assetIndexService, "stock.db.sqlite", "stock", "stock")
	backfillClips(ctx, log, dataDir, assetIndexService, "clips.db.sqlite", "clip", "clips")
	backfillImages(ctx, log, dataDir, assetIndexService)
	backfillVoiceovers(ctx, log, dataDir, assetIndexService)

	log.Info("Asset index backfill completed")
	return nil
}

func runBackfillAssetTree(args []string) error {
	dataDir := "data"
	if len(args) > 0 {
		fs := flag.NewFlagSet("backfill-asset-tree", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		dir := fs.String("data-dir", "data", "Data directory")
		if err := fs.Parse(args); err != nil {
			return err
		}
		dataDir = *dir
	}

	logger.Init("info", "console")
	logSvc := logger.Get()
	defer logger.Sync()

	assetsDBPath := filepath.Join(dataDir, "assets.db.sqlite")
	sqliteDB, err := storage.OpenSQLiteDB(assetsDBPath, logSvc)
	if err != nil {
		return fmt.Errorf("failed to open assets db: %w", err)
	}
	defer sqliteDB.Close()

	db := sqliteDB.DB
	if _, err := db.Exec("DELETE FROM asset_tree_nodes"); err != nil {
		return fmt.Errorf("failed to clear asset_tree_nodes: %w", err)
	}
	fmt.Println("Cleared existing asset tree nodes")

	repo, err := assettree.NewRepository(db, nil)
	if err != nil {
		return fmt.Errorf("failed to create repo: %w", err)
	}

	clipDBs := []struct {
		file   string
		source string
	}{
		{file: "clips.db.sqlite", source: "youtube"},
		{file: "artlist.db.sqlite", source: "artlist"},
		{file: "stock.db.sqlite", source: "stock"},
	}

	ctx := context.Background()
	for _, d := range clipDBs {
		syncSourceWithNesting(ctx, dataDir, d.file, d.source, repo)
	}

	syncFromImagesDB(ctx, dataDir, repo)
	syncFromVoiceoverDB(ctx, dataDir, repo)
	return nil
}

func productionLogger() (*zap.Logger, func(), error) {
	log, err := zap.NewProduction()
	if err != nil {
		return nil, nil, err
	}
	return log, func() { _ = log.Sync() }, nil
}

func appLogger() (*config.Config, *zap.Logger, func(), error) {
	cfg := config.Get()
	if err := cfg.Validate(); err != nil {
		return nil, nil, nil, fmt.Errorf("invalid configuration: %w", err)
	}

	logger.Init(cfg.GetLogLevel(), cfg.GetLogFormat())
	log := logger.Get()
	return cfg, log, func() { _ = logger.Sync() }, nil
}

func extractFileID(link string) string {
	if idx := strings.Index(link, "/d/"); idx != -1 {
		start := idx + 3
		end := strings.Index(link[start:], "/")
		if end == -1 {
			return link[start:]
		}
		return link[start : start+end]
	}
	return ""
}

func fetchAndHash(fileID string) (string, error) {
	url := fmt.Sprintf("https://drive.google.com/uc?id=%s", fileID)
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	h := md5.New()
	if _, err := io.Copy(h, resp.Body); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func isFilePath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".mp4", ".mkv", ".mov", ".avi", ".mp3", ".wav", ".txt", ".json", ".jpg", ".png", ".jpeg":
		return true
	}
	return false
}

func backfillClips(ctx context.Context, log *zap.Logger, dataDir string, svc *assetindex.Service, dbName, assetType, source string) {
	dbPath := filepath.Join(dataDir, dbName)
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		log.Warn("DB not found, skipping", zap.String("db", dbName))
		return
	}

	sqliteDB, err := storage.OpenSQLiteDB(dbPath, log)
	if err != nil {
		log.Error("Failed to open DB", zap.String("db", dbName), zap.Error(err))
		return
	}
	defer sqliteDB.Close()
	db := sqliteDB.DB

	hasStatus := checkColumnExists(db, "clips", "status")

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

	sqliteDB, err := storage.OpenSQLiteDB(dbPath, log)
	if err != nil {
		log.Error("Failed to open images DB", zap.Error(err))
		return
	}
	defer sqliteDB.Close()
	db := sqliteDB.DB

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

	sqliteDB, err := storage.OpenSQLiteDB(dbPath, log)
	if err != nil {
		log.Error("Failed to open voiceover DB", zap.Error(err))
		return
	}
	defer sqliteDB.Close()
	db := sqliteDB.DB

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

func syncSourceWithNesting(ctx context.Context, dataDir, dbFile, source string, repo *assettree.Repository) {
	dbPath := filepath.Join(dataDir, dbFile)
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return
	}

	sqliteDB, err := storage.OpenSQLiteDB(dbPath, nil)
	if err != nil {
		return
	}
	db := sqliteDB.DB
	defer sqliteDB.Close()

	pathMap := make(map[string]string)
	folderMetadata := make(map[string]struct {
		DriveLink   string
		DriveFileID string
	})

	var hasFoldersTable bool
	err = db.QueryRow("SELECT 1 FROM sqlite_master WHERE type='table' AND name='clip_folders'").Scan(&hasFoldersTable)
	if err == nil {
		rows, _ := db.Query("SELECT folder_id, folder_path, COALESCE(drive_link, ''), COALESCE(folder_id, '') FROM clip_folders")
		if rows != nil {
			defer rows.Close()
			for rows.Next() {
				var fid, fpath, dlink, dfileid sql.NullString
				if err := rows.Scan(&fid, &fpath, &dlink, &dfileid); err == nil && fid.String != "" {
					p := strings.Trim(fpath.String, "/")
					if p != "" {
						pathMap[p] = fid.String
						folderMetadata[fid.String] = struct {
							DriveLink   string
							DriveFileID string
						}{
							DriveLink:   dlink.String,
							DriveFileID: dfileid.String,
						}
					}
				}
			}
		}
	}

	ensurePathSegments := func(fullPath string) string {
		fullPath = strings.Trim(fullPath, "/")
		if fullPath == "" {
			return ""
		}

		segments := strings.Split(fullPath, "/")
		currentPath := ""
		parentID := ""

		for _, seg := range segments {
			if currentPath == "" {
				currentPath = seg
			} else {
				currentPath = currentPath + "/" + seg
			}

			folderID, exists := pathMap[currentPath]
			if !exists {
				folderID = "generated_" + source + "_" + strings.ReplaceAll(currentPath, "/", "_")
				pathMap[currentPath] = folderID
			}

			meta := folderMetadata[folderID]

			node := &assettree.AssetNode{
				ID:          folderID,
				Source:      source,
				AssetID:     folderID,
				Name:        seg,
				Type:        "folder",
				ParentID:    parentID,
				Path:        currentPath,
				Depth:       strings.Count(currentPath, "/"),
				IsFolder:    true,
				DriveFileID: meta.DriveFileID,
				DriveLink:   meta.DriveLink,
				Metadata:    "{}",
			}
			repo.UpsertNode(ctx, node)
			parentID = folderID
		}
		return parentID
	}

	var hasClipsTable bool
	err = db.QueryRow("SELECT 1 FROM sqlite_master WHERE type='table' AND name='clips'").Scan(&hasClipsTable)
	if err == nil {
		rows, _ := db.Query("SELECT id, name, filename, folder_id, folder_path, drive_link, drive_file_id, status FROM clips")
		if rows != nil {
			defer rows.Close()
			count := 0
			for rows.Next() {
				var id, name, filename, folderID, folderPath, driveLink, driveFileID, status sql.NullString
				if err := rows.Scan(&id, &name, &filename, &folderID, &folderPath, &driveLink, &driveFileID, &status); err != nil {
					continue
				}

				fPath := folderPath.String
				if isFilePath(fPath) {
					fPath = filepath.Dir(fPath)
				}

				effectiveParentID := ensurePathSegments(fPath)

				node := &assettree.AssetNode{
					ID:          id.String,
					Source:      source,
					AssetID:     id.String,
					Name:        name.String,
					Type:        "video",
					ParentID:    effectiveParentID,
					Path:        fPath,
					Depth:       strings.Count(fPath, "/"),
					IsFolder:    false,
					DriveFileID: driveFileID.String,
					DriveLink:   driveLink.String,
					Metadata:    fmt.Sprintf("{\"status\":\"%s\",\"filename\":\"%s\"}", status.String, filename.String),
				}
				repo.UpsertNode(ctx, node)
				count++
			}
			fmt.Printf("Synced %d items from %s (source: %s)\n", count, dbFile, source)
		}
	}
}

func syncFromImagesDB(ctx context.Context, dataDir string, repo *assettree.Repository) {
	dbPath := filepath.Join(dataDir, "images.db.sqlite")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return
	}

	sqliteDB, err := storage.OpenSQLiteDB(dbPath, nil)
	if err != nil {
		return
	}
	db := sqliteDB.DB
	defer sqliteDB.Close()

	var tableName string
	err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='images'").Scan(&tableName)
	if err != nil {
		return
	}

	rows, err := db.Query("SELECT id, description, drive_link, drive_file_id, thumb_url, status FROM images")
	if err != nil {
		return
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id, description, driveLink, driveFileID, thumbURL, status sql.NullString
		if err := rows.Scan(&id, &description, &driveLink, &driveFileID, &thumbURL, &status); err != nil {
			continue
		}

		name := description.String
		if name == "" {
			name = id.String
		}

		node := &assettree.AssetNode{
			ID:          id.String,
			Source:      "images",
			AssetID:     id.String,
			Name:        name,
			Type:        "image",
			ParentID:    "",
			Path:        "",
			Depth:       0,
			IsFolder:    false,
			DriveFileID: driveFileID.String,
			DriveLink:   driveLink.String,
			Metadata:    fmt.Sprintf("{\"thumb_url\":\"%s\",\"status\":\"%s\"}", thumbURL.String, status.String),
		}

		repo.UpsertNode(ctx, node)
		count++
	}
	fmt.Printf("Synced %d items from images.db.sqlite\n", count)
}

func syncFromVoiceoverDB(ctx context.Context, dataDir string, repo *assettree.Repository) {
	dbPath := filepath.Join(dataDir, "voiceover.db.sqlite")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return
	}

	sqliteDB, err := storage.OpenSQLiteDB(dbPath, nil)
	if err != nil {
		return
	}
	db := sqliteDB.DB
	defer sqliteDB.Close()

	var tableName string
	err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='voiceovers'").Scan(&tableName)
	if err != nil {
		return
	}

	rows, err := db.Query("SELECT id, text_preview, drive_link, drive_file_id, folder_id, folder_path, status FROM voiceovers")
	if err != nil {
		return
	}
	defer rows.Close()

	pathMap := make(map[string]string)
	ensurePathSegments := func(fullPath string) string {
		fullPath = strings.Trim(fullPath, "/")
		if fullPath == "" {
			return ""
		}
		segments := strings.Split(fullPath, "/")
		currentPath := ""
		parentID := ""
		for _, seg := range segments {
			if currentPath == "" {
				currentPath = seg
			} else {
				currentPath = currentPath + "/" + seg
			}
			folderID, exists := pathMap[currentPath]
			if !exists {
				folderID = "generated_voiceover_" + strings.ReplaceAll(currentPath, "/", "_")
				pathMap[currentPath] = folderID
			}
			node := &assettree.AssetNode{
				ID:       folderID,
				Source:   "voiceover",
				AssetID:  folderID,
				Name:     seg,
				Type:     "folder",
				ParentID: parentID,
				Path:     currentPath,
				Depth:    strings.Count(currentPath, "/"),
				IsFolder: true,
				Metadata: "{}",
			}
			repo.UpsertNode(ctx, node)
			parentID = folderID
		}
		return parentID
	}

	count := 0
	for rows.Next() {
		var id, text, driveLink, driveFileID, folderID, folderPath, status sql.NullString
		if err := rows.Scan(&id, &text, &driveLink, &driveFileID, &folderID, &folderPath, &status); err != nil {
			continue
		}

		fPath := folderPath.String
		if isFilePath(fPath) {
			fPath = filepath.Dir(fPath)
		}
		effectiveParentID := ensurePathSegments(fPath)

		name := text.String
		if len(name) > 50 {
			name = name[:47] + "..."
		}
		if name == "" {
			name = id.String
		}

		node := &assettree.AssetNode{
			ID:          id.String,
			Source:      "voiceover",
			AssetID:     id.String,
			Name:        name,
			Type:        "audio",
			ParentID:    effectiveParentID,
			Path:        fPath,
			Depth:       strings.Count(fPath, "/"),
			IsFolder:    false,
			DriveFileID: driveFileID.String,
			DriveLink:   driveLink.String,
			Metadata:    fmt.Sprintf("{\"status\":\"%s\"}", status.String),
		}

		repo.UpsertNode(ctx, node)
		count++
	}
	fmt.Printf("Synced %d items from voiceover.db.sqlite\n", count)
}
