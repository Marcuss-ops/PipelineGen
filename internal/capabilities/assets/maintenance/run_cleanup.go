package maintenance

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
)

// RunCleanup performs a full system cleanup.
//
// Phase order is significant:
//  1. Orphan files (local) — quick, deletes files on disk that no DB row references.
//  2. Deep consistency check — scans DB rows for orphans (only when deep=true).
//  3. Stale temp files — files older than 7 days in the temp directory.
//  4. DB optimisation — if not dryRun and at least one DB is configured: retention
//     delete, WAL checkpoint, then incremental-or-full VACUUM.
//
// We split each phase into its own file (run_cleanup.go, deep_cleanup.go,
// db_maintenance.go, temp_cleanup.go) so this function stays a thin
// orchestrator that reads top-to-bottom like a runbook.
func (s *Service) RunCleanup(ctx context.Context, deep bool, dryRun bool) (map[string]any, error) {
	s.log.Info("Starting system-wide cleanup", zap.Bool("deep", deep), zap.Bool("dry_run", dryRun))

	results := make(map[string]any)

	if s.deletionSvc == nil {
		s.log.Error("Deletion service not available for cleanup")
		return nil, fmt.Errorf("deletion service not initialized")
	}

	// 1. Orphan file cleanup
	assetsDir := s.cfg.Storage.AssetsPath()
	deleted, err := s.deletionSvc.CleanupOrphanFiles(ctx, assetsDir, dryRun)
	if err != nil {
		s.log.Error("Orphan file cleanup failed", zap.Error(err))
		results["orphan_cleanup_error"] = err.Error()
	} else {
		results["orphan_files_deleted"] = deleted
	}

	// 2. Asset Tree / Index consistency check
	if deep {
		s.log.Info("Deep consistency check started")
		deepResults, deepErr := s.runDeepCleanup(ctx, dryRun)
		if deepErr != nil {
			s.log.Error("deep_cleanup failed", zap.Error(deepErr))
			results["deep_cleanup_error"] = deepErr.Error()
		} else {
			for k, v := range deepResults {
				results["deep_cleanup_"+k] = v
			}
			results["deep_cleanup"] = "ok"
		}
	}

	// 3. Stale temp files cleanup (files older than 7 days)
	tempDir := s.cfg.Storage.TempPath()
	if _, err := os.Stat(tempDir); err == nil {
		deleted, err := s.cleanupStaleTempFiles(ctx, tempDir, 7*24*time.Hour, dryRun)
		if err != nil {
			s.log.Warn("temp file cleanup warning", zap.Error(err))
			results["temp_cleanup_error"] = err.Error()
		} else {
			results["temp_files_deleted"] = deleted
			s.log.Info("temp files cleanup completed", zap.Int("files_deleted", deleted))
		}
	} else {
		s.log.Debug("temp dir does not exist, skipping", zap.String("temp_dir", tempDir))
	}

	// 4. DB retention / WAL checkpoint / VACUUM
	if len(s.repos) > 0 && !dryRun {
		s.log.Info("Running database optimization and cleanup tasks")
		for i, repo := range s.repos {
			if repo == nil {
				continue
			}
			s.runDBOptimize(ctx, i, repo, results)
		}
	}

	return results, nil
}

// runDBOptimize runs retention + WAL checkpoint + VACUUM for a single repository.
// Pulled out of RunCleanup to keep that function a phase-by-phase runbook.
func (s *Service) runDBOptimize(ctx context.Context, i int, repo assets.MaintenanceRepository, results map[string]any) {
	s.log.Info("Optimizing database", zap.Int("db_index", i))

	// Run logs retention deletion (only succeeds on the DB containing api_requests).
	rowsAffected, err := repo.DeleteOldAPIRequests(ctx, s.cfg.Jobs.RetentionDays)
	if err == nil {
		s.log.Info("API request logs retention cleanup completed", zap.Int("db_index", i), zap.Int64("rows_deleted", rowsAffected))
		if i == 0 {
			results["api_requests_deleted"] = rowsAffected
		} else {
			results[fmt.Sprintf("api_requests_deleted_db_%d", i)] = rowsAffected
		}
	}

	// Execute WAL checkpoint with PASSIVE option (per Fase 1 recommendation):
	// doesn't require an exclusive lock and produces a gradual WAL shrink
	// without an I/O spike — safer for production load. Operators wanting
	// a forced truncate can override via WAL_CHECKPOINT_MODE env var
	// (allowed values: PASSIVE, FULL, RESTART, TRUNCATE).
	checkpointMode := validateCheckpointMode(os.Getenv("WAL_CHECKPOINT_MODE"))
	s.log.Info("Executing WAL checkpoint", zap.Int("db_index", i), zap.String("mode", checkpointMode))
	if err := repo.WALCheckpoint(ctx, checkpointMode); err != nil {
		s.log.Warn("WAL checkpoint failed", zap.Int("db_index", i), zap.Error(err))
	} else {
		s.log.Info("WAL checkpoint completed", zap.Int("db_index", i))
	}

	// Reclaim unused space (VACUUM)
	s.log.Info("Executing database VACUUM", zap.Int("db_index", i))
	if err := repo.IncrementalVacuum(ctx, 500); err != nil {
		s.log.Debug("Incremental vacuum skipped/failed, falling back to full VACUUM", zap.Int("db_index", i), zap.Error(err))
		if err := repo.FullVacuum(ctx); err != nil {
			s.log.Warn("Full VACUUM failed", zap.Int("db_index", i), zap.Error(err))
		} else {
			s.log.Info("Full VACUUM completed", zap.Int("db_index", i))
		}
	} else {
		s.log.Info("Incremental vacuum completed", zap.Int("db_index", i))
	}
}
