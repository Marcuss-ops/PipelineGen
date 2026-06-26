// cmd/admin/main.go — admin CLI entry point
//
// CLI entry point for one-shot admin operations (single binary entry,
// invoked as `pipelinegen admin <subcommand>` or directly as `./admin`).
//
// The post-recovery dispatcher honours all subcommands plus the pre-existing fleet:
//
//   - backfill-artlist-media-type (cmd/admin/backfill_artlist_media_type.go)
//   - benchmark                   (cmd/admin/benchmark.go)
//   - cleanup-orphans             (cmd/admin/cleanup.go)
//   - cleanup-all-orphans         (cmd/admin/cleanup.go)
//   - cleanup-artlist-empty-folders (cmd/admin/cleanup.go)
//   - cleanup-stock-orphans       (cmd/admin/cleanup.go)
//   - delete-specific-folders     (cmd/admin/cleanup.go)
//   - dr-qdrant                   (cmd/admin/dr_qdrant.go) — QDRANT-005C PR3:
//     list-snapshots / take-snapshot / restore-snapshot / apply-retention.
//   - list-drive-folder           (cmd/admin/list_drive_folder.go)
//   - reset-video-ai              (cmd/admin/reset_video_ai.go)
//   - sync-all-drive              (cmd/admin/cleanup.go)
//   - test-youtube                (cmd/admin/cleanup.go)
//   - upload-t5pre                (cmd/admin/upload_t5pre.go)
//   - verify-artlist-pipeline     (cmd/admin/verify.go)
//   - seed-channels, stock-reset, stock-subfolders-reset,
//     summarize-book, sync-outros, unify-catalogs,
//     list-styles, backfill-missing, db, gen-api-docs
//     (pre-existing fleet).
//
// The contract enforced by `cmd/admin/admin_test.go::TestAdminCommands_AreRegistered`
// is that every command documented below in `availableCommands` has a
// matching switch arm in the switch block. New subcommands MUST
// appear in BOTH the `availableCommands` list AND the switch.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// availableCommands is the canonical list of subcommands. Tested by
// `TestAdminCommands_AreRegistered` against the live switch block in
// main(). Keep it in lock-step with the switch below.
var availableCommands = []string{
	"backfill-artlist-media-type",
	"backfill-missing",
	"benchmark",
	"clean-qdrant-locators",
	"cleanup-all-orphans",
	"cleanup-artlist-empty-folders",
	"cleanup-orphans",
	"cleanup-stock-orphans",
	"db",
	"delete-specific-folders",
	"dr-qdrant",
	"gen-api-docs",
	"list-drive-folder",
	"list-styles",
	"reindex-qdrant",
	"reconcile-qdrant",
	"reset-video-ai",
	"seed-channels",
	"stock-reset",
	"stock-subfolders-reset",
	"summarize-book",
	"sync-all-drive",
	"sync-outros",
	"test-youtube",
	"unify-catalogs",
	"upload-t5pre",
	"verify-artlist-pipeline",
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	// ── AGENT-1 owned subcommands (cmd/admin/<file>.go) ─────────────
	case "backfill-artlist-media-type":
		err = runBackfillArtlistMediaType(args)
	case "benchmark":
		err = runBenchmark(args)
	case "clean-qdrant-locators":
		err = runCleanLocators(args)
	case "cleanup-all-orphans":
		err = runCleanupAllOrphans(args)
	case "cleanup-artlist-empty-folders":
		err = runCleanupArtlistEmptyFolders(args)
	case "cleanup-orphans":
		err = runCleanupOrphans(args)
	case "cleanup-stock-orphans":
		err = runCleanupStockOrphans(args)
	case "delete-specific-folders":
		err = runDeleteSpecificFolders(args)
	case "list-drive-folder":
		err = runListDriveFolder(args)
	case "reindex-qdrant":
		err = runReindexQdrant(args)
	case "reconcile-qdrant":
		err = runReconcileQdrant(args)
	case "reset-video-ai":
		err = runResetVideoAI(args)
	case "sync-all-drive":
		err = runSyncAllDrive(args)
	case "test-youtube":
		err = runTestYouTube(args)
	case "upload-t5pre":
		err = runUploadT5Pre(args)
	case "verify-artlist-pipeline":
		err = runVerifyArtlistPipeline(args)

	// ── Pre-existing fleet ─────────────────────────────────────────
	case "seed-channels":
		err = runSeedChannels(args)
	case "stock-reset":
		err = runResetStockDrive(args)
	case "stock-subfolders-reset":
		err = runResetStockSubfolders(args)
	case "summarize-book":
		err = runSummarizeBook(args)
	case "sync-outros":
		err = runSyncOutros(args)
	case "unify-catalogs":
		err = runUnifyCatalogs(args)
	case "list-styles":
		err = runListStyles(args) // fallback/default
	case "backfill-missing":
		err = runBackfillMissing(args)
	case "db":
		err = runDB(args)
	case "dr-qdrant":
		err = runDrQdrant(args)
	case "gen-api-docs":
		err = runGenAPIDocs(args)
	default:
		fmt.Printf("Unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Printf("Error running command %s: %v\n", cmd, err)
		os.Exit(1)
	}
}

// printUsage prints the canonical help block listing every documented
// subcommand. Tested by `TestAdminCommands_AreRegistered`.
func printUsage() {
	fmt.Println("Usage: admin <command> [args]")
	fmt.Println("Commands:")
	for _, c := range availableCommands {
		fmt.Printf("  %s\n", c)
	}
}

func cmdContext() context.Context {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		cancel()
	}()
	return ctx
}
