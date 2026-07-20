package main

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
)

func runListCartoonFiles(args []string) error {
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

	cartoonRootID := "12AY75eIFSvtxbte1ECocZ2A7WXAwBC3Q"
	subfolders := []string{"Anime", "Meme", "Comico", "Cartoni"}

	fmt.Println("=== Files in Cartoon Root ===")
	files, _ := root.Drive.Reader.ListFiles(ctx, cartoonRootID)
	for _, f := range files {
		fmt.Printf("Root File: %q (ID: %s, Mime: %s)\n", f.Name, f.ID, f.MimeType)
	}

	for _, sf := range subfolders {
		folderID, err := root.Drive.Admin.GetOrCreateFolder(ctx, sf, cartoonRootID)
		if err != nil {
			continue
		}
		fmt.Printf("\n=== Files in Subfolder: %s (ID: %s) ===\n", sf, folderID)
		sfFiles, _ := root.Drive.Reader.ListFiles(ctx, folderID)
		for _, f := range sfFiles {
			fmt.Printf("  File: %q (ID: %s, Mime: %s)\n", f.Name, f.ID, f.MimeType)
		}
	}

	return nil
}
