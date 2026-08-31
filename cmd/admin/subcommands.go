// cmd/admin/subcommands.go — subcommand registry and dispatcher
package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/emergency"
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/audit"
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/backfill"
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cleanup"
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/database"
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/drive"
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/maintenance"
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/rendering"
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/soundeffects"
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/reconcile"
)

var errUnknownCommand = errors.New("unknown command")

type commandHandler func([]string) error

func withTimeout(f func(context.Context, []string) error, timeout time.Duration) commandHandler {
	return func(args []string) error {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		return f(ctx, args)
	}
}

var commandRegistry = map[string]commandHandler{
	"delete-clip-by-drive-file":      cleanup.RunDeleteClipByDriveFile,
	"apply-asset-metadata":           maintenance.RunApplyAssetMetadata,
	"audit-google-doc-links":         audit.RunAuditGoogleDocLinks,
	"audit-google-doc-render":        audit.RunAuditGoogleDocRender,
	"apply-asset-metadata-batch":     maintenance.RunApplyAssetMetadataBatch,
	"apply-additional-sound-effects": soundeffects.RunApplyAdditionalSoundEffects,
	"backfill-asset-embeddings":      backfill.RunBackfillAssetEmbeddings,
	"backfill-embedding-contract":    backfill.RunBackfillEmbeddingContract,
	"backfill-clip-folder-path":      backfill.RunBackfillClipFolderPath,
	"backfill-media-asset-sources":   backfill.RunBackfillMediaAssetSources,
	"backfill-media-durations":       backfill.RunBackfillMediaDurations,
	"backfill-missing":               backfill.RunBackfillMissing,
	"backfill-payload-hash":          backfill.RunBackfillPayloadHash,
	"backfill-provider-timestamps":   backfill.RunBackfillProviderTimestamps,
	"backfill-source-url-metadata":   backfill.RunBackfillSourceURLMetadata,
	"benchmark":                      rendering.RunBenchmark,
	"broken-references":              audit.RunBrokenReferences,
	"check-drive-names":              drive.RunCheckDriveNames,
	"clip-drive-audit":               audit.RunClipDriveAudit,
	"clip-drive-orphan-cleanup":      audit.RunClipDriveOrphanCleanup,
	"check-indexed-ids":              audit.RunCheckIndexedIds,
	"classify-sound-effects":         soundeffects.RunClassifySoundEffects,
	"cleanup-all-orphans":            cleanup.RunCleanupAllOrphans,
	"cleanup-artlist-empty-folders":  cleanup.RunCleanupArtlistEmptyFolders,
	"cleanup-stock-orphans":          cleanup.RunCleanupStockOrphans,
	"cleanup-delete-folders":         cleanup.RunDeleteSpecificFolders,
	"cleanup-test-youtube":           cleanup.RunTestYouTube,
	"control-plane-verify":           maintenance.RunControlPlaneVerify,
	"db":                             database.RunDB,
	"db-backup":                      withTimeout(database.RunDBBackup, 15*time.Minute),
	"db-check":                       withTimeout(database.RunDBCheck, 5*time.Minute),
	"db-migrate-legacy-cache":        database.RunMigrateLegacyCache,
	"db-migrations":                  withTimeout(database.RunDBMigrations, 5*time.Minute),
	"db-restore":                     withTimeout(database.RunDBRestore, 15*time.Minute),
	"db-rotate":                      withTimeout(database.RunDBRotate, 5*time.Minute),
	"db-status":                      withTimeout(database.RunDBStatus, 5*time.Minute),
	"delete-drive-images":            cleanup.RunDeleteDriveImages,
	"download-sound-effects":         soundeffects.RunDownloadSoundEffects,
	"dr-qdrant":                      reconcile.RunDrQdrant,
	"drive-bootstrap":                drive.RunDriveBootstrap,
	"drive-doctor":                   drive.RunDriveDoctor,
	"drive-reconcile":                drive.RunDriveReconcile,
	"drive-create-folder":            drive.RunDriveCreateFolder,
	"export-sound-effects-metadata":  soundeffects.RunExportSoundEffectsMetadata,
	"folder-path-backfill":           backfill.RunFolderPathBackfill,
	"gen-api-docs":                   rendering.RunGenAPIDocs,
	"index-drive-clip":               drive.RunIndexDriveClip,
	"index-provided-sound-effects":   soundeffects.RunIndexProvidedSoundEffects,
	"keep-drive-folder-files":        drive.RunKeepDriveFolderFiles,
	"list-drive-folder":              drive.RunListDriveFolder,
	"multilingual-benchmark":         rendering.RunMultilingualBenchmark,
	"multilingual-render":            rendering.RunMultilingualRender,
	"normalize-sound-effects-drive":  soundeffects.RunNormalizeSoundEffectsDrive,
	"organize-drive-folder":          cleanup.RunOrganizeDriveFolder,
	"organize-foley-drive":           soundeffects.RunOrganizeFoleyDrive,
	"organize-sound-effects-drive":   soundeffects.RunOrganizeSoundEffectsDrive,
	"performance-backfill":           maintenance.RunPerformanceBackfill,
	"performance-cold-warm":          maintenance.RunPerformanceColdWarmReport,
	"performance-report":             maintenance.RunPerformanceReport,
	"reachability-graph":             audit.RunReachabilityGraph,
	"reconcile-orphaned-runs":        maintenance.RunReconcileOrphanedRuns,
	"reconcile-qdrant":               reconcile.RunReconcileQdrant,
	"recover-registry-from-qdrant":   emergency.RunRecoverRegistryFromQdrant,
	"reindex-qdrant":                 reconcile.RunReindexQdrant,
	"remove-drive-folder-recursive":  drive.RunRemoveDriveFolderRecursive,
	"repair-drive-links":             audit.RunRepairDriveLinks,
	"repair-stock-metadata":          audit.RunRepairStockMetadata,
	"reorganize-and-index-sfx":       soundeffects.RunReorganizeAndIndexSFX,
	"reset-video-ai":                 maintenance.RunResetVideoAI,
	"search-drive":                   drive.RunSearchDrive,
	"stock-reset":                    maintenance.RunResetStockDrive,
	"stock-subfolders-reset":         maintenance.RunResetStockSubfolders,
	"summarize-book":                 rendering.RunSummarizeBook,
	"sync-drive-folder":              drive.RunSyncDriveFolder,
	"sync-outros":                    maintenance.RunSyncOutros,
	"text-tracks-align-cues":         backfill.RunTextTracksAlignCues,
	"text-tracks-backfill":           backfill.RunTextTracksBackfill,
	"transcript-cues-backfill":       backfill.RunTranscriptCuesBackfill,
	"trash-drive-files":              drive.RunTrashDriveFiles,
	"trim-sound-effects":             soundeffects.RunTrimSoundEffects,
	"unify-catalogs":                 maintenance.RunUnifyCatalogs,
	"upload-drive-file":              drive.RunUploadDriveFile,
	"zombie-sweep":                   cleanup.RunZombieSweep,
}

func dispatchSubcommand(name string, args []string) error {
	handler, ok := commandRegistry[name]
	if !ok {
		return errUnknownCommand
	}
	return handler(args)
}

func printUsage() {
	fmt.Println("Available commands:")
	names := make([]string, 0, len(commandRegistry))
	for name := range commandRegistry {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Printf("  %s\n", name)
	}
}
