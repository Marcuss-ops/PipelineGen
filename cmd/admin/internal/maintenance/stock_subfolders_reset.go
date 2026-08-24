package maintenance

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"flag"
	"fmt"
	"os"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
)

// foldersToReset maps folder name → its current Drive ID (to delete).
var foldersToReset = map[string]string{
	"HipHop":    "11-O6LvlcL0Hj_ktiUOJDnpPYerSpWNiW",
	"Wwe":       "1_7U8yEeQZEH7vxgDIRketFL85F96O_Ws",
	"Discovery": "16D3qvbv3Y4TlNahQ3sWq6N7ITgwWm6DD",
}

func RunResetStockSubfolders(args []string) error {
	fs := flag.NewFlagSet("reset-stock-subfolders", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apply := fs.Bool("apply", false, "Actually delete and recreate (default: dry-run only)")
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

	if root.Drive == nil || root.Drive.Admin == nil {
		return fmt.Errorf("drive admin port is not available")
	}
	driveAdmin := root.Drive.Admin

	ctx := context.Background()

	fmt.Println("=== Folders to reset ===")
	for name, id := range foldersToReset {
		fmt.Printf("  %s: https://drive.google.com/drive/folders/%s\n", name, id)
	}

	if !*apply {
		fmt.Println("\nDRY RUN. Use --apply to delete and recreate.")
		return nil
	}

	// 1. Delete each folder
	fmt.Println("\n=== Deleting folders from Drive ===")
	for name, id := range foldersToReset {
		fmt.Printf("  Deleting %s (%s)... ", name, id)
		if err := driveAdmin.DeleteFolder(ctx, id); err != nil {
			fmt.Printf("FAILED: %v\n", err)
		} else {
			fmt.Println("OK")
		}
	}

	// 2. Clean database records for these specific folders
	fmt.Println("\n=== Cleaning database records ===")
	mediaDB := root.DB.DB
	if mediaDB != nil {
		for name := range foldersToReset {
			res, err := mediaDB.ExecContext(ctx,
				"DELETE FROM clip_folders WHERE source = 'stock' AND folder_path = ?", name)
			if err != nil {
				fmt.Printf("  Failed to clean clip_folders for %s: %v\n", name, err)
			} else {
				n, _ := res.RowsAffected()
				if n > 0 {
					fmt.Printf("  Deleted %d clip_folders rows for %s\n", n, name)
				}
			}
		}
	}

	// 3. Recreate empty folders under stock root
	fmt.Println("\n=== Recreating folders ===")
	for name := range foldersToReset {
		fmt.Printf("  Creating %s under stock root... ", name)
		id, err := driveAdmin.GetOrCreateFolder(ctx, name, stockRootFolder)
		if err != nil {
			fmt.Printf("FAILED: %v\n", err)
		} else {
			fmt.Printf("OK -> https://drive.google.com/drive/folders/%s\n", id)
		}
	}

	fmt.Println("\nDone!")
	return nil
}
