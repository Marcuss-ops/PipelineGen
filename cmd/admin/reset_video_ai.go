package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	"velox/go-master/internal/app"
)

const videoAIFolderName = "video ai"

func runResetVideoAI(args []string) error {
	fs := flag.NewFlagSet("reset-video-ai", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apply := fs.Bool("apply", false, "Actually delete and create (default: dry-run only)")
	sourceFolder := fs.String("folder", "1kr8c1KZmUus10mkIdqJlYqAzXDyoNZeY", "Source Drive folder ID to clear")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	deps, coreCleanup, err := app.ExportInitCoreMinimal(cfg, log)
	if err != nil {
		log.Fatal("Failed to initialize core services", zap.Error(err))
	}
	defer coreCleanup()

	if deps.DriveClient == nil {
		return fmt.Errorf("drive client is not available")
	}

	ctx := cmdContext()
	stockRootFolder := cfg.Drive.RootFolder()

	// Step 1: List and delete all items in the source folder
	fmt.Printf("📂 Source folder: %s\n", *sourceFolder)
	query := fmt.Sprintf("'%s' in parents and trashed = false", *sourceFolder)
	list, err := deps.DriveClient.Files.List().Q(query).
		Fields("files(id, name, mimeType)").
		PageSize(1000).
		Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to list folder: %w", err)
	}

	fmt.Printf("Found %d items to delete:\n", len(list.Files))
	for _, f := range list.Files {
		fmt.Printf("  🗑  %s (%s) [%s]\n", f.Name, f.Id, f.MimeType)
	}

	if *apply && len(list.Files) > 0 {
		fmt.Println("\nDeleting items...")
		for _, f := range list.Files {
			if err := deps.DriveClient.Files.Delete(f.Id).Context(ctx).Do(); err != nil {
				log.Warn("Failed to delete file", zap.String("name", f.Name), zap.String("id", f.Id), zap.Error(err))
			} else {
				fmt.Printf("  ✅ Deleted: %s\n", f.Name)
			}
		}
	}

	// Step 2: Create "video ai" folder on Drive under stock root
	var videoAIFolderID string
	if *apply {
		videoAIFolderID, err = getOrCreateDriveFolder(ctx, deps.DriveClient, videoAIFolderName, stockRootFolder)
		if err != nil {
			return fmt.Errorf("failed to create video ai folder: %w", err)
		}
		fmt.Printf("✅ Created Drive folder: %s (%s)\n", videoAIFolderName, videoAIFolderID)
	} else {
		fmt.Printf("\n📁 Would create Drive folder: %s under %s\n", videoAIFolderName, stockRootFolder)
	}

	// Step 3: Create DB entry in clip_folders
	if *apply {
		if err := createClipFolderEntry(ctx, deps.MediaDB.DB, videoAIFolderID, log); err != nil {
			return fmt.Errorf("failed to create DB entry: %w", err)
		}
		fmt.Printf("✅ Created DB entry: clipfolder_stock_video-ai\n")
	} else {
		fmt.Printf("📁 Would create DB entry: source=stock, group_name=%s\n", videoAIFolderName)
	}

	fmt.Println("\nDone!")
	return nil
}

func getOrCreateDriveFolder(ctx context.Context, svc *driveapi.Service, name, parentID string) (string, error) {
	query := fmt.Sprintf("name = '%s' and '%s' in parents and mimeType = 'application/vnd.google-apps.folder' and trashed = false",
		escapeName(name), parentID)
	list, err := svc.Files.List().Q(query).Fields("files(id, name)").Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("search folder: %w", err)
	}
	if len(list.Files) > 0 {
		return list.Files[0].Id, nil
	}

	folder := &driveapi.File{
		Name:     name,
		MimeType: "application/vnd.google-apps.folder",
		Parents:  []string{parentID},
	}
	created, err := svc.Files.Create(folder).Fields("id").Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("create folder: %w", err)
	}
	return created.Id, nil
}

func escapeName(name string) string {
	result := ""
	for _, c := range name {
		if c == '\'' {
			result += "\\'"
		} else {
			result += string(c)
		}
	}
	return result
}

func createClipFolderEntry(ctx context.Context, db *sql.DB, folderID string, log *zap.Logger) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.ExecContext(ctx, `
		INSERT OR REPLACE INTO clip_folders
			(id, source, source_url, video_id, folder_id, folder_path, local_folder_path, group_name,
			 manifest_txt_path, manifest_json_path, clip_count, processed_count, failed_count,
			 skipped_count, last_error, metadata, created_at, updated_at, search_key)
		VALUES
			(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"clipfolder_stock_video-ai",
		"stock",
		"",
		"",
		folderID,
		videoAIFolderName,
		"",
		videoAIFolderName,
		"",
		"",
		0,  // clip_count
		0,  // processed_count
		0,  // failed_count
		0,  // skipped_count
		"",
		"{}",
		now,
		now,
		"",
	)
	return err
}
