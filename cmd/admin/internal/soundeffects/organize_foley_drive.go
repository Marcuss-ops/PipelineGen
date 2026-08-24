package soundeffects

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

const foleyDriveFolderID = "1bCGQCeS8mxsTbqRcGktezbsJ3eTrAyWP"

type foleyDriveMove struct {
	file  drive.DriveFileInfo
	group string
}

func RunOrganizeFoleyDrive(args []string) error {
	fs := flag.NewFlagSet("organize-foley-drive", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fsFolder := fs.String("folder-id", foleyDriveFolderID, "Drive folder to organize")
	apply := fs.Bool("apply", false, "create subfolders and move files (default: dry-run only)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *fsFolder == "" {
		return fmt.Errorf("folder-id is required")
	}
	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()
	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("initialize composition: %w", err)
	}
	defer rootCleanup()
	if root == nil || root.Drive == nil || root.Drive.Reader == nil || root.Drive.Admin == nil {
		return fmt.Errorf("Drive reader and admin are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	files, err := root.Drive.Reader.ListFiles(ctx, *fsFolder)
	if err != nil {
		return fmt.Errorf("list Foley folder: %w", err)
	}
	moves := make([]foleyDriveMove, 0, len(files))
	counts := make(map[string]int)
	for _, file := range files {
		if file.MimeType == "application/vnd.google-apps.folder" {
			continue
		}
		group := classifyFoleyDriveName(file.Name)
		moves = append(moves, foleyDriveMove{file: file, group: group})
		counts[group]++
	}
	keys := make([]string, 0, len(counts))
	for group := range counts {
		keys = append(keys, group)
	}
	sort.Strings(keys)
	fmt.Printf("Foley root=%s direct_files=%d\n", *fsFolder, len(moves))
	for _, group := range keys {
		fmt.Printf("  %-20s %d\n", group, counts[group])
	}
	if !*apply {
		fmt.Println("Dry run only. Re-run with --apply to create folders and move files.")
		return nil
	}
	if len(moves) == 0 {
		return nil
	}
	folders := make(map[string]string, len(keys))
	for _, group := range keys {
		folderID, err := root.Drive.Admin.GetOrCreateFolder(ctx, group, *fsFolder)
		if err != nil {
			return fmt.Errorf("ensure Foley subfolder %s: %w", group, err)
		}
		folders[group] = folderID
	}
	for _, item := range moves {
		if err := root.Drive.Admin.MoveFile(ctx, item.file.ID, *fsFolder, folders[item.group]); err != nil {
			return fmt.Errorf("move Foley file %q to %s: %w", item.file.Name, item.group, err)
		}
	}
	fmt.Printf("Foley organization complete: subfolders=%d moved=%d\n", len(folders), len(moves))
	return nil
}

func classifyFoleyDriveName(name string) string {
	text := strings.ToLower(name)
	switch {
	case containsAny(text, "camera", "shutter", "flash"):
		return "Camera"
	case containsAny(text, "keyboard", "typewriter", "typing", "writing", "paper", "pencil"):
		return "Typing & Paper"
	case containsAny(text, "clock", "projector", "gear", "mechanical", "bike", "gun", "metal"):
		return "Mechanical & Machines"
	case containsAny(text, "crowd", "applause", "cheering", "sigh", "gasp", "dog", "animal"):
		return "Human & Animal"
	case containsAny(text, "horn", "bell", "whistle", "party blower"):
		return "Horns & Bells"
	case containsAny(text, "glass", "bone", "break", "shatter", "crunch", "cork", "pop"):
		return "Breakage & Pops"
	default:
		return "Other Foley"
	}
}

func containsAny(text string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}
