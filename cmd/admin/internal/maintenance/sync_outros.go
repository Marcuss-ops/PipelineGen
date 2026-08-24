package maintenance

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"go.uber.org/zap"
)

var supportedLanguages = []string{
	"italiano", "fra", "de", "pt", "es", "ru", "tr", "ind", "eng", "Polacco",
}

func RunSyncOutros(args []string) error {
	fs := flag.NewFlagSet("sync-outros", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apply := fs.Bool("apply", false, "Actually create missing folders on Drive and write to DB (default: dry-run only)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	root, _, coreCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		log.Fatal("Failed to initialize core services", zap.Error(err))
	}
	defer coreCleanup()

	if root.Drive == nil || root.Drive.Reader == nil || root.Drive.Admin == nil {
		return fmt.Errorf("drive admin/reader ports are not available")
	}

	ctx := cli.CmdContext()

	outroRootID := cfg.Drive.OutroFolder()
	if outroRootID == "" {
		outroRootID = "1MB9pTRjvHUdMXUtGOMBcvgRc-MZG2rA4" // user specified folder
	}

	if *apply {
		fmt.Printf("=== Starting Outros Synchronization (APPLY Mode) ===\n")
	} else {
		fmt.Printf("=== Starting Outros Synchronization (DRY RUN - use --apply to write) ===\n")
	}
	fmt.Printf("Outro Root Folder ID: %s\n\n", outroRootID)

	// Step 1: List subfolders of the Outro root folder
	query := fmt.Sprintf("'%s' in parents and mimeType = 'application/vnd.google-apps.folder' and trashed = false", outroRootID)
	list, err := root.Drive.Reader.SearchFiles(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to list outro folders: %w", err)
	}

	fmt.Printf("Found %d base outro folders on Drive.\n", len(list))

	for _, folder := range list {
		fmt.Printf("\nProcessing folder: %s (%s)\n", folder.Name, folder.ID)

		// Sync this base folder to clip_folders
		if *apply {
			err := upsertFolderToDB(ctx, root.DB.DB, folder.ID, folder.Name, "outro", "outro", "")
			if err != nil {
				log.Error("failed to upsert base folder to DB", zap.String("folder", folder.Name), zap.Error(err))
			} else {
				fmt.Printf("  ✅ Synced base folder to DB\n")
			}
		}

		// List current children of this folder
		childQuery := fmt.Sprintf("'%s' in parents and trashed = false", folder.ID)
		childList, err := root.Drive.Reader.SearchFiles(ctx, childQuery)
		if err != nil {
			log.Error("failed to list children", zap.String("folder", folder.Name), zap.Error(err))
			continue
		}

		// Map existing folders by lowercase name
		existingFolders := make(map[string]string) // name -> id
		for _, f := range childList {
			if f.MimeType == "application/vnd.google-apps.folder" {
				existingFolders[strings.ToLower(f.Name)] = f.ID
			}
		}

		// For each target language, ensure folder exists
		for _, lang := range supportedLanguages {
			langLower := strings.ToLower(lang)
			langFolderID, exists := existingFolders[langLower]
			if !exists {
				if *apply {
					// Wave C (June 2026): idempotent lookup-or-create replaces strict Files.Create.
					// GetOrCreateFolder returns the existing folder if a folder with the same
					// name already exists under parent — semantically slightly more lenient than
					// the previous strict create (which would 409 on duplicates). The call is
					// already gated behind `if !exists` so the semantic shift is benign.
					created, err := root.Drive.Admin.GetOrCreateFolder(ctx, lang, folder.ID)
					if err != nil {
						log.Error("failed to create language folder on Drive", zap.String("lang", lang), zap.Error(err))
						continue
					}
					langFolderID = created
					fmt.Printf("  📁 Created language folder: %s (%s)\n", lang, langFolderID)
				} else {
					fmt.Printf("  [DRY RUN] Would create language folder: %s\n", lang)
				}
			} else {
				fmt.Printf("  📁 Language folder already exists: %s (%s)\n", lang, langFolderID)
			}

			// Sync language folder to DB
			if *apply && langFolderID != "" {
				err := upsertFolderToDB(ctx, root.DB.DB, langFolderID, folder.Name+"_"+lang, "outro", folder.Name, lang)
				if err != nil {
					log.Error("failed to upsert language folder to DB", zap.String("lang", lang), zap.Error(err))
				} else {
					fmt.Printf("    ✅ Synced folder %s to DB\n", lang)
				}

				// Scan files inside this language folder and add them to media_assets
				fileQuery := fmt.Sprintf("'%s' in parents and mimeType != 'application/vnd.google-apps.folder' and trashed = false", langFolderID)
				fileList, err := root.Drive.Reader.SearchFiles(ctx, fileQuery)
				if err == nil {
					for _, file := range fileList {
						err := upsertFileToDB(ctx, root.DB.DB, file.ID, file.Name, folder.Name, lang, file.WebViewLink, file.WebContentLink)
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
					fileList, err := root.Drive.Reader.SearchFiles(ctx, fileQuery)
					if err == nil && len(fileList) > 0 {
						for _, file := range fileList {
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
			(id, source, name, tags, tags_norm, duration_ms, url, media_type, status, local_path, relative_path, drive_file_id, drive_link, download_link, legacy_file_md5, embedding_json, metadata_json, created_at, updated_at)
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
