package maintenance

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"flag"
	"fmt"
	"os"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
)

const stockRootFolder = "1wt4hqmHD5qEsNhpUUBszlRkSHhyFgtGh"

// keepFolders are the only folders to preserve on Drive.
var keepFolders = map[string]bool{
	"HipHop": true, "Discovery": true, "Wwe": true,
	"Boxing": true, "Music": true, "Crime": true,
}

func recordStockResetError(first *error, operation string, err error) {
	if err == nil || first == nil || *first != nil {
		return
	}
	*first = fmt.Errorf("reset-stock-drive: %s: %w", operation, err)
}

func RunResetStockDrive(args []string) error {
	fs := flag.NewFlagSet("reset-stock-drive", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apply := fs.Bool("apply", false, "Actually delete and recreate (default: dry-run only)")
	folder := fs.String("parent", stockRootFolder, "Stock root folder ID on Drive")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	root, _, coreCleanup, err := wiring.InitComposition(cfg, log)
	if err != nil {
		log.Fatal("Failed to initialize core services", zap.Error(err))
	}
	defer coreCleanup()

	if root.Drive == nil || root.Drive.Admin == nil || root.Drive.Reader == nil {
		return fmt.Errorf("drive admin/reader ports are not available")
	}
	driveAdmin := root.Drive.Admin
	driveReader := root.Drive.Reader

	ctx := context.Background()

	// 1. List all children of stock root folder
	fmt.Printf("=== Scanning root folder %s ===\n", *folder)
	query := fmt.Sprintf("'%s' in parents and trashed = false", *folder)
	list, err := driveReader.SearchFiles(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to list drive folder: %w", err)
	}

	fmt.Printf("Found %d items on Drive.\n", len(list))
	for _, f := range list {
		fmt.Printf("  %s (%s) [%s]\n", f.Name, f.ID, f.MimeType)
	}

	if !*apply {
		fmt.Println("\nDRY RUN. Use --apply to delete non-kept items and create missing folders.")
		return nil
	}

	var firstErr error

	// 2. Delete only items NOT in keepFolders
	fmt.Println("\n=== Deleting non-kept items from Drive ===")
	for _, f := range list {
		if keepFolders[f.Name] {
			fmt.Printf("  KEPT: %s (%s)\n", f.Name, f.ID)
			continue
		}
		fmt.Printf("  Deleting %s (%s)... ", f.Name, f.ID)
		err := driveAdmin.DeleteFolder(ctx, f.ID)
		if err != nil {
			fmt.Printf("FAILED: %v\n", err)
			recordStockResetError(&firstErr, fmt.Sprintf("delete Drive folder %s", f.ID), err)
		} else {
			fmt.Println("OK")
		}
	}

	// 3. Clean up database records
	fmt.Println("\n=== Cleaning up database records ===")
	mediaDB := root.DB.DB
	veloxDB := root.DB.DB

	if mediaDB != nil {
		for _, table := range []string{"media_assets", "clip_folders"} {
			res, err := mediaDB.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE source = 'stock'", table))
			if err != nil {
				fmt.Printf("  Failed to clean %s: %v\n", table, err)
				recordStockResetError(&firstErr, fmt.Sprintf("clean media.%s", table), err)
				continue
			}
			n, err := res.RowsAffected()
			if err != nil {
				fmt.Printf("  Failed to read affected rows for media.%s: %v\n", table, err)
				recordStockResetError(&firstErr, fmt.Sprintf("read affected rows for media.%s", table), err)
				continue
			}
			fmt.Printf("  Deleted %d rows from media.%s\n", n, table)
		}

		res, err := mediaDB.ExecContext(ctx, "DELETE FROM media_assets WHERE source = 'nvidia-animation'")
		if err != nil {
			fmt.Printf("  Failed to clean nvidia-animation: %v\n", err)
			recordStockResetError(&firstErr, "clean nvidia-animation media assets", err)
		} else {
			n, err := res.RowsAffected()
			if err != nil {
				fmt.Printf("  Failed to read affected rows for nvidia-animation: %v\n", err)
				recordStockResetError(&firstErr, "read affected rows for nvidia-animation", err)
			} else {
				fmt.Printf("  Deleted %d nvidia-animation rows from media_assets\n", n)
			}
		}
	}

	if veloxDB != nil {
		if _, err := veloxDB.ExecContext(ctx, "DELETE FROM asset_links WHERE asset_id IN (SELECT asset_id FROM asset_index WHERE source = 'stock')"); err != nil {
			fmt.Printf("  Failed to clean asset_links: %v\n", err)
			recordStockResetError(&firstErr, "clean asset links", err)
		}
		res, err := veloxDB.ExecContext(ctx, "DELETE FROM asset_index WHERE source = 'stock'")
		if err != nil {
			fmt.Printf("  Failed to clean asset_index: %v\n", err)
			recordStockResetError(&firstErr, "clean asset index", err)
		} else {
			n, err := res.RowsAffected()
			if err != nil {
				fmt.Printf("  Failed to read affected rows for asset_index: %v\n", err)
				recordStockResetError(&firstErr, "read affected rows for asset index", err)
			} else {
				fmt.Printf("  Deleted %d rows from velox.asset_index\n", n)
			}
		}

		res, err = veloxDB.ExecContext(ctx, "DELETE FROM script_stock_matches")
		if err != nil {
			fmt.Printf("  Failed to clean script_stock_matches: %v\n", err)
			recordStockResetError(&firstErr, "clean script stock matches", err)
		} else {
			n, err := res.RowsAffected()
			if err != nil {
				fmt.Printf("  Failed to read affected rows for script_stock_matches: %v\n", err)
				recordStockResetError(&firstErr, "read affected rows for script stock matches", err)
			} else {
				fmt.Printf("  Deleted %d rows from velox.script_stock_matches\n", n)
			}
		}
	}

	// 4. Create missing folders on Drive
	fmt.Println("\n=== Creating missing stock folders ===")
	for name := range keepFolders {
		fmt.Printf("  Checking %s... ", name)
		id, err := driveAdmin.GetOrCreateFolder(ctx, name, *folder)
		if err != nil {
			fmt.Printf("FAILED: %v\n", err)
			recordStockResetError(&firstErr, fmt.Sprintf("create Drive folder %s", name), err)
		} else {
			fmt.Printf("OK -> %s\n", id)
		}
	}

	if firstErr != nil {
		fmt.Printf("\n❌ Stock drive reset completed with errors: %v\n", firstErr)
		return firstErr
	}
	fmt.Println("\n✅ Stock drive reset complete!")
	return nil
}
