package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
)

func runUploadDriveFile(args []string) error {
	fs := flag.NewFlagSet("upload-drive-file", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	input := fs.String("input", "", "local file to upload")
	folderID := fs.String("folder-id", "", "Google Drive destination folder ID")
	filename := fs.String("filename", "", "destination filename on Drive")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *input == "" {
		return fmt.Errorf("-input is required")
	}
	if *folderID == "" {
		return fmt.Errorf("-folder-id is required")
	}
	if *filename == "" {
		return fmt.Errorf("-filename is required")
	}

	if _, err := os.Stat(*input); err != nil {
		return fmt.Errorf("input file unavailable: %w", err)
	}

	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("initialize composition: %w", err)
	}
	defer rootCleanup()

	if root == nil || root.Drive == nil || root.Drive.Publisher == nil {
		return fmt.Errorf("Drive publisher is required for upload")
	}
	adminUpload, err := delivery.NewAdminUploadService(root.Drive.Publisher)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	fmt.Printf("Uploading %s to Drive folder %s as %s...\n", *input, *folderID, *filename)
	result, err := adminUpload.Publish(ctx, delivery.AdminUploadCommand{
		LocalPath: *input,
		FolderID:  *folderID,
		Filename:  *filename,
	})
	if err != nil {
		return fmt.Errorf("upload file: %w", err)
	}
	if result == nil || strings.TrimSpace(result.FileID) == "" {
		return fmt.Errorf("upload completed without a Drive file ID")
	}

	fmt.Printf("File uploaded successfully: id=%s link=%s\n", result.FileID, result.WebViewLink)
	return nil
}
