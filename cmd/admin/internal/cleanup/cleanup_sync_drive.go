// cmd/admin/cleanup_sync_drive.go — full Google Drive synchronization
package cleanup

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
)

func RunSyncAllDrive(args []string) error {
	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	ctx := cli.CmdContext()

	root, _, rootCleanup, err := wiring.InitComposition(cfg, log)
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
