package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"velox/go-master/internal/repository/assettree"
	_ "github.com/mattn/go-sqlite3"
)

func main() {
	ctx := context.Background()
	dataDir := "data"
	assetsDBPath := filepath.Join(dataDir, "assets.db.sqlite")

	// Open Assets DB
	db, err := sql.Open("sqlite3", assetsDBPath)
	if err != nil {
		log.Fatalf("failed to open assets db: %v", err)
	}
	defer db.Close()

	// Clear existing tree for a clean backfill
	_, _ = db.Exec("DELETE FROM asset_tree_nodes")
	fmt.Println("Cleared existing asset tree nodes")

	repo, err := assettree.NewRepository(db, nil)
	if err != nil {
		log.Fatalf("failed to create repo: %v", err)
	}

	// 1. Sync from clip DBs (clips, artlist, stock)
	type dbMap struct {
		file   string
		source string
	}
	clipDBs := []dbMap{
		{file: "clips.db.sqlite", source: "youtube"},
		{file: "artlist.db.sqlite", source: "artlist"},
		{file: "stock.db.sqlite", source: "stock"},
	}

	for _, d := range clipDBs {
		syncFoldersFromClipsDB(ctx, dataDir, d.file, d.source, repo)
		syncFromClipsDB(ctx, dataDir, d.file, d.source, repo)
	}

	// 2. Sync from images.db.sqlite
	syncFromImagesDB(ctx, dataDir, repo)

	// 3. Sync from voiceover.db.sqlite
	syncFromVoiceoverDB(ctx, dataDir, repo)
}

func isFilePath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".mp4", ".mkv", ".mov", ".avi", ".mp3", ".wav", ".txt", ".json", ".jpg", ".png", ".jpeg":
		return true
	}
	return false
}

func syncFoldersFromClipsDB(ctx context.Context, dataDir, dbFile, sourceOverride string, repo *assettree.Repository) {
	dbPath := filepath.Join(dataDir, dbFile)
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return
	}
	defer db.Close()

	var tableName string
	err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='clip_folders'").Scan(&tableName)
	if err != nil {
		return
	}

	rows, err := db.Query("SELECT folder_id, folder_path FROM clip_folders")
	if err != nil {
		return
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var folderID, folderPath sql.NullString
		if err := rows.Scan(&folderID, &folderPath); err != nil {
			continue
		}

		if folderID.String == "" {
			continue
		}

		fPath := folderPath.String
		if isFilePath(fPath) {
			fPath = filepath.Dir(fPath)
			if fPath == "." {
				continue // Skip root-level files incorrectly marked as folders
			}
		}

		name := filepath.Base(fPath)
		if name == "." || name == "/" || name == "" {
			name = folderID.String
		}

		node := &assettree.AssetNode{
			ID:       folderID.String,
			Source:   sourceOverride,
			AssetID:  folderID.String,
			Name:     name,
			Type:     "folder",
			ParentID: "",
			Path:     fPath,
			Depth:    strings.Count(fPath, "/"),
			IsFolder: true,
			Metadata: "{}",
		}

		repo.UpsertNode(ctx, node)
		count++
	}
	fmt.Printf("Synced %d folders from %s (source: %s)\n", count, dbFile, sourceOverride)
}

func syncFromClipsDB(ctx context.Context, dataDir, dbFile, sourceOverride string, repo *assettree.Repository) {
	dbPath := filepath.Join(dataDir, dbFile)
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return
	}
	defer db.Close()

	var tableName string
	err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='clips'").Scan(&tableName)
	if err != nil {
		return
	}

	rows, err := db.Query("SELECT id, name, filename, folder_id, folder_path, drive_link, drive_file_id, status FROM clips")
	if err != nil {
		return
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id, name, filename, folderID, folderPath, driveLink, driveFileID, status sql.NullString
		if err := rows.Scan(&id, &name, &filename, &folderID, &folderPath, &driveLink, &driveFileID, &status); err != nil {
			continue
		}

		effectiveFolderID := folderID.String
		fPath := folderPath.String
		
		// If folderPath is actually a file path, extract the directory
		if isFilePath(fPath) {
			fPath = filepath.Dir(fPath)
			if fPath == "." {
				effectiveFolderID = "" // Root
			}
		}

		if effectiveFolderID != "" {
			syncFolderNode(ctx, repo, sourceOverride, effectiveFolderID, fPath)
		}

		node := &assettree.AssetNode{
			ID:          id.String,
			Source:      sourceOverride,
			AssetID:     id.String,
			Name:        name.String,
			Type:        "video",
			ParentID:    effectiveFolderID,
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
	fmt.Printf("Synced %d clips from %s (source: %s)\n", count, dbFile, sourceOverride)
}

func syncFolderNode(ctx context.Context, repo *assettree.Repository, source, folderID, folderPath string) {
	if folderID == "" {
		return
	}
	name := filepath.Base(folderPath)
	if name == "." || name == "/" || name == "" {
		name = folderID
	}
	node := &assettree.AssetNode{
		ID:       folderID,
		Source:   source,
		AssetID:  folderID,
		Name:     name,
		Type:     "folder",
		ParentID: "",
		Path:     folderPath,
		Depth:    strings.Count(folderPath, "/"),
		IsFolder: true,
		Metadata: "{}",
	}
	repo.UpsertNode(ctx, node)
}

func syncFromImagesDB(ctx context.Context, dataDir string, repo *assettree.Repository) {
	dbPath := filepath.Join(dataDir, "images.db.sqlite")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return
	}
	defer db.Close()

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
			Path:        name,
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

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return
	}
	defer db.Close()

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

	count := 0
	for rows.Next() {
		var id, text, driveLink, driveFileID, folderID, folderPath, status sql.NullString
		if err := rows.Scan(&id, &text, &driveLink, &driveFileID, &folderID, &folderPath, &status); err != nil {
			continue
		}

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
			ParentID:    folderID.String,
			Path:        folderPath.String,
			Depth:       strings.Count(folderPath.String, "/"),
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

