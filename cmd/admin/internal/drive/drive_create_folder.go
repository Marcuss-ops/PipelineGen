package drive

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	pathutil "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
)

// folderPlanStep is one segment of a dry-run folder plan.
type folderPlanStep struct {
	// Segment is the raw operator-supplied segment; Name is the canonical
	// (SafeFolderName) form actually used for lookup/creation.
	Segment  string `json:"segment"`
	Name     string `json:"name"`
	Status   string `json:"status"` // "exists" | "create" | "ambiguous" | "blocked"
	FolderID string `json:"folder_id,omitempty"`
	// Count is populated for the "ambiguous" status (number of matching
	// child folders Drive returned).
	Count int    `json:"count,omitempty"`
	Path  string `json:"path"`
}

// folderPlan is the full dry-run plan for a nested --name.
type folderPlan struct {
	ParentID string           `json:"parent_id"`
	Segments []folderPlanStep `json:"segments"`
}

// RunDriveCreateFolder creates (or reuses) a Drive folder under --parent.
//
// --name accepts a nested path separated by "/" (e.g. "job/it"), so the
// command can create arbitrary depth in one shot. Each segment is
// resolved through `drive.EnsureFolderPath` → `Admin.GetOrCreateFolder`,
// which is exact-match, idempotent and fail-closed on ambiguous names.
//
// --dry-run reports which segments already exist and which would be
// created, without writing anything to Drive.
func RunDriveCreateFolder(args []string) error {
	fs := flag.NewFlagSet("drive-create-folder", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	parent := fs.String("parent", "", "parent Google Drive folder ID")
	name := fs.String("name", "", "folder name; use '/' for a nested path (e.g. \"job/it\")")
	dryRun := fs.Bool("dry-run", false, "Show what would be created without writing to Drive")
	if err := fs.Parse(args); err != nil {
		return err
	}

	parentID := strings.TrimSpace(*parent)
	if parentID == "" {
		return fmt.Errorf("drive-create-folder: --parent is required")
	}
	segments := splitFolderPath(*name)
	if len(segments) == 0 {
		return fmt.Errorf("drive-create-folder: --name is required (non-empty folder name or path)")
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	admin, err := cli.BuildDriveAdminForCLI(cli.CmdContext(), cfg, log)
	if err != nil {
		return fmt.Errorf("drive-create-folder: build Drive admin: %w", err)
	}

	ctx := cli.CmdContext()

	if *dryRun {
		plan, planErr := planFolderPath(ctx, admin, parentID, segments)
		if planErr != nil {
			return fmt.Errorf("drive-create-folder: %w", planErr)
		}
		printFolderPlanText(plan)
		return nil
	}

	id, err := drive.EnsureFolderPath(ctx, admin, parentID, segments...)
	if err != nil {
		return fmt.Errorf("drive-create-folder: %w", err)
	}

	fmt.Printf("folder_id=%s\nfolder_name=%s\nfolder_path=%s\nparent_id=%s\nurl=https://drive.google.com/drive/folders/%s\n",
		id, segments[len(segments)-1], joinFolderPath(segments), parentID, id)
	return nil
}

// planFolderPath resolves a nested path segment-by-segment WITHOUT writing
// to Drive. For each segment it reports whether an exact-match folder
// already exists under the resolved parent ("exists", with its ID) or
// would be created ("create"). Once a segment is missing (or ambiguous),
// the remaining segments cannot be resolved against Drive and are reported
// as "create" against the not-yet-existing parent.
//
// The comparison uses the same canonicalisation as production
// (pathutil.SafeFolderName) and the same folder MIME filter, so the plan
// matches what EnsureFolderPath would actually do.
func planFolderPath(ctx context.Context, reader drive.Reader, parentID string, segments []string) (folderPlan, error) {
	plan := folderPlan{ParentID: parentID, Segments: make([]folderPlanStep, 0, len(segments))}
	current := parentID
	unresolved := false
	pathSoFar := ""

	for _, seg := range segments {
		canonical := pathutil.SafeFolderName(seg)
		step := folderPlanStep{Segment: seg, Name: canonical}
		if pathSoFar == "" {
			step.Path = canonical
		} else if canonical == "" {
			step.Path = pathSoFar
		} else {
			step.Path = pathSoFar + "/" + canonical
		}
		if canonical != "" {
			pathSoFar = step.Path
		}

		switch {
		case canonical == "":
			step.Status = "blocked"
			unresolved = true
		case unresolved || current == "":
			step.Status = "create"
		default:
			existingID, count, err := findChildFolderByName(ctx, reader, current, canonical)
			if err != nil {
				return plan, err
			}
			switch count {
			case 0:
				step.Status = "create"
				unresolved = true
			case 1:
				step.Status = "exists"
				step.FolderID = existingID
				current = existingID
			default:
				step.Status = "ambiguous"
				step.Count = count
				unresolved = true
			}
		}
		plan.Segments = append(plan.Segments, step)
	}
	return plan, nil
}

// findChildFolderByName returns the ID and count of non-trashed child
// FOLDERS of parentID whose name exactly equals canonicalName.
func findChildFolderByName(ctx context.Context, reader drive.Reader, parentID, canonicalName string) (string, int, error) {
	files, err := reader.ListFiles(ctx, parentID)
	if err != nil {
		return "", 0, fmt.Errorf("list children of %s: %w", parentID, err)
	}
	var id string
	count := 0
	for _, f := range files {
		if f.MimeType == driveFolderMimeType && f.Name == canonicalName {
			count++
			if id == "" {
				id = f.ID
			}
		}
	}
	return id, count, nil
}

func printFolderPlanText(plan folderPlan) {
	fmt.Println("Drive Create Folder — DRY RUN")
	fmt.Printf("Parent: %s\n\n", plan.ParentID)
	for _, s := range plan.Segments {
		switch s.Status {
		case "exists":
			fmt.Printf("  ✅ %-30s exists  %s  (reuse)\n", s.Path, s.FolderID)
		case "create":
			fmt.Printf("  ➕ %-30s create\n", s.Path)
		case "ambiguous":
			fmt.Printf("  ⚠️  %-30s ambiguous (%d folders named %q)\n", s.Path, s.Count, s.Name)
		case "blocked":
			fmt.Printf("  ❌ %-30s blocked (empty canonical name)\n", s.Path)
		}
	}
	fmt.Println("\nNo changes were made (dry run). Re-run without --dry-run to create.")
}
