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

	repo, err := assettree.NewRepository(db, nil)
	if err != nil {
		log.Fatalf("failed to create repo: %v", err)
	}

	// 1. Sync from clip DBs (clips, artlist, stock)
	clipDBs := []string{"clips.db.sqlite", "artlist.db.sqlite", "stock.db.sqlite"}
	for _, dbFile := range clipDBs {
		syncFoldersFromClipsDB(ctx, dataDir, dbFile, repo)
		syncFromClipsDB(ctx, dataDir, dbFile, repo)
	}

	// 2. Sync from images.db.sqlite
	syncFromImagesDB(ctx, dataDir, repo)

	// 3. Sync from voiceover.db.sqlite
	syncFromVoiceoverDB(ctx, dataDir, repo)
}

func syncFoldersFromClipsDB(ctx context.Context, dataDir, dbFile string, repo *assettree.Repository) {
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

	rows, err := db.Query("SELECT folder_id, source, folder_path FROM clip_folders")
	if err != nil {
		return
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var folderID, source, folderPath sql.NullString
		if err := rows.Scan(&folderID, &source, &folderPath); err != nil {
			continue
		}

		if folderID.String == "" {
			continue
		}

		sourceStr := source.String
		if sourceStr == "" {
			if strings.Contains(dbFile, "artlist") {
				sourceStr = "artlist"
			} else if strings.Contains(dbFile, "stock") {
				sourceStr = "stock"
			} else {
				sourceStr = "clips"
			}
		}

		name := filepath.Base(folderPath.String)
		if name == "." || name == "/" {
			name = folderID.String
		}

		// Calculate parent_id (naive approach: take parent of path)
		parentPath := filepath.Dir(folderPath.String)
		parentID := ""
		if parentPath != "." && parentPath != "/" {
			// In our flat DB, we don't always have parent folder IDs easily.
			// For now, we'll keep it flat or use a heuristic.
		}

		node := &assettree.AssetNode{
			ID:       folderID.String,
			Source:   sourceStr,
			AssetID:  folderID.String,
			Name:     name,
			Type:     "folder",
			ParentID: parentID,
			Path:     folderPath.String,
			Depth:    strings.Count(folderPath.String, "/"),
			IsFolder: true,
			Metadata: "{}",
		}

		repo.UpsertNode(ctx, node)
		count++
	}
	fmt.Printf("Synced %d folders from %s\n", count, dbFile)
}

func syncFromClipsDB(ctx context.Context, dataDir, dbFile string, repo *assettree.Repository) {
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

	rows, err := db.Query("SELECT id, source, name, folder_id, folder_path, drive_link, drive_file_id, status FROM clips")
	if err != nil {
		return
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id, source, name, folderID, folderPath, driveLink, driveFileID, status sql.NullString
		if err := rows.Scan(&id, &source, &name, &folderID, &folderPath, &driveLink, &driveFileID, &status); err != nil {
			continue
		}

		sourceStr := source.String
		if sourceStr == "" {
			if strings.Contains(dbFile, "artlist") {
				sourceStr = "artlist"
			} else if strings.Contains(dbFile, "stock") {
				sourceStr = "stock"
			} else {
				sourceStr = "clips"
			}
		}

		// Ensure folder exists
		if folderID.String != "" {
			syncFolderNode(ctx, repo, sourceStr, folderID.String, folderPath.String)
		}

		node := &assettree.AssetNode{
			ID:          id.String,
			Source:      sourceStr,
			AssetID:     id.String,
			Name:        name.String,
			Type:        "video",
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
	fmt.Printf("Synced %d clips from %s\n", count, dbFile)
}

func syncFolderNode(ctx context.Context, repo *assettree.Repository, source, folderID, folderPath string) {
	if folderID == "" {
		return
	}
	name := filepath.Base(folderPath)
	if name == "." || name == "/" {
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

	rows, err := db.Query("SELECT id, source, description, drive_link, drive_file_id, thumb_url, status FROM images")
	if err != nil {
		return
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id, source, description, driveLink, driveFileID, thumbURL, status sql.NullString
		if err := rows.Scan(&id, &source, &description, &driveLink, &driveFileID, &thumbURL, &status); err != nil {
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

		// Ensure folder exists
		if folderID.String != "" {
			fnode := &assettree.AssetNode{
				ID:       folderID.String,
				Source:   "voiceover",
				AssetID:  folderID.String,
				Name:     filepath.Base(folderPath.String),
				Type:     "folder",
				ParentID: "",
				Path:     folderPath.String,
				Depth:    strings.Count(folderPath.String, "/"),
				IsFolder: true,
				Metadata: "{}",
			}
			repo.UpsertNode(ctx, fnode)
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
