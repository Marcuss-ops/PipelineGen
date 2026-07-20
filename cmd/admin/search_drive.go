package main

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
)

func runSearchDrive(args []string) error {
	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		return err
	}
	defer rootCleanup()

	ctx := context.Background()

	fmt.Println("Listing all files under 1vfZQHVNZab-pU2fBaj4qzR3iSz1sOVhW:")
	files, err := root.Drive.Reader.ListFiles(ctx, "1vfZQHVNZab-pU2fBaj4qzR3iSz1sOVhW")
	if err != nil {
		return err
	}
	for _, f := range files {
		fmt.Printf("  File: %q (ID: %s, Mime: %s)\n", f.Name, f.ID, f.MimeType)
	}

	return nil
}
