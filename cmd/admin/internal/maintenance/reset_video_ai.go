// cmd/admin/reset_video_ai.go — recreate video AI Drive folder
//
// Recreates the stock "video ai" sub-folder on Google Drive and registers
// an entry in the canonical `clip_folders` SQLite table.
//
// Post-fix wiring:
//
//   - app.ExportInitCoreMinimal was removed in PR4d-final; we use
//     wiring.InitComposition(cfg, log) to obtain *ComposeRoot. The
//     Drive client and Uploader are reached through
//     root.Drive.DriveClient / root.Drive.DriveUploader.
//   - The Drive is reached through the canonical Pattern 0 ports
//     (drive.Admin + drive.Reader); raw driveapi.File calls are no
//     longer needed at this layer (Wave C, June 2026).
package maintenance

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

const videoAIFolderName = "video ai"

func RunResetVideoAI(args []string) error {
	fs := flag.NewFlagSet("reset-video-ai", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apply := fs.Bool("apply", false, "Actually delete and create (default: dry-run only)")
	sourceFolder := fs.String("folder", "1kr8c1KZmUus10mkIdqJlYqAzXDyoNZeY", "Source Drive folder ID to clear")
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

	if root.Drive == nil || root.Drive.Reader == nil || root.Drive.Admin == nil || root.Drive.Lifecycle == nil {
		return fmt.Errorf("drive admin/reader/lifecycle ports are not available")
	}

	ctx := cli.CmdContext()
	driveReader := root.Drive.Reader
	driveAdmin := root.Drive.Admin
	driveLifecycle := root.Drive.Lifecycle
	stockRootFolder := cfg.Drive.RootFolder()

	// Step 1: List and delete all items in the source folder
	fmt.Printf("📂 Source folder: %s\n", *sourceFolder)
	query := fmt.Sprintf("'%s' in parents and trashed = false", *sourceFolder)
	list, err := driveReader.SearchFiles(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to list folder: %w", err)
	}

	fmt.Printf("Found %d items to delete:\n", len(list))
	for _, f := range list {
		fmt.Printf("  🗑  %s (%s) [%s]\n", f.Name, f.ID, f.MimeType)
	}

	if *apply && len(list) > 0 {
		fmt.Println("\nDeleting items...")
		for _, f := range list {
			if err := driveLifecycle.Delete(ctx, f.ID); err != nil {
				log.Warn("Failed to delete file", zap.String("name", f.Name), zap.String("id", f.ID), zap.Error(err))
			} else {
				fmt.Printf("  ✅ Deleted: %s\n", f.Name)
			}
		}
	}

	// Step 2: Create "video ai" folder on Drive under stock root
	var videoAIFolderID string
	if *apply {
		videoAIFolderID, err = driveAdmin.GetOrCreateFolder(ctx, videoAIFolderName, stockRootFolder)
		if err != nil {
			return fmt.Errorf("failed to create video ai folder: %w", err)
		}
		fmt.Printf("✅ Created Drive folder: %s (%s)\n", videoAIFolderName, videoAIFolderID)
	} else {
		fmt.Printf("\n📁 Would create Drive folder: %s under %s\n", videoAIFolderName, stockRootFolder)
	}

	// Step 3: Create DB entry in clip_folders
	if *apply {
		if err := createClipFolderEntry(ctx, root, videoAIFolderID); err != nil {
			return fmt.Errorf("failed to create DB entry: %w", err)
		}
		fmt.Printf("✅ Created DB entry: clipfolder_stock_video-ai\n")
	} else {
		fmt.Printf("📁 Would create DB entry: source=stock, group_name=%s\n", videoAIFolderName)
	}

	fmt.Println("\nDone!")
	return nil
}

// createClipFolderEntry upserts the canonical `clip_folders` row for the
// "video ai" sub-folder.
//
// MEDIA-SSOT (POSTGRES-MEDIA-CUTOVER): the raw `clip_folders` INSERT OR
// REPLACE is gone — the canonical folder repository owns the operational row
// AND mirrors it into the PostgreSQL folder projection.
func createClipFolderEntry(ctx context.Context, root *wiring.ComposeRoot, folderID string) error {
	if root == nil || root.Repos == nil || root.Repos.ClipsRepo == nil {
		return fmt.Errorf("reset-video-ai: canonical clips repository is required")
	}
	now := time.Now().UTC()
	return root.Repos.ClipsRepo.UpsertFolder(ctx, &detail.ClipFolder{
		ID:         "clipfolder_stock_video-ai",
		Source:     "stock",
		FolderID:   folderID,
		FolderPath: videoAIFolderName,
		Group:      videoAIFolderName,
		Metadata:   "{}",
		CreatedAt:  now,
		UpdatedAt:  now,
	})
}
