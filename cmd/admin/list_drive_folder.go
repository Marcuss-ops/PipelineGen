package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
)

func runListDriveFolder(args []string) error {
	fs := flag.NewFlagSet("list-drive-folder", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	folder := fs.String("folder", "1MB9pTRjvHUdMXUtGOMBcvgRc-MZG2rA4", "Drive folder ID to list (defaults to Media root)")
	syncDB := fs.Bool("sync-db", true, "Sync the discovered folders to the database")
	cleanupPolluted := fs.Bool("cleanup-polluted", false, "Clean up polluted style folders under Media root")
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

	uploader := &drive.Uploader{Service: deps.DriveClient, Log: log}
	ctx := cmdContext()

	if *cleanupPolluted {
		fmt.Printf("=== Cleaning Up Polluted Style Folders Under Media Root ===\n")
		pollutedIDs := []string{
			"1VxdOW33FqlbPvvslkN6G73SLzqY2l1Tv", "1WjRQ8w9x4ZRdeHu8NKnGVfhZqYcLxTx_",
			"1wSLnwQKe456Mk76L1lzKKuTTFumqHwAC", "1AljfHhJRDQeS7rNTEOeIHuSPSCbrv0j0",
			"1tIJS7LKY_CQ_7JoWNVMD3Sgt3qc4tuYf", "1scDO6C4t45h8dSWm-u02T2ArhXzJnOiK",
			"1NiTRoleSxt4uZhSk3Cv6IGRwH-eILyOo", "12g6dp2uEfBtgD-czdtOqcb2IQgas3JyT",
			"1z6pK8v26bC-ycaUcYd4js8HW2XdVntfa", "1LdyO59ilaBB0cmVfL_mUb0Xl69knSx67",
			"1nIzrkBTgrvt6dsMnxhF_xJ16qGp7RuC3", "1rlYO2wpKUm-kpEBpXCNKu10PUs2jDsOl",
			"1FfoX_sRJMDMyeV9p-uThCQVYkZGXWmsR", "1Xb0_N8sKafCDP1jvLtcxNMMfoYg_6FvV",
			"15tMOTU17XRN9evRKHWqzQ_us8eWVR5GA", "1Lt9FqPixowdr2sxjIcArySfjWm19qQ6k",
			"1sU3oqQusGDQ8YE3K7mFBlVLrxCgEGjZ_", "1jDNjL0bggtY7nHKxbt1QaEiPyCZUpC9J",
			"1dXNLQ9dsWITYpRXdERRKeA3EUoHfRm_-", "1rbPqm1PJ5LI9G7l_uJSUz4W1lN_jNXCT",
			"141nC7yqzoOpahiFvV1P9ZMUKJyh8J90C",
		}
		for _, id := range pollutedIDs {
			fmt.Printf("Deleting polluted folder %s... ", id)
			if err := uploader.DeleteFolder(ctx, id); err != nil {
				fmt.Printf("failed: %v\n", err)
			} else {
				fmt.Printf("OK\n")
				// Delete from database
				_, _ = deps.DB.DB.ExecContext(ctx, "DELETE FROM clip_folders WHERE folder_id = ?", id)
			}
		}
		return nil
	}

	fmt.Printf("=== Scanning Google Drive Folder Hierarchy ===\n")
	fmt.Printf("Root Folder ID: %s\n", *folder)
	fmt.Printf("Sync to Database: %t\n\n", *syncDB)

	count, err := scanFolders(ctx, uploader, deps.ClipsOnlyRepo, *folder, "", "", *syncDB, log)
	if err != nil {
		return fmt.Errorf("scan failed: %w", err)
	}

	fmt.Printf("\nDone! Successfully processed %d folders.\n", count)
	return nil
}

func scanFolders(ctx context.Context, uploader *drive.Uploader, repo *clips.Repository, folderID, currentPath, source string, syncDB bool, log *zap.Logger) (int, error) {
	files, err := uploader.ListFiles(ctx, folderID)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, file := range files {
		if file.MimeType != "application/vnd.google-apps.folder" {
			continue
		}

		childSource := source
		childPath := file.Name
		if currentPath != "" {
			childPath = currentPath + "/" + file.Name
		} else {
			// Level 1: map root folder names to their lowercase sources
			nameLower := strings.ToLower(strings.TrimSpace(file.Name))
			switch nameLower {
			case "stock":
				childSource = "stock"
			case "clips":
				childSource = "youtube"
			case "books":
				childSource = "books"
			case "ai images":
				childSource = "videoai"
			case "outro":
				childSource = "outro"
			case "avatar ai":
				childSource = "avatar_ai"
			case "effetti suoni online":
				childSource = "sound_effects"
			case "immagini":
				childSource = "images"
			case "voiceover":
				childSource = "voiceover"
			case "copertine":
				childSource = "copertine"
			default:
				childSource = nameLower
			}
		}

		link := file.WebViewLink
		if link == "" {
			link = "https://drive.google.com/drive/folders/" + file.ID
		}

		fmt.Printf("Folder: %s (%s) [source: %s, path: %s, link: %s]\n", file.Name, file.ID, childSource, childPath, link)

		if syncDB && repo != nil {
			now := time.Now()
			cf := &models.ClipFolder{
				ID:              file.ID,
				Source:          childSource,
				SourceURL:       link,
				FolderID:        file.ID,
				FolderPath:      childPath,
				LocalFolderPath: "",
				Group:           file.Name,
				Metadata:        "{}",
				CreatedAt:       now,
				UpdatedAt:       now,
			}
			if err := repo.UpsertClipFolder(ctx, cf); err != nil {
				log.Warn("Failed to upsert folder in DB", zap.String("name", file.Name), zap.Error(err))
			} else {
				fmt.Printf("  -> Saved in DB\n")
				count++
			}
		}

		subCount, err := scanFolders(ctx, uploader, repo, file.ID, childPath, childSource, syncDB, log)
		if err != nil {
			log.Warn("Failed to scan subfolder", zap.String("name", file.Name), zap.Error(err))
		}
		count += subCount
	}
	return count, nil
}
