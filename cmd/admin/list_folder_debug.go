package main

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
)

func runListFolderDebug(args []string) error {
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

	if root.Drive == nil || root.Drive.Reader == nil {
		return fmt.Errorf("drive reader not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	folderID := "12AY75eIFSvtxbte1ECocZ2A7WXAwBC3Q"
	if len(args) > 0 {
		folderID = args[0]
	}

	fmt.Printf("Listing files in folder: %s\n", folderID)
	files, err := root.Drive.Reader.ListFiles(ctx, folderID)
	if err != nil {
		return fmt.Errorf("list files: %w", err)
	}

	for _, f := range files {
		fmt.Printf("File: name=%q id=%q mime=%q\n", f.Name, f.ID, f.MimeType)
	}
	return nil
}
