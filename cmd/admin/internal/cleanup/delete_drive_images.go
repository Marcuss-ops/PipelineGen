package cleanup

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
)

// runDeleteDriveImages permanently deletes only image files directly inside
// the requested Drive folder. Other files (including videos and metadata)
// are intentionally left untouched.
func RunDeleteDriveImages(args []string) error {
	fs := flag.NewFlagSet("delete-drive-images", flag.ContinueOnError)
	folderID := fs.String("folder", "", "Drive folder ID whose direct image files must be permanently deleted")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*folderID) == "" {
		return fmt.Errorf("--folder is required")
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
		return fmt.Errorf("Drive reader and admin ports are required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	files, err := root.Drive.Reader.ListFiles(ctx, strings.TrimSpace(*folderID))
	if err != nil {
		return fmt.Errorf("list Drive folder: %w", err)
	}

	deleted := 0
	for _, file := range files {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(file.MimeType)), "image/") {
			continue
		}
		fmt.Printf("permanently delete image: %s (%s)\n", file.Name, file.ID)
		if err := root.Drive.Admin.DeleteFile(ctx, file.ID); err != nil {
			return fmt.Errorf("permanently delete image %s (%s): %w", file.Name, file.ID, err)
		}
		deleted++
	}
	fmt.Printf("Drive image cleanup complete: deleted=%d\n", deleted)
	return nil
}
