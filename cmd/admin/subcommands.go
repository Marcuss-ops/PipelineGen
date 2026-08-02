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
	"sort"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/reconcile"
)

// errUnknownCommand is the canonical sentinel returned by
// dispatchSubcommand when the subcommand name is not registered.
// Callers (specifically func main) check errors.Is(err, errUnknownCommand)
// to decide whether to print the usage block + exit 1 vs. the standard
// error path.
var errUnknownCommand = errors.New("unknown command")

// availableCommands is derived from commandRegistry and sorted for stable
// help output. The registry is the only dispatch source of truth.
type commandHandler func([]string) error

var commandRegistry = map[string]commandHandler{
	"apply-asset-metadata":           runApplyAssetMetadata,
	"audit-google-doc-links":         runAuditGoogleDocLinks,
	"apply-asset-metadata-batch":     runApplyAssetMetadataBatch,
	"apply-additional-sound-effects": runApplyAdditionalSoundEffects,
	"backfill-asset-embeddings":      runBackfillAssetEmbeddings,
	"backfill-media-durations":       runBackfillMediaDurations,
	"backfill-missing":               runBackfillMissing,
	"backfill-visual-embeddings":     runBackfillVisualEmbeddings,
	"benchmark":                      runBenchmark,
	"check-drive-names":              runCheckDriveNames,
	"check-indexed-ids":              runCheckIndexedIds,
	"classify-sound-effects":         runClassifySoundEffects,
	"cleanup-all-orphans":            runCleanupAllOrphans,
	"cleanup-artlist-empty-folders":  runCleanupArtlistEmptyFolders,
	"cleanup-orphans":                runCleanupOrphans,
	"cleanup-stock-orphans":          runCleanupStockOrphans,
	"db":                             runDB,
	"delete-specific-folders":        runDeleteSpecificFolders,
	"download-sound-effects":         runDownloadSoundEffects,
	"drive-bootstrap":                runDriveBootstrap,
	"drive-create-folder":            runDriveCreateFolder,
	"drive-doctor":                   runDriveDoctor,
	"drive-reconcile":                runDriveReconcile,
	"dr-qdrant":                      reconcile.RunDrQdrant,
	"export-sound-effects-metadata":  runExportSoundEffectsMetadata,
	"folder-path-backfill":           runFolderPathBackfill,
	"fullimages-migrate":             runFullImagesMigrate,
	"gen-api-docs":                   runGenAPIDocs,
	"index-drive-clip":               runIndexDriveClip,
	"index-provided-sound-effects":   runIndexProvidedSoundEffects,
	"keep-drive-folder-files":        runKeepDriveFolderFiles,
	"list-drive-folder":              runListDriveFolder,
	"list-styles":                    runListStyles,
	"normalize-sound-effects-drive":  runNormalizeSoundEffectsDrive,
	"organize-drive-folder":          runOrganizeDriveFolder,
	"organize-foley-drive":           runOrganizeFoleyDrive,
	"organize-sound-effects-drive":   runOrganizeSoundEffectsDrive,
	"qdrant-maintenance":             runQdrantMaintenance,
	"qdrant-preflight":               runQdrantPreflight,
	"qdrant-readiness":               runQdrantReadiness,
	"reconcile-qdrant":               reconcile.RunReconcileQdrant,
	"remove-drive-folder-recursive":  runRemoveDriveFolderRecursive,
	"repair-drive-links":             runRepairDriveLinks,
	"reindex-qdrant":                 reconcile.RunReindexQdrant,
	"rename-indexed-sound-effects":   runRenameIndexedSoundEffects,
	"rename-sound-effects":           runRenameSoundEffects,
	"render-short":                   runRenderShort,
	"reorganize-and-index-sfx":       runReorganizeAndIndexSFX,
	"reset-video-ai":                 runResetVideoAI,
	"search-drive":                   runSearchDrive,
	"stock-reset":                    runResetStockDrive,
	"stock-subfolders-reset":         runResetStockSubfolders,
	"summarize-book":                 runSummarizeBook,
	"sync-all-drive":                 runSyncAllDrive,
	"sync-drive-folder":              runSyncDriveFolder,
	"sync-outros":                    runSyncOutros,
	"test-youtube":                   runTestYouTube,
	"text-tracks-backfill":           runTextTracksBackfill,
	"transcript-cues-backfill":       runTranscriptCuesBackfill,
	"trash-drive-files":              runTrashDriveFiles,
	"trim-sound-effects":             runTrimSoundEffects,
	"unify-catalogs":                 runUnifyCatalogs,
	"update-sound-effect-metadata":   runUpdateSoundEffectMetadata,
	"upload-drive-file":              runUploadDriveFile,
	"verify-artlist-pipeline":        runVerifyArtlistPipeline,
	"zombie-sweep":                   runZombieSweep,
}

var availableCommands = commandNames()

func commandNames() []string {
	names := make([]string, 0, len(commandRegistry))
	for name := range commandRegistry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// dispatchSubcommand routes `name` (the first argv after the binary
// name) + `args` to the matching subcommand implementation. Each arm
// is a one-line delegation to a run<Name> function defined in the
// per-subcommand file (e.g. runBackfillAssetEmbeddings in
// cmd/admin/backfill_asset_embeddings.go). Returns nil on success;
// returns errUnknownCommand (wrapped) for unmatched names so callers
// can branch on errors.Is for the usage-block exit path.
func dispatchSubcommand(name string, args []string) error {
	handler, ok := commandRegistry[name]
	if !ok {
		return fmt.Errorf("%w: %s", errUnknownCommand, name)
	}
	return handler(args)
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
