package cron

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/repository/scripts"
	"velox/go-master/internal/storage"
)

// DBMaintenanceJob handles periodic database maintenance tasks
type DBMaintenanceJob struct {
	scriptsRepo *scripts.ScriptRepository
	stockDB     *storage.SQLiteDB
	log         *zap.Logger
}

// NewDBMaintenanceJob creates a new database maintenance job
func NewDBMaintenanceJob(
	scriptsRepo *scripts.ScriptRepository,
	stockDB *storage.SQLiteDB,
	log *zap.Logger,
) *DBMaintenanceJob {
	return &DBMaintenanceJob{
		scriptsRepo: scriptsRepo,
		stockDB:     stockDB,
		log:         log,
	}
}

// Run executes the maintenance tasks
func (j *DBMaintenanceJob) Run(ctx context.Context) error {
	j.log.Info("Starting database maintenance job")

	// Task 1: Clean up old soft-deleted scripts (older than 30 days)
	if err := j.cleanupDeletedScripts(ctx); err != nil {
		j.log.Error("Failed to cleanup deleted scripts", zap.Error(err))
	}

	// Task 2: Vacuum databases to reclaim space
	if err := j.vacuumDatabases(ctx); err != nil {
		j.log.Error("Failed to vacuum databases", zap.Error(err))
	}

	// Task 3: Update database statistics
	if err := j.updateStats(ctx); err != nil {
		j.log.Error("Failed to update database stats", zap.Error(err))
	}

	j.log.Info("Database maintenance job completed")
	return nil
}

// cleanupDeletedScripts removes soft-deleted scripts older than 30 days
func (j *DBMaintenanceJob) cleanupDeletedScripts(ctx context.Context) error {
	if j.scriptsRepo == nil {
		j.log.Info("Scripts repository not available, skipping cleanup")
		return nil
	}

	// Hard delete scripts marked as deleted more than 30 days ago
	result, err := j.scriptsRepo.HardDeleteOldDeletedScripts(ctx, 30)
	if err != nil {
		return fmt.Errorf("failed to cleanup deleted scripts: %w", err)
	}

	j.log.Info("Cleaned up old deleted scripts", zap.Int64("deleted_count", result))
	return nil
}

// vacuumDatabases runs VACUUM on SQLite databases to reclaim space
func (j *DBMaintenanceJob) vacuumDatabases(ctx context.Context) error {
	j.log.Info("Vacuuming databases")

	if j.scriptsRepo != nil {
		if err := j.scriptsRepo.VacuumDatabase(ctx); err != nil {
			j.log.Error("Failed to vacuum scripts database", zap.Error(err))
		} else {
			j.log.Info("Vacuumed scripts database")
		}
	}

	if j.stockDB != nil && j.stockDB.DB != nil {
		if _, err := j.stockDB.DB.ExecContext(ctx, "VACUUM"); err != nil {
			j.log.Error("Failed to vacuum stock database", zap.Error(err))
		} else {
			j.log.Info("Vacuumed stock database")
		}
	}

	return nil
}

// updateStats updates query planner statistics
func (j *DBMaintenanceJob) updateStats(ctx context.Context) error {
	j.log.Info("Updating database statistics")

	if j.scriptsRepo != nil {
		if err := j.scriptsRepo.AnalyzeDatabase(ctx); err != nil {
			j.log.Error("Failed to analyze scripts database", zap.Error(err))
		} else {
			j.log.Info("Updated scripts database statistics")
		}
	}

	if j.stockDB != nil && j.stockDB.DB != nil {
		if _, err := j.stockDB.DB.ExecContext(ctx, "ANALYZE"); err != nil {
			j.log.Error("Failed to analyze stock database", zap.Error(err))
		} else {
			j.log.Info("Updated stock database statistics")
		}
	}

	return nil
}

// StartCron starts the periodic maintenance job
func (j *DBMaintenanceJob) StartCron(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		for {
			select {
			case <-ticker.C:
				if err := j.Run(ctx); err != nil {
					j.log.Error("Maintenance job failed", zap.Error(err))
				}
			case <-ctx.Done():
				ticker.Stop()
				return
			}
		}
	}()
}