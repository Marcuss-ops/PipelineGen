package main

import (
	"flag"
	"fmt"
	"os"

	"go.uber.org/zap"

	"velox/go-master/internal/app"
)

func runListDriveFolder(args []string) error {
	fs := flag.NewFlagSet("list-drive-folder", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	folder := fs.String("folder", "", "Drive folder ID to list")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *folder == "" {
		fmt.Fprintf(os.Stderr, "Usage: admin list-drive-folder --folder FOLDER_ID\n")
		fs.PrintDefaults()
		return nil
	}

	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	deps, coreCleanup, err := app.ExportInitCoreMinimal(cfg, log)
	if err != nil {
		log.Fatal("Failed to initialize core services", zap.Error(err))
	}
	defer coreCleanup()

	if deps.DriveClient == nil {
		return fmt.Errorf("drive client is not available")
	}

	ctx := cmdContext()

	query := fmt.Sprintf("'%s' in parents and trashed = false", *folder)
	list, err := deps.DriveClient.Files.List().Q(query).
		Fields("files(id, name, mimeType, size)").
		PageSize(1000).
		Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to list folder: %w", err)
	}

	fmt.Printf("Folder: %s\n", *folder)
	fmt.Printf("Found %d items:\n", len(list.Files))
	for _, f := range list.Files {
		mime := f.MimeType
		size := ""
		if f.Size > 0 {
			size = fmt.Sprintf(" (%d bytes)", f.Size)
		}
		fmt.Printf("  %s (%s) [%s]%s\n", f.Name, f.Id, mime, size)
	}

	return nil
}
