// cmd/admin/internal/drive/drive_rename.go — drive-rename: rename Drive
// files and folders.
//
// Usage:
//
//	go run ./cmd/admin drive-rename --file <FILE_ID> --new-name "New Name" [--apply]
//	go run ./cmd/admin drive-rename --from <PARENT_ID> --name "Old Name" --new-name "New Name" [--apply]
//	go run ./cmd/admin drive-rename --rename "<FILE_ID>=<New Name>" [--rename "<ID2>=<New Name 2>"] [--apply]
//
//	--file             Drive file/folder ID to rename (requires --new-name)
//	--from / --name    resolve the target by its current name inside a parent
//	--rename           explicit FILE_ID=new-name pair (repeatable)
//	--new-name         new display name
//	--allow-collision  proceed even when a sibling already carries the target name
//	--apply            actually rename (default: dry-run)
//	--json             emit machine-readable JSON
//
// godlike/07 NO-FAKE-AVAILABILITY: dry-run is the default and the only mode
// that runs without --apply; every target is resolved (metadata + parent
// listing) before a single Files.Update is issued. Ambiguity fails closed: a
// --name that matches more than one file is an error, never a silent
// first-match.
//
// The collision guard exists because Drive happily allows two siblings with
// the same name, and the canonical folder lookup (Admin.GetOrCreateFolder via
// drive.EnsureFolderPath) is EXACT-match: a duplicate folder is invisible to
// the reuse path and silently forks the tree. Renaming into a name that a
// sibling already owns is therefore reported as `collision` and skipped
// unless the operator explicitly opts in with --allow-collision.
//
// Renames go through the canonical drive.FileLifecycle port (Rename), not the
// deprecated Admin.RenameFile, so the CLI and POST /api/drive/rename share one
// owner for the Drive file-mutation fact.
package drive

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	platformdrive "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

// rawListFlag is a repeatable flag that does NOT split on commas. Rename
// pairs carry a free-form new name, which may legitimately contain a comma —
// reusing stringListFlag (drive-mv) would silently truncate such a name.
type rawListFlag []string

func (s *rawListFlag) String() string { return strings.Join(*s, ",") }

func (s *rawListFlag) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// renameTarget is one validated rename request. When ID is empty the target
// is resolved by name (From + CurrentName) once Drive is available.
type renameTarget struct {
	ID          string `json:"id"`
	NewName     string `json:"new_name"`
	From        string `json:"from,omitempty"`
	CurrentName string `json:"current_name,omitempty"`
}

// parseDriveRenamePairs splits `--rename` values of the form
// "<FILE_ID>=<New Name>" into targets. Fail-closed on a missing separator or
// an empty side so a typo cannot drop the id and rename nothing.
func parseDriveRenamePairs(pairs []string) ([]renameTarget, error) {
	targets := make([]renameTarget, 0, len(pairs))
	for _, raw := range pairs {
		idx := strings.Index(raw, "=")
		if idx <= 0 || idx == len(raw)-1 {
			return nil, fmt.Errorf("drive-rename: --rename expects \"FILE_ID=New Name\", got %q", raw)
		}
		id := strings.TrimSpace(raw[:idx])
		newName := strings.TrimSpace(raw[idx+1:])
		if id == "" || newName == "" {
			return nil, fmt.Errorf("drive-rename: --rename expects non-empty id and name, got %q", raw)
		}
		targets = append(targets, renameTarget{ID: id, NewName: newName})
	}
	return targets, nil
}

// parseDriveRenameArgs normalises and validates the raw CLI inputs. Pure so it
// can be unit-tested without Drive.
func parseDriveRenameArgs(files, pairs []string, from, name, newName string) ([]renameTarget, error) {
	from = strings.TrimSpace(from)
	name = strings.TrimSpace(name)
	newName = strings.TrimSpace(newName)

	for _, f := range files {
		if strings.TrimSpace(f) == "" {
			return nil, fmt.Errorf("drive-rename: --file must not be blank")
		}
	}

	if len(pairs) > 0 {
		if len(files) > 0 || from != "" || name != "" || newName != "" {
			return nil, fmt.Errorf("drive-rename: use either --rename or --file/--from/--name, not both")
		}
		return parseDriveRenamePairs(pairs)
	}

	switch {
	case from != "" && name == "":
		return nil, fmt.Errorf("drive-rename: --from requires --name")
	case name != "" && from == "":
		return nil, fmt.Errorf("drive-rename: --name requires --from")
	case len(files) == 0 && name == "":
		return nil, fmt.Errorf("drive-rename: provide --file or --from + --name (or --rename pairs)")
	case newName == "":
		return nil, fmt.Errorf("drive-rename: --new-name is required")
	}

	// One --new-name applies to one target: handing the same name to several
	// --file ids would mint duplicates the collision guard cannot reason
	// about. Bulk renames use --rename pairs.
	if len(files) > 1 {
		return nil, fmt.Errorf("drive-rename: --new-name applies to a single --file; use --rename \"ID=Name\" pairs for bulk")
	}

	if name != "" {
		return []renameTarget{{From: from, CurrentName: name, NewName: newName}}, nil
	}
	return []renameTarget{{ID: strings.TrimSpace(files[0]), NewName: newName}}, nil
}

// renameDecision is the pure per-target outcome.
type renameDecision struct {
	Status     string // "planned" | "skipped" | "collision" | "error"
	Reason     string
	Collisions int
}

// countSiblingNameCollisions counts siblings in the target's parent that
// already carry newName (the target itself excluded).
func countSiblingNameCollisions(siblings []platformdrive.DriveFileInfo, targetID, newName string) int {
	count := 0
	for _, f := range siblings {
		if f.ID == targetID {
			continue
		}
		if f.Name == newName {
			count++
		}
	}
	return count
}

// decideRename is the pure policy applied to every target before any Drive
// write. Kept pure (no Drive, no logging) so the dry-run/apply parity is
// testable: the same decision drives both the report and --apply.
func decideRename(currentName string, trashed bool, siblings []platformdrive.DriveFileInfo, targetID, newName string, allowCollision bool) renameDecision {
	if newName == "" {
		return renameDecision{Status: "error", Reason: "empty new name"}
	}
	if trashed {
		return renameDecision{Status: "skipped", Reason: "file is in the Drive trash"}
	}
	if currentName == newName {
		return renameDecision{Status: "skipped", Reason: "already named " + strconv.Quote(newName)}
	}
	collisions := countSiblingNameCollisions(siblings, targetID, newName)
	if collisions > 0 && !allowCollision {
		return renameDecision{
			Status:     "collision",
			Collisions: collisions,
			Reason:     fmt.Sprintf("%d sibling(s) already named %q (pass --allow-collision to proceed)", collisions, newName),
		}
	}
	return renameDecision{Status: "planned", Collisions: collisions}
}

// renameResult is the per-target outcome of a drive-rename run.
type renameResult struct {
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	NewName    string `json:"new_name"`
	Parent     string `json:"parent,omitempty"`
	Status     string `json:"status"` // "renamed" | "planned" | "skipped" | "collision" | "error"
	Collisions int    `json:"collisions,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

type driveRenameReport struct {
	Applied    bool           `json:"applied"`
	Renamed    int            `json:"renamed"`
	Planned    int            `json:"planned"`
	Skipped    int            `json:"skipped"`
	Collisions int            `json:"collisions"`
	Errors     int            `json:"errors"`
	Results    []renameResult `json:"results"`
}

// RunDriveRename renames Drive files/folders reported by --file, --from/--name
// or --rename pairs. Dry-run unless --apply is passed.
func RunDriveRename(args []string) error {
	fs := flag.NewFlagSet("drive-rename", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var pairs rawListFlag
	fs.Var(&pairs, "rename", "explicit rename pair \"FILE_ID=New Name\" (repeatable)")
	var files stringListFlag
	fs.Var(&files, "file", "Drive file/folder ID to rename (requires --new-name)")
	from := fs.String("from", "", "parent folder ID used with --name")
	name := fs.String("name", "", "current name of the target inside --from")
	newName := fs.String("new-name", "", "new display name")
	allowCollision := fs.Bool("allow-collision", false, "proceed when a sibling already carries the target name")
	apply := fs.Bool("apply", false, "actually rename (default: dry-run)")
	jsonOut := fs.Bool("json", false, "emit machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	targets, err := parseDriveRenameArgs(files, pairs, *from, *name, *newName)
	if err != nil {
		return err
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	root, _, rootCleanup, err := wiring.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("drive-rename: initialize composition: %w", err)
	}
	defer rootCleanup()

	if root == nil || root.Drive == nil || root.Drive.Reader == nil {
		return fmt.Errorf("drive-rename: Drive reader port is not available")
	}
	reader := root.Drive.Reader

	var lifecycle platformdrive.FileLifecycle
	if *apply {
		if root.Drive.Lifecycle == nil {
			return fmt.Errorf("drive-rename: Drive lifecycle port is not available (cannot rename)")
		}
		lifecycle = root.Drive.Lifecycle
	}

	ctx, cancel := context.WithTimeout(cli.CmdContext(), 10*time.Minute)
	defer cancel()

	// Name-based targets can only be resolved once Drive is available.
	resolved := make([]renameTarget, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	for _, t := range targets {
		if t.ID == "" {
			lookup, lerr := reader.FindFileByName(ctx, t.From, t.CurrentName)
			if lerr != nil {
				return fmt.Errorf("drive-rename: lookup %q under %s: %w", t.CurrentName, t.From, lerr)
			}
			switch len(lookup.Matches) {
			case 0:
				return fmt.Errorf("drive-rename: no file named %q in folder %s", t.CurrentName, t.From)
			case 1:
				t.ID = lookup.Matches[0].FileID
			default:
				return fmt.Errorf("drive-rename: %d files named %q in folder %s (ambiguous; rename by --file ID instead)",
					len(lookup.Matches), t.CurrentName, t.From)
			}
		}
		if _, dup := seen[t.ID]; dup {
			continue
		}
		seen[t.ID] = struct{}{}
		resolved = append(resolved, t)
	}

	report := driveRenameReport{Applied: *apply, Results: make([]renameResult, 0, len(resolved))}
	for _, t := range resolved {
		res := renameResult{ID: t.ID, NewName: t.NewName}

		meta, merr := reader.GetFileMeta(ctx, t.ID)
		if merr != nil {
			res.Status = "error"
			res.Reason = merr.Error()
			report.Errors++
			report.Results = append(report.Results, res)
			continue
		}
		res.Name = meta.Name
		if len(meta.Parents) > 0 {
			res.Parent = meta.Parents[0]
		}

		// Collision guard: only meaningful when Drive reports a parent. An
		// inaccessible parent listing fails closed (no rename without the
		// duplicate check) rather than overwriting a sibling's identity.
		var siblings []platformdrive.DriveFileInfo
		if res.Parent != "" {
			sibs, serr := reader.ListFiles(ctx, res.Parent)
			if serr != nil {
				res.Status = "error"
				res.Reason = fmt.Sprintf("cannot verify sibling names in parent %s: %v", res.Parent, serr)
				report.Errors++
				report.Results = append(report.Results, res)
				continue
			}
			siblings = sibs
		}

		decision := decideRename(meta.Name, meta.Trashed, siblings, t.ID, t.NewName, *allowCollision)
		res.Status = decision.Status
		res.Reason = decision.Reason
		res.Collisions = decision.Collisions

		if decision.Status == "planned" && *apply {
			if rerr := lifecycle.Rename(ctx, t.ID, t.NewName); rerr != nil {
				res.Status = "error"
				res.Reason = rerr.Error()
			} else {
				res.Status = "renamed"
			}
		}

		switch res.Status {
		case "renamed":
			report.Renamed++
		case "planned":
			report.Planned++
		case "skipped":
			report.Skipped++
		case "collision":
			report.Collisions++
		default:
			report.Errors++
		}
		report.Results = append(report.Results, res)
	}

	if *jsonOut {
		printDriveRenameJSON(report)
	} else {
		printDriveRenameText(report)
	}

	switch {
	case report.Errors > 0:
		return fmt.Errorf("drive-rename: %d file(s) could not be renamed", report.Errors)
	case report.Collisions > 0:
		return fmt.Errorf("drive-rename: %d file(s) blocked by a sibling name collision", report.Collisions)
	}
	log.Debug("drive-rename complete", zap.Bool("applied", report.Applied), zap.Int("renamed", report.Renamed))
	return nil
}

func printDriveRenameText(report driveRenameReport) {
	mode := "DRY RUN"
	if report.Applied {
		mode = "APPLY"
	}
	fmt.Printf("Drive Rename (%s)\n\n", mode)
	for _, r := range report.Results {
		label := r.Name
		if label == "" {
			label = r.ID
		}
		switch r.Status {
		case "renamed":
			fmt.Printf("  ✅ renamed    %s (%s)  → %s\n", label, r.ID, r.NewName)
		case "planned":
			fmt.Printf("  ➕ plan       %s (%s)  → %s\n", label, r.ID, r.NewName)
		case "skipped":
			fmt.Printf("  ⏭  skip       %s (%s)  %s\n", label, r.ID, r.Reason)
		case "collision":
			fmt.Printf("  ⚠️  collision  %s (%s)  → %s  %s\n", label, r.ID, r.NewName, r.Reason)
		case "error":
			fmt.Printf("  ❌ error      %s (%s)  %s\n", label, r.ID, r.Reason)
		}
	}
	fmt.Printf("\nSummary: renamed=%d planned=%d skipped=%d collisions=%d errors=%d\n",
		report.Renamed, report.Planned, report.Skipped, report.Collisions, report.Errors)
	if !report.Applied && report.Planned > 0 {
		fmt.Println("Pass --apply to execute the renames.")
	}
}

func printDriveRenameJSON(report driveRenameReport) {
	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "drive-rename: marshal JSON: %v\n", err)
		return
	}
	fmt.Println(string(b))
}
