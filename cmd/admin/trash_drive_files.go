package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
)

func runTrashDriveFiles(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("at least one Drive file ID is required")
	}
	permanent := false
	if strings.TrimSpace(args[0]) == "--permanent" {
		permanent = true
		args = args[1:]
	}
	if len(args) == 0 {
		return fmt.Errorf("at least one Drive file ID is required")
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
	if root == nil || root.Drive == nil || root.Drive.Admin == nil || root.Drive.Reader == nil {
		return fmt.Errorf("Drive reader and admin are required")
	}
	if permanent && root.Drive.Lifecycle == nil {
		return fmt.Errorf("Drive lifecycle is required for permanent deletion")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	seen := make(map[string]struct{})
	errorsCount := 0
	for _, rawID := range args {
		id := strings.TrimSpace(rawID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		meta, err := root.Drive.Reader.GetFileMeta(ctx, id)
		if err != nil {
			if permanent && strings.Contains(err.Error(), "404") {
				fmt.Printf("already permanently absent: %s\n", id)
				continue
			}
			return fmt.Errorf("verify Drive file %s: %w", id, err)
		}
		if permanent {
			fmt.Printf("permanently delete: %s (%s)\n", meta.Name, id)
			if err := root.Drive.Lifecycle.Delete(ctx, id); err != nil {
				fmt.Printf("permanent delete failed: %s: %v\n", id, err)
				errorsCount++
				continue
			}
			continue
		}
		if meta.Trashed {
			fmt.Printf("already in trash: %s (%s)\n", meta.Name, id)
			continue
		}
		fmt.Printf("trash: %s (%s)\n", meta.Name, id)
		if err := root.Drive.Admin.TrashFile(ctx, id); err != nil {
			return fmt.Errorf("trash Drive file %s: %w", id, err)
		}
	}
	if permanent {
		fmt.Printf("Drive permanent cleanup complete: requested=%d failed=%d\n", len(seen), errorsCount)
	} else {
		fmt.Printf("Drive cleanup complete: trashed=%d\n", len(seen))
	}
	return nil
}
