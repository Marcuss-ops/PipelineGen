// cmd/admin/remove_drive_folder_recursive.go — recursive Drive folder removal
//
// Recursively removes a Google Drive folder and ALL its subdirectories
// from Drive, SQLite (media_assets, clip_folders, drive_folder_catalog),
// and Qdrant (via the outbox-driven deletion state machine).
//
// Usage:
//
//	admin remove-drive-folder-recursive <drive-folder-id>
//
// Example:
//
//	admin remove-drive-folder-recursive 10p7NPodbQNjbSyvDIQJtowcmGeejwwlb
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

func runRemoveDriveFolderRecursive(args []string) error {
	fs := flag.NewFlagSet("remove-drive-folder-recursive", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apply := fs.Bool("apply", false, "Actually delete (default: dry-run that lists what would be removed)")
	sourceVideoIDs := fs.String("source-video-ids", "", "Comma-separated YouTube source IDs whose indexed clips must also be removed")
	assetsOnly := fs.Bool("assets-only", false, "Skip Drive folder traversal and remove only assets selected by --source-video-ids")
	if err := fs.Parse(args); err != nil {
		return err
	}

	folderIDs := fs.Args()
	if len(folderIDs) == 0 {
		return fmt.Errorf("at least one Drive folder ID is required")
	}

	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		log.Fatal("Failed to initialize composition root", zap.Error(err))
	}
	defer rootCleanup()

	if root.Drive == nil || root.Drive.Reader == nil {
		return fmt.Errorf("drive reader port is not available")
	}
	driveReader := root.Drive.Reader
	driveAdmin := root.Drive.Admin
	deletionSvc := root.Maint.DeletionSvc
	db := root.DB.DB

	ctx := cmdContext()

	for _, rootFolderID := range folderIDs {
		fmt.Printf("\n=== Processing root folder: %s ===\n", rootFolderID)

		// Step 1: Recursively collect all subfolder IDs
		fmt.Println("Step 1: Scanning folder tree...")
		var allFolders []folderInfo
		if !*assetsOnly {
			allFolders, err = collectAllSubfolders(ctx, driveReader, rootFolderID)
			if err != nil {
				return fmt.Errorf("failed to scan folder tree for %s: %w", rootFolderID, err)
			}
			// Include the root folder itself
			allFolders = append([]folderInfo{{ID: rootFolderID, Name: "(root)"}}, allFolders...)
		}
		fmt.Printf("Found %d total folders (including root).\n", len(allFolders))

		// Step 2: For each folder, find and count media_assets
		fmt.Println("\nStep 2: Counting media assets per folder...")
		totalAssets := 0
		for _, f := range allFolders {
			count, err := countAssetsInFolder(ctx, db, f.ID)
			if err != nil {
				log.Warn("Failed to count assets in folder", zap.String("folder_id", f.ID), zap.Error(err))
				continue
			}
			if count > 0 {
				fmt.Printf("  Folder %s (%s): %d assets\n", f.Name, f.ID, count)
			}
			totalAssets += count
		}
		fmt.Printf("Total assets to remove: %d\n", totalAssets)
		metadataAssets, err := listAssetsBySourceVideoIDs(ctx, db, *sourceVideoIDs)
		if err != nil {
			return fmt.Errorf("failed to list source-video assets: %w", err)
		}
		if len(metadataAssets) > 0 {
			fmt.Printf("Source-video assets to remove: %d\n", len(metadataAssets))
			totalAssets += len(metadataAssets)
		}

		if !*apply {
			fmt.Println("\nDRY RUN — use --apply to execute deletion.")
			fmt.Printf("Would remove %d folders and %d assets.\n", len(allFolders), totalAssets)
			continue
		}

		// Step 3: Delete media assets via DeletionService
		fmt.Println("\nStep 3: Deleting media assets...")
		assetFailures := 0
		for i, f := range allFolders {
			assets, err := listAssetsInFolder(ctx, db, f.ID)
			if err != nil {
				log.Warn("Failed to list assets in folder", zap.String("folder_id", f.ID), zap.Error(err))
				continue
			}
			for _, a := range assets {
				fmt.Printf("  [%d/%d] Deleting asset %s (%s) from folder %s... ",
					i+1, len(allFolders), a.Name, a.ID, f.Name)
				if deletionSvc != nil {
					if err := deletionSvc.DeleteClip(ctx, a.Source, a.ID, true); err != nil {
						fmt.Printf("FAILED: %v\n", err)
						assetFailures++
					} else {
						fmt.Println("OK")
					}
				} else {
					fmt.Println("SKIPPED (no deletion service)")
				}
			}
		}
		for _, a := range metadataAssets {
			if err := deletionSvc.DeleteClip(ctx, a.Source, a.ID, true); err != nil {
				fmt.Printf("  Deleting source-video asset %s (%s)... FAILED: %v\n", a.Name, a.ID, err)
				assetFailures++
			} else {
				fmt.Printf("  Deleting source-video asset %s (%s)... OK\n", a.Name, a.ID)
			}
		}

		// Step 4: Delete folders from Drive and clean up clip_folders + drive_folder_catalog
		fmt.Println("\nStep 4: Deleting folders from Drive and database...")
		folderFailures := 0
		// Process in reverse order (deepest first) to avoid orphan issues
		for i := len(allFolders) - 1; i >= 0; i-- {
			f := allFolders[i]
			fmt.Printf("  Deleting folder %s (%s)... ", f.Name, f.ID)

			// Delete from Drive
			if driveAdmin != nil {
				if err := driveAdmin.DeleteFolder(ctx, f.ID); err != nil {
					fmt.Printf("Drive delete FAILED: %v", err)
					folderFailures++
				} else {
					fmt.Printf("Drive OK")
				}
			} else {
				fmt.Printf("(no drive admin)")
			}

			// Clean up clip_folders table
			if db != nil {
				if _, err := db.ExecContext(ctx, "DELETE FROM clip_folders WHERE folder_id = ?", f.ID); err != nil {
					log.Warn("Failed to delete clip_folders row", zap.String("folder_id", f.ID), zap.Error(err))
				}
				// Clean up drive_folder_catalog table
				if _, err := db.ExecContext(ctx, "DELETE FROM drive_folder_catalog WHERE folder_id = ?", f.ID); err != nil {
					log.Warn("Failed to delete drive_folder_catalog row", zap.String("folder_id", f.ID), zap.Error(err))
				}
			}
			fmt.Println()
		}

		fmt.Printf("\nDone. Assets: %d deleted, %d failures. Folders: %d processed, %d failures.\n",
			totalAssets-assetFailures, assetFailures, len(allFolders)-folderFailures, folderFailures)
	}

	return nil
}

// folderInfo holds the name and ID of a Drive folder.
type folderInfo struct {
	ID   string
	Name string
}

// collectAllSubfolders recursively collects all subfolder IDs under the given parent.
func collectAllSubfolders(ctx context.Context, reader drive.Reader, parentID string) ([]folderInfo, error) {
	if reader == nil {
		return nil, fmt.Errorf("drive reader port not available")
	}
	files, err := reader.ListFiles(ctx, parentID)
	if err != nil {
		return nil, err
	}

	var folders []folderInfo
	for _, f := range files {
		if f.MimeType != "application/vnd.google-apps.folder" {
			continue
		}
		folders = append(folders, folderInfo{ID: f.ID, Name: f.Name})

		subs, err := collectAllSubfolders(ctx, reader, f.ID)
		if err != nil {
			return folders, fmt.Errorf("recursing into %s (%s): %w", f.Name, f.ID, err)
		}
		folders = append(folders, subs...)
	}
	return folders, nil
}

// assetRow is a minimal projection for deletion.
type assetRow struct {
	ID     string
	Name   string
	Source string
}

// countAssetsInFolder counts non-soft-deleted media_assets whose
// folder_id or parent_folder_id matches the given folderID.
func countAssetsInFolder(ctx context.Context, db *sql.DB, folderID string) (int, error) {
	var count int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_assets
		 WHERE `+asset.SoftDeleteFilter()+`
		 AND (folder_id = ? OR parent_folder_id = ?)`,
		folderID, folderID).Scan(&count)
	return count, err
}

// listAssetsInFolder lists non-soft-deleted media_assets whose
// folder_id or parent_folder_id matches the given folderID.
func listAssetsInFolder(ctx context.Context, db *sql.DB, folderID string) ([]assetRow, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, COALESCE(name, ''), source
		 FROM media_assets
		 WHERE `+asset.SoftDeleteFilter()+`
		 AND (folder_id = ? OR parent_folder_id = ?)
		 ORDER BY name`,
		folderID, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []assetRow
	for rows.Next() {
		var a assetRow
		if err := rows.Scan(&a.ID, &a.Name, &a.Source); err != nil {
			return out, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func listAssetsBySourceVideoIDs(ctx context.Context, db *sql.DB, raw string) ([]assetRow, error) {
	ids := make([]string, 0)
	for _, id := range strings.Split(raw, ",") {
		if value := strings.TrimSpace(id); value != "" {
			ids = append(ids, value)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := db.QueryContext(ctx,
		`SELECT id, COALESCE(name, ''), source FROM media_assets WHERE `+asset.SoftDeleteFilter()+
			` AND json_extract(COALESCE(metadata_json, '{}'), '$.source_video_id') IN (`+placeholders+") ORDER BY id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []assetRow
	for rows.Next() {
		var a assetRow
		if err := rows.Scan(&a.ID, &a.Name, &a.Source); err != nil {
			return out, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
