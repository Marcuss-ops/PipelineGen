// cmd/admin/internal/drive/drive_migrate_artlist.go — migrate-artlist-assets:
// move the children of the retired Artlist Drive root into the canonical
// stock root.
//
// Usage:
//
//	go run ./cmd/admin migrate-artlist-assets --from <LEGACY_ARTLIST_ROOT_ID> [--to <STOCK_ROOT_ID>] [--subfolder artlist] [--apply] [--json]
//
// The Artlist destination is retired: its dedicated Drive root folder
// (`drive.artlist_root_folder` / VELOX_DRIVE_ARTLIST_ROOT) and its local
// `media/artlist` storage path were removed, and the persisted destination
// key now folds into DeliveryDestinationStock
// (capabilities/delivery.CanonicalizeDestination). This command is the
// one-shot asset migration that keeps the files already living in the
// retired root reachable from the canonical stock tree: every child of
// --from is moved under <stock root>/<subfolder> (default "artlist"),
// preserving the per-asset folder names the catalog rows point at.
//
// godlike/07 NO-FAKE-AVAILABILITY:
//   - dry-run is the default; --apply is required to move anything;
//   - a name that already exists at the destination is reported as a
//     conflict and SKIPPED (never overwritten, never merged), so a second
//     run is a no-op and an ambiguous name stays a human decision;
//   - duplicate names inside the retired root are a conflict too: moving
//     both would recreate the very ambiguity the destination check exists
//     to prevent;
//   - any failed move is a non-zero exit, never a silent success.
package drive

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
	platformdrive "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

// defaultArtlistMigrationSubfolder is the folder created under the stock
// root. Legacy assets keep their historical name inside the canonical
// tree so the existing catalog rows stay meaningful.
const defaultArtlistMigrationSubfolder = "artlist"

// artlistMigrationTarget is the per-child outcome of a migration run.
type artlistMigrationTarget struct {
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
	Kind   string `json:"kind,omitempty"` // "folder" | "file"
	Status string `json:"status"`         // "moved" | "planned" | "skipped" | "conflict" | "error"
	Error  string `json:"error,omitempty"`
}

type artlistMigrationReport struct {
	From         string                   `json:"from"`
	DestFolderID string                   `json:"destination_folder_id,omitempty"`
	DestPath     string                   `json:"destination_path,omitempty"`
	Applied      bool                     `json:"applied"`
	Moved        int                      `json:"moved"`
	Planned      int                      `json:"planned"`
	Skipped      int                      `json:"skipped"`
	Conflicts    int                      `json:"conflicts"`
	Errors       int                      `json:"errors"`
	Results      []artlistMigrationTarget `json:"results"`
}

// migrationChild is the minimal per-child shape the planning rule needs.
// Kept local (and separate from platformdrive.DriveFileInfo) so the rule
// is unit-testable without a Drive client.
type migrationChild struct {
	ID       string
	Name     string
	IsFolder bool
}

// planArtlistMigration applies the migration rule to the children of the
// retired root:
//
//   - a child whose name already exists at the destination is a CONFLICT
//     (skipped — never overwritten, never merged);
//   - a duplicate name inside the source is a conflict too: only the first
//     occurrence is planned;
//   - every other child is planned (the caller decides whether to move it).
//
// existing holds the names already present in the destination folder.
// The result preserves the source listing order so the report is stable.
func planArtlistMigration(children []migrationChild, existing map[string]struct{}) []artlistMigrationTarget {
	targets := make([]artlistMigrationTarget, 0, len(children))
	seenSource := make(map[string]struct{}, len(children))

	for _, child := range children {
		name := strings.TrimSpace(child.Name)
		kind := "file"
		if child.IsFolder {
			kind = "folder"
		}
		target := artlistMigrationTarget{ID: child.ID, Name: name, Kind: kind}

		switch {
		case strings.TrimSpace(child.ID) == "":
			target.Status = "error"
			target.Error = "drive child has no id"
		case name == "":
			target.Status = "error"
			target.Error = "drive child has no name"
		default:
			if _, dup := seenSource[name]; dup {
				target.Status = "conflict"
				target.Error = "duplicate name in the retired root; resolve it by hand before migrating"
				break
			}
			seenSource[name] = struct{}{}
			if _, clash := existing[name]; clash {
				target.Status = "conflict"
				target.Error = "a child with this name already exists at the destination (not overwritten)"
				break
			}
			target.Status = "planned"
		}

		targets = append(targets, target)
	}
	return targets
}

// countArtlistMigration accumulates the report counters from the targets.
func countArtlistMigration(report *artlistMigrationReport) {
	for _, r := range report.Results {
		switch r.Status {
		case "moved":
			report.Moved++
		case "planned":
			report.Planned++
		case "skipped":
			report.Skipped++
		case "conflict":
			report.Conflicts++
		case "error":
			report.Errors++
		}
	}
}

// resolveDestFolder resolves the migration destination.
//
// On --apply the subfolder is created when missing; on a dry-run it is only
// looked up, and a missing subfolder simply means "empty conflict set".
// Returns the folder ID to list/move into ("" when it does not exist yet
// and we are not applying) and the human-readable destination path.
func resolveDestFolder(
	ctx context.Context,
	uploader *platformdrive.Uploader,
	destRoot, subfolder string,
	apply bool,
) (folderID, destPath string, err error) {
	folderID = destRoot
	destPath = destRoot
	sub := strings.TrimSpace(subfolder)
	if sub == "" {
		return folderID, destPath, nil
	}
	destPath = destRoot + "/" + sub

	if apply {
		folderID, err = uploader.GetOrCreateFolder(ctx, sub, destRoot)
		if err != nil {
			return "", destPath, fmt.Errorf("ensure destination folder %q under %s: %w", sub, destRoot, err)
		}
		return folderID, destPath, nil
	}

	lookup, err := uploader.FindFileByName(ctx, destRoot, sub)
	if err != nil {
		return "", destPath, fmt.Errorf("look up destination folder %q under %s: %w", sub, destRoot, err)
	}
	switch len(lookup.Matches) {
	case 0:
		return "", destPath, nil // created only on --apply
	case 1:
		return lookup.Matches[0].FileID, destPath, nil
	default:
		// Ambiguity fails closed: two candidates mean we cannot know which
		// tree the assets belong in.
		return "", destPath, fmt.Errorf("%d folders named %q under %s", len(lookup.Matches), sub, destRoot)
	}
}

// RunMigrateArtlistAssets is the CLI entry point.
func RunMigrateArtlistAssets(args []string) error {
	fs := flag.NewFlagSet("migrate-artlist-assets", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	from := fs.String("from", "", "retired Artlist root Drive folder ID (required)")
	to := fs.String("to", "", "canonical destination root folder ID (default: the configured stock root)")
	subfolder := fs.String("subfolder", defaultArtlistMigrationSubfolder,
		"child folder under the destination root (empty = move directly into the root)")
	apply := fs.Bool("apply", false, "actually move the assets (default: dry-run)")
	jsonOut := fs.Bool("json", false, "emit machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	fromID := strings.TrimSpace(*from)
	if fromID == "" {
		return fmt.Errorf("migrate-artlist-assets: --from <retired artlist root id> is required")
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	destRoot := strings.TrimSpace(*to)
	if destRoot == "" {
		destRoot = strings.TrimSpace(cfg.Drive.StockFolder())
	}
	if destRoot == "" {
		return fmt.Errorf("migrate-artlist-assets: no destination root: pass --to or configure the stock root folder")
	}
	if destRoot == fromID {
		return fmt.Errorf("migrate-artlist-assets: --from and --to are the same folder (%s)", fromID)
	}

	uploader, err := cli.BuildDriveAdminForCLI(cli.CmdContext(), cfg, log)
	if err != nil {
		return fmt.Errorf("migrate-artlist-assets: build Drive admin: %w", err)
	}

	ctx, cancel := context.WithTimeout(cli.CmdContext(), 30*time.Minute)
	defer cancel()

	children, err := uploader.ListFiles(ctx, fromID)
	if err != nil {
		return fmt.Errorf("migrate-artlist-assets: list retired root %s: %w", fromID, err)
	}

	destFolderID, destPath, err := resolveDestFolder(ctx, uploader, destRoot, *subfolder, *apply)
	if err != nil {
		return fmt.Errorf("migrate-artlist-assets: %w", err)
	}

	existing := make(map[string]struct{})
	if destFolderID != "" {
		existingChildren, lerr := uploader.ListFiles(ctx, destFolderID)
		if lerr != nil {
			return fmt.Errorf("migrate-artlist-assets: list destination %s: %w", destFolderID, lerr)
		}
		for _, child := range existingChildren {
			if name := strings.TrimSpace(child.Name); name != "" {
				existing[name] = struct{}{}
			}
		}
	}

	report := artlistMigrationReport{
		From:         fromID,
		DestFolderID: destFolderID,
		DestPath:     destPath,
		Applied:      *apply,
		Results:      planArtlistMigration(toMigrationChildren(children), existing),
	}

	if *apply {
		for i := range report.Results {
			res := &report.Results[i]
			if res.Status != "planned" {
				continue
			}
			if moveErr := uploader.MoveFile(ctx, res.ID, fromID, destFolderID); moveErr != nil {
				res.Status = "error"
				res.Error = moveErr.Error()
				continue
			}
			res.Status = "moved"
		}
	}

	countArtlistMigration(&report)

	if *jsonOut {
		printArtlistMigrationJSON(report)
	} else {
		printArtlistMigrationText(report)
	}

	switch {
	case report.Errors > 0:
		return fmt.Errorf("migrate-artlist-assets: %d asset(s) could not be migrated", report.Errors)
	case report.Conflicts > 0:
		// Conflicts are not fatal (a second run must stay green), but they
		// MUST be visible: the operator decides what to do with those names.
		log.Warn("migrate-artlist-assets: destination conflicts skipped",
			zap.Int("conflicts", report.Conflicts),
			zap.String("destination", destPath))
	}
	return nil
}

func toMigrationChildren(children []platformdrive.DriveFileInfo) []migrationChild {
	out := make([]migrationChild, 0, len(children))
	for _, child := range children {
		out = append(out, migrationChild{
			ID:       child.ID,
			Name:     child.Name,
			IsFolder: child.MimeType == driveFolderMimeType,
		})
	}
	return out
}

func printArtlistMigrationText(report artlistMigrationReport) {
	mode := "DRY RUN"
	if report.Applied {
		mode = "APPLY"
	}
	fmt.Printf("Artlist asset migration (%s) — %s → %s\n\n", mode, report.From, report.DestPath)
	for _, r := range report.Results {
		label := r.Name
		if label == "" {
			label = r.ID
		}
		switch r.Status {
		case "moved":
			fmt.Printf("  ✅ moved    %-7s %s (%s)\n", r.Kind, label, r.ID)
		case "planned":
			fmt.Printf("  ➕ plan     %-7s %s (%s)\n", r.Kind, label, r.ID)
		case "conflict":
			fmt.Printf("  ⚠️  conflict %-7s %s (%s)  %s\n", r.Kind, label, r.ID, r.Error)
		case "skipped":
			fmt.Printf("  ⏭  skip     %-7s %s (%s)  %s\n", r.Kind, label, r.ID, r.Error)
		case "error":
			fmt.Printf("  ❌ error    %-7s %s (%s)  %s\n", r.Kind, label, r.ID, r.Error)
		}
	}
	fmt.Printf("\nSummary: moved=%d planned=%d conflicts=%d skipped=%d errors=%d\n",
		report.Moved, report.Planned, report.Conflicts, report.Skipped, report.Errors)
	if len(report.Results) == 0 {
		fmt.Println("The retired root is empty — nothing to migrate.")
	}
	if !report.Applied && report.Planned > 0 {
		fmt.Println("Pass --apply to execute the moves.")
	}
}

func printArtlistMigrationJSON(report artlistMigrationReport) {
	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate-artlist-assets: marshal JSON: %v\n", err)
		return
	}
	fmt.Println(string(b))
}
