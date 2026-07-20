// cmd/admin/subcommands.go — subcommand registry and dispatcher
//
// CLI dispatcher for one-shot admin operations. The admin binary is
// invoked as `./admin <subcommand>` (or `pipelinegen admin <subcommand>`).
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
//   - zombie-sweep                (cmd/admin/zombie_sweep.go)
//   - dr-qdrant                   (cmd/admin/dr_qdrant.go)
//   - fullimages-migrate          (cmd/admin/fullimages_migrate.go)
//   - list-drive-folder           (cmd/admin/list_drive_folder.go)
//   - reset-video-ai              (cmd/admin/reset_video_ai.go)
//   - sync-all-drive              (cmd/admin/cleanup.go)
//   - test-youtube                (cmd/admin/cleanup.go)
//   - verify-artlist-pipeline     (cmd/admin/verify.go)
//   - stock-reset, stock-subfolders-reset,
//     summarize-book, sync-outros, unify-catalogs,
//     list-styles, backfill-missing, db, gen-api-docs
//     (pre-existing fleet).
//
// The contract enforced by `cmd/admin/admin_test.go::TestAdminCommands_AreRegistered`
// is that every command listed in `availableCommands` has a matching
// switch arm in `dispatchSubcommand`. New subcommands MUST appear in
// BOTH the `availableCommands` list AND the switch.

package main

import (
	"errors"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/reconcile"
)

// errUnknownCommand is the canonical sentinel returned by
// dispatchSubcommand when the subcommand name is not registered.
// Callers (specifically func main) check errors.Is(err, errUnknownCommand)
// to decide whether to print the usage block + exit 1 vs. the standard
// error path.
var errUnknownCommand = errors.New("unknown command")

// availableCommands is the canonical list of subcommands. Tested by
// `TestAdminCommands_AreRegistered` against the live switch block in
// dispatchSubcommand. Keep it in lock-step with the switch below.
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
	"drive-create-folder",
	"folder-path-backfill",
	"drive-doctor",
	"drive-reconcile",
	"download-sound-effects",
	"rename-sound-effects",
	"update-sound-effect-metadata",
	"apply-additional-sound-effects",
	"classify-sound-effects",
	"trim-sound-effects",
	"organize-sound-effects-drive",
	"normalize-sound-effects-drive",
	"organize-foley-drive",
	"trash-drive-files",
	"keep-drive-folder-files",
	"organize-ui-drive",
	"organize-family-drive",
	"index-drive-clip",
	"save-fish-commentary-index",
	"save-cave-commentary-index",
	"save-crab-commentary-index",
	"index-provided-sound-effects",
	"export-sound-effects-metadata",
	"rename-indexed-sound-effects",
	"upload-fish-clip",
	"render-fish-short",
	"render-beluga-short",
	"list-folder-debug",
	"reorganize-cartoon",
	"download-kids-music",
	"upload-drive-file",
	"index-kids-music-metadata",
	"check-indexed-ids",
	"reorganize-and-index-sfx",
	"check-drive-names",
	"list-cartoon-files",
	"check-db-cartoon-files",
	"search-drive",
	"gen-api-docs",
	"list-drive-folder",
	"sync-drive-folder",
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
	"text-tracks-backfill",
	"transcript-cues-backfill",
	"unify-catalogs",
	"backfill-asset-embeddings",
	"backfill-visual-embeddings",
	"fullimages-migrate",
	"verify-artlist-pipeline",
}

// dispatchSubcommand routes `name` (the first argv after the binary
// name) + `args` to the matching subcommand implementation. Each arm
// is a one-line delegation to a run<Name> function defined in the
// per-subcommand file (e.g. runBackfillAssetEmbeddings in
// cmd/admin/backfill_asset_embeddings.go). Returns nil on success;
// returns errUnknownCommand (wrapped) for unmatched names so callers
// can branch on errors.Is for the usage-block exit path.
func dispatchSubcommand(name string, args []string) error {
	switch name {
	// ── AGENT-1 owned subcommands (cmd/admin/<file>.go) ─────────────
	case "backfill-asset-embeddings":
		return runBackfillAssetEmbeddings(args)
	case "backfill-visual-embeddings":
		return runBackfillVisualEmbeddings(args)
	case "benchmark":
		return runBenchmark(args)
	case "zombie-sweep":
		return runZombieSweep(args)
	case "cleanup-all-orphans":
		return runCleanupAllOrphans(args)
	case "cleanup-artlist-empty-folders":
		return runCleanupArtlistEmptyFolders(args)
	case "cleanup-orphans":
		return runCleanupOrphans(args)
	case "cleanup-stock-orphans":
		return runCleanupStockOrphans(args)
	case "delete-specific-folders":
		return runDeleteSpecificFolders(args)
	case "list-drive-folder":
		return runListDriveFolder(args)
	case "sync-drive-folder":
		return runSyncDriveFolder(args)
	case "download-sound-effects":
		return runDownloadSoundEffects(args)
	case "rename-sound-effects":
		return runRenameSoundEffects(args)
	case "update-sound-effect-metadata":
		return runUpdateSoundEffectMetadata(args)
	case "apply-additional-sound-effects":
		return runApplyAdditionalSoundEffects(args)
	case "classify-sound-effects":
		return runClassifySoundEffects(args)
	case "trim-sound-effects":
		return runTrimSoundEffects(args)
	case "organize-sound-effects-drive":
		return runOrganizeSoundEffectsDrive(args)
	case "normalize-sound-effects-drive":
		return runNormalizeSoundEffectsDrive(args)
	case "organize-foley-drive":
		return runOrganizeFoleyDrive(args)
	case "trash-drive-files":
		return runTrashDriveFiles(args)
	case "keep-drive-folder-files":
		return runKeepDriveFolderFiles(args)
	case "organize-ui-drive":
		return runOrganizeUIDrive(args)
	case "organize-family-drive":
		return runOrganizeFamilyDrive(args)
	case "index-drive-clip":
		return runIndexDriveClip(args)
	case "save-fish-commentary-index":
		return runSaveFishCommentaryIndex(args)
	case "save-cave-commentary-index":
		return runSaveCaveCommentaryIndex(args)
	case "save-crab-commentary-index":
		return runSaveCrabCommentaryIndex(args)
	case "index-provided-sound-effects":
		return runIndexProvidedSoundEffects(args)
	case "export-sound-effects-metadata":
		return runExportSoundEffectsMetadata(args)
	case "rename-indexed-sound-effects":
		return runRenameIndexedSoundEffects(args)
	case "upload-fish-clip":
		return runUploadFishClip(args)
	case "render-fish-short":
		return runRenderFishShort(args)
	case "render-beluga-short":
		return runRenderBelugaShort(args)
	case "list-folder-debug":
		return runListFolderDebug(args)
	case "reorganize-cartoon":
		return runReorganizeCartoon(args)
	case "download-kids-music":
		return runDownloadKidsMusic(args)
	case "upload-drive-file":
		return runUploadDriveFile(args)
	case "index-kids-music-metadata":
		return runIndexKidsMusicMetadata(args)
	case "check-indexed-ids":
		return runCheckIndexedIds(args)
	case "reorganize-and-index-sfx":
		return runReorganizeAndIndexSFX(args)
	case "check-drive-names":
		return runCheckDriveNames(args)
	case "list-cartoon-files":
		return runListCartoonFiles(args)
	case "check-db-cartoon-files":
		return runCheckDBCartoonFiles(args)
	case "search-drive":
		return runSearchDrive(args)
	case "reindex-qdrant":
		return reconcile.RunReindexQdrant(args)
	case "qdrant-maintenance":
		return runQdrantMaintenance(args)
	case "reconcile-qdrant":
		return reconcile.RunReconcileQdrant(args)
	case "reset-video-ai":
		return runResetVideoAI(args)
	case "qdrant-readiness":
		return runQdrantReadiness(args)
	case "qdrant-preflight":
		return runQdrantPreflight(args)
	case "sync-all-drive":
		return runSyncAllDrive(args)
	case "test-youtube":
		return runTestYouTube(args)
	case "text-tracks-backfill":
		return runTextTracksBackfill(args)
	case "transcript-cues-backfill":
		return runTranscriptCuesBackfill(args)
	case "verify-artlist-pipeline":
		return runVerifyArtlistPipeline(args)

	// ── Pre-existing fleet ─────────────────────────────────────────
	case "stock-reset":
		return runResetStockDrive(args)
	case "stock-subfolders-reset":
		return runResetStockSubfolders(args)
	case "summarize-book":
		return runSummarizeBook(args)
	case "sync-outros":
		return runSyncOutros(args)
	case "unify-catalogs":
		return runUnifyCatalogs(args)
	case "list-styles":
		return runListStyles(args) // fallback/default
	case "backfill-missing":
		return runBackfillMissing(args)
	case "db":
		return runDB(args)
	case "dr-qdrant":
		return reconcile.RunDrQdrant(args)
	case "fullimages-migrate":
		return runFullImagesMigrate(args)
	case "drive-bootstrap":
		return runDriveBootstrap(args)
	case "drive-create-folder":
		return runDriveCreateFolder(args)
	case "folder-path-backfill":
		return runFolderPathBackfill(args)
	case "drive-doctor":
		return runDriveDoctor(args)
	case "drive-reconcile":
		return runDriveReconcile(args)
	case "gen-api-docs":
		return runGenAPIDocs(args)
	default:
		return fmt.Errorf("%w: %s", errUnknownCommand, name)
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
