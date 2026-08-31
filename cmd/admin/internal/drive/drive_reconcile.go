// cmd/admin/drive_reconcile.go — F5: Drive reconcile CLI command (July 2026)
//
// Recursively scans a Drive root folder, discovers existing subdirectories,
// and populates the drive_folder_catalog table. Also cross-references
// media_assets rows with Drive file IDs to ensure the catalog covers
// all folders referenced by existing assets.
//
// Usage:
//
//	go run ./cmd/admin drive-reconcile --root "ROOT_FOLDER_ID" [--apply] [--max-depth N]
//
//	--root       Drive folder ID of the unified media root (default: cfg.Drive.RootFolder())
//	--apply      Actually write to catalog (default: dry-run)
//	--max-depth  Maximum recursion depth (default: 5)
//	--sync-assets Cross-reference media_assets and populate missing catalog entries (default: false)
//
// godlike/06 SSOT (one canonical owner per fact): the reconcile CLI is the
// canonical SOLE writer for source=discovered entries in drive_folder_catalog.
//
// godlike/07 NO-FAKE-AVAILABILITY: --dry-run is the default. --apply is the
// operator-explicit opt-in. --sync-assets is gated behind a separate flag
// so a simple --apply doesn't accidentally mutate media_assets.
package drive

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	sqlitedelivery "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/delivery"
)

const driveFolderMimeType = "application/vnd.google-apps.folder"

type reconcileResult struct {
	Path     string
	FolderID string
	Source   string // "discovered", "cataloged", "asset_sync"
	Depth    int
	Error    string // empty on success
}

func RunDriveReconcile(args []string) error {
	fs := flag.NewFlagSet("drive-reconcile", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	rootID := fs.String("root", "", "Drive folder ID of the media root (default: cfg.Drive.RootFolder())")
	apply := fs.Bool("apply", false, "Actually write to catalog (default: dry-run only)")
	maxDepth := fs.Int("max-depth", 5, "Maximum recursion depth")
	syncAssets := fs.Bool("sync-assets", false, "Cross-reference media_assets and populate missing catalog entries")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	if strings.TrimSpace(*rootID) == "" {
		*rootID = cfg.Drive.RootFolder()
	}
	if strings.TrimSpace(*rootID) == "" {
		return fmt.Errorf("drive-reconcile: --root is required (no media_root_folder configured either)")
	}
	*rootID = strings.TrimSpace(*rootID)

	if !*apply {
		fmt.Println("Drive Reconcile — DRY RUN")
		fmt.Printf("Root: %s (max depth: %d)\n\n", *rootID, *maxDepth)

		// Quick listing: just show immediate children.
		uploader, err := cli.BuildDriveAdminForCLI(cli.CmdContext(), cfg, log)
		if err != nil {
			return fmt.Errorf("drive-reconcile: init Drive client: %w", err)
		}
		files, listErr := uploader.ListFiles(cli.CmdContext(), *rootID)
		if listErr != nil {
			return fmt.Errorf("drive-reconcile: list root folder: %w", listErr)
		}
		folders := 0
		for _, f := range files {
			if f.MimeType == driveFolderMimeType {
				folders++
				fmt.Printf("  📁 %-30s  %s\n", f.Name, f.ID)
			}
		}
		fmt.Printf("\n  %d folder(s), %d total entries\n", folders, len(files))
		fmt.Println("\nPass --apply to execute. Use --sync-assets to also cross-reference media_assets.")
		return nil
	}

	// --apply path.
	return executeReconcile(cli.CmdContext(), cfg, log, *rootID, *maxDepth, *syncAssets)
}

func executeReconcile(ctx context.Context, cfg *config.Config, log *zap.Logger, rootID string, maxDepth int, syncAssets bool) error {
	dbSet, err := cli.OpenDatabaseSet(cfg, log)
	if err != nil {
		return fmt.Errorf("drive-reconcile: open database set: %w", err)
	}
	defer dbSet.Close()

	catalogRepo := sqlitedelivery.NewRepository(dbSet.Primary.DB)

	uploader, err := cli.BuildDriveAdminForCLI(ctx, cfg, log)
	if err != nil {
		return fmt.Errorf("drive-reconcile: init Drive client: %w", err)
	}

	fmt.Println("Drive Reconcile")
	fmt.Printf("Root: %s (max depth: %d)\n\n", rootID, maxDepth)

	var results []reconcileResult
	discovered := 0
	errors := 0

	// Build a set of canonical namespace names for quick lookup.
	namespaceSet := make(map[string]string) // namespace -> destination
	for _, ns := range canonicalDriveNamespaces {
		namespaceSet[ns.Namespace] = ns.Destination
	}

	// Phase 1: Recursively scan starting from root.
	env := &reconcileEnv{
		Reader:       uploader,
		Repo:         catalogRepo,
		MaxDepth:     maxDepth,
		NamespaceSet: namespaceSet,
		Results:      &results,
		Discovered:   &discovered,
		Errors:       &errors,
	}
	if err := reconcileFolder(ctx, env, rootID, "", "", 0, true); err != nil {
		return fmt.Errorf("drive-reconcile: %w", err)
	}

	// Phase 2: Cross-reference media_assets (optional).
	assetSynced := 0
	if syncAssets {
		fmt.Println("\n--- Phase 2: media_assets cross-reference ---")
		n, syncErr := syncMediaAssetsToCatalog(ctx, dbSet.Primary.DB, catalogRepo)
		if syncErr != nil {
			fmt.Printf("  ⚠️  media_assets sync error: %v\n", syncErr)
			errors++
		} else {
			assetSynced = n
			fmt.Printf("  ✅ %d asset folder(s) synced to catalog\n", n)
		}
	}

	// Summary.
	fmt.Printf("\nStatus: %d discovered, %d asset-synced, %d errors\n", discovered, assetSynced, errors)
	if errors > 0 {
		return fmt.Errorf("drive-reconcile: %d error(s) during scan", errors)
	}
	return nil
}

// reconcileEnv holds the shared dependencies and mutable state for
// reconcileFolder. It is passed by pointer so that recursive calls
// operate on the same result counters and slice.
type reconcileEnv struct {
	Reader       drive.Reader
	Repo         *sqlitedelivery.Repository
	MaxDepth     int
	NamespaceSet map[string]string
	Results      *[]reconcileResult
	Discovered   *int
	Errors       *int
}

// reconcileFolder recursively scans a Drive folder and populates the catalog.
// When isRoot is true, the folder itself is not cataloged — only its children.
// Uses drive.Reader for ListFiles access.
func reconcileFolder(
	ctx context.Context,
	env *reconcileEnv,
	folderID, parentPath, parentNamespace string,
	depth int,
	isRoot bool,
) error {
	if depth > env.MaxDepth {
		return nil
	}

	files, err := env.Reader.ListFiles(ctx, folderID)
	if err != nil {
		*env.Errors++
		*env.Results = append(*env.Results, reconcileResult{
			Path:  parentPath,
			Depth: depth,
			Error: fmt.Sprintf("list folder %s: %v", folderID, err),
		})
		return nil // non-fatal: continue with other folders
	}

	for _, f := range files {
		if f.MimeType != driveFolderMimeType {
			continue
		}

		childPath := f.Name
		if parentPath != "" {
			childPath = parentPath + "/" + f.Name
		}

		// Determine the destination for this folder.
		dest := ""
		namespace := ""

		if isRoot {
			if d, ok := env.NamespaceSet[f.Name]; ok {
				dest = d
				namespace = f.Name
			}
		} else {
			namespace = parentNamespace
			if namespace != "" {
				for _, ns := range canonicalDriveNamespaces {
					if ns.Namespace == namespace {
						dest = ns.Destination
						break
					}
				}
			}
		}

		result := reconcileResult{
			Path:     childPath,
			FolderID: f.ID,
			Depth:    depth + 1,
		}

		if dest != "" {
			entry := &sqlitedelivery.CatalogEntry{
				Destination:    dest,
				Namespace:      namespace,
				Path:           childPath,
				FolderID:       f.ID,
				ParentFolderID: folderID,
				Source:         sqlitedelivery.SourceDiscovered,
				Status:         sqlitedelivery.StatusActive,
			}
			if _, upsertErr := env.Repo.Upsert(ctx, nil, entry); upsertErr != nil {
				result.Error = fmt.Sprintf("catalog write: %v", upsertErr)
				*env.Errors++
			} else {
				result.Source = "cataloged"
				*env.Discovered++
			}
		} else {
			result.Source = "discovered"
			*env.Discovered++
		}

		*env.Results = append(*env.Results, result)

		// Print progress.
		icon := "📁"
		if result.Error != "" {
			icon = "⚠️"
		} else if result.Source == "cataloged" {
			icon = "✅"
		}
		fmt.Printf("  %s %-40s  %s\n", icon, childPath, f.ID)
		if result.Error != "" {
			fmt.Printf("     ERROR: %s\n", result.Error)
		}

		// Recurse into subdirectories (non-fatal).
		_ = reconcileFolder(ctx, env, f.ID, childPath, namespace, depth+1, false)
	}

	return nil
}

// syncMediaAssetsToCatalog cross-references media_assets rows that have
// a non-empty drive_file_id against the drive_folder_catalog. For each
// asset whose parent folder is not yet in the catalog, it derives the
// destination from the asset's source/provider and inserts a catalog
// entry with source=discovered.
//
// godlike/07 minimum-blast-radius: read-only on media_assets; writes
// only to drive_folder_catalog via Upsert (idempotent).
func syncMediaAssetsToCatalog(ctx context.Context, db *sql.DB, repo *sqlitedelivery.Repository) (int, error) {
	if db == nil {
		return 0, fmt.Errorf("syncMediaAssets: db is nil")
	}

	// Query assets with a non-empty drive_file_id. The DISTINCT on
	// (source, provider) ensures we only create one catalog entry per
	// unique destination, even when thousands of assets share the same
	// source/provider pair.
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT ma.source, ma.provider
		FROM media_assets ma
		WHERE ma.drive_file_id IS NOT NULL AND ma.drive_file_id != ''
		ORDER BY ma.source, ma.provider
	`)
	if err != nil {
		return 0, fmt.Errorf("syncMediaAssets: query media_assets: %w", err)
	}
	defer rows.Close()

	synced := 0
	for rows.Next() {
		var source, provider string
		if err := rows.Scan(&source, &provider); err != nil {
			return synced, fmt.Errorf("syncMediaAssets: scan row: %w", err)
		}

		// Map source/provider to a destination key.
		dest := sourceToDestination(source, provider)
		if dest == "" {
			continue
		}
		namespace := destinationToNamespace(dest)
		if namespace == "" {
			continue
		}

		// Check whether the catalog already has an entry for this
		// specific (destination, path) pair — not just the destination.
		// A destination may have catalog entries for some paths but
		// still be missing the root-level namespace path.
		existing, findErr := repo.FindByDestinationAndPath(ctx, dest, namespace)
		if findErr != nil {
			continue // cannot determine existence; skip rather than risk duplicate
		}
		if existing != nil {
			continue // already cataloged with this path
		}

		// Insert a placeholder entry for this destination.
		// FolderID is empty: the operator should run drive-bootstrap
		// or another --apply reconcile to resolve actual folder IDs.
		entry := &sqlitedelivery.CatalogEntry{
			Destination:    dest,
			Namespace:      namespace,
			Path:           namespace,
			FolderID:       "",
			ParentFolderID: "",
			Source:         sqlitedelivery.SourceDiscovered,
			Status:         sqlitedelivery.StatusMissing,
		}
		if _, err := repo.Upsert(ctx, nil, entry); err != nil {
			continue // skip on write error; don't count as synced
		}
		synced++
	}
	if err := rows.Err(); err != nil {
		return synced, fmt.Errorf("syncMediaAssets: iterate rows: %w", err)
	}
	return synced, nil
}

// sourceToDestination maps a media_assets source/provider pair to a
// canonical DestinationKey. Only exact matches are mapped; substring
// heuristics are intentionally avoided to prevent false positives
// (e.g. "voiceover_image" should NOT map to "image").
// Returns "" if no canonical mapping exists.
func sourceToDestination(source, provider string) string {
	s := strings.ToLower(strings.TrimSpace(source))
	p := strings.ToLower(strings.TrimSpace(provider))

	// Canonical source values (exact matches only).
	switch s {
	case "youtube", "youtube_clip":
		return "youtube_clip"
	case "stock":
		return "stock"
	case "artlist":
		return "artlist"
	case "image":
		return "image"
	case "voiceover":
		return "voiceover"
	case "book":
		return "book"
	case "script":
		return "script"
	case "sound_effect":
		return "sound_effect"
	case "document":
		return "document"
	case "admin":
		return "admin"
	}

	// Provider-only inference (when source is not a canonical value).
	switch p {
	case "youtube":
		return "youtube_clip"
	case "stock", "pexels", "pixabay":
		return "stock"
	case "artlist":
		return "artlist"
	case "voiceover":
		return "voiceover"
	}
	return ""
}

// destinationToNamespace returns the canonical namespace for a
// DestinationKey. Mirrors canonicalDriveNamespaces.
func destinationToNamespace(dest string) string {
	for _, ns := range canonicalDriveNamespaces {
		if ns.Destination == dest {
			return ns.Namespace
		}
	}
	return ""
}
