package deletion

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"
)

// CleanupOrphanFiles removes files not referenced by the maintenance
// projection. It does not depend on mutation or completion ports.
func (s *DeletionService) CleanupOrphanFiles(ctx context.Context, assetsDir string, dryRun bool) (int, error) {
	s.log.Info("starting deep orphan file cleanup", zap.String("dir", assetsDir), zap.Bool("dry_run", dryRun))
	if s.maintenance.AssetIndex == nil {
		return 0, fmt.Errorf("deletion maintenance: asset index reader not wired")
	}
	dbAssets, err := s.maintenance.AssetIndex.ListAll(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to list assets from DB: %w", err)
	}

	referencedPaths := make(map[string]bool)
	for _, record := range dbAssets {
		if record != nil && record.LocalPath != "" {
			absPath, _ := filepath.Abs(record.LocalPath)
			referencedPaths[absPath] = true
		}
	}

	var deletedCount int
	err = filepath.Walk(assetsDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		absPath, _ := filepath.Abs(path)
		if referencedPaths[absPath] {
			return nil
		}
		s.log.Info("found orphan file", zap.String("path", path))
		if dryRun {
			deletedCount++
			return nil
		}
		if err := os.Remove(path); err != nil {
			s.log.Error("failed to delete orphan file", zap.String("path", path), zap.Error(err))
			return nil
		}
		deletedCount++
		return nil
	})
	return deletedCount, err
}
