// cmd/admin/main.go — admin CLI entry point
//
// CLI entry point for one-shot admin operations (single binary entry,
// invoked as `pipelinegen admin <subcommand>` or directly as `./admin`).
//
// The post-recovery dispatcher honours all subcommands plus the pre-existing fleet:
//
//   - benchmark                   (cmd/admin/benchmark.go)
//   - cleanup-orphans             (cmd/admin/cleanup.go)
//   - cleanup-all-orphans         (cmd/admin/cleanup.go)
//   - cleanup-artlist-empty-folders (cmd/admin/cleanup.go)
//   - qdrant-maintenance          (cmd/admin/qdrant_maintenance.go) — Issue 12:
//     unified qdrant maintenance with 3 modes: audit (classify all 8 categories),
//     repair-locators (strip drive_link/local_path keys), delete-invalid
//     (outbox-delete non-locator assets). Replaces clean-qdrant-locators (QDRANT-005)
//     and cleanup-qdrant-legacy (PR 14).
//   - cleanup-stock-orphans       (cmd/admin/cleanup.go)
//   - delete-specific-folders     (cmd/admin/cleanup.go)
//   - zombie-sweep                (cmd/admin/zombie_sweep.go) — JOBS-T01-ZOMBIE-SWEEP:
//     operator CLI subcommand for sweeping zombie jobs (status IN
//     (LEASED, RUNNING, FINALIZING) AND lease_expiry < cutoff).
//     Wraps the canonical (*SQLiteStore).MarkRunningJobsOlderThanFailed
//     (internal/infrastructure/database/sqlite/jobs/repository_lifecycle.go)
//     per godlike/06 one-canonical-owner-per-fact. --dry-run is the
//     default; --apply is the operator-explicit opt-in.
//   - dr-qdrant                   (cmd/admin/dr_qdrant.go) — QDRANT-005C PR3:
//     list-snapshots / take-snapshot / restore-snapshot / apply-retention.
//   - fullimages-migrate          (cmd/admin/fullimages_migrate.go) — PR-LEGACY-CLEANUP-FULLIMAGES-DOWNSTREAM-MIGRATION:
//     operator-facing migration CLI for the fullimages video→image
//     rename (LEGACY-CLEANUP-5-ITEM-ORCHESTRATION Item 5, Option B
//     verdict, shipped 2026-07-10). Default = dry-run; --apply writes
//     the canonical text replacements (URL, JSON, Go type/field/method)
//     to disk. Hermetic: pure filesystem scan, no live-stack dependency.
//   - list-drive-folder           (cmd/admin/list_drive_folder.go)
//   - reset-video-ai              (cmd/admin/reset_video_ai.go)
//   - sync-all-drive              (cmd/admin/cleanup.go)
//   - test-youtube                (cmd/admin/cleanup.go)
//   - verify-artlist-pipeline     (cmd/admin/verify.go)
//   - stock-reset, stock-subfolders-reset,
//     summarize-book, sync-outros, unify-catalogs,
//     list-styles, backfill-missing, db, gen-api-docs
//     (pre-existing fleet).
//   - backfill-monitored-sources-to-category-channels
//     (cmd/admin/backfill_monitored_sources_to_category_channels.go — Wave
//     CONFORMANCE-001 BACKFILL step into the canonical SoT; not part of
//     availableCommands because it is a one-shot migration, not an active
//     operator workflow).
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
	"backfill-missing",
	"benchmark",
	"zombie-sweep",
	"cleanup-all-orphans",
	"cleanup-artlist-empty-folders",
	"cleanup-orphans",
	"cleanup-stock-orphans",
	"db",
	"delete-specific-folders",
	"dr-qdrant",
	"drive-bootstrap",
	"drive-doctor",
	"drive-reconcile",
	"gen-api-docs",
	"list-drive-folder",
	"list-styles",
	"reindex-qdrant",
	"reconcile-qdrant",
	"reset-video-ai",
	"qdrant-maintenance",
	"qdrant-readiness",
	"qdrant-preflight",
	"stock-reset",
	"stock-subfolders-reset",
	"summarize-book",
	"sync-all-drive",
	"sync-outros",
	"test-youtube",
	"unify-catalogs",
	"backfill-asset-embeddings",
	"backfill-visual-embeddings",
	"fullimages-migrate",
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
	case "backfill-asset-embeddings":
		err = runBackfillAssetEmbeddings(args)
	case "backfill-visual-embeddings":
		err = runBackfillVisualEmbeddings(args)
	case "benchmark":
		err = runBenchmark(args)
	case "zombie-sweep":
		err = runZombieSweep(args)
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
	case "qdrant-maintenance":
		err = runQdrantMaintenance(args)
	case "reconcile-qdrant":
		err = runReconcileQdrant(args)
	case "reset-video-ai":
		err = runResetVideoAI(args)
	case "qdrant-readiness":
		err = runQdrantReadiness(args)
	case "qdrant-preflight":
		err = runQdrantPreflight(args)
	case "sync-all-drive":
		err = runSyncAllDrive(args)
	case "test-youtube":
		err = runTestYouTube(args)
	case "verify-artlist-pipeline":
		err = runVerifyArtlistPipeline(args)

	// ── Pre-existing fleet ─────────────────────────────────────────
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
	case "fullimages-migrate":
		err = runFullImagesMigrate(args)
	case "drive-bootstrap":
		err = runDriveBootstrap(args)
	case "drive-doctor":
		err = runDriveDoctor(args)
	case "drive-reconcile":
		err = runDriveReconcile(args)
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
	// AGENTS.md §7 post-write save ctx — admin CLI composition root;
	// same rationale as cmd/worker/main.go — admin is a one-shot binary
	// whose lifetime is bounded by the operator invocation.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		cancel()
	}()
	return ctx
}
