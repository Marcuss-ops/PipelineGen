package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
)

var supportedLanguages = []string{
	"italiano", "fra", "de", "pt", "es", "ru", "tr", "ind", "eng", "Polacco",
}

func runSyncOutros(args []string) error {
	fs := flag.NewFlagSet("sync-outros", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apply := fs.Bool("apply", false, "Actually create missing folders on Drive and write to DB (default: dry-run only)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	deps, coreCleanup, err := app.InitCore(cfg, log)
	if err != nil {
		log.Fatal("Failed to initialize core services", zap.Error(err))
	}
	defer coreCleanup()

	if deps.DriveClient == nil {
		return fmt.Errorf("drive client is not available")
	}

	ctx := cmdContext()

	outroRootID := ""
	if outroRootID == "" {
		outroRootID = "12z9U1KRM5C5WmC9xOS2kRtdBDCyL-vbq" // fallback
	}

	if *apply {
		fmt.Printf("=== Starting Outros Synchronization (APPLY Mode) ===\n")
	} else {
		fmt.Printf("=== Starting Outros Synchronization (DRY RUN - use --apply to write) ===\n")
	}
	fmt.Printf("Outro Root Folder ID: %s\n\n", outroRootID)

	// Step 1: List subfolders of the Outro root folder
	query := fmt.Sprintf("'%s' in parents and mimeType = 'application/vnd.google-apps.folder' and trashed = false", outroRootID)
	list, err := deps.DriveClient.Files.List().Q(query).Fields("files(id, name)").Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to list outro folders: %w", err)
	}

	fmt.Printf("Found %d base outro folders on Drive.\n", len(list.Files))

	for _, folder := range list.Files {
		fmt.Printf("\nProcessing folder: %s (%s)\n", folder.Name, folder.Id)

		// Sync this base folder to clip_folders
		if *apply {
			err := upsertFolderToDB(ctx, deps.DB.DB, folder.Id, folder.Name, "outro", "outro", "")
			if err != nil {
				log.Error("failed to upsert base folder to DB", zap.String("folder", folder.Name), zap.Error(err))
			} else {
				fmt.Printf("  ✅ Synced base folder to DB\n")
			}
		}

		// List current children of this folder
		childQuery := fmt.Sprintf("'%s' in parents and trashed = false", folder.Id)
		childList, err := deps.DriveClient.Files.List().Q(childQuery).Fields("files(id, name, mimeType, webViewLink, webContentLink)").Context(ctx).Do()
		if err != nil {
			log.Error("failed to list children", zap.String("folder", folder.Name), zap.Error(err))
			continue
		}

		// Map existing folders by lowercase name
		existingFolders := make(map[string]string) // name -> id
		for _, f := range childList.Files {
			if f.MimeType == "application/vnd.google-apps.folder" {
				existingFolders[strings.ToLower(f.Name)] = f.Id
			}
		}

		// For each target language, ensure folder exists
		for _, lang := range supportedLanguages {
			langLower := strings.ToLower(lang)
			langFolderID, exists := existingFolders[langLower]

			if !exists {
				if *apply {
					// Create folder on Drive
					newFolder := &driveapi.File{
						Name:     lang,
						MimeType: "application/vnd.google-apps.folder",
						Parents:  []string{folder.Id},
					}
					created, err := deps.DriveClient.Files.Create(newFolder).Fields("id").Context(ctx).Do()
					if err != nil {
						log.Error("failed to create language folder on Drive", zap.String("lang", lang), zap.Error(err))
						continue
					}
					langFolderID = created.Id
					fmt.Printf("  📁 Created language folder: %s (%s)\n", lang, langFolderID)
				} else {
					fmt.Printf("  [DRY RUN] Would create language folder: %s\n", lang)
				}
			} else {
				fmt.Printf("  📁 Language folder already exists: %s (%s)\n", lang, langFolderID)
			}

			// Sync language folder to DB
			if *apply && langFolderID != "" {
				err := upsertFolderToDB(ctx, deps.DB.DB, langFolderID, folder.Name+"_"+lang, "outro", folder.Name, lang)
				if err != nil {
					log.Error("failed to upsert language folder to DB", zap.String("lang", lang), zap.Error(err))
				} else {
					fmt.Printf("    ✅ Synced folder %s to DB\n", lang)
				}

				// Scan files inside this language folder and add them to media_assets
				fileQuery := fmt.Sprintf("'%s' in parents and mimeType != 'application/vnd.google-apps.folder' and trashed = false", langFolderID)
				fileList, err := deps.DriveClient.Files.List().Q(fileQuery).Fields("files(id, name, webViewLink, webContentLink)").Context(ctx).Do()
				if err == nil {
					for _, file := range fileList.Files {
						err := upsertFileToDB(ctx, deps.DB.DB, file.Id, file.Name, folder.Name, lang, file.WebViewLink, file.WebContentLink)
						if err != nil {
							log.Error("failed to upsert file to DB", zap.String("file", file.Name), zap.Error(err))
						} else {
							fmt.Printf("      🎬 Synced file: %s\n", file.Name)
						}
					}
				}
			} else if !*apply {
				// Dry run: list existing files inside the existing language folder if it exists
				if langFolderID != "" {
					fileQuery := fmt.Sprintf("'%s' in parents and mimeType != 'application/vnd.google-apps.folder' and trashed = false", langFolderID)
					fileList, err := deps.DriveClient.Files.List().Q(fileQuery).Fields("files(name)").Context(ctx).Do()
					if err == nil && len(fileList.Files) > 0 {
						for _, file := range fileList.Files {
							fmt.Printf("      [DRY RUN] Would sync file: %s\n", file.Name)
						}
					}
				}
			}
		}
	}

	fmt.Println("\nSynchronization complete.")
	return nil
}

func upsertFolderToDB(ctx context.Context, db *sql.DB, folderID, path, source, groupName, lang string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	id := "clipfolder_outro_" + folderID
	meta := map[string]any{
		"is_folder": true,
	}
	if lang != "" {
		meta["language"] = lang
	}
	metaJSON, _ := json.Marshal(meta)

	_, err := db.ExecContext(ctx, `
		INSERT OR REPLACE INTO clip_folders
			(id, source, source_url, video_id, folder_id, folder_path, local_folder_path, group_name,
			 manifest_txt_path, manifest_json_path, clip_count, processed_count, failed_count,
			 skipped_count, last_error, metadata, created_at, updated_at, search_key)
		VALUES
			(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, source, "", "", folderID, path, "", groupName, "", "", 0, 0, 0, 0, "", string(metaJSON), now, now, lang,
	)
	return err
}

func upsertFileToDB(ctx context.Context, db *sql.DB, fileID, name, groupName, lang, driveLink, downloadLink string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	meta := map[string]any{
		"category":      "outro",
		"language":      lang,
		"group_name":    groupName,
		"drive_link":    driveLink,
		"download_link": downloadLink,
	}
	metaJSON, _ := json.Marshal(meta)

	// Setup tags
	tags := []string{"outro", groupName, lang}
	tagsJSON, _ := json.Marshal(tags)
	tagsNorm := strings.Join(tags, " ")

	_, err := db.ExecContext(ctx, `
		INSERT INTO media_assets 
			(id, source, name, tags, tags_norm, duration_ms, url, media_type, status, local_path, relative_path, drive_file_id, drive_link, download_link, file_hash, embedding_json, metadata_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			source=excluded.source,
			name=excluded.name,
			tags=excluded.tags,
			tags_norm=excluded.tags_norm,
			url=excluded.url,
			drive_file_id=excluded.drive_file_id,
			drive_link=excluded.drive_link,
			download_link=excluded.download_link,
			metadata_json=excluded.metadata_json,
			updated_at=excluded.updated_at
		`, fileID, "outro", name, string(tagsJSON), tagsNorm,
		0, driveLink, "video", "ready", "", "", fileID, driveLink, downloadLink, "", "[]", string(metaJSON), now, now)

	return err
}
