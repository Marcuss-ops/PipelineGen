// cmd/admin/list_drive_folder.go — Drive folder scanner
//
// Recursive Drive folder scan, optional sync of discovered folders
// into the canonical `clip_folders` SQLite table, plus cleanup-polluted
// flag for the operator-driven Drive style cleanup.
//
// Post-fix wiring:
//
//   - app.ExportInitCoreMinimal was removed in PR4d-final; we use
//     app.InitComposition instead.
//   - drives internal/media/models is removed; the local
//     `folderRec` struct replaces `models.ClipFolder`. The struct
//     mirrors the column shape from migration 011_create_characters.sql
//     + the canonical schema in internal/infrastructure/database/canonical.go.
//   - internal/repository/clips is removed; the canonical
//     *assets.ClipsRepository (root.Repos.ClipsRepo) only knows about
//     `media_assets`, not the `clip_folders` table. We use raw SQL on
//     root.DB.DB to upsert rows to `clip_folders` (matches the pattern
//     used in internal/app/bootstrap.go::resolveDynamicDriveFolders).
//   - internal/upload/drive is removed; root.Drive.DriveUploader is the
//     canonical (internal/infrastructure/drive) replacement.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

const (
	defaultListDriveFolderRoot = "1MB9pTRjvHUdMXUtGOMBcvgRc-MZG2rA4"
	pollutedStyleFolderIDs     = "1VxdOW33FqlbPvvslkN6G73SLzqY2l1Tv,1WjRQ8w9x4ZRdeHu8NKnGVfhZqYcLxTx_," +
		"1wSLnwQKe456Mk76L1lzKKuTTFumqHwAC,1AljfHhJRDQeS7rNTEOeIHuSPSCbrv0j0," +
		"1tIJS7LKY_CQ_7JoWNVMD3Sgt3qc4tuYf,1scDO6C4t45h8dSWm-u02T2ArhXzJnOiK," +
		"1NiTRoleSxt4uZhSk3Cv6IGRwH-eILyOo,12g6dp2uEfBtgD-czdtOqcb2IQgas3JyT," +
		"1z6pK8v26bC-ycaUcYd4js8HW2XdVntfa,1LdyO59ilaBB0cmVfL_mUb0Xl69knSx67," +
		"1nIzrkBTgrvt6dsMnxhF_xJ16qGp7RuC3,1rlYO2wpKUm-kpEBpXCNKu10PUs2jDsOl," +
		"1FfoX_sRJMDMyeV9p-uThCQVYkZGXWmsR,1Xb0_N8sKafCDP1jvLtcxNMMfoYg_6FvV," +
		"15tMOTU17XRN9evRKHWqzQ_us8eWVR5GA,1Lt9FqPixowdr2sxjIcArySfjWm19qQ6k," +
		"1sU3oqQusGDQ8YE3K7mFBlVLrxCgEGjZ_,1jDNjL0bggtY7nHKxbt1QaEiPyCZUpC9J," +
		"1dXNLQ9dsWITYpRXdERRKeA3EUoHfRm_,1rbPqm1PJ5LI9G7l_uJSUz4W1lN_jNXCT," +
		"141nC7yqzoOpahiFvV1P9ZMUKJyh8J90C"
)

// folderRec mirrors the columns of `clip_folders` used by the canonical
// listing + Drive folder sync code (see internal/app/bootstrap.go:240
// for the canonical INSERT shape). Used purely to (de)serialise a
// folder before issuing the SQL upsert.
type folderRec struct {
	ID          string
	Source      string
	GroupName   string
	FolderID    string
	FolderPath  string
	SourceURL   string
	SearchKey   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func runListDriveFolder(args []string) error {
	fs := flag.NewFlagSet("list-drive-folder", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	folder := fs.String("folder", defaultListDriveFolderRoot, "Drive folder ID to list (defaults to Media root)")
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

	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		log.Fatal("Failed to initialize composition root", zap.Error(err))
	}
	defer rootCleanup()

	if root.Drive == nil || root.Drive.DriveClient == nil {
		return fmt.Errorf("drive client is not available")
	}

	uploader := root.Drive.DriveUploader
	ctx := cmdContext()

	if *cleanupPolluted {
		return runListDriveFolderCleanupPolluted(ctx, root.DB.DB, uploader, log)
	}

	fmt.Printf("=== Scanning Google Drive Folder Hierarchy ===\n")
	fmt.Printf("Root Folder ID: %s\n", *folder)
	fmt.Printf("Sync to Database: %t\n\n", *syncDB)

	count, err := scanFolders(ctx, uploader, root.DB.DB, *folder, "", "", *syncDB, log)
	if err != nil {
		return fmt.Errorf("scan failed: %w", err)
	}

	fmt.Printf("\nDone! Successfully processed %d folders.\n", count)
	return nil
}

func runListDriveFolderCleanupPolluted(ctx context.Context, db *sql.DB, uploader *drive.Uploader, log *zap.Logger) error {
	fmt.Printf("=== Cleaning Up Polluted Style Folders Under Media Root ===\n")
	for _, id := range strings.Split(pollutedStyleFolderIDs, ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		fmt.Printf("Deleting polluted folder %s... ", id)
		if uploader == nil {
			fmt.Println("SKIPPED (no drive uploader)")
			continue
		}
		if err := uploader.DeleteFolder(ctx, id); err != nil {
			fmt.Printf("failed: %v\n", err)
			continue
		}
		fmt.Printf("OK\n")
		if db != nil {
			if _, err := db.ExecContext(ctx, "DELETE FROM clip_folders WHERE folder_id = ?", id); err != nil {
				log.Warn("failed to delete clip_folders row for polluted folder", zap.String("id", id), zap.Error(err))
			}
		}
	}
	return nil
}

// scanFolders recursively walks a Drive folder hierarchy, prints entries
// and (when syncDB is true) upserts each discovered folder into the
// canonical `clip_folders` SQLite table.
//
// Replaces the legacy `clips.Repository.UpsertClipFolder` call, which
// which lived in the deleted internal/repository/clips package. The
// upward-compatible column shape (id, source, source_url, folder_id,
// folder_path, group_name, search_key, created_at, updated_at) matches
// migration 011_create_characters.sql and the runtime INSERT pattern
// used by internal/app/bootstrap.go::resolveDynamicDriveFolders.
func scanFolders(
	ctx context.Context,
	uploader *drive.Uploader,
	db *sql.DB,
	folderID, currentPath, source string,
	syncDB bool,
	log *zap.Logger,
) (int, error) {
	if uploader == nil {
		return 0, fmt.Errorf("drive uploader not available")
	}
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

		if syncDB && db != nil {
			now := time.Now().UTC()
			cf := folderRec{
				ID:         file.ID,
				Source:     childSource,
				GroupName:  file.Name,
				FolderID:   file.ID,
				FolderPath: childPath,
				SourceURL:  link,
				SearchKey:  strings.ToLower(strings.ReplaceAll(childSource+file.Name, " ", "")),
				CreatedAt:  now,
				UpdatedAt:  now,
			}
			if err := upsertClipFolder(ctx, db, cf); err != nil {
				log.Warn("Failed to upsert folder in DB", zap.String("name", file.Name), zap.Error(err))
			} else {
				fmt.Printf("  -> Saved in DB\n")
				count++
			}
		}

		subCount, err := scanFolders(ctx, uploader, db, file.ID, childPath, childSource, syncDB, log)
		if err != nil {
			log.Warn("Failed to scan subfolder", zap.String("name", file.Name), zap.Error(err))
		}
		count += subCount
	}
	return count, nil
}

// upsertClipFolder writes a single folderRec into the `clip_folders`
// table using the canonical INSERT OR REPLACE shape. Mirrors
// internal/app/bootstrap.go::resolveDynamicDriveFolders and migration
// 011_create_characters.sql column set.
func upsertClipFolder(ctx context.Context, db *sql.DB, cf folderRec) error {
	_, err := db.ExecContext(ctx, `
		INSERT OR REPLACE INTO clip_folders
			(id, source, source_url, folder_id, folder_path, group_name, search_key,
			 metadata, created_at, updated_at)
		VALUES
			(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cf.ID, cf.Source, cf.SourceURL, cf.FolderID, cf.FolderPath,
		cf.GroupName, cf.SearchKey, "{}", cf.CreatedAt.UTC().Format(time.RFC3339),
		cf.UpdatedAt.UTC().Format(time.RFC3339),
	)
	return err
}
