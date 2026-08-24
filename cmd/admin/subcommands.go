// cmd/admin/subcommands.go — subcommand registry and dispatcher
package main

import (
	"errors"
	"fmt"
	"sort"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/audit"
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/backfill"
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cleanup"
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/database"
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/drive"
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/maintenance"
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/qdrant"
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/rendering"
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/soundeffects"
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/reconcile"
)

var errUnknownCommand = errors.New("unknown command")

type commandHandler func([]string) error

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
	"backfill-visual-embeddings":     qdrant.RunBackfillVisualEmbeddings,
	"benchmark":                      rendering.RunBenchmark,
	"broken-references":              audit.RunBrokenReferences,
	"check-drive-names":              drive.RunCheckDriveNames,
	"clip-drive-audit":               audit.RunClipDriveAudit,
	"clip-drive-orphan-cleanup":      cleanup.RunClipDriveOrphanCleanup,
	"check-indexed-ids":              audit.RunCheckIndexedIds,
	"classify-sound-effects":         soundeffects.RunClassifySoundEffects,
	"cleanup-all-orphans":            cleanup.RunCleanupAllOrphans,
	"cleanup-artlist-empty-folders":  cleanup.RunCleanupArtlistEmptyFolders,
	"cleanup-orphans":                cleanup.RunCleanupOrphans,
	"cleanup-stock-orphans":          cleanup.RunCleanupStockOrphans,
	"control-plane":                  maintenance.RunControlPlane,
	"db":                             database.RunDB,
	"delete-specific-folders":        cleanup.RunDeleteSpecificFolders,
	"delete-drive-images":            cleanup.RunDeleteDriveImages,
	"download-sound-effects":         soundeffects.RunDownloadSoundEffects,
	"drive-bootstrap":                drive.RunDriveBootstrap,
	"drive-create-folder":            drive.RunDriveCreateFolder,
	"drive-doctor":                   drive.RunDriveDoctor,
	"drive-reconcile":                drive.RunDriveReconcile,
	"dr-qdrant":                      reconcile.RunDrQdrant,
	"export-sound-effects-metadata":  soundeffects.RunExportSoundEffectsMetadata,
	"folder-path-backfill":           backfill.RunFolderPathBackfill,
	"gen-api-docs":                   rendering.RunGenAPIDocs,
	"identity-audit":                 audit.RunIdentityAudit,
	"index-drive-clip":               drive.RunIndexDriveClip,
	"index-provided-sound-effects":   soundeffects.RunIndexProvidedSoundEffects,
	"keep-drive-folder-files":        drive.RunKeepDriveFolderFiles,
	"list-drive-folder":              drive.RunListDriveFolder,
	"list-styles":                    maintenance.RunListStyles,
	"migrate-legacy-cache":           database.RunMigrateLegacyCache,
	"multilingual-benchmark":         rendering.RunMultilingualBenchmark,
	"multilingual-render":            rendering.RunMultilingualRender,
	"normalize-sound-effects-drive":  soundeffects.RunNormalizeSoundEffectsDrive,
	"organize-drive-folder":          cleanup.RunOrganizeDriveFolder,
	"organize-foley-drive":           soundeffects.RunOrganizeFoleyDrive,
	"organize-sound-effects-drive":   soundeffects.RunOrganizeSoundEffectsDrive,
	"performance-backfill":           maintenance.RunPerformanceBackfill,
	"performance-report":             maintenance.RunPerformanceReport,
	"qdrant-maintenance":             qdrant.RunQdrantMaintenance,
	"qdrant-bucket-report":           qdrant.RunQdrantBucketReport,
	"qdrant-enrichment-recover":      qdrant.RunQdrantEnrichmentRecover,
	"qdrant-preflight":               qdrant.RunQdrantPreflight,
	"qdrant-readiness":               qdrant.RunQdrantReadiness,
	"reachability-graph":             audit.RunReachabilityGraph,
	"reconcile-orphaned-runs":        maintenance.RunReconcileOrphanedRuns,
	"reconcile-qdrant":               reconcile.RunReconcileQdrant,
	"remove-drive-folder-recursive":  drive.RunRemoveDriveFolderRecursive,
	"repair-drive-links":             audit.RunRepairDriveLinks,
	"repair-stock-metadata":          audit.RunRepairStockMetadata,
	"reindex-qdrant":                 reconcile.RunReindexQdrant,
	"rename-indexed-sound-effects":   soundeffects.RunRenameIndexedSoundEffects,
	"rename-sound-effects":           soundeffects.RunRenameSoundEffects,
	"render-short":                   rendering.RunRenderShort,
	"reorganize-and-index-sfx":       soundeffects.RunReorganizeAndIndexSFX,
	"reset-video-ai":                 maintenance.RunResetVideoAI,
	"search-drive":                   drive.RunSearchDrive,
	"sqlite-audit":                   audit.RunSQLiteAudit,
	"stock-reset":                    maintenance.RunResetStockDrive,
	"storage-snapshot":               audit.RunStorageSnapshot,
	"stock-subfolders-reset":         maintenance.RunResetStockSubfolders,
	"summarize-book":                 rendering.RunSummarizeBook,
	"sync-all-drive":                 cleanup.RunSyncAllDrive,
	"sync-drive-folder":              drive.RunSyncDriveFolder,
	"sync-outros":                    maintenance.RunSyncOutros,
	"test-youtube":                   cleanup.RunTestYouTube,
	"text-tracks-align-cues":         backfill.RunTextTracksAlignCues,
	"text-tracks-backfill":           backfill.RunTextTracksBackfill,
	"transcript-cues-backfill":       backfill.RunTranscriptCuesBackfill,
	"trash-drive-files":              drive.RunTrashDriveFiles,
	"trim-sound-effects":             soundeffects.RunTrimSoundEffects,
	"unify-catalogs":                 maintenance.RunUnifyCatalogs,
	"update-sound-effect-metadata":   soundeffects.RunUpdateSoundEffectMetadata,
	"upload-drive-file":              drive.RunUploadDriveFile,
	"verify-projection":              audit.RunVerifyProjection,
	"zombie-sweep":                   cleanup.RunZombieSweep,
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

func dispatchSubcommand(name string, args []string) error {
	handler, ok := commandRegistry[name]
	if !ok {
		return fmt.Errorf("%w: %s", errUnknownCommand, name)
	}
	return handler(args)
}

func printUsage() {
	fmt.Println("Usage: admin <command> [args]")
	fmt.Println("Commands:")
	for _, c := range availableCommands {
		fmt.Printf("  %s\n", c)
	}
}
