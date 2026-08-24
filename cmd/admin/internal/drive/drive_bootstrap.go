// cmd/admin/drive_bootstrap.go — F4: Drive bootstrap CLI command (July 2026)
//
// Creates or verifies the 10 canonical subdirectories under a Drive root
// folder and populates the drive_folder_catalog table so the publisher
// can resolve folder IDs without calling Drive's folder listing API.
//
// Usage:
//
//		go run ./cmd/admin drive-bootstrap --root "ROOT_FOLDER_ID" [--apply]
//
//	  --root    Drive folder ID of the unified media root (required)
//	  --apply   Actually create folders (default: dry-run, prints what
//	            would be created)
//	  --db-path Override canonical SQLite DB path
//
// Canonical 10 subdirectories:
//
//	clips, stock, artlist, images, voiceovers, books, scripts,
//	sound_effects, documents, admin
//
// Each maps to its corresponding DestinationKey namespace (F2).
//
// godlike/06 SSOT (one canonical owner per fact):
//   - canonicalDriveNamespaces is the canonical list of destination→namespace
//     mappings. Both drive-bootstrap and drive-doctor consume it.
//   - The bootstrap CLI is the canonical SOLE writer of the bootstrap source
//     in drive_folder_catalog. Folders created by this command are marked
//     source=bootstrap.
//
// godlike/07 NO-FAKE-AVAILABILITY: --dry-run is the default (no Drive
// writes, no catalog writes). --apply is the operator-explicit opt-in.
package drive

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	sqlitedelivery "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/delivery"
)

// ErrAdminNoDB is surfaced by admin CLI commands when the canonical DB
// path cannot be resolved. Shared by drive-bootstrap, drive-doctor, and
// zombie-sweep (which wraps it in its own sentinel).
var ErrAdminNoDB = errors.New("admin: canonical DB path not configured (set VELOX_DATA_DIR or check cfg.Storage.PrimaryDBPath)")

// ErrDriveBootstrapNoRoot is surfaced when --root is empty or missing.
var ErrDriveBootstrapNoRoot = errors.New("drive-bootstrap: --root is required (Drive folder ID of the unified media root)")

// canonicalDriveNamespaces is the ordered list of 10 canonical Drive
// subdirectories created under the unified media root. Shared by
// drive-bootstrap and drive-doctor per godlike/06 SSOT.
var canonicalDriveNamespaces = []struct {
	Namespace   string
	Destination string
}{
	{"clips", "youtube_clip"},
	{"stock", "stock"},
	{"artlist", "artlist"},
	{"images", "image"},
	{"voiceovers", "voiceover"},
	{"books", "book"},
	{"scripts", "script"},
	{"sound_effects", "sound_effect"},
	{"documents", "document"},
	{"admin", "admin"},
}

type bootstrapResult struct {
	Namespace string
	FolderID  string
	Error     string // empty on success
}

func RunDriveBootstrap(args []string) error {
	fs := flag.NewFlagSet("drive-bootstrap", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	rootID := fs.String("root", "", "Drive folder ID of the unified media root (required)")
	apply := fs.Bool("apply", false, "Actually create folders and write to catalog (default: dry-run only)")
	dbPath := fs.String("db-path", "", "Canonical SQLite DB path (default: $VELOX_DATA_DIR/media.db.sqlite)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if strings.TrimSpace(*rootID) == "" {
		return ErrDriveBootstrapNoRoot
	}
	*rootID = strings.TrimSpace(*rootID)

	// Dry-run: just print the planned structure.
	if !*apply {
		fmt.Print(formatBootstrapDryRunOutput(*rootID))
		return nil
	}

	// --apply path: load config, open Drive, create folders, populate catalog.
	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	return executeBootstrap(cli.CmdContext(), cfg, log, *rootID, *dbPath)
}

func executeBootstrap(ctx context.Context, cfg *config.Config, log *zap.Logger, rootID, dbPathFlag string) error {
	// Resolve DB path.
	path := cli.ResolveDBPath(cfg, dbPathFlag)
	if path == "" {
		return ErrAdminNoDB
	}

	sqliteDB, err := storage.OpenSQLiteDB(path, log)
	if err != nil {
		return fmt.Errorf("drive-bootstrap: open DB: %w", err)
	}
	defer sqliteDB.Close()

	catalogRepo := sqlitedelivery.NewRepository(sqliteDB.DB)

	// Build Drive admin client — mirrors build_bundles_drive.go pattern.
	driveAdmin, err := cli.BuildDriveAdminForCLI(ctx, cfg, log)
	if err != nil {
		return fmt.Errorf("drive-bootstrap: init Drive client: %w", err)
	}

	fmt.Println("Drive Bootstrap")
	fmt.Printf("Root: %s\n\n", rootID)

	var results []bootstrapResult
	created := 0
	failed := 0

	for _, ns := range canonicalDriveNamespaces {
		result := bootstrapResult{Namespace: ns.Namespace}

		folderID, err := drive.EnsureFolderPath(ctx, driveAdmin, rootID, ns.Namespace)
		if err != nil {
			result.Error = err.Error()
			failed++
			results = append(results, result)
			fmt.Printf("  ❌ %-20s error: %v\n", ns.Namespace, err)
			continue
		}

		result.FolderID = folderID

		// Populate catalog.
		entry := &sqlitedelivery.CatalogEntry{
			Destination:    ns.Destination,
			Namespace:      ns.Namespace,
			Path:           ns.Namespace,
			FolderID:       folderID,
			ParentFolderID: rootID,
			Source:         sqlitedelivery.SourceBootstrap,
			Status:         sqlitedelivery.StatusActive,
		}
		if _, upsertErr := catalogRepo.Upsert(ctx, nil, entry); upsertErr != nil {
			result.Error = fmt.Sprintf("folder created but catalog write failed: %v", upsertErr)
			failed++
			results = append(results, result)
			fmt.Printf("  ⚠️  %-20s folder %s created, but catalog write failed: %v\n", ns.Namespace, folderID, upsertErr)
			continue
		}

		created++
		results = append(results, result)
		fmt.Printf("  ✅ %-20s folder: %s\n", ns.Namespace, folderID)
	}
	_ = results

	fmt.Printf("\nStatus: %d created/verified, %d failed\n", created, failed)
	if failed > 0 {
		return fmt.Errorf("drive-bootstrap: %d folder(s) failed", failed)
	}
	return nil
}

// buildDriveAdminForCLI constructs a *drive.Uploader for admin CLI commands.
// Mirrors the pattern in internal/app/build_bundles_drive.go:
// NewDriveServiceFromFiles + &drive.Uploader{Service, Log}.
// Returns the concrete *Uploader so callers can use methods from both
// drive.Admin (folder CRUD) and drive.Reader (ListFiles, SearchFiles).
func BuildDriveAdminForCLI(ctx context.Context, cfg *config.Config, log *zap.Logger) (*drive.Uploader, error) {
	svc, err := drive.NewDriveServiceFromFiles(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("NewDriveServiceFromFiles: %w", err)
	}
	return &drive.Uploader{Service: svc, Log: log}, nil
}

// formatBootstrapDryRunOutput returns the human-readable dry-run output
// for the given root folder ID. Pure function (no I/O) so callers can
// test the format without standing up Drive or DB.
//
// godlike/07 minimum-blast-radius: a future refactor that changes the
// format will break the byte-stable test, forcing the operator to
// consciously ack the change.
func formatBootstrapDryRunOutput(rootID string) string {
	var b strings.Builder
	fmt.Fprintln(&b, "Drive Bootstrap — DRY RUN")
	fmt.Fprintf(&b, "Root: %s\n\n", rootID)
	fmt.Fprintln(&b, "Would create/verify the following 10 canonical subdirectories:")
	for _, ns := range canonicalDriveNamespaces {
		fmt.Fprintf(&b, "  ✅ %-20s → %s/  (destination: %s)\n", ns.Namespace, ns.Namespace, ns.Destination)
	}
	fmt.Fprintln(&b, "\nPass --apply to execute the bootstrap.")
	return b.String()
}
