// cmd/admin/cleanup_orphan_files.go — local filesystem orphan cleanup
package cleanup

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"flag"
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
)

func RunCleanupOrphans(args []string) error {
	fs := flag.NewFlagSet("cleanup-orphans", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apply := fs.Bool("apply", false, "Actually delete orphan files (default: dry-run only)")
	dir := fs.String("dir", "", "Assets directory to scan (default: config Storage.DataDir)")
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

	assetsDir := *dir
	if assetsDir == "" {
		assetsDir = cfg.Storage.DataDir
	}
	if absDir, err := filepath.Abs(assetsDir); err == nil {
		assetsDir = absDir
	}

	deletionSvc := root.Maint.DeletionSvc
	if deletionSvc == nil {
		return fmt.Errorf("deletion service is not available (composition root missing Maint bundle)")
	}

	if *apply {
		fmt.Printf("Starting DEEP ORPHAN CLEANUP in %s (APPLY mode - files WILL be deleted)\n", assetsDir)
	} else {
		fmt.Printf("Starting DEEP ORPHAN CLEANUP in %s (DRY RUN - no files will be deleted)\n", assetsDir)
		fmt.Println("Use --apply to actually delete orphan files")
	}
	fmt.Println()

	ctx := cli.CmdContext()
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
