// cmd/admin/cleanup.go — cleanup subcommands (orchestrator)
//
// All cleanup subcommands (cleanup-orphans, cleanup-all-orphans,
// cleanup-artlist-empty-folders, cleanup-stock-orphans,
// delete-specific-folders, sync-all-drive, test-youtube) were migrated
// off the deleted legacy packages `internal/media`, `internal/upload/drive`
// and the deleted `app.ExportInitCoreMinimal` helper.
//
// Post-fix wiring:
//
//   - `app.InitComposition(cfg, log)` returns the canonical *ComposeRoot
//     (replacing `app.ExportInitCoreMinimal(cfg, log) *CoreDeps` which
//     was removed in PR4d-final).
//   - The deletion service now lives at `root.Maint.DeletionSvc`. The
//     canonical `deletion.NewDeletionService` (PR-wave 11) collapsed the
//     historical three source-specific clip repos
//     (ArtlistRepo / ClipsOnlyRepo / StockDriveRepo) into ONE
//     `*assets.ClipsRepository` — the source discriminator is now on the
//     row, not on the repo constructor argument. Caller code that passed
//     three different repos for artlist/stock/clips simply passes the
//     single `root.Repos.ClipsRepo` three times (matches the wiring in
//     `internal/app/composition.go::BuildMaintBundle`).
//   - The Google Drive client and uploader are reached through
//     `root.Drive.DriveClient` and `root.Drive.DriveUploader` (canonical
//     `internal/infrastructure/drive` types), no longer through the
//     removed `internal/upload/drive` package.
//
// Per-subcommand implementations live in companion files:
//   - cleanup_orphan_files.go  — runCleanupOrphans (local filesystem)
//   - cleanup_drive_orphans.go — runCleanupAllOrphans, runCleanupArtlistEmptyFolders, runCleanupStockOrphans
//   - cleanup_delete_folders.go — runDeleteSpecificFolders
//   - cleanup_sync_drive.go    — runSyncAllDrive
//   - cleanup_test_youtube.go  — runTestYouTube
//
// Public subcommand dispatch is documented in `cmd/admin/main.go::main()`
// and covered by `cmd/admin/admin_test.go::TestAdminCommands_AreRegistered`.
package main
