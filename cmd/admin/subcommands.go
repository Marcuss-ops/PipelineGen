// cmd/admin/subcommands.go — canonical subcommand registry and dispatcher.
//
// Each command is declared exactly once in subcommandRegistry. The public
// command list, dispatch index and usage output are derived from that registry,
// preventing the historical list/switch drift.
package main

import (
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/reconcile"
)

var errUnknownCommand = errors.New("unknown command")

type subcommandHandler func([]string) error

type subcommandEntry struct {
	name string
	run  subcommandHandler
}

var subcommandRegistry = []subcommandEntry{
	{name: "backfill-missing", run: runBackfillMissing},
	{name: "benchmark", run: runBenchmark},
	{name: "zombie-sweep", run: runZombieSweep},
	{name: "cleanup-all-orphans", run: runCleanupAllOrphans},
	{name: "cleanup-artlist-empty-folders", run: runCleanupArtlistEmptyFolders},
	{name: "cleanup-orphans", run: runCleanupOrphans},
	{name: "cleanup-stock-orphans", run: runCleanupStockOrphans},
	{name: "db", run: runDB},
	{name: "delete-specific-folders", run: runDeleteSpecificFolders},
	{name: "dr-qdrant", run: reconcile.RunDrQdrant},
	{name: "drive-bootstrap", run: runDriveBootstrap},
	{name: "drive-create-folder", run: runDriveCreateFolder},
	{name: "folder-path-backfill", run: runFolderPathBackfill},
	{name: "drive-doctor", run: runDriveDoctor},
	{name: "drive-reconcile", run: runDriveReconcile},
	{name: "download-sound-effects", run: runDownloadSoundEffects},
	{name: "rename-sound-effects", run: runRenameSoundEffects},
	{name: "update-sound-effect-metadata", run: runUpdateSoundEffectMetadata},
	{name: "apply-additional-sound-effects", run: runApplyAdditionalSoundEffects},
	{name: "classify-sound-effects", run: runClassifySoundEffects},
	{name: "trim-sound-effects", run: runTrimSoundEffects},
	{name: "organize-sound-effects-drive", run: runOrganizeSoundEffectsDrive},
	{name: "normalize-sound-effects-drive", run: runNormalizeSoundEffectsDrive},
	{name: "organize-foley-drive", run: runOrganizeFoleyDrive},
	{name: "trash-drive-files", run: runTrashDriveFiles},
	{name: "keep-drive-folder-files", run: runKeepDriveFolderFiles},
	{name: "organize-ui-drive", run: runOrganizeUIDrive},
	{name: "organize-family-drive", run: runOrganizeFamilyDrive},
	{name: "index-drive-clip", run: runIndexDriveClip},
	{name: "index-provided-sound-effects", run: runIndexProvidedSoundEffects},
	{name: "export-sound-effects-metadata", run: runExportSoundEffectsMetadata},
	{name: "rename-indexed-sound-effects", run: runRenameIndexedSoundEffects},
	{name: "list-folder-debug", run: runListFolderDebug},
	{name: "reorganize-cartoon", run: runReorganizeCartoon},
	{name: "download-kids-music", run: runDownloadKidsMusic},
	{name: "upload-drive-file", run: runUploadDriveFile},
	{name: "index-kids-music-metadata", run: runIndexKidsMusicMetadata},
	{name: "check-indexed-ids", run: runCheckIndexedIDs},
	{name: "reorganize-and-index-sfx", run: runReorganizeAndIndexSFX},
	{name: "check-drive-names", run: runCheckDriveNames},
	{name: "list-cartoon-files", run: runListCartoonFiles},
	{name: "check-db-cartoon-files", run: runCheckDBCartoonFiles},
	{name: "search-drive", run: runSearchDrive},
	{name: "gen-api-docs", run: runGenAPIDocs},
	{name: "list-drive-folder", run: runListDriveFolder},
	{name: "sync-drive-folder", run: runSyncDriveFolder},
	{name: "list-styles", run: runListStyles},
	{name: "reindex-qdrant", run: reconcile.RunReindexQdrant},
	{name: "reconcile-qdrant", run: reconcile.RunReconcileQdrant},
	{name: "reset-video-ai", run: runResetVideoAI},
	{name: "qdrant-maintenance", run: runQdrantMaintenance},
	{name: "qdrant-readiness", run: runQdrantReadiness},
	{name: "qdrant-preflight", run: runQdrantPreflight},
	{name: "stock-reset", run: runResetStockDrive},
	{name: "stock-subfolders-reset", run: runResetStockSubfolders},
	{name: "summarize-book", run: runSummarizeBook},
	{name: "sync-all-drive", run: runSyncAllDrive},
	{name: "sync-outros", run: runSyncOutros},
	{name: "test-youtube", run: runTestYouTube},
	{name: "text-tracks-backfill", run: runTextTracksBackfill},
	{name: "transcript-cues-backfill", run: runTranscriptCuesBackfill},
	{name: "unify-catalogs", run: runUnifyCatalogs},
	{name: "backfill-asset-embeddings", run: runBackfillAssetEmbeddings},
	{name: "backfill-visual-embeddings", run: runBackfillVisualEmbeddings},
	{name: "fullimages-migrate", run: runFullImagesMigrate},
	{name: "verify-artlist-pipeline", run: runVerifyArtlistPipeline},
}

var subcommandHandlers, availableCommands = buildSubcommandIndex(subcommandRegistry)

func buildSubcommandIndex(entries []subcommandEntry) (map[string]subcommandHandler, []string) {
	handlers := make(map[string]subcommandHandler, len(entries))
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.name == "" {
			panic("admin subcommand registry contains an empty name")
		}
		if entry.run == nil {
			panic("admin subcommand registry contains a nil handler for " + entry.name)
		}
		if _, exists := handlers[entry.name]; exists {
			panic("admin subcommand registry contains duplicate command " + entry.name)
		}
		handlers[entry.name] = entry.run
		names = append(names, entry.name)
	}
	return handlers, names
}

func dispatchSubcommand(name string, args []string) error {
	run, ok := subcommandHandlers[name]
	if !ok {
		return fmt.Errorf("%w: %s", errUnknownCommand, name)
	}
	return run(args)
}

func printUsage() {
	fmt.Println("Usage: admin <command> [args]")
	fmt.Println("Commands:")
	for _, command := range availableCommands {
		fmt.Printf("  %s\n", command)
	}
}
