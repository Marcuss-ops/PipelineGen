package drive

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

const glitchDriveFolderID = "1fS8tKwcn019gFBUl5J3fLFBovN5ojvT7"

func RunKeepDriveFolderFiles(args []string) error {
	fs := flag.NewFlagSet("keep-drive-folder-files", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	folderID := fs.String("folder-id", glitchDriveFolderID, "Drive folder to clean")
	keep := fs.Int("keep", 10, "maximum number of direct files to keep")
	apply := fs.Bool("apply", false, "actually trash files (default: dry-run only)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keep < 0 {
		return fmt.Errorf("keep must be non-negative")
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
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	files, err := root.Drive.Reader.ListFiles(ctx, strings.TrimSpace(*folderID))
	if err != nil {
		return fmt.Errorf("list Drive folder: %w", err)
	}
	files = filterDriveNonFolders(files)
	sort.Slice(files, func(i, j int) bool { return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name) })
	fmt.Printf("Drive folder=%s files=%d keep=%d\n", *folderID, len(files), *keep)
	if len(files) <= *keep {
		fmt.Println("No cleanup needed")
		return nil
	}
	for _, file := range files[*keep:] {
		fmt.Printf("trash: %s (%s)\n", file.Name, file.ID)
	}
	if !*apply {
		fmt.Println("Dry run only. Re-run with --apply to trash files.")
		return nil
	}
	for _, file := range files[*keep:] {
		if err := root.Drive.Admin.TrashFile(ctx, file.ID); err != nil {
			return fmt.Errorf("trash %q: %w", file.Name, err)
		}
	}
	fmt.Printf("Drive folder cleanup complete: kept=%d trashed=%d\n", *keep, len(files)-*keep)
	return nil
}

func filterDriveNonFolders(files []drive.DriveFileInfo) []drive.DriveFileInfo {
	result := make([]drive.DriveFileInfo, 0, len(files))
	for _, file := range files {
		if file.MimeType != "application/vnd.google-apps.folder" {
			result = append(result, file)
		}
	}
	return result
}
