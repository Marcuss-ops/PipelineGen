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

	db, err := sql.Open("sqlite3", assetsDBPath)
	if err != nil {
		log.Fatalf("failed to open assets db: %v", err)
	}
	defer db.Close()

	_, _ = db.Exec("DELETE FROM asset_tree_nodes")
	fmt.Println("Cleared existing asset tree nodes")

	repo, err := assettree.NewRepository(db, nil)
	if err != nil {
		log.Fatalf("failed to create repo: %v", err)
	}

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
		// Use a combined sync function that handles nesting
		syncSourceWithNesting(ctx, dataDir, d.file, d.source, repo)
	}

	syncFromImagesDB(ctx, dataDir, repo)
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

func syncSourceWithNesting(ctx context.Context, dataDir, dbFile, source string, repo *assettree.Repository) {
	dbPath := filepath.Join(dataDir, dbFile)
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return
	}
	defer db.Close()

	// 1. Map paths to folder IDs and collect folder metadata
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

	// 2. Ensure all path segments exist as folders
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
				// Generate a deterministic ID for intermediate folders if they don't exist in DB
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

	// 3. Sync Clips
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
				
				// If DB had a folder_id, but our path logic generated a different one or didn't find it,
				// we trust the path logic for hierarchy but we could also use folderID.String if it matches.
				// For now, path logic is safer for nesting.

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

	// Create a dummy ensurePathSegments for this scope if needed, 
	// but we can just use a shared one. For now I'll use the one in syncSourceWithNesting logic by moving it out.
	// Actually, I'll just copy the logic or refactor.
	
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

	// 1. Map paths to folder IDs (shared logic)
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


