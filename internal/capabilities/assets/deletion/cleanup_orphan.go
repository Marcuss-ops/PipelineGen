package deletion

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"
)

// CleanupOrphanFiles scans assetsDir and removes any file not
// referenced by an asset in the database. dryRun=true only logs
// and counts candidates without removing them.
func (s *DeletionService) CleanupOrphanFiles(ctx context.Context, assetsDir string, dryRun bool) (int, error) {
	s.log.Info("starting deep orphan file cleanup", zap.String("dir", assetsDir), zap.Bool("dry_run", dryRun))

	// 1. Get all assets from database
	dbAssets, err := s.assetIndexSvc.ListAll(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to list assets from DB: %w", err)
	}

	// Build map of absolute local paths for fast lookup
	referencedPaths := make(map[string]bool)
	for _, asset := range dbAssets {
		if asset.LocalPath != "" {
			absPath, _ := filepath.Abs(asset.LocalPath)
			referencedPaths[absPath] = true
		}
	}

	// 2. Scan directory
	var deletedCount int
	err = filepath.Walk(assetsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		absPath, _ := filepath.Abs(path)
		if !referencedPaths[absPath] {
			s.log.Info("found orphan file", zap.String("path", path))
			if !dryRun {
				if err := os.Remove(path); err != nil {
					s.log.Error("failed to delete orphan file", zap.String("path", path), zap.Error(err))
				} else {
					deletedCount++
				}
			} else {
				deletedCount++
			}
		}
		return nil
	})

	return deletedCount, err
}
