package assets

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"
)

// cleanupStaleTempFiles removes files older than maxAge from tempDir (recursive).
// Pulled out of service.go so the file stays short.
func (s *Service) cleanupStaleTempFiles(ctx context.Context, tempDir string, maxAge time.Duration, dryRun bool) (int, error) {
	var deleted int
	cutoff := time.Now().Add(-maxAge)

	err := filepath.Walk(tempDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if info.IsDir() {
			return nil // skip directories (will be cleaned up if empty after files gone)
		}
		if info.ModTime().After(cutoff) {
			return nil
		}

		if dryRun {
			s.log.Info("[DRY RUN] would delete stale temp file",
				zap.String("file", path),
				zap.Time("modified", info.ModTime()))
		} else {
			if err := os.Remove(path); err != nil {
				s.log.Warn("failed to delete stale temp file",
					zap.String("file", path),
					zap.Error(err))
				return nil
			}
			s.log.Info("deleted stale temp file",
				zap.String("file", info.Name()),
				zap.Time("modified", info.ModTime()))
		}
		deleted++
		return nil
	})
	if err != nil {
		return deleted, fmt.Errorf("walk failed: %w", err)
	}

	return deleted, nil
}
