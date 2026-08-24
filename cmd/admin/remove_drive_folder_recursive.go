// cmd/admin/remove_drive_folder_recursive.go — recursive Drive folder removal
//
// Recursively removes a Google Drive folder and ALL its subdirectories
// from Drive, SQLite (media_assets, clip_folders, drive_folder_catalog),
// and Qdrant (via the outbox-driven deletion state machine).
//
// Deletion is orchestrated in the safe order:
//
//	PLAN           — scan folders, resolve + deduplicate asset IDs
//	PREFLIGHT      — fail-fast before any mutation
//	DELETE ASSETS  — dispatch each unique asset through the outbox
//	WAIT TERMINAL  — wait for every asset to reach DELETED
//	DELETE FOLDERS — only now delete Drive folders (deepest first)
//	VERIFY         — confirm zero remaining assets
//
// Drive folders are NEVER deleted before the canonical assets have
// reached terminal deletion.
//
// Usage:
//
//	admin remove-drive-folder-recursive <drive-folder-id>
//
// Flags:
//
//	--apply       execute deletion (default is a non-destructive dry-run)
//	--dry-run     only plan and report what would be deleted (no mutation; this is the default)
//	--delete-root also delete the root folder itself (default: keep root, delete only its subfolders)
//
// Example:
//
//	admin remove-drive-folder-recursive 10p7NPodbQNjbSyvDIQJtowcmGeejwwlb
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

func runRemoveDriveFolderRecursive(args []string) error {
	fs := flag.NewFlagSet("remove-drive-folder-recursive", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apply := fs.Bool("apply", false, "Execute deletion (default is a non-destructive dry-run)")
	dryRun := fs.Bool("dry-run", false, "Only plan and report what would be deleted (no mutation; this is the default)")
	deleteRoot := fs.Bool("delete-root", false, "Also delete the root folder itself (default: keep root and delete only its subfolders)")
	sourceVideoIDs := fs.String("source-video-ids", "", "Comma-separated YouTube source IDs whose indexed clips must also be removed")
	assetsOnly := fs.Bool("assets-only", false, "Skip Drive folder traversal and remove only assets selected by --source-video-ids")
	planFile := fs.String("plan-file", "", "Path to persist the deletion plan manifest (default: ./deletion-plan-<rootID>-<timestamp>.json)")
	timeout := fs.Duration("timeout", 10*time.Minute, "Maximum time to wait for assets to reach terminal deletion")
	if err := fs.Parse(args); err != nil {
		return err
	}

	folderIDs := fs.Args()
	if len(folderIDs) == 0 {
		return fmt.Errorf("at least one Drive folder ID is required")
	}
	if *timeout <= 0 {
		return fmt.Errorf("--timeout must be positive")
	}
	if *apply && *dryRun {
		return fmt.Errorf("--apply and --dry-run are mutually exclusive")
	}

	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		log.Fatal("Failed to initialize composition root", zap.Error(err))
	}
	defer rootCleanup()

	if root.Drive == nil || root.Drive.Reader == nil {
		return fmt.Errorf("drive reader port is not available")
	}
	driveReader := root.Drive.Reader
	driveAdmin := root.Drive.Admin
	deletionSvc := root.Maint.DeletionSvc
	db := root.DB.DB

	ctx := cmdContext()

	// Start the outbox worker pool once for the whole command so the
	// Drive/Qdrant/SQLite deletion chain can actually advance to terminal.
	// Pool.Start owns the worker lifecycle and blocks until ctx is
	// cancelled, so it runs asynchronously alongside the dispatch below.
	var poolStarted bool
	startPool := func() error {
		if poolStarted {
			return nil
		}
		if root.Outbox == nil || root.Outbox.EventsPool == nil {
			return fmt.Errorf("outbox events pool is not configured — cannot drive asset deletion to terminal")
		}
		poolStarted = true
		go root.Outbox.EventsPool.Start(ctx, 1)
		return nil
	}
	defer func() {
		if poolStarted {
			_ = root.Outbox.EventsPool.Stop(15 * time.Second)
		}
	}()

	for _, rootFolderID := range folderIDs {
		fmt.Printf("\n=== Processing root folder: %s ===\n", rootFolderID)

		// ── PLAN ──────────────────────────────────────────────────────
		fmt.Println("PLAN: scanning folder tree + resolving assets...")
		var allFolders []folderInfo
		if !*assetsOnly {
			allFolders, err = collectAllSubfolders(ctx, driveReader, rootFolderID)
			if err != nil {
				return fmt.Errorf("failed to scan folder tree for %s: %w", rootFolderID, err)
			}
			// Root is KEPT by default; include it only when --delete-root
			// is set (explicit intent to destroy the root too).
			if *deleteRoot {
				allFolders = append([]folderInfo{{ID: rootFolderID, Name: "(root)"}}, allFolders...)
			}
		}

		// Deduplicate strictly by canonical asset_id: the same asset is
		// discoverable via folder_id AND parent_folder_id, so it must not
		// be counted (or dispatched) twice.
		assets, rawRefs, err := collectUniqueAssets(ctx, db, allFolders, *sourceVideoIDs)
		if err != nil {
			return err
		}
		assetIDs := make([]string, 0, len(assets))
		for _, a := range assets {
			assetIDs = append(assetIDs, a.ID)
		}
		folderIDs := make([]string, 0, len(allFolders))
		for _, f := range allFolders {
			folderIDs = append(folderIDs, f.ID)
		}

		plan := deletionPlan{
			RootFolderID: rootFolderID,
			DeleteRoot:   *deleteRoot,
			Folders:      len(allFolders),
			AssetsRaw:    rawRefs,
			AssetsUnique: len(assetIDs),
			Duplicates:   rawRefs - len(assetIDs),
			FolderIDs:    folderIDs,
			AssetIDs:     assetIDs,
		}

		// Persist the plan manifest BEFORE the first mutation so operators
		// have a durable recovery record even if the run fails part-way.
		planPath := *planFile
		if planPath == "" {
			planPath = fmt.Sprintf("deletion-plan-%s-%d.json", rootFolderID, time.Now().Unix())
		}
		if err := writeDeletionPlan(plan, planPath); err != nil {
			return err
		}

		rootAction := "KEEP ROOT"
		if *deleteRoot {
			rootAction = "DELETE ROOT"
		}
		fmt.Printf("  root folder:   %s\n", rootFolderID)
		fmt.Printf("  action:        %s\n", rootAction)
		fmt.Printf("  drive folders: %d scheduled for deletion\n", len(allFolders))
		fmt.Printf("  assets:        %d references, %d unique, %d duplicates\n", rawRefs, len(assetIDs), plan.Duplicates)
		applyLabel := "NO"
		if *apply {
			applyLabel = "YES"
		}
		fmt.Printf("  apply:         %s\n", applyLabel)
		fmt.Printf("  plan manifest: %s\n", planPath)

		if !*apply {
			fmt.Println("\nDRY RUN — use --apply to execute deletion.")
			fmt.Printf("Would delete %d unique assets then %d folders.\n", len(assetIDs), len(allFolders))
			continue
		}

		// ── PREFLIGHT ────────────────────────────────────────────────
		// Fail fast BEFORE the first mutation: the dispatcher, the
		// Drive/Qdrant delete handlers, the outbox pool, and SQLite must
		// all be ready, otherwise an asset would be dispatched and its
		// chain stall (or dead-letter). Never let a half-wired command
		// mutate anything.
		if deletionSvc == nil {
			return fmt.Errorf("deletion service is not configured — cannot delete assets")
		}
		if db == nil {
			return fmt.Errorf("database is not configured")
		}
		if len(allFolders) > 0 && driveAdmin == nil {
			return fmt.Errorf("drive admin port is not configured — cannot delete folders")
		}
		if len(assetIDs) > 0 {
			if root.Outbox == nil || root.Outbox.Dispatcher == nil {
				return fmt.Errorf("preflight: outbox dispatcher is not wired (DeleteAsset would fail ErrDeletionDispatcherUnavailable)")
			}
			if root.Outbox.EventsPool == nil {
				return fmt.Errorf("preflight: outbox events pool is not configured (cannot drive deletion to terminal)")
			}
			if err := checkDeletionHandlers(root.Outbox.EventsRegistry); err != nil {
				return err
			}
			if err := checkMediaAssetsReady(db); err != nil {
				return err
			}
			if err := startPool(); err != nil {
				return err
			}
		}

		// ── DELETE ASSETS ────────────────────────────────────────────
		fmt.Printf("\nDELETE ASSETS: dispatching %d unique asset(s)...\n", len(assetIDs))
		assetFailures := 0
		for i, a := range assets {
			fmt.Printf("  [%d/%d] Deleting asset %s (%s)... ", i+1, len(assets), a.Name, a.ID)
			if err := deletionSvc.DeleteAsset(ctx, a.ID, true); err != nil {
				fmt.Printf("FAILED: %v\n", err)
				assetFailures++
			} else {
				fmt.Println("OK")
			}
		}
		if assetFailures > 0 {
			return fmt.Errorf("%d asset(s) failed to dispatch — aborting before folder deletion", assetFailures)
		}

		// ── WAIT TERMINAL ────────────────────────────────────────────
		// Never touch Drive folders until every asset has reached DELETED
		// (or its row is gone) and both deletion events completed.
		if len(assetIDs) > 0 {
			fmt.Printf("\nWAIT TERMINAL: waiting for %d asset(s) to reach terminal deletion...\n", len(assetIDs))
			waitCtx, waitCancel := context.WithTimeout(ctx, *timeout)
			err = waitForAllAssetDeletions(waitCtx, db, assetIDs)
			waitCancel()
			if err != nil {
				return err
			}
		}

		// ── DELETE FOLDERS ───────────────────────────────────────────
		// Only now delete Drive folders + clean clip_folders /
		// drive_folder_catalog. Reverse order (deepest first).
		fmt.Printf("\nDELETE FOLDERS: deleting %d folder(s) from Drive and database...\n", len(allFolders))
		folderFailures := 0
		for i := len(allFolders) - 1; i >= 0; i-- {
			f := allFolders[i]
			fmt.Printf("  Deleting folder %s (%s)... ", f.Name, f.ID)

			if driveAdmin != nil {
				if err := driveAdmin.DeleteFolder(ctx, f.ID); err != nil {
					fmt.Printf("Drive delete FAILED: %v", err)
					folderFailures++
				} else {
					fmt.Printf("Drive OK")
				}
			} else {
				fmt.Printf("(no drive admin)")
			}

			if db != nil {
				if _, err := db.ExecContext(ctx, "DELETE FROM clip_folders WHERE folder_id = ?", f.ID); err != nil {
					log.Warn("Failed to delete clip_folders row", zap.String("folder_id", f.ID), zap.Error(err))
				}
				if _, err := db.ExecContext(ctx, "DELETE FROM drive_folder_catalog WHERE folder_id = ?", f.ID); err != nil {
					log.Warn("Failed to delete drive_folder_catalog row", zap.String("folder_id", f.ID), zap.Error(err))
				}
			}
			fmt.Println()
		}

		// ── VERIFY ───────────────────────────────────────────────────
		fmt.Println("\nVERIFY: checking for remaining assets...")
		remaining := 0
		for _, f := range allFolders {
			count, err := countAssetsInFolder(ctx, db, f.ID)
			if err != nil {
				return fmt.Errorf("verify count assets in folder %s: %w", f.ID, err)
			}
			remaining += count
		}
		if *sourceVideoIDs != "" {
			remainingMeta, err := listAssetsBySourceVideoIDs(ctx, db, *sourceVideoIDs)
			if err != nil {
				return fmt.Errorf("verify source-video assets: %w", err)
			}
			remaining += len(remainingMeta)
		}
		if remaining != 0 {
			return fmt.Errorf("verification failed: %d asset(s) still present after terminal deletion", remaining)
		}
		fmt.Println("VERIFY OK: 0 remaining assets.")

		fmt.Printf("\nDone. Assets: %d dispatched, %d failures. Folders: %d processed, %d failures.\n",
			len(assetIDs)-assetFailures, assetFailures, len(allFolders)-folderFailures, folderFailures)
	}

	return nil
}

// folderInfo holds the name and ID of a Drive folder.
type folderInfo struct {
	ID   string
	Name string
}

// collectAllSubfolders recursively collects all subfolder IDs under the given parent.
func collectAllSubfolders(ctx context.Context, reader drive.Reader, parentID string) ([]folderInfo, error) {
	if reader == nil {
		return nil, fmt.Errorf("drive reader port not available")
	}
	files, err := reader.ListFiles(ctx, parentID)
	if err != nil {
		return nil, err
	}

	var folders []folderInfo
	for _, f := range files {
		if f.MimeType != "application/vnd.google-apps.folder" {
			continue
		}
		folders = append(folders, folderInfo{ID: f.ID, Name: f.Name})

		subs, err := collectAllSubfolders(ctx, reader, f.ID)
		if err != nil {
			return folders, fmt.Errorf("recursing into %s (%s): %w", f.Name, f.ID, err)
		}
		folders = append(folders, subs...)
	}
	return folders, nil
}

// assetRow is a minimal projection for deletion. Source is intentionally
// absent: deletion is asset-centric and never consults the source column.
type assetRow struct {
	ID   string
	Name string
}

// deletionPlan is the durable, pre-mutation manifest for a single root
// folder removal. It captures the raw reference count, the deduplicated
// asset set, and the duplicate delta so recovery can reconstruct exactly
// what was scheduled — never derived after the mutation.
type deletionPlan struct {
	RootFolderID string   `json:"root_folder_id"`
	DeleteRoot   bool     `json:"delete_root"`
	Folders      int      `json:"folders"`
	AssetsRaw    int      `json:"assets_raw"`
	AssetsUnique int      `json:"assets_unique"`
	Duplicates   int      `json:"duplicates"`
	FolderIDs    []string `json:"folder_ids"`
	AssetIDs     []string `json:"asset_ids"`
}

// writeDeletionPlan persists the plan manifest as indented JSON.
func writeDeletionPlan(plan deletionPlan, path string) error {
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal deletion plan: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write deletion plan %s: %w", path, err)
	}
	return nil
}

// deletionPreflightEventTypes are the canonical outbox event types that
// MUST have a registered handler before any asset is dispatched. A missing
// handler means the emitted event would dead-letter and the chain would
// never reach terminal deletion.
var deletionPreflightEventTypes = []string{
	"asset.drive.delete_requested",
	"asset.index.delete_requested",
}

// checkDeletionHandlers verifies the Drive + Qdrant/index delete handlers
// are registered in the outbox event registry. Fail-closed: a missing
// handler must abort BEFORE the first mutation, not after the first asset
// has already been dispatched into a dead-letter.
func checkDeletionHandlers(registry *outboxevents.HandlerRegistry) error {
	if registry == nil {
		return fmt.Errorf("preflight: outbox handler registry is not configured")
	}
	for _, evt := range deletionPreflightEventTypes {
		if _, ok := registry.Get(evt); !ok {
			return fmt.Errorf("preflight: no outbox handler registered for %q (deletion would dead-letter)", evt)
		}
	}
	return nil
}

// checkMediaAssetsReady verifies the canonical media_assets table exists.
// The lifecycle_state column is already exercised by the PLAN-phase listing
// queries, so table presence is the remaining readiness gate.
func checkMediaAssetsReady(db *sql.DB) error {
	var n int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='media_assets'`).Scan(&n); err != nil {
		return fmt.Errorf("preflight: media_assets table check failed: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("preflight: media_assets table missing (SQLite lifecycle not ready)")
	}
	return nil
}

// collectUniqueAssets deduplicates (by canonical asset_id) all assets
// discoverable under the given folders PLUS the --source-video-ids
// selection. It returns the deduplicated assets (sorted by ID) and the
// raw reference count (before dedup) so the plan can report duplicates.
func collectUniqueAssets(ctx context.Context, db *sql.DB, folders []folderInfo, sourceVideoIDs string) (assets []assetRow, rawRefs int, err error) {
	unique := make(map[string]assetRow)
	for _, f := range folders {
		list, err := listAssetsInFolder(ctx, db, f.ID)
		if err != nil {
			return nil, 0, fmt.Errorf("list assets in folder %s: %w", f.ID, err)
		}
		rawRefs += len(list)
		for _, a := range list {
			unique[a.ID] = a
		}
	}
	metadataAssets, err := listAssetsBySourceVideoIDs(ctx, db, sourceVideoIDs)
	if err != nil {
		return nil, 0, err
	}
	rawRefs += len(metadataAssets)
	for _, a := range metadataAssets {
		unique[a.ID] = a
	}

	ids := make([]string, 0, len(unique))
	for id := range unique {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make([]assetRow, 0, len(ids))
	for _, id := range ids {
		out = append(out, unique[id])
	}
	return out, rawRefs, nil
}

// waitForAllAssetDeletions waits for every asset to reach terminal
// deletion (lifecycle_state DRIVE_DELETED/DELETED with both deletion
// events completed, or row gone). Reuses the canonical per-asset waiter
// from delete_clip_by_drive_file.go.
func waitForAllAssetDeletions(ctx context.Context, db *sql.DB, assetIDs []string) error {
	for i, id := range assetIDs {
		if err := waitForAssetDeletion(ctx, db, id); err != nil {
			return fmt.Errorf("asset %d/%d (%s): %w", i+1, len(assetIDs), id, err)
		}
		fmt.Printf("  [%d/%d] asset %s reached terminal deletion\n", i+1, len(assetIDs), id)
	}
	return nil
}

// countAssetsInFolder counts non-soft-deleted media_assets whose
// folder_id or parent_folder_id matches the given folderID.
func countAssetsInFolder(ctx context.Context, db *sql.DB, folderID string) (int, error) {
	var count int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_assets
		 WHERE `+asset.SoftDeleteFilter()+`
		 AND (folder_id = ? OR parent_folder_id = ?)`,
		folderID, folderID).Scan(&count)
	return count, err
}

// listAssetsInFolder lists non-soft-deleted media_assets whose
// folder_id or parent_folder_id matches the given folderID.
func listAssetsInFolder(ctx context.Context, db *sql.DB, folderID string) ([]assetRow, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, COALESCE(name, '')
		 FROM media_assets
		 WHERE `+asset.SoftDeleteFilter()+`
		 AND (folder_id = ? OR parent_folder_id = ?)
		 ORDER BY name`,
		folderID, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []assetRow
	for rows.Next() {
		var a assetRow
		if err := rows.Scan(&a.ID, &a.Name); err != nil {
			return out, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func listAssetsBySourceVideoIDs(ctx context.Context, db *sql.DB, raw string) ([]assetRow, error) {
	ids := make([]string, 0)
	for _, id := range strings.Split(raw, ",") {
		if value := strings.TrimSpace(id); value != "" {
			ids = append(ids, value)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := db.QueryContext(ctx,
		`SELECT id, COALESCE(name, '') FROM media_assets WHERE `+asset.SoftDeleteFilter()+
			` AND json_extract(COALESCE(metadata_json, '{}'), '$.source_video_id') IN (`+placeholders+") ORDER BY id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []assetRow
	for rows.Next() {
		var a assetRow
		if err := rows.Scan(&a.ID, &a.Name); err != nil {
			return out, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
