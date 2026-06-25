// cmd/admin/cleanup.go — AGENT-1 recovery (June 2026)
//
// All cleanup subcommands (cleanup-orphans, cleanup-all-orphans,
// cleanup-artlist-empty-folders, cleanup-stock-orphans,
// delete-specific-folders, sync-all-drive, test-youtube) were migrated
// off the deleted legacy packages `internal/media`, `internal/upload/drive`
// and the deleted `app.ExportInitCoreMinimal` helper.
//
// Post-fix wiring:
//
//   - `app.InitComposition(cfg, log)` returns the canonical *ComposeRoot
//     (replacing `app.ExportInitCoreMinimal(cfg, log) *CoreDeps` which
//     was removed in PR4d-final).
//   - The deletion service now lives at `root.Maint.DeletionSvc`. The
//     canonical `deletion.NewDeletionService` (PR-wave 11) collapsed the
//     historical three source-specific clip repos
//     (ArtlistRepo / ClipsOnlyRepo / StockDriveRepo) into ONE
//     `*assets.ClipsRepository` — the source discriminator is now on the
//     row, not on the repo constructor argument. Caller code that passed
//     three different repos for artlist/stock/clips simply passes the
//     single `root.Repos.ClipsRepo` three times (matches the wiring in
//     `internal/app/composition.go::BuildMaintBundle`).
//   - The Google Drive client and uploader are reached through
//     `root.Drive.DriveClient` and `root.Drive.DriveUploader` (canonical
//     `internal/infrastructure/drive` types), no longer through the
//     removed `internal/upload/drive` package.
//
// Public subcommand dispatch is documented in `cmd/admin/main.go::main()`
// and covered by `cmd/admin/admin_test.go::TestAdminCommands_AreRegistered`.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

func runCleanupOrphans(args []string) error {
	fs := flag.NewFlagSet("cleanup-orphans", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apply := fs.Bool("apply", false, "Actually delete orphan files (default: dry-run only)")
	dir := fs.String("dir", "", "Assets directory to scan (default: config Storage.DataDir)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	// AGENT-1 fix: app.ExportInitCoreMinimal was removed in PR4d-final.
	// Use app.InitComposition to obtain the canonical *ComposeRoot.
	// The returned *backgroundJobs is internal to internal/app; we
	// discard it with `_` since the cmd/admin CLI does not start any
	// background workers — it just uses the configured root + cleanup.
	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		log.Fatal("Failed to initialize composition root", zap.Error(err))
	}
	defer rootCleanup()

	assetsDir := *dir
	if assetsDir == "" {
		assetsDir = cfg.Storage.DataDir
	}
	if absDir, err := filepath.Abs(assetsDir); err == nil {
		assetsDir = absDir
	}

	var driveUploader *drive.Uploader
	if root.Drive != nil && root.Drive.DriveClient != nil {
		driveUploader = root.Drive.DriveUploader
	}

	// AGENT-1 fix: the canonical DeletionService is pre-built by the
	// composition root (BuildMaintBundle). The old code constructed a
	// one-off service with three source-specific clip repos; the new
	// code reuses the singleton because repo collapsing makes the
	// per-source diff a row discriminator, not a constructor argument.
	deletionSvc := root.Maint.DeletionSvc
	if deletionSvc == nil {
		return fmt.Errorf("deletion service is not available (composition root missing Maint bundle)")
	}

	// AGENT-1 note: in the legacy wrapper, three repos (artlist/clips/
	// stock) collapsed to a single *assets.ClipsRepository on the
	// canonical DeletionService. Root.Repos.ClipsRepo already carries
	// all three responsibilities. Maint.DeletionSvc is itself built
	// with the right collapsed shape in BuildMaintBundle — no further
	// constructor adaptation is required at this site.
	_ = driveUploader

	if *apply {
		fmt.Printf("Starting DEEP ORPHAN CLEANUP in %s (APPLY mode - files WILL be deleted)\n", assetsDir)
	} else {
		fmt.Printf("Starting DEEP ORPHAN CLEANUP in %s (DRY RUN - no files will be deleted)\n", assetsDir)
		fmt.Println("Use --apply to actually delete orphan files")
	}
	fmt.Println()

	ctx := cmdContext()
	deleted, err := deletionSvc.CleanupOrphanFiles(ctx, assetsDir, !*apply)
	if err != nil {
		return fmt.Errorf("orphan cleanup failed: %w", err)
	}

	if *apply {
		fmt.Printf("\n✅ Cleanup complete: %d orphan files deleted\n", deleted)
	} else {
		fmt.Printf("\n📋 Dry-run complete: %d orphan files would be deleted\n", deleted)
	}
	return nil
}

func runCleanupAllOrphans(args []string) error {
	fs := flag.NewFlagSet("cleanup-all-orphans", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apply := fs.Bool("apply", false, "Actually delete folders (default: dry-run only)")
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
	driveClient := root.Drive.DriveClient
	driveUploader := root.Drive.DriveUploader

	targets := []struct {
		name     string
		rootID   string
		dbPrefix string
	}{
		{"Artlist", "1OAAf5dawAppdopsgCq1yHFGPUXCI9Vbk", "artlist"},
		{"Stock", "1wt4hqmHD5qEsNhpUUBszlRkSHhyFgtGh", "stock"},
		{"YouTube Clips", "1r4B_m3Gz_5f2f5O-vNqG6_G8_G8_G8_G", "clips"},
	}

	ctx := cmdContext()
	for _, t := range targets {
		if t.rootID == "" || t.rootID == "1r4B_m3Gz_5f2f5O-vNqG6_G8_G8_G8_G" {
			fmt.Printf("\n--- Skipping %s: Root ID not configured or placeholder ---\n", t.name)
			continue
		}

		fmt.Printf("\n--- Checking %s (Root: %s) ---\n", t.name, t.rootID)
		query := fmt.Sprintf("'%s' in parents and mimeType = 'application/vnd.google-apps.folder' and trashed = false", t.rootID)
		list, err := driveClient.Files.List().Q(query).Fields("files(id, name)").Context(ctx).Do()
		if err != nil {
			fmt.Printf("Error listing %s: %v\n", t.name, err)
			continue
		}

		fmt.Printf("Found %d folders on Drive.\n", len(list.Files))

		var orphans []struct{ id, name string }
		for _, f := range list.Files {
			var dummy int
			var dbErr error
			switch t.dbPrefix {
			case "artlist", "stock", "clips":
				dbErr = root.DB.DB.QueryRowContext(ctx, "SELECT 1 FROM media_assets WHERE id = ?", f.Id).Scan(&dummy)
			}

			if dbErr != nil {
				orphans = append(orphans, struct{ id, name string }{f.Id, f.Name})
			}
		}

		if len(orphans) == 0 {
			fmt.Printf("No orphan folders found for %s.\n", t.name)
			continue
		}

		fmt.Printf("Found %d orphan folders for %s.\n", len(orphans), t.name)
		if !*apply {
			for _, f := range orphans {
				fmt.Printf("  - [DRY RUN] Would delete: %s (%s)\n", f.name, f.id)
			}
			continue
		}

		for _, f := range orphans {
			fmt.Printf("  - Deleting %s (%s)... ", f.name, f.id)
			if driveUploader == nil {
				fmt.Println("SKIPPED (no drive uploader)")
				continue
			}
			err := driveUploader.DeleteFolder(ctx, f.id)
			if err != nil {
				fmt.Printf("FAILED: %v\n", err)
			} else {
				fmt.Println("OK")
			}
		}
	}

	return nil
}

func runCleanupArtlistEmptyFolders(args []string) error {
	fs := flag.NewFlagSet("cleanup-artlist-empty-folders", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apply := fs.Bool("apply", false, "Actually delete folders (default: dry-run only)")
	parentID := fs.String("parent", "1OAAf5dawAppdopsgCq1yHFGPUXCI9Vbk", "The Artlist root folder ID to scan on Drive")
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
	driveClient := root.Drive.DriveClient
	driveUploader := root.Drive.DriveUploader

	ctx := cmdContext()
	fmt.Printf("Scanning Drive folder: %s\n", *parentID)
	query := fmt.Sprintf("'%s' in parents and mimeType = 'application/vnd.google-apps.folder' and trashed = false", *parentID)

	list, err := driveClient.Files.List().Q(query).Fields("files(id, name)").Context(ctx).Do()
	if err != nil {
		log.Fatal("Failed to list folders on Drive", zap.Error(err))
	}

	fmt.Printf("Found %d folders on Drive.\n", len(list.Files))

	var orphanFolders []struct{ id, name string }
	for _, f := range list.Files {
		var dummy int
		err := root.DB.DB.QueryRowContext(ctx, "SELECT 1 FROM media_assets WHERE id = ? AND json_extract(COALESCE(metadata_json,'{}'), '$.is_folder') = 1", f.Id).Scan(&dummy)
		if err != nil {
			orphanFolders = append(orphanFolders, struct{ id, name string }{f.Id, f.Name})
		}
	}

	if len(orphanFolders) == 0 {
		fmt.Println("No orphan folders found on Drive.")
		return nil
	}

	fmt.Printf("Found %d orphan folders on Drive (not in DB).\n", len(orphanFolders))
	if !*apply {
		fmt.Println("DRY RUN: The following folders would be DELETED from Drive (use --apply to execute):")
		for _, f := range orphanFolders {
			fmt.Printf("- %s (ID: %s)\n", f.name, f.id)
		}
		return nil
	}

	if driveUploader == nil {
		return fmt.Errorf("drive uploader not available for apply mode")
	}
	fmt.Println("Deleting orphan folders from Drive...")
	for _, f := range orphanFolders {
		fmt.Printf("Deleting %s (%s)... ", f.name, f.id)
		err := driveUploader.DeleteFolder(ctx, f.id)
		if err != nil {
			fmt.Printf("FAILED: %v\n", err)
		} else {
			fmt.Println("OK")
		}
	}

	fmt.Println("\nCleanup complete.")
	return nil
}

func runCleanupStockOrphans(args []string) error {
	fs := flag.NewFlagSet("cleanup-stock-orphans", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apply := fs.Bool("apply", false, "Actually delete folders (default: dry-run only)")
	parentID := fs.String("parent", "1wt4hqmHD5qEsNhpUUBszlRkSHhyFgtGh", "The Stock root folder ID to scan on Drive")
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
	driveClient := root.Drive.DriveClient
	driveUploader := root.Drive.DriveUploader

	ctx := cmdContext()
	fmt.Printf("Scanning Drive folder: %s\n", *parentID)
	query := fmt.Sprintf("'%s' in parents and mimeType = 'application/vnd.google-apps.folder' and trashed = false", *parentID)

	list, err := driveClient.Files.List().Q(query).Fields("files(id, name)").Context(ctx).Do()
	if err != nil {
		log.Fatal("Failed to list folders on Drive", zap.Error(err))
	}

	fmt.Printf("Found %d folders on Drive.\n", len(list.Files))

	var orphanFolders []struct{ id, name string }
	for _, f := range list.Files {
		var dummy int
		err := root.DB.DB.QueryRowContext(ctx, "SELECT 1 FROM media_assets WHERE id = ? AND json_extract(COALESCE(metadata_json,'{}'), '$.is_folder') = 1", f.Id).Scan(&dummy)
		if err != nil {
			orphanFolders = append(orphanFolders, struct{ id, name string }{f.Id, f.Name})
		}
	}

	if len(orphanFolders) == 0 {
		fmt.Println("No orphan folders found on Drive.")
		return nil
	}

	fmt.Printf("Found %d orphan folders on Drive (not in DB).\n", len(orphanFolders))
	if !*apply {
		fmt.Println("DRY RUN: The following folders would be DELETED from Drive (use --apply to execute):")
		for _, f := range orphanFolders {
			fmt.Printf("- %s (ID: %s)\n", f.name, f.id)
		}
		return nil
	}

	if driveUploader == nil {
		return fmt.Errorf("drive uploader not available for apply mode")
	}
	fmt.Println("Deleting orphan folders from Drive...")
	for _, f := range orphanFolders {
		fmt.Printf("Deleting %s (%s)... ", f.name, f.id)
		err := driveUploader.DeleteFolder(ctx, f.id)
		if err != nil {
			fmt.Printf("FAILED: %v\n", err)
		} else {
			fmt.Println("OK")
		}
	}

	fmt.Println("\nCleanup complete.")
	return nil
}

// AGENT-1 note: pre-fix the legacy `media.NewDeletionService` was rebuilt
// here with source-specific repos. Post-fix we reuse root.Maint.DeletionSvc;
// the source discriminator is on the row (column `source`) and the
// canonical DeletionService.DeleteClip handles all sources through the
// single ClipsRepo.
func runDeleteSpecificFolders(args []string) error {
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
	driveUploader := root.Drive.DriveUploader
	deletionSvc := root.Maint.DeletionSvc

	ctx := cmdContext()
	folderIDs := []string{
		"1M7qauleXrKliDsouP4H9Iodl_y2Z0o8-",
		"10hGPV1wqV6a-ZbToDSIHjM8CodJPm3Hg",
		"1OYP_pPMqbGffhxqtE4e0_zzUhviJo_Hu",
		"1FYqGXJfWkgr1MpMUKIa5qujTOr4Ld5x6",
		"1SXl-eitwXLvZBkFyeGIKIJWnZgmH6k5S",
		"1TEvlglo4KU-TrvJXs4_I9N5bDFyp2yO2",
		"17TNgc4l4Kx2zx237EDr5_Iu4bSBrNlNN",
		"1s6tETo-59Dd8LkXGwDiDAxwc0T_ACbfl",
		"1OCgsOYhRHFIGOsW9UGXDDkZcrnyuppkI",
		"1StoxaT_MVM_GIKWT4PrKhmaBj8IZjc_u",
		"1LxcaHzmO8F1fKAMftifytM3xIxiAwSHZ",
		"10FOpE-yHPpo_BQ7VCJB2Hx75xy9m01ea",
		"1Fzy-ofMDePJBpdv9kxs7klb05792ANxk",
		"1UzhMeE8iN4RsgH9GVctMQ_Tm5xHT75se",
		"1Cirzat1wv7qMlyLhb0OV-t9N3zon63Wo",
		"1tiR3aeB4W1cN84SSfb54yeVQrBMCpRWu",
		"1qUNhNJig0gHRKLux8Numlzti5mS5Yx_N",
		"1U3Jgrfa-nMkJDxcw1UjneqYfFRdtmW6W",
		"1geGeH-rxPRRacUtbYa_FA_iH8BGXjY5b",
		"1hqLR1B2bLe9Vc438xzPOMkatc44xqx42",
		"1eR_ehplczPGsuwypd_N1ZTAmN5_4AdvU",
		"1-NNBwwucOD5dsL2wsR4bNWo8HNFAnYND",
		"1DFyMZhweZpn636GpA8SJ_PHweGczb8x6",
		"1132AnWcbsdGjTmoZAWmB_RKdFloJNo2w",
		"1WTVgfxXiEBTPBqFDeRSlOcvY5E7tsoCw",
		"1EKVF3YHDtQU6sMdolBF2OuVnmNdMbhMs",
		"1Ih335jighEGqz27_OSbz_9I6Oe17pPD7",
		"1P7S2D2zNmhNsuSgNlVVNUjFnRq0yQ6Wc",
		"1T1QxQhjNcMkDSXDjDiGZH9mtflPwucQ8",
		"1RO23-aYSECwHUNGoeatMeKzteZ5yFGE-",
		"1mZifx2S2EA0EYiBVJZm5U5gOc-4kiHXz",
		"1tMmvVPLeIQlAXGBJ8q0GWdS92INLFKE6",
		"1acx0qvYlRUxc6FSC1B19RcU4-N3kAJLZ",
		"1HwKXo-szV4BjnkUZAw34I5vgzNfWM83W",
		"1t0SM9N2cp6B-bGhrYoDKQiEi09gxVqIG",
		"1mJxbaMrSr9XUAyKEyMsJhyxnkSaIO3RY",
		"1Qatr7H-NoKiolIFh6SIrhM19nlYWdOPj",
	}

	for _, id := range folderIDs {
		fmt.Printf("Deleting %s... ", id)

		if deletionSvc != nil {
			err := deletionSvc.DeleteClip(ctx, "artlist", id, true)
			if err == nil {
				fmt.Println("OK (Artlist DB)")
				continue
			}

			err = deletionSvc.DeleteClip(ctx, "stock", id, true)
			if err == nil {
				fmt.Println("OK (Stock DB)")
				continue
			}
		}

		if driveUploader == nil {
			fmt.Println("FAILED (drive uploader unavailable)")
			continue
		}
		err := driveUploader.DeleteFolder(ctx, id)
		if err == nil {
			fmt.Println("OK (Drive only)")
		} else {
			fmt.Printf("FAILED: %v\n", err)
		}
	}

	fmt.Println("Done")
	return nil
}

func runSyncAllDrive(args []string) error {
	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	ctx := cmdContext()

	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		log.Fatal("Failed to initialize composition root", zap.Error(err))
	}
	defer rootCleanup()

	fmt.Println("Starting full Google Drive synchronization...")

	if root.Sync != nil && root.Sync.CatalogSync != nil {
		fmt.Println("Syncing catalog (stock, clips, artlist)...")
		summary, err := root.Sync.CatalogSync.SyncAll(ctx)
		if err != nil {
			fmt.Printf("Catalog sync failed: %v\n", err)
		} else {
			fmt.Printf("Catalog sync completed: %d synced, %d failed\n", summary.Synced, summary.Failed)
			for _, item := range summary.Roots {
				fmt.Printf("  - %s: %d synced, %d failed\n", item.Name, item.Synced, item.Failed)
			}
		}
	}

	if root.Domains != nil && root.Domains.VoiceoverSync != nil {
		fmt.Println("Syncing voiceovers...")
		summary, err := root.Domains.VoiceoverSync.Sync(ctx)
		if err != nil {
			fmt.Printf("Voiceover sync failed: %v\n", err)
		} else {
			fmt.Printf("Voiceover sync completed: %d synced, %d failed\n", summary.Synced, summary.Failed)
		}
	}

	if root.Domains != nil && root.Domains.ImageService != nil {
		fmt.Println("Syncing images...")
		if err := root.Domains.ImageService.SyncFromDrive(ctx); err != nil {
			fmt.Printf("Image sync failed: %v\n", err)
		} else {
			fmt.Println("Image sync completed")
		}
	}

	fmt.Println("Synchronization complete!")
	return nil
}

func runTestYouTube(args []string) error {
	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	deps, err := app.WireServices(cfg, log, "")
	if err != nil {
		log.Error("Failed to wire services", zap.Error(err))
		return err
	}

	fmt.Println("Services wired successfully!")
	fmt.Printf("Registry: %v\n", deps.Registry != nil)

	if deps.Lifecycle != nil {
		defer deps.Lifecycle.Stop(context.Background())
	}
	return nil
}
