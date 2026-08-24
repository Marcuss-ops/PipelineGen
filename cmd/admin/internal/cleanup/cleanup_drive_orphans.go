// cmd/admin/cleanup_drive_orphans.go — Drive folder orphan cleanup subcommands
//
// Consolidates the 3 orphan-folder scan-and-delete subcommands:
//   - runCleanupAllOrphans: multi-source Drive folder orphan scan
//   - runCleanupArtlistEmptyFolders: Artlist-specific Drive orphan scan
//   - runCleanupStockOrphans: Stock-specific Drive orphan scan
//
// All three share the same pattern: scan Drive → query media_assets in DB → delete orphans.
package cleanup

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"flag"
	"fmt"
	"os"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
)

func RunCleanupAllOrphans(args []string) error {
	fs := flag.NewFlagSet("cleanup-all-orphans", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apply := fs.Bool("apply", false, "Actually delete folders (default: dry-run only)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	root, _, rootCleanup, err := wiring.InitComposition(cfg, log)
	if err != nil {
		log.Fatal("Failed to initialize composition root", zap.Error(err))
	}
	defer rootCleanup()

	if root.Drive == nil || root.Drive.Reader == nil {
		return fmt.Errorf("drive reader port is not available")
	}
	driveReader := root.Drive.Reader
	driveAdmin := root.Drive.Admin

	targets := []struct {
		name     string
		rootID   string
		dbPrefix string
	}{
		{"Artlist", "1OAAf5dawAppdopsgCq1yHFGPUXCI9Vbk", "artlist"},
		{"Stock", "1wt4hqmHD5qEsNhpUUBszlRkSHhyFgtGh", "stock"},
		{"YouTube Clips", "1r4B_m3Gz_5f2f5O-vNqG6_G8_G8_G8_G", "clips"},
	}

	ctx := cli.CmdContext()
	for _, t := range targets {
		if t.rootID == "" || t.rootID == "1r4B_m3Gz_5f2f5O-vNqG6_G8_G8_G8_G" {
			fmt.Printf("\n--- Skipping %s: Root ID not configured or placeholder ---\n", t.name)
			continue
		}

		fmt.Printf("\n--- Checking %s (Root: %s) ---\n", t.name, t.rootID)
		query := fmt.Sprintf("'%s' in parents and mimeType = 'application/vnd.google-apps.folder' and trashed = false", t.rootID)
		list, err := driveReader.SearchFiles(ctx, query)
		if err != nil {
			fmt.Printf("Error listing %s: %v\n", t.name, err)
			continue
		}

		fmt.Printf("Found %d folders on Drive.\n", len(list))

		var orphans []struct{ id, name string }
		for _, f := range list {
			var dummy int
			var dbErr error
			switch t.dbPrefix {
			case "artlist", "stock", "clips":
				dbErr = root.DB.DB.QueryRowContext(ctx, "SELECT 1 FROM media_assets WHERE id = ?", f.ID).Scan(&dummy)
			}

			if dbErr != nil {
				orphans = append(orphans, struct{ id, name string }{f.ID, f.Name})
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
			if driveAdmin == nil {
				fmt.Println("SKIPPED (no drive uploader)")
				continue
			}
			err := driveAdmin.DeleteFolder(ctx, f.id)
			if err != nil {
				fmt.Printf("FAILED: %v\n", err)
			} else {
				fmt.Println("OK")
			}
		}
	}

	return nil
}

func RunCleanupArtlistEmptyFolders(args []string) error {
	fs := flag.NewFlagSet("cleanup-artlist-empty-folders", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apply := fs.Bool("apply", false, "Actually delete folders (default: dry-run only)")
	parentID := fs.String("parent", "1OAAf5dawAppdopsgCq1yHFGPUXCI9Vbk", "The Artlist root folder ID to scan on Drive")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	root, _, rootCleanup, err := wiring.InitComposition(cfg, log)
	if err != nil {
		log.Fatal("Failed to initialize composition root", zap.Error(err))
	}
	defer rootCleanup()

	if root.Drive == nil || root.Drive.Reader == nil {
		return fmt.Errorf("drive reader port is not available")
	}
	driveReader := root.Drive.Reader
	driveAdmin := root.Drive.Admin

	ctx := cli.CmdContext()
	fmt.Printf("Scanning Drive folder: %s\n", *parentID)
	query := fmt.Sprintf("'%s' in parents and mimeType = 'application/vnd.google-apps.folder' and trashed = false", *parentID)

	list, err := driveReader.SearchFiles(ctx, query)
	if err != nil {
		log.Fatal("Failed to list folders on Drive", zap.Error(err))
	}

	fmt.Printf("Found %d folders on Drive.\n", len(list))

	var orphanFolders []struct{ id, name string }
	for _, f := range list {
		var dummy int
		err := root.DB.DB.QueryRowContext(ctx, "SELECT 1 FROM media_assets WHERE id = ? AND json_extract(COALESCE(metadata_json,'{}'), '$.is_folder') = 1", f.ID).Scan(&dummy)
		if err != nil {
			orphanFolders = append(orphanFolders, struct{ id, name string }{f.ID, f.Name})
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

	if driveAdmin == nil {
		return fmt.Errorf("drive uploader not available for apply mode")
	}
	fmt.Println("Deleting orphan folders from Drive...")
	for _, f := range orphanFolders {
		fmt.Printf("Deleting %s (%s)... ", f.name, f.id)
		err := driveAdmin.DeleteFolder(ctx, f.id)
		if err != nil {
			fmt.Printf("FAILED: %v\n", err)
		} else {
			fmt.Println("OK")
		}
	}

	fmt.Println("\nCleanup complete.")
	return nil
}

func RunCleanupStockOrphans(args []string) error {
	fs := flag.NewFlagSet("cleanup-stock-orphans", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apply := fs.Bool("apply", false, "Actually delete folders (default: dry-run only)")
	parentID := fs.String("parent", "1wt4hqmHD5qEsNhpUUBszlRkSHhyFgtGh", "The Stock root folder ID to scan on Drive")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	root, _, rootCleanup, err := wiring.InitComposition(cfg, log)
	if err != nil {
		log.Fatal("Failed to initialize composition root", zap.Error(err))
	}
	defer rootCleanup()

	if root.Drive == nil || root.Drive.Reader == nil {
		return fmt.Errorf("drive reader port is not available")
	}
	driveReader := root.Drive.Reader
	driveAdmin := root.Drive.Admin

	ctx := cli.CmdContext()
	fmt.Printf("Scanning Drive folder: %s\n", *parentID)
	query := fmt.Sprintf("'%s' in parents and mimeType = 'application/vnd.google-apps.folder' and trashed = false", *parentID)

	list, err := driveReader.SearchFiles(ctx, query)
	if err != nil {
		log.Fatal("Failed to list folders on Drive", zap.Error(err))
	}

	fmt.Printf("Found %d folders on Drive.\n", len(list))

	var orphanFolders []struct{ id, name string }
	for _, f := range list {
		var dummy int
		err := root.DB.DB.QueryRowContext(ctx, "SELECT 1 FROM media_assets WHERE id = ? AND json_extract(COALESCE(metadata_json,'{}'), '$.is_folder') = 1", f.ID).Scan(&dummy)
		if err != nil {
			orphanFolders = append(orphanFolders, struct{ id, name string }{f.ID, f.Name})
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

	if driveAdmin == nil {
		return fmt.Errorf("drive uploader not available for apply mode")
	}
	fmt.Println("Deleting orphan folders from Drive...")
	for _, f := range orphanFolders {
		fmt.Printf("Deleting %s (%s)... ", f.name, f.id)
		err := driveAdmin.DeleteFolder(ctx, f.id)
		if err != nil {
			fmt.Printf("FAILED: %v\n", err)
		} else {
			fmt.Println("OK")
		}
	}

	fmt.Println("\nCleanup complete.")
	return nil
}
