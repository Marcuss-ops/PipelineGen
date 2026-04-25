package cron

import (
	"context"
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
	// This would require adding a hard delete method to the repository
	// For now, we just log the intent
	j.log.Info("Cleanup deleted scripts task - placeholder for hard delete implementation")
	return nil
}

// vacuumDatabases runs VACUUM on SQLite databases to reclaim space
func (j *DBMaintenanceJob) vacuumDatabases(ctx context.Context) error {
	j.log.Info("Vacuuming databases")

	if j.scriptsRepo != nil {
		// Note: To implement VACUUM, we would need access to the underlying *sql.DB
		// This is a placeholder for the actual implementation
		j.log.Info("Vacuum scripts database - placeholder")
	}

	if j.stockDB != nil && j.stockDB.DB != nil {
		j.log.Info("Vacuum stock database - placeholder")
	}

	return nil
}

// updateStats updates query planner statistics
func (j *DBMaintenanceJob) updateStats(ctx context.Context) error {
	j.log.Info("Updating database statistics")

	// ANALYZE command could be run here to update statistics
	// This is a placeholder for the actual implementation

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